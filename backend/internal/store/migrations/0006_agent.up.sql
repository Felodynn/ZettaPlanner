-- Planner agent (BACKEND.md Phase 6).
--
-- `threads`, `messages`, `agent_turns`, `proposals` and `proposal_changes` all
-- exist from 0001. What is missing is everything a turn needs in order to be
-- *readable afterwards* — which is the whole point of Phase 6's instrumentation
-- requirement: "every tool call, every model call, every retry is persisted."

-- ── turn lifecycle ───────────────────────────────────────────────────────────

-- `cancelled` is a real outcome, not a flavour of failed. A user who stops a
-- turn did not experience a bug, and a retrospective counting failures should
-- not count their interruptions among them. The audit taxonomy has had
-- turn.cancelled since 0001; the status column had not caught up.
ALTER TABLE agent_turns DROP CONSTRAINT agent_turns_status_check;
ALTER TABLE agent_turns
  ADD CONSTRAINT agent_turns_status_check
  CHECK (status IN ('queued', 'running', 'complete', 'failed', 'abandoned', 'cancelled'));

ALTER TABLE agent_turns
  -- finish_reason says *why* a turn ended, which `status` cannot: a turn that
  -- ran cleanly to a `finish` call and one that hit MaxTurns are both over, and
  -- only one of them answered the question.
  --
  -- `no_output` is the one worth naming. A turn that declines in prose and
  -- writes nothing looks identical to a completed one from the outside — the
  -- gap LumixAgents Phase 6 left open. Here it is a distinct, non-success
  -- outcome that the API reports and the trace shows.
  ADD COLUMN finish_reason TEXT CHECK (finish_reason IS NULL OR finish_reason IN (
              'finished', 'no_output', 'max_turns', 'max_tokens', 'max_wall_clock',
              'error', 'cancelled', 'abandoned')),
  ADD COLUMN error_detail  TEXT,
  -- Retries are counted because the retry policy is a claim that has to be
  -- checkable against real runs: exactly the transient classes, exactly once.
  ADD COLUMN retries       INT NOT NULL DEFAULT 0,
  ADD COLUMN queued_at     TIMESTAMPTZ,
  ADD COLUMN wall_ms       INT,
  -- The user message that triggered this turn, so a transcript reader can pair
  -- prompt with run without inferring it from timestamps.
  ADD COLUMN message_id    UUID REFERENCES messages(id);

COMMENT ON COLUMN agent_turns.finish_reason IS
  'Why the turn ended. `no_output` means it produced no write, no proposal and '
  'no handoff — a non-success that would otherwise be indistinguishable from a '
  'completed run.';

-- The queue, in the order it will be served. §5.3: a second user''s prompt
-- while a turn is running is queued *and shown as queued*, which means the
-- position has to be answerable cheaply on every poll.
CREATE INDEX agent_turns_queue ON agent_turns (project_id, queued_at)
  WHERE status = 'queued';

-- A running turn whose process died is not a turn in flight. The boot sweep
-- and the lease reaper both need to find them in one index scan.
CREATE INDEX agent_turns_live ON agent_turns (heartbeat_at)
  WHERE status = 'running';

-- ── the trace ────────────────────────────────────────────────────────────────

-- `zettaplanner trace <turn_id>` replays a turn (Phase 6 exit criterion).
--
-- Messages carry the conversation; they do not carry what happened *between*
-- messages. A model call that returned unparseable content and was retried
-- leaves no message at all, and that is precisely the event worth reading when
-- a turn behaved oddly. So every model call, every tool call, every denial and
-- every retry is appended here.
--
-- Append-only, like audit_log and for the same reason: a trace that can be
-- edited after the fact is not evidence. Unlike audit_log this is diagnostic
-- rather than permanent — it is scoped to a turn and may be pruned by age in a
-- later version, which audit_log may never be.
CREATE TABLE turn_events (
  id          UUID PRIMARY KEY,
  turn_id     UUID NOT NULL REFERENCES agent_turns(id),
  seq         INT NOT NULL,
  kind        TEXT NOT NULL CHECK (kind IN (
                'model_call', 'model_error', 'model_retry',
                'tool_call', 'tool_result', 'tool_denied', 'tool_unknown',
                'limit', 'note')),
  -- The tool or model this event is about; free text because it names an
  -- external thing whose vocabulary we do not own.
  subject     TEXT NOT NULL DEFAULT '',
  detail      JSONB NOT NULL DEFAULT '{}',
  error_class TEXT,
  duration_ms INT,
  tokens_in   INT,
  tokens_out  INT,
  at          TIMESTAMPTZ NOT NULL,
  UNIQUE (turn_id, seq)
);
CREATE INDEX turn_events_turn ON turn_events (turn_id, seq);

-- Same rule as audit_log and messages: no updates, no deletes. A trace that can
-- be rewritten is a trace nobody can rely on when the question is "what
-- actually happened".
CREATE OR REPLACE FUNCTION zp_turn_events_append_only() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'turn_events is append-only: % is not permitted', TG_OP;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER turn_events_no_update BEFORE UPDATE OR DELETE ON turn_events
  FOR EACH STATEMENT EXECUTE FUNCTION zp_turn_events_append_only();

-- ── proposals ────────────────────────────────────────────────────────────────

-- Phase 6 creates proposals; Phase 7 decides and applies them. A proposal
-- raised by an agent records which user asked for it, because an agent has the
-- permissions of the person who instructed it and no more (D11) — and the
-- approval check in Phase 7 has to know whose dev-owned fields the changes are
-- confined to.
ALTER TABLE proposals ADD COLUMN on_behalf_of UUID REFERENCES users(id);

COMMENT ON COLUMN proposals.on_behalf_of IS
  'The instructing user when an agent raised this. Set together with an agent '
  'actor_id; the pair is what lets a reader see both who asked and what ran.';

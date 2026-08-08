ALTER TABLE proposals DROP COLUMN IF EXISTS on_behalf_of;

DROP TRIGGER IF EXISTS turn_events_no_update ON turn_events;
DROP FUNCTION IF EXISTS zp_turn_events_append_only();
DROP INDEX IF EXISTS turn_events_turn;
DROP TABLE IF EXISTS turn_events;

DROP INDEX IF EXISTS agent_turns_live;
DROP INDEX IF EXISTS agent_turns_queue;

ALTER TABLE agent_turns
  DROP COLUMN IF EXISTS finish_reason,
  DROP COLUMN IF EXISTS error_detail,
  DROP COLUMN IF EXISTS retries,
  DROP COLUMN IF EXISTS queued_at,
  DROP COLUMN IF EXISTS wall_ms,
  DROP COLUMN IF EXISTS message_id;

ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_status_check;
ALTER TABLE agent_turns
  ADD CONSTRAINT agent_turns_status_check
  CHECK (status IN ('queued', 'running', 'complete', 'failed', 'abandoned'));

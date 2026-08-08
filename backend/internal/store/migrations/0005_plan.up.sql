-- Plan model (BACKEND.md Phase 4).
--
-- The tables are already in 0001. What is missing is everything that makes the
-- plan's own vocabularies closed, and a way to issue a human-facing ref that is
-- never reissued.

-- ── work item refs ───────────────────────────────────────────────────────────
--
-- ZP-14 must never be reused after ZP-14 is dropped: the audit log, every
-- worklog and every conversation referring to it would otherwise be ambiguous,
-- and no amount of care later can disambiguate them. 0001 already refuses to
-- make the ref unique index partial for the same reason.
--
-- A counter rather than max(ref)+1: parsing an integer back out of text is both
-- fragile and racy, and a counter that only ever goes up cannot reissue even if
-- every work item in the project is deleted. The UPDATE ... RETURNING that
-- reads it takes a row lock, which serializes two concurrent creates in the
-- same project without an advisory lock or a retry loop.
ALTER TABLE projects ADD COLUMN ref_seq INT NOT NULL DEFAULT 0;

COMMENT ON COLUMN projects.ref_seq IS
  'Monotonic counter behind work_items.ref. Incremented by UPDATE ... RETURNING '
  'inside the creating transaction; never decremented, never reset — a reissued '
  'ref makes every historical reference to it ambiguous.';

-- Backfill for any project that already has work items. None exist at the time
-- this migration is written, but a counter that starts below the highest
-- existing ref would hand out a duplicate on the very first create.
UPDATE projects p
   SET ref_seq = COALESCE((
         SELECT max((regexp_match(w.ref, '^' || p.key || '-([0-9]+)$'))[1]::int)
           FROM work_items w
          WHERE w.project_id = p.id), 0);

-- ── closed vocabularies ──────────────────────────────────────────────────────
--
-- 0001 left these three columns as bare TEXT because the contract's schema
-- block does. Every other status column in this database carries its set, and
-- the reason is the same here: a typo'd status is not a validation failure, it
-- is a work item that no query ever finds again.

-- Milestones are coarser than work items on purpose. A milestone is not
-- `blocked` or `in_review` — those are properties of the items under it, and
-- offering them invites a milestone status that contradicts its own contents.
ALTER TABLE milestones
  ADD CONSTRAINT milestones_status_check
  CHECK (status IN ('planned', 'active', 'done', 'dropped'));

-- Subtasks share the work item vocabulary rather than having a smaller one of
-- their own. One set of statuses across the plan is one thing to teach the
-- planner agent, and it makes "how long did this spend in each status" the same
-- query at both levels (§6.5 constraint 2).
ALTER TABLE sub_tasks
  ADD CONSTRAINT sub_tasks_status_check
  CHECK (status IN ('todo', 'in_progress', 'blocked', 'review', 'done', 'dropped'));

-- NULL means nobody has decided, which is different from `normal` — an
-- explicitly ordinary item and an untriaged one are not the same fact, and
-- defaulting the column would erase the distinction on every create.
ALTER TABLE work_items
  ADD CONSTRAINT work_items_priority_check
  CHECK (priority IS NULL OR priority IN ('low', 'normal', 'high', 'urgent'));

-- ── indexes the plan's own queries need ──────────────────────────────────────

-- The cycle walk seeds from one item's outgoing `blocks` edges and follows them
-- forward; the primary key (from_item_id, to_item_id, kind) already serves
-- that direction. What it does not serve is "what is blocking me", which the
-- warning query and the item view both ask — and 0001's item_dependencies_to
-- covers that. Nothing to add here; the note is so the next person does not
-- add a redundant index looking for one.

-- Worklogs filtered by kind within a project: the retrospective reads
-- `deviation` and `progress` separately, and the existing index is on time
-- alone.
CREATE INDEX worklogs_project_kind ON worklogs (project_id, kind, created_at DESC);

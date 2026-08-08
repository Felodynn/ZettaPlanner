DROP INDEX IF EXISTS worklogs_project_kind;

ALTER TABLE work_items DROP CONSTRAINT IF EXISTS work_items_priority_check;
ALTER TABLE sub_tasks DROP CONSTRAINT IF EXISTS sub_tasks_status_check;
ALTER TABLE milestones DROP CONSTRAINT IF EXISTS milestones_status_check;

ALTER TABLE projects DROP COLUMN IF EXISTS ref_seq;

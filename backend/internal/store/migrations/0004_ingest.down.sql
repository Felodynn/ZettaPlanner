DROP INDEX IF EXISTS ingests_unfinished;
DROP INDEX IF EXISTS ingest_pages_ingest;

ALTER TABLE ingest_pages DROP CONSTRAINT IF EXISTS ingest_pages_status_check;
ALTER TABLE ingest_pages
  DROP COLUMN IF EXISTS attempts,
  DROP COLUMN IF EXISTS error_detail,
  DROP COLUMN IF EXISTS extracted_at,
  DROP COLUMN IF EXISTS duration_ms,
  DROP COLUMN IF EXISTS raw_output;

ALTER TABLE ingests
  DROP COLUMN IF EXISTS source_filename,
  DROP COLUMN IF EXISTS source_path,
  DROP COLUMN IF EXISTS page_count,
  DROP COLUMN IF EXISTS merged,
  DROP COLUMN IF EXISTS merged_at,
  DROP COLUMN IF EXISTS accepted_at;

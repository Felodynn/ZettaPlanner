-- Ingest: draw-to-product (BACKEND.md Phase 3).
--
-- CONTRACT §6.1 has the two tables but not the columns the phase's own rules
-- require, so they are added here rather than invented at write time:
--
--   * merge is a **distinct step**, not an implementation detail of extraction,
--     so its output is stored where a human can review it before accepting —
--     ingests.merged. Recomputing it on read would make "what did I approve"
--     unanswerable the moment the merge rules change.
--   * **retry is per page**, so a page carries its own attempt count and its own
--     error detail. A failed page fails the page, not the ingest.
--   * the raw upload is kept forever alongside the rasterized pages. When
--     extraction is wrong you will want the input, and re-running is neither
--     free nor reproducible.

ALTER TABLE ingests
  ADD COLUMN source_filename TEXT,
  ADD COLUMN source_path     TEXT,
  ADD COLUMN page_count      INT NOT NULL DEFAULT 0,
  ADD COLUMN merged          JSONB,
  ADD COLUMN merged_at       TIMESTAMPTZ,
  ADD COLUMN accepted_at     TIMESTAMPTZ;

COMMENT ON COLUMN ingests.merged IS
  'The merge step''s reviewable output: cross-page reconciliation, the conflicts '
  'it refused to decide, and the edge endpoints it could not resolve. Written '
  'once when the ingest reaches review and again after a per-page retry.';

COMMENT ON COLUMN ingests.source_path IS
  'The uploaded bytes, unmodified. Kept forever: when extraction is wrong you '
  'want the input, and re-running costs tokens and is not reproducible.';

-- page_idx is 0-based and dense. A page that fails extraction keeps its slot,
-- because "page 3 of 5 is unreadable" is the useful thing to say and a
-- renumbered sequence cannot say it.
ALTER TABLE ingest_pages
  ADD COLUMN attempts     INT NOT NULL DEFAULT 0,
  ADD COLUMN error_detail TEXT,
  ADD COLUMN extracted_at TIMESTAMPTZ,
  ADD COLUMN duration_ms  INT,
  ADD COLUMN raw_output   TEXT;

-- raw_output is the model's answer byte for byte, before parsing.
--
-- `extracted` is JSONB and therefore already a rewriting: keys are reordered,
-- a field we do not know about is dropped, and an array we failed to decode
-- becomes null. Every one of those makes "what did the model actually say"
-- unanswerable, which is the exact question you have when an extraction is
-- wrong. Written on success and on failure — a malformed answer is the one you
-- most want to read.
COMMENT ON COLUMN ingest_pages.raw_output IS
  'The model''s response content verbatim, before parsing. Kept forever.';

-- The page states, closed like every other vocabulary in this schema. The
-- ingest-level CHECK already exists (0001); this is its per-page counterpart,
-- and without it a typo'd status is a page that never leaves pending.
ALTER TABLE ingest_pages
  ADD CONSTRAINT ingest_pages_status_check
  CHECK (status IN ('pending', 'extracting', 'extracted', 'failed'));

CREATE INDEX ingest_pages_ingest ON ingest_pages (ingest_id, page_idx);

-- Extraction runs in the background and the process can be restarted under it.
-- A page left in `extracting` by a killed process is not a page in flight, and
-- the recovery sweep at boot needs to find it in one index scan.
CREATE INDEX ingests_unfinished ON ingests (status)
  WHERE status IN ('uploaded', 'extracting', 'merging');

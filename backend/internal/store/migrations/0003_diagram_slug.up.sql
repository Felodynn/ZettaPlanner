-- A diagram needs a stable identity, for the same reason an entity does.
--
-- CONTRACT §6.1's diagrams table has no slug, but §6.2's serialized document
-- opens with `diagram: auth-context` and the git projection writes paths derived
-- from it. Deriving that from the display name at serialization time would make
-- a rename look like a delete plus an add — exactly what §6.2 says stable
-- identity in paths exists to prevent. So the slug is stored, assigned once, and
-- does not follow the name.

ALTER TABLE diagrams ADD COLUMN slug TEXT;

-- Backfill from the name for any diagram that predates this column. Matches
-- domain/diagram.Slugify: lower-case, non-alphanumerics collapsed to a single
-- dash, no leading or trailing dash.
UPDATE diagrams
   SET slug = trim(both '-' from regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g'))
 WHERE slug IS NULL;

-- A name that slugifies to nothing at all still needs an identity.
UPDATE diagrams SET slug = 'diagram-' || left(id::text, 8) WHERE coalesce(slug, '') = '';

ALTER TABLE diagrams ALTER COLUMN slug SET NOT NULL;

CREATE UNIQUE INDEX diagrams_slug ON diagrams (project_id, slug) WHERE deleted_at IS NULL;

COMMENT ON COLUMN diagrams.slug IS
  'Stable identity, assigned at creation and never following the name. The git '
  'projection derives paths from it (CONTRACT.md 6.2).';

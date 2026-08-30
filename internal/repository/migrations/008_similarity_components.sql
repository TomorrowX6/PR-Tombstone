-- Persist the per-signal similarity breakdown so the API and dashboard can
-- show users exactly why a historical match surfaced instead of a single
-- opaque score.
ALTER TABLE similarity_matches
    ADD COLUMN IF NOT EXISTS components JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Similarity matches are derived data. Rows created before this migration do
-- not have trustworthy component values, so keeping them would expose an old
-- aggregate score beside an empty breakdown. Drop only the derived matches;
-- they will be rebuilt the next time an open PR is analyzed or synchronized.
DELETE FROM similarity_matches;

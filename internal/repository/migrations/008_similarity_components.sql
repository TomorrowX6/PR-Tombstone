-- Persist the per-signal similarity breakdown so the API and dashboard can
-- show users exactly why a historical match surfaced instead of a single
-- opaque score.
ALTER TABLE similarity_matches
    ADD COLUMN IF NOT EXISTS components JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Widen embedding storage from 64 to 1536 dimensions.
--
-- Existing 64-dimensional vectors (local/fixture provider) are zero-padded,
-- which preserves cosine distance exactly: dot products and norms are
-- unchanged by trailing zeros, so historical similarity scores remain valid.
-- Nothing is truncated. New writes must already match the storage width:
-- OpenAI-compatible providers request dimensions=1536 explicitly, and
-- embedding.Fit errors on oversize vectors instead of truncating them.
--
-- The HNSW index depends on the column type and is rebuilt after the change.
DROP INDEX IF EXISTS tombstone_embeddings_approach_idx;

ALTER TABLE tombstone_embeddings
    ALTER COLUMN title_embedding TYPE vector(1536)
        USING CASE WHEN title_embedding IS NULL THEN NULL
                   ELSE ('[' || trim(both '[]' from title_embedding::text) || repeat(',0', 1536 - 64) || ']')::vector(1536) END,
    ALTER COLUMN description_embedding TYPE vector(1536)
        USING CASE WHEN description_embedding IS NULL THEN NULL
                   ELSE ('[' || trim(both '[]' from description_embedding::text) || repeat(',0', 1536 - 64) || ']')::vector(1536) END,
    ALTER COLUMN discussion_embedding TYPE vector(1536)
        USING CASE WHEN discussion_embedding IS NULL THEN NULL
                   ELSE ('[' || trim(both '[]' from discussion_embedding::text) || repeat(',0', 1536 - 64) || ']')::vector(1536) END,
    ALTER COLUMN approach_embedding TYPE vector(1536)
        USING CASE WHEN approach_embedding IS NULL THEN NULL
                   ELSE ('[' || trim(both '[]' from approach_embedding::text) || repeat(',0', 1536 - 64) || ']')::vector(1536) END;

CREATE INDEX IF NOT EXISTS tombstone_embeddings_approach_idx
    ON tombstone_embeddings USING hnsw (approach_embedding vector_cosine_ops);

WITH duplicate_jobs AS (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY repository_id, pr_number, kind ORDER BY id) AS position
    FROM analysis_jobs
    WHERE status IN ('pending', 'running')
)
UPDATE analysis_jobs
SET status = 'failed', last_error = 'deduplicated during migration', updated_at = NOW()
WHERE id IN (SELECT id FROM duplicate_jobs WHERE position > 1);

CREATE UNIQUE INDEX IF NOT EXISTS analysis_jobs_active_unique_idx
    ON analysis_jobs(repository_id, pr_number, kind)
    WHERE status IN ('pending', 'running');

UPDATE pull_requests AS pull_request
SET snapshot = jsonb_set(
    pull_request.snapshot - 'evidence',
    '{files}',
    COALESCE(
        (
            SELECT jsonb_agg(file_item - 'patch')
            FROM jsonb_array_elements(
                CASE WHEN jsonb_typeof(pull_request.snapshot->'files') = 'array'
                    THEN pull_request.snapshot->'files'
                    ELSE '[]'::jsonb
                END
            ) AS file_item
        ),
        '[]'::jsonb
    ),
    TRUE
)
WHERE pull_request.snapshot ? 'evidence'
   OR pull_request.snapshot::text LIKE '%"patch"%';

UPDATE pr_files SET patch = '' WHERE COALESCE(patch, '') <> '';
UPDATE evidence_items SET body = '' WHERE type = 'diff' AND body <> '';

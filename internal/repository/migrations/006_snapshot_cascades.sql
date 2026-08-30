DELETE FROM pr_files child
WHERE NOT EXISTS (SELECT 1 FROM pull_requests parent WHERE parent.repository_id=child.repository_id AND parent.number=child.pr_number);
DELETE FROM pr_commits child
WHERE NOT EXISTS (SELECT 1 FROM pull_requests parent WHERE parent.repository_id=child.repository_id AND parent.number=child.pr_number);
DELETE FROM evidence_items child
WHERE NOT EXISTS (SELECT 1 FROM pull_requests parent WHERE parent.repository_id=child.repository_id AND parent.number=child.pr_number);
DELETE FROM similarity_matches child
WHERE NOT EXISTS (SELECT 1 FROM pull_requests parent WHERE parent.repository_id=child.repository_id AND parent.number IN (child.new_pr_number, child.old_pr_number) GROUP BY parent.repository_id HAVING COUNT(DISTINCT parent.number)=CASE WHEN child.new_pr_number=child.old_pr_number THEN 1 ELSE 2 END);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pr_files_pull_request_fk') THEN
        ALTER TABLE pr_files
            ADD CONSTRAINT pr_files_pull_request_fk
            FOREIGN KEY (repository_id, pr_number)
            REFERENCES pull_requests(repository_id, number)
            ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pr_commits_pull_request_fk') THEN
        ALTER TABLE pr_commits
            ADD CONSTRAINT pr_commits_pull_request_fk
            FOREIGN KEY (repository_id, pr_number)
            REFERENCES pull_requests(repository_id, number)
            ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'evidence_items_pull_request_fk') THEN
        ALTER TABLE evidence_items
            ADD CONSTRAINT evidence_items_pull_request_fk
            FOREIGN KEY (repository_id, pr_number)
            REFERENCES pull_requests(repository_id, number)
            ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'similarity_matches_new_pr_fk') THEN
        ALTER TABLE similarity_matches
            ADD CONSTRAINT similarity_matches_new_pr_fk
            FOREIGN KEY (repository_id, new_pr_number)
            REFERENCES pull_requests(repository_id, number)
            ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'similarity_matches_old_pr_fk') THEN
        ALTER TABLE similarity_matches
            ADD CONSTRAINT similarity_matches_old_pr_fk
            FOREIGN KEY (repository_id, old_pr_number)
            REFERENCES pull_requests(repository_id, number)
            ON DELETE CASCADE;
    END IF;
END $$;

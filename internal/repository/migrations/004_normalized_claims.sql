CREATE TABLE IF NOT EXISTS tombstone_claims (
    id BIGSERIAL PRIMARY KEY,
    tombstone_id BIGINT NOT NULL REFERENCES tombstones(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    position INT NOT NULL,
    claim TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    UNIQUE (tombstone_id, category, position)
);

CREATE TABLE IF NOT EXISTS claim_evidence (
    claim_id BIGINT NOT NULL REFERENCES tombstone_claims(id) ON DELETE CASCADE,
    repository_id BIGINT NOT NULL,
    pr_number INT NOT NULL,
    evidence_id TEXT NOT NULL,
    PRIMARY KEY (claim_id, evidence_id),
    FOREIGN KEY (repository_id, pr_number, evidence_id)
        REFERENCES evidence_items(repository_id, pr_number, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS claim_evidence_item_idx
    ON claim_evidence(repository_id, pr_number, evidence_id);

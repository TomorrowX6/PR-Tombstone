CREATE TABLE IF NOT EXISTS maintenance_runs (
    name TEXT PRIMARY KEY,
    ran_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE maintenance_runs IS 'Cross-instance lease timestamps for periodic worker maintenance.';

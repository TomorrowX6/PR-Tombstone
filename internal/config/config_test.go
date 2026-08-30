package config

import "testing"

func TestVercelDefaults(t *testing.T) {
	t.Setenv("VERCEL", "1")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("VERCEL_PROJECT_PRODUCTION_URL", "pr-tombstone.example.test")
	t.Setenv("DB_MAX_OPEN_CONNS", "")
	t.Setenv("DB_MAX_IDLE_CONNS", "")

	cfg := Load()
	if cfg.PublicBaseURL != "https://pr-tombstone.example.test" {
		t.Fatalf("PublicBaseURL = %q", cfg.PublicBaseURL)
	}
	if cfg.DBMaxOpenConns != 4 || cfg.DBMaxIdleConns != 1 {
		t.Fatalf("pool = %d/%d", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	}
	if cfg.VercelWorkerBatch != 5 || cfg.VercelWorkerBudget.String() != "50s" {
		t.Fatalf("worker defaults = %d/%s", cfg.VercelWorkerBatch, cfg.VercelWorkerBudget)
	}
}

func TestExplicitDeploymentSettingsWin(t *testing.T) {
	t.Setenv("VERCEL", "1")
	t.Setenv("PUBLIC_BASE_URL", "https://custom.example.test")
	t.Setenv("VERCEL_PROJECT_PRODUCTION_URL", "ignored.example.test")
	t.Setenv("DB_MAX_OPEN_CONNS", "8")
	t.Setenv("DB_MAX_IDLE_CONNS", "3")
	t.Setenv("VERCEL_WORKER_BATCH", "2")
	t.Setenv("VERCEL_WORKER_BUDGET", "25s")

	cfg := Load()
	if cfg.PublicBaseURL != "https://custom.example.test" {
		t.Fatalf("PublicBaseURL = %q", cfg.PublicBaseURL)
	}
	if cfg.DBMaxOpenConns != 8 || cfg.DBMaxIdleConns != 3 {
		t.Fatalf("pool = %d/%d", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	}
	if cfg.VercelWorkerBatch != 2 || cfg.VercelWorkerBudget.String() != "25s" {
		t.Fatalf("worker settings = %d/%s", cfg.VercelWorkerBatch, cfg.VercelWorkerBudget)
	}
}

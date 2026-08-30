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

	cfg := Load()
	if cfg.PublicBaseURL != "https://custom.example.test" {
		t.Fatalf("PublicBaseURL = %q", cfg.PublicBaseURL)
	}
	if cfg.DBMaxOpenConns != 8 || cfg.DBMaxIdleConns != 3 {
		t.Fatalf("pool = %d/%d", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	}
	if cfg.VercelWorkerBatch != 5 || cfg.VercelWorkerBudget.String() != "50s" {
		t.Fatalf("worker defaults = %d/%s", cfg.VercelWorkerBatch, cfg.VercelWorkerBudget)
	}
}

func TestOAuthEnabledRequiresBothCredentials(t *testing.T) {
	if (Config{}).OAuthEnabled() {
		t.Fatal("empty config must not enable OAuth")
	}
	if (Config{OAuthClientID: "id"}).OAuthEnabled() {
		t.Fatal("client id alone must not enable OAuth")
	}
	if (Config{OAuthClientSecret: "secret"}).OAuthEnabled() {
		t.Fatal("client secret alone must not enable OAuth")
	}
	if !(Config{OAuthClientID: "id", OAuthClientSecret: "secret"}).OAuthEnabled() {
		t.Fatal("both credentials must enable OAuth")
	}
}

func TestOAuthWebBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://api.github.com":          "https://github.com",
		"https://api.github.com/":         "https://github.com",
		"https://ghe.example.com/api/v3":  "https://ghe.example.com",
		"https://ghe.example.com/api/v3/": "https://ghe.example.com",
		"":                                "https://github.com",
	}
	for apiBase, want := range cases {
		if got := (Config{GitHubAPIBaseURL: apiBase}).OAuthWebBaseURL(); got != want {
			t.Fatalf("OAuthWebBaseURL(%q) = %q, want %q", apiBase, got, want)
		}
	}
}

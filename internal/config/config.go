package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains runtime configuration. Secrets are read from the environment
// or an external secret manager and are never included in API responses.
type Config struct {
	HTTPAddr           string
	DatabaseURL        string
	DBMaxOpenConns     int
	DBMaxIdleConns     int
	GitHubAppID        int64
	GitHubPrivateKey   string
	WebhookSecret      string
	GitHubAPIBaseURL   string
	GitHubAppSlug      string
	PublicBaseURL      string
	DashboardToken     string
	OAuthClientID      string
	OAuthClientSecret  string
	SessionTTL         time.Duration
	OAuthACLTTL        time.Duration
	AIProvider         string
	AIBaseURL          string
	AIAPIKey           string
	AIModel            string
	EmbeddingProvider  string
	EmbeddingBaseURL   string
	EmbeddingAPIKey    string
	EmbeddingModel     string
	JobPollInterval    time.Duration
	RetentionDays      int
	CronSecret         string
	VercelWorkerBatch  int
	VercelWorkerBudget time.Duration
}

func Load() Config {
	maxOpenConns, maxIdleConns := 20, 5
	if os.Getenv("VERCEL") != "" {
		// A small per-instance pool prevents many concurrently scaled function
		// instances from exhausting a managed PostgreSQL connection limit.
		maxOpenConns, maxIdleConns = 4, 1
	}
	return Config{
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		DatabaseURL:        env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/pr_tombstone?sslmode=disable"),
		DBMaxOpenConns:     positiveIntEnv("DB_MAX_OPEN_CONNS", maxOpenConns),
		DBMaxIdleConns:     nonNegativeIntEnv("DB_MAX_IDLE_CONNS", maxIdleConns),
		GitHubAppID:        int64Env("GITHUB_APP_ID", 0),
		GitHubPrivateKey:   os.Getenv("GITHUB_PRIVATE_KEY"),
		WebhookSecret:      os.Getenv("GITHUB_WEBHOOK_SECRET"),
		GitHubAPIBaseURL:   env("GITHUB_API_BASE_URL", "https://api.github.com"),
		GitHubAppSlug:      os.Getenv("GITHUB_APP_SLUG"),
		PublicBaseURL:      publicBaseURL(),
		DashboardToken:     os.Getenv("DASHBOARD_TOKEN"),
		OAuthClientID:      os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		OAuthClientSecret:  os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
		SessionTTL:         durationEnv("SESSION_TTL", 14*24*time.Hour),
		OAuthACLTTL:        durationEnv("OAUTH_ACL_TTL", time.Hour),
		AIProvider:         env("AI_PROVIDER", "rules"),
		AIBaseURL:          env("AI_BASE_URL", ""),
		AIAPIKey:           os.Getenv("AI_API_KEY"),
		AIModel:            env("AI_MODEL", ""),
		EmbeddingProvider:  env("EMBEDDING_PROVIDER", "local"),
		EmbeddingBaseURL:   os.Getenv("EMBEDDING_BASE_URL"),
		EmbeddingAPIKey:    os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingModel:     os.Getenv("EMBEDDING_MODEL"),
		JobPollInterval:    durationEnv("JOB_POLL_INTERVAL", 2*time.Second),
		RetentionDays:      intEnv("RETENTION_DAYS", 30),
		CronSecret:         os.Getenv("CRON_SECRET"),
		VercelWorkerBatch:  5,
		VercelWorkerBudget: 50 * time.Second,
	}
}

func publicBaseURL() string {
	if value := os.Getenv("PUBLIC_BASE_URL"); value != "" {
		return value
	}
	for _, key := range []string{"VERCEL_PROJECT_PRODUCTION_URL", "VERCEL_URL"} {
		if host := os.Getenv(key); host != "" {
			if len(host) >= 7 && (host[:7] == "http://" || (len(host) >= 8 && host[:8] == "https://")) {
				return host
			}
			return "https://" + host
		}
	}
	return "http://localhost:5173"
}

// OAuthEnabled reports whether GitHub OAuth login is configured. Without it
// the dashboard falls back to the self-host modes (DASHBOARD_TOKEN bearer or
// fully open when both are empty).
func (c Config) OAuthEnabled() bool {
	return c.OAuthClientID != "" && c.OAuthClientSecret != ""
}

// OAuthWebBaseURL derives the OAuth authorize/token web endpoint from the
// REST API base URL so GitHub Enterprise hosts keep working: api.github.com
// maps to github.com and <host>/api/v3 maps to <host>.
func (c Config) OAuthWebBaseURL() string {
	base := strings.TrimSuffix(c.GitHubAPIBaseURL, "/")
	if base == "" || base == "https://api.github.com" {
		return "https://github.com"
	}
	base = strings.TrimSuffix(base, "/api/v3")
	return base
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}

func positiveIntEnv(key string, fallback int) int {
	value := intEnv(key, fallback)
	if value < 1 {
		return fallback
	}
	return value
}

func nonNegativeIntEnv(key string, fallback int) int {
	value := intEnv(key, fallback)
	if value < 0 {
		return fallback
	}
	return value
}

func int64Env(key string, fallback int64) int64 {
	value, err := strconv.ParseInt(os.Getenv(key), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}

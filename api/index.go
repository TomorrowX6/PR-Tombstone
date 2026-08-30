// Package handler exposes the PR Tombstone API as a single Vercel Go
// Function. The regular cmd/server and cmd/worker entry points remain the
// local/container deployment path.
package handler

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"pr-tombstone/internal/analyzer"
	"pr-tombstone/internal/config"
	"pr-tombstone/internal/embedding"
	"pr-tombstone/internal/github"
	"pr-tombstone/internal/httpapi"
	"pr-tombstone/internal/ingest"
	"pr-tombstone/internal/jobs"
	"pr-tombstone/internal/repository"
)

const cronPath = "/api/cron/worker"

type application struct {
	api    http.Handler
	worker *jobs.Worker
	config config.Config
	logger *slog.Logger
}

var runtimeState struct {
	sync.Mutex
	app *application
}

// Handler is the Vercel Go Runtime entrypoint.
func Handler(w http.ResponseWriter, r *http.Request) {
	r = restoreRewrittenPath(r)
	app, err := loadApplication(r.Context())
	if err != nil {
		slog.Error("initialize Vercel application", "error", err)
		http.Error(w, "service initialization failed", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == cronPath {
		app.handleCron(w, r)
		return
	}
	app.api.ServeHTTP(w, r)
}

func loadApplication(ctx context.Context) (*application, error) {
	runtimeState.Lock()
	defer runtimeState.Unlock()
	if runtimeState.app != nil {
		return runtimeState.app, nil
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	store, err := repository.OpenWithPool(ctx, cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}

	var auth *github.AppAuthenticator
	if cfg.GitHubAppID != 0 || cfg.GitHubPrivateKey != "" {
		auth, err = github.NewAppAuthenticator(cfg.GitHubAppID, cfg.GitHubPrivateKey, cfg.GitHubAPIBaseURL, nil)
		if err != nil {
			_ = store.Close()
			return nil, err
		}
	}
	embedder := embedding.New(cfg.EmbeddingProvider, cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel)
	service := &ingest.Service{
		Store: store, Config: cfg,
		Analyze: analyzer.New(cfg.AIProvider, cfg.AIBaseURL, cfg.AIAPIKey, cfg.AIModel),
		Embed:   embedder,
		Auth:    auth,
	}
	app := &application{
		api: (&httpapi.Server{
			Store: store, Config: cfg, Logger: logger, Embed: embedder, Auth: auth,
		}).Handler(),
		worker: &jobs.Worker{
			Store: store, Service: service, RetentionDays: cfg.RetentionDays, Logger: logger,
		},
		config: cfg,
		logger: logger,
	}
	runtimeState.app = app
	return app, nil
}

func (a *application) handleCron(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if a.config.CronSecret == "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "CRON_SECRET is not configured"})
		return
	}
	if !validBearer(r.Header.Get("Authorization"), a.config.CronSecret) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="PR Tombstone Worker"`)
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "unauthorized"})
		return
	}
	result, err := a.worker.RunBatch(r.Context(), a.config.VercelWorkerBatch, a.config.VercelWorkerBudget)
	if err != nil && !errors.Is(err, context.Canceled) {
		a.logger.Error("Vercel worker batch failed", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "result": result})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": err == nil, "result": result})
}

func validBearer(header, secret string) bool {
	if secret == "" {
		return false
	}
	provided := sha256.Sum256([]byte(header))
	expected := sha256.Sum256([]byte("Bearer " + secret))
	return subtle.ConstantTimeCompare(provided[:], expected[:]) == 1
}

func restoreRewrittenPath(r *http.Request) *http.Request {
	path := r.URL.Query().Get("__vercel_path")
	if path == "" {
		return r
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	query := urlCopy.Query()
	query.Del("__vercel_path")
	urlCopy.Path = path
	urlCopy.RawPath = ""
	urlCopy.RawQuery = query.Encode()
	clone.URL = &urlCopy
	return clone
}

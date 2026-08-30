package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"pr-tombstone/internal/analyzer"
	"pr-tombstone/internal/config"
	"pr-tombstone/internal/embedding"
	"pr-tombstone/internal/github"
	"pr-tombstone/internal/ingest"
	"pr-tombstone/internal/jobs"
	"pr-tombstone/internal/repository"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	store, err := repository.OpenWithPool(ctx, cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		logger.Error("migrate database", "error", err)
		os.Exit(1)
	}
	var auth *github.AppAuthenticator
	if cfg.GitHubAppID != 0 || cfg.GitHubPrivateKey != "" {
		auth, err = github.NewAppAuthenticator(cfg.GitHubAppID, cfg.GitHubPrivateKey, cfg.GitHubAPIBaseURL, nil)
		if err != nil {
			logger.Error("configure GitHub App", "error", err)
			os.Exit(1)
		}
	}
	service := &ingest.Service{Store: store, Config: cfg, Analyze: analyzer.New(cfg.AIProvider, cfg.AIBaseURL, cfg.AIAPIKey, cfg.AIModel), Embed: embedding.New(cfg.EmbeddingProvider, cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel), Auth: auth}
	worker := &jobs.Worker{Store: store, Service: service, PollInterval: cfg.JobPollInterval, RetentionDays: cfg.RetentionDays, Logger: logger}
	if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

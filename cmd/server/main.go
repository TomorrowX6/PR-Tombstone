package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pr-tombstone/internal/config"
	"pr-tombstone/internal/embedding"
	"pr-tombstone/internal/github"
	"pr-tombstone/internal/httpapi"
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
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: (&httpapi.Server{Store: store, Config: cfg, Logger: logger, Embed: embedding.New(cfg.EmbeddingProvider, cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel), Auth: auth}).Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second}
	go func() {
		logger.Info("server listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			cancel()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = server.Shutdown(shutdownCtx)
}

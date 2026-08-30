package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"pr-tombstone/internal/analyzer"
	"pr-tombstone/internal/config"
	"pr-tombstone/internal/embedding"
	"pr-tombstone/internal/evidence"
	"pr-tombstone/internal/ingest"
	"pr-tombstone/internal/model"
	"pr-tombstone/internal/repository"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	store, err := repository.OpenWithPool(ctx, cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		slog.Error("migrate database", "error", err)
		os.Exit(1)
	}
	repoID, err := store.EnsureRepository(ctx, model.Repository{GitHubID: 987654, InstallationID: 123456, Owner: "fixture-owner", Name: "fixture-repository"})
	if err != nil {
		slog.Error("save repository", "error", err)
		os.Exit(1)
	}
	now := time.Now().UTC()
	snapshot := model.PullRequestSnapshot{RepositoryID: repoID, Number: 18331, Title: "Serialize Vulkan pipeline destruction", Body: "Attempt to protect pipeline destruction with a global mutex.", Author: "fixture-contributor", AuthorAssociation: "CONTRIBUTOR", CreatedAt: now.Add(-48 * time.Hour), ClosedAt: &now, BaseBranch: "main", HeadBranch: "pipeline-mutex", Labels: []string{"vulkan", "performance"}, Files: []model.ChangedFile{{Filename: "GPU/Vulkan/VulkanRenderManager.cpp", Status: "modified", Additions: 18, Deletions: 4, Changes: 22, Patch: "+ global mutex around pipeline destruction"}}, Evidence: []model.EvidenceItem{
		{ID: "pr_body:18331", RepositoryID: repoID, PRNumber: 18331, Type: "pr_body", Author: "fixture-contributor", Body: "Attempt to protect pipeline destruction with a global mutex.", SourceURL: "https://example.invalid/fixture-owner/fixture-repository/pull/18331", CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "review_comment:1838821", RepositoryID: repoID, PRNumber: 18331, Type: "review_comment", Author: "fixture-maintainer", AuthorAssociation: "MEMBER", Path: "GPU/Vulkan/VulkanRenderManager.cpp", Line: 481, Body: "We won't merge this because the global lock introduces render-thread stalls and hides the underlying lifetime problem.", SourceURL: "https://example.invalid/fixture-owner/fixture-repository/pull/18331#discussion_r1838821", CreatedAt: now.Add(-2 * time.Hour)},
	}}
	ranked := evidence.Rank(snapshot.Evidence)
	result, err := (analyzer.LocalAnalyzer{}).Analyze(ctx, model.AnalysisInput{Snapshot: snapshot, Evidence: ranked})
	if err != nil {
		slog.Error("analyze fixture", "error", err)
		os.Exit(1)
	}
	if err := store.SaveAnalysis(ctx, snapshot, *result, ranked, 0.92, "local-rules"); err != nil {
		slog.Error("save fixture", "error", err)
		os.Exit(1)
	}
	id, err := store.TombstoneIDForPR(ctx, repoID, snapshot.Number)
	if err != nil {
		slog.Error("find fixture tombstone", "error", err)
		os.Exit(1)
	}
	values, err := ingest.EmbedResult(ctx, embedding.LocalProvider{}, snapshot, ranked, result)
	if err != nil {
		slog.Error("embed fixture", "error", err)
		os.Exit(1)
	}
	if err := store.SaveEmbeddings(ctx, id, values); err != nil {
		slog.Error("save fixture embeddings", "error", err)
		os.Exit(1)
	}
	if err := store.UpsertDecisionRelations(ctx, repoID, snapshot, id, result, nil); err != nil {
		slog.Error("save fixture relations", "error", err)
		os.Exit(1)
	}
	slog.Info("fixture loaded", "repository_id", repoID, "pr", snapshot.Number)
}

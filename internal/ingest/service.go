package ingest

import (
	"context"
	"fmt"
	"strings"

	"pr-tombstone/internal/analyzer"
	"pr-tombstone/internal/confidence"
	"pr-tombstone/internal/config"
	"pr-tombstone/internal/embedding"
	"pr-tombstone/internal/evidence"
	"pr-tombstone/internal/github"
	"pr-tombstone/internal/model"
	"pr-tombstone/internal/repository"
	"pr-tombstone/internal/similarity"
)

type Service struct {
	Store   *repository.Store
	Config  config.Config
	Analyze analyzer.Analyzer
	Embed   embedding.Provider
	Auth    *github.AppAuthenticator
}

// Process fetches the complete read-only snapshot, ranks evidence, verifies
// claims and persists the result atomically.
func (s *Service) Process(ctx context.Context, job *repository.Job) error {
	repo, err := s.Store.RepositoryForJob(ctx, job)
	if err != nil {
		return err
	}
	if s.Auth == nil {
		return fmt.Errorf("github app credentials are not configured")
	}
	token, err := s.Auth.InstallationToken(ctx, job.InstallationID)
	if err != nil {
		return err
	}
	client := github.NewClient(s.Config.GitHubAPIBaseURL, token, nil)
	snapshot, err := client.FetchSnapshot(ctx, repo.Owner, repo.Name, job.PRNumber, repo.ID)
	if err != nil {
		return err
	}
	settings, settingsErr := s.Store.GetSettings(ctx, repo.ID)
	if settingsErr == nil && settings.ContentsEnabled {
		appendRepositoryContext(ctx, client, repo, &snapshot)
	}
	if job.Kind == "similarity" {
		if err := s.Store.SaveSnapshotOnly(ctx, snapshot); err != nil {
			return err
		}
		history, err := s.Store.ListTombstones(ctx, repo.ID)
		if err != nil {
			return err
		}
		var semanticScores map[int]float64
		if s.Embed != nil {
			queryVector, embedErr := s.Embed.Embed(ctx, snapshot.Title+"\n"+snapshot.Body)
			if embedErr == nil {
				semanticScores, _ = s.Store.SemanticScores(ctx, repo.ID, queryVector, len(history))
			}
		}
		matches := similarity.CompareWithSemantic(snapshot, history, semanticScores)
		if err := s.Store.SaveSimilarityMatches(ctx, repo.ID, snapshot.Number, matches); err != nil {
			return err
		}
		if err := s.Store.UpsertSimilarityRelations(ctx, repo.ID, snapshot, matches); err != nil {
			return err
		}
		if settingsErr == nil && settings.NotifyMode == "check" && len(snapshot.Commits) > 0 {
			// Check creation is best effort. The app can run v0.1 without Checks
			// write permission; enabling this setting opts the repository in.
			conclusion, title, summary := "success", "No strong historical conflicts", "No preserved attempt crossed the related-history threshold."
			if len(matches) > 0 {
				conclusion, title, summary = "neutral", "Historical context found", fmt.Sprintf("PR #%d: %s", matches[0].OldPRNumber, matches[0].Reason)
			}
			_ = client.CreateCheckRun(ctx, repo.Owner, repo.Name, "PR Tombstone / Historical Context", snapshot.Commits[len(snapshot.Commits)-1].SHA, conclusion, title, summary)
		}
		return nil
	}
	ranked := evidence.Rank(snapshot.Evidence)
	result, err := s.Analyze.Analyze(ctx, model.AnalysisInput{Snapshot: snapshot, Evidence: ranked})
	if err != nil {
		return err
	}
	result = VerifyResult(result, ranked)
	result.Outcomes = VerifiedOutcomes(result.Outcomes)
	result.AffectedAreas = verifiedAreas(result.AffectedAreas, snapshot.Files)
	confidenceValue := aggregateConfidence(result)
	if err := s.Store.SaveAnalysis(ctx, snapshot, *result, ranked, confidenceValue, s.Config.AIModel); err != nil {
		return err
	}
	if s.Embed == nil {
		return nil
	}
	tombstoneID, err := s.Store.TombstoneIDForPR(ctx, repo.ID, snapshot.Number)
	if err != nil {
		return err
	}
	values, err := EmbedResult(ctx, s.Embed, snapshot, ranked, result)
	if err != nil {
		return err
	}
	if err := s.Store.SaveEmbeddings(ctx, tombstoneID, values); err != nil {
		return err
	}
	return s.Store.UpsertDecisionRelations(ctx, repo.ID, snapshot, tombstoneID, result, nil)
}

func appendRepositoryContext(ctx context.Context, client *github.Client, repo model.Repository, snapshot *model.PullRequestSnapshot) {
	paths := []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS", "CONTRIBUTING.md", ".github/CONTRIBUTING.md"}
	for _, path := range paths {
		body, sourceURL, err := client.GetRepositoryContent(ctx, repo.Owner, repo.Name, path, snapshot.BaseBranch)
		if err != nil {
			// Context files are optional and should never block preservation of
			// the pull request itself, including permission and 404 failures.
			continue
		}
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if len(body) > 64<<10 {
			body = body[:64<<10]
		}
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceItem{
			ID: "repository_context:" + path, RepositoryID: repo.ID, PRNumber: snapshot.Number,
			Type: "repository_context", Author: "repository", Path: path, Body: body,
			SourceURL: sourceURL, CreatedAt: snapshot.CreatedAt,
		})
	}
}

func EmbedResult(ctx context.Context, provider embedding.Provider, snapshot model.PullRequestSnapshot, items []model.EvidenceItem, result *model.AnalysisResult) (embedding.Set, error) {
	var discussion strings.Builder
	for _, item := range evidence.ForAnalysis(items) {
		if item.Type != "diff" {
			discussion.WriteString(item.Body)
			discussion.WriteByte('\n')
		}
	}
	approach := result.Summary
	for _, claim := range result.AttemptedApproach {
		approach += " " + claim.Claim
	}
	for _, claim := range result.RejectedOrQuestionedApproaches {
		approach += " " + claim.Claim
	}
	texts := []string{snapshot.Title, truncate(snapshot.Body, 64<<10), discussion.String(), truncate(approach, 64<<10)}
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vector, err := provider.Embed(ctx, text)
		if err != nil {
			return embedding.Set{}, err
		}
		vectors[i], err = embedding.Fit(vector)
		if err != nil {
			return embedding.Set{}, err
		}
	}
	return embedding.Set{Title: vectors[0], Description: vectors[1], Discussion: vectors[2], Approach: vectors[3]}, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "")
}

// VerifyResult drops claims that cite no valid evidence ID or carry an empty
// claim text, and recomputes each claim's confidence against the ranked items.
// It is the single verification gate for every analysis path; the eval
// harness reuses it so measurements reflect production behavior.
func VerifyResult(result *model.AnalysisResult, items []model.EvidenceItem) *model.AnalysisResult {
	valid := map[string]bool{}
	for _, item := range items {
		valid[item.ID] = true
	}
	filter := func(in []model.Claim) []model.Claim {
		out := make([]model.Claim, 0, len(in))
		for _, claim := range in {
			ids := []string{}
			for _, id := range claim.EvidenceIDs {
				if valid[id] {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 && claim.Claim != "" {
				claim.EvidenceIDs = ids
				claim.Confidence = confidence.Score(claim.Claim, items, ids)
				out = append(out, claim)
			}
		}
		return out
	}
	result.AttemptedApproach = filter(result.AttemptedApproach)
	result.ValuableFindings = filter(result.ValuableFindings)
	result.RejectedOrQuestionedApproaches = filter(result.RejectedOrQuestionedApproaches)
	result.UnresolvedQuestions = filter(result.UnresolvedQuestions)
	result.SuggestedFutureDirection = filter(result.SuggestedFutureDirection)
	return result
}

func aggregateConfidence(result *model.AnalysisResult) float64 {
	total := 0.0
	count := 0
	for _, claims := range [][]model.Claim{result.AttemptedApproach, result.ValuableFindings, result.RejectedOrQuestionedApproaches, result.UnresolvedQuestions, result.SuggestedFutureDirection} {
		for _, claim := range claims {
			total += claim.Confidence
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

// VerifiedOutcomes de-duplicates the outcome list, drops values outside the
// model's outcome vocabulary, and falls back to unknown when nothing remains.
// Exported for the eval harness, which must score against the same vocabulary.
func VerifiedOutcomes(values []model.Outcome) []model.Outcome {
	allowed := map[model.Outcome]bool{
		model.OutcomeSuperseded: true, model.OutcomeDuplicate: true, model.OutcomeDesignDisagreement: true,
		model.OutcomeImplementationProblem: true, model.OutcomePerformanceConcern: true, model.OutcomeRegressionRisk: true,
		model.OutcomeMissingTests: true, model.OutcomeInsufficientEvidence: true, model.OutcomeCannotReproduce: true,
		model.OutcomeScopeTooLarge: true, model.OutcomeUpstreamResolution: true, model.OutcomeInactiveOrAbandoned: true,
		model.OutcomeNoLongerNeeded: true, model.OutcomeUnknown: true,
	}
	seen := make(map[model.Outcome]bool)
	out := make([]model.Outcome, 0, len(values))
	for _, value := range values {
		if allowed[value] && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return []model.Outcome{model.OutcomeUnknown}
	}
	return out
}

func verifiedAreas(values []string, files []model.ChangedFile) []string {
	allowed := make(map[string]bool)
	for _, file := range files {
		allowed[file.Filename] = true
	}
	seen := make(map[string]bool)
	out := make([]string, 0, min(len(values), 20))
	for _, value := range values {
		if allowed[value] && !seen[value] && len(out) < 20 {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		for _, file := range files {
			if !seen[file.Filename] && len(out) < 20 {
				seen[file.Filename] = true
				out = append(out, file.Filename)
			}
		}
	}
	return out
}

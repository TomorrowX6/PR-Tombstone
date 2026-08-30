package evidence

import (
	"sort"
	"strings"
	"time"

	"pr-tombstone/internal/model"
)

// Rank applies deterministic, explainable evidence preferences before an AI
// provider sees any content.
func Rank(items []model.EvidenceItem) []model.EvidenceItem {
	result := append([]model.EvidenceItem(nil), items...)
	latest := time.Time{}
	for _, item := range result {
		if item.CreatedAt.After(latest) {
			latest = item.CreatedAt
		}
	}
	for i := range result {
		item := &result[i]
		score := 0.10
		switch item.Type {
		case "review":
			score += 0.35
		case "review_comment":
			score += 0.28
		case "issue_comment":
			score += 0.16
		case "timeline":
			score += 0.08
		case "pr_body":
			score += 0.05
		case "commit":
			score += 0.10
		case "repository_context":
			score += 0.04
		case "diff":
			score += 0.02
		}
		if item.Type == "review" && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(item.Body)), "CHANGES_REQUESTED") {
			score += 0.20
		}
		if strings.EqualFold(item.AuthorAssociation, "MEMBER") || strings.EqualFold(item.AuthorAssociation, "COLLABORATOR") || strings.EqualFold(item.AuthorAssociation, "OWNER") {
			score += 0.18
		}
		if !latest.IsZero() && !item.CreatedAt.IsZero() && latest.Sub(item.CreatedAt) < 72*time.Hour {
			score += 0.10
		}
		lower := strings.ToLower(item.Body)
		for _, phrase := range []string{"won't merge", "will not merge", "because", "do not merge", "performance", "regression", "superseded", "duplicate"} {
			if strings.Contains(lower, phrase) {
				score += 0.05
				break
			}
		}
		item.RankScore = score
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].RankScore > result[j].RankScore })
	return result
}

// ForAnalysis applies a deterministic prompt budget while preserving evidence
// identity. It keeps high-ranked items first and truncates individual bodies;
// the full non-diff evidence remains available in the database and UI.
func ForAnalysis(items []model.EvidenceItem) []model.EvidenceItem {
	const (
		maxItems = 200
		maxBytes = 128 << 10
		maxBody  = 16 << 10
	)
	ranked := Rank(items)
	out := make([]model.EvidenceItem, 0, min(len(ranked), maxItems))
	used := 0
	for _, item := range ranked {
		if len(out) >= maxItems || used >= maxBytes {
			break
		}
		body := item.Body
		if len(body) > maxBody {
			body = strings.ToValidUTF8(body[:maxBody], "")
		}
		remaining := maxBytes - used
		if len(body) > remaining {
			body = strings.ToValidUTF8(body[:remaining], "")
		}
		item.Body = body
		used += len(body)
		out = append(out, item)
	}
	return out
}

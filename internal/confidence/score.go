package confidence

import (
	"strings"

	"pr-tombstone/internal/model"
)

// Score is platform confidence, not the untrusted model-provided confidence.
// The factors mirror the README and are clamped to [0,1].
func Score(claim string, items []model.EvidenceItem, evidenceIDs []string) float64 {
	if len(evidenceIDs) == 0 {
		return 0
	}
	evidenceByID := make(map[string]model.EvidenceItem, len(items))
	for _, item := range items {
		evidenceByID[item.ID] = item
	}
	value := 0.0
	strong := false
	maintainer := 0
	for _, id := range evidenceIDs {
		item, ok := evidenceByID[id]
		if !ok {
			continue
		}
		lower := strings.ToLower(item.Body)
		if strings.Contains(lower, "because") || strings.Contains(lower, "won't merge") || strings.Contains(lower, "will not merge") || strings.Contains(lower, "do not merge") {
			value += 0.40
			strong = true
		}
		if item.Type == "review" && strings.Contains(strings.ToUpper(item.Body), "CHANGES_REQUESTED") {
			value += 0.20
		}
		if item.Type == "review" || item.Type == "review_comment" {
			strong = true
		}
		if item.AuthorAssociation == "MEMBER" || item.AuthorAssociation == "COLLABORATOR" || item.AuthorAssociation == "OWNER" {
			maintainer++
			value += 0.10
		}
		if item.Type == "issue_comment" && strings.Contains(lower, "close") {
			value += 0.15
		}
		if strings.Contains(lower, "performance") || strings.Contains(lower, "stall") || strings.Contains(lower, "slow") {
			value += 0.05
		}
		if strings.Contains(lower, "i think") || strings.Contains(lower, "maybe") || strings.Contains(lower, "perhaps") {
			value -= 0.20
		}
		if strings.Contains(lower, "conflict") || strings.Contains(lower, "disagree") {
			value -= 0.30
		}
	}
	if len(evidenceIDs) > 1 {
		value += 0.15
	}
	if !strong {
		value -= 0.20
	}
	_ = claim
	if maintainer == 0 {
		value -= 0.10
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

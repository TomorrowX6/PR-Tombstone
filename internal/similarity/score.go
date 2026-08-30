package similarity

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"pr-tombstone/internal/model"
)

// Weights and thresholds are provisional v0.2 heuristics. They are named
// constants instead of buried literals and will be calibrated against the
// labeled dogfood dataset before any stronger user-facing claim is attached
// to the resulting score.
const (
	weightHeadline = 0.30
	weightFiles    = 0.25
	weightPaths    = 0.15
	weightApproach = 0.15
	weightLabels   = 0.10
	weightSymbols  = 0.05

	relatedThreshold = 0.60
	warningThreshold = 0.80
)

// reason renders the real component values instead of a fixed phrase so
// users can judge why a match surfaced and can see when the semantic signal
// was unavailable.
func reason(c model.ScoreComponents) string {
	parts := make([]string, 0, 6)
	if c.Semantic != nil {
		parts = append(parts, fmt.Sprintf("semantic %.0f%%", *c.Semantic*100))
	} else {
		parts = append(parts, "semantic unavailable")
	}
	parts = append(parts,
		fmt.Sprintf("title/body %.0f%%", c.TitleBody*100),
		fmt.Sprintf("files %.0f%%", c.Files*100),
		fmt.Sprintf("modules %.0f%%", c.Paths*100),
		fmt.Sprintf("approach %.0f%%", c.Approach*100),
	)
	if c.Labels > 0 {
		parts = append(parts, fmt.Sprintf("labels %.0f%%", c.Labels*100))
	}
	return strings.Join(parts, " · ")
}

// Compare implements the v0.2 explainable similarity score. It intentionally
// combines lexical and structural signals so a semantic match cannot hide a
// completely different code area.
func Compare(current model.PullRequestSnapshot, history []model.Tombstone) []model.SimilarityMatch {
	return CompareWithSemantic(current, history, nil)
}

// CompareWithSemantic combines provider-backed semantic scores with structural
// signals. Missing semantic entries fall back to deterministic token overlap.
func CompareWithSemantic(current model.PullRequestSnapshot, history []model.Tombstone, semantic map[int]float64) []model.SimilarityMatch {
	result := make([]model.SimilarityMatch, 0)
	for _, old := range history {
		if old.State != model.StateActive {
			continue
		}
		if old.PR.Number == current.Number {
			continue
		}
		components := model.ScoreComponents{
			TitleBody: jaccard(tokens(current.Title+" "+current.Body), tokens(old.PR.Title+" "+old.PR.Body)),
			Files:     fileOverlap(current.Files, old.PR.Files),
			Paths:     moduleOverlap(current.Files, old.PR.Files),
			Approach:  jaccard(tokens(current.Title+" "+current.Body), tokens(old.Summary+" "+claimsText(old))),
			Labels:    jaccard(stringSet(current.Labels), stringSet(old.PR.Labels)),
			Symbols:   symbolOverlap(current, old.PR),
		}
		// The headline slot uses the provider-backed semantic score when one
		// exists; otherwise it falls back to the lexical title/body overlap and
		// the reason string must disclose the fallback instead of claiming a
		// semantic match.
		headline := components.TitleBody
		if value, ok := semantic[old.PR.Number]; ok {
			scaled := max(0, min(1, value))
			components.Semantic = &scaled
			headline = scaled
		}
		score := weightHeadline*headline + weightFiles*components.Files + weightPaths*components.Paths + weightApproach*components.Approach + weightLabels*components.Labels + weightSymbols*components.Symbols
		if score < relatedThreshold {
			continue
		}
		relationship := "related history"
		if score > warningThreshold {
			relationship = "historical warning"
		}
		result = append(result, model.SimilarityMatch{NewPRNumber: current.Number, OldPRNumber: old.PR.Number, Score: score, Relationship: relationship, Reason: reason(components), Components: components})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	return result
}

func claimsText(t model.Tombstone) string {
	var parts []string
	for _, c := range append(append([]model.Claim{}, t.AttemptedApproach...), t.RejectedOrQuestionedApproaches...) {
		parts = append(parts, c.Claim)
	}
	return strings.Join(parts, " ")
}

func tokens(value string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range tokenPattern.FindAllString(strings.ToLower(value), -1) {
		if len(token) > 2 {
			out[token] = struct{}{}
		}
	}
	return out
}
func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		for key := range tokens(value) {
			out[key] = struct{}{}
		}
	}
	return out
}
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for key := range a {
		if _, ok := b[key]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(len(a)+len(b)-intersection)
}
func fileOverlap(a, b []model.ChangedFile) float64 {
	left := map[string]struct{}{}
	right := map[string]struct{}{}
	for _, file := range a {
		left[file.Filename] = struct{}{}
	}
	for _, file := range b {
		right[file.Filename] = struct{}{}
	}
	return jaccard(left, right)
}
func moduleOverlap(a, b []model.ChangedFile) float64 {
	left := map[string]struct{}{}
	right := map[string]struct{}{}
	for _, file := range a {
		left[module(file.Filename)] = struct{}{}
	}
	for _, file := range b {
		right[module(file.Filename)] = struct{}{}
	}
	return jaccard(left, right)
}
func module(path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) < 2 {
		return path
	}
	return strings.Join(parts[:len(parts)-1], "/")
}
func symbolOverlap(a model.PullRequestSnapshot, b model.PullRequestSnapshot) float64 {
	return jaccard(tokens(filesText(a.Files)), tokens(filesText(b.Files)))
}
func filesText(files []model.ChangedFile) string {
	var out []string
	for _, file := range files {
		out = append(out, file.Filename, file.Patch)
	}
	return strings.Join(out, " ")
}

var tokenPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_:-]{2,}`)

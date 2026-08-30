package similarity

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"pr-tombstone/internal/model"
)

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
		titleBody := jaccard(tokens(current.Title+" "+current.Body), tokens(old.PR.Title+" "+old.PR.Body))
		if value, ok := semantic[old.PR.Number]; ok {
			titleBody = max(0, min(1, value))
		}
		files := fileOverlap(current.Files, old.PR.Files)
		paths := moduleOverlap(current.Files, old.PR.Files)
		approach := jaccard(tokens(current.Title+" "+current.Body), tokens(old.Summary+" "+claimsText(old)))
		labels := jaccard(stringSet(current.Labels), stringSet(old.PR.Labels))
		symbols := symbolOverlap(current, old.PR)
		score := 0.30*titleBody + 0.25*files + 0.15*paths + 0.15*approach + 0.10*labels + 0.05*symbols
		if score < 0.60 {
			continue
		}
		relationship := "related history"
		if score > 0.80 {
			relationship = "historical warning"
		}
		result = append(result, model.SimilarityMatch{NewPRNumber: current.Number, OldPRNumber: old.PR.Number, Score: score, Relationship: relationship, Reason: fmt.Sprintf("%.0f%% same files, %.0f%% same modules, semantic approach match", files*100, paths*100)})
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

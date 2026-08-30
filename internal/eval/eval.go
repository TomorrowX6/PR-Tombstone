// Package eval implements the dogfood evaluation harness: it collects real
// closed-unmerged pull requests, compares the analyzer's predicted outcomes
// against human labels, and reports multi-label precision/recall/F1 plus
// evidence-grounding metrics.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pr-tombstone/internal/model"
)

// OutcomeVocabulary is the closed set of outcomes the model can emit. The
// label protocol and the scorer both validate against this list so a typo in
// an annotation fails loudly instead of silently scoring as an error.
var OutcomeVocabulary = []model.Outcome{
	model.OutcomeSuperseded,
	model.OutcomeDuplicate,
	model.OutcomeDesignDisagreement,
	model.OutcomeImplementationProblem,
	model.OutcomePerformanceConcern,
	model.OutcomeRegressionRisk,
	model.OutcomeMissingTests,
	model.OutcomeInsufficientEvidence,
	model.OutcomeCannotReproduce,
	model.OutcomeScopeTooLarge,
	model.OutcomeUpstreamResolution,
	model.OutcomeInactiveOrAbandoned,
	model.OutcomeNoLongerNeeded,
	model.OutcomeUnknown,
}

// DecisionEvidenceKinds are evidence types that contain a human's stated
// reasoning. Generic timeline events are deliberately excluded because events
// such as closed/labeled/cross-referenced do not by themselves ground a
// technical decision.
var DecisionEvidenceKinds = map[string]bool{
	"review": true, "review_comment": true, "issue_comment": true,
}

// Case is one collected pull request: the full snapshot plus the ranked
// evidence the analyzer would see in production. The snapshot's own evidence
// list is cleared on save to avoid duplicating Case.Evidence.
type Case struct {
	ID       string                    `json:"id"`
	Owner    string                    `json:"owner"`
	Name     string                    `json:"name"`
	Number   int                       `json:"number"`
	URL      string                    `json:"url"`
	Snapshot model.PullRequestSnapshot `json:"snapshot"`
	Evidence []model.EvidenceItem      `json:"evidence"`
}

// Label is one human annotation following the protocol in docs/EVAL.md.
// Outcomes holds the gold-standard reasons the PR was not merged; a label of
// just [unknown] means the discussion does not establish a reason.
type Label struct {
	CaseID    string          `json:"case_id"`
	Annotator string          `json:"annotator"`
	Outcomes  []model.Outcome `json:"outcomes"`
	Notes     string          `json:"notes,omitempty"`
}

// Prediction is the analyzer output for one case after the same verification
// pipeline production runs (ingest.VerifyResult + ingest.VerifiedOutcomes).
type Prediction struct {
	CaseID         string          `json:"case_id"`
	Outcomes       []model.Outcome `json:"outcomes"`
	DecisionClaims int             `json:"decision_claims"`
	GroundedClaims int             `json:"grounded_claims"`
	Error          string          `json:"error,omitempty"`
}

// ValidateOutcomes rejects empty lists, values outside the vocabulary, and
// labels that mix unknown with a concrete outcome. Unknown means the available
// discussion does not establish a reason, so it must stand alone.
func ValidateOutcomes(values []model.Outcome) error {
	if len(values) == 0 {
		return fmt.Errorf("outcomes must not be empty")
	}
	allowed := make(map[model.Outcome]bool, len(OutcomeVocabulary))
	for _, value := range OutcomeVocabulary {
		allowed[value] = true
	}
	seen := make(map[model.Outcome]bool, len(values))
	for _, value := range values {
		if !allowed[value] {
			return fmt.Errorf("outcome %q is outside the vocabulary", value)
		}
		if seen[value] {
			return fmt.Errorf("outcome %q is duplicated", value)
		}
		seen[value] = true
	}
	if seen[model.OutcomeUnknown] && len(values) != 1 {
		return fmt.Errorf("outcome %q must be the only outcome", model.OutcomeUnknown)
	}
	return nil
}

func casesDir(datasetDir string) string { return filepath.Join(datasetDir, "cases") }

// SaveCase writes one collected case to <dataset>/cases/<id>.json.
func SaveCase(datasetDir string, c Case) error {
	dir := casesDir(datasetDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	c.Snapshot.Evidence = nil
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, c.ID+".json"), append(data, '\n'), 0o644)
}

// LoadCases reads every collected case under <dataset>/cases.
func LoadCases(datasetDir string) ([]Case, error) {
	dir := casesDir(datasetDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Case, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var c Case
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// LoadLabels reads <dataset>/labels.jsonl and validates every entry.
func LoadLabels(datasetDir string) (map[string]Label, error) {
	path := filepath.Join(datasetDir, "labels.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Label)
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var label Label
		if err := json.Unmarshal([]byte(line), &label); err != nil {
			return nil, fmt.Errorf("labels.jsonl:%d: %w", lineNumber+1, err)
		}
		if label.CaseID == "" {
			return nil, fmt.Errorf("labels.jsonl:%d: missing case_id", lineNumber+1)
		}
		if err := ValidateOutcomes(label.Outcomes); err != nil {
			return nil, fmt.Errorf("labels.jsonl:%d (%s): %w", lineNumber+1, label.CaseID, err)
		}
		out[label.CaseID] = label
	}
	return out, nil
}

// ScaffoldLabels writes an empty labels.jsonl for every collected case so an
// annotator only has to fill in the outcomes and notes.
func ScaffoldLabels(datasetDir, annotator string) (int, error) {
	cases, err := LoadCases(datasetDir)
	if err != nil {
		return 0, err
	}
	var builder strings.Builder
	for _, c := range cases {
		line, err := json.Marshal(Label{CaseID: c.ID, Annotator: annotator})
		if err != nil {
			return 0, err
		}
		builder.Write(line)
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(datasetDir, "labels.jsonl"), []byte(builder.String()), 0o644); err != nil {
		return 0, err
	}
	return len(cases), nil
}

package eval

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pr-tombstone/internal/model"
)

func TestValidateOutcomes(t *testing.T) {
	if err := ValidateOutcomes([]model.Outcome{model.OutcomeDuplicate}); err != nil {
		t.Fatalf("valid single outcome rejected: %v", err)
	}
	if err := ValidateOutcomes([]model.Outcome{}); err == nil {
		t.Fatal("empty outcomes must be rejected")
	}
	if err := ValidateOutcomes([]model.Outcome{"invented_reason"}); err == nil {
		t.Fatal("out-of-vocabulary outcome must be rejected")
	}
	if err := ValidateOutcomes([]model.Outcome{model.OutcomeUnknown, model.OutcomeUnknown}); err == nil {
		t.Fatal("duplicated outcomes must be rejected")
	}
	if err := ValidateOutcomes([]model.Outcome{model.OutcomeUnknown, model.OutcomeDuplicate}); err == nil {
		t.Fatal("unknown must not be mixed with a concrete outcome")
	}
}

func TestSaveLoadCaseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	collected := Case{
		ID:     "owner__repo__7",
		Owner:  "owner",
		Name:   "repo",
		Number: 7,
		URL:    "https://github.com/owner/repo/pull/7",
		Snapshot: model.PullRequestSnapshot{
			Number:   7,
			Title:    "attempt a thing",
			Evidence: []model.EvidenceItem{{ID: "pr_body:7", Type: "pr_body", Body: "body"}},
		},
		Evidence: []model.EvidenceItem{{ID: "pr_body:7", Type: "pr_body", Body: "body"}},
	}
	if err := SaveCase(dir, collected); err != nil {
		t.Fatalf("SaveCase: %v", err)
	}
	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	if len(cases) != 1 || cases[0].ID != collected.ID {
		t.Fatalf("cases = %+v", cases)
	}
	if len(cases[0].Snapshot.Evidence) != 0 {
		t.Fatal("snapshot evidence must be cleared on save to avoid duplication")
	}
	if len(cases[0].Evidence) != 1 {
		t.Fatal("case evidence must survive the round trip")
	}
}

func TestScaffoldAndLoadLabels(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"a__b__1", "a__b__2"} {
		if err := SaveCase(dir, Case{ID: id, Owner: "a", Name: "b", Number: 1}); err != nil {
			t.Fatalf("SaveCase: %v", err)
		}
	}
	count, err := ScaffoldLabels(dir, "tester")
	if err != nil {
		t.Fatalf("ScaffoldLabels: %v", err)
	}
	if count != 2 {
		t.Fatalf("scaffolded %d labels, want 2", count)
	}
	if _, err := LoadLabels(dir); err == nil {
		t.Fatal("unfilled scaffold must fail validation")
	}
	path := filepath.Join(dir, "labels.jsonl")
	filled := `{"case_id":"a__b__1","annotator":"tester","outcomes":["duplicate"]}` + "\n" +
		`{"case_id":"a__b__2","annotator":"tester","outcomes":["unknown"],"notes":"no reason stated"}` + "\n"
	if err := os.WriteFile(path, []byte(filled), 0o644); err != nil {
		t.Fatalf("write labels: %v", err)
	}
	labels, err := LoadLabels(dir)
	if err != nil {
		t.Fatalf("LoadLabels: %v", err)
	}
	if len(labels) != 2 || labels["a__b__2"].Notes == "" {
		t.Fatalf("labels = %+v", labels)
	}
}

func TestEvaluateScoresPerfectPredictions(t *testing.T) {
	predictions := []Prediction{
		{CaseID: "a__b__1", Outcomes: []model.Outcome{model.OutcomeDuplicate}, DecisionClaims: 1, GroundedClaims: 1},
		{CaseID: "a__b__2", Outcomes: []model.Outcome{model.OutcomeUnknown}, DecisionClaims: 0, GroundedClaims: 0},
	}
	labels := map[string]Label{
		"a__b__1": {CaseID: "a__b__1", Outcomes: []model.Outcome{model.OutcomeDuplicate}},
		"a__b__2": {CaseID: "a__b__2", Outcomes: []model.Outcome{model.OutcomeUnknown}},
	}
	report := Evaluate(predictions, labels, []Case{{ID: "a__b__1"}, {ID: "a__b__2"}})
	s := report.Summary
	if s.Labeled != 2 || s.ExactMatch != 2 || s.ExactMatchRate != 1 {
		t.Fatalf("summary = %+v", s)
	}
	if s.MicroPrecision != 1 || s.MicroRecall != 1 || s.MicroF1 != 1 {
		t.Fatalf("perfect micro scores expected, got %+v", s)
	}
	if s.UnknownAgree != 1 {
		t.Fatalf("unknown agreement = %d, want 1", s.UnknownAgree)
	}
	if s.DecisionCoverage != 1 {
		t.Fatalf("decision coverage = %f, want 1", s.DecisionCoverage)
	}
	if s.ClaimGrounding != 1 {
		t.Fatalf("claim grounding = %f, want 1", s.ClaimGrounding)
	}
}

func TestEvaluateMicroScores(t *testing.T) {
	predictions := []Prediction{
		{CaseID: "a__b__1", Outcomes: []model.Outcome{model.OutcomeDuplicate, model.OutcomePerformanceConcern}, DecisionClaims: 2, GroundedClaims: 1},
	}
	labels := map[string]Label{
		"a__b__1": {CaseID: "a__b__1", Outcomes: []model.Outcome{model.OutcomeDuplicate, model.OutcomeMissingTests}},
	}
	report := Evaluate(predictions, labels, []Case{{ID: "a__b__1"}})
	s := report.Summary
	if s.ExactMatch != 0 {
		t.Fatal("partial overlap must not count as an exact match")
	}
	if s.MicroPrecision != 0.5 || s.MicroRecall != 0.5 || s.MicroF1 != 0.5 {
		t.Fatalf("micro scores = %+v, want 0.5 each", s)
	}
	if s.ClaimGrounding != 0.5 {
		t.Fatalf("claim grounding = %f, want 0.5", s.ClaimGrounding)
	}
}

func TestEvaluateSkipsUnlabeled(t *testing.T) {
	predictions := []Prediction{{CaseID: "a__b__1", Outcomes: []model.Outcome{model.OutcomeUnknown}}}
	report := Evaluate(predictions, map[string]Label{}, []Case{{ID: "a__b__1"}})
	if report.Summary.Labeled != 0 || report.Summary.Unlabeled != 1 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	if report.Summary.ExactMatchRate != 0 || report.Summary.MicroF1 != 0 {
		t.Fatalf("unlabeled cases must not produce scores: %+v", report.Summary)
	}
}

func TestEvaluateSkipsInferenceFailures(t *testing.T) {
	predictions := []Prediction{
		{CaseID: "a__b__1", Outcomes: []model.Outcome{model.OutcomeUnknown}, Error: "provider timeout"},
		{CaseID: "a__b__2", Outcomes: []model.Outcome{model.OutcomeDuplicate}},
	}
	labels := map[string]Label{
		"a__b__1": {CaseID: "a__b__1", Outcomes: []model.Outcome{model.OutcomeMissingTests}},
		"a__b__2": {CaseID: "a__b__2", Outcomes: []model.Outcome{model.OutcomeDuplicate}},
	}
	report := Evaluate(predictions, labels, []Case{{ID: "a__b__1"}, {ID: "a__b__2"}})
	if report.Summary.Labeled != 2 || report.Summary.Skipped != 1 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	if report.Summary.ExactMatchRate != 1 || report.Summary.MicroF1 != 1 {
		t.Fatalf("failed inference must be excluded from accuracy metrics: %+v", report.Summary)
	}
}

func TestEvaluateMacroF1IsMeanOfClassF1(t *testing.T) {
	predictions := []Prediction{
		{CaseID: "a", Outcomes: []model.Outcome{model.OutcomeDuplicate}},
		{CaseID: "b", Outcomes: []model.Outcome{model.OutcomeDuplicate}},
	}
	labels := map[string]Label{
		"a": {CaseID: "a", Outcomes: []model.Outcome{model.OutcomeDuplicate}},
		"b": {CaseID: "b", Outcomes: []model.Outcome{model.OutcomeMissingTests}},
	}
	report := Evaluate(predictions, labels, []Case{{ID: "a"}, {ID: "b"}})
	var sum float64
	for _, class := range report.Classes {
		sum += class.F1
	}
	want := sum / float64(len(report.Classes))
	if math.Abs(report.Summary.MacroF1-want) > 1e-12 {
		t.Fatalf("macro F1 = %f, want mean class F1 %f", report.Summary.MacroF1, want)
	}
}

func TestRenderReportContainsSections(t *testing.T) {
	report := Evaluate(
		[]Prediction{{CaseID: "a__b__1", Outcomes: []model.Outcome{model.OutcomeDuplicate}, DecisionClaims: 1, GroundedClaims: 1}},
		map[string]Label{"a__b__1": {CaseID: "a__b__1", Outcomes: []model.Outcome{model.OutcomeDuplicate}, Notes: "clear duplicate"}},
		[]Case{{ID: "a__b__1", Owner: "a", Name: "b", Number: 1}},
	)
	rendered := RenderReport(report, "rules")
	for _, section := range []string{"# Dogfood evaluation report", "## Outcome scores", "## Evidence grounding", "## Per-case comparison", "a__b__1", "clear duplicate"} {
		if !strings.Contains(rendered, section) {
			t.Fatalf("rendered report misses %q:\n%s", section, rendered)
		}
	}
}

package eval

import (
	"fmt"
	"strings"

	"pr-tombstone/internal/model"
)

// RenderReport renders the evaluation as a self-contained markdown document:
// headline scores, per-class confusion table, evidence-grounding metrics and
// a per-case appendix for annotation review.
func RenderReport(report Report, modelName string) string {
	s := report.Summary
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Dogfood evaluation report\n\n")
	fmt.Fprintf(&builder, "- Analyzer: `%s`\n", modelName)
	fmt.Fprintf(&builder, "- Cases collected: %d, labeled: %d, unlabeled (skipped): %d\n", s.Cases, s.Labeled, s.Unlabeled)
	fmt.Fprintf(&builder, "- Exact-set match rate: %d/%d (%.1f%%)\n", s.ExactMatch, s.Labeled, s.ExactMatchRate*100)
	fmt.Fprintf(&builder, "- Unknown outcome: gold %d · predicted %d · agreement %d\n\n", s.GoldUnknown, s.PredUnknown, s.UnknownAgree)

	fmt.Fprintf(&builder, "## Outcome scores (multi-label)\n\n")
	fmt.Fprintf(&builder, "| Aggregation | Precision | Recall | F1 |\n|---|---|---|---|\n")
	fmt.Fprintf(&builder, "| Micro | %.3f | %.3f | %.3f |\n", s.MicroPrecision, s.MicroRecall, s.MicroF1)
	fmt.Fprintf(&builder, "| Macro | %.3f | %.3f | %.3f |\n\n", s.MacroPrecision, s.MacroRecall, s.MacroF1)

	fmt.Fprintf(&builder, "| Outcome | Gold | Predicted | TP | FP | FN | Precision | Recall | F1 |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, class := range report.Classes {
		fmt.Fprintf(&builder, "| %s | %d | %d | %d | %d | %d | %.3f | %.3f | %.3f |\n",
			class.Class, class.Gold, class.Predicted, class.TruePos, class.FalsePos, class.FalseNeg, class.Precision, class.Recall, class.F1)
	}
	builder.WriteString("\n")

	fmt.Fprintf(&builder, "## Evidence grounding\n\n")
	fmt.Fprintf(&builder, "- Decision coverage: %.1f%% of cases with a real gold outcome produced at least one evidence-backed decision claim.\n", s.DecisionCoverage*100)
	fmt.Fprintf(&builder, "- Claim grounding: %.1f%% of decision claims cited human discussion evidence (review, comments, timeline).\n\n", s.ClaimGrounding*100)

	fmt.Fprintf(&builder, "## Per-case comparison\n\n")
	fmt.Fprintf(&builder, "| Case | Gold | Predicted | Match | Notes |\n|---|---|---|---|---|\n")
	for _, detail := range report.Details {
		gold := strings.Join(outcomeNames(detail.Gold), ", ")
		pred := strings.Join(outcomeNames(detail.Pred), ", ")
		match := "✗"
		if detail.Match {
			match = "✓"
		}
		notes := strings.ReplaceAll(detail.Notes, "|", "\\|")
		if detail.Error != "" {
			notes = "ERROR: " + detail.Error
		}
		fmt.Fprintf(&builder, "| %s | %s | %s | %s | %s |\n", detail.CaseID, gold, pred, match, notes)
	}
	return builder.String()
}

func outcomeNames(values []model.Outcome) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

package eval

import (
	"math"
	"sort"

	"pr-tombstone/internal/model"
)

// ClassMetrics holds the confusion counts and derived scores for one outcome
// class across the labeled cases.
type ClassMetrics struct {
	Class     model.Outcome `json:"class"`
	Gold      int           `json:"gold"`
	Predicted int           `json:"predicted"`
	TruePos   int           `json:"true_positives"`
	FalsePos  int           `json:"false_positives"`
	FalseNeg  int           `json:"false_negatives"`
	Precision float64       `json:"precision"`
	Recall    float64       `json:"recall"`
	F1        float64       `json:"f1"`
}

// Summary carries the dataset-level counts.
type Summary struct {
	Cases     int `json:"cases"`
	Labeled   int `json:"labeled"`
	Skipped   int `json:"skipped"`
	Unlabeled int `json:"unlabeled"`

	ExactMatch     int     `json:"exact_match"`
	ExactMatchRate float64 `json:"exact_match_rate"`

	GoldUnknown  int `json:"gold_unknown"`
	PredUnknown  int `json:"pred_unknown"`
	UnknownAgree int `json:"unknown_agree"`

	// DecisionCoverage is the fraction of successfully evaluated labeled cases
	// with a real (non-unknown) gold outcome for which the analyzer produced at
	// least one evidence-backed decision claim.
	DecisionCoverage float64 `json:"decision_coverage"`
	// ClaimGrounding is the fraction of decision claims whose cited evidence
	// includes explicit human discussion (review or comments) rather than only
	// the PR body, diffs, or generic timeline events.
	ClaimGrounding float64 `json:"claim_grounding"`

	MicroPrecision float64 `json:"micro_precision"`
	MicroRecall    float64 `json:"micro_recall"`
	MicroF1        float64 `json:"micro_f1"`
	MacroPrecision float64 `json:"macro_precision"`
	MacroRecall    float64 `json:"macro_recall"`
	MacroF1        float64 `json:"macro_f1"`
}

// CaseDetail is the per-case comparison shown in the report appendix.
type CaseDetail struct {
	CaseID string          `json:"case_id"`
	Owner  string          `json:"owner"`
	Name   string          `json:"name"`
	Number int             `json:"number"`
	Gold   []model.Outcome `json:"gold"`
	Pred   []model.Outcome `json:"pred"`
	Match  bool            `json:"match"`
	Notes  string          `json:"notes,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Report is the full evaluation output, serialized as JSON and rendered to
// markdown by RenderReport.
type Report struct {
	Summary Summary        `json:"summary"`
	Classes []ClassMetrics `json:"classes"`
	Details []CaseDetail   `json:"details"`
	Preds   []Prediction   `json:"predictions"`
}

func rate(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func f1Score(precision, recall float64) float64 {
	if precision+recall == 0 {
		return 0
	}
	return 2 * precision * recall / (precision + recall)
}

// Evaluate scores predictions against labels. Unlabeled cases and inference
// failures are reported but excluded from accuracy metrics, so transient model
// or network errors do not masquerade as wrong "unknown" predictions.
func Evaluate(predictions []Prediction, labels map[string]Label, cases []Case) Report {
	byID := make(map[string]Case, len(cases))
	for _, c := range cases {
		byID[c.ID] = c
	}
	report := Report{Preds: predictions}

	counts := map[model.Outcome]*[4]int{}
	var microTP, microFP, microFN int
	realGoldCount := 0
	covered := 0
	decisionClaims, groundedClaims := 0, 0

	for _, prediction := range predictions {
		label, ok := labels[prediction.CaseID]
		report.Summary.Cases++
		if !ok {
			report.Summary.Unlabeled++
			continue
		}
		report.Summary.Labeled++

		if prediction.Error != "" {
			report.Summary.Skipped++
			report.Details = append(report.Details, CaseDetail{
				CaseID: prediction.CaseID,
				Owner:  caseOwner(byID, prediction.CaseID),
				Name:   caseName(byID, prediction.CaseID),
				Number: caseNumber(byID, prediction.CaseID),
				Gold:   label.Outcomes,
				Pred:   prediction.Outcomes,
				Notes:  label.Notes,
				Error:  prediction.Error,
			})
			continue
		}

		goldSet := setOf(label.Outcomes)
		predSet := setOf(prediction.Outcomes)
		exact := len(goldSet) == len(predSet)
		if exact {
			for value := range goldSet {
				if !predSet[value] {
					exact = false
					break
				}
			}
		}
		if exact {
			report.Summary.ExactMatch++
		}

		goldUnknown := len(goldSet) == 1 && goldSet[model.OutcomeUnknown]
		predUnknown := len(predSet) == 1 && predSet[model.OutcomeUnknown]
		if goldUnknown {
			report.Summary.GoldUnknown++
		}
		if predUnknown {
			report.Summary.PredUnknown++
		}
		if goldUnknown && predUnknown {
			report.Summary.UnknownAgree++
		}

		for _, value := range OutcomeVocabulary {
			entry := counts[value]
			if entry == nil {
				entry = &[4]int{}
				counts[value] = entry
			}
			if goldSet[value] {
				entry[0]++
			}
			if predSet[value] {
				entry[1]++
			}
			switch {
			case goldSet[value] && predSet[value]:
				entry[2]++
				microTP++
			case predSet[value]:
				entry[3]++
				microFP++
			case goldSet[value]:
				microFN++
			}
		}

		if !goldUnknown {
			realGoldCount++
			if prediction.DecisionClaims > 0 {
				covered++
			}
		}
		decisionClaims += prediction.DecisionClaims
		groundedClaims += prediction.GroundedClaims

		report.Details = append(report.Details, CaseDetail{
			CaseID: prediction.CaseID,
			Owner:  caseOwner(byID, prediction.CaseID),
			Name:   caseName(byID, prediction.CaseID),
			Number: caseNumber(byID, prediction.CaseID),
			Gold:   label.Outcomes,
			Pred:   prediction.Outcomes,
			Match:  exact,
			Notes:  label.Notes,
		})
	}

	scored := report.Summary.Labeled - report.Summary.Skipped
	report.Summary.ExactMatchRate = rate(report.Summary.ExactMatch, scored)
	report.Summary.DecisionCoverage = rate(covered, realGoldCount)
	report.Summary.ClaimGrounding = rate(groundedClaims, decisionClaims)
	report.Summary.MicroPrecision = rate(microTP, microTP+microFP)
	report.Summary.MicroRecall = rate(microTP, microTP+microFN)
	report.Summary.MicroF1 = f1Score(report.Summary.MicroPrecision, report.Summary.MicroRecall)

	var macroP, macroR, macroF1 float64
	activeClasses := 0
	for _, value := range OutcomeVocabulary {
		entry := counts[value]
		if entry == nil {
			continue
		}
		gold, pred, tp, fp := entry[0], entry[1], entry[2], entry[3]
		if gold == 0 && pred == 0 {
			continue
		}
		fn := gold - tp
		class := ClassMetrics{Class: value, Gold: gold, Predicted: pred, TruePos: tp, FalsePos: fp, FalseNeg: fn}
		class.Precision = rate(tp, tp+fp)
		class.Recall = rate(tp, tp+fn)
		class.F1 = f1Score(class.Precision, class.Recall)
		report.Classes = append(report.Classes, class)
		macroP += class.Precision
		macroR += class.Recall
		macroF1 += class.F1
		activeClasses++
	}
	if activeClasses > 0 {
		macroP /= float64(activeClasses)
		macroR /= float64(activeClasses)
		macroF1 /= float64(activeClasses)
	}
	report.Summary.MacroPrecision = macroP
	report.Summary.MacroRecall = macroR
	report.Summary.MacroF1 = macroF1

	sort.Slice(report.Details, func(i, j int) bool { return report.Details[i].CaseID < report.Details[j].CaseID })
	return report
}

func setOf(values []model.Outcome) map[model.Outcome]bool {
	out := make(map[model.Outcome]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func caseOwner(cases map[string]Case, id string) string { return cases[id].Owner }
func caseName(cases map[string]Case, id string) string  { return cases[id].Name }
func caseNumber(cases map[string]Case, id string) int   { return cases[id].Number }

// clamp avoids NaN in JSON output for degenerate datasets.
func clamp(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return value
}

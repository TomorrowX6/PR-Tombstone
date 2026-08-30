package eval

import (
	"context"

	"pr-tombstone/internal/analyzer"
	"pr-tombstone/internal/evidence"
	"pr-tombstone/internal/ingest"
	"pr-tombstone/internal/model"
)

// Runner executes the analyzer over collected cases with exactly the
// production post-processing (evidence ranking, claim verification, outcome
// vocabulary filtering) so the scored output reflects what users see.
type Runner struct {
	Analyze analyzer.Analyzer
}

// Predict analyzes one case and folds the verified claims into the outcome
// and grounding counters used by the evidence-accuracy metrics.
func (r Runner) Predict(ctx context.Context, c Case) Prediction {
	prediction := Prediction{CaseID: c.ID}
	ranked := evidence.Rank(c.Evidence)
	result, err := r.Analyze.Analyze(ctx, model.AnalysisInput{Snapshot: c.Snapshot, Evidence: ranked})
	if err != nil {
		prediction.Error = err.Error()
		prediction.Outcomes = []model.Outcome{model.OutcomeUnknown}
		return prediction
	}
	result = ingest.VerifyResult(result, ranked)
	prediction.Outcomes = ingest.VerifiedOutcomes(result.Outcomes)

	// Evidence grounding: a decision claim counts as grounded when at least
	// one cited evidence item is human discussion (review / comments /
	// timeline) rather than only the PR body or a raw diff.
	evidenceType := make(map[string]string, len(ranked))
	for _, item := range ranked {
		evidenceType[item.ID] = item.Type
	}
	grounded := func(claims []model.Claim) int {
		count := 0
		for _, claim := range claims {
			for _, id := range claim.EvidenceIDs {
				if DecisionEvidenceKinds[evidenceType[id]] {
					count++
					break
				}
			}
		}
		return count
	}
	claims := result.RejectedOrQuestionedApproaches
	prediction.DecisionClaims = len(claims)
	prediction.GroundedClaims = grounded(claims)
	return prediction
}

// Run evaluates every case and scores the labeled subset.
func (r Runner) Run(ctx context.Context, cases []Case, labels map[string]Label) (Report, error) {
	predictions := make([]Prediction, 0, len(cases))
	for _, c := range cases {
		predictions = append(predictions, r.Predict(ctx, c))
	}
	return Evaluate(predictions, labels, cases), nil
}

// DefaultRunner builds the analyzer from the same environment configuration
// as the production pipeline, defaulting to the deterministic rules engine.
func DefaultRunner(provider, baseURL, apiKey, modelName string) Runner {
	return Runner{Analyze: analyzer.New(provider, baseURL, apiKey, modelName)}
}

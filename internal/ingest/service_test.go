package ingest

import (
	"testing"

	"pr-tombstone/internal/model"
)

func TestVerifyResultRecomputesConfidenceAndDropsUnknownEvidence(t *testing.T) {
	items := []model.EvidenceItem{{ID: "review:1", Type: "review", AuthorAssociation: "MEMBER", Body: "will not merge because of regression"}}
	result := &model.AnalysisResult{AttemptedApproach: []model.Claim{
		{Claim: "Supported", Confidence: 1, EvidenceIDs: []string{"review:1", "missing"}},
		{Claim: "Unsupported", Confidence: 1, EvidenceIDs: []string{"missing"}},
	}}
	verified := VerifyResult(result, items)
	if len(verified.AttemptedApproach) != 1 || len(verified.AttemptedApproach[0].EvidenceIDs) != 1 {
		t.Fatalf("unexpected claims: %+v", verified.AttemptedApproach)
	}
	if verified.AttemptedApproach[0].Confidence >= 1 || verified.AttemptedApproach[0].Confidence <= 0 {
		t.Fatalf("confidence was not recomputed: %f", verified.AttemptedApproach[0].Confidence)
	}
}

func TestOutputEnumsAndAreasAreBounded(t *testing.T) {
	outcomes := VerifiedOutcomes([]model.Outcome{"invented", model.OutcomeDuplicate, model.OutcomeDuplicate})
	if len(outcomes) != 1 || outcomes[0] != model.OutcomeDuplicate {
		t.Fatalf("unexpected outcomes: %v", outcomes)
	}
	areas := verifiedAreas([]string{"invented", "src/a.go"}, []model.ChangedFile{{Filename: "src/a.go"}, {Filename: "src/b.go"}})
	if len(areas) != 1 || areas[0] != "src/a.go" {
		t.Fatalf("unexpected areas: %v", areas)
	}
}

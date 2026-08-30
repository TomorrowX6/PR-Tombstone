package confidence

import (
	"pr-tombstone/internal/model"
	"testing"
	"time"
)

func TestScoreRequiresEvidence(t *testing.T) {
	if got := Score("claim", nil, nil); got != 0 {
		t.Fatalf("got %v", got)
	}
	items := []model.EvidenceItem{{ID: "review:1", Type: "review", AuthorAssociation: "MEMBER", Body: "We will not merge this because it introduces a performance stall.", CreatedAt: time.Now()}}
	if got := Score("claim", items, []string{"review:1"}); got < 0.4 {
		t.Fatalf("expected strong score, got %v", got)
	}
}

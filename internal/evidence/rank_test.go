package evidence

import (
	"pr-tombstone/internal/model"
	"strings"
	"testing"
	"time"
)

func TestRankPrefersMaintainerReview(t *testing.T) {
	items := []model.EvidenceItem{{ID: "comment", Type: "issue_comment", Body: "maybe", CreatedAt: time.Now().Add(-time.Hour)}, {ID: "review", Type: "review", AuthorAssociation: "MEMBER", Body: "We will not merge because of performance", CreatedAt: time.Now()}}
	ranked := Rank(items)
	if ranked[0].ID != "review" {
		t.Fatalf("got %s first", ranked[0].ID)
	}
}

func TestForAnalysisEnforcesBudget(t *testing.T) {
	items := make([]model.EvidenceItem, 250)
	for i := range items {
		items[i] = model.EvidenceItem{ID: string(rune(i + 1)), Type: "review", Body: strings.Repeat("x", 20<<10)}
	}
	bounded := ForAnalysis(items)
	if len(bounded) > 200 {
		t.Fatalf("too many items: %d", len(bounded))
	}
	total := 0
	for _, item := range bounded {
		total += len(item.Body)
		if len(item.Body) > 16<<10 {
			t.Fatalf("body exceeds limit: %d", len(item.Body))
		}
	}
	if total > 128<<10 {
		t.Fatalf("prompt exceeds limit: %d", total)
	}
}

package httpapi

import (
	"testing"

	"pr-tombstone/internal/model"
)

func TestMergeTombstonesKeepsLexicalThenAddsSemantic(t *testing.T) {
	primary := []model.Tombstone{{ID: 1}, {ID: 2}}
	secondary := []model.Tombstone{{ID: 2}, {ID: 3}}
	merged := mergeTombstones(primary, secondary, 10)
	if len(merged) != 3 || merged[0].ID != 1 || merged[1].ID != 2 || merged[2].ID != 3 {
		t.Fatalf("unexpected hybrid merge: %+v", merged)
	}
}

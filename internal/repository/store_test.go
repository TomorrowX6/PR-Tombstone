package repository

import (
	"testing"

	"pr-tombstone/internal/model"
)

func TestRedactedSnapshotDoesNotMutateInput(t *testing.T) {
	original := model.PullRequestSnapshot{
		Files:    []model.ChangedFile{{Filename: "secret.go", Patch: "+ private source"}},
		Evidence: []model.EvidenceItem{{ID: "diff:secret.go", Type: "diff", Body: "+ private source"}},
	}
	stored := redactedSnapshot(original)
	if stored.Files[0].Patch != "" || stored.Evidence != nil {
		t.Fatalf("snapshot was not redacted: %+v", stored)
	}
	if original.Files[0].Patch == "" || len(original.Evidence) != 1 {
		t.Fatalf("input was mutated: %+v", original)
	}
}

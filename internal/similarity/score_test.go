package similarity

import (
	"pr-tombstone/internal/model"
	"testing"
)

func TestCompareUsesStructuralAndSemanticSignals(t *testing.T) {
	current := model.PullRequestSnapshot{Number: 22, Title: "Serialize Vulkan pipeline destruction", Body: "Use a global mutex for pipeline lifetime", Labels: []string{"vulkan"}, Files: []model.ChangedFile{{Filename: "GPU/Vulkan/VulkanRenderManager.cpp"}}}
	history := []model.Tombstone{{Repository: model.Repository{ID: 1}, PR: model.PullRequestSnapshot{Number: 11, Title: "Vulkan pipeline destruction mutex", Body: "Protect pipeline lifetime with a global mutex", Labels: []string{"vulkan"}, Files: []model.ChangedFile{{Filename: "GPU/Vulkan/VulkanRenderManager.cpp"}}}, State: model.StateActive, Summary: "global mutex introduces render-thread stalls"}}
	got := Compare(current, history)
	if len(got) != 1 {
		t.Fatalf("expected one match, got %d", len(got))
	}
	if got[0].Score < 0.6 {
		t.Fatalf("expected related match, got %v", got[0].Score)
	}
}

func TestCompareExcludesInactiveHistory(t *testing.T) {
	current := model.PullRequestSnapshot{Number: 2, Title: "same approach", Files: []model.ChangedFile{{Filename: "a.go"}}}
	history := []model.Tombstone{{PR: model.PullRequestSnapshot{Number: 1, Title: "same approach", Files: []model.ChangedFile{{Filename: "a.go"}}}, State: model.StateArchivedAsMerged}}
	if matches := Compare(current, history); len(matches) != 0 {
		t.Fatalf("inactive Tombstone matched: %+v", matches)
	}
}

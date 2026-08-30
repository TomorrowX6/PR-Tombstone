package similarity

import (
	"strings"
	"testing"

	"pr-tombstone/internal/model"
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

func TestCompareExposesExplainableComponents(t *testing.T) {
	current := model.PullRequestSnapshot{Number: 22, Title: "Serialize Vulkan pipeline destruction", Body: "Use a global mutex for pipeline lifetime", Labels: []string{"vulkan"}, Files: []model.ChangedFile{{Filename: "GPU/Vulkan/VulkanRenderManager.cpp"}}}
	history := []model.Tombstone{{Repository: model.Repository{ID: 1}, PR: model.PullRequestSnapshot{Number: 11, Title: "Vulkan pipeline destruction mutex", Body: "Protect pipeline lifetime with a global mutex", Labels: []string{"vulkan"}, Files: []model.ChangedFile{{Filename: "GPU/Vulkan/VulkanRenderManager.cpp"}}}, State: model.StateActive, Summary: "global mutex introduces render-thread stalls"}}
	got := CompareWithSemantic(current, history, map[int]float64{11: 0.87})
	if len(got) != 1 {
		t.Fatalf("expected one match, got %d", len(got))
	}
	components := got[0].Components
	if components.Semantic == nil || *components.Semantic != 0.87 {
		t.Fatalf("semantic component = %v, want 0.87", components.Semantic)
	}
	if components.Files != 1 || components.Paths != 1 || components.Labels != 1 {
		t.Fatalf("structural components wrong: %+v", components)
	}
	if components.TitleBody <= 0 || components.Approach <= 0 {
		t.Fatalf("lexical components should be positive: %+v", components)
	}
	expectedScore := 0.30*0.87 + 0.25*1 + 0.15*1 + 0.15*components.Approach + 0.10*1 + 0.05*components.Symbols
	if got[0].Score < expectedScore-1e-9 || got[0].Score > expectedScore+1e-9 {
		t.Fatalf("score %v does not match weighted components %v", got[0].Score, expectedScore)
	}
	for _, fragment := range []string{"semantic 87%", "files 100%", "modules 100%", "title/body", "approach"} {
		if !strings.Contains(got[0].Reason, fragment) {
			t.Fatalf("reason %q missing %q", got[0].Reason, fragment)
		}
	}
}

func TestCompareDisclosesMissingSemanticScore(t *testing.T) {
	current := model.PullRequestSnapshot{Number: 22, Title: "Serialize Vulkan pipeline destruction", Body: "Use a global mutex for pipeline lifetime", Files: []model.ChangedFile{{Filename: "GPU/Vulkan/VulkanRenderManager.cpp"}}}
	history := []model.Tombstone{{Repository: model.Repository{ID: 1}, PR: model.PullRequestSnapshot{Number: 11, Title: "Vulkan pipeline destruction mutex", Body: "Protect pipeline lifetime with a global mutex", Files: []model.ChangedFile{{Filename: "GPU/Vulkan/VulkanRenderManager.cpp"}}}, State: model.StateActive, Summary: "global mutex introduces render-thread stalls"}}
	got := Compare(current, history)
	if len(got) != 1 {
		t.Fatalf("expected one match, got %d", len(got))
	}
	if got[0].Components.Semantic != nil {
		t.Fatal("semantic component must be nil when no provider score exists")
	}
	if !strings.Contains(got[0].Reason, "semantic unavailable") {
		t.Fatalf("reason must disclose the missing semantic score: %q", got[0].Reason)
	}
	if strings.Contains(got[0].Reason, "semantic approach match") {
		t.Fatalf("reason must not claim a semantic match without one: %q", got[0].Reason)
	}
}

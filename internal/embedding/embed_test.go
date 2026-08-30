package embedding

import (
	"context"
	"testing"
)

func TestLocalProviderIsDeterministicAndSized(t *testing.T) {
	provider := LocalProvider{}
	first, err := provider.Embed(context.Background(), "Vulkan pipeline lifetime")
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Embed(context.Background(), "Vulkan pipeline lifetime")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != Dimensions {
		t.Fatalf("got dimension %d", len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatal("local embedding is not deterministic")
		}
	}
}

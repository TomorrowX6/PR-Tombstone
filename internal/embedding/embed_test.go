package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if len(first) != LocalDimensions {
		t.Fatalf("got dimension %d", len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatal("local embedding is not deterministic")
		}
	}
}

func TestFitPadsShorterVectorsWithoutChangingCosineGeometry(t *testing.T) {
	a := normalize([]float32{1, -2, 3, 0.5})
	b := normalize([]float32{-0.5, 2, 1, 4})
	paddedA, err := Fit(a)
	if err != nil {
		t.Fatal(err)
	}
	paddedB, err := Fit(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(paddedA) != StorageDimensions {
		t.Fatalf("padded dimension = %d", len(paddedA))
	}
	for i := len(a); i < StorageDimensions; i++ {
		if paddedA[i] != 0 {
			t.Fatalf("tail must be zero-filled, got %v at %d", paddedA[i], i)
		}
	}
	if dot(a, b) != dot(paddedA, paddedB) {
		t.Fatal("zero-padding must preserve dot products")
	}
	if norm(a) != norm(paddedA) {
		t.Fatal("zero-padding must preserve norms")
	}
}

func TestFitNeverTruncates(t *testing.T) {
	oversize := make([]float32, StorageDimensions+1)
	if _, err := Fit(oversize); err == nil {
		t.Fatal("oversize provider vectors must error, not silently truncate")
	}
	exact := make([]float32, StorageDimensions)
	if _, err := Fit(exact); err != nil {
		t.Fatalf("exact-width vector must pass through: %v", err)
	}
}

func TestOpenAIProviderRequestsStorageDimensions(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"embedding":[%s]}]}`, strings.Repeat("0,", StorageDimensions-1)+"1")
	}))
	defer server.Close()
	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test-key", Model: "text-embedding-3-small"}
	if _, err := provider.Embed(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if request["dimensions"] != float64(StorageDimensions) {
		t.Fatalf("request dimensions = %v, want %d", request["dimensions"], StorageDimensions)
	}
}

func TestOpenAIProviderRejectsWrongWidth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"embedding":[%s]}]}`, strings.TrimRight(strings.Repeat("0,", 99)+"1", ","))
	}))
	defer server.Close()
	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test-key", Model: "legacy-model"}
	if _, err := provider.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("provider returning the wrong width must fail loudly")
	}
}

func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i] * b[i])
	}
	return sum
}

func norm(a []float32) float64 {
	var sum float64
	for _, value := range a {
		sum += float64(value * value)
	}
	return math.Sqrt(sum)
}

package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestClientRetriesRateLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "title": "retried"})
	}))
	defer server.Close()

	result, err := NewClient(server.URL, "TOKEN", server.Client()).GetPullRequest(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatal(err)
	}
	if result.Number != 42 || calls.Load() != 2 {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
}

func TestListClosedPullRequestsPaginates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		count := 1
		if page == 1 {
			count = 100
		}
		values := make([]map[string]any, count)
		for i := range values {
			values[i] = map[string]any{"number": (page-1)*100 + i + 1, "merged": false}
		}
		_ = json.NewEncoder(w).Encode(values)
	}))
	defer server.Close()

	numbers, err := NewClient(server.URL, "TOKEN", server.Client()).ListClosedPullRequestNumbers(context.Background(), "owner", "repo", 150)
	if err != nil {
		t.Fatal(err)
	}
	if len(numbers) != 101 || numbers[100] != 101 {
		t.Fatalf("unexpected numbers: len=%d tail=%v", len(numbers), numbers[len(numbers)-1])
	}
}

func TestGetRepositoryContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/contents/.github/CODEOWNERS" || r.URL.Query().Get("ref") != "main" {
			http.NotFound(w, r)
			return
		}
		content := []byte("/internal/ @maintainer\n")
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "file", "encoding": "base64", "size": len(content), "content": base64.StdEncoding.EncodeToString(content), "html_url": "https://example.invalid/context"})
	}))
	defer server.Close()

	body, source, err := NewClient(server.URL, "TOKEN", server.Client()).GetRepositoryContent(context.Background(), "owner", "repo", ".github/CODEOWNERS", "main")
	if err != nil {
		t.Fatal(err)
	}
	if body != "/internal/ @maintainer\n" || source == "" {
		t.Fatalf("body=%q source=%q", body, source)
	}
}

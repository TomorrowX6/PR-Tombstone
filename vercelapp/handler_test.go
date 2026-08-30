package vercelapp

import (
	"net/http/httptest"
	"testing"
)

func TestValidBearer(t *testing.T) {
	if !validBearer("Bearer secret-value", "secret-value") {
		t.Fatal("expected matching bearer token")
	}
	for _, header := range []string{"", "secret-value", "bearer secret-value", "Bearer wrong"} {
		if validBearer(header, "secret-value") {
			t.Fatalf("unexpected match for %q", header)
		}
	}
	if validBearer("Bearer anything", "") {
		t.Fatal("empty configured secret must never authenticate")
	}
}

func TestRestoreRewrittenPath(t *testing.T) {
	req := httptest.NewRequest("GET", "https://example.test/api/index?__vercel_path=/api/tombstones/42&q=mutex", nil)
	restored := restoreRewrittenPath(req)
	if restored.URL.Path != "/api/tombstones/42" {
		t.Fatalf("path = %q", restored.URL.Path)
	}
	if restored.URL.Query().Get("q") != "mutex" {
		t.Fatal("original query was not preserved")
	}
	if restored.URL.Query().Has("__vercel_path") {
		t.Fatal("internal routing query must be removed")
	}
	if req.URL.Path != "/api/index" {
		t.Fatal("input request was mutated")
	}
}

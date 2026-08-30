package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pr-tombstone/internal/config"
)

func TestSessionTokenHashDeterministic(t *testing.T) {
	hash := sessionTokenHash("token-value")
	if hash != sessionTokenHash("token-value") {
		t.Fatal("hash must be deterministic")
	}
	if len(hash) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(hash))
	}
	if sessionTokenHash("token-value") == sessionTokenHash("other") {
		t.Fatal("different tokens must not collide")
	}
}

func TestRandomTokenURLSafe(t *testing.T) {
	token, err := randomToken()
	if err != nil {
		t.Fatalf("randomToken: %v", err)
	}
	if len(token) != 43 {
		t.Fatalf("token length = %d, want 43 (32 bytes base64url)", len(token))
	}
	if strings.ContainsAny(token, "+/=") {
		t.Fatalf("token must be base64url-safe: %q", token)
	}
	other, _ := randomToken()
	if other == token {
		t.Fatal("successive tokens must differ")
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	server := &Server{Config: config.Config{PublicBaseURL: "https://pr.example.test"}}
	cookie := server.sessionCookie("token", fixedExpiry())
	if cookie.Name != sessionCookieName || cookie.Value != "token" {
		t.Fatalf("cookie = %+v", cookie)
	}
	if !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie hardening: %+v", cookie)
	}
	if !cookie.Secure {
		t.Fatal("Secure must be set for an https PublicBaseURL")
	}
	insecure := (&Server{Config: config.Config{PublicBaseURL: "http://localhost:5173"}}).sessionCookie("token", fixedExpiry())
	if insecure.Secure {
		t.Fatal("Secure must not be set for an http PublicBaseURL")
	}
}

func TestLegacyTokenMatches(t *testing.T) {
	server := &Server{Config: config.Config{DashboardToken: "shared-secret"}}
	request := func(authorization string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/repositories", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		return req
	}
	if !server.legacyTokenMatches(request("Bearer shared-secret")) {
		t.Fatal("correct bearer token must match")
	}
	if server.legacyTokenMatches(request("Bearer wrong")) {
		t.Fatal("wrong bearer token must not match")
	}
	if server.legacyTokenMatches(request("Basic shared-secret")) {
		t.Fatal("non-bearer scheme must not match")
	}
	if server.legacyTokenMatches(request("")) {
		t.Fatal("missing header must not match")
	}
	if (&Server{Config: config.Config{}}).legacyTokenMatches(request("Bearer shared-secret")) {
		t.Fatal("unconfigured token must never match")
	}
}

func TestAuthenticateOpenMode(t *testing.T) {
	server := &Server{Config: config.Config{}}
	handler := server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/repositories", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("open mode must pass through, got %d", recorder.Code)
	}
}

func TestAuthenticateTokenMode(t *testing.T) {
	server := &Server{Config: config.Config{DashboardToken: "shared-secret"}}
	handler := server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/repositories", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("token mode without bearer = %d, want 401", denied.Code)
	}
	allowed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/repositories", nil)
	request.Header.Set("Authorization", "Bearer shared-secret")
	handler.ServeHTTP(allowed, request)
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("token mode with bearer = %d, want 204", allowed.Code)
	}
}

func TestAuthenticateOAuthModeRequiresSession(t *testing.T) {
	server := &Server{Config: config.Config{OAuthClientID: "id", OAuthClientSecret: "secret"}}
	handler := server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/repositories", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("OAuth mode without session = %d, want 401", denied.Code)
	}
}

func TestAuthenticatePublicPathsBypassAuth(t *testing.T) {
	server := &Server{Config: config.Config{DashboardToken: "shared-secret", OAuthClientID: "id", OAuthClientSecret: "secret"}}
	handler := server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, path := range []string{"/api/healthz", "/livez", "/readyz", "/api/github/webhook", "/api/auth/login", "/api/auth/callback", "/api/auth/logout", "/api/auth/me"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("public path %s = %d, want 204", path, recorder.Code)
		}
	}
}

func TestOAuthLoginWithoutConfiguration(t *testing.T) {
	server := &Server{Config: config.Config{}}
	recorder := httptest.NewRecorder()
	server.oauthLogin(recorder, httptest.NewRequest(http.MethodGet, "/api/auth/login", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("login without OAuth config = %d, want 404", recorder.Code)
	}
}

func TestOAuthLogoutClearsSession(t *testing.T) {
	server := &Server{Config: config.Config{}}
	recorder := httptest.NewRecorder()
	server.oauthLogout(recorder, httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("logout = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"ok":true`) {
		t.Fatalf("logout body = %s", recorder.Body.String())
	}
	cleared := false
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout must clear the session cookie")
	}
}

func TestAuthStatusOpenMode(t *testing.T) {
	server := &Server{Config: config.Config{}}
	recorder := httptest.NewRecorder()
	server.authStatus(recorder, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("auth status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"mode":"open"`) || !strings.Contains(body, `"user":null`) {
		t.Fatalf("auth status body = %s", body)
	}
}

func fixedExpiry() time.Time { return time.Unix(4102444800, 0) }

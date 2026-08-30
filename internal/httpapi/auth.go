package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"pr-tombstone/internal/auth"
	"pr-tombstone/internal/model"
)

// OAuth login flow:
//
//	GET  /api/auth/login    → set short-lived state cookie, redirect to GitHub
//	GET  /api/auth/callback → verify state, exchange code, snapshot the user's
//	                          installations and repositories, create a session
//	POST /api/auth/logout   → drop the server-side session
//	GET  /api/auth/me       → report the resolved auth mode and current user
//
// The session cookie is HttpOnly and holds an opaque token; only its SHA-256
// hash is stored. The OAuth user token is never persisted anywhere.

const (
	sessionCookieName = "prt_session"
	stateCookieName   = "prt_oauth_state"
	stateCookieTTL    = 10 * time.Minute
)

func sessionTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *Server) sessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.Config.PublicBaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	}
}

func (s *Server) oauthFlow() auth.OAuth {
	return auth.OAuth{
		ClientID:     s.Config.OAuthClientID,
		ClientSecret: s.Config.OAuthClientSecret,
		APIBase:      s.Config.GitHubAPIBaseURL,
		WebBase:      s.Config.OAuthWebBaseURL(),
	}
}

func (s *Server) oauthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.Config.OAuthEnabled() {
		http.Error(w, "GitHub OAuth login is not configured", http.StatusNotFound)
		return
	}
	state, err := randomToken()
	if err != nil {
		http.Error(w, "generate state", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/api/auth",
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.Config.PublicBaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(stateCookieTTL / time.Second),
	})
	http.Redirect(w, r, s.oauthFlow().LoginURL(s.callbackURL(), state), http.StatusFound)
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.Config.OAuthEnabled() {
		http.Error(w, "GitHub OAuth login is not configured", http.StatusNotFound)
		return
	}
	stateCookie, err := r.Cookie(stateCookieName)
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	if err != nil || stateCookie.Value == "" || state == "" || stateCookie.Value != state {
		s.Logger.Warn("oauth callback state mismatch")
		http.Error(w, "OAuth state mismatch; restart login", http.StatusBadRequest)
		return
	}
	if code == "" {
		http.Error(w, "missing OAuth code", http.StatusBadRequest)
		return
	}
	flow := s.oauthFlow()
	token, err := flow.Exchange(r.Context(), code, s.callbackURL())
	if err != nil {
		s.Logger.Error("oauth exchange", "error", err)
		http.Error(w, "OAuth exchange failed", http.StatusBadGateway)
		return
	}
	profile, err := flow.User(r.Context(), token)
	if err != nil {
		s.Logger.Error("oauth user lookup", "error", err)
		http.Error(w, "OAuth user lookup failed", http.StatusBadGateway)
		return
	}
	installations, err := flow.Installations(r.Context(), token)
	if err != nil {
		s.Logger.Error("oauth installations lookup", "error", err)
		http.Error(w, "OAuth installations lookup failed", http.StatusBadGateway)
		return
	}
	for _, installationID := range installations {
		repositories, repoErr := flow.Repositories(r.Context(), token, installationID)
		if repoErr != nil {
			s.Logger.Error("oauth repositories lookup", "installation", installationID, "error", repoErr)
			http.Error(w, "OAuth repositories lookup failed", http.StatusBadGateway)
			return
		}
		if err := s.Store.UpsertInstallation(r.Context(), model.Installation{GitHubID: installationID}); err != nil {
			http.Error(w, "persist installation", http.StatusInternalServerError)
			return
		}
		for _, repo := range repositories {
			if _, err := s.Store.EnsureRepository(r.Context(), model.Repository{GitHubID: repo.GitHubID, InstallationID: installationID, Owner: repo.Owner, Name: repo.Name, Private: repo.Private}); err != nil {
				http.Error(w, "persist installation repository", http.StatusInternalServerError)
				return
			}
		}
	}
	user, err := s.Store.UpsertDashboardUser(r.Context(), profile.GitHubID, profile.Login, profile.Name, profile.AvatarURL)
	if err != nil {
		http.Error(w, "persist dashboard user", http.StatusInternalServerError)
		return
	}
	if err := s.Store.ReplaceUserInstallations(r.Context(), user.ID, installations); err != nil {
		http.Error(w, "refresh installation ACL", http.StatusInternalServerError)
		return
	}
	sessionToken, err := randomToken()
	if err != nil {
		http.Error(w, "create session", http.StatusInternalServerError)
		return
	}
	expires := time.Now().Add(s.Config.SessionTTL)
	if err := s.Store.CreateSession(r.Context(), user.ID, sessionTokenHash(sessionToken), expires); err != nil {
		http.Error(w, "persist session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, Value: "", Path: "/api/auth", MaxAge: -1})
	http.SetCookie(w, s.sessionCookie(sessionToken, expires))
	s.Logger.Info("dashboard login", "user", profile.Login, "installations", len(installations))
	http.Redirect(w, r, strings.TrimRight(s.Config.PublicBaseURL, "/")+"/", http.StatusFound)
}

func (s *Server) oauthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		_ = s.Store.DeleteSession(r.Context(), sessionTokenHash(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mode := "open"
	switch {
	case s.Config.OAuthEnabled():
		mode = "oauth"
	case s.Config.DashboardToken != "":
		mode = "token"
	}
	response := map[string]any{"mode": mode, "user": nil}
	if user := accessibleUser(r); user != nil {
		response["user"] = user
	} else {
		// /api/auth/me is public, so the session must be resolved here as well.
		if s.Config.OAuthEnabled() {
			if user, _, err := s.resolveSession(r); err == nil && user != nil {
				response["user"] = *user
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) callbackURL() string {
	return strings.TrimRight(s.Config.PublicBaseURL, "/") + "/api/auth/callback"
}

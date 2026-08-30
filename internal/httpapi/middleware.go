package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"pr-tombstone/internal/model"
	"pr-tombstone/internal/repository"
)

// Dashboard authentication modes resolved by authenticate:
//
//   - OAuth (GITHUB_OAUTH_CLIENT_ID/SECRET configured): requires a valid
//     server-side session cookie; every repository-scoped endpoint is then
//     restricted to the user's accessible GitHub App installations (ACL).
//   - Legacy token (DASHBOARD_TOKEN set, OAuth not configured): the shared
//     Bearer token, unrestricted. Documented self-host fallback only.
//   - Open (neither configured): fully open, single-user/self-host only.
type requestAuth struct {
	user          *model.DashboardUser
	installations []int64 // nil = unrestricted (legacy/open modes)
}

var authContextKey struct{}

var publicPaths = map[string]bool{
	"/api/healthz":        true,
	"/livez":              true,
	"/readyz":             true,
	"/api/github/webhook": true,
	"/api/github/install": true,
	"/api/github/setup":   true,
	"/api/auth/login":     true,
	"/api/auth/callback":  true,
	"/api/auth/logout":    true,
	"/api/auth/me":        true,
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		// A correct legacy Bearer token always stays valid so self-host
		// deployments that later adopt OAuth do not lose automation access.
		if s.legacyTokenMatches(r) {
			next.ServeHTTP(w, r)
			return
		}
		if s.Config.OAuthEnabled() {
			user, installations, err := s.resolveSession(r)
			if err != nil {
				s.Logger.Warn("resolve dashboard session", "error", err)
			}
			if user == nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="PR Tombstone"`)
				http.Error(w, "dashboard login required", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), &authContextKey, requestAuth{user: user, installations: installations})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if s.Config.DashboardToken != "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="PR Tombstone"`)
			http.Error(w, "dashboard authentication required", http.StatusUnauthorized)
			return
		}
		// Neither OAuth nor a dashboard token is configured: a single-user
		// self-host instance stays fully open.
		next.ServeHTTP(w, r)
	})
}

func (s *Server) legacyTokenMatches(r *http.Request) bool {
	if s.Config.DashboardToken == "" {
		return false
	}
	scheme, provided, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(s.Config.DashboardToken))
	return ok && strings.EqualFold(scheme, "Bearer") && subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}

func (s *Server) resolveSession(r *http.Request) (*model.DashboardUser, []int64, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, nil, nil
	}
	user, err := s.Store.SessionUser(r.Context(), sessionTokenHash(cookie.Value))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	installations, err := s.Store.UserInstallations(r.Context(), user.ID)
	if err != nil {
		return nil, nil, err
	}
	return &user, installations, nil
}

// accessibleInstallations returns the caller's installation ACL, or nil when
// the request is unrestricted (legacy token/open modes).
func accessibleInstallations(r *http.Request) []int64 {
	if auth, ok := r.Context().Value(&authContextKey).(requestAuth); ok && auth.user != nil {
		return auth.installations
	}
	return nil
}

// accessibleUser returns the authenticated dashboard user, if any.
func accessibleUser(r *http.Request) *model.DashboardUser {
	if auth, ok := r.Context().Value(&authContextKey).(requestAuth); ok {
		return auth.user
	}
	return nil
}

// requireRepositoryAccess enforces the installation ACL for a repository id.
func (s *Server) requireRepositoryAccess(w http.ResponseWriter, r *http.Request, repoID int64) bool {
	ids := accessibleInstallations(r)
	if ids == nil {
		return true
	}
	err := s.Store.CheckRepositoryAccess(r.Context(), repoID, ids)
	return writeAccessError(w, err, "repository not found")
}

// requireTombstoneAccess enforces the installation ACL for a tombstone id.
func (s *Server) requireTombstoneAccess(w http.ResponseWriter, r *http.Request, tombstoneID int64) bool {
	ids := accessibleInstallations(r)
	if ids == nil {
		return true
	}
	err := s.Store.CheckTombstoneAccess(r.Context(), tombstoneID, ids)
	return writeAccessError(w, err, "tombstone not found")
}

func writeAccessError(w http.ResponseWriter, err error, notFoundMessage string) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, notFoundMessage, http.StatusNotFound)
	case errors.Is(err, repository.ErrForbidden):
		http.Error(w, "the repository is outside your accessible installations", http.StatusForbidden)
	default:
		http.Error(w, "access check failed", http.StatusInternalServerError)
	}
	return false
}

package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pr-tombstone/internal/config"
	"pr-tombstone/internal/embedding"
	"pr-tombstone/internal/github"
	"pr-tombstone/internal/model"
	"pr-tombstone/internal/observability"
	"pr-tombstone/internal/repository"
	"pr-tombstone/internal/version"
	"pr-tombstone/internal/webhook"
)

type Server struct {
	Store   *repository.Store
	Config  config.Config
	Logger  *slog.Logger
	Embed   embedding.Provider
	Auth    *github.AppAuthenticator
	Metrics *observability.Metrics
}

type githubRepositoryPayload struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

type githubWebhook struct {
	Action       string `json:"action"`
	Installation *struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	} `json:"installation"`
	Repository          githubRepositoryPayload   `json:"repository"`
	Repositories        []githubRepositoryPayload `json:"repositories"`
	RepositoriesAdded   []githubRepositoryPayload `json:"repositories_added"`
	RepositoriesRemoved []githubRepositoryPayload `json:"repositories_removed"`
	PullRequest         struct {
		Number int  `json:"number"`
		Merged bool `json:"merged"`
	} `json:"pull_request"`
}

func (s *Server) Handler() http.Handler {
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	if s.Metrics == nil {
		s.Metrics = observability.New()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/healthz", s.health)
	mux.HandleFunc("/livez", s.live)
	mux.HandleFunc("/readyz", s.health)
	mux.HandleFunc("/metrics", s.metrics)
	mux.HandleFunc("/api/jobs", s.jobs)
	mux.HandleFunc("/api/github/install", s.githubInstall)
	mux.HandleFunc("/api/github/setup", s.githubSetup)
	mux.HandleFunc("/api/github/webhook", s.webhook)
	mux.HandleFunc("/api/repositories", s.repositories)
	mux.HandleFunc("/api/repositories/", s.repositories)
	mux.HandleFunc("/api/tombstones/", s.tombstones)
	mux.HandleFunc("/api/graph/", s.graph)
	mux.HandleFunc("/api/auth/login", s.oauthLogin)
	mux.HandleFunc("/api/auth/callback", s.oauthCallback)
	mux.HandleFunc("/api/auth/logout", s.oauthLogout)
	mux.HandleFunc("/api/auth/me", s.authStatus)
	return s.observe(cors(s.authenticate(mux)))
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive", "version": version.Version})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.DB.PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, err := s.Store.JobStats(r.Context(), accessibleInstallations(r))
	if err != nil {
		http.Error(w, "read job metrics", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_ = s.Metrics.WritePrometheus(w, stats)
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, err := s.Store.JobStats(r.Context(), accessibleInstallations(r))
	if err != nil {
		http.Error(w, "read jobs", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) githubInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Config.GitHubAppSlug == "" {
		http.Error(w, "GITHUB_APP_SLUG is not configured", http.StatusServiceUnavailable)
		return
	}
	// The slug is configuration, not a user-controlled URL. GitHub returns to
	// the configured public app URL with installation_id and setup_action.
	installURL := "https://github.com/apps/" + s.Config.GitHubAppSlug + "/installations/new"
	http.Redirect(w, r, installURL, http.StatusFound)
}

func (s *Server) githubSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	installationID, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if err != nil || installationID <= 0 {
		http.Error(w, "invalid installation_id", http.StatusBadRequest)
		return
	}
	action := r.URL.Query().Get("setup_action")
	if action == "" {
		action = "install"
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		target, err := url.Parse(s.Config.PublicBaseURL)
		if err != nil || target.Scheme == "" || target.Host == "" {
			http.Error(w, "PUBLIC_BASE_URL is invalid", http.StatusInternalServerError)
			return
		}
		target.Path = "/"
		query := target.Query()
		query.Set("installation_id", strconv.FormatInt(installationID, 10))
		query.Set("setup_action", action)
		target.RawQuery = query.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "installation_id": installationID, "setup_action": action})
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if !webhook.VerifySignature(s.Config.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	event := r.Header.Get("X-GitHub-Event")
	if delivery == "" || event == "" {
		http.Error(w, "missing GitHub headers", http.StatusBadRequest)
		return
	}
	var payload githubWebhook
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if event == "pull_request" && (payload.Installation == nil || payload.Installation.ID <= 0 || payload.Repository.ID <= 0 || payload.Repository.Name == "" || payload.Repository.Owner.Login == "" || payload.PullRequest.Number <= 0) {
		http.Error(w, "incomplete pull request payload", http.StatusBadRequest)
		return
	}
	if (event == "installation" || event == "installation_repositories") && (payload.Installation == nil || payload.Installation.ID <= 0) {
		http.Error(w, "missing installation", http.StatusBadRequest)
		return
	}
	created, err := s.Store.RecordDelivery(r.Context(), delivery, event, body)
	if err != nil {
		http.Error(w, "record delivery", http.StatusInternalServerError)
		return
	}
	if !created {
		s.Metrics.ObserveWebhookDuplicate()
		writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "duplicate": true})
		return
	}
	deliveryComplete := false
	defer func() {
		if deliveryComplete {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.Store.ReleaseDelivery(cleanupCtx, delivery); err != nil {
			s.Logger.Warn("release failed webhook delivery", "delivery_id", delivery, "error", err)
		}
	}()
	completeDelivery := func() error {
		if err := s.Store.CompleteDelivery(r.Context(), delivery); err != nil {
			return err
		}
		deliveryComplete = true
		return nil
	}
	if event == "installation" {
		installation := model.Installation{GitHubID: payload.Installation.ID, AccountLogin: payload.Installation.Account.Login, AccountType: payload.Installation.Account.Type}
		switch payload.Action {
		case "created", "unsuspend":
			err = s.Store.UpsertInstallation(r.Context(), installation)
			if err == nil {
				err = s.ensureRepositories(r.Context(), installation.GitHubID, payload.Repositories)
			}
		case "suspend":
			err = s.Store.UpsertInstallation(r.Context(), installation)
			if err == nil {
				err = s.Store.SuspendInstallation(r.Context(), installation.GitHubID, true)
			}
		case "deleted":
			err = s.Store.DeleteInstallation(r.Context(), installation.GitHubID)
		default:
			err = nil
		}
		if err != nil {
			http.Error(w, "process installation", http.StatusInternalServerError)
			return
		}
		if err := completeDelivery(); err != nil {
			http.Error(w, "complete delivery", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "installation_id": installation.GitHubID})
		return
	}
	if event == "installation_repositories" {
		installation := model.Installation{GitHubID: payload.Installation.ID, AccountLogin: payload.Installation.Account.Login, AccountType: payload.Installation.Account.Type}
		err = s.Store.UpsertInstallation(r.Context(), installation)
		if err == nil {
			err = s.ensureRepositories(r.Context(), installation.GitHubID, payload.RepositoriesAdded)
		}
		if err == nil {
			for _, repositoryPayload := range payload.RepositoriesRemoved {
				if deleteErr := s.Store.DeleteRepositoryByGitHubID(r.Context(), installation.GitHubID, repositoryPayload.ID); deleteErr != nil {
					err = deleteErr
					break
				}
			}
		}
		if err != nil {
			http.Error(w, "process installation repositories", http.StatusInternalServerError)
			return
		}
		if err := completeDelivery(); err != nil {
			http.Error(w, "complete delivery", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "added": len(payload.RepositoriesAdded), "removed": len(payload.RepositoriesRemoved)})
		return
	}
	if event != "pull_request" || payload.PullRequest.Number == 0 {
		if err := completeDelivery(); err != nil {
			http.Error(w, "complete delivery", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "queued": false})
		return
	}
	installationID := int64(0)
	if payload.Installation != nil {
		installationID = payload.Installation.ID
	}
	repoID, err := s.Store.EnsureRepository(r.Context(), model.Repository{GitHubID: payload.Repository.ID, InstallationID: installationID, Owner: payload.Repository.Owner.Login, Name: payload.Repository.Name, Private: payload.Repository.Private})
	if err != nil {
		http.Error(w, "save repository", http.StatusInternalServerError)
		return
	}
	switch payload.Action {
	case "closed":
		if payload.PullRequest.Merged {
			if err := s.Store.SetTombstoneState(r.Context(), repoID, payload.PullRequest.Number, model.StateArchivedAsMerged); err != nil {
				http.Error(w, "archive merged tombstone", http.StatusInternalServerError)
				return
			}
			if err := completeDelivery(); err != nil {
				http.Error(w, "complete delivery", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "queued": false, "reason": "merged"})
			return
		}
		err = s.Store.SetTombstoneState(r.Context(), repoID, payload.PullRequest.Number, model.StateActive)
		if err == nil {
			err = s.Store.Enqueue(r.Context(), repoID, installationID, payload.PullRequest.Number, "tombstone")
		}
	case "reopened":
		err = s.Store.SetTombstoneState(r.Context(), repoID, payload.PullRequest.Number, model.StateSuspended)
	case "opened", "synchronize":
		err = s.Store.Enqueue(r.Context(), repoID, installationID, payload.PullRequest.Number, "similarity")
	}
	if err != nil {
		http.Error(w, "queue event", http.StatusInternalServerError)
		return
	}
	if err := completeDelivery(); err != nil {
		http.Error(w, "complete delivery", http.StatusInternalServerError)
		return
	}
	queued := (payload.Action == "closed" && !payload.PullRequest.Merged) || payload.Action == "opened" || payload.Action == "synchronize"
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "queued": queued})
}

func (s *Server) ensureRepositories(ctx context.Context, installationID int64, repositories []githubRepositoryPayload) error {
	for _, repositoryPayload := range repositories {
		if repositoryPayload.ID <= 0 || repositoryPayload.Name == "" || repositoryPayload.Owner.Login == "" {
			continue
		}
		if _, err := s.Store.EnsureRepository(ctx, model.Repository{GitHubID: repositoryPayload.ID, InstallationID: installationID, Owner: repositoryPayload.Owner.Login, Name: repositoryPayload.Name, Private: repositoryPayload.Private}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) repositories(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/repositories/") {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/repositories/"), "/"), "/")
		if len(parts) == 2 && parts[1] == "settings" && (r.Method == http.MethodGet || r.Method == http.MethodPut) {
			s.settings(w, r, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "backfill" && r.Method == http.MethodPost {
			s.backfill(w, r, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "history" && r.Method == http.MethodGet {
			s.history(w, r, parts[0])
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	items, err := s.Store.ListRepositories(r.Context(), accessibleInstallations(r))
	if err != nil {
		http.Error(w, "list repositories", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"repositories": items})
}

func (s *Server) history(w http.ResponseWriter, r *http.Request, rawID string) {
	repoID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		http.Error(w, "invalid repository id", http.StatusBadRequest)
		return
	}
	if !s.requireRepositoryAccess(w, r, repoID) {
		return
	}
	items, err := s.Store.ListOpenPRHistory(r.Context(), repoID)
	if err != nil {
		http.Error(w, "read new PR history", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pull_requests": items})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request, rawID string) {
	repoID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		http.Error(w, "invalid repository id", http.StatusBadRequest)
		return
	}
	if !s.requireRepositoryAccess(w, r, repoID) {
		return
	}
	if _, err := s.Store.GetRepository(r.Context(), repoID); err != nil {
		http.Error(w, "repository not found", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		settings, err := s.Store.GetSettings(r.Context(), repoID)
		if err != nil {
			http.Error(w, "read settings", 500)
			return
		}
		writeJSON(w, 200, settings)
		return
	}
	var settings model.RepositorySettings
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&settings); err != nil {
		http.Error(w, "invalid settings", 400)
		return
	}
	settings.RepositoryID = repoID
	if err := s.Store.UpdateSettings(r.Context(), settings); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rawID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/graph/"), "/")
	if !strings.HasPrefix(rawID, "repository/") {
		http.NotFound(w, r)
		return
	}
	repoID, err := strconv.ParseInt(strings.TrimPrefix(rawID, "repository/"), 10, 64)
	if err != nil {
		http.Error(w, "invalid repository id", http.StatusBadRequest)
		return
	}
	if !s.requireRepositoryAccess(w, r, repoID) {
		return
	}
	graph, err := s.Store.DecisionGraph(r.Context(), repoID)
	if err != nil {
		http.Error(w, "read decision graph", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, graph)
}

func (s *Server) backfill(w http.ResponseWriter, r *http.Request, rawID string) {
	repoID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		http.Error(w, "invalid repository id", http.StatusBadRequest)
		return
	}
	if !s.requireRepositoryAccess(w, r, repoID) {
		return
	}
	repo, err := s.Store.GetRepository(r.Context(), repoID)
	if err != nil {
		http.Error(w, "repository not found", http.StatusNotFound)
		return
	}
	limit := 50
	var since *time.Time
	scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
	switch scope {
	case "100":
		limit = 100
	case "year":
		limit = 3000
		value := time.Now().UTC().AddDate(-1, 0, 0)
		since = &value
	case "all":
		limit = 3000
	case "", "50", "custom":
	default:
		http.Error(w, "scope must be 50, 100, year, all, or custom", http.StatusBadRequest)
		return
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 3000 {
		limit = 3000
	}
	if s.Auth == nil {
		http.Error(w, "GitHub App credentials are not configured", http.StatusServiceUnavailable)
		return
	}
	operationCtx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	token, err := s.Auth.InstallationToken(operationCtx, repo.InstallationID)
	if err != nil {
		http.Error(w, "GitHub installation token", http.StatusBadGateway)
		return
	}
	numbers, err := github.NewClient(s.Config.GitHubAPIBaseURL, token, nil).ListClosedPullRequestNumbersSince(operationCtx, repo.Owner, repo.Name, limit, since)
	if err != nil {
		http.Error(w, "GitHub history import", http.StatusBadGateway)
		return
	}
	queued := 0
	for _, number := range numbers {
		created, err := s.Store.EnqueueUnique(operationCtx, repo.ID, repo.InstallationID, number, "tombstone")
		if err != nil {
			http.Error(w, "queue backfill", http.StatusInternalServerError)
			return
		}
		if created {
			queued++
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": queued, "eligible": len(numbers), "requested": limit, "scope": firstNonEmpty(scope, "50"), "api_cap_reached": len(numbers) == 3000})
}

func (s *Server) tombstones(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tombstones/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if parts[0] == "repository" && len(parts) >= 2 {
		repoID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			http.Error(w, "invalid repository id", 400)
			return
		}
		if !s.requireRepositoryAccess(w, r, repoID) {
			return
		}
		if len(parts) == 2 && r.Method == http.MethodGet {
			query := r.URL.Query().Get("q")
			limit := queryInt(r, "limit", 100, 1, 3000)
			offset := queryInt(r, "offset", 0, 0, 1000000)
			var items []model.Tombstone
			if query != "" {
				items, err = s.Store.SearchTombstonesPage(r.Context(), repoID, query, limit+1, offset)
				if err == nil && s.Embed != nil && offset == 0 {
					if vector, embedErr := s.Embed.Embed(r.Context(), query); embedErr == nil {
						if semantic, semanticErr := s.Store.SearchSemantic(r.Context(), repoID, vector, 20); semanticErr == nil && len(semantic) > 0 {
							items = mergeTombstones(items, semantic, limit+1)
						}
					}
				}
			} else {
				items, err = s.Store.ListTombstonesPage(r.Context(), repoID, limit+1, offset)
			}
			if err != nil {
				http.Error(w, "list tombstones", 500)
				return
			}
			hasMore := len(items) > limit
			if hasMore {
				items = items[:limit]
			}
			for i := range items {
				items[i].Evidence = nil
				items[i].PR.Evidence = nil
				for j := range items[i].PR.Files {
					items[i].PR.Files[j].Patch = ""
				}
			}
			writeJSON(w, 200, map[string]any{"tombstones": items, "limit": limit, "offset": offset, "has_more": hasMore})
			return
		}
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "invalid tombstone id", 400)
		return
	}
	if !s.requireTombstoneAccess(w, r, id) {
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		item, err := s.Store.GetTombstone(r.Context(), id)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			http.Error(w, "tombstone not found", 404)
			return
		}
		item.Evidence, _ = s.Store.ListEvidence(r.Context(), item.Repository.ID, item.PR.Number)
		writeJSON(w, 200, item)
		return
	}
	if len(parts) == 2 && parts[1] == "related" && r.Method == http.MethodGet {
		item, err := s.Store.GetTombstone(r.Context(), id)
		if err != nil {
			http.Error(w, "tombstone not found", http.StatusNotFound)
			return
		}
		matches, err := s.Store.ListSimilarityMatches(r.Context(), item.Repository.ID, item.PR.Number)
		if err != nil {
			http.Error(w, "list related history", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"matches": matches})
		return
	}
	if len(parts) == 2 && parts[1] == "state" && r.Method == http.MethodPut {
		var request struct {
			State model.TombstoneState `json:"state"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		if err := s.Store.SetTombstoneStateByID(r.Context(), id, request.State); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "tombstone not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "state": request.State})
		return
	}
	if len(parts) == 2 && parts[1] == "reanalyze" && r.Method == http.MethodPost {
		item, err := s.Store.GetTombstone(r.Context(), id)
		if err != nil {
			http.Error(w, "tombstone not found", 404)
			return
		}
		if err := s.Store.Enqueue(r.Context(), item.Repository.ID, item.Repository.InstallationID, item.PR.Number, "tombstone"); err != nil {
			http.Error(w, "queue reanalysis", 500)
			return
		}
		writeJSON(w, 202, map[string]any{"queued": true})
		return
	}
	http.NotFound(w, r)
}

func mergeTombstones(primary, secondary []model.Tombstone, limit int) []model.Tombstone {
	if limit < 1 {
		limit = 100
	}
	out := make([]model.Tombstone, 0, min(limit, len(primary)+len(secondary)))
	seen := make(map[int64]bool)
	for _, group := range [][]model.Tombstone{primary, secondary} {
		for _, item := range group {
			if seen[item.ID] || len(out) >= limit {
				continue
			}
			seen[item.ID] = true
			out = append(out, item)
		}
	}
	return out
}

func queryInt(r *http.Request, key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || value < minimum {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := make([]byte, 12)
		if _, err := rand.Read(requestID); err != nil {
			requestID = []byte(strconv.FormatInt(started.UnixNano(), 10))
		}
		id := hex.EncodeToString(requestID)
		w.Header().Set("X-Request-ID", id)
		wrapped := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		if wrapped.status == 0 {
			wrapped.status = http.StatusOK
		}
		s.Metrics.ObserveRequest(wrapped.status, r.URL.Path == "/api/github/webhook")
		s.Logger.Info("http request", "request_id", id, "method", r.Method, "path", r.URL.Path, "status", wrapped.status, "duration_ms", time.Since(started).Milliseconds())
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-GitHub-Event, X-GitHub-Delivery, X-Hub-Signature-256")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pr-tombstone/internal/model"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// APIError preserves the HTTP status so callers can distinguish an optional
// resource (for example CODEOWNERS) from a hard GitHub API failure.
type APIError struct {
	Path       string
	StatusCode int
	Status     string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github api %s: %s: %s", e.Path, e.Status, e.Body)
}

func IsNotFound(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.StatusCode == http.StatusNotFound
}

func NewClient(baseURL, token string, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: client}
}

type pullRequestResponse struct {
	Number            int          `json:"number"`
	Title             string       `json:"title"`
	Body              *string      `json:"body"`
	User              userResponse `json:"user"`
	AuthorAssociation string       `json:"author_association"`
	CreatedAt         time.Time    `json:"created_at"`
	ClosedAt          *time.Time   `json:"closed_at"`
	Base              struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Merged   bool       `json:"merged"`
	MergedAt *time.Time `json:"merged_at"`
	Labels   []struct {
		Name string `json:"name"`
	} `json:"labels"`
	HTMLURL string `json:"html_url"`
}

func (c *Client) ListClosedPullRequestNumbers(ctx context.Context, owner, repo string, limit int) ([]int, error) {
	return c.ListClosedPullRequestNumbersSince(ctx, owner, repo, limit, nil)
}

func (c *Client) ListClosedPullRequestNumbersSince(ctx context.Context, owner, repo string, limit int, since *time.Time) ([]int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 3000 {
		limit = 3000
	}
	perPage := 100
	values, err := listPages[pullRequestResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls?state=closed&sort=updated&direction=desc", url.PathEscape(owner), url.PathEscape(repo)), perPage, (limit+perPage-1)/perPage)
	if err != nil {
		return nil, err
	}
	if len(values) > limit {
		values = values[:limit]
	}
	numbers := make([]int, 0, len(values))
	for _, value := range values {
		if since != nil && (value.ClosedAt == nil || value.ClosedAt.Before(*since)) {
			continue
		}
		if value.MergedAt == nil && !value.Merged {
			numbers = append(numbers, value.Number)
		}
	}
	return numbers, nil
}

type userResponse struct {
	Login string `json:"login"`
}

type fileResponse struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
	Patch     string `json:"patch"`
	BlobURL   string `json:"blob_url"`
}

type commitResponse struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  *struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commit"`
}

type reviewResponse struct {
	ID                int64        `json:"id"`
	User              userResponse `json:"user"`
	State             string       `json:"state"`
	Body              string       `json:"body"`
	AuthorAssociation string       `json:"author_association"`
	SubmittedAt       *time.Time   `json:"submitted_at"`
	HTMLURL           string       `json:"html_url"`
}

type commentResponse struct {
	ID                int64        `json:"id"`
	User              userResponse `json:"user"`
	Body              string       `json:"body"`
	CreatedAt         time.Time    `json:"created_at"`
	HTMLURL           string       `json:"html_url"`
	Path              string       `json:"path"`
	Line              *int         `json:"line"`
	AuthorAssociation string       `json:"author_association"`
}

type timelineResponse struct {
	ID        int64        `json:"id"`
	Event     string       `json:"event"`
	Actor     userResponse `json:"actor"`
	CreatedAt time.Time    `json:"created_at"`
	CommitID  string       `json:"commit_id"`
	Body      string       `json:"body"`
	HTMLURL   string       `json:"html_url"`
}

func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, number int) (pullRequestResponse, error) {
	var result pullRequestResponse
	err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), number), &result)
	return result, err
}

func (c *Client) FetchSnapshot(ctx context.Context, owner, repo string, number int, repositoryID int64) (model.PullRequestSnapshot, error) {
	pr, err := c.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return model.PullRequestSnapshot{}, err
	}

	files, err := c.listFiles(ctx, owner, repo, number)
	if err != nil {
		return model.PullRequestSnapshot{}, err
	}
	commits, err := c.listCommits(ctx, owner, repo, number)
	if err != nil {
		return model.PullRequestSnapshot{}, err
	}
	reviews, err := c.listReviews(ctx, owner, repo, number)
	if err != nil {
		return model.PullRequestSnapshot{}, err
	}
	reviewComments, err := c.listReviewComments(ctx, owner, repo, number)
	if err != nil {
		return model.PullRequestSnapshot{}, err
	}
	issueComments, err := c.listIssueComments(ctx, owner, repo, number)
	if err != nil {
		return model.PullRequestSnapshot{}, err
	}
	timeline, err := c.listTimeline(ctx, owner, repo, number)
	if err != nil {
		return model.PullRequestSnapshot{}, err
	}

	body := ""
	if pr.Body != nil {
		body = *pr.Body
	}
	labels := make([]string, 0, len(pr.Labels))
	for _, label := range pr.Labels {
		labels = append(labels, label.Name)
	}
	snapshot := model.PullRequestSnapshot{
		RepositoryID: repositoryID, Number: pr.Number, Title: pr.Title, Body: body,
		Author: pr.User.Login, AuthorAssociation: pr.AuthorAssociation,
		CreatedAt: pr.CreatedAt, ClosedAt: pr.ClosedAt, BaseBranch: pr.Base.Ref,
		HeadBranch: pr.Head.Ref, Merged: pr.Merged, Labels: labels,
	}
	for _, f := range files {
		snapshot.Files = append(snapshot.Files, model.ChangedFile{Filename: f.Filename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Changes: f.Changes, Patch: f.Patch})
	}
	for _, commit := range commits {
		author := ""
		if commit.Commit.Author != nil {
			author = commit.Commit.Author.Name
		}
		snapshot.Commits = append(snapshot.Commits, model.Commit{SHA: commit.SHA, Message: commit.Commit.Message, Author: author})
	}
	for _, review := range reviews {
		snapshot.Reviews = append(snapshot.Reviews, model.Review{ID: review.ID, State: review.State, Body: review.Body, Reviewer: review.User.Login, AuthorAssociation: review.AuthorAssociation, SubmittedAt: review.SubmittedAt})
	}
	for _, commit := range commits {
		if strings.TrimSpace(commit.Commit.Message) == "" {
			continue
		}
		author := ""
		if commit.Commit.Author != nil {
			author = commit.Commit.Author.Name
		}
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceItem{ID: "commit:" + commit.SHA, RepositoryID: repositoryID, PRNumber: number, Type: "commit", Author: author, Body: commit.Commit.Message, SourceURL: commit.HTMLURL, CreatedAt: pr.CreatedAt})
	}

	snapshot.Evidence = append(snapshot.Evidence, model.EvidenceItem{ID: fmt.Sprintf("pr_body:%d", number), RepositoryID: repositoryID, PRNumber: number, Type: "pr_body", Author: pr.User.Login, AuthorAssociation: pr.AuthorAssociation, Body: body, SourceURL: pr.HTMLURL, CreatedAt: pr.CreatedAt})
	for _, review := range reviews {
		if strings.TrimSpace(review.Body) == "" {
			continue
		}
		created := time.Time{}
		if review.SubmittedAt != nil {
			created = *review.SubmittedAt
		}
		body := strings.TrimSpace(review.State + "\n" + review.Body)
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceItem{ID: fmt.Sprintf("review:%d", review.ID), RepositoryID: repositoryID, PRNumber: number, Type: "review", Author: review.User.Login, AuthorAssociation: review.AuthorAssociation, Body: body, SourceURL: review.HTMLURL, CreatedAt: created})
	}
	for _, comment := range reviewComments {
		line := 0
		if comment.Line != nil {
			line = *comment.Line
		}
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceItem{ID: fmt.Sprintf("review_comment:%d", comment.ID), RepositoryID: repositoryID, PRNumber: number, Type: "review_comment", Author: comment.User.Login, AuthorAssociation: comment.AuthorAssociation, Path: comment.Path, Line: line, Body: comment.Body, SourceURL: comment.HTMLURL, CreatedAt: comment.CreatedAt})
	}
	for _, comment := range issueComments {
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceItem{ID: fmt.Sprintf("issue_comment:%d", comment.ID), RepositoryID: repositoryID, PRNumber: number, Type: "issue_comment", Author: comment.User.Login, AuthorAssociation: comment.AuthorAssociation, Body: comment.Body, SourceURL: comment.HTMLURL, CreatedAt: comment.CreatedAt})
	}
	for _, item := range timeline {
		body := item.Event
		if item.Body != "" {
			body += ": " + item.Body
		}
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceItem{ID: fmt.Sprintf("timeline:%d", item.ID), RepositoryID: repositoryID, PRNumber: number, Type: "timeline", Author: item.Actor.Login, Body: body, SourceURL: item.HTMLURL, CreatedAt: item.CreatedAt})
	}
	for _, file := range snapshot.Files {
		if strings.TrimSpace(file.Patch) == "" {
			continue
		}
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceItem{ID: "diff:" + file.Filename, RepositoryID: repositoryID, PRNumber: number, Type: "diff", Path: file.Filename, Body: file.Patch, SourceURL: pr.HTMLURL, CreatedAt: pr.CreatedAt})
	}
	return snapshot, nil
}

func (c *Client) listFiles(ctx context.Context, owner, repo string, number int) ([]fileResponse, error) {
	return listPages[fileResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/files", url.PathEscape(owner), url.PathEscape(repo), number), 100, 30)
}
func (c *Client) listCommits(ctx context.Context, owner, repo string, number int) ([]commitResponse, error) {
	return listPages[commitResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/commits", url.PathEscape(owner), url.PathEscape(repo), number), 100, 30)
}
func (c *Client) listReviews(ctx context.Context, owner, repo string, number int) ([]reviewResponse, error) {
	return listPages[reviewResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", url.PathEscape(owner), url.PathEscape(repo), number), 100, 30)
}
func (c *Client) listReviewComments(ctx context.Context, owner, repo string, number int) ([]commentResponse, error) {
	return listPages[commentResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/comments", url.PathEscape(owner), url.PathEscape(repo), number), 100, 30)
}
func (c *Client) listIssueComments(ctx context.Context, owner, repo string, number int) ([]commentResponse, error) {
	return listPages[commentResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", url.PathEscape(owner), url.PathEscape(repo), number), 100, 30)
}
func (c *Client) listTimeline(ctx context.Context, owner, repo string, number int) ([]timelineResponse, error) {
	return listPages[timelineResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/issues/%d/timeline", url.PathEscape(owner), url.PathEscape(repo), number), 100, 30)
}

func listPages[T any](ctx context.Context, c *Client, path string, perPage, maxPages int) ([]T, error) {
	var all []T
	for page := 1; page <= maxPages; page++ {
		var values []T
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		if err := c.getJSON(ctx, path+separator+"per_page="+strconv.Itoa(perPage)+"&page="+strconv.Itoa(page), &values); err != nil {
			return nil, err
		}
		all = append(all, values...)
		if len(values) < perPage {
			break
		}
	}
	return all, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	const maxAttempts = 4
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return err
		}
		c.setHeaders(req)
		resp, err := c.http.Do(req)
		if err != nil {
			if attempt+1 == maxAttempts {
				return err
			}
			if err := waitContext(ctx, backoff(attempt)); err != nil {
				return err
			}
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if len(data) == 0 {
				return nil
			}
			return json.Unmarshal(data, out)
		}
		if attempt+1 < maxAttempts && retryable(resp) {
			if err := waitContext(ctx, retryDelay(resp, attempt)); err != nil {
				return err
			}
			continue
		}
		return &APIError{Path: path, StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(data))}
	}
	return fmt.Errorf("github api %s: retry budget exhausted", path)
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func retryable(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout {
		return true
	}
	return resp.StatusCode == http.StatusForbidden && (resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0")
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if raw := strings.TrimSpace(resp.Header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			return clampDelay(time.Duration(seconds) * time.Second)
		}
		if retryAt, err := http.ParseTime(raw); err == nil {
			return clampDelay(time.Until(retryAt))
		}
	}
	if raw := strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset")); raw != "" && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return clampDelay(time.Until(time.Unix(unix, 0)) + time.Second)
		}
	}
	return backoff(attempt)
}

func backoff(attempt int) time.Duration {
	return time.Duration(250*(1<<min(attempt, 5))) * time.Millisecond
}

func clampDelay(delay time.Duration) time.Duration {
	if delay < 0 {
		return 0
	}
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type contentResponse struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	HTMLURL  string `json:"html_url"`
	Size     int    `json:"size"`
}

// GetRepositoryContent reads a small text file through the Contents API. It
// intentionally rejects directories, non-base64 payloads, and large files so
// optional repository context cannot turn into an unbounded model prompt.
func (c *Client) GetRepositoryContent(ctx context.Context, owner, repo, path, ref string) (string, string, error) {
	escapedPath := make([]string, 0)
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part != "" {
			escapedPath = append(escapedPath, url.PathEscape(part))
		}
	}
	endpoint := fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), strings.Join(escapedPath, "/"))
	if ref != "" {
		endpoint += "?ref=" + url.QueryEscape(ref)
	}
	var response contentResponse
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return "", "", err
	}
	if response.Type != "file" || response.Encoding != "base64" {
		return "", response.HTMLURL, fmt.Errorf("github content %s is not a base64 file", path)
	}
	if response.Size > 256<<10 {
		return "", response.HTMLURL, fmt.Errorf("github content %s exceeds 256 KiB", path)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil {
		return "", response.HTMLURL, fmt.Errorf("decode github content %s: %w", path, err)
	}
	return string(decoded), response.HTMLURL, nil
}

func (c *Client) CreateCheckRun(ctx context.Context, owner, repo, name, headSHA, conclusion, title, summary string) error {
	if conclusion != "success" && conclusion != "neutral" {
		conclusion = "neutral"
	}
	body, err := json.Marshal(map[string]any{
		"name":       name,
		"head_sha":   headSHA,
		"status":     "completed",
		"conclusion": conclusion,
		"output":     map[string]string{"title": title, "summary": summary},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+fmt.Sprintf("/repos/%s/%s/check-runs", url.PathEscape(owner), url.PathEscape(repo)), strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github check run: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

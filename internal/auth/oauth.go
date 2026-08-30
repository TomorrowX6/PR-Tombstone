// Package auth implements dashboard authentication: the GitHub OAuth web
// flow used for login plus the user-token profile and installation lookups
// that build the user-to-installation ACL snapshot.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// OAuth implements the GitHub OAuth web application flow used for dashboard
// login. The flow is driven with the GitHub App's OAuth client credentials;
// the resulting user token authorizes profile and installation reads. The
// token itself is never persisted — access snapshots refresh on every login.
type OAuth struct {
	ClientID, ClientSecret string
	APIBase, WebBase       string
	HTTP                   *http.Client
}

// UserProfile is the GitHub account identity resolved during login.
type UserProfile struct {
	GitHubID  int64
	Login     string
	Name      string
	AvatarURL string
}

// LoginURL builds the GitHub authorize redirect for the web flow.
func (o OAuth) LoginURL(redirectURI, state string) string {
	query := url.Values{
		"client_id":    {o.ClientID},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	return strings.TrimRight(o.WebBase, "/") + "/login/oauth/authorize?" + query.Encode()
}

// Exchange trades the authorization code for a user access token.
func (o OAuth) Exchange(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{
		"client_id":     {o.ClientID},
		"client_secret": {o.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.WebBase, "/")+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := o.do(req, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		if out.Error != "" {
			return "", fmt.Errorf("oauth exchange: %s: %s", out.Error, out.Description)
		}
		return "", errors.New("oauth exchange returned no access token")
	}
	return out.AccessToken, nil
}

// User resolves the account identity of a user token.
func (o OAuth) User(ctx context.Context, token string) (UserProfile, error) {
	var profile struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := o.get(ctx, "/user", token, &profile); err != nil {
		return UserProfile{}, err
	}
	if profile.ID == 0 || profile.Login == "" {
		return UserProfile{}, errors.New("oauth user lookup returned no identity")
	}
	return UserProfile{GitHubID: profile.ID, Login: profile.Login, Name: profile.Name, AvatarURL: profile.AvatarURL}, nil
}

// Installations lists the GitHub App installation IDs visible to a user
// token. These ids become the user's repository ACL. The endpoint is
// paginated at 100 entries; all pages are folded into one snapshot.
func (o OAuth) Installations(ctx context.Context, token string) ([]int64, error) {
	ids := make([]int64, 0)
	for page := 1; ; page++ {
		var response struct {
			Installations []struct {
				ID int64 `json:"id"`
			} `json:"installations"`
		}
		if err := o.get(ctx, "/user/installations?per_page=100&page="+strconv.Itoa(page), token, &response); err != nil {
			return nil, err
		}
		for _, installation := range response.Installations {
			ids = append(ids, installation.ID)
		}
		if len(response.Installations) < 100 {
			return ids, nil
		}
	}
}

func (o OAuth) get(ctx context.Context, path, token string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(o.APIBase, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return o.do(req, into)
}

func (o OAuth) do(req *http.Request, into any) error {
	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("oauth: %s %s: %s", req.Method, req.URL.Path, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(into)
}

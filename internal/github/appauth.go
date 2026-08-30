package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type AppAuthenticator struct {
	appID      int64
	privateKey *rsa.PrivateKey
	baseURL    string
	client     *http.Client
	mu         sync.Mutex
	tokens     map[int64]cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

func NewAppAuthenticator(appID int64, privateKeyPEM, baseURL string, client *http.Client) (*AppAuthenticator, error) {
	if appID == 0 || strings.TrimSpace(privateKeyPEM) == "" {
		return nil, errors.New("github app id and private key are required")
	}
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, `\n`, "\n")
	key, err := parsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &AppAuthenticator{appID: appID, privateKey: key, baseURL: strings.TrimRight(baseURL, "/"), client: client, tokens: make(map[int64]cachedToken)}, nil
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("github private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse github private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github private key is not RSA")
	}
	return rsaKey, nil
}

func (a *AppAuthenticator) JWT() (string, error) {
	now := time.Now().UTC()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.appID,
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	message := header + "." + payload
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, a.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign github app jwt: %w", err)
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (a *AppAuthenticator) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	a.mu.Lock()
	if cached, ok := a.tokens[installationID]; ok && time.Until(cached.expiresAt) > 2*time.Minute {
		a.mu.Unlock()
		return cached.token, nil
	}
	a.mu.Unlock()

	jwt, err := a.JWT()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/app/installations/%d/access_tokens", a.baseURL, installationID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("github installation token: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode installation token: %w", err)
	}
	if result.Token == "" {
		return "", errors.New("github returned an empty installation token")
	}
	a.mu.Lock()
	a.tokens[installationID] = cachedToken{token: result.Token, expiresAt: result.ExpiresAt}
	a.mu.Unlock()
	return result.Token, nil
}

package embedding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	// LocalDimensions is the output width of the deterministic offline
	// provider. It exists for fixtures and tests, not for production semantics.
	LocalDimensions = 64
	// StorageDimensions is the width of the tombstone_embeddings vector
	// columns. Production providers must return exactly this width;
	// OpenAI-compatible providers request it explicitly through the
	// dimensions parameter instead of truncating a wider native vector.
	StorageDimensions = 1536
)

type Provider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type Set struct {
	Title       []float32
	Description []float32
	Discussion  []float32
	Approach    []float32
}

// LocalProvider is deterministic, offline, and suitable for fixtures. It is
// not intended to replace a semantic model in production.
type LocalProvider struct{}

func (LocalProvider) Embed(_ context.Context, text string) ([]float32, error) {
	vector := make([]float32, LocalDimensions)
	for _, token := range strings.Fields(strings.ToLower(text)) {
		digest := sha256.Sum256([]byte(token))
		for i := 0; i < 4; i++ {
			index := int(digest[i]) % LocalDimensions
			sign := float32(1)
			if digest[i+4]&1 == 1 {
				sign = -1
			}
			vector[index] += sign
		}
	}
	return normalize(vector), nil
}

type OpenAICompatibleProvider struct {
	BaseURL, APIKey, Model string
	HTTPClient             *http.Client
}

func (p OpenAICompatibleProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if p.BaseURL == "" || p.APIKey == "" || p.Model == "" {
		return nil, errors.New("embedding provider requires AI_BASE_URL, AI_API_KEY and AI_MODEL")
	}
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	// Request the storage width explicitly instead of accepting the model's
	// native width and truncating it, which would discard most of the vector's
	// semantics. Providers that cannot honor dimensions fail loudly below.
	body, _ := json.Marshal(map[string]any{"model": p.Model, "input": text, "dimensions": StorageDimensions})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("embedding provider: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var decoded struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
		return nil, errors.New("embedding provider returned no vector")
	}
	// The provider was asked for exactly the storage width. Accepting a
	// different width and padding or truncating it would hide a model that
	// ignores the dimensions parameter, so fail loudly instead.
	if got := len(decoded.Data[0].Embedding); got != StorageDimensions {
		return nil, fmt.Errorf("embedding provider %q returned %d dimensions, expected %d (configure a model that honors the dimensions parameter)", p.Model, got, StorageDimensions)
	}
	return decoded.Data[0].Embedding, nil
}

func New(provider, baseURL, key, model string) Provider {
	if strings.EqualFold(provider, "local") || strings.EqualFold(provider, "rules") || baseURL == "" {
		return LocalProvider{}
	}
	return OpenAICompatibleProvider{BaseURL: baseURL, APIKey: key, Model: model}
}

func normalize(vector []float32) []float32 {
	var sum float64
	for _, value := range vector {
		sum += float64(value * value)
	}
	if sum == 0 {
		return vector
	}
	scale := float32(1 / math.Sqrt(sum))
	for i := range vector {
		vector[i] *= scale
	}
	return vector
}

// Fit adapts a provider vector to the storage width. Vectors shorter than the
// storage width are zero-padded, which preserves cosine distance exactly
// (dot products and norms are unchanged). Longer vectors are never silently
// truncated, because truncating e.g. 1536 dimensions to 64 discards most of a
// real model's semantics; Fit returns an error instead so misconfiguration
// surfaces immediately.
func Fit(vector []float32) ([]float32, error) {
	switch {
	case len(vector) == StorageDimensions:
		return vector, nil
	case len(vector) > StorageDimensions:
		return nil, fmt.Errorf("embedding vector has %d dimensions, storage supports at most %d", len(vector), StorageDimensions)
	}
	padded := make([]float32, StorageDimensions)
	copy(padded, vector)
	return padded, nil
}
func Encode(vector []float32) string {
	parts := make([]string, len(vector))
	for i, value := range vector {
		parts[i] = fmt.Sprintf("%.8f", value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
func Digest(vector []float32) string {
	digest := sha256.Sum256([]byte(Encode(vector)))
	return hex.EncodeToString(digest[:])
}

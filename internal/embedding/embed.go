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

const Dimensions = 64

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
	vector := make([]float32, Dimensions)
	for _, token := range strings.Fields(strings.ToLower(text)) {
		digest := sha256.Sum256([]byte(token))
		for i := 0; i < 4; i++ {
			index := int(digest[i]) % Dimensions
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
	body, _ := json.Marshal(map[string]string{"model": p.Model, "input": text})
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
	return Fit(decoded.Data[0].Embedding), nil
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
func Fit(vector []float32) []float32 {
	if len(vector) == Dimensions {
		return vector
	}
	result := make([]float32, Dimensions)
	copy(result, vector)
	return normalize(result)
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

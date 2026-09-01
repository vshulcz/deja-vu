package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	URL    string
	Model  string
	apiKey string
	HTTP   *http.Client
}

var probeURLs = []string{"http://localhost:11434/api/embed", "http://localhost:1234/v1/embeddings"}

// Off reports whether embedding is switched off for this process. The probe
// below reaches localhost, so without a way to say no, a machine running a
// local model answers differently from one that is not — a test suite that
// asserts "no endpoint" goes red on a developer's machine and stays green on
// CI, which is the worst way round for a contributor to meet it.
func Off() bool { return os.Getenv("DEJA_EMBED_OFF") == "1" }

func New() (*Client, error) {
	model := os.Getenv("DEJA_EMBED_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}
	// A configured endpoint wins over the switch: saying where to embed is
	// asking for it, and the switch is about the probe below.
	if endpoint := os.Getenv("DEJA_EMBED_URL"); endpoint != "" {
		return &Client{URL: endpoint, Model: model, apiKey: embedAPIKey(endpoint), HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
	}
	if Off() {
		return nil, fmt.Errorf("embedding is off (DEJA_EMBED_OFF=1)")
	}
	for _, endpoint := range probeURLs {
		c := &Client{URL: endpoint, Model: model, HTTP: &http.Client{Timeout: 30 * time.Second}}
		if err := c.probe(); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("embedding endpoint unavailable (set DEJA_EMBED_URL)")
}

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	body, err := json.Marshal(map[string]any{"model": c.Model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding endpoint returned %s", resp.Status)
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
		Data       []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	result := out.Embeddings
	if len(result) == 0 {
		result = make([][]float32, len(out.Data))
		for i := range out.Data {
			result[i] = out.Data[i].Embedding
		}
	}
	if len(result) != len(texts) {
		return nil, fmt.Errorf("embedding endpoint returned %d vectors for %d texts", len(result), len(texts))
	}
	return result, nil
}

func embedAPIKey(endpoint string) string {
	if key := os.Getenv("DEJA_EMBED_KEY"); key != "" {
		return key
	}
	parsed, err := url.Parse(endpoint)
	if err == nil && strings.EqualFold(parsed.Scheme, "https") && strings.EqualFold(parsed.Hostname(), "api.openai.com") {
		return os.Getenv("OPENAI_API_KEY")
	}
	return ""
}

func (c *Client) probe() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	v, err := c.Embed(ctx, []string{"deja probe"})
	if err != nil || len(v) != 1 || len(v[0]) == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("empty embedding response")
	}
	return nil
}

func IsOllama(endpoint string) bool {
	return strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/api/embed")
}

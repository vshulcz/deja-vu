// Package embed computes and stores embedding vectors for index records and
// reranks search hits by them.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client talks to an embedding endpoint.
type Client struct {
	URL    string
	Model  string
	apiKey string
	HTTP   *http.Client
	// QueryPrefix and PassagePrefix go in front of a search query and of an
	// indexed record. Retrieval models of the e5 family are trained on
	// "query: " and "passage: " and answer worse without them; both are empty
	// unless DEJA_EMBED_QUERY_PREFIX / DEJA_EMBED_PASSAGE_PREFIX say otherwise,
	// so an endpoint that wants none sees the text as before.
	QueryPrefix   string
	PassagePrefix string
}

// Made reports whether the vectors of s came from this client's model and
// passage prefix. Another model's vectors live in another space, and e5 vectors
// made with "passage: " compared with a query embedded without its "query: "
// counterpart rank by accident; either way the scores look valid and are not.
// A sidecar that names no model predates the field and is taken as it is.
func (c *Client) Made(s Sidecar) bool {
	return s.Model == "" || s.Model == c.storedModel()
}

// MadeNote names the mismatch Made found, once per process.
func (c *Client) MadeNote(s Sidecar) string {
	if saidModelMismatch {
		return ""
	}
	saidModelMismatch = true
	return fmt.Sprintf("deja: the vector sidecar was made with %q, the embedder is set to %q — semantic search is off until `deja embed` runs with the same model and DEJA_EMBED_PASSAGE_PREFIX\n", s.Model, c.storedModel())
}

var saidModelMismatch bool

// storedModel is the name a sidecar is filed under: the model, plus the
// passage prefix when there is one. Vectors made with another prefix are not
// the same vectors, and this is what makes a changed prefix embed again.
func (c *Client) storedModel() string {
	if c.PassagePrefix == "" {
		return c.Model
	}
	return c.Model + "|" + c.PassagePrefix
}

var probeURLs = []string{"http://localhost:11434/api/embed", "http://localhost:1234/v1/embeddings"}

// Off reports whether embedding is switched off for this process. The probe
// below reaches localhost, so without a way to say no, a machine running a
// local model answers differently from one that is not — a test suite that
// asserts "no endpoint" goes red on a developer's machine and stays green on
// CI, which is the worst way round for a contributor to meet it.
func Off() bool { return os.Getenv("DEJA_EMBED_OFF") == "1" }

// New builds a client from the DEJA_EMBED_* environment.
func New() (*Client, error) {
	model := os.Getenv("DEJA_EMBED_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}
	// "off" is how a caller says there is no endpoint and none is to be looked
	// for, the same thing DEJA_EMBED_OFF=1 says. Without it the probe below
	// reaches whatever is listening on this machine, so a developer running
	// Ollama got a different answer out of `deja doctor` — and out of the
	// tests — than a machine with nothing wired. Any other configured endpoint
	// wins over the switch: saying where to embed is asking for it.
	if endpoint := os.Getenv("DEJA_EMBED_URL"); strings.EqualFold(strings.TrimSpace(endpoint), "off") {
		return nil, errNoEmbedEndpoint
	} else if endpoint != "" {
		return &Client{URL: endpoint, Model: model, apiKey: embedAPIKey(endpoint), HTTP: &http.Client{Timeout: 120 * time.Second}, QueryPrefix: os.Getenv("DEJA_EMBED_QUERY_PREFIX"), PassagePrefix: os.Getenv("DEJA_EMBED_PASSAGE_PREFIX")}, nil
	}
	if Off() {
		return nil, errors.New("embedding is off (DEJA_EMBED_OFF=1)")
	}
	for _, endpoint := range probeURLs {
		c := &Client{URL: endpoint, Model: model, HTTP: &http.Client{Timeout: 120 * time.Second}, QueryPrefix: os.Getenv("DEJA_EMBED_QUERY_PREFIX"), PassagePrefix: os.Getenv("DEJA_EMBED_PASSAGE_PREFIX")}
		if err := c.probe(); err == nil {
			return c, nil
		}
	}
	return nil, errNoEmbedEndpoint
}

// errNoEmbedEndpoint is what every "nothing to embed with" path returns, so a
// caller can tell it from a transport failure.
var errNoEmbedEndpoint = errors.New("embedding endpoint unavailable (set DEJA_EMBED_URL)")

// Embed returns one vector per text.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 120 * time.Second}
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
		// The body says why: Ollama answers a batch holding one empty input
		// with 400 and {"error":"empty content"}, and the status alone sent
		// people hunting (#4975).
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		d := strings.TrimSpace(string(detail))
		if c.apiKey != "" {
			// An endpoint that echoes the request must not hand the key back.
			d = strings.ReplaceAll(d, c.apiKey, "<redacted>")
		}
		if d != "" {
			return nil, fmt.Errorf("embedding endpoint returned %s: %s", resp.Status, d)
		}
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
		return errors.New("empty embedding response")
	}
	return nil
}

// IsOllama reports whether endpoint is Ollama's /api/embed.
func IsOllama(endpoint string) bool {
	return strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/api/embed")
}

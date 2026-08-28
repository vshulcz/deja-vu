package embed

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestEmbedAuthorization(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		dejaKey    string
		openAIKey  string
		wantHeader string
	}{
		{name: "no key", endpoint: "https://example.com/v1/embeddings"},
		{name: "explicit key", endpoint: "https://example.com/v1/embeddings", dejaKey: "deja-key", wantHeader: "Bearer deja-key"},
		{name: "explicit key takes precedence", endpoint: "https://api.openai.com/v1/embeddings", dejaKey: "deja-key", openAIKey: "openai-key", wantHeader: "Bearer deja-key"},
		{name: "OpenAI key for OpenAI", endpoint: "https://api.openai.com/v1/embeddings", openAIKey: "openai-key", wantHeader: "Bearer openai-key"},
		{name: "OpenAI key not sent over plaintext", endpoint: "http://api.openai.com/v1/embeddings", openAIKey: "openai-key"},
		{name: "OpenAI key not sent to localhost", endpoint: "http://localhost:1234/v1/embeddings", openAIKey: "openai-key"},
		{name: "OpenAI key not sent to LAN", endpoint: "http://192.168.1.20:1234/v1/embeddings", openAIKey: "openai-key"},
		{name: "OpenAI key not sent to compatible endpoint", endpoint: "https://example.com/v1/embeddings", openAIKey: "openai-key"},
		{name: "OpenAI key not sent to lookalike host", endpoint: "https://api.openai.com.example.com/v1/embeddings", openAIKey: "openai-key"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DEJA_EMBED_URL", tc.endpoint)
			t.Setenv("DEJA_EMBED_MODEL", "test-model")
			t.Setenv("DEJA_EMBED_KEY", tc.dejaKey)
			t.Setenv("OPENAI_API_KEY", tc.openAIKey)

			client, err := New()
			if err != nil {
				t.Fatal(err)
			}
			var gotHeader string
			client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				gotHeader = req.Header.Get("Authorization")
				return embeddingResponse(req, http.StatusOK, `{"data":[{"embedding":[1]}]}`), nil
			})}

			if _, err := client.Embed(context.Background(), []string{"hello"}); err != nil {
				t.Fatal(err)
			}
			if gotHeader != tc.wantHeader {
				t.Errorf("Authorization = %q, want %q", gotHeader, tc.wantHeader)
			}
		})
	}
}

func TestEmbedErrorsDoNotLeakCredential(t *testing.T) {
	const secret = "credential-that-must-not-appear"
	t.Setenv("DEJA_EMBED_URL", "https://example.com/v1/embeddings")
	t.Setenv("DEJA_EMBED_KEY", secret)

	tests := []struct {
		name      string
		transport roundTripFunc
	}{
		{
			name: "transport error",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("upstream unavailable")
			},
		},
		{
			name: "HTTP error",
			transport: func(req *http.Request) (*http.Response, error) {
				return embeddingResponse(req, http.StatusUnauthorized, ""), nil
			},
		},
		{
			name: "decode error",
			transport: func(req *http.Request) (*http.Response, error) {
				return embeddingResponse(req, http.StatusOK, "not JSON"), nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := New()
			if err != nil {
				t.Fatal(err)
			}
			client.HTTP = &http.Client{Transport: tc.transport}

			_, err = client.Embed(context.Background(), []string{"hello"})
			if err == nil {
				t.Fatal("expected embedding error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("embedding error leaked credential: %q", err)
			}
		})
	}
}

func embeddingResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		Status:     http.StatusText(status),
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbedAuthorizationHeader(t *testing.T) {
	tests := []struct {
		name       string
		client     Client
		wantHeader string
	}{
		{
			name:       "with api key",
			client:     Client{Model: "test", APIKey: "secret"},
			wantHeader: "Bearer secret",
		},
		{
			name:   "without api key",
			client: Client{Model: "test"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != tc.wantHeader {
					t.Fatalf("Authorization = %q, want %q", got, tc.wantHeader)
				}
				_, _ = w.Write([]byte(`{"data":[{"embedding":[1]}]}`))
			}))
			defer server.Close()

			client := tc.client
			client.URL = server.URL
			if _, err := client.Embed(context.Background(), []string{"hello"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEmbedAPIKeyResolution(t *testing.T) {
	t.Setenv("DEJA_EMBED_KEY", "deja-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	if got := embedAPIKey("https://api.openai.com/v1/embeddings"); got != "deja-key" {
		t.Fatalf("explicit key = %q, want deja-key", got)
	}

	t.Setenv("DEJA_EMBED_KEY", "")
	if got := embedAPIKey("https://api.openai.com/v1/embeddings"); got != "openai-key" {
		t.Fatalf("OpenAI fallback = %q, want openai-key", got)
	}
	if got := embedAPIKey("http://localhost:1234/v1/embeddings"); got != "" {
		t.Fatalf("local endpoint received OpenAI key: %q", got)
	}
	if got := embedAPIKey("https://example.com/v1/embeddings"); got != "" {
		t.Fatalf("custom endpoint received OpenAI key: %q", got)
	}
}

func TestNewLoadsEmbeddingKey(t *testing.T) {
	t.Setenv("DEJA_EMBED_URL", "https://example.com/v1/embeddings")
	t.Setenv("DEJA_EMBED_MODEL", "embedding-model")
	t.Setenv("DEJA_EMBED_KEY", "deja-key")
	client, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if client.APIKey != "deja-key" {
		t.Fatalf("APIKey = %q, want deja-key", client.APIKey)
	}
}

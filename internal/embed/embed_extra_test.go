package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestEmbedRejectsCountMismatchOnOllamaShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1,0],[1,0],[1,0]]}`))
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, Model: "m"}
	if _, err := c.Embed(context.Background(), []string{"one"}); err == nil {
		t.Fatal("expected count-mismatch error for the embeddings shape")
	}
}

func TestSemanticSearchBadQueryVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Two vectors for a single query text: len(query) != 1 downstream.
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]},{"embedding":[0,1]}]}`))
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, Model: "m"}
	if _, err := SemanticSearch(context.Background(), t.TempDir(), search.Options{Query: "q"}, Sidecar{Vectors: []Vector{{Key: "k", Values: []float32{1, 0}}}}, c); err == nil {
		t.Fatal("expected bad query vector error")
	}
}

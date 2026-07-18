package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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

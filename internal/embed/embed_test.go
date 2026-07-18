package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestClientShapesAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want float32
	}{
		{"ollama", `{"embeddings":[[1,2]]}`, 1},
		{"openai", `{"data":[{"embedding":[3,4]}]}`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path == "" {
					t.Error("bad request")
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["model"] == nil {
					t.Error("bad body")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c := &Client{URL: s.URL, Model: "test"}
			v, err := c.Embed(context.Background(), []string{"hello"})
			if err != nil || len(v) != 1 || v[0][0] != tc.want {
				t.Fatalf("vectors=%v err=%v", v, err)
			}
		})
	}
	s := httptest.NewServer(http.NotFoundHandler())
	defer s.Close()
	if _, err := (&Client{URL: s.URL, Model: "test"}).Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected status error")
	}
}

func TestNewExplicitAndProbeOrder(t *testing.T) {
	t.Setenv("DEJA_EMBED_MODEL", "probe-model")
	t.Setenv("DEJA_EMBED_URL", "http://explicit.invalid/api/embed")
	c, err := New()
	if err != nil || c.URL != os.Getenv("DEJA_EMBED_URL") || c.Model != "probe-model" {
		t.Fatalf("client=%v err=%v", c, err)
	}
}

func TestSidecarRoundTripCorruptAndRerank(t *testing.T) {
	d := t.TempDir()
	dir := filepath.Join(d, "index.db")
	s := Sidecar{Model: "m", Generation: "g", Dim: 2, Covered: 2, Vectors: []Vector{{Offset: 4, Key: "claude:a", Values: []float32{1, 0}}, {Offset: 8, Key: "claude:b", Values: []float32{0, 1}}}}
	if err := write(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil || len(got.Vectors) != 2 || got.Vectors[1].Values[1] != 1 {
		t.Fatalf("sidecar=%+v err=%v", got, err)
	}
	hits := []search.Hit{{Session: session("a"), Score: 2}, {Session: session("b"), Score: 1}}
	client := &Client{URL: "", Model: "m", HTTP: http.DefaultClient}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`)) }))
	defer server.Close()
	client.URL = server.URL
	ranked, err := Rerank(context.Background(), hits, "q", got, client)
	if err != nil || ranked[0].Session.ID != "a" {
		t.Fatalf("ranked=%v err=%v", ranked, err)
	}
	if err := os.WriteFile(Path(dir), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil {
		t.Fatal("expected corrupt error")
	}
}

func session(id string) model.Session { return model.Session{ID: id, Harness: "claude"} }

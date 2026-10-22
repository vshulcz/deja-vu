package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// madeNoteTested is set once the test below has run in this process: the note
// about a foreign sidecar is said once per process, so a repeated run (-count)
// finds it already spent.
var madeNoteTested bool

// Vectors made by another model, or with another passage prefix, are never
// compared with a query of this client: the rerank leaves the lexical order
// alone, the semantic tier returns nothing, and the mismatch is named once.
func TestSearchIgnoresASidecarMadeByAnotherModelOrPrefix(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alpha", "beta", "gamma"} {
		line := `{"type":"user","message":{"role":"user","content":"` + id + ` notes"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The records of alpha and gamma point one way, beta's the other, and the
	// query the way of beta: a rerank that trusts the vectors puts beta first.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		out := make([][]float32, len(body.Input))
		for i, in := range body.Input {
			out[i] = []float32{1, 0}
			if strings.Contains(in, "alpha") || strings.Contains(in, "gamma") {
				out[i] = []float32{0, 1}
			}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer ts.Close()
	t.Setenv("DEJA_EMBED_URL", ts.URL)

	hits := func() []search.Hit {
		out := make([]search.Hit, 0, 3)
		for _, h := range []struct {
			id    string
			score float64
		}{{"alpha", 1}, {"beta", 0.9}, {"gamma", 0}} {
			out = append(out, search.Hit{Session: model.Session{ID: h.id, Harness: "claude"}, Score: h.score})
		}
		return out
	}
	ids := func(hs []search.Hit) string {
		s := make([]string, 0, len(hs))
		for _, h := range hs {
			s = append(s, h.Session.ID)
		}
		return strings.Join(s, " ")
	}
	said := func(run func(notice *os.File)) string {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "notice")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		run(f)
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	const noteText = "vector sidecar was made with"
	spent := madeNoteTested
	madeNoteTested = true
	noted := 0

	for _, c := range []struct {
		name                      string
		madeModel, madePrefix     string
		searchModel, searchPrefix string
		trusted                   bool
	}{
		{"same model", "made-a", "", "made-a", "", true},
		{"same model and prefix", "made-b", "passage: ", "made-b", "passage: ", true},
		{"another model", "made-c", "", "searches-c", "", false},
		{"another passage prefix", "made-d", "passage: ", "made-d", "", false},
		{"a prefix the sidecar lacks", "made-e", "", "made-e", "passage: ", false},
	} {
		if _, err := embed.EmbedIndex(dir, &embed.Client{URL: ts.URL, Model: c.madeModel, PassagePrefix: c.madePrefix}, nil); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		t.Setenv("DEJA_EMBED_MODEL", c.searchModel)
		t.Setenv("DEJA_EMBED_PASSAGE_PREFIX", c.searchPrefix)
		var reranked []search.Hit
		rerankNote := said(func(notice *os.File) {
			reranked = maybeRerank(dir, hits(), search.Options{Query: "needle"}, notice)
		})
		var semanticHits []search.Hit
		var semantic bool
		semanticNote := said(func(notice *os.File) {
			semanticHits, semantic = maybeSemantic(dir, nil, search.Options{Query: "needle"}, notice)
		})
		noted += strings.Count(rerankNote+semanticNote, noteText)
		if c.trusted {
			if got := ids(reranked); got != "beta alpha gamma" {
				t.Errorf("%s: rerank order = %q, want the vectors to reorder it (this measures nothing otherwise)", c.name, got)
			}
			if !semantic || len(semanticHits) == 0 {
				t.Errorf("%s: no semantic hits from a sidecar of this model", c.name)
			}
			if strings.Contains(rerankNote+semanticNote, noteText) {
				t.Errorf("%s: a sidecar of this model was called foreign: %q %q", c.name, rerankNote, semanticNote)
			}
			continue
		}
		if got := ids(reranked); got != "alpha beta gamma" || reranked[0].Score != 1 {
			t.Errorf("%s: rerank used foreign vectors: order %q, top score %v", c.name, got, reranked[0].Score)
		}
		if semantic || len(semanticHits) != 0 {
			t.Errorf("%s: semantic search used foreign vectors: %v", c.name, ids(semanticHits))
		}
	}
	if !spent && noted != 1 {
		t.Errorf("the mismatch was named %d times, want once", noted)
	}
	if spent && noted > 1 {
		t.Errorf("the mismatch was named %d times in one process", noted)
	}
}

package embed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// staleStore indexes three sessions whose first two records are the same size,
// then forgets the first. Every later record shifts by exactly that size, so a
// vector still keyed to the second session now points at the third one's text —
// the collision this guard exists for, built on purpose rather than hoped for.
func staleStore(t *testing.T) (dir string, vectors []Vector, gen string) {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	if err := os.MkdirAll(filepath.Join(root, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, id, text string) {
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":%q}}`+"\n", id, text)
		if err := os.WriteFile(filepath.Join(root, "project", name), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.jsonl", "aaa", "the vault rotation broke the staging deploy")
	write("b.jsonl", "bbb", "the kafka consumer keeps flapping at noon!!")
	write("c.jsonl", "ccc", "the scheduler retries on every single timeout")
	for key, value := range map[string]string{
		"HOME": tmp, "USERPROFILE": tmp, "DEJA_CLAUDE_ROOT": root,
		"DEJA_CODEX_ROOT": filepath.Join(tmp, "codex"), "DEJA_OPENCODE_DB": filepath.Join(tmp, "open.db"),
		"DEJA_AIDER_ROOTS": filepath.Join(tmp, "aider"), "DEJA_GEMINI_ROOT": filepath.Join(tmp, "gemini"),
		"DEJA_CURSOR_ROOT": filepath.Join(tmp, "cursor"), "DEJA_CURSOR_CLI_ROOT": filepath.Join(tmp, "cursor-cli"),
		"DEJA_ANTIGRAVITY_ROOT": filepath.Join(tmp, "antigravity"), "DEJA_GROK_ROOT": filepath.Join(tmp, "grok"),
		"DEJA_QWEN_ROOT": filepath.Join(tmp, "qwen"),
	} {
		t.Setenv(key, value)
	}
	dir = filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	records, err := index.ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen, err = index.Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		vectors = append(vectors, Vector{Offset: r.Offset, Key: r.Record.Key, Values: []float32{1, 0}})
	}
	if _, err := index.Forget(dir, index.ForgetOptions{Session: "aaa"}); err != nil {
		t.Fatal(err)
	}
	return dir, vectors, gen
}

func fakeEmbedClient(t *testing.T) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1,0]]}`))
	}))
	t.Cleanup(ts.Close)
	return &Client{URL: ts.URL}
}

// Vectors address records by byte offset, and a rebuild moves them. A sidecar
// from before the rebuild made semantic search quote one session's text under
// another session's name.
func TestSemanticSearchRefusesASidecarFromAnotherIndex(t *testing.T) {
	dir, vectors, gen := staleStore(t)
	hits, err := SemanticSearch(context.Background(), dir, search.Options{Query: "anything", All: true},
		Sidecar{Generation: gen, Dim: 2, Vectors: vectors}, fakeEmbedClient(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if !strings.Contains(h.Snippets[0], "kafka") && h.Session.ID == "bbb" {
			t.Errorf("hit named session %s but quoted %q", h.Session.ID, h.Snippets[0])
		}
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits from a sidecar built for an earlier index", len(hits))
	}
}

// A sidecar built for the index in front of it is used as before.
func TestSemanticSearchUsesACurrentSidecar(t *testing.T) {
	dir, _, _ := staleStore(t)
	records, err := index.ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := index.Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	var fresh []Vector
	for _, r := range records {
		fresh = append(fresh, Vector{Offset: r.Offset, Key: r.Record.Key, Values: []float32{1, 0}})
	}
	hits, err := SemanticSearch(context.Background(), dir, search.Options{Query: "anything", All: true},
		Sidecar{Generation: gen, Dim: 2, Vectors: fresh}, fakeEmbedClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a current sidecar produced no semantic hits")
	}
	for _, h := range hits {
		want := map[string]string{"bbb": "kafka", "ccc": "scheduler"}[h.Session.ID]
		if want == "" || !strings.Contains(h.Snippets[0], want) {
			t.Errorf("hit named session %s but quoted %q", h.Session.ID, h.Snippets[0])
		}
	}
}

// Stale is the rule itself: an empty sidecar is not stale (there is nothing to
// mislead with), and a generation that matches is not stale.
func TestStaleRule(t *testing.T) {
	dir, vectors, gen := staleStore(t)
	if Stale(dir, Sidecar{}) {
		t.Error("an empty sidecar counts as stale")
	}
	if !Stale(dir, Sidecar{Generation: gen, Vectors: vectors}) {
		t.Error("a sidecar from before the rebuild does not count as stale")
	}
	now, err := index.Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if Stale(dir, Sidecar{Generation: now, Vectors: vectors}) {
		t.Error("a sidecar for this index counts as stale")
	}
}

// The rule rests on two rebuilds being distinguishable. A timestamp alone is
// not: on a coarse clock a forget straight after an index build shares it, and
// the sidecar from before then reads as current.
func TestGenerationSeparatesTwoRebuildsInOneTick(t *testing.T) {
	dir, _, before := staleStore(t)
	after, err := index.Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Errorf("the generation is %q before and after a rebuild that moved every offset", before)
	}
}

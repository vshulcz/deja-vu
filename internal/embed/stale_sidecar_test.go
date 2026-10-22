package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// threeSessionStore indexes three sessions and returns the index directory with
// its records, newest state.
func threeSessionStore(t *testing.T) (dir string, records []index.OffsetRecord) {
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
	write("b.jsonl", "bbb", "the kafka consumer keeps flapping at noon")
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
	if len(records) < 3 {
		t.Fatalf("fixture indexed %d records, want 3", len(records))
	}
	return dir, records
}

func fakeEmbedClient(t *testing.T) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1,0]]}`))
	}))
	t.Cleanup(ts.Close)
	return &Client{URL: ts.URL}
}

// Vectors address records by byte offset, so a sidecar built before a rebuild
// can pair one session's key with another session's record — semantic search
// then quotes text under a name that never said it. The pairing here is built
// deliberately rather than waited for: what matters is that a sidecar from
// another index is refused before its offsets are used at all.
func TestSemanticSearchRefusesASidecarFromAnotherIndex(t *testing.T) {
	dir, records := threeSessionStore(t)
	crossed := []Vector{
		{Offset: records[2].Offset, Key: records[1].Record.Key, Values: []float32{1, 0}},
	}
	hits, err := SemanticSearch(context.Background(), dir, search.Options{Query: "anything", All: true},
		Sidecar{Generation: "built-for-an-older-index", Dim: 2, Vectors: crossed}, fakeEmbedClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits from a sidecar built for another index: %q under session %s",
			len(hits), hits[0].Snippets[0], hits[0].Session.ID)
	}
}

// The same crossed vector with the current generation: the layout holds, but
// the record at the offset belongs to another session, so it is refused too
// rather than quoted under the wrong name. The fixture really does cross a key
// with another session's text.
func TestCrossedVectorIsRefusedOnItsOwnIndex(t *testing.T) {
	dir, records := threeSessionStore(t)
	if records[2].Record.Key == records[1].Record.Key || strings.Contains(records[2].Record.Text, "kafka") {
		t.Fatal("the fixture did not cross a session key with another session's record")
	}
	gen, err := index.Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := SemanticSearch(context.Background(), dir, search.Options{Query: "anything", All: true},
		Sidecar{Generation: gen, Dim: 2, Vectors: []Vector{
			{Offset: records[2].Offset, Key: records[1].Record.Key, Values: []float32{1, 0}},
		}}, fakeEmbedClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits from a vector whose offset holds another session's record: %q under %s",
			len(hits), hits[0].Snippets[0], hits[0].Session.ID)
	}
}

// A sidecar built for the index in front of it is used as before.
func TestSemanticSearchUsesACurrentSidecar(t *testing.T) {
	dir, records := threeSessionStore(t)
	gen, err := index.Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	fresh := make([]Vector, 0, len(records))
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
		want := map[string]string{"aaa": "vault", "bbb": "kafka", "ccc": "scheduler"}[h.Session.ID]
		if want == "" || !strings.Contains(h.Snippets[0], want) {
			t.Errorf("hit named session %s but quoted %q", h.Session.ID, h.Snippets[0])
		}
	}
}

// Stale is the rule: an empty sidecar has nothing to mislead with, a matching
// generation is current, anything else is not.
func TestStaleRule(t *testing.T) {
	dir, records := threeSessionStore(t)
	vectors := []Vector{{Offset: records[0].Offset, Key: records[0].Record.Key, Values: []float32{1, 0}}}
	if Stale(dir, Sidecar{}) {
		t.Error("an empty sidecar counts as stale")
	}
	if !Stale(dir, Sidecar{Generation: "built-for-an-older-index", Vectors: vectors}) {
		t.Error("a sidecar from another index does not count as stale")
	}
	gen, err := index.Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if Stale(dir, Sidecar{Generation: gen, Vectors: vectors}) {
		t.Error("a sidecar for this index counts as stale")
	}
}

// A rewrite that drops a session moves every later record. A vector found by
// identity still answers with its own session's text: the misattribution of
// #1355 is what made offsets-only sidecars stale, and identity is what lets
// this one be used.
func TestAVectorFindsItsRecordAfterARewrite(t *testing.T) {
	dir, _ := threeSessionStore(t)
	vec := func(text string) []float32 {
		switch {
		case strings.Contains(text, "vault"):
			return []float32{1, 0, 0}
		case strings.Contains(text, "kafka"):
			return []float32{0, 1, 0}
		}
		return []float32{0, 0, 1}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		out := make([][]float32, len(body.Input))
		for i, text := range body.Input {
			out[i] = vec(text)
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer ts.Close()
	client := &Client{URL: ts.URL, Model: "test"}
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "a.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if Stale(dir, s) {
		t.Fatal("a sidecar whose vectors carry their identity was called stale")
	}
	hits, err := SemanticSearch(context.Background(), dir, search.Options{Query: "the scheduler", All: true}, s, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Session.ID != "ccc" || !strings.Contains(hits[0].Snippets[0], "scheduler") {
		t.Fatalf("after the rewrite the scheduler question found %+v", hits)
	}
	for _, h := range hits {
		if h.Session.ID == "aaa" {
			t.Errorf("the dropped session came back: %+v", h)
		}
	}
}

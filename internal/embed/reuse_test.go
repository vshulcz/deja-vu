package embed

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// countingClient answers every embed request and records how many texts it was
// asked to embed.
func countingClient(t *testing.T, embedded *atomic.Int64) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		n := len(body.Input)
		embedded.Add(int64(n))
		out := make([][]float32, n)
		for i := range out {
			out[i] = []float32{1, 0}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	t.Cleanup(ts.Close)
	return &Client{URL: ts.URL, Model: "test"}
}

func embedStore(t *testing.T) (dir string, addSession func(id string)) {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	if err := os.MkdirAll(filepath.Join(root, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"session ` + id + ` about the scheduler"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, "project", id+".jsonl"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		write(id)
	}
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
	// Growing a session already in the index is the common event and the only
	// one that appends in place; a brand-new session rebuilds records.bin, where
	// no offset can be trusted afterwards.
	return dir, func(id string) {
		f, err := os.OpenFile(filepath.Join(root, "project", "a.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		line := `{"type":"user","sessionId":"a","timestamp":"2026-01-02T00:00:00Z","message":{"role":"user","content":"and then ` + id + ` happened to the scheduler"}}` + "\n"
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := index.Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// Embedding costs a network call per record. A session that grows is appended
// to records.bin in place, so every vector already paid for still points at the
// record it was made from — only the new message needs embedding.
func TestEmbedIndexOnlyEmbedsWhatIsNew(t *testing.T) {
	var embedded atomic.Int64
	client := countingClient(t, &embedded)
	dir, addSession := embedStore(t)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if first := embedded.Load(); first != 5 {
		t.Fatalf("first build embedded %d records, want 5 — the fixture is wrong", first)
	}
	addSession("f") // one more message in a session already indexed
	embedded.Store(0)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if again := embedded.Load(); again != 1 {
		t.Errorf("one more message re-embedded %d records, want 1", again)
	}
}

// The layout rule underneath it, stated directly: a stamp that changed is a
// rebuild and invalidates every offset; the same stamp with a file that only
// grew is an append and invalidates nothing; a file that shrank moved records.
func TestSameLayout(t *testing.T) {
	for name, tc := range map[string]struct {
		was, now string
		want     bool
	}{
		"identical":         {"2026-01-01T00:00:00Z+100", "2026-01-01T00:00:00Z+100", true},
		"appended":          {"2026-01-01T00:00:00Z+100", "2026-01-01T00:00:00Z+180", true},
		"shrank":            {"2026-01-01T00:00:00Z+180", "2026-01-01T00:00:00Z+100", false},
		"rebuilt":           {"2026-01-01T00:00:00Z+100", "2026-01-01T00:00:09Z+100", false},
		"no size at all":    {"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z+100", false},
		"empty":             {"", "2026-01-01T00:00:00Z+100", false},
		"no stamp":          {"+100", "+180", false},
		"size not a number": {"2026-01-01T00:00:00Z+abc", "2026-01-01T00:00:00Z+180", false},
		"negative size":     {"2026-01-01T00:00:00Z+-5", "2026-01-01T00:00:00Z+180", false},
	} {
		if got := sameLayout(tc.was, tc.now); got != tc.want {
			t.Errorf("%s: sameLayout(%q, %q) = %v", name, tc.was, tc.now, got)
		}
	}
}

// The other side of the rule, end to end: a new session rebuilds records.bin,
// so no offset survives and every vector has to be made again. Cheap would be
// wrong here.
func TestEmbedIndexRebuildsEverythingAfterANewSession(t *testing.T) {
	var embedded atomic.Int64
	client := countingClient(t, &embedded)
	dir, _ := embedStore(t)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	line := `{"type":"user","sessionId":"z","timestamp":"2026-01-03T00:00:00Z","message":{"role":"user","content":"a brand new session about the scheduler"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "project", "z.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	embedded.Store(0)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if again := embedded.Load(); again != 6 {
		t.Errorf("a rebuild re-embedded %d records, want all 6 — offsets moved", again)
	}
}

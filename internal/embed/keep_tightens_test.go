package embed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A policy that tightens has to reach the sidecar. The vectors are already off
// the machine and cannot be recalled, but SemanticSearch reads this file
// directly, so vectors carried forward for records the caller now withholds
// keep answering with sessions the owner has since closed off (#1311). The
// layout is unchanged between the two runs, which is exactly the case where
// EmbedIndex reuses what it has.
func TestEmbedIndexDropsVectorsTheCallerNoLongerAllows(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude", "-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"KEEPME semantic needle"}}` + "\n" +
		`{"type":"user","sessionId":"s","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"DROPME the acquisition price"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"), []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := make([][]float64, len(req.Input))
		for i := range out {
			out[i] = []float64{1}
		}
		b, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(b)
	}))
	defer ts.Close()
	client := &Client{URL: ts.URL, Model: "m"}

	// Yesterday: the loose rule embedded everything.
	before, err := EmbedIndex(dir, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Vectors) < 2 {
		t.Fatalf("the fixture embedded %d records, so the reuse path is never exercised", len(before.Vectors))
	}

	// Today: the record is withheld. Nothing about the index changed.
	after, err := EmbedIndex(dir, client, func(r index.Record) bool { return !strings.Contains(r.Text, "DROPME") })
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Vectors) != len(before.Vectors)-1 {
		t.Errorf("the sidecar kept %d vectors after one record was withheld, had %d", len(after.Vectors), len(before.Vectors))
	}
	if after.Covered != len(after.Vectors) {
		t.Errorf("Covered says %d, sidecar holds %d vectors", after.Covered, len(after.Vectors))
	}
	// And the vector on disk is gone, not just absent from the returned value.
	onDisk, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk.Vectors) != len(after.Vectors) {
		t.Errorf("the file holds %d vectors, the caller was told %d", len(onDisk.Vectors), len(after.Vectors))
	}
}

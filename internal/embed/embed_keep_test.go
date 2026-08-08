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

// keep is how embed keeps content the trust policy withholds off the wire to an
// external endpoint. A dropped record must never reach the client at all.
func TestEmbedIndexKeepFiltersRecords(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude", "-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"KEEPME semantic needle"}}` + "\n" +
		`{"type":"user","sessionId":"s","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"DROPME imported secret"}}` + "\n"
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
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		got = append(got, req.Input...)
		out := make([][]float64, len(req.Input))
		for i := range out {
			out[i] = []float64{1}
		}
		b, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(b)
	}))
	defer ts.Close()

	keep := func(r index.Record) bool { return !strings.Contains(r.Text, "DROPME") }
	if _, err := EmbedIndex(dir, &Client{URL: ts.URL, Model: "m"}, keep); err != nil {
		t.Fatal(err)
	}
	for _, in := range got {
		if strings.Contains(in, "DROPME") {
			t.Errorf("a dropped record reached the endpoint: %q", in)
		}
	}
	kept := false
	for _, in := range got {
		if strings.Contains(in, "KEEPME") {
			kept = true
		}
	}
	if !kept {
		t.Errorf("the kept record never reached the endpoint: %v", got)
	}
}

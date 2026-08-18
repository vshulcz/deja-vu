package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
)

// doctor is where someone goes when semantic results stop coming. Vectors
// address records by offset, so search refuses a sidecar built for an earlier
// index — and doctor was still reporting its coverage, which reads as "the
// embeddings are there" on the one screen meant to say otherwise.
func TestDoctorReportsNoCoverageForASidecarSearchRefuses(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the scheduler retries twice"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		out := make([][]float32, len(body.Input))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer ts.Close()
	if _, err := embed.EmbedIndex(dir, &embed.Client{URL: ts.URL, Model: "test"}, nil); err != nil {
		t.Fatal(err)
	}
	before := collectDoctorEmbed(dir)
	if before == nil || before.Coverage == 0 {
		t.Fatal("no coverage before a rebuild, so the test measures nothing")
	}

	// A second session rebuilds records.bin, so no offset in the sidecar means
	// what it did and search refuses it.
	line2 := `{"type":"user","message":{"role":"user","content":"the cache eviction wiped sessions"},"timestamp":"2026-08-03T10:00:00Z","sessionId":"bbb","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "b.jsonl"), []byte(line2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	s, err := embed.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !embed.Stale(dir, s) {
		t.Fatal("the rebuild left the sidecar usable, so the test measures nothing")
	}
	if r := collectDoctorEmbed(dir); r != nil && r.Coverage > 0 {
		t.Errorf("doctor reports %.1f%% coverage from a sidecar search will not use", r.Coverage)
	}
}

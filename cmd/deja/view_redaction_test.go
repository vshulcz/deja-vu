package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// The page is a file on disk whose whole purpose is to be looked at and passed
// around, and `share` and `sync export` both redact on the way out. This one
// wrote whatever the index held — an index from an older deja, or one built
// before a pattern was fixed — and then counted the markers ingest had written
// and called them masked here (#1768).
func TestTheViewRedactsWhatTheIndexKept(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "8f14e45fceea167a5a36dedd4bea2543"
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	rec := `{"type":"user","sessionId":"v1","cwd":"/w","timestamp":"2026-08-22T01:00:00Z","message":{"role":"user","content":"vexlorb {\"api_key\": \"` + secret + `\"} and ` + token + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NO_REDACT", "1")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NO_REDACT", "")

	out := filepath.Join(tmp, "view.html")
	path, masked, err := writeViewHTML(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if strings.Contains(page, secret) {
		t.Error("the page carries an api key the index kept")
	}
	if strings.Contains(page, token) {
		t.Error("the page carries a github token the index kept")
	}
	if masked < 2 {
		t.Errorf("the page reports %d masked, and it masked at least two", masked)
	}
	if !strings.Contains(page, redact.Marker) {
		t.Error("nothing in the page says a secret was taken out of it")
	}
}

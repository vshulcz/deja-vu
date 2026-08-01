package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Result lines elide the middle of a long id, so what a reader copies out of a
// search is a head and a tail rather than a prefix (#707).
func TestFindByPrefixFallsBackToASubstring(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"00000000-0001-4a1b-9c2d-000000000001", "00000000-0002-4a1b-9c2d-000000000002"} {
		body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"work in ` + id + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The tail alone, as it appears after the ellipsis in a result line.
	s, ok, err := FindByPrefix(dir, "0000000002")
	if err != nil || !ok {
		t.Fatalf("substring lookup: ok=%v err=%v", ok, err)
	}
	if s.ID != "00000000-0002-4a1b-9c2d-000000000002" {
		t.Errorf("resolved %q", s.ID)
	}
	// A real prefix still wins outright.
	s, ok, err = FindByPrefix(dir, "00000000-0001")
	if err != nil || !ok || s.ID != "00000000-0001-4a1b-9c2d-000000000001" {
		t.Fatalf("prefix lookup: %q ok=%v err=%v", s.ID, ok, err)
	}
	// Nothing matching stays nothing.
	if _, ok, err := FindByPrefix(dir, "not-in-this-store"); err != nil || ok {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}

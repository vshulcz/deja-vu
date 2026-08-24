package index

import (
	"os"
	"path/filepath"
	"testing"
)

// A uuid is case-insensitive by RFC 4122 and harnesses print it either way, so
// an id pasted in the other case is still that id. Every lookup here compared
// bytes, and deja answered "no session matches" about a session it holds and
// prints that id for (#1620).
func TestFindByPrefixResolvesAnIdInTheOtherCase(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "abcd1234-0001-4a1b-9c2d-000000000001"
	body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the pool work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The precondition: the id as stored resolves.
	if s, ok, err := FindByPrefix(dir, "abcd1234"); err != nil || !ok || s.ID != id {
		t.Fatalf("the id as stored: %q ok=%v err=%v", s.ID, ok, err)
	}
	for _, p := range []string{"ABCD1234", "AbCd1234", "ABCD1234-0001-4A1B-9C2D-000000000001"} {
		s, ok, err := FindByPrefix(dir, p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !ok || s.ID != id {
			t.Errorf("%s found %q (ok=%v); it is the same id in another case", p, s.ID, ok)
		}
	}
	// The control: a prefix that names nothing still names nothing.
	if _, ok, err := FindByPrefix(dir, "ZZZZ9999"); err != nil || ok {
		t.Errorf("ZZZZ9999 resolved (ok=%v, err=%v); case folding must not match everything", ok, err)
	}
}

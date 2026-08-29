package index

import (
	"os"
	"path/filepath"
	"testing"
)

// The same session on two machines has to be ranked the same way. #2558 gave
// the import path the counts behind Touched; Words and Counted were still
// dropped, and both feed ranking — Words is the length BM25 normalises by, and
// a zero there is exactly the marathon-wins case search.go's comment describes;
// Counted is the corpus size in messages, where a session with no count
// degrades to one document (#2569).
func TestImportedSessionIsCountedLikeALocalOne(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "claude", "-w-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	setHome(t, home)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))

	line := func(role, text, at string) string {
		return `{"type":"` + role + `","sessionId":"s1","cwd":"/w/app","timestamp":"` + at +
			`","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	body := line("user", "why does the pool starve under load?", "2026-08-01T10:00:00Z") +
		line("assistant", "the fix was the retry budget, not the pool", "2026-08-01T10:02:00Z") +
		line("user", "and what did the pool change cost us?", "2026-08-01T10:03:00Z") +
		line("assistant", "two hours and a revert", "2026-08-01T10:04:00Z")
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(tmp, "local.db")
	t.Setenv("DEJA_INDEX_DIR", local)
	if err := Ensure(local, "", true, nil); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportFull(local, exp); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(tmp, "remote.db")
	if _, err := Import(remote, exp); err != nil {
		t.Fatal(err)
	}

	find := func(dir string) SessionMeta {
		t.Helper()
		metas, err := AllMeta(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range metas {
			if m.ID == "s1" || m.OrigID == "s1" {
				return m
			}
		}
		t.Fatalf("session not found among %d metas in %s", len(metas), dir)
		return SessionMeta{}
	}
	here, there := find(local), find(remote)
	if here.Words == 0 {
		t.Fatal("the local session has no word count, so this measures nothing")
	}
	if there.Words != here.Words {
		t.Errorf("imported words=%d, local words=%d — the two machines normalise the same session differently",
			there.Words, here.Words)
	}
	if there.Counted != here.Counted {
		t.Errorf("imported counted=%d, local counted=%d — the corpus size disagrees across the sync",
			there.Counted, here.Counted)
	}
}

package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// aider has no session ids, and deja numbered sessions by their place in the
// history file. People delete that file because aider appends to it forever;
// deja keeps what it held, and the next aider run at the same path started a
// new file whose first session took id 1 again and overwrote the kept one
// (#4332).
func TestANewAiderHistoryKeepsTheDeletedOnesSessions(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "aider")
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(proj, ".aider.chat.history.md")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(hist, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("# aider chat started at 2026-09-30 10:00:00\n\n#### fix the retry loop in worker.go  \n\nChanged the loop to back off.\n\n#### now add jitter to the backoff  \n\nAdded jitter with rand.Int63n.\n")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "home", ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("DEJA_AIDER_ROOTS", root)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(hist); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "jitter"}); len(ss) != 1 {
		t.Fatalf("the deleted history's session was not kept: %#v", ss)
	}
	write("\n# aider chat started at 2026-10-01 09:00:00\n\n#### rename the config loader  \n\nRenamed it to loadSettings.\n")
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "jitter"}); len(ss) != 1 {
		t.Fatalf("the new history overwrote the kept session: %#v", ss)
	}
	if ss, _ := Search(dir, search.Options{Query: "loadSettings"}); len(ss) != 1 {
		t.Fatalf("the new history's session is not indexed: %#v", ss)
	}
	// aider appends to the new file: both are still there, once each.
	f, err := os.OpenFile(hist, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n#### and the env override  \n\nloadSettings reads DEJA_ENV first.\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"jitter", "loadSettings"} {
		if ss, _ := Search(dir, search.Options{Query: q}); len(ss) != 1 {
			t.Fatalf("%q after an append: %#v", q, ss)
		}
	}
	// A rebuild reads the file whole, and the kept session is in no file.
	if err := Ensure(dir, "aider", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"jitter", "loadSettings"} {
		if ss, _ := Search(dir, search.Options{Query: q}); len(ss) != 1 {
			t.Fatalf("%q after a rebuild: %#v", q, ss)
		}
	}
}

package index

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A session forgotten under the id an older deja gave it stays forgotten. aider
// ids were the history's path and the session's place in it, and became the
// path and the start time (#4332): the tombstone names the old id, and the
// rebuild that follows the upgrade indexed the session again under the new one.
func TestAForgottenAiderSessionStaysForgottenUnderItsNewID(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "aider")
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(proj, ".aider.chat.history.md")
	doc := "# aider chat started at 2026-09-30 10:00:00\n\n#### fix the retry loop in worker.go  \n\nChanged the loop to back off.\n\n" +
		"# aider chat started at 2026-09-30 11:00:00\n\n#### paste the staging token here  \n\nNoted the hunter2 token.\n"
	if err := os.WriteFile(hist, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("DEJA_AIDER_ROOTS", root)
	h := sha1.Sum([]byte(hist))
	legacy := "aider:aider-" + hex.EncodeToString(h[:6]) + "-2"
	if err := writeTombstones(map[string]bool{legacy: true}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	for _, rebuild := range []bool{true, false} {
		if err := Ensure(dir, "aider", rebuild, nil); err != nil {
			t.Fatal(err)
		}
		if ss, _ := Search(dir, search.Options{Query: "hunter2"}); len(ss) != 0 {
			t.Fatalf("the session forgotten as %s is back (rebuild=%v): %#v", legacy, rebuild, ss)
		}
		if ss, _ := Search(dir, search.Options{Query: "retry"}); len(ss) != 1 {
			t.Fatalf("the session nobody forgot is gone (rebuild=%v): %#v", rebuild, ss)
		}
		// The next pass re-reads the file after an append.
		f, err := os.OpenFile(hist, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString("\n#### and the hunter2 rotation  \n\nRotated.\n")
		_ = f.Close()
	}
}

// A session the re-read file still holds under another id has not left it. Two
// launches in one second are told apart by order, so taking the first out of
// the file hands the second the first's id: the second's old record was kept
// as one that had left the file, and the session was indexed twice.
func TestAnAiderSessionWithANewIDIsNotKeptTwice(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "aider")
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(proj, ".aider.chat.history.md")
	first := "# aider chat started at 2026-09-30 10:00:00\n\n#### the first launch  \n\nAnswered the alpha question.\n\n"
	second := "# aider chat started at 2026-09-30 10:00:00\n\n#### the second launch  \n\nAnswered the bravo question.\n"
	if err := os.WriteFile(hist, []byte(first+second), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
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
	if err := os.WriteFile(hist, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "aider", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "bravo"}); len(ss) != 1 {
		t.Fatalf("the session the file still holds is indexed %d times: %#v", len(ss), ss)
	}
}

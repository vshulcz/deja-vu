package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// One repo, two checkouts: sessions recorded in a linked worktree belong to the
// project, not to that directory. #2333 took bare-name suffix matching out of
// the scope rule, and this is the case that rule was written for — the
// candidates carry each worktree root's full name form, so it still holds from
// either side, including a fresh worktree with no history of its own.
func TestAWorktreeSharesTheProjectsHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)

	main := filepath.Join(tmp, "src", "app")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(main, "init", "-q")
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(main, "add", "README.md")
	git(main, "commit", "-qm", "seed")
	feature := filepath.Join(tmp, "wt", "app-feature")
	git(main, "worktree", "add", "-q", feature, "-b", "feature")

	// One session, recorded in the main checkout only.
	dirName := strings.ReplaceAll(main, string(filepath.Separator), "-")
	store := filepath.Join(root, dirName)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-6 * time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"onmain","timestamp":%q,"cwd":%q,`+
		`"message":{"role":"user","content":"the retry budget on main needs a cap"}}`, at, main)
	if err := os.WriteFile(filepath.Join(store, "onmain.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	fromMain := hookContextFor(t, dir, fmt.Sprintf(
		`{"hook_event_name":"SessionStart","cwd":%q,"session_id":"a1"}`, main))
	if !strings.Contains(fromMain, "retry budget on main") {
		t.Fatalf("the checkout's own session is missing, so this measures nothing:\n%s", fromMain)
	}
	fromWorktree := hookContextFor(t, dir, fmt.Sprintf(
		`{"hook_event_name":"SessionStart","cwd":%q,"session_id":"a2"}`, feature))
	if !strings.Contains(fromWorktree, "retry budget on main") {
		t.Errorf("a linked worktree does not see the repo's history:\n%s", fromWorktree)
	}
}

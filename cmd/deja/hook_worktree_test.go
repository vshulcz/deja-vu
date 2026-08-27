package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Work done in a linked worktree is work on the same project, and the session
// start there has to open with the same memory. `ProjectNameCandidates` knows
// that (internal/digest), but nothing held the hook itself to it — and the hook
// is where the project is decided: it reads CLAUDE_PROJECT_DIR, or the payload's
// cwd, and everything downstream follows that one string.
func TestASessionStartedInAWorktreeGetsTheProjectsMemory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	withStatsStores(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "checkout")
	side := filepath.Join(tmp, "checkout-fix")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "seed")
	git("worktree", "add", "-q", side, "-b", "fix")

	// The sessions were worked in the main checkout.
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "checkout", "one.jsonl"), "wtterm", []string{
		strings.TrimSpace(claudeRecord(t, map[string]any{
			"type": "user", "sessionId": "wtterm", "cwd": repo, "timestamp": old,
			"message": map[string]any{"role": "user", "content": "pgbouncer runs in transaction mode and prepared statements are off"},
		})),
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, where := range []struct{ name, dir string }{
		{"the main checkout", repo},
		{"a linked worktree", side},
	} {
		t.Run(where.name, func(t *testing.T) {
			t.Setenv("CLAUDE_PROJECT_DIR", where.dir)
			t.Chdir(where.dir)
			// A Windows path in a JSON string is a run of invalid escapes, and
			// the payload would not parse at all — the same reason
			// hook_cwd_test.go does this.
			withHookStdin(t, hookPayload(t, map[string]string{"source": "startup", "session_id": "ses_wt", "cwd": where.dir}))
			out := captureStdout(t, func() {
				if err := runHookContext(index.DefaultDir(), true); err != nil {
					t.Error(err)
				}
			})
			if !strings.Contains(out, "transaction mode") {
				t.Errorf("a session started in %s opened with no memory of the project:\n%q", where.name, out)
			}
		})
	}
}

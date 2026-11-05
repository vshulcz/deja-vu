package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func initReviewRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "pool.go"), []byte("package main\nfunc pool() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "pool.go")
	runGit("commit", "-m", "initial pool")
	return repo
}

func TestReviewSurfacesRepoNotesAndPriorSessions(t *testing.T) {
	hermeticEnv(t)
	repo := initReviewRepo(t)
	t.Setenv("DEJA_REPO_DIR", repo)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Seed a repo note in .deja/notes.jsonl and a Claude session touching pool.go.
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	if _, err := sources.AppendRepoNoteTagged(repo, "proj", "pool.go connections must be closed before retry", []string{"pool"}, now); err != nil {
		t.Fatal(err)
	}

	claudeFile := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-home-u-proj", "sess-pool-1234.jsonl")
	if err := os.MkdirAll(filepath.Dir(claudeFile), 0o755); err != nil {
		t.Fatal(err)
	}
	rec1 := `{"type":"user","sessionId":"sess-pool-1234","cwd":` + `"` + filepath.ToSlash(repo) + `"` + `,"timestamp":"2026-03-14T10:00:00Z","message":{"role":"user","content":"fix pool.go connection leak"}}`
	rec2 := `{"type":"assistant","sessionId":"sess-pool-1234","cwd":` + `"` + filepath.ToSlash(repo) + `"` + `,"timestamp":"2026-03-14T10:01:00Z","message":{"role":"assistant","content":"closed idle connections in pool.go before retry"}}`
	if err := os.WriteFile(claudeFile, []byte(rec1+"\n"+rec2+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Modify pool.go in the working tree so bare `deja review` inspects working-tree changes.
	if err := os.WriteFile(filepath.Join(repo, "pool.go"), []byte("package main\nfunc pool() { /* changed */ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "review", "--json")
	if err != nil {
		t.Fatalf("review --json: %v\n%s", err, out)
	}
	var env reviewEnvelopeJSON
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if env.Target != "working-tree" {
		t.Fatalf("target = %q, want working-tree", env.Target)
	}
	if len(env.ChangedFiles) != 1 || env.ChangedFiles[0] != "pool.go" {
		t.Fatalf("changed_files = %+v, want [pool.go]", env.ChangedFiles)
	}
	if len(env.Notes) == 0 || env.Notes[0].Origin != "repo" || !strings.Contains(env.Notes[0].Text, "pool.go connections") {
		t.Fatalf("notes = %+v, want repo note about pool.go", env.Notes)
	}
	if len(env.Files) != 1 || len(env.Files[0].Sessions) == 0 || env.Files[0].Sessions[0].Harness != "claude" {
		t.Fatalf("files = %+v, want claude session on pool.go", env.Files)
	}

	// Text output should also include both the repo note and the prior session.
	textOut, err := captureRun(t, "review", "HEAD")
	if err != nil {
		t.Fatalf("review HEAD: %v\n%s", err, textOut)
	}
	for _, want := range []string{"[repo]", "pool.go connections", "Prior sessions:", "pool.go", "[claude]"} {
		if !strings.Contains(textOut, want) {
			t.Errorf("review HEAD text missing %q:\n%s", want, textOut)
		}
	}
}

func TestReviewFlagValidation(t *testing.T) {
	hermeticEnv(t)
	for _, bad := range [][]string{
		{"review", "--limit", "0"},
		{"review", "--limit"},
		{"review", "--project"},
		{"review", "--harness", "not-a-harness"},
		{"review", "--unknown"},
		{"review", "HEAD", "HEAD~1"},
	} {
		if _, err := captureRun(t, bad...); err == nil {
			t.Fatalf("%v: expected error", bad)
		}
	}
}

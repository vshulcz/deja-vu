package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestTaskScoresRanksByFileOverlap(t *testing.T) {
	ss := []model.Session{
		{Harness: "claude", ID: "old", Title: "jwt refresh", Messages: []model.Message{
			{Role: "user", Text: "the expiry bug lives in auth.go and jwks.go"},
		}},
		{Harness: "claude", ID: "new", Title: "css tweaks", Messages: []model.Message{
			{Role: "user", Text: "make the landing hero wider"},
		}},
	}
	scores, matched := taskScores(ss, []string{"auth.go", "jwks.go"})
	if scores["claude:old"] != 2 || scores["claude:new"] != 0 {
		t.Fatalf("scores = %#v, want old=2 new=0", scores)
	}
	if strings.Join(matched, ",") != "auth.go,jwks.go" {
		t.Fatalf("matched = %v", matched)
	}
}

func TestTaskScoresEmptyWithoutSignal(t *testing.T) {
	ss := []model.Session{{Harness: "claude", ID: "s", Title: "anything"}}
	if scores, matched := taskScores(ss, nil); scores != nil || matched != nil {
		t.Fatalf("no files must mean no scores, got %#v %v", scores, matched)
	}
	if scores, matched := taskScores(ss, []string{"auth.go"}); scores != nil || matched != nil {
		t.Fatalf("zero overlap must return nil, got %#v %v", scores, matched)
	}
}

func TestChangedTaskFilesReadsGitState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "auth.go"), []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.sum"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "seed")
	if err := os.WriteFile(filepath.Join(repo, "jwks.go"), []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := changedTaskFiles(repo)
	set := map[string]bool{}
	for _, f := range got {
		set[f] = true
	}
	if !set["jwks.go"] || !set["auth.go"] {
		t.Fatalf("want uncommitted jwks.go and committed auth.go, got %v", got)
	}
	if set["go.sum"] {
		t.Fatalf("lockfile noise must be filtered, got %v", got)
	}
}

func TestChangedTaskFilesOutsideRepo(t *testing.T) {
	if got := changedTaskFiles(t.TempDir()); got != nil {
		t.Fatalf("outside a repo must return nil, got %v", got)
	}
}

func TestHookContextReceiptNamesTaskFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	repo := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "auth.go"), []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := sources.ClaudeProjectName(repo)
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	fresh := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, proj, "task.jsonl"), "task", []string{
		`{"type":"user","sessionId":"task","timestamp":"` + old + `","message":{"role":"user","content":"token expiry check in auth.go uses < instead of <= and drops the last second"}}`,
		`{"type":"assistant","sessionId":"task","timestamp":"` + old + `","message":{"role":"assistant","content":[{"type":"text","text":"fixed the comparison in auth.go and added a regression test for expiry"}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, proj, "other.jsonl"), "other", []string{
		`{"type":"user","sessionId":"other","timestamp":"` + fresh + `","message":{"role":"user","content":"reword the landing hero copy and center the pricing table"}}`,
		`{"type":"assistant","sessionId":"other","timestamp":"` + fresh + `","message":{"role":"assistant","content":[{"type":"text","text":"updated the hero copy and pricing layout styles"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", repo)

	digest, sessions, _, matched := hookDigestResult(dir)
	if sessions == 0 || digest == "" {
		t.Fatal("expected recall output")
	}
	if len(matched) == 0 || matched[0] != "auth.go" {
		t.Fatalf("matched = %v, want auth.go first", matched)
	}
	authPos := strings.Index(digest, "expiry")
	heroPos := strings.Index(digest, "hero")
	if authPos == -1 {
		t.Fatalf("task-matched session missing from digest:\n%s", digest)
	}
	if heroPos != -1 && heroPos < authPos {
		t.Fatalf("task-matched session must outrank fresher unrelated one:\n%s", digest)
	}
}

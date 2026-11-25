package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/hookengine"
	"github.com/vshulcz/deja-vu/internal/index"
)

func TestDoctorCodexHookExecutedRecognizesAgyPrefix(t *testing.T) {
	hermeticEnv(t)
	codexHome := filepath.Join(t.TempDir(), "codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)

	histPath := filepath.Join(codexHome, "history.jsonl")
	if err := os.WriteFile(histPath, []byte(`{"session_id":"conv-42","text":"check hook state","ts":1700000000}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	earlier := now.Add(-2 * time.Minute)
	if err := os.Chtimes(hooksPath, earlier, earlier); err != nil {
		t.Fatal(err)
	}

	dir := index.DefaultDir()
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	line := "agy:conv-42 agy-digest " + now.Format(time.RFC3339) + "\n"
	if err := os.WriteFile(hookseenPath(dir), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	if !codexHookExecuted(hooksPath) {
		t.Fatal("codexHookExecuted missed timestamped agy: entry in .hookseen")
	}
}

func TestForgetOnCheckpointConcurrentWithRememberInjectedRace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	rememberInjectedIDsFor(dir, "conv-race", "proj", []string{"compaction:keep-mark"})

	var wg sync.WaitGroup
	for g := range 12 {
		wg.Go(func() {
			for i := range 20 {
				rememberInjectedIDs(dir, "conv-race", fmt.Sprintf("q:%d-%d", g, i))
				rememberInjectedIDsFor(dir, antigravityDigestKey("conv-race"), "proj", []string{fmt.Sprintf("hit-%d-%d", g, i)})
				_ = alreadyInjected(dir, "conv-race")
			}
		})
	}
	for g := range 4 {
		wg.Go(func() {
			for i := range 10 {
				forgetOnCheckpoint(dir, "conv-race", fmt.Sprintf("marker-%d-%d", g, i))
			}
		})
	}
	wg.Wait()

	raw, err := os.ReadFile(hookseenPath(dir))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", hookseenPath(dir), err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatal(".hookseen left empty or missing trailing newline")
	}
	if !alreadyInjected(dir, "conv-race")["compaction:keep-mark"] {
		t.Fatal("compaction delivery mark lost under concurrent forgetOnCheckpoint race")
	}
}

func TestAntigravityCoalesceAndCommandStreakLifecycle(t *testing.T) {
	hermeticEnv(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	withFrozenCoordClock(t, now)

	steps := coalesceAntigravitySteps([]antigravityInjectStep{
		{EphemeralMessage: "   "},
		{EphemeralMessage: frameRecall("prior fact")},
		{EphemeralMessage: frameRecall("prior fact")},
		{EphemeralMessage: frameRecall(evidence.LabelUserRule + " always run -race")},
		{EphemeralMessage: "tail reminder"},
	})
	if len(steps) != 1 {
		t.Fatalf("coalesceAntigravitySteps len = %d, want 1", len(steps))
	}
	msg := steps[0].EphemeralMessage
	if count := strings.Count(msg, "<deja-recall>"); count != 1 {
		t.Fatalf("expected 1 recall frame delimiter, got %d:\n%s", count, msg)
	}
	if !strings.HasPrefix(msg, hookengine.RuleFrameHeader) {
		t.Fatalf("expected RuleFrameHeader for merged step:\n%s", msg)
	}
	if strings.Count(msg, "prior fact") != 1 {
		t.Fatalf("duplicate block kept across merged steps:\n%s", msg)
	}

	repo := filepath.Join(t.TempDir(), "repo")
	makeFakeGitRepo(t, repo)
	dir := index.DefaultDir()
	if got := coordRecordCommandOutcome(dir, repo, "conv-agy", "go test ./...", true); got != 1 {
		t.Fatalf("first failed command streak = %d, want 1", got)
	}
	if got := coordRecordCommandOutcome(dir, repo, "conv-agy", "go test ./...", true); got != 2 {
		t.Fatalf("second failed command streak = %d, want 2", got)
	}
	if got := coordRecordCommandOutcome(dir, repo, "conv-agy", "go test ./...", false); got != 0 {
		t.Fatalf("passing verify command streak = %d, want 0", got)
	}

	st := loadCoordState(dir)
	sess := st.Sessions["conv-agy"]
	if sess == nil || sess.VerifyCmd == "" {
		t.Fatalf("expected recorded verification command after reset: %+v", sess)
	}
}

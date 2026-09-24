package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// wordsFixStore holds a session that hit an error and answered it in prose:
// what to set and why, with no command run afterwards. fix pairs an error with
// a command, so this session gives it no pair at all.
func wordsFixStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-w")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for _, id := range []string{"w1", "w2"} {
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/w","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the suite will not run: license check failed: SVC_DEV_TOKEN is missing or wrong"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/w","timestamp":"2026-07-21T10:01:00Z","message":{"role":"assistant","content":"license check failed: SVC_DEV_TOKEN is missing or wrong means the token is not in the tree. It lives in the shared vault entry svc-dev, and the suite reads it from the environment — export SVC_DEV_TOKEN before running anything, there is no file to add."}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A session that explains the remedy instead of running it used to make fix a
// dead end: "no session on this machine ran a command after that error", over
// an index whose own words hold the answer. In a 12-run A/B the one deja run
// that failed the task failed here — the model called fix, read that sentence
// and stopped (#3947).
func TestFixAnswersWithWhatWasSaidWhenNothingWasRun(t *testing.T) {
	dir := wordsFixStore(t)

	got, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"license check failed: SVC_DEV_TOKEN is missing or wrong"}`))
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if !strings.Contains(got, "SVC_DEV_TOKEN") || !strings.Contains(got, "vault") {
		t.Errorf("the session that answered the error in words is not on the page:\n%s", got)
	}
	// Said, not implied: under a fix call a page of sessions with no such line
	// reads as commands to run, and nobody ran these after this error.
	if !strings.Contains(got, "No session ran a command after that error") {
		t.Errorf("the answer does not say which of the two it is:\n%s", got)
	}
	if strings.Contains(got, "ran next:") {
		t.Errorf("prose was handed over as a command that ran:\n%s", got)
	}
}

// The fallback is a page of transcript text, so it stays inside the budget the
// tool's own description promises and keeps the frame that marks it as data.
func TestTheFallbackPageIsFramedAndFitsTheBudget(t *testing.T) {
	dir := wordsFixStore(t)

	got, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"license check failed: SVC_DEV_TOKEN is missing or wrong"}`))
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if len(got) > recallMCPBudget {
		t.Errorf("the fallback answered with %d bytes, over the %d budget", len(got), recallMCPBudget)
	}
	if !strings.HasPrefix(got, recallFrameHeader) {
		t.Errorf("the page is not framed as transcript data:\n%s", got)
	}
}

// An error nothing in the store talks about still gets the plain sentence: the
// fallback is a second answer, not a licence to return unrelated sessions.
func TestAnErrorNobodyMentionsStillGetsThePlainAnswer(t *testing.T) {
	dir := wordsFixStore(t)

	got, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"zonkobuffer overflow at shard 7"}`))
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if strings.Contains(got, "SVC_DEV_TOKEN") {
		t.Errorf("an unrelated session was served as the answer:\n%s", got)
	}
	if !strings.Contains(got, "No session on this machine ran a command after that error") {
		t.Errorf("the plain answer is gone:\n%s", got)
	}
}

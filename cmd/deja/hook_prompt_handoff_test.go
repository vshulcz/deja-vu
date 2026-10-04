package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A new session that opens by asking to carry on another one gets that
// session's state, on its first prompt only and only when the prompt names the
// session or its harness.
func TestTheFirstPromptThatAsksToContinueASessionGetsItsPacket(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "src.jsonl"), "c0ffee11-src", []string{
		`{"type":"user","sessionId":"c0ffee11-src","timestamp":"` + old +
			`","message":{"role":"user","content":"the nightly export drops rows past the first page"}}`,
		`{"type":"assistant","sessionId":"c0ffee11-src","timestamp":"` + old +
			`","message":{"role":"assistant","content":[{"type":"text","text":"Root cause: the cursor resets after page one; fixed by carrying the page token."}]}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	ask := func(sid, prompt string) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"prompt": prompt, "session_id": sid})
		var out bytes.Buffer
		if err := runHookPromptMode(index.DefaultDir(), bytes.NewReader(payload), &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	byID := ask("new-1", "перенеси сюда сессию c0ffee11-src и продолжай")
	if !strings.Contains(byID, "carrying the page token") || !strings.Contains(byID, "this prompt names claude session") {
		t.Fatalf("a first prompt naming a session id did not get that session's packet:\n%s", byID)
	}
	if !strings.Contains(byID, "untrusted reference data") {
		t.Errorf("the packet went in without the frame:\n%s", byID)
	}
	// Once per session: the same request again is not a second handoff.
	if again := ask("new-1", "перенеси сюда сессию c0ffee11-src и продолжай"); strings.Contains(again, "this prompt names") {
		t.Errorf("the same handoff went in twice:\n%s", again)
	}

	byHarness := ask("new-2", "continue the claude session from before")
	if !strings.Contains(byHarness, "carrying the page token") {
		t.Errorf("a first prompt naming the harness did not get its newest session here:\n%s", byHarness)
	}

	// "Continue" on its own is the commonest turn there is, and not a handoff.
	if got := ask("new-3", "continue with the export fix"); strings.Contains(got, "this prompt names") {
		t.Errorf("a plain continue was read as a handoff:\n%s", got)
	}
	// A pasted command that resumes a session is not a request to continue it.
	if got := ask("new-4", "в сессии claude --resume c0ffee11-src удали два последних сообщения"); strings.Contains(got, "this prompt names") {
		t.Errorf("a request about a session was read as a handoff of it:\n%s", got)
	}
}

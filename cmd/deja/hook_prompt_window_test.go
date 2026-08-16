package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The per-prompt hook asks the ranking for a fixed number of candidates and
// then filters them: the trust policy can withhold one, a weak match is
// dropped, a marathon session can narrow to nothing, the session being written
// is never recalled to itself, and anything already injected this conversation
// is skipped. Only two survivors are ever used.
//
// The window was sized against two of those — its comment said "to leave room
// after excluding the current/too-fresh sessions" — and three more arrived
// afterwards. When the filtered ones fill it, the answer below them is never
// looked at and the hook says nothing at all, on a machine where it is
// installed and reported healthy.
//
// Staged on the weak-match filter, which needs no state and is always on: nine
// sessions carry the question's ordinary words and outrank the one session
// carrying its rare one, exactly the way a plainly worded question ranks.
func TestTheHookLooksPastTheCandidatesItWillNotUse(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	write := func(id, text string) {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + old +
				`","message":{"role":"user","content":"` + text + `"}}`,
		})
	}
	// Enough history for the shared term to still count as rare.
	for i := 0; i < 200; i++ {
		write(fmt.Sprintf("noise%03d", i), "notes on the weekly planning meeting and the roadmap")
	}
	// Ten sessions that all answer the question, so the ranking has no reason to
	// prefer one of them and every one clears the bar to be injected.
	shown := make([]string, 0, 9)
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("probe%02d", i)
		write(id, "the hydrostat_probe reported a stale pressure reading again")
		if i < 9 {
			shown = append(shown, id)
		}
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	// Nine have already been shown in this conversation, which is the ordinary
	// state by the fourth or fifth prompt rather than a contrived one.
	var seen strings.Builder
	for _, id := range shown {
		fmt.Fprintf(&seen, "agent-1 %s\n", id)
	}
	if err := os.WriteFile(index.DefaultDir()+".hookseen", []byte(seen.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"stale pressure reading from the hydrostat_probe again","session_id":"agent-1"}`)
	if err := runHookPrompt(index.DefaultDir(), in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("the candidates the hook cannot use filled its window and the one it could was never looked at: " +
			"nothing was recalled")
	}
	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("hook output is not the expected envelope: %v\n%s", err, out.String())
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "hydrostat_probe") {
		t.Errorf("the session that answers the question was reached but not recalled:\n%s",
			resp.HookSpecificOutput.AdditionalContext)
	}
}

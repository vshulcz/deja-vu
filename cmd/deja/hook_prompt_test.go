package main

import (
	"bytes"
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestHookPromptInjectsOnRelevantHit(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"the long beta session broke again"}`)
	if err := runHookPrompt(in, &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("bad json %q: %v", out.String(), err)
	}
	if resp.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Fatalf("event = %q", resp.HookSpecificOutput.HookEventName)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "deja-vu recalled") && !strings.Contains(ctx, "deja found prior sessions") {
		t.Fatalf("context missing narration lead: %q", ctx)
	}
	if len(ctx) > promptHookBudget+256 {
		t.Fatalf("injection too large: %d", len(ctx))
	}
}

func TestHookPromptSilentPaths(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	for name, prompt := range map[string]string{
		"no meaningful terms": `{"prompt":"ok do it"}`,
		"no matches":          `{"prompt":"quetzalcoatl zeppelin framework meltdown"}`,
		"empty":               `{}`,
		"garbage":             `not json at all`,
	} {
		var out bytes.Buffer
		if err := runHookPrompt(strings.NewReader(prompt), &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out.Len() != 0 {
			t.Fatalf("%s: expected silence, got %q", name, out.String())
		}
	}
}

func TestPromptSearchTerms(t *testing.T) {
	got := promptSearchTerms("Why is the connection pool exhausted again in the gateway???")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "connection") || !strings.Contains(joined, "pool") || !strings.Contains(joined, "gateway") {
		t.Fatalf("terms = %v", got)
	}
	if len(promptSearchTerms("a of to")) != 0 {
		t.Fatal("stop words must not produce terms")
	}
}

func TestPromptCandidatesOrderAndPairs(t *testing.T) {
	got := promptCandidates([]string{"pool", "connection", "gateway"})
	if got[0] != "connection gateway pool" {
		t.Fatalf("first candidate = %q", got[0])
	}
	joined := strings.Join(got, "|")
	for _, pair := range []string{"connection gateway", "connection pool", "gateway pool"} {
		if !strings.Contains(joined, pair) {
			t.Fatalf("missing pair %q in %v", pair, got)
		}
	}
}

func TestLimitHandoffTip(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", "lim.jsonl"), "lim", []string{
		`{"type":"user","sessionId":"lim","timestamp":"` + now + `","message":{"role":"user","content":"continue please"}}`,
		`{"type":"assistant","sessionId":"lim","timestamp":"` + now + `","message":{"role":"assistant","content":"You have reached your usage limit reached for today"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	recent, err := index.Recent(index.DefaultDir(), 1)
	if err != nil || len(recent) == 0 {
		t.Fatalf("recent: %v %v", recent, err)
	}
	t.Logf("newest: id=%s updated=%v msgs=%d", recent[0].ID, recent[0].Updated, len(recent[0].Messages))
	tip := limitHandoffTip()
	if !strings.Contains(tip, "usage limit") || !strings.Contains(tip, "deja handoff") {
		t.Fatalf("tip = %q", tip)
	}
}

func TestSSHSyncTipThresholdAndOnce(t *testing.T) {
	withStatsStores(t)
	var ss []model.Session
	for i := 0; i < 6; i++ {
		ss = append(ss, model.Session{ID: strconv.Itoa(i), Messages: []model.Message{{Role: "user", Text: "run ssh mini and check"}}})
	}
	tip := sshSyncTip(ss)
	if !strings.Contains(tip, "deja sync ssh") {
		t.Fatalf("tip = %q", tip)
	}
	if again := sshSyncTip(ss); again != "" {
		t.Fatalf("tip must show once, got %q", again)
	}
	// Below threshold: silent (fresh sentinel dir).
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "idx"))
	if tip := sshSyncTip(ss[:2]); tip != "" {
		t.Fatalf("below threshold tip = %q", tip)
	}
}

func TestHookPromptCitationAndDedupe(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	in := `{"prompt":"the long beta session broke again","session_id":"agent-1"}`
	var out bytes.Buffer
	if err := runHookPrompt(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `If it helped, say: \"deja-vu recalled:`) {
		t.Fatalf("citation line missing: %q", out.String())
	}
	// Same session asks again: the same memory must not be re-injected.
	var out2 bytes.Buffer
	if err := runHookPrompt(strings.NewReader(in), &out2); err != nil {
		t.Fatal(err)
	}
	if out2.Len() != 0 {
		t.Fatalf("repeat injection for same session: %q", out2.String())
	}
	// A different agent session still gets it.
	var out3 bytes.Buffer
	if err := runHookPrompt(strings.NewReader(`{"prompt":"the long beta session broke again","session_id":"agent-2"}`), &out3); err != nil {
		t.Fatal(err)
	}
	if out3.Len() == 0 {
		t.Fatal("fresh session should still receive the memory")
	}
}

func TestAgentCreditsCountedFromIndex(t *testing.T) {
	now := time.Now()
	ss := []model.Session{{ID: "a", Messages: []model.Message{
		{Role: "assistant", Text: "deja-vu recalled: jwt fix — reusing it.", Time: now},
		{Role: "assistant", Text: "deja-vu recalled: old one", Time: now.Add(-9 * 24 * time.Hour)},
		{Role: "user", Text: "deja-vu recalled should not count from users"},
	}}}
	r := buildStats(ss, now)
	if r.AgentCredits != 2 || r.WeekCredits != 1 {
		t.Fatalf("credits = %d/%d, want 2/1", r.AgentCredits, r.WeekCredits)
	}
}

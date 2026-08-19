package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// twoDecisionStore writes a session that decided one thing and then decided
// otherwise, so the answer under the excerpt and the newest conclusion are the
// same sentence.
func twoDecisionStore(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	turns := []struct{ role, text string }{
		{"user", "how do we handle the retry queue backoff"},
		{"assistant", "FIRST: we used a fixed one second delay and it synchronised every worker."},
		{"user", "filler about something else entirely"},
		{"assistant", "unrelated chatter that says nothing about the decision at hand."},
		{"user", "what did we decide about the retry queue backoff in the end"},
		{"assistant", "SECOND: we settled on full jitter with three attempts, then give up."},
	}
	var b strings.Builder
	for i, m := range turns {
		b.WriteString(`{"type":"` + m.role + `","sessionId":"a","timestamp":"2026-01-02T03:0` +
			string(rune('0'+i)) + `:00Z","message":{"role":"` + m.role + `","content":"` + m.text + `"}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The answer under the excerpt and the newest conclusion are usually the same
// sentence, and it was printed twice — 16% of a small payload spent saying one
// thing, while the conclusions list lost one of its three slots (#1319).
func TestRecallDoesNotPayTwiceForOneAnswer(t *testing.T) {
	dir := twoDecisionStore(t)
	text, _, _, _, err := recallTextResult(dir, "decide retry queue backoff end", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), "- "))
		if !strings.HasPrefix(ln, "→ ") {
			continue
		}
		seen[ln]++
	}
	for line, n := range seen {
		if n > 1 {
			t.Errorf("the payload says this %d times: %q", n, line)
		}
	}
	// The freed slot goes to the fact that was pushed out.
	if !strings.Contains(text, "SECOND") {
		t.Errorf("the answer is missing:\n%s", text)
	}
	if !strings.Contains(text, "FIRST") {
		t.Errorf("the earlier decision was dropped rather than promoted:\n%s", text)
	}
}

// A conclusion that is not the answer stays, and a hit with no answer line at
// all keeps every conclusion.
func TestConclusionsSurviveWhenTheyAreNotTheAnswer(t *testing.T) {
	if got := withoutShownAnswer([]string{"we capped retries at three"}, []string{"the retry queue stalls"}); len(got) != 1 {
		t.Errorf("a conclusion was dropped against a plain excerpt: %q", got)
	}
	if got := withoutShownAnswer([]string{"we capped retries at three"}, nil); len(got) != 1 {
		t.Errorf("a conclusion was dropped with no excerpts at all: %q", got)
	}
	// The two paths can trim the same sentence differently, so a prefix counts
	// — in both directions, since either side can be the longer one.
	long := "we capped retries at three attempts with full jitter. then we raised it."
	shortForm := "we capped retries at three attempts with full jitter."
	if got := withoutShownAnswer([]string{shortForm}, []string{"→ " + long}); len(got) != 0 {
		t.Errorf("the conclusion was the shorter form and was printed twice: %q", got)
	}
	if got := withoutShownAnswer([]string{long}, []string{"→ " + shortForm}); len(got) != 0 {
		t.Errorf("the conclusion was the longer form and was printed twice: %q", got)
	}
	// A shared opening is not a shared fact.
	if got := withoutShownAnswer([]string{"we decided to use exponential backoff"}, []string{"→ we decided to"}); len(got) != 1 {
		t.Errorf("a distinct conclusion was dropped for sharing an opening: %q", got)
	}
}

// Dropping the repeat must not cost the block a line: the conclusions are asked
// for one more than they show, so three still arrive when the session has four
// things to say.
func TestTheFreedSlotIsFilled(t *testing.T) {
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	turns := []struct{ role, text string }{
		// The matched question comes last, so its answer is also the newest
		// conclusion — which is the case where the repeat costs a slot.
		{"user", "the timeouts"},
		{"assistant", "TIMEOUTS: the read timeout stays at thirty seconds for now."},
		{"user", "the pool"},
		{"assistant", "POOL: capped the connection pool at sixteen per worker."},
		{"user", "the alerts"},
		{"assistant", "ALERTS: page only on the second consecutive failure."},
		{"user", "what did we decide about the retry queue backoff in the end"},
		{"assistant", "ANSWER: we settled on full jitter with three attempts, then give up."},
	}
	var b strings.Builder
	for i, m := range turns {
		b.WriteString(`{"type":"` + m.role + `","sessionId":"a","timestamp":"2026-01-02T03:0` +
			string(rune('0'+i)) + `:00Z","message":{"role":"` + m.role + `","content":"` + m.text + `"}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, _, _, _, err := recallTextResult(dir, "decide retry queue backoff end", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	block := text[strings.Index(text, "what this session concluded:"):]
	if n := strings.Count(block, "→ "); n != 3 {
		t.Errorf("the block shows %d conclusions, want 3:\n%s", n, block)
	}
	if strings.Count(text, "ANSWER: we settled") != 1 {
		t.Errorf("the answer is repeated or missing:\n%s", text)
	}
}

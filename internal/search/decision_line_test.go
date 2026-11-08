package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A long session mentions its subject in passing before it decides anything
// about it. The block has to quote the line that concluded, not the first line
// that happened to say the word.
//
// Five ways of choosing by where and how often a word fell were measured on a
// real store and none moved the number: rarity within the session, frequency
// within it, earliest against latest, and how many times the line repeats the
// word. All of them read "I'll look at the shard later" and "we moved the shard
// to the region" as the same line.
// The same in Russian. The markers were an English list, so every line of a
// Russian session read as a passing mention — and half the sessions on a real
// store are Russian.
func TestBlockPrefersTheRussianLineThatConcluded(t *testing.T) {
	at := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	msgs := []model.Message{
		{Role: "assistant", Text: "потом посмотрю про шардирование, пока не трогаю", Time: at},
		{Role: "assistant", Text: "снова про шардирование, руки не дошли", Time: at.Add(time.Minute)},
		{Role: "assistant", Text: "в итоге шардирование перенесли на регион", Time: at.Add(2 * time.Minute)},
	}
	s := model.Session{ID: "one", Harness: "claude", Project: "app", Started: at, Updated: at, Messages: msgs}
	block := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"шардирование"})
	if !strings.Contains(block, "перенесли на регион") {
		t.Fatalf("the decision was dropped in favour of two passing mentions:\n%s", block)
	}
}

func TestBlockPrefersTheLineThatConcluded(t *testing.T) {
	at := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	msgs := []model.Message{
		{Role: "assistant", Text: "will look at the quicksilver thing later", Time: at},
		{Role: "assistant", Text: "quicksilver again, still open", Time: at.Add(time.Minute)},
		{Role: "assistant", Text: "the fix: quicksilver retries are capped at four", Time: at.Add(2 * time.Minute)},
	}
	s := model.Session{ID: "one", Harness: "claude", Project: "app", Started: at, Updated: at, Messages: msgs}
	block := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"quicksilver"})
	// The block holds two lines and there are three to choose from. Which of
	// them leads is decided elsewhere — they are printed in the order they were
	// said, so that two conclusions read as a sequence. What this asserts is
	// that the line which concluded is one of the two, where before it was the
	// one dropped: all three matched the query once, and the tie went to
	// whichever came first.
	if !strings.Contains(block, "capped at four") {
		t.Fatalf("the decision was dropped in favour of two passing mentions:\n%s", block)
	}
}

func TestBlockPrefersTrajectoryVerifiedConclusionWithoutMarkers(t *testing.T) {
	at := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	msgs := []model.Message{
		{Role: "user", Text: "investigate quicksilver retry storm", Time: at},
		{Role: "assistant", Text: "checking quicksilver retry logs now", Time: at.Add(time.Minute)},
		{Role: "assistant", Text: "quicksilver backoff still reproducing locally", Time: at.Add(2 * time.Minute)},
		{Role: "edit", Text: "internal/quicksilver/retry.go", Time: at.Add(3 * time.Minute)},
		{Role: "command", Text: "go test ./internal/quicksilver/... → exit 0", Time: at.Add(4 * time.Minute)},
		{Role: "assistant", Text: "Updated `internal/quicksilver/retry.go` to cap quicksilver jitter attempts at 4.", Time: at.Add(5 * time.Minute)},
	}
	s := model.Session{ID: "traj", Harness: "claude", Project: "app", Started: at, Updated: at, Messages: msgs}
	block := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"quicksilver"})
	if !strings.Contains(block, "internal/quicksilver/retry.go") {
		t.Fatalf("expected trajectory-verified synthesis to be selected into recall block, got:\n%s", block)
	}
}

func TestIsBoilerplateToolOutputAndAutoRecallTruncation(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"Output: [read direct | 5ms]", true},
		{"Output: Showing lines 1-50 of 100", true},
		{"Output: Created file.", true},
		{"Output: Made 1 replacement in foo.go", true},
		{"Output: Made 3 replacements in bar.go", true},
		{"Output: Made progress on the task", false},
		{"Output: real error output", false},
		{"Plain assistant line", false},
	} {
		if got := isBoilerplateToolOutput(tc.line); got != tc.want {
			t.Errorf("isBoilerplateToolOutput(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}

	at := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	ss := []model.Session{
		{
			ID: "s1", Harness: "claude", Project: "app", Started: at, Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "configure quicksilver retry policy", Time: at},
				{Role: "assistant", Text: "The fix: quicksilver retry policy now uses exponential jitter.", Time: at.Add(time.Minute)},
			},
		},
		{
			ID: "s2", Harness: "claude", Project: "app", Started: at, Updated: at.Add(2 * time.Minute),
			Messages: []model.Message{
				{Role: "user", Text: "check quicksilver connection limits", Time: at},
				{Role: "assistant", Text: "We decided to cap quicksilver connection pool at 32 per worker.", Time: at.Add(time.Minute)},
			},
		},
	}
	res := BuildAutoRecall(ss, AutoRecallOptions{Mode: RecallSafe, ProjectNames: []string{"app"}, Now: at.Add(time.Hour)})
	if res.Text == "" {
		t.Fatal("expected non-empty BuildAutoRecall text")
	}
	block := AutoRecallDigestFor(ss, 160, []string{"quicksilver"})
	if block == "" {
		t.Fatal("expected non-empty AutoRecallDigestFor block")
	}
	if got := SafeLine("hello\u2800world"); got != "hello world" {
		t.Fatalf("SafeLine braille blank = %q, want 'hello world'", got)
	}
	evSess := model.Session{
		ID: "ev", Harness: "claude", Project: "app",
		Messages: []model.Message{
			{Role: "command", Text: "$ go test ./..."},
			{Role: "tool-output", Text: "Showing lines 1-10\nPASS"},
		},
	}
	_ = evidenceRewrite(evSess, "", []string{"Ran: `go test ./...`", "Output: Showing lines 1-10", "Output: PASS"}, true)
	_ = WithoutOwnReport("déjà vu: prior note\nactual line")
}

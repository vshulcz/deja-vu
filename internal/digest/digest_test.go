package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestShareFilteringHelpers(t *testing.T) {
	longProse := strings.Repeat("this readable sentence has enough words ", 4)
	longDigits := strings.Repeat("1234567890", 10)
	for _, tc := range []struct {
		name string
		line string
		want bool
	}{
		{"digest.Short", "fix parser", true},
		{"prose", longProse, true},
		{"mostly digits", longDigits, false},
		{"empty", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeProse(tc.line); got != tc.want {
				t.Fatalf("looksLikeProse = %v, want %v", got, tc.want)
			}
		})
	}

	if !looksLikeDataDump("{" + strings.Repeat(`"k":"v",`, 80) + "}") {
		t.Fatal("expected long json dump")
	}
	if !looksLikeDataDump(strings.Repeat("x", 201)) {
		t.Fatal("expected long token dump")
	}
	if looksLikeDataDump("ordinary explanation with spaces") {
		t.Fatal("ordinary prose marked as dump")
	}

	for _, s := range []string{"", "<task-notification x>", `{"tool_use":true}`, strings.Repeat("x", 201)} {
		if !noisyMessage(s) {
			t.Fatalf("expected noisy message %q", s)
		}
	}
	if noisyMessage("user explains the regression") {
		t.Fatal("plain message marked noisy")
	}
	if !noiseLine("file.go:18: match") || !noiseLine("  123 diff --git a b") {
		t.Fatal("expected noisy lines")
	}
}

func TestHandoffTailEmptyAndBudget(t *testing.T) {
	if got := tailSection(model.Session{}, 100); got != "" {
		t.Fatalf("empty session tail = %q", got)
	}
	s := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: strings.Repeat("x", 500)},
	}}
	if got := tailSection(s, 40); len(got) > 40 {
		t.Fatalf("tail ignored budget: %d bytes", len(got))
	}
	if got := tailSection(s, 0); got != "" {
		t.Fatalf("zero budget tail = %q", got)
	}
}

func TestHandoffCleanDropsPreamblesAndRepeats(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "<environment_context><cwd>/x</cwd></environment_context>"},
		{Role: "user", Text: "hi"},
		{Role: "user", Text: "hi"},
		{Role: "user", Text: "Comments on artifact URI: file:///brain/plan.md approved"},
		{Role: "user", Text: "real question about retries"},
	}}
	got := cleanSession(s)
	if len(got.Messages) != 2 || got.Messages[0].Text != "hi" || got.Messages[1].Text != "real question about retries" {
		t.Fatalf("cleaned = %#v", got.Messages)
	}
}

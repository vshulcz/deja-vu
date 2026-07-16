package main

import (
	"bytes"
	"os"
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
		{"short", "fix parser", true},
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
		if !noisyShareMessage(s) {
			t.Fatalf("expected noisy message %q", s)
		}
	}
	if noisyShareMessage("user explains the regression") {
		t.Fatal("plain message marked noisy")
	}
	if !noiseLine("file.go:18: match") || !noiseLine("  123 diff --git a b") {
		t.Fatal("expected noisy lines")
	}
}

func TestStatsFormattingHelpers(t *testing.T) {
	if got := trimRunes("abcdef", 3); got != "ab…" {
		t.Fatalf("trimRunes = %q", got)
	}
	if got := trimRunes("abcdef", 1); got != "a" {
		t.Fatalf("trimRunes n=1 = %q", got)
	}
	if got := scaledBar(1, 100, 10); got != 1 {
		t.Fatalf("scaledBar = %d", got)
	}
	if got := scaledBar(0, 100, 10); got != 0 {
		t.Fatalf("scaledBar zero = %d", got)
	}
	if valueOrDash("") != "-" || valueOrDash("x") != "x" {
		t.Fatal("valueOrDash mismatch")
	}
	if !statNoise("<local-command x>") || statNoise("real title") {
		t.Fatal("statNoise mismatch")
	}
	if got := statTitle(model.Session{Title: "<command-noise>", Messages: []model.Message{{Role: "assistant", Text: "skip"}, {Role: "user", Text: strings.Repeat("word ", 20)}}}); !strings.HasSuffix(got, "…") {
		t.Fatalf("statTitle = %q", got)
	}
	if got := statTitle(model.Session{Title: "Clean title"}); got != "Clean title" {
		t.Fatalf("statTitle title = %q", got)
	}
	if got := statHarnessTag("claude", true); !strings.Contains(got, "[claude]") || !strings.Contains(got, statOrange) {
		t.Fatalf("claude tag = %q", got)
	}
	if got := statHarnessTag("codex", true); !strings.Contains(got, statGreen) {
		t.Fatalf("codex tag = %q", got)
	}
	if got := statHarnessTag("opencode", true); !strings.Contains(got, statBlue) {
		t.Fatalf("opencode tag = %q", got)
	}
	if got := statHarnessTag("other", true); got != "[other]" {
		t.Fatalf("other tag = %q", got)
	}
	t.Setenv("NO_COLOR", "1")
	if statColorOK(os.Stdout) || statColorOK(&bytes.Buffer{}) {
		t.Fatal("color should be disabled")
	}
}

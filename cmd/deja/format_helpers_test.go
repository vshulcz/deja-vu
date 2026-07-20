package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/stats"
)

func TestStatsFormattingHelpers(t *testing.T) {
	if got := stats.TrimRunes("abcdef", 3); got != "ab…" {
		t.Fatalf("stats.TrimRunes = %q", got)
	}
	if got := stats.TrimRunes("abcdef", 1); got != "a" {
		t.Fatalf("stats.TrimRunes n=1 = %q", got)
	}
	if got := stats.ScaledBar(1, 100, 10); got != 1 {
		t.Fatalf("stats.ScaledBar = %d", got)
	}
	if got := stats.ScaledBar(0, 100, 10); got != 0 {
		t.Fatalf("stats.ScaledBar zero = %d", got)
	}
	if valueOrDash("") != "-" || valueOrDash("x") != "x" {
		t.Fatal("valueOrDash mismatch")
	}
	if !stats.Noise("<local-command x>") || stats.Noise("real title") {
		t.Fatal("stats.Noise mismatch")
	}
	if got := stats.Title(model.Session{Title: "<command-noise>", Messages: []model.Message{{Role: "assistant", Text: "skip"}, {Role: "user", Text: strings.Repeat("word ", 20)}}}); !strings.HasSuffix(got, "…") {
		t.Fatalf("stats.Title = %q", got)
	}
	if got := stats.Title(model.Session{Title: "Clean title"}); got != "Clean title" {
		t.Fatalf("stats.Title title = %q", got)
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

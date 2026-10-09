package termwidth

import (
	"strings"
	"testing"
)

func TestWrapNeverBreaksAWord(t *testing.T) {
	s := "kept MaxConnLifetime at 4m so connections still retire before the proxy's 5m idle cut"
	for _, w := range []int{20, 33, 80} {
		for _, line := range Wrap(s, w) {
			if Columns(line) > w {
				t.Errorf("width %d: %q is wider", w, line)
			}
		}
		if got := strings.Join(Wrap(s, w), " "); got != s {
			t.Errorf("width %d: words changed: %q", w, got)
		}
	}
	if got := Wrap(s, 0); len(got) != 1 || got[0] != s {
		t.Errorf("width 0 must leave the text alone, got %q", got)
	}
}

func TestIndentHangsContinuationLines(t *testing.T) {
	got := Indent("one two three four five six seven eight nine ten", 24, "  · ", "    ")
	want := "  · one two three four\n    five six seven eight\n    nine ten"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := Indent("short", 80, "  · ", "    "); got != "  · short" {
		t.Errorf("short line changed: %q", got)
	}
	if got := Indent("a b", 0, "> ", "  "); got != "> a b" {
		t.Errorf("width 0 changed: %q", got)
	}
}

func TestWrapTextLeavesCodeFencesAlone(t *testing.T) {
	code := "    if err := pool.Exec(ctx, \"select 1 from a very long table name\"); err != nil {"
	text := "a sentence that is long enough to need a wrap here\n```\n" + code + "\n```\n- a bullet that also runs on past the edge"
	got := WrapText(text, 30)
	if !strings.Contains(got, code) {
		t.Errorf("fenced code was rewrapped:\n%s", got)
	}
	if !strings.Contains(got, "- a bullet that also runs on\n  past the edge") {
		t.Errorf("bullet continuation not hung under the text:\n%s", got)
	}
	if WrapText(text, 0) != text {
		t.Error("width 0 changed the text")
	}
}

func TestWrapKeepsAnOverlongWordWhole(t *testing.T) {
	got := Wrap("see /very/long/path/that/does/not/fit/anywhere.go now", 20)
	if got[1] != "/very/long/path/that/does/not/fit/anywhere.go" {
		t.Errorf("got %q", got)
	}
}

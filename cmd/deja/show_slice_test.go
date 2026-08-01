package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

func TestSliceMessages(t *testing.T) {
	ms := make([]model.Message, 10)
	for i := range ms {
		ms[i] = model.Message{Text: string(rune('a' + i))}
	}
	cases := []struct {
		offset, limit int
		want          string
	}{
		{0, 3, "abc"},
		{7, 5, "hij"},
		{0, 50, "abcdefghij"},
		{10, 5, ""},
		// Past the end is empty, not a panic: --offset takes any number the
		// reader types.
		{99, 5, ""},
	}
	for _, c := range cases {
		var b strings.Builder
		for _, m := range sliceMessages(ms, c.offset, c.limit) {
			b.WriteString(m.Text)
		}
		if b.String() != c.want {
			t.Errorf("offset=%d limit=%d → %q, want %q", c.offset, c.limit, b.String(), c.want)
		}
	}
}

// Both flags are documented for `deja show`, and only the JSON path honoured
// them — the text output printed the whole session (#709).
// sliceMessages must survive an offset past the end — --offset takes any
// number the reader types.
func TestSliceMessagesPastTheEnd(t *testing.T) {
	ms := []model.Message{{Text: "a"}, {Text: "b"}}
	for _, off := range []int{2, 3, 1000} {
		if got := sliceMessages(ms, off, 5); len(got) != 0 {
			t.Errorf("offset %d returned %d messages", off, len(got))
		}
	}
}

func TestShowHonoursOffsetAndLimitInTextOutput(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, `{"type":"user","sessionId":"ten","cwd":"/w/p","timestamp":"2026-07-21T10:0`+
			string(rune('0'+i))+`:00Z","message":{"role":"user","content":"message number `+string(rune('0'+i))+`"}}`)
	}
	if err := os.WriteFile(filepath.Join(root, "ten.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	count := func(out string) int { return strings.Count(out, "message number") }

	whole, err := captureRun(t, "show", "ten")
	if err != nil {
		t.Fatal(err)
	}
	// No flags: the session is printed whole, or every reader who pipes
	// `deja show` into a pager silently loses the tail. A ten-message session
	// is also too small to be worth mentioning the slice flags for.
	if got := count(whole); got != 10 {
		t.Errorf("show without flags returned %d messages", got)
	}
	quiet, err := captureRunStderr(t, "show", "ten")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "reads a slice") {
		t.Errorf("a ten-message session suggested slicing: %q", quiet)
	}
	limited, err := captureRun(t, "show", "ten", "--limit", "3")
	if err != nil {
		t.Fatal(err)
	}
	if got := count(limited); got != 3 {
		t.Errorf("--limit 3 returned %d messages", got)
	}
	offset, err := captureRun(t, "show", "ten", "--offset", "7")
	if err != nil {
		t.Fatal(err)
	}
	if got := count(offset); got != 3 || !strings.Contains(offset, "message number 7") || strings.Contains(offset, "message number 6") {
		t.Errorf("--offset 7 returned %d messages: %q", got, offset)
	}
	both, err := captureRun(t, "show", "ten", "--offset", "7", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	if got := count(both); got != 2 {
		t.Errorf("--offset 7 --limit 2 returned %d messages", got)
	}
}

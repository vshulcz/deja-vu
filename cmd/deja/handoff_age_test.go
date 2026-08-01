package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// humanAge already ends in "old", and the warning appended the word again:
// "this session is 11d old old" (#743).
func TestHumanAgeReadsAsASentence(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Minute, "30m old"},
		{5 * time.Hour, "5h old"},
		{11 * 24 * time.Hour, "11d old"},
	}
	for _, c := range cases {
		got := humanAge(c.d)
		if got != c.want {
			t.Errorf("humanAge(%v) = %q, want %q", c.d, got, c.want)
		}
		// The warning interpolates this verbatim, so it must not need another
		// word after it.
		line := "this session is " + got + ";"
		if strings.Contains(line, "old old") {
			t.Errorf("warning would read %q", line)
		}
	}
}

func TestHandoffStaleWarningReadsAsASentence(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	old := time.Now().AddDate(0, 0, -11).UTC().Format(time.RFC3339)
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"` + old + `","message":{"role":"user","content":"the retry budget keeps blowing up"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRunStderr(t, "handoff", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "this session is 11d old;") {
		t.Errorf("warning: %q", out)
	}
	if strings.Contains(out, "old old") {
		t.Errorf("still doubled: %q", out)
	}
}

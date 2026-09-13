package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedSessionsAhead indexes n transcripts stamped a year from now.
func seedSessionsAhead(t *testing.T, n int) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": fmt.Sprintf("ahead%d", i), "cwd": "/tmp/app",
			"timestamp": time.Now().AddDate(1, 0, i).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": "the pool was exhausted while the migration held the lock"},
		})
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("ahead%d.jsonl", i)), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// The clock row is the sentence a reader lands on when a store reads wrong, and
// the pronoun in it came from the helper `deja last` uses — a helper whose word
// carries its own verb ("that one is at the top of this list"), so spliced after
// "leads with" it read "leads with that one is".
func TestTheClockRowReadsAsASentence(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		want string
	}{
		{"one", 1, "`deja last` leads with it"},
		{"several", 2, "`deja last` leads with one of them"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedSessionsAhead(t, tc.n)
			out, err := captureRun(t, "doctor")
			if err != nil {
				t.Fatal(err)
			}
			var line string
			for _, l := range strings.Split(out, "\n") {
				if strings.Contains(l, "later than this machine's clock") {
					line = strings.TrimSpace(l)
					break
				}
			}
			if line == "" {
				t.Fatalf("doctor said nothing about %d session(s) stamped ahead:\n%s", tc.n, out)
			}
			if !strings.Contains(line, tc.want) {
				t.Errorf("clock row: %q\nwant it to end with %q", line, tc.want)
			}
			for _, broken := range []string{"with that one is", "with those are"} {
				if strings.Contains(line, broken) {
					t.Errorf("clock row splices a verb into the pronoun: %q", line)
				}
			}
		})
	}
}

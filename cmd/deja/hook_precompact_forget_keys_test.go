package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A compaction throws away what the session was shown; the seen list has to
// forget all of it. The session-start rows (start:<sid>) and the once mark
// (once:<sid>) outlived the compaction, so the digest the model had just lost
// was the one deja refused to send again (#3307). Kimi is where it shows most:
// those two are the only rows it ever writes.
func TestPrecompactForgetsTheStartRowsAndTheOnceMark(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seen := dir + ".hookseen"
	lines := []string{
		"start:sess-1 other-session 2026-09-08T08:13:38Z start:proj",
		"once:sess-1 once-digest",
		"sess-1 block-fingerprint",
		"start:sess-2 other-session 2026-09-08T08:13:39Z start:proj",
		"once:sess-2 once-digest",
		"sess-2 someone-elses-block",
	}
	if err := os.WriteFile(seen, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forgetInjected(dir, "sess-1")
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, gone := range []string{"start:sess-1", "once:sess-1", "sess-1 block-fingerprint"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived the compaction:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"start:sess-2", "once:sess-2", "sess-2 someone-elses-block"} {
		if !strings.Contains(got, kept) {
			t.Errorf("another session's row %q was dropped:\n%s", kept, got)
		}
	}
}

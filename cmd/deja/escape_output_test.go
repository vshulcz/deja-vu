package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A transcript can carry terminal control sequences — from a harness, from a
// web page an agent pasted, or across `deja sync import` from another machine.
// The listing and the share digest printed them verbatim (#1090).
func TestListingAndShareStripControls(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("NO_COLOR", "1")
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// Written as JSON escapes because a raw control byte is not valid inside a
	// JSON string, which is also how a real harness records one.
	const probe = `probe \u001b[31mRED\u001b[0m \u001b[2K \u001b[1A \u001b]0;pwned\u0007 \u0000 end`
	line := fmt.Sprintf(`{"type":"user","sessionId":"s-esc","cwd":"/tmp/esc","timestamp":"2026-06-01T10:00:00Z","message":{"role":"user","content":"%s"}}`, probe)
	writeClaudeFixture(t, filepath.Join(root, "projects", "-tmp-esc", "s-esc.jsonl"), "s-esc", []string{line})
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"last", []string{"last", "5"}},
		{"share", []string{"share", "s-esc"}},
		{"search", []string{"search", "probe"}},
		{"show", []string{"show", "s-esc"}},
	} {
		out, err := captureRun(t, tc.args...)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for _, c := range []struct {
			b    byte
			name string
		}{{0x1b, "ESC"}, {0x07, "BEL"}, {0x00, "NUL"}} {
			if bytes.IndexByte([]byte(out), c.b) >= 0 {
				t.Errorf("%s passed %s through to the terminal:\n%q", tc.name, c.name, out)
			}
		}
		if strings.TrimSpace(out) == "" {
			t.Errorf("%s printed nothing; the probe measured the wrong thing", tc.name)
		}
	}
}

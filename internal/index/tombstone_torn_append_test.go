package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A process killed between a key and its newline leaves the file ending
// mid-line. Losing that key is the accepted cost of appending without a lock
// (#1029) — taking the next forget down with it is not: the new key was glued
// onto the partial one, so the authoritative copy held neither, and only the
// mirror beside the index still knew. Take the index away and the forgotten
// session comes back (#2195).
func TestATornTombstoneLineDoesNotSwallowTheNextForget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(privacyDir(), "tombstones")
	if err := os.MkdirAll(privacyDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("claude:torn-half"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := appendTombstones([]string{"claude:b1"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Read back from this file alone: the mirror is a second copy for the case
	// where this one is gone, and it must not be what makes this pass.
	if got := readTombstoneFile(path); !got["claude:b1"] {
		t.Errorf("the forget after a torn line is not in the file it is written to:\n%s", raw)
	}
	if strings.Contains(string(raw), "claude:torn-halfclaude:b1") {
		t.Errorf("the new key was appended onto the partial line:\n%s", raw)
	}
	// The interrupted key is still lost as a key and kept as a line: what it
	// says is not a session anyone forgot, and rewriting it away is a decision
	// for the reader, not for the writer that found it.
	if lines := strings.Count(string(raw), "\n"); lines != 2 {
		t.Errorf("file holds %d newlines, want 2:\n%s", lines, raw)
	}
}

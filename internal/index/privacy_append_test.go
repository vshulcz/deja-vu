package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forget only grows the tombstone set, and rewriting the whole file was the
// cost of the command on a machine with a large history (#1029). Appending
// must leave the set readable and complete.
func TestAppendTombstonesKeepsEarlierKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := appendTombstones([]string{"claude:b", "claude:a"}); err != nil {
		t.Fatal(err)
	}
	if err := appendTombstones([]string{"claude:c"}); err != nil {
		t.Fatal(err)
	}
	// A no-op call must not create noise in the file.
	if err := appendTombstones(nil); err != nil {
		t.Fatal(err)
	}
	got := readTombstones()
	for _, want := range []string{"claude:a", "claude:b", "claude:c"} {
		if !got[want] {
			t.Errorf("%q missing from the set after append: %v", want, got)
		}
	}
	raw, err := os.ReadFile(filepath.Join(privacyDir(), "tombstones"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 3 {
		t.Errorf("file holds %d lines, want 3:\n%s", lines, raw)
	}
}

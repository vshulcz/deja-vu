package usage

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The rotation's fast path (#2220) reads each line's stamp as text, which works
// because the stamp is the first field and always UTC. Both are properties of
// the writers rather than of the file format, and moving `Time` down the struct
// — or stamping with an offset — would turn the scan off with nothing failing:
// the log would quietly go back to costing a full parse on every hook.
func TestEveryLineDejaWritesOpensWithAUTCStamp(t *testing.T) {
	dir := t.TempDir()
	RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	RecordServedSessions(dir, KindHook, 600, 1, false, 6000, []string{"claude:a1"})
	RecordResultRaw(dir, KindDejaVu, 0, 0, true, 0)

	check := func(what string) {
		t.Helper()
		b, err := os.ReadFile(Path(dir))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if len(lines) == 0 || lines[0] == "" {
			t.Fatalf("%s: the log is empty, so this measures nothing", what)
		}
		for i, line := range lines {
			if !strings.HasPrefix(line, `{"t":"`) {
				t.Errorf("%s: line %d does not open with the stamp: %.60q", what, i, line)
				continue
			}
			stamp, _, _ := strings.Cut(line[len(`{"t":"`):], `"`)
			if !strings.HasSuffix(stamp, "Z") {
				t.Errorf("%s: line %d is stamped %q, not UTC", what, i, stamp)
			}
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				t.Errorf("%s: line %d carries %q, which is not RFC 3339: %v", what, i, stamp, err)
			}
		}
	}
	check("as written")

	// And after a rotation, which rewrites every line it keeps.
	old := Event{Time: time.Now().UTC().Add(-40 * 24 * time.Hour), Kind: KindRecall, Bytes: 1, Sessions: 1}
	appendEventForTest(t, dir, old)
	rotate(Path(dir))
	check("after a rotation")

	// The snapshot log is written by a second encoder with its own settings.
	RecordDigestTerms(dir, KindHook, "a digest with <html> & \"quotes\"", 1, 100, []string{"pool"}, "claude:a1")
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.HasPrefix(line, `{"t":"`) {
			t.Errorf("snapshot line %d does not open with the stamp: %.60q", i, line)
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// writeMixedPeers points deja at a peers file holding the names that break a
// column padded by runes: an ordinary one, a long one, a CJK one, and one
// bounded to 80 columns since #1808.
func writeMixedPeers(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peers.json")
	body := `{"peers":[
	{"host":"laptop","last_push":"2026-08-24T10:00:00Z","last_pull":"2026-08-24T10:00:00Z"},
	{"host":"build-server-in-the-other-office","last_push":"2026-08-23T10:00:00Z"},
	{"host":"数据分片服务器","last_push":"2026-08-22T10:00:00Z"},
	{"host":"` + strings.Repeat("w", 300) + `","last_push":"2026-08-21T10:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_PEERS_FILE", path)
}

// The Sync section is a column, and what it holds is a name someone typed. It
// was padded with %-12s: a 32-character name got no padding at all, a CJK name
// was padded as if each character were one column wide, and an 80-column name
// pushed the state off the screen (#1821).
func TestTheSyncSectionLinesUpWhateverThePeerIsCalled(t *testing.T) {
	hermeticEnv(t)
	writeMixedPeers(t)
	var b strings.Builder
	doctorPeers(&b, t.TempDir(), time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC))

	var starts []int
	for _, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "Sync") {
			continue
		}
		i := strings.Index(line, "last exchange")
		if i < 0 {
			continue
		}
		starts = append(starts, termwidth.Columns(line[:i]))
	}
	if len(starts) != 4 {
		t.Fatalf("want a state line per peer, got %d:\n%s", len(starts), b.String())
	}
	for _, at := range starts[1:] {
		if at != starts[0] {
			t.Errorf("the states start at different columns %v:\n%s", starts, b.String())
			break
		}
	}
	// The control: every name is still on the screen, so the column was not
	// bought by throwing the names away.
	for _, want := range []string{"laptop", "build-server-in-the-other-office", "数据分片服务器"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("%q is missing from the section:\n%s", want, b.String())
		}
	}
}

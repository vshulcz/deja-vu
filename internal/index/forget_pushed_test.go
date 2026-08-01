package index

import (
	"path/filepath"
	"testing"
	"time"
)

// Forgetting removes the local copy and keeps the session out of later pushes,
// but a machine that already has it keeps it. Saying nothing read as "it is
// gone everywhere" to someone forgetting a customer name (#788).
func TestForgetReportsWhereItWasAlreadyPushed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.jsonl")
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := Manifest{Sessions: map[string]SessionMeta{
		"claude:s1": {ID: "s1", Harness: "claude", Path: path, Started: started, Updated: started},
	}}
	matched := map[string]bool{"claude:s1": true}

	// Nothing pushed anywhere.
	if peers, exported := pushedTo(m, matched); len(peers) != 0 || exported {
		t.Errorf("with no watermarks: peers=%v exported=%v", peers, exported)
	}

	// A directory export carries no peer name.
	m.ExportWatermarks = map[string]int64{path: started.UnixNano()}
	if peers, exported := pushedTo(m, matched); len(peers) != 0 || !exported {
		t.Errorf("after a directory export: peers=%v exported=%v", peers, exported)
	}

	// Named peers are named, and a watermark older than the session is not a
	// push of it.
	m.ExportWatermarks = map[string]int64{
		"mini\x00" + path:  started.UnixNano(),
		"work\x00" + path:  started.Add(time.Hour).UnixNano(),
		"older\x00" + path: started.Add(-time.Hour).UnixNano(),
	}
	peers, exported := pushedTo(m, matched)
	if exported {
		t.Error("named peers were reported as an unnamed export")
	}
	if len(peers) != 2 || peers[0] != "mini" || peers[1] != "work" {
		t.Errorf("peers = %v, want [mini work]", peers)
	}
}

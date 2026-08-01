package index

import (
	"os"
	"testing"
	"time"
)

// The "covering" line answers how far back a memory goes, and long-running
// sessions are the ones carrying the old history. Deriving the floor from the
// last activity hid everything before it (#786).
func TestOverviewCoversWhenTheOldestWorkBegan(t *testing.T) {
	_, dir := allHarnessEnv(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	m := Manifest{Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{
		"claude:long":  {ID: "long", Harness: "claude", Started: started, Updated: updated},
		"claude:short": {ID: "short", Harness: "claude", Started: updated, Updated: updated},
	}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	ov, err := Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ov.Oldest.Equal(started) {
		t.Errorf("oldest = %s, want the earliest start %s", ov.Oldest, started)
	}
	if !ov.Newest.Equal(updated) {
		t.Errorf("newest = %s, want %s", ov.Newest, updated)
	}

	// A session whose start never made it into the manifest still has to bound
	// the range, or an older store reports no coverage at all.
	m.Sessions = map[string]SessionMeta{"claude:nostart": {ID: "nostart", Harness: "claude", Updated: updated}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	ov, err = Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ov.Oldest.Equal(updated) {
		t.Errorf("oldest = %s, want the fallback %s", ov.Oldest, updated)
	}
}

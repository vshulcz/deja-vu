package peers

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writePeers points deja at a peers file with the given body.
func writePeers(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// One machine named twice used to be two rows: the second was frozen, because
// Record stops at the first match, so it showed "failed 235 days ago" beside
// the same machine reporting a sync this morning — and a bare `deja sync`
// opened two connections to it (#1853).
func TestOneMachineNamedTwiceIsOneMachine(t *testing.T) {
	writePeers(t, `{"peers":[
	{"host":"laptop","last_push":"2026-08-24T10:00:00Z"},
	{"host":"laptop","last_push":"2026-01-01T10:00:00Z","last_error":"stale copy"}]}`)

	list := Load()
	if len(list) != 1 {
		t.Fatalf("the same host came back %d times: %#v", len(list), list)
	}
	// The newer exchange wins, and the error belongs to it — the stale row's
	// failure is not news about a machine that has synced since.
	if got := list[0].LastPush.Format("2006-01-02"); got != "2026-08-24" {
		t.Errorf("the folded row kept the older push: %s", got)
	}
	if list[0].LastError != "" {
		t.Errorf("the folded row inherited an error from an exchange it is newer than: %q", list[0].LastError)
	}
}

// The other direction: the newer row is the one that failed, so its error is
// what the reader needs to see.
func TestFoldingKeepsTheNewerFailure(t *testing.T) {
	writePeers(t, `{"peers":[
	{"host":"laptop","last_push":"2026-01-01T10:00:00Z"},
	{"host":"laptop","last_push":"2026-08-24T10:00:00Z","last_error":"ssh laptop: exit status 255"}]}`)

	list := Load()
	if len(list) != 1 {
		t.Fatalf("the same host came back %d times", len(list))
	}
	if list[0].LastError == "" {
		t.Errorf("the newest exchange failed and the row says nothing: %#v", list[0])
	}
}

// A recorded exchange leaves one row, whichever copy it started from, and the
// file it writes back holds one too.
func TestRecordingLeavesOneRowPerMachine(t *testing.T) {
	writePeers(t, `{"peers":[
	{"host":"laptop","last_push":"2026-08-24T10:00:00Z"},
	{"host":"laptop","last_push":"2026-01-01T10:00:00Z","last_error":"stale copy"}]}`)

	if err := Record("laptop", false, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	list := Load()
	if len(list) != 1 {
		t.Fatalf("recording left %d rows for one machine: %#v", len(list), list)
	}
	if list[0].LastError != "" {
		t.Errorf("a successful exchange left an error behind: %q", list[0].LastError)
	}
	if err := Record("laptop", false, time.Now(), errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	list = Load()
	if len(list) != 1 || list[0].LastError == "" {
		t.Errorf("a failure did not land on the one row: %#v", list)
	}
}

// The control: two different machines stay two machines.
func TestFoldingKeepsDistinctMachinesApart(t *testing.T) {
	writePeers(t, `{"peers":[
	{"host":"laptop","last_push":"2026-08-24T10:00:00Z"},
	{"host":"build-box","last_push":"2026-08-23T10:00:00Z"}]}`)
	if list := Load(); len(list) != 2 {
		t.Errorf("two machines came back as %d: %#v", len(list), list)
	}
}

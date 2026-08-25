package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/peers"
)

// hostileHostsFile writes a peers file holding a host with an escape byte and
// one long enough to fill a screen, and points deja at it.
func hostileHostsFile(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peers.json")
	body, err := json.Marshal(map[string]any{"peers": []map[string]any{
		{"host": "build\x1b[31mALERT\x1b[0m\rrewound", "last_push": time.Now().Add(-48 * time.Hour).Format(time.RFC3339)},
		{"host": strings.Repeat("w", 300), "last_push": time.Now().Add(-72 * time.Hour).Format(time.RFC3339)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_PEERS_FILE", path)
}

// A host name is printed on the screen someone reads when sync is misbehaving;
// an escape byte in it rewrites that screen (#1808).
func TestTheDoctorDoesNotHandTheTerminalAPeerName(t *testing.T) {
	hermeticEnv(t)
	hostileHostsFile(t)
	var b strings.Builder
	doctorPeers(&b, t.TempDir(), time.Now())
	out := b.String()
	if strings.ContainsAny(out, "\x1b\r") {
		t.Errorf("the doctor's Sync section carried an escape or a rewind:\n%q", out)
	}
	if !strings.Contains(out, "ALERT") {
		t.Errorf("the host deja is talking to is gone from the line:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 400 {
			t.Errorf("one peer line is %d bytes:\n%s", len(line), line[:120])
		}
	}
}

// The name is bounded on screen and nowhere else: ssh is still handed the host
// as written, and `deja sync forget` still matches on it.
func TestForgetStillMatchesTheHostAsWritten(t *testing.T) {
	hermeticEnv(t)
	hostileHostsFile(t)
	long := strings.Repeat("w", 300)
	out, err := captureRunStderr(t, "sync", "forget", long)
	if err != nil {
		t.Fatal(err)
	}
	_ = out
	for _, p := range peersLoadForTest() {
		if p.Host == long {
			t.Errorf("forget did not match the host it was given in full")
		}
	}
}

func peersLoadForTest() []peers.Peer { return peers.Load() }

// The other machine's deja writes on this screen, and it is a machine this one
// does not control: its output reached the terminal raw.
func TestRemoteOutputCannotRewriteTheLocalScreen(t *testing.T) {
	hostile := "deja: exported 3 records\x1b[31m\rHACKED" + strings.Repeat(" padding", 400)
	got := remoteOutputForEcho(hostile)
	if strings.ContainsAny(got, "\x1b\r") {
		t.Errorf("remote output carried an escape or a rewind: %q", got[:60])
	}
	if len(got) > remoteEchoMax+8 {
		t.Errorf("remote output printed %d bytes", len(got))
	}
	if !strings.Contains(got, "exported 3 records") {
		t.Errorf("the report itself was lost: %q", got[:60])
	}
	if short := remoteOutputForEcho("deja: exported 3 records"); short != "deja: exported 3 records" {
		t.Errorf("an ordinary report was altered: %q", short)
	}
}

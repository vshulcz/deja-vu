package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A peer's name is a borrowed string: an ssh alias someone typed, or what
// another machine called itself. The report must not hand a terminal what that
// string asks for, and the JSON must hand a tool the name it needs to act on —
// two different answers to one input, and the docs settle which is which.
func TestAPeerNameCannotWriteOnTheReport(t *testing.T) {
	tmp := hermeticEnv(t)
	const nasty = "lap\x1b[31mtop\x07\nSync: peers    everything is fine"
	peersFile := filepath.Join(tmp, "peers.json")
	body, err := json.Marshal(map[string]any{"peers": []map[string]any{
		{"host": nasty, "machine": nasty, "last_pull": time.Now().UTC().Format(time.RFC3339)},
		{"host": strings.Repeat("a", 120), "last_push": time.Now().UTC().Format(time.RFC3339)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(peersFile, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_PEERS_FILE", peersFile)

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	sync := doctorSection(out, "Sync:")
	if strings.Contains(sync, "\x1b") || strings.Contains(sync, "\x07") {
		t.Errorf("a peer name carried a control byte into the report: %q", sync)
	}
	// One row per peer: a name with a newline in it must not become a second
	// line that reads as deja's own.
	rows := 0
	for _, line := range strings.Split(sync, "\n") {
		if strings.Contains(line, "last exchange") {
			rows++
		}
	}
	if rows != 2 {
		t.Errorf("two peers produced %d rows:\n%s", rows, sync)
	}
	// Sanitised, not dropped: the escape becomes a space and the rest of the
	// name is still readable, so the row still names a machine.
	if !strings.Contains(sync, "lap") || !strings.Contains(sync, "top") {
		t.Errorf("the name is gone rather than sanitised:\n%s", sync)
	}

	// The JSON is the other answer: `host` is a name to act on — `deja sync ssh
	// <host>` — so it is reported as the file spells it, and the encoder is
	// what keeps the control bytes out of a terminal.
	raw, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "\x1b") || strings.Contains(raw, "\x07") {
		t.Errorf("doctor --json emitted a raw control byte")
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host string `json:"host"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	found := false
	for _, p := range report.Sync.Peers {
		if p.Host == nasty {
			found = true
		}
	}
	if !found {
		t.Errorf("doctor --json did not report the host as the file spells it: %+v", report.Sync.Peers)
	}
}

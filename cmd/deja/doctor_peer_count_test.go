package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The count beside a machine is looked up by name, and the two names come from
// different places: the peers row is the ssh alias someone typed, while the
// stamp on an imported session is what the sending machine calls itself. macOS
// hands back a capitalised hostname, so `ssh laptop` against a host named
// `Laptop` reported none of the sessions it had sent (#1876).
func TestTheSessionCountFindsTheMachineInEitherCase(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"laptop","last_pull":"2026-08-20T10:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < 3; i++ {
		rec := map[string]any{
			"harness": "claude", "session_id": "remote" + string(rune('a'+i)), "project": "proj",
			"role": "user", "text": "retry loop", "time": "2026-08-20T10:00:0" + string(rune('0'+i)) + "Z",
			"origin": "Laptop",
		}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if n, err := index.Import(dir, batch); err != nil || n != 3 {
		t.Fatalf("import: %d records, %v", n, err)
	}

	var text strings.Builder
	doctorPeers(&text, dir, time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	if !strings.Contains(text.String(), "3 sessions from there") {
		t.Errorf("the row does not count the sessions that machine sent:\n%s", text.String())
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host     string `json:"host"`
				Sessions int    `json:"sessions_from_there"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 || report.Sync.Peers[0].Sessions != 3 {
		t.Errorf("--json counts %#v, want three sessions from laptop", report.Sync.Peers)
	}
}

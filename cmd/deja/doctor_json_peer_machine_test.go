package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The text row says which machine an ssh host turns out to be (#2415), and
// `sessions_from_there` is counted by that same name. The JSON carried the
// host alone, so a consumer could not join a peer to the work that came from
// it (#2423).
func TestDoctorJSONPeerCarriesTheMachineName(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	body := `{"peers":[{"host":"vlad@10.0.0.7","machine":"quicksilver","last_pull":"2026-08-28T10:00:00Z"},` +
		`{"host":"mini","last_pull":"2026-08-28T09:00:00Z"}]}`
	if err := os.WriteFile(peersFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, _ := captureBoth(t, "doctor", "--json")
	var report struct {
		Sync struct {
			Peers []map[string]any `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	if len(report.Sync.Peers) != 2 {
		t.Fatalf("want two peer rows, got %d", len(report.Sync.Peers))
	}
	for _, p := range report.Sync.Peers {
		switch p["host"] {
		case "vlad@10.0.0.7":
			if p["machine"] != "quicksilver" {
				t.Errorf("the row does not say which machine that host is: %v", p)
			}
		case "mini":
			// A machine that has never said what it calls itself adds nothing.
			if _, ok := p["machine"]; ok {
				t.Errorf("the row invented a name: %v", p)
			}
		}
	}
}

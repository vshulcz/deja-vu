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

// A peers file that cannot be parsed used to read exactly like a machine with
// no peers, on both surfaces, while `deja sync` walked nothing at all. The
// trust policy has said `unreadable` with the reason since #1027; sync is the
// section that exists because a stopped sync does not announce itself (#1840).
func TestAnUnreadablePeersFileIsReportedAsSuch(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"truncated", `{"peers":[{"host":"laptop","last_push":"2026-08-24T10:00:00Z"}`},
		{"peers is not a list", `{"peers":"not a list"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			path := filepath.Join(tmp, "peers.json")
			t.Setenv("DEJA_PEERS_FILE", path)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}

			var text strings.Builder
			doctorPeers(&text, t.TempDir(), time.Now())
			if strings.Contains(text.String(), "no other machines yet") {
				t.Errorf("a broken peers file reads as an empty one:\n%s", text.String())
			}
			if !strings.Contains(text.String(), path) {
				t.Errorf("the report does not say which file to look at:\n%s", text.String())
			}

			var out strings.Builder
			if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
				t.Fatal(err)
			}
			var report struct {
				Sync struct {
					State string `json:"state"`
					Error string `json:"error"`
				} `json:"sync"`
			}
			if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
				t.Fatal(err)
			}
			if report.Sync.State != "unreadable" {
				t.Errorf("sync.state = %q, want unreadable", report.Sync.State)
			}
			if report.Sync.Error == "" {
				t.Errorf("sync.error is empty, so a script knows something is wrong and not what")
			}
		})
	}
}

// The control: a file deja can read reports ok, and a machine with no file at
// all is not an error — it is a machine that has never synced.
func TestAReadablePeersFileReportsOK(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":"laptop","last_push":"2026-08-24T10:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := collectDoctorSync(t.TempDir()); got.State != "ok" || got.Error != "" {
		t.Errorf("a readable file reports %#v", got)
	}

	t.Setenv("DEJA_PEERS_FILE", filepath.Join(tmp, "absent.json"))
	if got := collectDoctorSync(t.TempDir()); got.State != "ok" || got.Error != "" {
		t.Errorf("a machine that never synced reports %#v", got)
	}
	var text strings.Builder
	doctorPeers(&text, t.TempDir(), time.Now())
	if !strings.Contains(text.String(), "no other machines yet") {
		t.Errorf("a machine with no peers lost its own line:\n%s", text.String())
	}
}

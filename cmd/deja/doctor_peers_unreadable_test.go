package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/peers"
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

// The sentence a broken file used to get from `deja sync` told the reader to
// name their first machine — which is what they had already done (#1840).
func TestBareSyncSaysThePeersFileIsBrokenRatherThanEmpty(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":"laptop"`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runSyncAll(index.DefaultDir(), false)
	if err == nil {
		t.Fatal("a broken peers file did not stop the sync")
	}
	if strings.Contains(err.Error(), "no machines to sync with yet") {
		t.Errorf("a broken file reads as a machine that never synced: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error does not say which file to look at: %v", err)
	}

	// The control: with no file at all, the invitation is still the right
	// sentence.
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(tmp, "absent.json"))
	err = runSyncAll(index.DefaultDir(), false)
	if err == nil || !strings.Contains(err.Error(), "no machines to sync with yet") {
		t.Errorf("a machine that never synced lost its invitation: %v", err)
	}
}

// Every odd shape of the file, and what deja calls it. An empty file is not
// "nothing configured": deja never writes one, so it means a write that did not
// finish, and the reader should look at it.
func TestThePeersFileShapesAreClassified(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"empty file", "", "unreadable"},
		{"whitespace only", "  \n", "unreadable"},
		{"no peers key", "{}", "ok"},
		{"null peers", `{"peers":null}`, "ok"},
		{"a peer with no host", `{"peers":[{"last_push":"2026-08-24T10:00:00Z"}]}`, "ok"},
		{"top-level array", "[]", "unreadable"},
		{"top-level string", `"x"`, "unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			path := filepath.Join(tmp, "peers.json")
			t.Setenv("DEJA_PEERS_FILE", path)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			got := collectDoctorSync(t.TempDir())
			if got.State != tc.want {
				t.Errorf("state = %q, want %q (error %q)", got.State, tc.want, got.Error)
			}
			if got.State == "unreadable" && got.Error == "" {
				t.Errorf("unreadable with no reason")
			}
			if got.Peers == nil {
				t.Errorf("peers is null rather than an empty list, which a consumer has to special-case")
			}
		})
	}
}

// The two failure modes the shape table cannot write as a file body: no
// permission to read, and a directory where the file should be.
func TestAFileDejaCannotOpenIsReportedToo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything, so the denied case cannot be built")
	}
	t.Run("permission denied", func(t *testing.T) {
		tmp := hermeticEnv(t)
		path := filepath.Join(tmp, "peers.json")
		t.Setenv("DEJA_PEERS_FILE", path)
		if err := os.WriteFile(path, []byte(`{"peers":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o000); err != nil {
			t.Skipf("cannot drop permissions here: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
		// Chmod succeeding is not the same as access being denied: on Windows
		// it toggles a read-only attribute and the file opens as before, so the
		// skip above never fired and this asserted a state the platform cannot
		// reach. Ask the file itself.
		if f, err := os.Open(path); err == nil {
			_ = f.Close()
			t.Skip("this platform still reads a file with no permission bits")
		}
		got := collectDoctorSync(t.TempDir())
		if got.State != "unreadable" || !strings.Contains(got.Error, "permission denied") {
			t.Errorf("a file deja may not read reports %#v", got)
		}
	})
	t.Run("a directory in its place", func(t *testing.T) {
		tmp := hermeticEnv(t)
		path := filepath.Join(tmp, "peers.json")
		t.Setenv("DEJA_PEERS_FILE", path)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		got := collectDoctorSync(t.TempDir())
		if got.State != "unreadable" || got.Error == "" {
			t.Errorf("a directory where the file should be reports %#v", got)
		}
	})
}

// Snapshot is one read: the list and the reason describe the same state of the
// file, which two calls could not promise while a sync rewrites it.
func TestSnapshotAnswersBothFromOneRead(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":"laptop"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	list, why := peers.Snapshot()
	if why != "" || len(list) != 1 {
		t.Fatalf("a readable file gave %d peers and %q", len(list), why)
	}
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":`), 0o600); err != nil {
		t.Fatal(err)
	}
	list, why = peers.Snapshot()
	if why == "" {
		t.Error("a broken file gave no reason")
	}
	if len(list) != 0 {
		t.Errorf("a broken file still produced %d peers", len(list))
	}
}

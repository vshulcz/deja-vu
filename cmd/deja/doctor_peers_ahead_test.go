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

// A stamp in the future made the age negative, and everything under a minute
// reads as "just now" — so a peer seventy years out looked like the healthiest
// machine on the screen whose whole purpose is to show a sync that stopped
// (#1855). deja says this about sessions already (index.StampedAhead, #1753).
func TestAPeerStampedAheadIsNotReportedAsJustSynced(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	ahead := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"peers":[{"host":"laptop","last_push":"` + ahead + `"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	doctorPeers(&text, t.TempDir(), time.Now())
	if strings.Contains(text.String(), "just now") {
		t.Errorf("a stamp in the future reads as a sync that just happened:\n%s", text.String())
	}
	if !strings.Contains(text.String(), "clock") {
		t.Errorf("the line does not say why the date cannot be trusted:\n%s", text.String())
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host    string `json:"host"`
				Ahead   bool   `json:"stamped_ahead"`
				LastPsh string `json:"last_push"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 {
		t.Fatalf("want one peer, got %#v", report.Sync.Peers)
	}
	if !report.Sync.Peers[0].Ahead {
		t.Errorf("--json does not say the stamp is ahead of this machine's clock: %#v", report.Sync.Peers[0])
	}
	// The stamp itself is still reported as written — the reader may need it.
	if report.Sync.Peers[0].LastPsh == "" {
		t.Errorf("the stamp was dropped rather than flagged: %#v", report.Sync.Peers[0])
	}
}

// The control: an ordinary peer says neither, on either surface.
func TestAnOrdinaryPeerIsNotFlaggedAsAhead(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	when := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":"laptop","last_push":"`+when+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	doctorPeers(&text, t.TempDir(), time.Now())
	if strings.Contains(text.String(), "clock") {
		t.Errorf("an ordinary peer was flagged:\n%s", text.String())
	}
	if !strings.Contains(text.String(), "hours ago") {
		t.Errorf("an ordinary peer lost its age:\n%s", text.String())
	}
	if got := collectDoctorSync(t.TempDir()); len(got.Peers) != 1 || got.Peers[0].Ahead {
		t.Errorf("an ordinary peer is flagged in --json: %#v", got.Peers)
	}
}

// The slack: a peers file written by a machine a moment ahead, or an NTP step
// landing between the write and the read, must not put "one of the two clocks
// is wrong" against a healthy peer. A real skew still does (#1855).
func TestTheClockSentenceHasSlackForJitter(t *testing.T) {
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		delta time.Duration
		want  bool
	}{
		{"a second behind", -time.Second, false},
		{"exactly now", 0, false},
		{"a millisecond ahead", time.Millisecond, false},
		{"thirty seconds ahead", 30 * time.Second, false},
		{"a minute ahead", time.Minute, false},
		{"two minutes ahead", 2 * time.Minute, true},
		{"two days ahead", 48 * time.Hour, true},
	} {
		if got := peerStampedAhead(now.Add(tc.delta), now); got != tc.want {
			t.Errorf("%s: flagged=%v, want %v", tc.name, got, tc.want)
		}
	}
}

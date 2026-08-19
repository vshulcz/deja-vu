package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/peers"
)

// Naming the host every time is what makes a three-machine setup six commands
// and a thing to remember. One exchange is enough for deja to know a machine.
func TestSyncRemembersAHostItHasTalkedTo(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = fakeSSHOK()

	if err := runSync(os.Getenv("DEJA_INDEX_DIR"), []string{"ssh", "mini"}); err != nil {
		t.Fatal(err)
	}
	list := peers.Load()
	if len(list) != 1 || list[0].Host != "mini" {
		t.Fatalf("the host was not remembered: %+v", list)
	}
	if list[0].LastPush.IsZero() {
		t.Error("a push was not recorded")
	}
}

// A failed exchange is recorded too, or a peer that has been broken for a week
// looks exactly like one with nothing to send.
func TestSyncRecordsAFailedExchange(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		return "no route to host", errors.New("exit status 255")
	}
	if err := runSync(os.Getenv("DEJA_INDEX_DIR"), []string{"ssh", "mini"}); err == nil {
		t.Fatal("an unreachable host was reported as success")
	}
	list := peers.Load()
	if len(list) != 1 {
		t.Fatalf("the host was not remembered: %+v", list)
	}
	if list[0].LastError == "" {
		t.Error("the failure was not recorded")
	}
	if !list[0].Last().IsZero() {
		t.Error("a failed exchange was recorded as memory having moved")
	}
}

func TestBareSyncSaysWhatToDoWithNoPeers(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	err := runSync(os.Getenv("DEJA_INDEX_DIR"), nil)
	if err == nil {
		t.Fatal("a sync with no peers reported success")
	}
	if !strings.Contains(err.Error(), "deja sync ssh") {
		t.Errorf("the error does not say how to add one: %v", err)
	}
}

// The point of the bare form: every machine, both ways, without being told.
func TestBareSyncExchangesWithEveryPeerBothWays(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	for _, h := range []string{"mini", "laptop"} {
		if err := peers.Record(h, false, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	old := sshRunner
	defer func() { sshRunner = old }()
	var hosts []string
	var pulls, pushes int
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && len(args) > 1 {
			hosts = append(hosts, args[0])
			switch {
			case strings.Contains(args[1], "sync export"):
				pulls++
				return "deja: exported 0 records", nil
			case strings.Contains(args[1], "sync import"):
				pushes++
				return "deja: imported 0 records", nil
			}
		}
		if args[len(args)-1] == "mktemp -d" || (len(args) > 1 && args[1] == "mktemp -d") {
			return "/tmp/remote-out", nil
		}
		return "", nil
	}
	if err := runSync(os.Getenv("DEJA_INDEX_DIR"), nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mini", "laptop"} {
		found := false
		for _, h := range hosts {
			if h == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s was never contacted: %v", want, hosts)
		}
	}
	if pulls == 0 {
		t.Error("nothing was pulled, so this machine only ever sends")
	}
}

// One unreachable laptop must not stop the server from getting what the
// desktop did.
func TestBareSyncKeepsGoingPastAnUnreachableMachine(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	for _, h := range []string{"broken", "mini"} {
		if err := peers.Record(h, false, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	old := sshRunner
	defer func() { sshRunner = old }()
	reached := map[string]bool{}
	ok := fakeSSHOK()
	sshRunner = func(name string, args ...string) (string, error) {
		if len(args) > 0 {
			reached[args[0]] = true
			if args[0] == "broken" {
				return "no route to host", errors.New("exit status 255")
			}
		}
		return ok(name, args...)
	}
	if err := runSync(os.Getenv("DEJA_INDEX_DIR"), nil); err != nil {
		t.Fatalf("one broken machine failed the whole sync: %v", err)
	}
	if !reached["mini"] {
		t.Error("mini was skipped because broken came first")
	}
}

func TestPeerLineNamesWhatIsWrong(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		p    peers.Peer
		want []string
	}{
		{"a healthy peer",
			peers.Peer{Host: "mini", LastPush: now.Add(-2 * time.Hour), LastPull: now.Add(-2 * time.Hour)},
			[]string{"2 hours ago"}},
		{"one that takes but never sends",
			peers.Peer{Host: "mini", LastPush: now.Add(-time.Hour)},
			[]string{"nothing received yet"}},
		{"one this machine never sent to",
			peers.Peer{Host: "mini", LastPull: now.Add(-time.Hour)},
			[]string{"nothing sent yet"}},
		{"one that has been failing",
			peers.Peer{Host: "mini", LastPush: now.Add(-8 * 24 * time.Hour), LastPull: now.Add(-8 * 24 * time.Hour), LastError: "no route to host"},
			[]string{"8 days ago", "no route to host"}},
		{"one never reached at all",
			peers.Peer{Host: "mini"},
			[]string{"never exchanged"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := peerLine(tc.p, 0, now)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("peer line %q does not say %q", got, want)
				}
			}
		})
	}
	// The count answers "did anything ever actually arrive from there".
	if got := peerLine(peers.Peer{Host: "mini", LastPull: now}, 41, now); !strings.Contains(got, "41 sessions from there") {
		t.Errorf("the line does not say how much came from there: %q", got)
	}
}

func TestDoctorReportsTheMachinesItSyncsWith(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	var buf bytes.Buffer
	doctorPeers(&buf, os.Getenv("DEJA_INDEX_DIR"), time.Now())
	if !strings.Contains(buf.String(), "deja sync ssh") {
		t.Errorf("with no peers, doctor does not say how to add one:\n%s", buf.String())
	}

	if err := peers.Record("mini", false, time.Now().Add(-6*24*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctorPeers(&buf, os.Getenv("DEJA_INDEX_DIR"), time.Now())
	out := buf.String()
	for _, want := range []string{"Sync:", "mini", "6 days ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

// fakeSSHOK is a remote that answers every step of a push and a pull.
func fakeSSHOK() func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		if name != "ssh" || len(args) < 2 {
			return "", nil
		}
		switch {
		case args[1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case strings.Contains(args[1], "sync export"):
			return "deja: exported 0 records", nil
		case strings.Contains(args[1], "sync import"):
			return "deja: imported 0 records", nil
		}
		return "", nil
	}
}

package peers

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func peersFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	return path
}

// Naming a host once is what makes `deja sync` know about it afterwards.
func TestRecordRemembersAHostAndWhichWayMemoryMoved(t *testing.T) {
	peersFile(t)
	push := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	pull := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)

	if err := Record("mini", false, push, nil); err != nil {
		t.Fatal(err)
	}
	list := Load()
	if len(list) != 1 || list[0].Host != "mini" {
		t.Fatalf("the host was not remembered: %+v", list)
	}
	if !list[0].LastPush.Equal(push) {
		t.Errorf("push time = %v, want %v", list[0].LastPush, push)
	}
	if !list[0].LastPull.IsZero() {
		t.Errorf("a push wrote a pull time: %v", list[0].LastPull)
	}

	if err := Record("mini", true, pull, nil); err != nil {
		t.Fatal(err)
	}
	list = Load()
	if len(list) != 1 {
		t.Fatalf("the second exchange added a second host: %+v", list)
	}
	if !list[0].LastPull.Equal(pull) || !list[0].LastPush.Equal(push) {
		t.Errorf("the two directions were not kept apart: %+v", list[0])
	}
	if got := list[0].Last(); !got.Equal(pull) {
		t.Errorf("Last = %v, want the more recent of the two", got)
	}
}

// A peer that has been failing for a week is what the report exists to show,
// and it is invisible if only successes are written down. The timestamp must
// not move: "last exchange yesterday" has to mean memory actually moved.
func TestRecordKeepsTheFailureAndNotTheTime(t *testing.T) {
	peersFile(t)
	ok := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	if err := Record("mini", false, ok, nil); err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	if err := Record("mini", false, later, errors.New("ssh: no route to host")); err != nil {
		t.Fatal(err)
	}
	p := Load()[0]
	if !p.LastPush.Equal(ok) {
		t.Errorf("a failed exchange moved the clock: %v", p.LastPush)
	}
	if p.LastError == "" {
		t.Error("the failure was not recorded")
	}
	// And it clears once one works again.
	if err := Record("mini", false, later, nil); err != nil {
		t.Fatal(err)
	}
	if p := Load()[0]; p.LastError != "" {
		t.Errorf("the old failure outlived a successful exchange: %q", p.LastError)
	}
}

func TestForgetDropsOneHost(t *testing.T) {
	peersFile(t)
	now := time.Now()
	for _, h := range []string{"mini", "laptop"} {
		if err := Record(h, false, now, nil); err != nil {
			t.Fatal(err)
		}
	}
	found, err := Forget("mini")
	if err != nil || !found {
		t.Fatalf("Forget(mini) = %v, %v", found, err)
	}
	list := Load()
	if len(list) != 1 || list[0].Host != "laptop" {
		t.Errorf("the wrong host was dropped: %+v", list)
	}
	if found, _ := Forget("nobody"); found {
		t.Error("forgetting an unknown host reported a hit")
	}
}

// A malformed file must not break a sync: doctor is where a bad config gets
// reported, the same rule the trust policy follows.
func TestLoadTreatsAnUnreadableFileAsNoPeers(t *testing.T) {
	path := peersFile(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if list := Load(); len(list) != 0 {
		t.Errorf("a malformed file produced peers: %+v", list)
	}
	// A field of the wrong type is the dangerous one. json.Unmarshal fills in
	// every entry it could read and returns an error alongside them — measured:
	// three peers where the middle host is a number yields two usable entries
	// and one blank. Handing those back would be worse than handing back
	// nothing, because the next exchange writes the list it was given and the
	// damage becomes the file.
	bad := `{"peers":[{"host":"mini"},{"host":123},{"host":"laptop"}]}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if list := Load(); len(list) != 0 {
		t.Errorf("a partly-decoded file produced a list a later write would make permanent: %+v", list)
	}
	if err := Record("newhost", false, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if list := Load(); len(list) != 1 || list[0].Host != "newhost" {
		t.Errorf("recording after a bad file did not start clean: %+v", list)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A nameless entry is not a machine anyone can reach.
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":"  "},{"host":"mini"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	list := Load()
	if len(list) != 1 || list[0].Host != "mini" {
		t.Errorf("a nameless peer survived: %+v", list)
	}
}

// The list is read on every sync and rewritten on every exchange; a crash
// midway must not leave something Load silently reads as no peers.
func TestSaveReplacesTheFileWhole(t *testing.T) {
	path := peersFile(t)
	if err := Record("mini", false, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the temp file was left behind: %v", err)
	}
	if err := Record("laptop", false, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if list := Load(); len(list) != 2 {
		t.Errorf("a second write lost the first host: %+v", list)
	}
}

// A push with nothing to send opens no connection: the host is remembered, and
// what it did last time is left alone rather than overwritten with a zero
// (#1780).
func TestRecordingNothingKeepsWhatWasThere(t *testing.T) {
	// The package's own convention: XDG_CONFIG_HOME decides this path on Linux,
	// so setting HOME alone leaves every test in the package sharing one real
	// file — which is how this one first failed on CI and passed here.
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(t.TempDir(), "peers.json"))
	when := time.Now().Add(-time.Hour)
	if err := Record("mini", false, when, nil); err != nil {
		t.Fatal(err)
	}
	if err := Record("mini", false, time.Time{}, nil); err != nil {
		t.Fatal(err)
	}
	list := Load()
	if len(list) != 1 {
		t.Fatalf("peers = %v", list)
	}
	if got := list[0].LastPush; got.Sub(when.UTC()).Abs() > time.Second {
		t.Errorf("the earlier exchange was overwritten: %v", got)
	}
	// A host deja has never reached is remembered with nothing claimed.
	if err := Record("fresh", false, time.Time{}, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range Load() {
		if p.Host == "fresh" && (!p.LastPush.IsZero() || p.LastError != "") {
			t.Errorf("a host that was never contacted reads as %+v", p)
		}
	}
}

// A push with nothing to send tells us nothing about the machine, so the last
// failure stands: clearing it would say the host is fine when deja never
// contacted it (#1780).
func TestNothingToSendLeavesAnEarlierFailureStanding(t *testing.T) {
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(t.TempDir(), "peers.json"))
	if err := Record("mini", false, time.Now(), errors.New("connection refused")); err != nil {
		t.Fatal(err)
	}
	if err := Record("mini", false, time.Time{}, nil); err != nil {
		t.Fatal(err)
	}
	list := Load()
	if len(list) != 1 || list[0].LastError == "" {
		t.Errorf("the failure was cleared by a push that never opened a connection: %+v", list)
	}
	// A real exchange is what clears it.
	if err := Record("mini", false, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if got := Load()[0].LastError; got != "" {
		t.Errorf("a successful exchange left the old failure: %q", got)
	}
}

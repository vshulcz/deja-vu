package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A stored digest outlives the rule it was served under, and the only way to
// hold it to a later one was recognising project names inside its own prose —
// which fails as soon as those sessions leave the index. The record names its
// own projects now (#2324).
func TestASnapshotNamesTheProjectsItWasBuiltFrom(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordServedFrom(dir, KindRecall, "a digest of two projects", 2, 900,
		[]string{"claude:abc", "claude:def"},
		[]string{"work/app", "imported:secretclient/api"}, "local+imported")

	got := lastSnapshot(t, dir)
	if len(got.Projects) != 2 || got.Projects[0] != "work/app" || got.Projects[1] != "imported:secretclient/api" {
		t.Errorf("projects = %v, want both, in the order they were served", got.Projects)
	}
	if got.Kind != KindRecall || got.Sessions != 2 || got.Policy != "local+imported" {
		t.Errorf("the rest of the record changed: %+v", got)
	}
	// The paired event is written by the same call, stamped alike.
	events := Events(dir, 0)
	if len(events) != 1 || !events[0].Time.Equal(got.Time) {
		t.Errorf("events = %+v, want one stamped like the digest at %s", events, got.Time)
	}
}

// The déjà-vu hook knows the receiving session, the terms and the projects.
func TestADejaVuDigestRecordsTermsAndProjects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigestFrom(dir, KindDejaVu, "a block worth reading", "ses_1234", 1, 400,
		[]string{"quaxbolt"}, []string{"work/app"}, []string{"claude:abc"})

	got := lastSnapshot(t, dir)
	if got.Into != "ses_1234" || len(got.Terms) != 1 || len(got.Projects) != 1 {
		t.Errorf("record = %+v, want the receiver, the terms and the project", got)
	}
	if events := Events(dir, 0); len(events) != 1 || events[0].Into != "ses_1234" {
		t.Errorf("events = %+v, want the receiver on the event too", events)
	}
}

// handoff writes a digest of one session under a named policy and no receiver.
func TestAPolicyDigestWithProjectsOmitsWhatItDoesNotKnow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigestPolicyFrom(dir, KindHandoff, "a handoff", "", 1, 100,
		[]string{"work/app"}, "local-only")

	got := lastSnapshot(t, dir)
	if got.Into != "" || got.Policy != "local-only" || len(got.Projects) != 1 {
		t.Errorf("record = %+v", got)
	}
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"into"`) || strings.Contains(string(b), `"terms"`) {
		t.Errorf("empty fields were written out: %s", strings.TrimSpace(string(b)))
	}
}

// A writer that knows no projects leaves the field out, so a reader sees what
// it saw before the field existed.
func TestASnapshotWithoutProjectsOmitsTheField(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordServedSnapshot(dir, KindRecall, "a digest", 1, 100, []string{"claude:abc"}, "")

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"projects"`) {
		t.Errorf("empty projects were written out: %s", strings.TrimSpace(string(b)))
	}
}

func lastSnapshot(t *testing.T, dir string) Snapshot {
	t.Helper()
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var got Snapshot
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// forget takes the messages and the notes' borrowed titles; the digests served
// from those sessions stayed, so a page republished forgotten text (#2325).
func TestForgetSnapshotsDropsWhatItIsToldAndNothingElse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordServedFrom(dir, KindRecall, "a peer digest", 1, 100, nil, []string{"imported:client/api"}, "")
	RecordServedFrom(dir, KindRecall, "my own digest", 1, 100, nil, []string{"work/app"}, "")

	match := func(s Snapshot) bool {
		for _, p := range s.Projects {
			if p == "imported:client/api" {
				return true
			}
		}
		return false
	}
	if n := CountSnapshots(dir, match); n != 1 {
		t.Fatalf("CountSnapshots = %d, want the one record that matches", n)
	}
	gone, err := ForgetSnapshots(dir, match)
	if err != nil {
		t.Fatal(err)
	}
	if gone != 1 {
		t.Errorf("dropped %d records, want 1", gone)
	}
	left := Snapshots(dir, 0)
	if len(left) != 1 || left[0].Digest != "my own digest" {
		t.Errorf("what is left = %+v, want the other project's digest", left)
	}
	// The events stay: they say something was served, not what it said.
	if events := Events(dir, 0); len(events) != 2 {
		t.Errorf("events = %d, want both", len(events))
	}
	// A sweep that matches nothing rewrites nothing.
	if n, err := ForgetSnapshots(dir, match); err != nil || n != 0 {
		t.Errorf("second sweep dropped %d (err %v), want 0", n, err)
	}
}

// An empty log is not an error, and there is nothing to count in it.
func TestForgetSnapshotsOnAnEmptyLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if n, err := ForgetSnapshots(dir, func(Snapshot) bool { return true }); err != nil || n != 0 {
		t.Errorf("ForgetSnapshots on an empty log = %d, %v", n, err)
	}
	if n := CountSnapshots(dir, func(Snapshot) bool { return true }); n != 0 {
		t.Errorf("CountSnapshots on an empty log = %d", n)
	}
}

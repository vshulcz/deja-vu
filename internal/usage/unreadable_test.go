package usage

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// An injection whose host sent a payload deja could not decode is recorded
// either way — the memory did go out — and both writers carry the flag, so
// neither surface reads like a row from a host that sent nothing (#2161).
func TestAnUnreadablePayloadIsWrittenToBothLogs(t *testing.T) {
	dir := t.TempDir()
	RecordDigestPolicySessionsUnread(dir, KindHook, "a digest", "ses_half", 2, 4000, "local-only",
		[]string{"claude:one"}, []string{"beta"})

	events := read(Path(dir))
	if len(events) != 1 {
		t.Fatalf("want one event, got %d", len(events))
	}
	if !events[0].Unreadable {
		t.Errorf("the event does not say the payload could not be read: %+v", events[0])
	}
	// The receiver a half-decoded payload did name is kept: the flag is about
	// the payload, not about the id.
	if events[0].Into != "ses_half" {
		t.Errorf("the event dropped the receiver: %+v", events[0])
	}

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &snap); err != nil {
		t.Fatal(err)
	}
	if !snap.Unreadable {
		t.Errorf("the snapshot does not say the payload could not be read:\n%s", b)
	}
	if snap.Into != "ses_half" || snap.Policy != "local-only" || len(snap.Projects) != 1 {
		t.Errorf("the snapshot lost a field on the way: %+v", snap)
	}
}

// The ordinary recorders leave the flag off, so an older row and a readable
// one read the same.
func TestAReadablePayloadIsNotFlagged(t *testing.T) {
	dir := t.TempDir()
	RecordDigestPolicySessionsFrom(dir, KindHook, "a digest", "ses1", 2, 4000, "local-only", nil, nil)
	events := read(Path(dir))
	if len(events) != 1 || events[0].Unreadable {
		t.Errorf("a readable payload was flagged: %+v", events)
	}
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "unreadable") {
		t.Errorf("the snapshot carries the flag for a payload deja read:\n%s", b)
	}
}

// The déjà vu hook's own recorder: the prompt is what it recalls from, so a
// decode that fails on a later field still injects — and the row carries the
// receiver the payload did name beside the flag about the payload (#2773).
func TestTheDejaVuRecorderKeepsBothTheFlagAndTheReceiver(t *testing.T) {
	dir := t.TempDir()
	RecordDigestFromUnread(dir, KindDejaVu, "a digest", "ses_vu", 2, 4000,
		[]string{"pgbouncer"}, []string{"beta"}, []string{"claude:one"})

	events := read(Path(dir))
	if len(events) != 1 {
		t.Fatalf("want one event, got %d", len(events))
	}
	if !events[0].Unreadable || events[0].Into != "ses_vu" {
		t.Errorf("the event lost half of what it was told: %+v", events[0])
	}
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &snap); err != nil {
		t.Fatal(err)
	}
	if !snap.Unreadable || snap.Into != "ses_vu" || len(snap.Terms) != 1 {
		t.Errorf("the snapshot lost a field on the way: %+v", snap)
	}
}

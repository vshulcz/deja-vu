package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Both readers reverse the file and call the result newest first, so an event
// appended out of stamp order sat wherever it arrived — and the cut took the
// first N of that, dropping events newer than the ones it kept (#2140).
func TestEventsAndSnapshotsAreNewestByStamp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	at := func(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }

	var events strings.Builder
	for _, e := range []struct {
		kind string
		age  int
	}{{"hook", 5}, {"mcp", 1}, {"search", 9}} {
		line, err := json.Marshal(map[string]any{
			"t": at(e.age).UTC().Format(time.RFC3339Nano), "kind": e.kind, "bytes": 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		events.Write(line)
		events.WriteByte('\n')
	}
	if err := os.WriteFile(Path(dir), []byte(events.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Events(dir, 0)
	if len(got) != 3 {
		t.Fatalf("read %d events, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Time.After(got[i-1].Time) {
			t.Errorf("events are not newest first: %s then %s", got[i-1].Time, got[i].Time)
		}
	}
	// And the cut takes the newest N, not the last N appended.
	if two := Events(dir, 2); len(two) != 2 || two[0].Kind != "mcp" || two[1].Kind != "hook" {
		t.Errorf("Events(2) = %v, want the mcp and hook events — the two newest by stamp", kinds(two))
	}

	var snaps strings.Builder
	for _, s := range []struct {
		kind string
		age  int
	}{{"hook", 5}, {"mcp", 1}, {"search", 9}} {
		line, err := json.Marshal(map[string]any{
			"t": at(s.age).UTC().Format(time.RFC3339Nano), "kind": s.kind, "digest": "d-" + s.kind,
		})
		if err != nil {
			t.Fatal(err)
		}
		snaps.Write(line)
		snaps.WriteByte('\n')
	}
	if err := os.WriteFile(SnapshotPath(dir), []byte(snaps.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	// `deja log --last` reads snaps[0] as the most recent digest.
	if one := Snapshots(dir, 1); len(one) != 1 || one[0].Kind != "mcp" {
		t.Errorf("Snapshots(1) = %v, want the mcp digest — the newest by stamp", one)
	}
}

func kinds(es []Event) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Kind)
	}
	return out
}

// Events written in stamp order keep the order they arrived in when two share
// a stamp: the later line is the later event.
func TestEqualStampsKeepArrivalOrder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	var b strings.Builder
	for _, kind := range []string{"first", "second", "third"} {
		line, err := json.Marshal(map[string]any{"t": stamp, "kind": kind, "bytes": 1})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(Path(dir), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := kinds(Events(dir, 0))
	want := []string{"third", "second", "first"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Events = %v, want %v — with one stamp, the last appended is the newest", got, want)
		}
	}
}

// Reading is sorted; keeping is not. The rotation rewrites the file from what
// it reads, so ordering that by stamp would delete the digest served last
// whenever the clock was behind when it was written — the one thing the
// sibling rotation in usage.go refuses to do (#2140).
func TestRotationKeepsWhatArrivedLastNotWhatIsStampedLatest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := SnapshotPath(dir)
	now := time.Now()
	// Enough records to rotate, all stamped far in the past except the ones at
	// the end: the last appended carries the oldest stamp of the lot.
	body := strings.Repeat("x", RecordRoom/2)
	var b strings.Builder
	for i := range snapshotsToKeep * 3 {
		line, err := marshalSnapshot(Snapshot{
			Time: now.Add(-time.Duration(i) * time.Minute), Kind: "hook",
			Bytes: len(body), Digest: fmt.Sprintf("d%03d ", i) + body,
		})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	// Served last, by a machine whose clock was a year behind.
	last, err := marshalSnapshot(Snapshot{
		Time: now.AddDate(-1, 0, 0), Kind: "mcp", Bytes: len(body), Digest: "served-last " + body,
	})
	if err != nil {
		t.Fatal(err)
	}
	b.Write(last)
	b.WriteByte('\n')
	if err = os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < snapshotRotateAt {
		t.Fatalf("the file is %d bytes and rotation starts at %d, so this measures nothing", fi.Size(), snapshotRotateAt)
	}
	rotateSnapshots(p, 1)
	kept, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kept), "served-last") {
		t.Errorf("rotation dropped the digest that arrived last because its stamp was the oldest")
	}
}

// The rotation's fast path (#2220) reads each line's stamp as text, which works
// because the stamp is the first field and always UTC. Both are properties of
// the writers rather than of the file format, and moving `Time` down the struct
// — or stamping with an offset — would turn the scan off with nothing failing:
// the log would quietly go back to costing a full parse on every hook.
func TestEveryLineDejaWritesOpensWithAUTCStamp(t *testing.T) {
	dir := t.TempDir()
	RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
	RecordServedSessions(dir, KindHook, 600, 1, false, 6000, []string{"claude:a1"})
	RecordResultRaw(dir, KindDejaVu, 0, 0, true, 0)

	check := func(what string) {
		t.Helper()
		b, err := os.ReadFile(Path(dir))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if len(lines) == 0 || lines[0] == "" {
			t.Fatalf("%s: the log is empty, so this measures nothing", what)
		}
		for i, line := range lines {
			if !strings.HasPrefix(line, `{"t":"`) {
				t.Errorf("%s: line %d does not open with the stamp: %.60q", what, i, line)
				continue
			}
			stamp, _, _ := strings.Cut(line[len(`{"t":"`):], `"`)
			if !strings.HasSuffix(stamp, "Z") {
				t.Errorf("%s: line %d is stamped %q, not UTC", what, i, stamp)
			}
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				t.Errorf("%s: line %d carries %q, which is not RFC 3339: %v", what, i, stamp, err)
			}
		}
	}
	check("as written")

	// And after a rotation, which rewrites every line it keeps.
	old := Event{Time: time.Now().UTC().Add(-40 * 24 * time.Hour), Kind: KindRecall, Bytes: 1, Sessions: 1}
	appendEventForTest(t, dir, old)
	rotate(Path(dir))
	check("after a rotation")

	// The snapshot log is written by a second encoder with its own settings.
	RecordDigestTerms(dir, KindHook, "a digest with <html> & \"quotes\"", 1, 100, []string{"pool"}, "claude:a1")
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.HasPrefix(line, `{"t":"`) {
			t.Errorf("snapshot line %d does not open with the stamp: %.60q", i, line)
		}
	}
}

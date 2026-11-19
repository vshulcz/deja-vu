package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// When every event is older than the window it keeps, the rotation falls back
// to the newest `keepAtLeast`. Those have to fit under `rotateAt`, or the
// rotation leaves the file over its own threshold and the next write rotates
// again — the state #1971 measured in the injection log, where half of two
// concurrent injections was rewritten away.
//
// The relationship is between three numbers in this file and one thing outside
// it: how much an event can carry. Only `ids` grows, and it grows with the
// sessions an answer holds, which its own byte budget bounds. This states the
// room each event has; `TestARealEventFitsItsRoom` in cmd/deja holds the
// writers to it, measured at 2.9 kB for the widest a writer produces.
//
// The fallback is the branch this bounds. The ordinary rotation — keep what is
// inside the window — has no size rule, and the comment on EventRoom says what
// that leaves open.
func TestTheKeptEventsFitTheLogTheyAreKeptIn(t *testing.T) {
	if keepAtLeast*EventRoom >= rotateAt {
		t.Errorf("%d events of %d bytes is %d, and the log rotates at %d: a rotation would leave it over its own threshold",
			keepAtLeast, EventRoom, keepAtLeast*EventRoom, rotateAt)
	}
}

// And an event of that size really is that size — the bound is on the record as
// written, not on the fields before they are marshalled.
func TestAnEventAtItsRoomIsWithinIt(t *testing.T) {
	dir := t.TempDir()
	// Wider than any writer produces: a filename's worth of id, twenty times
	// over, against the eleven a real recall carried.
	ids := make([]string, 20)
	for i := range ids {
		ids[i] = strings.Repeat("a", 70)
	}
	RecordServedSessions(dir, KindRecall, 4096, len(ids), false, 40960, ids)

	events := read(Path(dir))
	if len(events) != 1 {
		t.Fatalf("wrote one event, read %d", len(events))
	}
	b, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	if n := len(b); n > EventRoom {
		t.Errorf("an event carrying %d ids is %d bytes against %d of room", len(ids), n, EventRoom)
	}
}

// oneLine writes a log holding exactly the given line.
func oneLine(t *testing.T, line string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The counters kept a line with a stamp; `deja log` kept one with a kind. So a
// half-written line was a row on one surface and nothing on the other — and a
// line with no kind, which no counter has a case for, still set `since`, the
// window every figure on the impact screen is measured from (#1917).
func TestBothReadersAgreeOnWhatAnEventIs(t *testing.T) {
	for _, c := range []struct {
		name, line string
		kept       bool
	}{
		{"whole", `{"t":"2026-08-20T10:00:00Z","kind":"recall","bytes":100,"sessions":1}`, true},
		{"unknown kind", `{"t":"2026-08-20T10:00:00Z","kind":"weather","bytes":100}`, true},
		{"no stamp", `{"kind":"recall","bytes":100,"sessions":1}`, false},
		{"no kind", `{"t":"2026-08-20T10:00:00Z","bytes":100,"sessions":1}`, false},
		{"neither", `{"bytes":100,"sessions":1}`, false},
	} {
		dir := oneLine(t, c.line)
		byCounters := len(read(Path(dir)))
		byLog := len(Events(dir, 0))
		if byCounters != byLog {
			t.Errorf("%s: the counters read %d and deja log reads %d", c.name, byCounters, byLog)
		}
		want := 0
		if c.kept {
			want = 1
		}
		if byCounters != want {
			t.Errorf("%s: kept %d, want %d", c.name, byCounters, want)
		}
	}
}

// A line no counter can count must not decide the period the counts cover.
func TestALineWithNoKindDoesNotMoveTheWindow(t *testing.T) {
	dir := oneLine(t, `{"t":"2020-01-01T00:00:00Z","bytes":100,"sessions":1}`)
	if tot := Totals(dir); !tot.Since.IsZero() {
		t.Errorf("a kindless line pulled the window back to %s", tot.Since.Format("2006-01-02"))
	}
	if imp := Impact(dir); !imp.Since.IsZero() {
		t.Errorf("the impact screen dates its counts from %s", imp.Since.Format("2006-01-02"))
	}
}

// An unknown kind is a different thing: deja may have written it, an older or
// newer version of itself, so it stays an event even though nothing counts it.
func TestAnUnknownKindStaysAnEvent(t *testing.T) {
	dir := oneLine(t, `{"t":"2026-08-20T10:00:00Z","kind":"weather","bytes":100}`)
	if got := len(Events(dir, 0)); got != 1 {
		t.Errorf("deja log hides an event it does not recognise: %d", got)
	}
	if tot := Totals(dir); tot.Since.IsZero() {
		t.Error("an unknown kind stopped counting towards the window")
	}
}

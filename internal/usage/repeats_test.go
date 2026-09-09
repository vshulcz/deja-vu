package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeEvents(t *testing.T, dir string, events ...Event) {
	t.Helper()
	var b []byte
	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b = append(append(b, line...), '\n')
	}
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Several injections into one session inside a second is the fingerprint of a
// config that collected deja's hook more than once (#3421): the hooks dedupe
// per session and per prompt, so one prompt is one injection.
func TestRepeatedInjectionsCountsOneSecond(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	writeEvents(t, dir,
		Event{Time: at, Kind: KindDejaVu, Bytes: 800, Into: "a"},
		Event{Time: at.Add(time.Millisecond), Kind: KindDejaVu, Bytes: 800, Into: "a"},
		// A later second, one injection: ordinary.
		Event{Time: at.Add(5 * time.Second), Kind: KindDejaVu, Bytes: 800, Into: "a"},
		// Another session in the same second is a fleet, not a repeat.
		Event{Time: at, Kind: KindDejaVu, Bytes: 800, Into: "b"},
		// A tool line and a prompt block in the same second are two channels,
		// not one hook firing twice.
		Event{Time: at, Kind: KindTool, Bytes: 90, Into: "a"},
		// Not an injection at all: an agent calling a tool twice is busy.
		Event{Time: at, Kind: KindRecall, Bytes: 500, Into: "a"},
		Event{Time: at, Kind: KindRecall, Bytes: 500, Into: "a"},
		// No receiver recorded, so nothing can be said about repeats.
		Event{Time: at, Kind: KindDejaVu, Bytes: 800},
		Event{Time: at, Kind: KindDejaVu, Bytes: 800},
	)

	got := RepeatedInjections(dir)
	if len(got) != 1 {
		t.Fatalf("repeats = %+v, want the one doubled session", got)
	}
	if got[0].Into != "a" || got[0].Kind != KindDejaVu || got[0].Count != 2 {
		t.Errorf("repeat = %+v", got[0])
	}
	if !got[0].At.Equal(at.Add(time.Millisecond)) {
		t.Errorf("At = %v, want the last of the pair", got[0].At)
	}
}

// A clean log says nothing.
func TestRepeatedInjectionsIsSilentOnAHealthyLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	writeEvents(t, dir,
		Event{Time: at, Kind: KindDejaVu, Bytes: 800, Into: "a"},
		Event{Time: at.Add(time.Minute), Kind: KindHook, Bytes: 800, Into: "a"},
	)
	if got := RepeatedInjections(dir); got != nil {
		t.Errorf("repeats = %+v, want none", got)
	}
	// And a machine with no log at all.
	if got := RepeatedInjections(filepath.Join(t.TempDir(), "index.db")); got != nil {
		t.Errorf("repeats = %+v on a machine that has never served one", got)
	}
}

// Worst first when two sessions doubled in the same second, newest first
// otherwise — the reader reads the top line.
func TestRepeatedInjectionsOrdersWhatMatters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	var events []Event
	for i := 0; i < 3; i++ {
		events = append(events, Event{Time: at, Kind: KindDejaVu, Bytes: 800, Into: "old"})
	}
	for i := 0; i < 2; i++ {
		events = append(events, Event{Time: at.Add(time.Hour), Kind: KindDejaVu, Bytes: 800, Into: "new"})
	}
	writeEvents(t, dir, events...)

	got := RepeatedInjections(dir)
	if len(got) != 2 {
		t.Fatalf("repeats = %+v, want both sessions", got)
	}
	if got[0].Into != "new" {
		t.Errorf("order = %+v, want the newest first", got)
	}

	// Same second, different counts: the heavier one leads.
	writeEvents(t, dir,
		Event{Time: at, Kind: KindDejaVu, Bytes: 800, Into: "light"},
		Event{Time: at, Kind: KindDejaVu, Bytes: 800, Into: "light"},
		Event{Time: at, Kind: KindTool, Bytes: 90, Into: "heavy"},
		Event{Time: at, Kind: KindTool, Bytes: 90, Into: "heavy"},
		Event{Time: at, Kind: KindTool, Bytes: 90, Into: "heavy"},
	)
	if got := RepeatedInjections(dir); len(got) != 2 || got[0].Into != "heavy" {
		t.Errorf("order = %+v, want the worst first", got)
	}
}

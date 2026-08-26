package usage

import (
	"encoding/json"
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

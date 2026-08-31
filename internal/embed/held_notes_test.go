package embed

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func heldIDs(hits []search.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Session.ID)
	}
	return out
}

func noteMatch(id string, score float64) match {
	return match{
		session: model.Session{ID: id, Harness: "deja"},
		record:  index.Record{Text: "pool cap settled at 40"},
		score:   score,
	}
}

// A promoted note is one distilled line where its transcript is a whole
// session, so it is often worded further from the query than the source it was
// made from. The floor then cut the note while serving the source — and the
// answer `promote` says it just put in front was not in the result at all,
// which is the "not in fifty results" case of #2803.
func TestANoteBelowTheFloorIsServedWithItsSource(t *testing.T) {
	out := []search.Hit{
		{Session: model.Session{ID: "other", Harness: "claude"}, Score: 0.81},
		{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 0.62},
	}
	held := map[string]match{
		"deja:deja-note-claude-sess1": noteMatch("deja-note-claude-sess1", 0.30),
		// A note whose source nobody asked about stays out.
		"deja:deja-note-claude-elsewhere": noteMatch("deja-note-claude-elsewhere", 0.40),
	}
	got := withHeldNotes(out, held)
	search.LiftNotesAboveTheirSource(got)

	ids := heldIDs(got)
	want := []string{"other", "deja-note-claude-sess1", "sess1"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v, want %v", ids, want)
		}
	}
}

// Nothing held, nothing changed — and a held note whose source is absent is not
// a result on its own.
func TestHeldNotesTravelOnlyWithTheirSource(t *testing.T) {
	out := []search.Hit{{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 0.7}}
	if got := withHeldNotes(out, nil); len(got) != 1 {
		t.Errorf("nothing was held and the answer changed: %v", heldIDs(got))
	}
	held := map[string]match{"deja:deja-note-claude-gone": noteMatch("deja-note-claude-gone", 0.4)}
	if got := withHeldNotes(out, held); len(got) != 1 {
		t.Errorf("a note whose source is not in the answer was served anyway: %v", heldIDs(got))
	}
}

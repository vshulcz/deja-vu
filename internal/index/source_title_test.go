package index

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A title the source authored — a cursor composer name, a goose description, a
// zed thread summary — reached the one-line surfaces whole: 384 characters
// with the line breaks still in it, so one session printed several rows of
// `deja last` and the text after a break began "[claude · …", which is that
// listing's own format. Derived titles have been collapsed and bounded since
// they existed; this is the same bound on the other branch.
func TestSourceTitleIsBoundedLikeADerivedOne(t *testing.T) {
	msg := model.Message{Role: "user", Text: "the retry queue stalls on staging and every worker wakes at once, needs jitter"}
	for _, tc := range []struct {
		name  string
		title string
	}{
		{"multiline", "retry queue jitter\n[claude · fake · 2026-08-19 · deadbeef] a line in the listing's own format"},
		{"long", strings.Repeat("the retry queue stalls on staging and the workers wake together ", 6)},
		{"tabbed", "retry\tqueue\tjitter\tand\tbackoff"},
		{"carriage return", "retry queue jitter\r[claude · fake] rewound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := metaForSession(model.Session{
				Harness: "cursor", ID: "c1", Project: "p", Title: tc.title,
				Messages: []model.Message{msg},
			})
			if strings.ContainsAny(m.Title, "\n\r\t") {
				t.Errorf("the title still breaks the line: %q", m.Title)
			}
			// 61 is the ceiling, not the length: 60 characters plus the mark,
			// and TrimSpace can leave it shorter.
			if n := utf8.RuneCountInString(m.Title); n > 61 {
				t.Errorf("the title is %d characters: %q", n, m.Title)
			}
		})
	}
}

// What the source said still wins over what deja would derive, and a title
// that already fits arrives untouched — no mark claiming something was cut.
func TestSourceTitleShortEnoughIsUnchanged(t *testing.T) {
	const title = "retry queue jitter"
	m := metaForSession(model.Session{
		Harness: "cursor", ID: "c1", Project: "p", Title: title,
		Messages: []model.Message{{Role: "user", Text: "something else entirely"}},
	})
	if m.Title != title {
		t.Errorf("metaForSession rewrote a title that fits: %q", m.Title)
	}
	if m.AgentTitle {
		t.Error("a source title is not an agent title")
	}
}

// A derived title is unchanged by this: it was already collapsed and cut.
func TestDerivedTitleUnchanged(t *testing.T) {
	m := metaForSession(model.Session{
		Harness: "claude", ID: "d1", Project: "p",
		Messages: []model.Message{{Role: "user", Text: strings.Repeat("the retry queue stalls on staging ", 8)}},
	})
	if n := utf8.RuneCountInString(m.Title); n > 61 {
		t.Errorf("derived title is %d characters: %q", n, m.Title)
	}
	if !strings.HasSuffix(m.Title, "…") {
		t.Errorf("a cut derived title lost its mark: %q", m.Title)
	}
}

// A promoted note's title ends in its state, and the state is what the
// one-line surfaces read it for. The bound must not cut it off, and a state
// change must still be visible to whatever compares the two.
func TestPromotedNoteTitleKeepsItsState(t *testing.T) {
	long := strings.Repeat("the retry queue stalls on staging ", 3)
	for _, state := range []string{"accepted", "rejected", "superseded"} {
		title := strings.TrimSpace(long) + " [" + state + "]"
		m := metaForSession(model.Session{
			Harness: "deja", ID: "deja-2026-08-19-app", Project: "p", Title: title,
			Messages: []model.Message{{Role: "user", Text: "[" + state + "] the retry queue needs jitter"}},
		})
		if !strings.HasSuffix(m.Title, "["+state+"]") {
			t.Errorf("the state was cut off the note title: %q", m.Title)
		}
	}
	// And a note whose state changes has a different title, so the row is
	// rewritten rather than left saying the old one.
	one := metaForSession(model.Session{Harness: "deja", ID: "n", Title: strings.TrimSpace(long) + " [accepted]"})
	two := metaForSession(model.Session{Harness: "deja", ID: "n", Title: strings.TrimSpace(long) + " [rejected]"})
	if one.Title == two.Title {
		t.Errorf("two states produced one title: %q", one.Title)
	}
}

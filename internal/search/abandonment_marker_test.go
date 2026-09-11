package search

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// GaveUp is a property of a whole session: one line anywhere saying something
// was dropped marks all of it. On a session someone can scan, "one path here was
// abandoned — check the excerpts" names a real thing to look for. On a marathon
// it is certain and therefore empty: measured over twelve real queries, the mark
// sat on 23 of 60 hits, and the sessions carrying it were this machine's longest
// — 4.2 million words, 3.1 million, 2.0 million.
func TestTheAbandonmentMarkIsNotPutOnAMarathon(t *testing.T) {
	hit := func(words int) Hit {
		return Hit{
			Session: model.Session{
				Harness: "claude", ID: "s1", Project: "app", GaveUp: true, Words: words,
				Messages: []model.Message{{Role: "user", Text: "the exporter retries too hard"}},
			},
			Count:    1,
			Snippets: []string{"the exporter retries too hard"},
		}
	}
	render := func(h Hit) string {
		var b bytes.Buffer
		Print(&b, []Hit{h}, query.Options{Query: "exporter"})
		return b.String()
	}

	const mark = "mentions backing an approach out"
	if got := render(hit(2000)); !strings.Contains(got, mark) {
		t.Errorf("a session a reader can scan lost the mark:\n%s", got)
	}
	if got := render(hit(4_264_226)); strings.Contains(got, mark) {
		t.Errorf("a 4.2-million-word session was marked as having abandoned one path:\n%s", got)
	}
	// A session whose length was never recorded keeps it: an unknown is not a
	// marathon.
	if got := render(hit(0)); !strings.Contains(got, mark) {
		t.Errorf("a session with no recorded length lost the mark:\n%s", got)
	}
}

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// bridgedStore holds the three things a rephrased question needs to be
// answerable: the session that settled it in the project's own word, a session
// that explains that word in ordinary ones, and enough other sessions that the
// ordinary words are not rare by accident.
func bridgedStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	var sessions []model.Session
	for i := 0; i < 80; i++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("filler%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "notes on the weekly planning meeting and the invoice"}},
		})
	}
	// Said in three sessions, which is the evidence the co-occurrence map asks
	// for before it records a pairing.
	for i := 0; i < 3; i++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("explains%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: "the debounce is how long we wait before sending what someone typed"}},
		})
	}
	sessions = append(sessions, model.Session{
		ID: "answer", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "we set the debounce on the search box to 250ms"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A question asked in words the project does not use reaches the session that
// settled it, through the sentence that ties the two vocabularies.
//
// The rescue rung in cooccur.go cannot: it intersects postings, so the best it
// can reach is the record holding both vocabularies — the sentence that
// explains the word, never the session that used it and settled something.
func TestARephrasedQuestionReachesTheAnswer(t *testing.T) {
	dir := bridgedStore(t)
	terms := []string{"long", "wait", "sending", "typed"}
	ranked, _, _, _, err := ProjectRelevant(dir, []string{"p"}, terms, 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	got := make([]string, 0, len(ranked))
	for _, s := range ranked {
		got = append(got, s.ID)
		if s.ID == "answer" {
			found = true
		}
	}
	if !found {
		t.Errorf("the session that settled it was never reached: %v", got)
	}
}

// The same machinery must not answer a question whose subject the project never
// held. Its words are ordinary, and the corpus's neighbours for ordinary words
// are every subject it has — which is how a bridge becomes a machine for
// confident wrong answers.
func TestAnAbsentSubjectIsNotBridgedTo(t *testing.T) {
	dir := bridgedStore(t)
	ranked, _, strong, _, err := ProjectRelevant(dir, []string{"p"}, []string{"open", "file", "read"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range ranked {
		if s.ID == "answer" && strong[i] > 0 {
			t.Errorf("a question about something the project never held was "+
				"answered with %q", s.ID)
		}
	}
}

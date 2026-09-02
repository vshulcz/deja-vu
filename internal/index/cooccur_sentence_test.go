package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// The co-occurrence map holds the link a reworded question needs, and the tier
// that reads it required the whole AND to match after one substitution — so it
// could only ever fire on a query short enough not to need it. A sentence,
// which is what an agent and a person both type, never matched (#2331).
func TestTheNeighbourRescueFiresOnASentence(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	var sessions []model.Session
	// Ordinary work, so the question's ordinary words are not rare by accident.
	for i := 0; i < 60; i++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("noise%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: fmt.Sprintf("the go service on shard %d was restarted after the weekly deploy", i)}},
		})
	}
	// The sentence that ties the reader's word to the project's, said in three
	// sessions, which is the evidence the map asks for.
	for i := 0; i < 3; i++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("explains%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: "pgbouncer is the connection proxy we put in front of postgres"}},
		})
	}
	// What the question is really after, in the project's own word.
	sessions = append(sessions, model.Session{
		ID: "answer", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user",
			Text: "the pgbouncer pool ran out of connections and the go service could not open one"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}

	// A sentence, not two words: the question the issue was written about.
	// Asked of the tier itself, because the ranked path finds the session by
	// other means and would hide whether this rung fired at all.
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	o := query.Options{Query: "why would a go service exhaust its postgres pool behind a connection proxy", All: true}
	res, err := cooccurNarrowedSearch(dir, m, o)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range res.Sessions {
		ids = append(ids, s.ID)
	}
	if !strings.Contains(strings.Join(ids, " "), "answer") {
		t.Errorf("the neighbour rescue did not fire on a sentence: tier=%q %v", res.Tier, ids)
	}
	if res.Variants["proxy"] == nil {
		t.Errorf("the swap is not narrated: %v", res.Variants)
	}
}

// The narrowing takes the words that identify something, not the first three
// it sees: a question is mostly ordinary words and they are what the AND was
// failing on.
func TestTheNarrowedAndKeepsOnlyIdentifyingWords(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	var sessions []model.Session
	for i := 0; i < 60; i++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("noise%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: fmt.Sprintf("the service was restarted after the deploy on shard %d", i)}},
		})
	}
	sessions = append(sessions, model.Session{
		ID: "rare", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "the zephyrine quarto ledger was rebuilt"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := identifyingTerms(dir, m, []string{"the", "service", "was", "restarted", "zephyrine", "quarto"}, nil)
	for _, ordinary := range []string{"service", "restarted"} {
		for _, g := range got {
			if g == ordinary {
				t.Errorf("an ordinary word was kept: %v", got)
			}
		}
	}
	if len(got) == 0 {
		t.Fatalf("every word was dropped: %v", got)
	}
}

// And the rung is wired where it belongs: below the ranked tier, which answers
// better wherever it answers at all. Asked through the ladder, a sentence
// whose subject the project calls something else comes back with the session
// that settled it.
func TestTheLadderReachesTheNeighbourRescue(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	var sessions []model.Session
	for i := 0; i < 60; i++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("noise%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: fmt.Sprintf("weekly deploy notes for shard %d, nothing unusual", i)}},
		})
	}
	for i := 0; i < 3; i++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("explains%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: "zephyrine is the quarto scheduler we run the nightly jobs on"}},
		})
	}
	sessions = append(sessions, model.Session{
		ID: "answer", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user",
			Text: "quarto was moved to the first of the month"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	res, err := SearchDetailed(dir, query.Options{
		Query: "when did we last change the zephyrine schedule", All: true})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range res.Sessions {
		ids = append(ids, s.ID)
	}
	if !strings.Contains(strings.Join(ids, " "), "answer") {
		t.Errorf("the ladder never reached the rescue: tier=%q %v", res.Tier, ids)
	}
}

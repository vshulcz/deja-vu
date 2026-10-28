package index

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// fellIndex indexes one single-message session per entry, id -> project, text.
func fellIndex(t *testing.T, sessions [][3]string) string {
	t.Helper()
	dir := filepath.Join(hermeticIndexEnv(t), "idx")
	ss := make([]model.Session, 0, len(sessions))
	for i, s := range sessions {
		ss = append(ss, model.Session{
			ID: s[0], Harness: "claude", Project: s[1], Updated: time.Now().Add(-time.Duration(i) * time.Minute),
			Messages: []model.Message{{Role: "user", Text: s[2]}},
		})
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

var fellCorpus = [][3]string{
	{"a1", "alpha", "the walrus importer stalled on the third batch"},
	{"a2", "alpha", "walrus dashboards were slow on monday"},
	{"a3", "alpha", "lunch order for the offsite"},
	{"b1", "beta", "billing tables moved to the new cluster"},
	{"b2", "beta", "billing export runs nightly now"},
	{"b3", "beta", "walrus logo for the slides"},
	{"me", "alpha", "the walrus billing pelican run broke again"},
}

func resultIDs(r SearchResult) []string {
	out := make([]string, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		out = append(out, s.ID)
	}
	return out
}

// Recall asks the whole machine first and the project after it. When every
// rung above the ranked tier came back empty for the machine, the project's
// search starts at the ranked tier and answers the same as the whole ladder.
func TestANarrowerSearchStartsWhereTheMachineFell(t *testing.T) {
	dir := fellIndex(t, fellCorpus)
	mine := map[string]bool{"me": true}
	machine, err := SearchDetailed(dir, search.Options{Query: "walrus billing pelican", All: true, ExcludeSessions: mine})
	if err != nil {
		t.Fatal(err)
	}
	if machine.Fell == 0 || machine.Tier != query.TierRelevance {
		t.Fatalf("the machine's search did not fall to the ranked tier: fell=%d tier=%s %v", machine.Fell, machine.Tier, resultIDs(machine))
	}
	o := search.Options{Query: "walrus billing pelican", All: true, ExcludeSessions: mine, Projects: []string{"alpha"}}
	whole, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	o.FellThrough = machine.Fell
	short, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.Sessions) == 0 || !slices.Equal(resultIDs(whole), resultIDs(short)) || whole.Tier != short.Tier {
		t.Fatalf("starting at the ranked tier changed the answer: ladder %v (%s), from the ranked tier %v (%s)",
			resultIDs(whole), whole.Tier, resultIDs(short), short.Tier)
	}
}

// A search a rung answers has not fallen, and a narrower one must walk the
// ladder itself.
func TestAnAnsweredSearchHasNotFallen(t *testing.T) {
	dir := fellIndex(t, fellCorpus)
	r, err := SearchDetailed(dir, search.Options{Query: "walrus importer", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Fell != 0 || len(r.Sessions) == 0 {
		t.Fatalf("an exact answer is marked as fallen: fell=%d %v", r.Fell, resultIDs(r))
	}
}

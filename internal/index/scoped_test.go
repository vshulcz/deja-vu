package index

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The sessions PoolScope adds are read only for the project's answer. Read
// with the pool, they decided the machine search's own tier: a quoted phrase
// whose pool held no session saying it came back exact, built from the
// project's sessions alone, where without them the search went on down the
// ladder (#5486).
func TestPoolScopeDoesNotDecideTheWiderAnswer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	now := time.Now()
	n := search.PoolSize(5) + 300
	var ss []model.Session
	for i := 0; i < n; i++ {
		at := now.Add(-time.Duration(i) * time.Minute)
		text, project := fmt.Sprintf("alpha went first and beta went after, run %d", i), "other"
		if i >= n-12 {
			// Older, so the pool leaves them out; they say the phrase.
			text, project = fmt.Sprintf("alpha beta settled it, run %d", i), "shop"
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s-%05d", i), Project: project, Started: at, Updated: at,
			Messages: []model.Message{{Role: "user", Text: text, Time: at}}})
	}
	writeTestStore(t, dir, ss...)

	o := query.Options{Query: `"alpha beta"`, Window: 5}
	plain, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	o.PoolScope = []string{"shop"}
	scoped, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Tier != plain.Tier {
		t.Errorf("with the project's sessions read alongside, the tier is %q, without them %q", scoped.Tier, plain.Tier)
	}
	own := 0
	for _, s := range scoped.Sessions {
		if scoped.Unread == nil || !scoped.Unread.Scoped[s.Harness+":"+s.ID] {
			own++
		}
	}
	if own != len(plain.Sessions) {
		t.Errorf("the wider search ranks %d sessions of its own, without the scope %d", own, len(plain.Sessions))
	}

	// A scope no session belongs to adds nothing to read. It handed the scan
	// no offsets, which reads the whole record log: 20 s and 3 GB on a 40k
	// store.
	o.Query, o.PoolScope = "alpha", []string{"nowhere"}
	scanned := 0
	wholeLogScanned = func() { scanned++ }
	defer func() { wholeLogScanned = nil }()
	r, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if scanned > 0 {
		t.Errorf("an empty scope scanned %d records of the whole log", scanned)
	}
	if r.Unread == nil || !r.Unread.ScopeWhole || len(r.Unread.Scoped) > 0 {
		t.Errorf("an empty scope: unread %+v", r.Unread)
	}
}

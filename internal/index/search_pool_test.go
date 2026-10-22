package index

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A word most of the store holds matched every session, and ranking read and
// scored each one to show fifteen: 22 s and 2.6 GB on a 40k-session store
// (#5467). A search that reads a window hands ranking the pool and the sample
// that checks the length estimate, and counts the rest without reading it.
func TestCommonWordSearchScoresOnlyThePool(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	now := time.Now()
	n := search.PoolSize(15) + 600
	ss := make([]model.Session, 0, n)
	// Sessions that differ in age, length and how often they say the word,
	// so the page depends on more than which is newest.
	filler := strings.Repeat("checked the service config and the queue settings again ", 8)
	for i := range n {
		at := now.Add(-time.Duration(i%97) * 24 * time.Hour).Add(-time.Duration(i) * time.Minute)
		ms := []model.Message{{Role: "user", Text: fmt.Sprintf("the deploy of service %d is slow", i), Time: at}}
		for j := range i % 5 {
			ms = append(ms, model.Message{Role: "assistant", Text: strings.Repeat("deploy ", 1+(i*7+j)%4) + filler[:(i*13+j*29)%len(filler)], Time: at})
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s-%05d", i), Project: "shop", Started: at, Updated: at, Messages: ms})
	}
	writeTestStore(t, dir, ss...)

	// Without a window every matching session is read: the control.
	full, err := SearchDetailed(dir, query.Options{Query: "deploy", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Sessions) != n || full.Unread != nil {
		t.Fatalf("an unwindowed search read %d of %d sessions", len(full.Sessions), n)
	}

	o := query.Options{Query: "deploy", Window: 15}
	res, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil {
		t.Fatalf("a search reading 15 hits scored all %d sessions the word matched", len(res.Sessions))
	}
	if want := search.PoolSize(15) + len(res.Unread.Sample); len(res.Sessions) != want {
		t.Errorf("scored %d sessions, want the pool and its sample, %d", len(res.Sessions), want)
	}
	if got := len(res.Sessions) + res.Unread.Sessions; got != n {
		t.Errorf("read and unread add up to %d, want the %d that matched", got, n)
	}

	o.Unread = res.Unread
	r, err := search.RunDetailed(res.Sessions, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != n || !r.Capped {
		t.Errorf("total %d capped %v, want %d capped", r.Total, r.Capped, n)
	}
	whole, err := search.Run(full.Sessions, query.Options{Query: "deploy", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 15 {
		t.Fatalf("page holds %d hits, want 15", len(r.Hits))
	}
	for i, h := range r.Hits {
		if h.Session.ID != whole[i].Session.ID {
			t.Errorf("hit %d is %s, the full ranking has %s", i+1, h.Session.ID, whole[i].Session.ID)
		}
	}
}

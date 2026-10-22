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

// phraseStore holds apart sessions that say alpha and beta in one message
// but never together, and five that say "alpha beta".
func phraseStore(t *testing.T, apart int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "index.db")
	now := time.Now()
	ss := make([]model.Session, 0, apart+5)
	for i := 0; i < apart+5; i++ {
		at := now.Add(-time.Duration(i) * time.Minute)
		text := fmt.Sprintf("alpha went out first and beta followed in run %d", i)
		if i%(apart/5+1) == 0 && i/(apart/5+1) < 5 {
			text = fmt.Sprintf("the alpha beta rollout of run %d", i)
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s-%05d", i), Project: "shop", Started: at, Updated: at,
			Messages: []model.Message{{Role: "user", Text: text, Time: at}}})
	}
	writeTestStore(t, dir, ss...)
	return dir
}

// The postings say which records hold every word of a quoted phrase, not
// whether the words stand together, and a paged search counted every unread
// session they named: "alpha beta" said 106 matched where 5 say it (#5495).
// The unread ones are checked against the text the way the read ones are.
// The phrase sessions are spread so that two of them fall outside the pool.
func TestQuotedPhraseCountsOnlyTheSessionsThatSayIt(t *testing.T) {
	apart := search.PoolSize(15) + 900
	dir := phraseStore(t, apart)

	// The control: a plain word's postings are exact, and every session
	// holding it counts.
	res, err := SearchDetailed(dir, query.Options{Query: "alpha", Window: 15})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil || res.Unread.Estimated {
		t.Fatalf("control: unread %+v", res.Unread)
	}
	if got := len(res.Sessions) + res.Unread.Sessions; got != apart+5 {
		t.Errorf("control: read and unread add up to %d, want %d", got, apart+5)
	}

	o := query.Options{Query: `"alpha beta"`, Window: 15}
	res, err = SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil {
		t.Fatalf("the phrase search read every candidate")
	}
	o.Unread = res.Unread
	r, err := search.RunDetailed(res.Sessions, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 5 || r.Estimated {
		t.Errorf("total %d estimated %v, want the 5 sessions that say it", r.Total, r.Estimated)
	}
	// The ones the check found outside the pool are ranked with it, so the
	// page is not short of what the total says.
	if len(r.Hits) != 5 {
		t.Errorf("page holds %d hits, want all 5", len(r.Hits))
	}
}

// Past the budget the unread sessions are not checked one by one: their count
// is scaled by the sample read with the pool, and says it is an estimate.
func TestQuotedPhrasePastTheBudgetIsAnEstimate(t *testing.T) {
	apart := search.PoolSize(15) + 900
	dir := phraseStore(t, apart)
	defer func(b int) { verifyUnreadBudget = b }(verifyUnreadBudget)
	verifyUnreadBudget = 10

	o := query.Options{Query: `"alpha beta"`, Window: 15}
	res, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil || !res.Unread.Estimated {
		t.Fatalf("unread %+v, want an estimate", res.Unread)
	}
	o.Unread = res.Unread
	r, err := search.RunDetailed(res.Sessions, o)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Estimated || r.Total > 50 {
		t.Errorf("total %d estimated %v, want an estimate near 5", r.Total, r.Estimated)
	}
}

// A plain word some sessions hold only in a command: the read drops those for
// their role, not because the postings were wrong, and the check re-read every
// unread match of the word on that miss alone. Only the unread sessions that
// hold it in tool records alone are checked, and the count is the ones that
// say it.
func TestAPlainWordInCommandsChecksOnlyTheCommandOnlySessions(t *testing.T) {
	n := search.PoolSize(15) + 900
	dir := filepath.Join(t.TempDir(), "index.db")
	now := time.Now()
	ss := make([]model.Session, 0, n)
	spoken := 0
	for i := 0; i < n; i++ {
		at := now.Add(-time.Duration(i) * time.Minute)
		msg := model.Message{Role: "user", Text: fmt.Sprintf("the zzquokka rollout of run %d", i), Time: at}
		if i%10 == 1 {
			msg = model.Message{Role: roleCommand, Text: fmt.Sprintf("make zzquokka RUN=%d", i), Time: at}
		} else {
			spoken++
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s-%05d", i), Project: "shop", Started: at, Updated: at,
			Messages: []model.Message{{Role: "user", Text: fmt.Sprintf("run %d", i), Time: at}, msg}})
	}
	writeTestStore(t, dir, ss...)
	unreadPostingsChecked.Store(0)
	res, err := SearchDetailed(dir, query.Options{Query: "zzquokka", Window: 15})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil || res.Unread.Sessions == 0 {
		t.Fatalf("every match was read: %d sessions, unread %+v", len(res.Sessions), res.Unread)
	}
	if got := len(res.Sessions) + res.Unread.Sessions; got != spoken {
		t.Errorf("read and unread add up to %d, want the %d sessions that say it", got, spoken)
	}
	if got := unreadPostingsChecked.Load(); got > int64(n/10+1) {
		t.Errorf("checked %d postings, more than the %d sessions holding the word only in a command", got, n/10+1)
	}
}

// Past the budget the sessions holding the word only in a command are not
// checked one by one, and the count is scaled and says so.
func TestAPlainWordInCommandsPastTheBudgetIsAnEstimate(t *testing.T) {
	defer func(b int) { verifyUnreadBudget = b }(verifyUnreadBudget)
	verifyUnreadBudget = 10
	n := search.PoolSize(15) + 300
	dir := filepath.Join(t.TempDir(), "index.db")
	now := time.Now()
	ss := make([]model.Session, 0, n)
	for i := 0; i < n; i++ {
		at := now.Add(-time.Duration(i) * time.Minute)
		msg := model.Message{Role: "user", Text: fmt.Sprintf("the zzquokka rollout of run %d", i), Time: at}
		if i%2 == 1 {
			msg = model.Message{Role: roleCommand, Text: fmt.Sprintf("make zzquokka RUN=%d", i), Time: at}
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s-%05d", i), Project: "shop", Started: at, Updated: at,
			Messages: []model.Message{{Role: "user", Text: fmt.Sprintf("run %d", i), Time: at}, msg}})
	}
	writeTestStore(t, dir, ss...)
	res, err := SearchDetailed(dir, query.Options{Query: "zzquokka", Window: 15})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil || !res.Unread.Estimated {
		t.Fatalf("unread %+v, want an estimate", res.Unread)
	}
}

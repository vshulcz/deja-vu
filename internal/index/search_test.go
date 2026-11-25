package index

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// phraseStore holds apart sessions that say alpha and beta in one message
// but never together, and five that say "alpha beta".
func phraseStore(t *testing.T, apart int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "index.db")
	now := time.Now()
	ss := make([]model.Session, 0, apart+5)
	for i := range apart + 5 {
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
	for i := range n {
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
	for i := range n {
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

// The pool was drawn before the trust policy ran. Sessions it withholds
// took pool slots from ones the page could show, and the ones left unread
// were counted as matches, so the total named sessions no page would ever
// serve (#5488). Under Options.Activation the pool leaves them out and says
// how many.
func TestCommonWordPoolLeavesOutWhatThePolicyWithholds(t *testing.T) {
	tmp := t.TempDir()
	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"mcp":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	dir := filepath.Join(tmp, "index.db")
	now := time.Now()
	imported, local := search.PoolSize(15)+200, search.PoolSize(15)+300
	ss := make([]model.Session, 0, imported+local)
	// The imported sessions are newer and say the word more often, so a pool
	// drawn without the policy is made of them.
	for i := range imported + local {
		project, at, says := "shop", now.Add(-time.Duration(30+i)*time.Hour), 1
		if i < imported {
			project, at, says = "imported:peer/shop", now.Add(-time.Duration(i)*time.Minute), 4
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s-%05d", i), Project: project, Started: at, Updated: at,
			Messages: []model.Message{{Role: "user", Text: strings.Repeat("deploy ", says) + fmt.Sprintf("service %d", i), Time: at}}})
	}
	writeTestStore(t, dir, ss...)

	// The control: without the activation the pool is drawn as before, and
	// the imported sessions fill it.
	res, err := SearchDetailed(dir, query.Options{Query: "deploy", Window: 15})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil || countImported(res.Sessions) == 0 {
		t.Fatalf("control: the pool held no imported session (%d read)", len(res.Sessions))
	}

	res, err = SearchDetailed(dir, query.Options{Query: "deploy", Window: 15, Activation: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil {
		t.Fatalf("read all %d sessions", len(res.Sessions))
	}
	if n := countImported(res.Sessions); n > 0 {
		t.Errorf("the pool read %d sessions the policy withholds on mcp", n)
	}
	if got := len(res.Sessions) + res.Unread.Sessions; got != local {
		t.Errorf("read and unread add up to %d, want the %d the policy allows", got, local)
	}
	if res.Unread.Withheld != imported {
		t.Errorf("withheld %d, want %d", res.Unread.Withheld, imported)
	}
}

func countImported(ss []model.Session) int {
	n := 0
	for _, s := range ss {
		if strings.HasPrefix(s.Project, "imported:") {
			n++
		}
	}
	return n
}

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

// What a search does before it has found anything has to stay the size of
// the question, not the size of the store. On a 12k-session store a Cyrillic
// recall spent 2-3 s in the glued step and every recall spent 190 ms walking
// the stores (#5386).

// fillGluedByWalk is the glued step as it was: every token in the catalog
// against every form. The keyed lookup has to find exactly this.
func fillGluedByWalk(terms []string, matchesPer [][]string, catalog map[string]bool) {
	formsPer := make([][]string, len(terms))
	for i, term := range terms {
		if len(matchesPer[i]) > 0 || len([]rune(term)) < 5 || !isCyrToken(term) {
			continue
		}
		for _, f := range append(stemMatchForms(term), term) {
			if len([]rune(f)) >= 4 {
				formsPer[i] = append(formsPer[i], f)
			}
		}
	}
	found := make([]map[string]bool, len(terms))
	for tok := range catalog {
		for i, forms := range formsPer {
			for _, f := range forms {
				if len([]rune(tok)) <= len([]rune(f)) || (!strings.HasPrefix(tok, f) && !strings.HasSuffix(tok, f)) {
					continue
				}
				if found[i] == nil {
					found[i] = map[string]bool{}
				}
				found[i][tok] = true
				break
			}
		}
	}
	for i, set := range found {
		if len(set) == 0 {
			continue
		}
		out := make([]string, 0, len(set))
		for tok := range set {
			out = append(out, tok)
		}
		sort.Strings(out)
		if len(out) > 8 {
			out = out[:8]
		}
		matchesPer[i] = out
	}
}

// fillerTokens are n tokens the glued step must never return for the terms
// below: Cyrillic, but sharing no four-rune head or tail with any of their
// forms.
func fillerTokens(r *rand.Rand, n int) []string {
	letters := []rune("шщжцъэю")
	out := make([]string, n)
	for i := range out {
		k := 5 + r.Intn(8)
		b := make([]rune, k)
		for j := range b {
			b[j] = letters[r.Intn(len(letters))]
		}
		out[i] = string(b) + strconv.Itoa(i)
	}
	return out
}

var gluedTerms = []string{"настройки", "миграцию", "соединение", "кластеров", "retry", "токен"}

func gluedCatalog(r *rand.Rand, fillers int) map[string]bool {
	set := map[string]bool{}
	// Fillers from a stream of their own, so the glued tokens below are the
	// same whatever the number of fillers.
	for _, tok := range fillerTokens(rand.New(rand.NewSource(r.Int63())), fillers) { //nolint:gosec // a seeded fixture, not crypto
		set[tok] = true
	}
	glue := []string{"k8s", "v2", "x", "под", "пере", "_", "ё", "\xff", "-"}
	for _, term := range gluedTerms {
		forms := append(stemMatchForms(term), term)
		for range 12 {
			f := forms[r.Intn(len(forms))]
			g := glue[r.Intn(len(glue))]
			switch r.Intn(4) {
			case 0:
				set[g+f] = true
			case 1:
				set[f+g] = true
			case 2:
				set[f] = true // the form itself: not longer, never a match
			default:
				// One rune short of the form at either end: no match.
				rs := []rune(f)
				set[string(rs[1:])+g] = true
				set[g+string(rs[:len(rs)-1])] = true
			}
		}
	}
	return set
}

func TestGluedLookupFindsWhatTheWalkFound(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		r := rand.New(rand.NewSource(seed)) //nolint:gosec // a seeded fixture, not crypto
		set := gluedCatalog(r, 300)
		idx := newTokenIndex(set)
		terms := gluedTerms
		got := make([][]string, len(terms))
		want := make([][]string, len(terms))
		fillGluedMatches(terms, got, idx)
		fillGluedByWalk(terms, want, set)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: lookup %q, walk %q", seed, got, want)
		}
	}
	// Not vacuous: the catalog does hold glued forms.
	got := make([][]string, len(gluedTerms))
	fillGluedMatches(gluedTerms, got, newTokenIndex(gluedCatalog(rand.New(rand.NewSource(1)), 0))) //nolint:gosec // a seeded fixture, not crypto
	if len(got[0]) == 0 {
		t.Fatal("no glued form of настройки found; the comparison above proves nothing")
	}
}

func TestGluedStepDoesNotGrowWithTheCatalog(t *testing.T) {
	compared := func(fillers int) int64 {
		idx := newTokenIndex(gluedCatalog(rand.New(rand.NewSource(7)), fillers)) //nolint:gosec // a seeded fixture, not crypto
		idx.gluedKeys()                                                          // built once per catalog, then reused by every query
		before := gluedCompared.Load()
		fillGluedMatches(gluedTerms, make([][]string, len(gluedTerms)), idx)
		return gluedCompared.Load() - before
	}
	small, large := compared(1000), compared(20000)
	if small == 0 {
		t.Fatal("the glued step compared nothing; the test no longer reaches it")
	}
	if large != small {
		t.Errorf("the glued step compared %d tokens on a 1k-token catalog and %d on a 20k one, want the same", small, large)
	}
}

// The walk stats every transcript on the machine. What it does per file has
// to be the stat and nothing that costs more: it built the whole harness
// registry twice per file, which was most of 190 ms on 12k files.
func TestStoreWalkDoesNotRebuildTheRegistryPerFile(t *testing.T) {
	h := newTwoWayEnv(t)
	for i := range 400 {
		sid := fmt.Sprintf("w%03d", i)
		h.put("app", sid, hPrompt("app", sid, 1, 0, "walk me"))
	}
	old := currentFilesWith("", nil)
	if len(old) < 400 {
		t.Fatalf("walk found %d files, want 400", len(old))
	}
	perFile := testing.AllocsPerRun(3, func() { currentFilesWith("", old) }) / float64(len(old))
	// A stat and its FileState are a handful (up to ~40 on Windows); a registry is hundreds.
	if perFile > 45 {
		t.Errorf("the walk allocates %.0f times per file, want at most 45", perFile)
	}
}

func TestStatFilesSharedOutMatchesOneByOne(t *testing.T) {
	h := newTwoWayEnv(t)
	paths := map[string]bool{}
	for i := range 3 * statPerWorker {
		sid := fmt.Sprintf("p%04d", i)
		h.put("app", sid, hPrompt("app", sid, 1, 0, strings.Repeat("x", i%50)))
		paths[h.path("app", sid)] = true
	}
	paths[h.path("app", "gone")] = true // not there: left out either way
	dirPath := h.path("app", "adir")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	paths[dirPath] = true // a directory: left out either way
	want := map[string]FileState{}
	for p := range paths {
		if fi, err := os.Lstat(p); err == nil && !fi.IsDir() {
			want[p] = walkFileState(p, fi)
		}
	}
	got := statFiles(paths, nil)
	if len(got) != 3*statPerWorker || !reflect.DeepEqual(got, want) {
		t.Errorf("shared-out walk found %d files, one by one %d, and they differ: %v", len(got), len(want), !reflect.DeepEqual(got, want))
	}
}

// A search on an unchanged index decodes the manifest once, the way a new
// process has to, and a long-lived server's next recall not at all. The
// freshness check decoded its own copy every time, outside the cache the
// search then read from: twice per CLI search, once per recall (#5386).
func TestSearchDecodesTheManifestOnce(t *testing.T) {
	dir := recallReadsStore(t)
	o := query.Options{Query: "frobnicator handshake"}
	// Built for one harness; the first unscoped check rebuilds it for all.
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir) // as a new process finds it
	before := manifestDecodes.Load()
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, o); err != nil || len(ss) == 0 {
		t.Fatalf("search found %d sessions, err %v; want a hit", len(ss), err)
	}
	if n := manifestDecodes.Load() - before; n != 1 {
		t.Errorf("a search decoded the manifest %d times, want 1", n)
	}
	before = manifestDecodes.Load()
	if _, err := EnsureForSearchStale(dir, o, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(dir, o); err != nil {
		t.Fatal(err)
	}
	if n := manifestDecodes.Load() - before; n != 0 {
		t.Errorf("the next recall in the same process decoded the manifest %d times, want 0", n)
	}
}

// The freshness check reads the manifest through the cache now, so the cache
// has to see a damaged sessions.gob: that is the error that rebuilds the
// index, and answering from memory left the file broken on disk.
func TestDamagedSessionsFileStillRebuildsThroughTheCache(t *testing.T) {
	dir := recallReadsStore(t)
	o := query.Options{Query: "frobnicator handshake"}
	for range 2 {
		if err := EnsureForSearch(dir, o, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.bin"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(dir); err != nil {
		t.Errorf("sessions.bin is still damaged after a freshness check: %v", err)
	}
}

// A reader who waits seconds for an answer can say which part of the wait it
// was: taking the lock, walking the stores, or ingesting what changed. Without
// this the three are one number and a report can only guess (#3021).
func TestSearchTraceNamesTheStages(t *testing.T) {
	root := t.TempDir()
	setHome(t, root)
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	store := filepath.Join(root, "claude", "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","cwd":"/work/app",` +
		`"message":{"role":"user","content":"tracedneedle in the loader"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// One more turn, so the search has something to ingest inline.
	more := `{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","cwd":"/work/app",` +
		`"message":{"role":"assistant","content":"tracedneedle answered"}}` + "\n"
	f, err := os.OpenFile(filepath.Join(store, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(more); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEJA_TRACE", "1")
	got := captureStderr(t, func() {
		if _, err := EnsureForSearchStale(dir, query.Options{Query: "tracedneedle"}, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, stage := range []string{"walk stores", "append"} {
		if !strings.Contains(got, stage) {
			t.Errorf("the trace does not name %q:\n%s", stage, got)
		}
	}

	// And it is silent otherwise: this runs on every search.
	t.Setenv("DEJA_TRACE", "")
	quiet := captureStderr(t, func() {
		if _, err := EnsureForSearchStale(dir, query.Options{Query: "tracedneedle"}, nil); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(quiet, "trace ") {
		t.Errorf("the trace printed without being asked:\n%s", quiet)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}

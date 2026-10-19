package index

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

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
		out[i] = string(b) + fmt.Sprint(i)
	}
	return out
}

var gluedTerms = []string{"настройки", "миграцию", "соединение", "кластеров", "retry", "токен"}

func gluedCatalog(r *rand.Rand, fillers int) map[string]bool {
	set := map[string]bool{}
	// Fillers from a stream of their own, so the glued tokens below are the
	// same whatever the number of fillers.
	for _, tok := range fillerTokens(rand.New(rand.NewSource(r.Int63())), fillers) {
		set[tok] = true
	}
	glue := []string{"k8s", "v2", "x", "под", "пере", "_", "ё", "\xff", "-"}
	for _, term := range gluedTerms {
		forms := append(stemMatchForms(term), term)
		for k := 0; k < 12; k++ {
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
		r := rand.New(rand.NewSource(seed))
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
	fillGluedMatches(gluedTerms, got, newTokenIndex(gluedCatalog(rand.New(rand.NewSource(1)), 0)))
	if len(got[0]) == 0 {
		t.Fatal("no glued form of настройки found; the comparison above proves nothing")
	}
}

func TestGluedStepDoesNotGrowWithTheCatalog(t *testing.T) {
	compared := func(fillers int) int64 {
		idx := newTokenIndex(gluedCatalog(rand.New(rand.NewSource(7)), fillers))
		idx.gluedKeys() // built once per catalog, then reused by every query
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
	for i := 0; i < 400; i++ {
		sid := fmt.Sprintf("w%03d", i)
		h.put("app", sid, hPrompt("app", sid, 1, 0, "walk me"))
	}
	old := currentFilesWith("", nil)
	if len(old) < 400 {
		t.Fatalf("walk found %d files, want 400", len(old))
	}
	perFile := testing.AllocsPerRun(3, func() { currentFilesWith("", old) }) / float64(len(old))
	// A stat and its FileState are a handful; a registry is hundreds.
	if perFile > 30 {
		t.Errorf("the walk allocates %.0f times per file, want at most 30", perFile)
	}
}

func TestStatFilesSharedOutMatchesOneByOne(t *testing.T) {
	h := newTwoWayEnv(t)
	paths := map[string]bool{}
	for i := 0; i < 3*statPerWorker; i++ {
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
			want[p] = walkFileState(p, fi, nil)
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
	for i := 0; i < 2; i++ {
		if err := EnsureForSearch(dir, o, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.gob"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(dir); err != nil {
		t.Errorf("sessions.gob is still damaged after a freshness check: %v", err)
	}
}

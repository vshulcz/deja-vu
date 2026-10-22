package index

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A recall whose words never meet as typed walks the part-of-word step, then
// ranks, then hangs that ranking under a thin answer. Each step used to read
// what the one before it had already read (#5378).
func recallReadsStore(t *testing.T) string {
	t.Helper()
	h := newTwoWayEnv(t)
	// Past tailFloor, so a thin answer gets a ranked tail under it.
	for i, topic := range []string{"pool", "queue", "cache", "outbox", "router", "parser", "mailer", "billing", "search", "upload", "worker", "gateway"} {
		sid := fmt.Sprintf("r%d", i)
		h.put("app", sid, richSession("app", sid, 2+i%20, topic))
	}
	h.put("svc", "s5", hPrompt("svc", "s5", 6, 0, "the frobnicator handshake times out after the deploy")+
		hSay("svc", "s5", 6, 1, "The handshake timeout comes from the frobnicator retry loop."))
	// One session holding most of the log, the shape of a marathon.
	var long strings.Builder
	long.WriteString(hPrompt("svc", "s4", 5, 0, "the ledger marathon starts here"))
	for i := 1; i < 120; i++ {
		long.WriteString(hSay("svc", "s4", 5, i%60, fmt.Sprintf("ledger step %d of the marathon", i)))
	}
	h.put("svc", "s4", long.String())
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestOneRankingPerLadderPass(t *testing.T) {
	dir := recallReadsStore(t)
	before := relevanceRuns.Load()
	res, err := searchDetailedRead(dir, query.Options{Query: "frobnicat handshake times"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("the part-of-word step found nothing; the test no longer reaches the tail")
	}
	if n := relevanceRuns.Load() - before; n != 1 {
		t.Errorf("one pass of the ladder ranked the store %d times, want 1", n)
	}
}

func TestPartOfWordStepReadsTheCatalogsDirectories(t *testing.T) {
	dir := recallReadsStore(t)
	if _, err := tokenIndexCached(dir); err != nil {
		t.Fatal(err)
	}
	bare := []string{"zorble", "frobnic"}
	before := bucketDirReads.Load()
	posts, variants, err := intersectSubstringPostingsDetailed(dir, bare)
	if err != nil {
		t.Fatal(err)
	}
	if n := bucketDirReads.Load() - before; n != 0 {
		t.Errorf("the part-of-word step read %d bucket directories off disk with the catalog cached, want 0", n)
	}
	if len(posts) == 0 {
		t.Fatal("no postings; the comparison below would be vacuous")
	}
	// The same answer as reading every directory off disk.
	idx, _ := tokenIndexCached(dir)
	paths := make([]bucketDir, 0, len(idx.dirs))
	for _, d := range idx.dirs {
		paths = append(paths, bucketDir{path: d.path})
	}
	wantPosts, wantVariants, _, err := substringPostingsFrom(paths, bare, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(posts, wantPosts) || fmt.Sprint(variants) != fmt.Sprint(wantVariants) {
		t.Errorf("catalog read gives %v %v, disk read %v %v", posts, variants, wantPosts, wantVariants)
	}
}

func TestOneLongSessionLoadsThroughTheTable(t *testing.T) {
	dir := recallReadsStore(t)
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, r := range recs {
		if r.Record.Key == "claude:s4" {
			want = append(want, r.Record.Role+"|"+r.Record.Text)
		}
	}
	if int64(len(want))*offsetsWalkShare <= int64(len(recs)) {
		t.Fatalf("s4 holds %d of %d records, under the walk share; the test proves nothing", len(want), len(recs))
	}
	before := atomic.LoadInt64(&recordOffsetReads)
	s, ok, err := FindByIdentity(dir, "claude", "s4")
	if err != nil || !ok {
		t.Fatalf("load s4: %v %v", ok, err)
	}
	if atomic.LoadInt64(&recordOffsetReads) == before {
		t.Error("one long session walked the whole log instead of reading its own records")
	}
	got := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		got = append(got, m.Role+"|"+m.Text)
	}
	if !slices.Equal(got, want) {
		t.Errorf("table read gives %d records, the walk %d", len(got), len(want))
	}
}

// The close-spelling step stops comparing a word once it is past the limit;
// within it, the distance is the one the full table gives.
func TestDamerauRunesStopsOnlyPastTheLimit(t *testing.T) {
	full := func(a, b []rune) int {
		d := make([][]int, len(a)+1)
		for i := range d {
			d[i] = make([]int, len(b)+1)
			d[i][0] = i
		}
		for j := range d[0] {
			d[0][j] = j
		}
		for i := 1; i <= len(a); i++ {
			for j := 1; j <= len(b); j++ {
				cost := 1
				if a[i-1] == b[j-1] {
					cost = 0
				}
				d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
				if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
					d[i][j] = min(d[i][j], d[i-2][j-2]+1)
				}
			}
		}
		return d[len(a)][len(b)]
	}
	alphabet := []rune("абвгдеж")
	rng := rand.New(rand.NewSource(5378)) //nolint:gosec // a seeded fixture, not crypto
	word := func() []rune {
		w := make([]rune, 1+rng.Intn(12))
		for i := range w {
			w[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return w
	}
	for range 20000 {
		a, b := word(), word()
		for _, limit := range []int{1, 2} {
			want := full(a, b)
			got := damerauDistanceRunes(string(a), string(b), limit)
			if (want <= limit) != (got <= limit) || (want <= limit && got != want) {
				t.Fatalf("damerauDistanceRunes(%q, %q, %d) = %d, full table %d", string(a), string(b), limit, got, want)
			}
		}
	}
}

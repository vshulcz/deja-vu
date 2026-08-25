package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	query "github.com/vshulcz/deja-vu/internal/query"
)

// seedQuerySideCorpus writes sessions whose text is decomposed (NFD), the form
// a macOS path hands back, and returns the index dir.
func seedQuerySideCorpus(t *testing.T, sessions int) string {
	t.Helper()
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	const word = "cafe\u0301" // e + combining acute, written as escapes on purpose
	for s := 0; s < sessions; s++ {
		var lines []string
		for i := 0; i < 20; i++ {
			text := fmt.Sprintf("the %s parser retried %d times before the pool was exhausted", word, i)
			lines = append(lines, fmt.Sprintf(`{"type":"user","sessionId":"s%d","cwd":"/proj","timestamp":"2026-08-20T10:%02d:00Z","message":{"role":"user","content":%q}}`, s, i%60, text))
		}
		if err := os.WriteFile(filepath.Join(claude, fmt.Sprintf("s%d.jsonl", s)), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The per-record check ran the query side — parts, composed form, CJK-folded
// form — for every record it looked at, so a decomposed query, which takes the
// retry path on every record, allocated more than twice what the same search
// allocated with an ASCII query (49385 against 22984 over 120 sessions). The
// query does not change between records (#1885).
//
// Measured as a ratio against the plain query on the same corpus, so the
// assertion does not depend on the machine or on how much the reader's Go
// version allocates elsewhere. The threshold is 1.3 because the two searches
// allocate the same amount once the query side is shared — the ratio is 1.0
// here and 2.14 on the branch before this — and because -race and a low GOGC
// move both sides together (checked).
func TestTheQuerySideIsNotRebuiltForEveryRecord(t *testing.T) {
	dir := seedQuerySideCorpus(t, 40)
	allocs := func(q string) float64 {
		return testing.AllocsPerRun(5, func() {
			if _, err := Search(dir, query.Options{Query: q, All: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
	plain := allocs("pool exhausted")
	decomposed := allocs("cafe\u0301 parser")
	if plain == 0 {
		t.Fatal("the plain search allocated nothing, so the ratio below says nothing")
	}
	if ratio := decomposed / plain; ratio > 1.3 {
		t.Errorf("a decomposed query allocates %.2fx the plain one (%v against %v): the query side is being rebuilt per record",
			ratio, decomposed, plain)
	}
}

// The retry paths still do what they are for: a query typed in one normal form
// finds text stored in the other.
func TestADecomposedQueryStillFindsComposedText(t *testing.T) {
	dir := seedQuerySideCorpus(t, 2)
	for _, q := range []string{"cafe\u0301 parser", "caf\u00e9 parser"} {
		got, err := Search(dir, query.Options{Query: q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Errorf("%+q found nothing", q)
		}
	}
}

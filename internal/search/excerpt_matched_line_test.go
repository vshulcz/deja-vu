package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The line a search matched is the one line an excerpt must not drop as a
// dump. A Go panic in tool output matched on its `panic:` line, and the
// excerpt showed the stack frames around it (#5456).
func TestExcerptKeepsTheMatchedDumpLine(t *testing.T) {
	goPanic := strings.Join([]string{
		"=== RUN   TestQuokkaRetry",
		"--- FAIL: TestQuokkaRetry (0.00s)",
		"panic: assignment to entry in nil map [recovered]",
		"goroutine 7 [running]:",
		"testing.tRunner.func1.2({0x1029c4e20, 0x102a0b8d0})",
		"\t/usr/local/go/src/testing/testing.go:1632 +0x1bc",
		"FAIL\tquokkabloom/retry\t0.214s",
	}, "\n")
	npm := strings.Join([]string{
		"> marmotgrid@1.4.0 preinstall",
		"> node scripts/check.js",
		"npm ERR! code ERESOLVE",
		"npm ERR! ERESOLVE unable to resolve dependency tree",
		"npm ERR! Found: react@18.2.0",
	}, "\n")
	sess := func(id, text string) model.Session {
		return model.Session{Harness: "claude", Project: "p", ID: id,
			Messages: []model.Message{{Role: "tool", Text: text}}}
	}
	for _, c := range []struct {
		name, text, q string
		regex         bool
		want, notWant string
	}{
		{"plain", goPanic, "assignment to entry in nil map", false, "panic: assignment to entry in nil map", "goroutine 7"},
		{"pasted line", goPanic, "panic: assignment to entry in nil map", false, "panic: assignment to entry in nil map", "goroutine 7"},
		{"regex", goPanic, "^panic: assignment", true, "panic: assignment to entry in nil map", "goroutine 7"},
		{"npm", npm, "unable to resolve dependency tree", false, "ERESOLVE unable to resolve dependency tree", "Found: react"},
		// Control: searching the noise shows the noise, and nothing else of it.
		{"goroutine", goPanic, "goroutine running", false, "goroutine 7 [running]", "panic: assignment"},
	} {
		hits, err := Run([]model.Session{sess("s-"+c.name, c.text)}, Options{Query: c.q, Regex: c.regex})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 {
			t.Errorf("%s: %d hits, want 1", c.name, len(hits))
			continue
		}
		got := strings.Join(hits[0].Snippets, " ")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: excerpt %q lost the matched line %q", c.name, got, c.want)
		}
		if strings.Contains(got, c.notWant) {
			t.Errorf("%s: excerpt %q kept the unmatched dump line %q", c.name, got, c.notWant)
		}
	}

	// The error tier has no words to centre on, only the paste: the pasted
	// line is kept, and the excerpt starts there even past the window.
	long := strings.Repeat("ok  quokkabloom/other/pkg  0.1s coverage fine\n", 12) + goPanic
	hits := ErrorHits([]model.Session{sess("s-err", long)}, "panic: assignment to entry in nil map [recovered]")
	if len(hits) != 1 || !strings.Contains(strings.Join(hits[0].Snippets, " "), "panic: assignment to entry in nil map") {
		t.Errorf("error tier: excerpt %q lost the pasted line", hits[0].Snippets)
	}
	if hits := ErrorHits([]model.Session{sess("s-err0", goPanic)}, ""); strings.Contains(strings.Join(hits[0].Snippets, " "), "panic:") {
		t.Errorf("error tier with no paste kept a dump line: %q", hits[0].Snippets)
	}
}

package index

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestPostingRoundTripCarriesTheToolBit(t *testing.T) {
	in := []posting{
		{Off: 10, Sid: 1},
		{Off: 4096, Sid: 1, Tool: true},
		{Off: 1 << 20, Sid: 70000, Tool: true},
		{Off: 1<<20 + 3, Sid: 70000},
	}
	got := decodePostings(encodePostings(in))
	if len(got) != len(in) {
		t.Fatalf("got %d postings, want %d", len(got), len(in))
	}
	for i, p := range got {
		if p != in[i] {
			t.Fatalf("posting %d = %+v, want %+v", i, p, in[i])
		}
	}
}

func TestCapKeepsSpeechBeforeToolRecords(t *testing.T) {
	// One session, four sentences and ninety-six command lines. Reading only
	// half the session's postings must not spend the whole budget on commands.
	var posts []posting
	for i := 0; i < 4; i++ {
		posts = append(posts, posting{Off: int64(i), Sid: 7})
	}
	for i := 0; i < 96; i++ {
		posts = append(posts, posting{Off: int64(100 + i), Sid: 7, Tool: true})
	}
	got := capPostingsPerSession(posts, 50)
	speech, tool := 0, 0
	for _, p := range got {
		if p.Tool {
			tool++
		} else {
			speech++
		}
	}
	if speech != 4 {
		t.Fatalf("kept %d of 4 speech postings", speech)
	}
	if want := 46; tool != want {
		t.Fatalf("kept %d tool postings, want %d", tool, want)
	}
}

func TestCapSpendsEverythingOnSpeechWhenThereIsEnoughOfIt(t *testing.T) {
	var posts []posting
	for i := 0; i < 80; i++ {
		posts = append(posts, posting{Off: int64(i), Sid: 3})
	}
	for i := 0; i < 80; i++ {
		posts = append(posts, posting{Off: int64(1000 + i), Sid: 3, Tool: true})
	}
	got := capPostingsPerSession(posts, 20)
	for _, p := range got {
		if p.Tool {
			t.Fatal("speech alone fills the budget, no tool posting should survive")
		}
	}
	if len(got) != 20 {
		t.Fatalf("kept %d postings, want 20", len(got))
	}
}

func TestCapLeavesSmallSessionsAlone(t *testing.T) {
	posts := []posting{
		{Off: 1, Sid: 2, Tool: true},
		{Off: 2, Sid: 2},
	}
	if got := capPostingsPerSession(posts, 64); len(got) != 2 {
		t.Fatalf("a session under the bound keeps every posting, got %d", len(got))
	}
}

func TestIsToolRole(t *testing.T) {
	for _, r := range []string{roleFiles, roleCommand, roleToolOutput} {
		if !isToolRole(r) {
			t.Errorf("%q should be a tool role", r)
		}
	}
	for _, r := range []string{"user", "assistant", "developer"} {
		if isToolRole(r) {
			t.Errorf("%q is speech", r)
		}
	}
}

func TestTokenizedPartIndexesOnlyThePathOfASpan(t *testing.T) {
	span := "/w/pkg/retry.go\nfunc retry() error {\n\treturn nil\n}"
	if got := tokenizedPart(roleEdit, span); got != "/w/pkg/retry.go" {
		t.Fatalf("got %q, want the path alone", got)
	}
	if got := tokenizedPart("user", span); got != span {
		t.Fatal("speech is indexed whole")
	}
	// A span with no body still has to yield its path rather than nothing.
	if got := tokenizedPart(roleEdit, "/w/only.go"); got != "/w/only.go" {
		t.Fatalf("got %q", got)
	}
}

func TestTopTouchedFilesRanksAndFiltersAgentFiles(t *testing.T) {
	ms := []model.Message{
		{Role: roleFiles, Text: "/w/app.go\n/w/app.go"},
		{Role: roleFiles, Text: "/w/app.go\n/w/util.go\n/Users/x/.claude/notes.md\n/w/build.log"},
		{Role: "user", Text: "/w/never.go"},
	}
	got := topTouchedFiles(ms)
	if len(got) != 2 || got[0] != "/w/app.go" || got[1] != "/w/util.go" {
		t.Fatalf("got %v, want the busiest repository files only", got)
	}
	if topTouchedFiles(nil) != nil {
		t.Fatal("no file records, nothing stored")
	}
}

func TestTopTouchedFilesIsCapped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "/w/f%02d.go\n", i)
	}
	if got := topTouchedFiles([]model.Message{{Role: roleFiles, Text: b.String()}}); len(got) != touchedFileCap {
		t.Fatalf("stored %d paths, want the cap of %d", len(got), touchedFileCap)
	}
}

// Tool output is indexed whole below a threshold and filtered above it. A short
// output is usually the answer to something; a 50 KB build log is progress
// nobody searches, and indexing all of it took the commonest word on a
// 1150-session store from 22 ms to 57 ms.
func TestSignalLinesKeepsShortOutputWhole(t *testing.T) {
	short := "--- FAIL: TestRetry (0.01s)\n    retry_test.go:42: got 3 want 4\nFAIL\tpkg\t0.1s"
	if got := tokenizedPart(roleToolOutput, short); got != short {
		t.Fatalf("short output must be indexed whole:\n%s", got)
	}
}

func TestSignalLinesFiltersLongLogs(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "downloading github.com/example/module/v%d\n", i)
	}
	b.WriteString("--- FAIL: TestRetry (0.01s)\n")
	b.WriteString("    retry_test.go:42: got 3 want 4\n")
	got := tokenizedPart(roleToolOutput, b.String())
	if len(got) >= b.Len() {
		t.Fatalf("a long log should shrink: %d of %d bytes", len(got), b.Len())
	}
	// The verdict and the line explaining it both survive.
	if !strings.Contains(got, "--- FAIL: TestRetry") {
		t.Error("the verdict must be indexed")
	}
	if !strings.Contains(got, "retry_test.go:42") {
		t.Error("the line after a verdict is what it was about")
	}
	if strings.Contains(got, "downloading github.com/example/module/v200") {
		t.Error("progress lines are what the filter exists to drop")
	}
}

func TestSignalLinesFallsBackWhenNothingMatches(t *testing.T) {
	// A long output with no error-shaped line still has to be findable.
	quiet := strings.Repeat("all quiet on the build front\n", 500)
	got := tokenizedPart(roleToolOutput, quiet)
	if got == "" {
		t.Fatal("indexing nothing makes the record unreachable")
	}
	// Head plus tail since #614: the head alone is the least informative part
	// of exactly the records that matched nothing.
	if len(got) > signalFloor+signalTail+64 {
		t.Fatalf("the fallback should be bounded, got %d bytes", len(got))
	}
}

func TestSignalLinesLeavesOtherRolesAlone(t *testing.T) {
	long := strings.Repeat("a sentence someone actually typed. ", 500)
	if got := tokenizedPart("assistant", long); got != long {
		t.Fatal("speech is indexed whole however long it is")
	}
}

// An output that matches none of the signal substrings used to be indexed by
// its head alone, and the head is the least informative part of exactly those
// records. Measured on a 1153-session store: 394 outputs match no signal line,
// 262 are long enough to have a tail, and 186 of those carry a token the head
// does not — text no query could reach (#614).
func TestSignalLinesKeepsBothEndsWhenNothingMatches(t *testing.T) {
	var b strings.Builder
	b.WriteString("HEADTOKEN starts here\n")
	for b.Len() < signalFloor*2 {
		b.WriteString("ordinary chatter about nothing in particular\n")
	}
	b.WriteString("TAILTOKEN the vendor said the model name is not valid\n")
	got := signalLines(b.String())

	if !strings.Contains(got, "HEADTOKEN") {
		t.Error("dropped the head")
	}
	if !strings.Contains(got, "TAILTOKEN") {
		t.Error("dropped the tail, so the end of a long output stays unreachable")
	}
	// Both ends, not the whole thing: the byte saving is the reason the filter
	// exists.
	if len(got) >= b.Len() {
		t.Fatalf("kept %d of %d bytes — the filter stopped filtering", len(got), b.Len())
	}
	if len(got) > signalFloor+signalTail+64 {
		t.Fatalf("kept %d bytes, budget is %d", len(got), signalFloor+signalTail)
	}
}

// Short enough to fit in head plus tail: keep it whole rather than splicing
// two overlapping halves together.
func TestSignalLinesKeepsAShortUnmatchedOutputWhole(t *testing.T) {
	var b strings.Builder
	b.WriteString("nothing here matches a signal substring\n")
	for b.Len() < signalFloor+100 {
		b.WriteString("more ordinary chatter\n")
	}
	in := b.String()
	if got := signalLines(in); got != in {
		t.Fatalf("spliced a %d-byte output that fits in %d", len(in), signalFloor+signalTail)
	}
}

// A matched output is unchanged by any of this.
func TestSignalLinesUnchangedWhenSomethingMatches(t *testing.T) {
	var b strings.Builder
	b.WriteString("--- FAIL: TestThing\n    thing_test.go:12: wrong\n")
	for b.Len() < signalFloor*2 {
		b.WriteString("filler line\n")
	}
	got := signalLines(b.String())
	if !strings.Contains(got, "--- FAIL: TestThing") || !strings.Contains(got, "thing_test.go:12") {
		t.Fatalf("lost the verdict: %q", got)
	}
	if strings.Contains(got, "filler line") {
		t.Error("kept the filler around a matched line")
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
)

// seedCJKTitleIndex is seedLongTitleIndex with Chinese titles: same shape, same
// shared file, a title long enough to be trimmed on any bar.
func seedCJKTitleIndex(t *testing.T, titles []string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-cjk")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i, title := range titles {
		sid := fmt.Sprintf("cj%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/cj","timestamp":"2026-07-1` +
			strconv.Itoa(i) + `T10:00:00Z","message":{"role":"user","content":"` + title + `"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w/cj","timestamp":"2026-07-1` +
			strconv.Itoa(i) + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/cj/retry.go"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

var cjkTitles = []string{
	"重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开",
	"重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开二",
	"重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开三",
}

// #1076 bounded the bar in runes, which is the same thing as columns only for
// Latin text. A Chinese title is one rune and two columns per character, so
// the 38-rune cap fit an 80-column bar on paper and printed 98 columns — the
// memory segment ran off the edge exactly as it did before that fix.
func TestStatuslineFitsTheBarWithCJKTitle(t *testing.T) {
	dir := seedCJKTitleIndex(t, cjkTitles)
	// 82 is where the usage numbers start to fit by rune count and not by
	// column count: the segment before them is 58 runes and 77 columns.
	for _, width := range []int{60, 80, 82, 120} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		line := statuslineWith(t, dir, "/anywhere/cj01.jsonl")
		if got := termwidth.Columns(line); got > width {
			t.Errorf("%d-column bar: line prints %d columns: %q", width, got, line)
		}
		// The segment has to arrive whole; a title the terminal cuts loses its
		// closing quote, which is the visible half of #1076.
		if !strings.Contains(line, "”") {
			t.Errorf("%d-column bar: the memory segment lost its closing quote: %q", width, line)
		}
	}
}

// The bar still grows with the terminal — a fixed conservative cap would pass
// the test above and show the same stub on every width.
func TestStatuslineCJKTitleGrowsWithTheBar(t *testing.T) {
	dir := seedCJKTitleIndex(t, cjkTitles)

	t.Setenv("COLUMNS", "60")
	narrow := statuslineWith(t, dir, "/anywhere/cj01.jsonl")
	t.Setenv("COLUMNS", "120")
	wide := statuslineWith(t, dir, "/anywhere/cj01.jsonl")

	if termwidth.Columns(wide) <= termwidth.Columns(narrow) {
		t.Errorf("the line did not grow with the bar: narrow=%d wide=%d",
			termwidth.Columns(narrow), termwidth.Columns(wide))
	}
	// And the trim is real on the narrow bar: the whole title is 28 characters,
	// so a 60-column bar cannot be showing all of it.
	if strings.Contains(narrow, "错开") {
		t.Errorf("60-column bar shows the untrimmed title: %q", narrow)
	}
}

// safeForStatusline is what bounds the title, and its bound is what changed.
func TestSafeForStatuslineBoundsColumns(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		max  int
		want int
	}{
		{"latin under the cap", "retry queue stalls", 38, 18},
		// One short of the cap plus the ellipsis: the cut landed on a space,
		// which is trimmed before the mark is added.
		{"latin over the cap", "the retry queue stalls on staging and the workers wake together", 38, 38},
		{"chinese over the cap", "重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开", 38, 39},
		{"chinese exactly at the cap", "重试队列在预发环境卡住了工作进程同", 34, 34},
		{"kana over the cap", "リトライキューがステージングで詰まりワーカーが同時に起きる", 20, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := termwidth.Columns(safeForStatusline(tc.in, tc.max))
			if got != tc.want {
				t.Errorf("safeForStatusline(%q, %d) prints %d columns, want %d (%q)",
					tc.in, tc.max, got, tc.want, safeForStatusline(tc.in, tc.max))
			}
		})
	}
}

// `statuslineMinTitle`'s comment says what happens below the floor — "the count
// form says more than three words and an ellipsis" — and nothing reached it:
// memorySegment never passed a budget that low, so a bar too narrow for the
// floored title ran off the edge instead of using the shorter sentence (#1903).
func TestANarrowBarTakesTheCountFormWhenItFits(t *testing.T) {
	when := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	m := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: "retry loop in the parser", Last: when}

	floored := "deja · " + statuslineMemoryLineWithin(m, statuslineMinTitle, statuslineMinName)
	count := "deja · " + statuslineMemoryLineWithin(m, 0, statuslineMinName)
	if barColumns(count) >= barColumns(floored) {
		t.Fatalf("this memory does not shorten in the count form (%d against %d), so the test says nothing",
			barColumns(count), barColumns(floored))
	}

	// A bar between the two: the count form fits, the title form does not.
	width := barColumns(count)
	got := memorySegment(m, width)
	if barColumns(got) > width {
		t.Errorf("%d columns on a %d-column bar: %q", barColumns(got), width, got)
	}
	if strings.Contains(got, "“") {
		t.Errorf("the title form was kept though it does not fit: %q", got)
	}
	if !strings.Contains(got, "3 earlier sessions") {
		t.Errorf("the count form is not what was printed: %q", got)
	}
}

// Where the title fits, it stays: a count is a statistic, the title is the
// memory.
func TestAWideBarKeepsTheTitle(t *testing.T) {
	m := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: "retry loop in the parser",
		Last: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)}
	got := memorySegment(m, 80)
	if !strings.Contains(got, "retry loop") {
		t.Errorf("a bar with room lost the title: %q", got)
	}
}

// And where neither form fits, the title stays rather than being traded for a
// sentence that does not fit either — the decision
// TestStatuslineNarrowBarKeepsAReadableTitle pins.
func TestWhenNothingFitsTheTitleSurvives(t *testing.T) {
	m := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: "retry loop in the parser",
		Last: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)}
	got := memorySegment(m, 20)
	if !strings.Contains(got, "“") {
		t.Errorf("a bar too narrow for either form dropped the title: %q", got)
	}
}

// The memory half is the one withFileMemory says has to survive, so it is
// fitted before any of the usage numbers are appended. Only the title was
// elastic: the filename was fixed at 28 columns, so a long path put the segment
// past the bar however far the title had been cut, and a bar has no horizontal
// scroll (#1880, the shape of #1076).
func TestTheMemorySegmentFitsWhereTheFloorsAllowIt(t *testing.T) {
	when := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	long := fileMemory{Path: "/p/" + strings.Repeat("n", 60) + ".go", Sessions: 3, Title: "retry", Last: when}
	wide := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: strings.Repeat("計", 40), Last: when}
	for _, c := range []struct {
		name  string
		mem   fileMemory
		width int
	}{
		{"long name, short title", long, 60},
		{"long name, long title", fileMemory{Path: long.Path, Sessions: 3, Title: strings.Repeat("word ", 12), Last: when}, 60},
		{"wide script title", wide, 60},
	} {
		if got := memorySegment(c.mem, c.width); barColumns(got) > c.width {
			t.Errorf("%s: %d columns on a %d-column bar: %q", c.name, barColumns(got), c.width, got)
		}
	}
}

// Below both floors the segment is still over-width, and that is the older
// decision: a title cut to a stub in quotation marks says less than the count
// form, which is what TestStatuslineNarrowBarKeepsAReadableTitle pins. What
// this asserts is that the filename gives way as far as it may, so the segment
// is no wider than the floors require.
func TestOnATooNarrowBarTheFilenameStillGivesWay(t *testing.T) {
	m := fileMemory{
		Path:     "/p/" + strings.Repeat("n", 60) + ".go",
		Sessions: 3,
		Title:    "retry",
		Last:     time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC),
	}
	got := memorySegment(m, 40)
	fixedName := "deja · " + statuslineMemoryLineWithin(m, statuslineMinTitle, statuslineMaxName)
	if barColumns(got) >= barColumns(fixedName) {
		t.Errorf("the filename did not give way: %q is not narrower than %q", got, fixedName)
	}
	// The title floor still holds, so the segment says what the session was
	// about rather than showing a stub.
	open, shut := strings.Index(got, "“"), strings.Index(got, "”")
	if open < 0 || shut < open {
		t.Errorf("the quoted title is gone: %q", got)
	}
}

// An ordinary bar is unchanged: the fitting only takes away when it has to.
func TestAnOrdinaryMemorySegmentIsNotTrimmed(t *testing.T) {
	m := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: "retry loop", Last: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)}
	got := memorySegment(m, 80)
	if !strings.Contains(got, "parser.go") || !strings.Contains(got, "retry loop") {
		t.Errorf("a segment that fits was trimmed anyway: %q", got)
	}
	if strings.Contains(got, "…") {
		t.Errorf("a segment that fits carries an ellipsis: %q", got)
	}
}

// The budgets are columns of allowance, not columns of line: a title shorter
// than its budget gives nothing back when the budget is cut. Subtracting the
// overflow in one step therefore left bars over-width while both parts were
// still above their floors — 57 of 2496 measured. This walks the grid and
// allows an over-width segment only where even both floors would not fit.
func TestNoBarIsLeftOverWidthWhileThePartsCanStillGiveWay(t *testing.T) {
	when := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	titles := []string{"retry", strings.Repeat("word ", 12), strings.Repeat("計", 30)}
	for width := 20; width <= 120; width += 4 {
		for nameLen := 5; nameLen <= 80; nameLen += 5 {
			for _, title := range titles {
				m := fileMemory{Path: "/p/" + strings.Repeat("n", nameLen) + ".go", Sessions: 3, Title: title, Last: when}
				got := memorySegment(m, width)
				if barColumns(got) <= width {
					continue
				}
				atFloor := "deja · " + statuslineMemoryLineWithin(m, statuslineMinTitle, statuslineMinName)
				if barColumns(atFloor) <= width {
					t.Fatalf("width=%d name=%d: %d columns, though the floors fit in %d: %q",
						width, nameLen, barColumns(got), barColumns(atFloor), got)
				}
			}
		}
	}
}

// injectedBytes is the size of what the hook actually put in front of the
// model. In plain mode that is the framed block on stdout; in the JSON shape it
// is the additionalContext the reply carries.
func injectedBytes(t *testing.T, hookOutput string) int {
	t.Helper()
	text := strings.TrimSpace(hookOutput)
	if strings.HasPrefix(text, "{") {
		var reply struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(text), &reply); err != nil {
			t.Fatalf("hook reply is not the shape the statusline counts: %v\n%s", err, hookOutput)
		}
		text = reply.HookSpecificOutput.AdditionalContext
	}
	if text == "" {
		t.Fatal("the hook produced nothing, so there is nothing to count")
	}
	return len(text)
}

// A machine whose project has no history of its own still gets the environment
// block at every session start, and on such a day that block is the whole of
// deja's work. The statusline said "0 B injected" through it — the untrue line
// its own comment cites #1403 for — because `Empty` means no session went in,
// not that nothing was served (#1962).
func TestTheStatuslineCountsTheBlockItInjected(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions)
	withHookStdin(t, `{"source":"startup","session_id":"ses_env"}`)
	out := captureStdout(t, func() {
		if err := runHookContext(dir, true); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "separate sessions") {
		t.Fatalf("no environment block went out, so there is nothing to count:\n%q", out)
	}

	var line strings.Builder
	if err := runStatusline(dir, strings.NewReader(""), &line); err != nil {
		t.Fatal(err)
	}
	// Asserted positively: runStatusline returns early on a warmup, a policy
	// rule or a quiet week, and every one of those lines would satisfy "does
	// not say 0 B injected" while saying nothing about the block at all.
	// The size is measured from what went out, not written down here: a byte
	// count in the source pins the block's wording, so changing a sentence in
	// it failed this test for a reason that has nothing to do with counting.
	got := strings.TrimSpace(line.String())
	want := fmt.Sprintf("deja · no recalls yet today · %d B injected", injectedBytes(t, out))
	if got != want {
		t.Errorf("statusline = %q,\n                 want %q", got, want)
	}
}

// The week that contains today has to contain today's bytes. `injected` there
// counts hook deliveries deja pushed unprompted, which is what this was.
func TestTheWeekCountsAnInjectionWithNoProjectSession(t *testing.T) {
	dir := t.TempDir()
	usage.RecordResultRaw(dir, usage.KindHook, 501, 0, true, 0)

	_, _, injected, injectedBytes := usage.Week(dir)
	if injected != 1 || injectedBytes != 501 {
		t.Errorf("week counted %d injections and %d bytes, want 1 and 501", injected, injectedBytes)
	}
	_, _, today := usage.TodayDemand(dir)
	if today != 501 {
		t.Errorf("today counted %d injected bytes, want 501", today)
	}
}

// And a lookup that found nothing stays out of both, which is the flag's other
// half: an empty recall serves the sentence saying so, and counting its bytes
// as memory re-used would be the mirror of the bug above.
func TestARecallThatMatchedNothingStaysOutOfTheDemandCounts(t *testing.T) {
	dir := t.TempDir()
	usage.RecordResultRaw(dir, usage.KindRecall, 211, 0, true, 0)

	if recalls, bytes, _, _ := usage.Week(dir); recalls != 0 || bytes != 0 {
		t.Errorf("week counted an empty recall: %d recalls, %d bytes", recalls, bytes)
	}
	if recalls, bytes, _ := usage.TodayDemand(dir); recalls != 0 || bytes != 0 {
		t.Errorf("today counted an empty recall: %d recalls, %d bytes", recalls, bytes)
	}
}

// seedTouchedIndex builds a store where several sessions work on the same
// file, so the manifest carries a Touched entry they share.
func seedTouchedIndex(t *testing.T, sessions int, sharedFile string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-t")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i := range sessions {
		sid := fmt.Sprintf("t%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/t","timestamp":"2026-07-1` +
			strconv.Itoa(i%10) + `T10:00:00Z","message":{"role":"user","content":"pool sizing, round ` + strconv.Itoa(i) + `"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w/t","timestamp":"2026-07-1` +
			strconv.Itoa(i%10) + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"` + sharedFile + `"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStatuslineMemoryNamesTheFileAndTheEarlierSession(t *testing.T) {
	dir := seedTouchedIndex(t, 3, "/w/t/pool.go")
	// The current session is one of them; the other two are its memory.
	in := transcriptSource{TranscriptPath: "/anywhere/t02.jsonl"}
	m, ok := statuslineMemory(dir, in)
	if !ok {
		t.Fatal("two earlier sessions touched the same file and nothing was reported")
	}
	if m.Path != "/w/t/pool.go" {
		t.Fatalf("path = %q", m.Path)
	}
	if m.Sessions != 2 {
		t.Fatalf("sessions = %d, want the two that are not the current one", m.Sessions)
	}
	line := statuslineMemoryLine(m)
	if !strings.Contains(line, "pool.go") {
		t.Fatalf("line does not name the file: %q", line)
	}
	// #581: name a decision, not a count, when one is available — and it must
	// be the *newest* earlier session's. Every seeded session says something
	// slightly different so that reversing the sort is visible here; with
	// identical texts this assertion could not tell them apart.
	if !strings.Contains(line, "pool sizing") {
		t.Fatalf("line reports a statistic where a memory was available: %q", line)
	}
	if !strings.Contains(line, "round 1") {
		t.Fatalf("line names the wrong earlier session (want the newest, round 1): %q", line)
	}
	if !m.Last.Equal(newestOtherUpdated(t, dir, "t02")) {
		t.Fatalf("Last is not the newest earlier session: %v", m.Last)
	}
}

// The status line names an earlier session by title, so a trust rule that
// withholds a peer's content must hold here too — it was the one surface an
// imported session's title showed through while search, blame and restore all
// refused (#1026).
func TestStatuslineMemoryHonoursTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	cfg := filepath.Join(tmp, "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	root := filepath.Join(tmp, "claude", "proj-w")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// The current, local session edits /w/pkg/cache.go.
	cur := filepath.Join(root, "cur.jsonl")
	body := `{"type":"user","sessionId":"cur","cwd":"/w","timestamp":"2026-05-02T10:00:00Z","message":{"role":"user","content":"cache work"}}` + "\n" +
		`{"type":"assistant","sessionId":"cur","cwd":"/w","timestamp":"2026-05-02T10:00:05Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/pkg/cache.go","old_string":"x"}}]}}` + "\n"
	if err := os.WriteFile(cur, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// A peer's session touched the same absolute path, arriving by sync.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := `{"harness":"claude","session_id":"peer","project":"svc","role":"user","text":"IMPORTEDFILE a peer's cache fix"}` + "\n" +
		`{"harness":"claude","session_id":"peer","project":"svc","role":"files","text":"/w/pkg/cache.go"}` + "\n"
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), []byte(peer), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	in := transcriptSource{TranscriptPath: cur}

	// Allowed: the peer's session is the earlier one the bar names.
	writePolicy(t, `{"activations":{"search":{"imported":true}}}`)
	if m, ok := statuslineMemory(dir, in); !ok || !strings.Contains(m.Title, "IMPORTEDFILE") {
		t.Fatalf("imported file-memory did not surface under an allowing rule: ok=%v title=%q", ok, m.Title)
	}
	// Denied: no other session is allowed, so the bar stays silent rather than
	// naming the peer.
	writePolicy(t, `{"activations":{"search":{"imported":false}}}`)
	if m, ok := statuslineMemory(dir, in); ok {
		t.Errorf("status line leaked an imported session's title under deny-imported: %q", m.Title)
	}
}

// newestOtherUpdated is the newest Updated among sessions that are not id.
func newestOtherUpdated(t *testing.T, dir, id string) time.Time {
	t.Helper()
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out time.Time
	for _, m := range metas {
		if m.ID != id && m.Updated.After(out) {
			out = m.Updated
		}
	}
	return out
}

// Silence is the requirement, not a zero: a file no one else has worked on
// has no memory to report, and "0 sessions" would be permanent noise.
func TestStatuslineMemorySilentWhenAlone(t *testing.T) {
	dir := seedTouchedIndex(t, 1, "/w/t/pool.go")
	if _, ok := statuslineMemory(dir, transcriptSource{TranscriptPath: "/anywhere/t00.jsonl"}); ok {
		t.Fatal("the only session that touched the file should report nothing")
	}
}

func TestStatuslineMemorySilentWithoutATranscriptPath(t *testing.T) {
	dir := seedTouchedIndex(t, 3, "/w/t/pool.go")
	for _, tp := range []string{"", "   ", "/"} {
		if _, ok := statuslineMemory(dir, transcriptSource{TranscriptPath: tp}); ok {
			t.Fatalf("transcript_path %q should report nothing", tp)
		}
	}
	// A session deja has never indexed: the id is real-looking but unknown.
	if _, ok := statuslineMemory(dir, transcriptSource{TranscriptPath: "/x/never-seen.jsonl"}); ok {
		t.Fatal("an unknown session should report nothing")
	}
}

func TestStatuslineMemorySilentWithoutAnIndex(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "no-index")
	if _, ok := statuslineMemory(dir, transcriptSource{TranscriptPath: "/x/t00.jsonl"}); ok {
		t.Fatal("no index, nothing to say")
	}
}

// The payload is written by a host deja does not control, so every shape it
// could arrive in has to cost nothing rather than break the line.
func TestReadStatuslineInputSurvivesAnything(t *testing.T) {
	for _, body := range []string{
		"", "   ", "not json at all", "{", "null", "[]", `{"transcript_path":123}`,
		`{"transcript_path":null,"workspace":null}`,
		`{"transcript_path":"/x/a.jsonl"}`,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("payload %q panicked: %v", body, r)
				}
			}()
			readStatuslineInput(strings.NewReader(body))
		}()
	}
	got := readStatuslineInput(strings.NewReader(`{"transcript_path":"/x/a.jsonl","workspace":{"current_dir":"/w"}}`))
	if got.TranscriptPath != "/x/a.jsonl" {
		t.Fatalf("parsed = %+v", got)
	}
}

// A status line runs on every assistant message. Reading a payload bigger than
// the limit must not hang or blow memory.
func TestReadStatuslineInputIsBounded(t *testing.T) {
	// Count what is actually consumed. Asserting on the parsed result alone
	// passes with no limit at all, and passes for a function that returns the
	// zero value unconditionally — it observes nothing about the bound.
	huge := strings.Repeat("x", 4<<20)
	counted := &countingReader{r: strings.NewReader(huge)}
	got := readStatuslineInput(counted)
	if got.TranscriptPath != "" {
		t.Fatalf("garbage produced a path: %q", got.TranscriptPath)
	}
	if counted.n > 1<<20 {
		t.Fatalf("read %d bytes from a hostile payload, cap is %d", counted.n, 1<<20)
	}
	if counted.n == 0 {
		t.Fatal("read nothing at all, so the bound proves nothing")
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestStatuslineMemoryReachesTheLine(t *testing.T) {
	dir := seedTouchedIndex(t, 3, "/w/t/pool.go")
	var out bytes.Buffer
	payload := `{"transcript_path":"/anywhere/t02.jsonl"}`
	if err := runStatusline(dir, strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "deja · ") {
		t.Fatalf("line lost its prefix: %q", got)
	}
	if !strings.Contains(got, "pool.go") {
		t.Fatalf("the memory did not reach the line: %q", got)
	}
	// It still says what deja served; the memory is added, not substituted.
	if !strings.Contains(got, "recall") {
		t.Fatalf("the usage half is gone: %q", got)
	}
	// The whole point of shortening: a status line lives in whatever width the
	// terminal has.
	if len([]rune(got)) > 140 {
		t.Fatalf("line is %d runes, too long for a status bar: %q", len([]rune(got)), got)
	}
}

// Without a payload the line must be exactly what it was before #581 — every
// harness that pipes nothing, and every interactive run.
func TestStatuslineUnchangedWithoutPayload(t *testing.T) {
	dir := seedTouchedIndex(t, 3, "/w/t/pool.go")
	var out bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "pool.go") {
		t.Fatalf("memory appeared without a transcript path: %q", out.String())
	}
	if !strings.HasPrefix(out.String(), "deja · ") {
		t.Fatalf("line = %q", out.String())
	}
}

func TestTrimStatuslineTitle(t *testing.T) {
	if got := trimStatuslineTitle("short one"); got != "short one" {
		t.Fatalf("got %q", got)
	}
	long := trimStatuslineTitle(strings.Repeat("ab", 60))
	if len([]rune(long)) != statuslineMaxTitle+1 || !strings.HasSuffix(long, "…") {
		t.Fatalf("got %d runes: %q", len([]rune(long)), long)
	}
	// A multi-line title would break the status bar into two rows.
	if got := trimStatuslineTitle("first\nsecond"); strings.Contains(got, "\n") {
		t.Fatalf("newline survived: %q", got)
	}
}

func TestShortenUsage(t *testing.T) {
	got := shortenUsage("deja · 12 recalls · 18.1 KB ctx today · 14.2 KB injected · ~85× less than replaying")
	if got != "12 recalls · 18.1 KB ctx today" {
		t.Fatalf("got %q", got)
	}
	// Already short: nothing to drop, and no prefix left behind.
	if got := shortenUsage("deja · quiet today"); got != "quiet today" {
		t.Fatalf("got %q", got)
	}
}

// A session with no title falls back to the count. Both singular and plural
// have to read right — "1 earlier sessions" is the kind of thing that makes a
// status bar look unfinished.
func TestStatuslineMemoryLineWithoutATitle(t *testing.T) {
	base := fileMemory{Path: "/w/t/pool.go", Sessions: 1, Last: time.Now().Add(-72 * time.Hour)}
	one := statuslineMemoryLine(base)
	if !strings.Contains(one, "1 earlier session ") {
		t.Fatalf("singular reads wrong: %q", one)
	}
	base.Sessions = 4
	many := statuslineMemoryLine(base)
	if !strings.Contains(many, "4 earlier sessions") {
		t.Fatalf("plural reads wrong: %q", many)
	}
	// A title made only of whitespace is no title.
	base.Title = "   "
	if got := statuslineMemoryLine(base); strings.Contains(got, `""`) {
		t.Fatalf("empty quotes in the line: %q", got)
	}
}

// The title is whatever the user typed first, and it lands in a status bar. A
// carriage return rewrites the line, an ANSI escape recolours the whole bar
// for everything printed after it, and a bell makes the terminal beep on every
// refresh — several times a minute.
func TestStatuslineTitleCannotBreakTheBar(t *testing.T) {
	for _, title := range []string{
		"has\ttab", "has\rcarriage", "esc\x1b[31mred\x1b[0m", "bell\x07here",
		"nul\x00byte", "vertical\vtab",
	} {
		got := trimStatuslineTitle(title)
		for _, r := range got {
			if r == '\x1b' || unicode.IsControl(r) {
				t.Errorf("control character %q survived in %q -> %q", r, title, got)
			}
		}
	}
	// Words must not run together where a control character was.
	if got := trimStatuslineTitle("has\ttab"); got != "has tab" {
		t.Errorf("got %q, want the words kept apart", got)
	}
	// Runs of whitespace collapse rather than padding the bar.
	if got := trimStatuslineTitle("a   \n\n  b"); got != "a b" {
		t.Errorf("got %q", got)
	}
	// Truncation counts runes, so a multi-byte character is never split.
	long := trimStatuslineTitle(strings.Repeat("→", 60))
	if !strings.HasSuffix(long, "…") || strings.ContainsRune(long, '�') {
		t.Errorf("truncation damaged the text: %q", long)
	}
}

// The filename reaches the bar from a recorded tool call, which is the same
// class of source as the title: it can carry a carriage return or an escape
// just as easily, and it is not length-bounded by anything upstream.
func TestStatuslineFilenameCannotBreakTheBar(t *testing.T) {
	got := statuslineMemoryLine(fileMemory{
		Path: "/w/pool\r\x1b[31mEVIL.go", Sessions: 2, Title: "clean", Last: time.Now(),
	})
	for _, r := range got {
		if unicode.IsControl(r) {
			t.Fatalf("control character %q survived from the filename: %q", r, got)
		}
	}
	long := statuslineMemoryLine(fileMemory{
		Path: "/w/" + strings.Repeat("n", 200) + ".go", Sessions: 2, Title: "t", Last: time.Now(),
	})
	if len([]rune(long)) > 100 {
		t.Fatalf("a long filename produced a %d-rune line: %q", len([]rune(long)), long)
	}
}

// The quieter half of the same problem: U+202E reverses the rendering of
// everything after it, and zero-width characters pad the bar invisibly.
// Neither is a control character, so unicode.IsControl alone lets them past.
func TestStatuslineStripsFormatCharacters(t *testing.T) {
	for _, in := range []string{"fix \u202ereversed text", "a\u200bb", "c\u2060d", "e\ufefff"} {
		got := trimStatuslineTitle(in)
		for _, r := range got {
			if unicode.Is(unicode.Cf, r) {
				t.Errorf("format character %q survived in %q -> %q", r, in, got)
			}
		}
	}
}

// A session with no timestamp — imported, or from a thin source — would
// otherwise put "Jan 1 0001" on the bar.
func TestStatuslineMemoryLineOmitsAZeroDate(t *testing.T) {
	withTitle := statuslineMemoryLine(fileMemory{Path: "/w/a.go", Sessions: 1, Title: "x"})
	noTitle := statuslineMemoryLine(fileMemory{Path: "/w/a.go", Sessions: 1})
	for _, got := range []string{withTitle, noTitle} {
		if strings.Contains(got, "0001") {
			t.Fatalf("year-one date on the bar: %q", got)
		}
		if strings.HasSuffix(got, " ·") || strings.HasSuffix(got, "· ") {
			t.Fatalf("dangling separator where the date was: %q", got)
		}
	}
}

// A title with a quote in it rendered as 1 earlier: "why does "x" break".
func TestStatuslineQuotesDoNotNest(t *testing.T) {
	got := statuslineMemoryLine(fileMemory{
		Path: "/w/a.go", Sessions: 1, Title: `why does "x" break`, Last: time.Now(),
	})
	if strings.Count(got, `"`) > 0 && strings.Count(got, `"`)%2 != 0 {
		t.Fatalf("unbalanced quotes: %q", got)
	}
	if !strings.Contains(got, "“") || !strings.Contains(got, "”") {
		t.Fatalf("the quoting marks are not the outer pair: %q", got)
	}
}

// Two harnesses can carry the same session id. Excluding by id alone drops a
// genuinely different session from the count.
func TestStatuslineMemoryIdentityIsHarnessAndID(t *testing.T) {
	self := index.SessionMeta{ID: "dup", Harness: "claude"}
	metas := []index.SessionMeta{
		self,
		{ID: "dup", Harness: "codex", Touched: []string{"/w/a.go"}},
		{ID: "other", Harness: "claude", Touched: []string{"/w/a.go"}},
	}
	got := sessionsTouching(metas, "/w/a.go", self)
	if len(got) != 2 {
		t.Fatalf("counted %d sessions, want both the other-harness one and the other id", len(got))
	}
}

// The busiest file of a session is often the one being written from scratch,
// which by definition no earlier session touched. Stopping at Touched[0] made
// the feature silent for 41 of 676 sessions on a real store that had a memory
// to report one entry down.
func TestStatuslineMemoryLooksPastTheBusiestFile(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-d")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	read := func(sid, path string, n int) string {
		out := ""
		var outSb436 strings.Builder
		for i := range n {
			outSb436.WriteString(`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w/d","timestamp":"2026-07-1` +
				strconv.Itoa(i%9) + `T10:0` + strconv.Itoa(i%9) + `:00Z","message":{"role":"assistant","content":` +
				`[{"type":"tool_use","name":"Read","input":{"file_path":"` + path + `"}}]}}` + "\n")
		}
		out += outSb436.String()
		return out
	}
	// The current session hammers a brand new file and also opens an old one.
	current := `{"type":"user","sessionId":"cur","cwd":"/w/d","timestamp":"2026-07-18T10:00:00Z","message":{"role":"user","content":"write the new sharder"}}` + "\n" +
		read("cur", "/w/d/brand_new.go", 5) + read("cur", "/w/d/old_pool.go", 1)
	if err := os.WriteFile(filepath.Join(root, "cur.jsonl"), []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	// An earlier session that only knows the old file.
	earlier := `{"type":"user","sessionId":"old","cwd":"/w/d","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"pool sizing decision"}}` + "\n" +
		read("old", "/w/d/old_pool.go", 3)
	if err := os.WriteFile(filepath.Join(root, "old.jsonl"), []byte(earlier), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, ok := statuslineMemory(dir, transcriptSource{TranscriptPath: "/x/cur.jsonl"})
	if !ok {
		t.Fatal("silent, though an earlier session decided the second file this session touched")
	}
	if filepath.Base(m.Path) != "old_pool.go" {
		t.Fatalf("reported %q, want the file that actually has a memory", m.Path)
	}
}

// The bar redraws every few hundred milliseconds. Sessions stamped the same
// second — a shared shutdown, an import, anything below second granularity —
// used to swap places between two of those redraws, because the sort was over
// AllMeta's map order on Updated alone. Measured 40/20 over 60 runs (#668).
func TestStatuslineMemoryNamesTheSameSessionEveryTime(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-t")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	// Every session ends on the same stamp, so Updated decides nothing.
	for i := range 6 {
		sid := fmt.Sprintf("tie%d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/t","timestamp":"2026-07-11T10:00:00Z","message":{"role":"user","content":"subject number ` + strconv.Itoa(i) + `"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w/t","timestamp":"2026-07-11T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/w/t/shared.go"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	in := transcriptSource{TranscriptPath: filepath.Join(root, "tie0.jsonl")}
	first, ok := statuslineMemory(dir, in)
	if !ok {
		t.Fatal("no memory reported for a file five other sessions touched")
	}
	// Each call re-reads the manifest, so each one sees a fresh map order.
	for i := range 40 {
		got, ok := statuslineMemory(dir, in)
		if !ok {
			t.Fatalf("run %d reported nothing", i)
		}
		if got.Title != first.Title || got.Path != first.Path {
			t.Fatalf("run %d named a different session: %q then %q", i, first.Title, got.Title)
		}
	}
}

func TestNewestFirstIsATotalOrder(t *testing.T) {
	early := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	meta := func(h, id string, at time.Time) index.SessionMeta {
		return index.SessionMeta{Harness: h, ID: id, Updated: at}
	}
	cases := []struct {
		name string
		a, b index.SessionMeta
		want bool
	}{
		{"newer wins", meta("claude", "z", late), meta("claude", "a", early), true},
		{"older loses", meta("claude", "a", early), meta("claude", "z", late), false},
		// Same second: the pair that identifies a session decides, so both
		// directions answer and answer opposite things.
		{"tie falls to harness", meta("claude", "z", early), meta("cursor", "a", early), true},
		{"tie falls to harness, other way", meta("cursor", "a", early), meta("claude", "z", early), false},
		{"tie falls to id within one harness", meta("claude", "a", early), meta("claude", "b", early), true},
		{"tie falls to id, other way", meta("claude", "b", early), meta("claude", "a", early), false},
		{"identical is not less", meta("claude", "a", early), meta("claude", "a", early), false},
	}
	for _, c := range cases {
		if got := newestFirst(c.a, c.b); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The statusline is the one surface a person watches all day, and it already
// speaks up for a build in flight and for a rule that turns memory off. An
// index that is gone — a cleared cache, a moved directory, an unmounted volume
// — left it reporting an ordinary day while recall could answer nothing (#2419).
func TestTheStatuslineSaysWhenThereIsNoIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-08-28T10:00:00Z","cwd":"/work/app",` +
		`"message":{"role":"user","content":"the retry budget on main"}}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: with an index in place the line is the ordinary one.
	var ok bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(`{"session_id":"s1","cwd":"/work/app"}`), &ok); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ok.String(), "index") {
		t.Fatalf("a healthy machine already talks about the index: %q", ok.String())
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	var gone bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(`{"session_id":"s1","cwd":"/work/app"}`), &gone); err != nil {
		t.Fatal(err)
	}
	got := gone.String()
	if strings.Contains(got, "recalls yet today") || strings.Contains(got, "injected") {
		t.Errorf("the index is gone and the line reads as a quiet day: %q", got)
	}
	if !strings.Contains(got, "index") {
		t.Errorf("the line says nothing about the index: %q", got)
	}
}

// The line renders on every prompt, and it read the whole usage log twice to do
// it — 16 ms on a 1.2 MB log, and two passes that can straddle a write and
// print two numbers that were never true together. The invariant is about the
// source: the status line asks the log once (#2224).
func TestTheStatusLineReadsTheUsageLogOnce(t *testing.T) {
	src, err := os.ReadFile("statusline.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"usage.TodayDemand(", "usage.Week(", "usage.TodayRaw("} {
		if strings.Contains(string(src), gone) {
			t.Errorf("statusline.go still calls %s — each is a pass over the log, and the line takes two", gone)
		}
	}
	if !strings.Contains(string(src), "usage.StatusCounters(") {
		t.Error("statusline.go no longer reads the counters through the single pass")
	}
}

// A rule that activates nothing turns recall off everywhere, and the line
// people always have on screen read like an ordinary quiet day — true today,
// and still true tomorrow for a reason nothing named (#1012).
func TestStatuslineSaysWhenAPolicyTurnsMemoryOff(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatusline(dir, strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "off here") {
		t.Errorf("an unrestricted machine was called off: %q", out.String())
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"auto":{"local":false,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out.Reset()
	if err := runStatusline(dir, strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "no recalls yet today") {
		t.Errorf("a machine with recall switched off reads as a quiet day: %q", got)
	}
	if !strings.Contains(got, "trust policy") {
		t.Errorf("the line does not name what switched it off: %q", got)
	}

	// A rule that narrows the auto path is not the same as switching it off:
	// local sessions still reach the agent.
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"auto":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runStatusline(dir, strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "off here") {
		t.Errorf("a narrowed search path was reported as memory off: %q", out.String())
	}
}

// The bar generalised one rule to all of memory: an `auto` rule read as
// "activates nothing" while search and MCP still answered, and a `search` rule
// — the reader's own queries — produced no line at all (#1102).
func TestStatuslineNamesWhichPathsARuleSwitchesOff(t *testing.T) {
	tmp := hermeticEnv(t)
	pol := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", pol)
	write := func(body string) {
		if err := os.WriteFile(pol, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deny := func(a string) string {
		return `{"activations":{"` + a + `":{"local":false,"imported":false}}}`
	}

	write(deny("auto"))
	if got := policyStatusLine(); got != "deja · auto off · the trust policy (`deja doctor`)" {
		t.Errorf("auto-only rule: %q", got)
	}
	write(deny("search"))
	if got := policyStatusLine(); !strings.Contains(got, "search off") {
		t.Errorf("a rule on the reader's own searches said: %q", got)
	}
	write(`{"activations":{"auto":{"local":false,"imported":false},"search":{"local":false,"imported":false},"mcp":{"local":false,"imported":false}}}`)
	if got := policyStatusLine(); !strings.Contains(got, "activates nothing") {
		t.Errorf("a rule that really does switch everything off: %q", got)
	}
	if err := os.Remove(pol); err != nil {
		t.Fatal(err)
	}
	if got := policyStatusLine(); got != "" {
		t.Errorf("no rule at all should leave the ordinary line: %q", got)
	}
}

// The quiet-day line is what the bar shows all day when the week has recalls
// and today has none, and it counted in plural regardless: "1 agent recalls"
// (#1600). The line directly below it in the same file already branches at one.
func TestQuietStatuslineCountsInSingularAtOne(t *testing.T) {
	dir := quietWeekIndex(t, 1)
	var buf bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(`{"session_id":"x","cwd":"/work/one"}`), &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "1 agent recalls") {
		t.Errorf("quiet line says %q", got)
	}
	if !strings.Contains(got, "1 agent recall,") {
		t.Errorf("quiet line lost the count: %q", got)
	}

	// Two keeps the plural.
	dir = quietWeekIndex(t, 2)
	buf.Reset()
	if err := runStatusline(dir, strings.NewReader(`{"session_id":"x","cwd":"/work/one"}`), &buf); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "2 agent recalls") {
		t.Errorf("quiet line lost its plural above one: %q", got)
	}
}

// quietWeekIndex writes n recall events two days old — inside the week, before
// today — straight into the usage log, which is the only way to have a week
// that today does not repeat.
func quietWeekIndex(t *testing.T, n int) string {
	t.Helper()
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	lines := make([]string, 0, n)
	for range n {
		e := usage.Event{Time: time.Now().Add(-48 * time.Hour), Kind: usage.KindRecall, Bytes: 1500, Sessions: 1}
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(usage.Path(dir), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The status bar re-runs on every assistant message. It read stdin with
// ReadAll, which returns when the host closes the pipe — and a host that opens
// stdin and holds it open hung the line for as long as it was allowed to
// (#1074). The hooks have had a bound since #846.
func TestStatuslineDoesNotWaitForeverOnStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Never written to, never closed — the shape a stuck host presents.
	defer func() { _ = w.Close(); _ = r.Close() }()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		readStatuslineInput(r)
		done <- time.Since(start)
	}()
	select {
	case took := <-done:
		if took > 5*time.Second {
			t.Errorf("read took %v with stdin open and empty, want the %v bound", took, hookStdinWait)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the status line is still waiting on stdin after 10s; the bound is %v", hookStdinWait)
	}
}

// And the payload is still read when the host sends one.
func TestStatuslineStillReadsItsPayload(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	go func() {
		_, _ = w.WriteString(`{"transcript_path":"/tmp/does-not-exist.jsonl","cwd":"/tmp/x"}`)
		_ = w.Close()
	}()
	in := readStatuslineInput(r)
	if in.TranscriptPath != "/tmp/does-not-exist.jsonl" {
		t.Errorf("payload lost: %#v", in)
	}
}

func TestStatuslineEmpty(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	var out bytes.Buffer
	if err := runStatusline(index.DefaultDir(), strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "deja · no recalls yet today · 0 B injected" {
		t.Fatalf("empty statusline = %q", got)
	}
}

type chunkReader struct {
	chunks []string
	reads  int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.reads >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.reads])
	r.reads++
	return n, nil
}

func TestDrainStdinNonFileReader(t *testing.T) {
	r := &chunkReader{chunks: []string{"{}", "ignored"}}
	readStatuslineInput(r)
	if r.reads != len(r.chunks) {
		t.Fatalf("reads = %d, want %d", r.reads, len(r.chunks))
	}
}

func TestStatuslineMissingUsageFile(t *testing.T) {
	hermeticEnv(t)
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	var out bytes.Buffer
	if err := runStatusline(index.DefaultDir(), strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "deja · no recalls yet today · 0 B injected" {
		t.Fatalf("statusline = %q", got)
	}
}

func TestStatuslineCountsRecalls(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	usage.Record(dir, usage.KindRecall, 2048)
	usage.Record(dir, usage.KindHook, 1024)
	usage.Record(dir, usage.KindSearch, 4096) // human search, excluded
	var out bytes.Buffer
	if err := runStatusline(index.DefaultDir(), strings.NewReader(`{"session_id":"x"}`), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "1 recall") || !strings.Contains(got, "2.0 KB ctx") || !strings.Contains(got, "1.0 KB injected") {
		t.Fatalf("statusline = %q", got)
	}
}

func TestStatuslineSingular(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	usage.Record(dir, usage.KindContext, 100)
	var out bytes.Buffer
	if err := runStatusline(index.DefaultDir(), strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 recall ·") {
		t.Fatalf("statusline = %q", out.String())
	}
}

// While the first index builds there is nothing to report but the build
// itself, and the status bar is where the user is already looking.
func TestStatuslineReportsColdBuild(t *testing.T) {
	dir := t.TempDir()
	st := warmupStatus{Phase: "indexing", Total: 4, Done: 3, Updated: time.Now().UnixNano()}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warmupStatusPath(dir), b, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "███") || !strings.Contains(got, "75%") {
		t.Fatalf("statusline = %q, want a filled bar and the percentage", got)
	}
	// Once the build is done the line goes back to reporting recalls.
	_ = os.Remove(warmupStatusPath(dir))
	out.Reset()
	if err := runStatusline(dir, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "███") {
		t.Fatalf("statusline still claims a build: %q", out.String())
	}
}

// Most people already run something in the status bar, so the conflict path is
// the common one. It has to hand over a line that works, not advice.
func TestStatuslineConflictOffersAWorkingCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"statusLine":{"type":"command","command":"bash /opt/theirs.sh"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := installStatusline("/bin/deja", false)
	if err == nil {
		t.Fatal("replaced someone else's statusline")
	}
	msg := err.Error()
	for _, want := range []string{
		"bash /opt/theirs.sh", // theirs still runs
		"/bin/deja statusline",
		"json=$(cat)", // Claude pipes session JSON: capture once, feed both
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("conflict message is not a usable command, missing %q:\n%s", want, msg)
		}
	}
}

// A day of nothing but auto-recall is exactly the day the headline count is
// zero — and the line still has to say what did arrive. Reporting "0 B
// injected" there is the same untrue-line problem #1403 was opened about.
func TestStatuslineReportsInjectionsWhenNoAgentAsked(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "index.db")
	for range 5 {
		usage.RecordResult(dir, usage.KindHook, 400, 1, false)
	}
	var out bytes.Buffer
	if err := runStatusline(dir, strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "no recalls yet today") || !strings.Contains(got, "2.0 KB injected") {
		t.Fatalf("statusline = %q", got)
	}
}

// statuslineWith runs the real command with a payload naming the current
// session, which is the only path that adds the file-memory segment.
func statuslineWith(t *testing.T, dir, transcript string) string {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(`{"transcript_path":"` + transcript + `"}`)
	if err := runStatusline(dir, in, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// seedLongTitleIndex is the realistic case the caps were written for: a title
// long enough to be trimmed and a filename long enough to matter. The 20-rune
// titles in seedTouchedIndex never reach the 38-rune cap, so they cannot show
// whether the cap is bounded by anything.
func seedLongTitleIndex(t *testing.T, sharedFile string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-lt")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	titles := []string{
		"the connection pool sizing keeps starving under load on the billing workers",
		"why does the retry budget reset between attempts in the upload queue",
		"the wal checkpoint stalls the writer for eight seconds every hour",
	}
	for i, title := range titles {
		sid := fmt.Sprintf("lt%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/lt","timestamp":"2026-07-1` +
			strconv.Itoa(i) + `T10:00:00Z","message":{"role":"user","content":"` + title + `"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w/lt","timestamp":"2026-07-1` +
			strconv.Itoa(i) + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + sharedFile + `"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// 28 for the name and 38 for the title never added up to a bar anyone
// measured: the memory segment alone reached 95 columns and the terminal cut
// it mid-word, losing the closing quote and the date — the half withFileMemory
// deliberately puts first (#1076).
func TestStatuslineFitsTheBar(t *testing.T) {
	t.Setenv("COLUMNS", "")
	dir := seedLongTitleIndex(t, "/w/lt/connection_pool_manager.go")
	line := statuslineWith(t, dir, "/anywhere/lt02.jsonl")

	if !strings.Contains(line, "connection_pool_manager.go") {
		t.Fatalf("no file memory in the line, wrong fixture: %q", line)
	}
	if w := visibleLen(line); w > 80 {
		t.Errorf("statusline is %d columns, runs off an 80-column bar: %q", w, line)
	}
	// The segment has to arrive whole: a title that is trimmed and then
	// truncated again by the terminal loses its closing quote.
	if !strings.Contains(line, "”") {
		t.Errorf("the memory segment lost its closing quote: %q", line)
	}
	// The title has to have actually been shortened past the 38-rune cap —
	// otherwise the line fits for reasons that have nothing to do with the
	// budget, and a fixed budget would pass this test too.
	open, shut := strings.Index(line, "“"), strings.Index(line, "”")
	if open < 0 || shut < open {
		t.Fatalf("no quoted title: %q", line)
	}
	if got := visibleLen(line[open:shut]) - 1; got >= statuslineMaxTitle {
		t.Errorf("title is %d runes: the 80-column bar did not shrink it below the %d cap: %q",
			got, statuslineMaxTitle, line)
	}
}

// The usage numbers are the half that gives way — that is what withFileMemory
// documents — but only as far as the bar demands, and they come back when
// there is room.
func TestStatuslineKeepsTheNumbersWhenTheyFit(t *testing.T) {
	dir := seedLongTitleIndex(t, "/w/lt/connection_pool_manager.go")

	t.Setenv("COLUMNS", "200")
	wide := statuslineWith(t, dir, "/anywhere/lt02.jsonl")
	if !strings.Contains(wide, "recall") {
		t.Errorf("a 200-column bar dropped the usage numbers: %q", wide)
	}

	t.Setenv("COLUMNS", "80")
	narrow := statuslineWith(t, dir, "/anywhere/lt02.jsonl")
	if visibleLen(narrow) > 80 {
		t.Errorf("80-column bar: %d columns %q", visibleLen(narrow), narrow)
	}
	if visibleLen(wide) <= visibleLen(narrow) {
		t.Errorf("the line did not grow with the bar: wide=%d narrow=%d", visibleLen(wide), visibleLen(narrow))
	}
}

// Without a payload there is no memory segment and the line was always short;
// that path must not change.
func TestStatuslineWithoutPayloadIsUnchanged(t *testing.T) {
	t.Setenv("COLUMNS", "")
	dir := seedTouchedIndex(t, 3, "/w/t/connection_pool_manager.go")
	var out bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	line := out.String()
	if strings.Contains(line, "earlier") {
		t.Errorf("no payload, yet a memory segment appeared: %q", line)
	}
	if w := visibleLen(line); w > 80 {
		t.Errorf("no-payload line is %d columns: %q", w, line)
	}
}

// A bar too narrow for name, frame and floor still gets a readable title
// fragment rather than a stub — the line overflows a little instead.
func TestStatuslineNarrowBarKeepsAReadableTitle(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	dir := seedLongTitleIndex(t, "/w/lt/connection_pool_manager.go")
	line := statuslineWith(t, dir, "/anywhere/lt02.jsonl")

	open := strings.Index(line, "“")
	shut := strings.Index(line, "”")
	if open < 0 || shut < open {
		t.Fatalf("no quoted title on the line: %q", line)
	}
	if got := visibleLen(line[open:shut]); got < statuslineMinTitle {
		t.Errorf("title cut to %d runes, below the floor of %d: %q", got, statuslineMinTitle, line)
	}
}

// Standalone usage lines (when no file memory applies) also have to fit the
// bar: the full four-part line with "less than replaying" is 87+ columns and
// wrapped on an 80-column bar (#5756).
func TestStandaloneUsageFitsTheBar(t *testing.T) {
	full := "deja · 38 recalls · 163.4 KB ctx today · 219.7 KB injected · ~1388× less than replaying"
	for _, tc := range []struct {
		width int
		want  string
	}{
		{100, full},
		{80, "deja · 38 recalls · 163.4 KB ctx today · 219.7 KB injected · ~1388×"},
		{60, "deja · 38 recalls · 163.4 KB ctx today · 219.7 KB injected"},
		{45, "deja · 38 recalls · 163.4 KB ctx today"},
		{25, "deja · 38 recalls"},
	} {
		got := fitUsage(full, tc.width)
		if got != tc.want {
			t.Errorf("fitUsage(%d) = %q, want %q", tc.width, got, tc.want)
		}
		if visibleLen(got) > tc.width {
			t.Errorf("fitUsage(%d) is %d columns: %q", tc.width, visibleLen(got), got)
		}
	}
}

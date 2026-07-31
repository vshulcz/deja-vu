package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
)

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
	for i := 0; i < sessions; i++ {
		sid := fmt.Sprintf("t%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/t","timestamp":"2026-07-1` +
			fmt.Sprint(i%10) + `T10:00:00Z","message":{"role":"user","content":"pool sizing, round ` + fmt.Sprint(i) + `"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w/t","timestamp":"2026-07-1` +
			fmt.Sprint(i%10) + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"` + sharedFile + `"}}]}}` + "\n"
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
		for i := 0; i < n; i++ {
			out += `{"type":"assistant","sessionId":"` + sid + `","cwd":"/w/d","timestamp":"2026-07-1` +
				fmt.Sprint(i%9) + `T10:0` + fmt.Sprint(i%9) + `:00Z","message":{"role":"assistant","content":` +
				`[{"type":"tool_use","name":"Read","input":{"file_path":"` + path + `"}}]}}` + "\n"
		}
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

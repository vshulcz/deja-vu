package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
			fmt.Sprint(i%10) + `T10:00:00Z","message":{"role":"user","content":"why is the pool sized like this"}}` + "\n" +
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
	in := statuslineInput{TranscriptPath: "/anywhere/t02.jsonl"}
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
	// #581: name a decision, not a count, when one is available.
	if !strings.Contains(line, "pool sized") {
		t.Fatalf("line reports a statistic where a memory was available: %q", line)
	}
}

// Silence is the requirement, not a zero: a file no one else has worked on
// has no memory to report, and "0 sessions" would be permanent noise.
func TestStatuslineMemorySilentWhenAlone(t *testing.T) {
	dir := seedTouchedIndex(t, 1, "/w/t/pool.go")
	if _, ok := statuslineMemory(dir, statuslineInput{TranscriptPath: "/anywhere/t00.jsonl"}); ok {
		t.Fatal("the only session that touched the file should report nothing")
	}
}

func TestStatuslineMemorySilentWithoutATranscriptPath(t *testing.T) {
	dir := seedTouchedIndex(t, 3, "/w/t/pool.go")
	for _, tp := range []string{"", "   ", "/"} {
		if _, ok := statuslineMemory(dir, statuslineInput{TranscriptPath: tp}); ok {
			t.Fatalf("transcript_path %q should report nothing", tp)
		}
	}
	// A session deja has never indexed: the id is real-looking but unknown.
	if _, ok := statuslineMemory(dir, statuslineInput{TranscriptPath: "/x/never-seen.jsonl"}); ok {
		t.Fatal("an unknown session should report nothing")
	}
}

func TestStatuslineMemorySilentWithoutAnIndex(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "no-index")
	if _, ok := statuslineMemory(dir, statuslineInput{TranscriptPath: "/x/t00.jsonl"}); ok {
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
	if got.TranscriptPath != "/x/a.jsonl" || got.Workspace.CurrentDir != "/w" {
		t.Fatalf("parsed = %+v", got)
	}
}

// A status line runs on every assistant message. Reading a payload bigger than
// the limit must not hang or blow memory.
func TestReadStatuslineInputIsBounded(t *testing.T) {
	huge := strings.Repeat("x", 4<<20)
	got := readStatuslineInput(strings.NewReader(huge))
	if got.TranscriptPath != "" {
		t.Fatalf("garbage produced a path: %q", got.TranscriptPath)
	}
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

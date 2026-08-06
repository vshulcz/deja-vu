package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

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
			fmt.Sprint(i) + `T10:00:00Z","message":{"role":"user","content":"` + title + `"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w/lt","timestamp":"2026-07-1` +
			fmt.Sprint(i) + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + sharedFile + `"}}]}}` + "\n"
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
	open, close := strings.Index(line, "“"), strings.Index(line, "”")
	if open < 0 || close < open {
		t.Fatalf("no quoted title: %q", line)
	}
	if got := visibleLen(line[open:close]) - 1; got >= statuslineMaxTitle {
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
	close := strings.Index(line, "”")
	if open < 0 || close < open {
		t.Fatalf("no quoted title on the line: %q", line)
	}
	if got := visibleLen(line[open:close]); got < statuslineMinTitle {
		t.Errorf("title cut to %d runes, below the floor of %d: %q", got, statuslineMinTitle, line)
	}
}

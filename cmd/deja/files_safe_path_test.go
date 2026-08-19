package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// A file name can hold an escape or a carriage return on any Unix host, and
// the path is recorded from the tool call verbatim. #1090 stripped those from
// the reading surfaces; this row was missed, so a name with a colour escape
// recoloured the rest of the screen and one with a return printed its tail
// over its head — the row then named a file the reader does not have.
func TestFilesRowStripsWhatTheTerminalActsOn(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range []string{
		"retry_\x1b[31mred\x1b[0m_handler.go",
		"retry_\rrewound_handler.go",
		"retry_\x07bell_handler.go",
		"plain_retry_handler.go",
	} {
		full := filepath.Join(repo, "src", name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			// Windows refuses these names; there the defect cannot be reached.
			t.Skipf("cannot create %q here: %v", name, err)
		}
		paths = append(paths, full)
	}

	root := filepath.Join(tmp, "claude", "proj-h")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	var body strings.Builder
	body.WriteString(`{"type":"user","sessionId":"h1","cwd":` + jsonString(repo) + `,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the retry storm on checkout"}}` + "\n")
	for _, p := range paths {
		q, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		body.WriteString(`{"type":"assistant","sessionId":"h1","cwd":` + jsonString(repo) +
			`,"timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":` +
			string(q) + `}}]}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(root, "h1.jsonl"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := runFiles(index.DefaultDir(), []string{"retry"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// The fixture has to have reached the table, or this passes on an empty
	// answer.
	if !strings.Contains(out, "handler.go") {
		t.Fatalf("no rows in the table: %q", out)
	}
	for _, r := range out {
		if r == '\n' {
			continue
		}
		if unicode.IsControl(r) {
			t.Errorf("a control character reached the terminal: %q in %q", r, out)
			break
		}
	}

	// And the rows still line up: the escape bytes were counted as width
	// before, so the row carrying them ended in a different column.
	var widths []int
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  ") {
			rows = append(rows, line)
			widths = append(widths, termwidth.Columns(line))
		}
	}
	if len(rows) < 2 {
		t.Fatalf("wrong fixture: %d rows", len(rows))
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("row %d is %d columns, row 0 is %d:\n%s", i, w, widths[0], strings.Join(rows, "\n"))
		}
	}
}

// The escapes are stripped before the path is measured, not after. A run of
// them collapses to one space, so measuring the path with them still in it
// buys fewer characters of the name than the column has room for.
func TestFilesRowPathTrimsAfterStripping(t *testing.T) {
	const dir = "/w/repo/a_long_enough_directory_to_force_a_trim/nested/"
	const name = "_retry_backoff_and_jitter_handler.go"
	plain := filesRowPath(dir+"plain"+name, 40)
	escaped := filesRowPath(dir+"plain\x1b\x1b\x1b\x1b\x1b\x1b"+name, 40)
	if termwidth.Columns(escaped) != termwidth.Columns(plain) {
		t.Errorf("the escapes spent %d columns of the budget:\n  plain   %q\n  escaped %q",
			termwidth.Columns(plain)-termwidth.Columns(escaped), plain, escaped)
	}
	if termwidth.Columns(plain) > 40 {
		t.Errorf("row is %d columns: %q", termwidth.Columns(plain), plain)
	}
	// The trim has to have bitten, or both sides fit whole and the comparison
	// above is about nothing.
	if !strings.HasPrefix(plain, "…") || !strings.HasPrefix(escaped, "…") {
		t.Fatalf("wrong fixture, nothing was trimmed:\n  %q\n  %q", plain, escaped)
	}
	if !strings.HasSuffix(plain, "handler.go") {
		t.Fatalf("wrong fixture, the name did not survive the trim: %q", plain)
	}
}

// A newline in a file name is not a second row of deja's output.
func TestFilesRowPathStaysOnOneLine(t *testing.T) {
	got := filesRowPath("/w/repo/src/retry_\nsecond_line_handler.go", 56)
	if strings.Contains(got, "\n") {
		t.Errorf("the row carries a newline: %q", got)
	}
	if !strings.Contains(got, "second_line_handler.go") {
		t.Errorf("the tail after the newline was dropped rather than joined: %q", got)
	}
}

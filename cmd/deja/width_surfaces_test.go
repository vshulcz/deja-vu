package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// widthStore indexes one session that names several files inside a repository
// on this disk — `files` only reports paths it can still find, so the fixture
// has to build the tree as well as the transcript.
func widthStore(t *testing.T) {
	t.Helper()
	hermeticEnv(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, rel := range []string{
		"internal/application/partner/requests.go",
		"internal/domain/partner/webhook_delivery.go",
	} {
		full := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package partner\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, full)
	}
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"type":"user","message":{"role":"user","content":"why does the retry queue stall on staging when the workers back off with a fixed delay instead of full jitter, and every one of them wakes at the same moment"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/app"}` + "\n")
	for _, full := range paths {
		esc := strings.ReplaceAll(full, `\`, `\\`)
		b.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"` + esc + `"}}]},"timestamp":"2026-08-02T10:01:00Z","sessionId":"aaa","cwd":"/app"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
}

// The path column was a fixed 56, so on a 60-column pane every row wrapped
// (#604). COLUMNS is the override a reader sets on purpose, and it is what a
// test can drive without a pty.
func TestFilesFitsTheTerminal(t *testing.T) {
	widthStore(t)
	t.Setenv("COLUMNS", "50")
	out, err := captureRun(t, "files", "retry")
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		rows++
		if len([]rune(line)) > 50 {
			t.Errorf("a row ran to %d runes at COLUMNS=50: %q", len([]rune(line)), line)
		}
		if !strings.HasSuffix(strings.TrimSpace(line), "1") {
			t.Errorf("the count fell off the row: %q", line)
		}
	}
	if rows == 0 {
		t.Fatalf("no file rows to judge:\n%s", out)
	}
}

// The tail of a path is what identifies it, so the cut takes the head.
func TestFilesKeepsTheFileName(t *testing.T) {
	widthStore(t)
	t.Setenv("COLUMNS", "44")
	out, err := captureRun(t, "files", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "requests.go") || !strings.Contains(out, "webhook_delivery.go") {
		t.Errorf("a narrow window lost the file names:\n%s", out)
	}
}

// A pipe is not a terminal: the whole path goes out, because a script reading
// this wants the text rather than the layout.
func TestFilesIsNotCutWhenPiped(t *testing.T) {
	widthStore(t)
	// First with a narrow window, to show the fixture's paths are long enough
	// that cutting would be visible at all.
	t.Setenv("COLUMNS", "44")
	out, err := captureRun(t, "files", "retry")
	if err != nil {
		t.Fatal(err)
	}
	// The width cut takes more than trimPath's head removal does, which is what
	// gives the pipe assertion below something to be different from.
	if strings.Contains(out, filepath.ToSlash(filepath.Join("internal", "application", "partner", "requests.go"))) {
		t.Fatalf("a narrow window did not cut these paths, so this test means nothing:\n%s", out)
	}
	// Narrow enough that a terminal would have cut it, and the pipe must not.
	t.Setenv("COLUMNS", "")
	out, err = captureRun(t, "files", "retry")
	if err != nil {
		t.Fatal(err)
	}
	// trimPath still drops the head of a deep path (#727); what must not happen
	// is a second, width-driven cut on top of it.
	if !strings.Contains(out, filepath.ToSlash(filepath.Join("internal", "application", "partner", "requests.go"))) {
		t.Errorf("the path a pipe gets was cut further than trimPath cuts:\n%s", out)
	}
}

// And the search itself: the printer can budget all it likes, but the width has
// to reach it from the command that owns the terminal.
func TestSearchOutputFitsTheTerminal(t *testing.T) {
	widthStore(t)
	t.Setenv("COLUMNS", "50")
	out, err := captureRun(t, "retry")
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		seen = true
		if len([]rune(line)) > 50 {
			t.Errorf("a snippet ran to %d runes at COLUMNS=50: %q", len([]rune(line)), line)
		}
	}
	if !seen {
		t.Fatalf("no snippet lines to judge:\n%s", out)
	}
}

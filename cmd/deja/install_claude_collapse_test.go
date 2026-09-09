package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Claude Code's settings.json is the one hook config that never collapsed a
// pile of deja's own entries. Every other writer keeps the first and drops the
// rest (qwen in #2745, cursor in #2691); this one adopted each of them in turn
// and rewrote them all into byte-identical copies, so an install "succeeded"
// and left the hook firing once per copy. One machine reached eight entries per
// event that way — every prompt paid for eight processes and got the same
// memory eight times (#3421).
func TestInstallCollapsesClaudeHooksOntoOne(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var entries []string
	for i := 0; i < 8; i++ {
		entries = append(entries, `{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt"}]}`)
	}
	doubled := `{"hooks":{"UserPromptSubmit":[` + strings.Join(entries, ",") + `]}}`
	if err := os.WriteFile(path, []byte(doubled), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installTarget("claude-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readFile(t, path), "hook-prompt"); n != 1 {
		t.Fatalf("install left %d copies of the prompt hook:\n%s", n, readFile(t, path))
	}
}

// A line the reader wrote around deja's hook is theirs, and it already calls
// the hook: deja's own entry beside it is the duplicate, and the collapse must
// take that one rather than the wrapper.
func TestCollapseKeepsAReadersOwnLine(t *testing.T) {
	ours := `{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt"}]}`
	theirs := `{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt | tee /tmp/deja.log"}]}`
	// Either order: the file keeps whichever entry it was written in, and the
	// wrapper is the one to survive both times.
	for name, entries := range map[string][]string{
		"wrapper first": {theirs, ours},
		"wrapper last":  {ours, theirs},
	} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			doubled := `{"hooks":{"UserPromptSubmit":[` + strings.Join(entries, ",") + `]}}`
			if err := os.WriteFile(path, []byte(doubled), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installTarget("claude-auto", "/usr/local/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, path)
			if !strings.Contains(got, "tee /tmp/deja.log") {
				t.Fatalf("the collapse threw away a line deja did not write:\n%s", got)
			}
			if n := strings.Count(got, "hook-prompt"); n != 1 {
				t.Fatalf("the hook still runs %d times:\n%s", n, got)
			}
		})
	}
}

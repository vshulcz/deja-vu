package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// "try fewer words" is right in direction and empty in content: the reader has
// to guess which of their words to drop, while deja read the per-term counts to
// decide there was no intersection (#826).
func TestNoMatchesNamesTheTermsThatMatchAlone(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"the sewage macerator showed blockage"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"a1","cwd":"/proj"}`,
		`{"type":"user","message":{"role":"user","content":"the bilge pump impeller was worn"},"timestamp":"2026-07-02T10:00:00Z","sessionId":"a2","cwd":"/proj"}`,
	}
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	dir := indexDirForTest()
	got := termCountLine(dir, "macerator pump")
	if !strings.Contains(got, `"macerator" in 1`) || !strings.Contains(got, `"pump" in 1`) {
		t.Errorf("the counts are not named: %q", got)
	}
	if !strings.Contains(got, "no session has them together") {
		t.Errorf("the line does not say what it means: %q", got)
	}

	// One known word, one not: name the one that can be kept.
	mixed := termCountLine(dir, "macerator zzzqqq")
	if !strings.Contains(mixed, `"macerator" in 1`) || strings.Contains(mixed, "zzzqqq") {
		t.Errorf("mixed query: %q", mixed)
	}

	// Nothing to drop: every term is unknown, and a row of zeroes is noise.
	if got := termCountLine(dir, "zzzqqq wwwqqq"); got != "" {
		t.Errorf("a query of unknown words got a line: %q", got)
	}
	// A single term has nothing to intersect with.
	if got := termCountLine(dir, "macerator"); got != "" {
		t.Errorf("a one-word query got a line: %q", got)
	}
	// And the whole no-match path still prints its own line.
	out, err := captureRunStderr(t, "macerator", "zzzqqq")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "on their own") {
		t.Errorf("the counts line did not reach the no-match output:\n%s", out)
	}
}

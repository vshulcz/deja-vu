package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A correct plural is not a misspelling. The word-forms rung could not reach
// the singular of a noun ending in "e", so the fuzzy rung answered and the
// reader was told they had typed it wrong (#2137).
func TestAPluralIsAnsweredAsAWordFormNotASpelling(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message": map[string]any{"role": "user", "content": "the ingest pipeline stalled and the warm cache " +
			"was dropped on every release"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"pipelines", "caches", "releases"} {
		out, _ := captureRun(t, "search", q)
		if !strings.Contains(out, "s1") {
			t.Fatalf("%q finds nothing, so this measures nothing:\n%s", q, out)
		}
		errOut, err := captureRunStderr(t, "search", q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(errOut, "close spellings") {
			t.Errorf("%q is the plural, not a misspelling:\n%s", q, errOut)
		}
		if !strings.Contains(errOut, "word forms") {
			t.Errorf("%q lost the word-forms line:\n%s", q, errOut)
		}
	}
	// A word that really is mistyped keeps the sentence about spelling.
	errOut, err := captureRunStderr(t, "search", "pipelien")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "close spellings") {
		t.Errorf("a typo should still be answered as one:\n%s", errOut)
	}
}

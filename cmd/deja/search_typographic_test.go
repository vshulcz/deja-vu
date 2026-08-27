package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The end of #2117: a query pasted with typographic punctuation reaches the
// word the index holds, rather than missing outright or being rescued by the
// close-spellings fallback — which tells the reader they misspelled something
// they did not.
func TestAPastedQueryFindsWhatItAsksFor(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the retry budget keeps blowing up under load"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The premise: the plain query works, so the rest is about the punctuation.
	if out, _ := captureRun(t, "search", "retry budget"); !strings.Contains(out, "retry budget") {
		t.Fatalf("the plain query found nothing, so this measures nothing: %q", out)
	}
	for _, q := range []string{
		"“retry budget”",
		"—retry—",
		"«retry»",
		"budget…",
		"retry’s budget",
	} {
		var out string
		stderr := captureStderr(t, func() { out, _ = captureRun(t, "search", q) })
		if !strings.Contains(out, "s1") {
			t.Errorf("%q found nothing: %q %q", q, out, strings.TrimSpace(stderr))
			continue
		}
		if strings.Contains(stderr, "close spellings") {
			t.Errorf("%q was treated as a misspelling: %q", q, strings.TrimSpace(stderr))
		}
	}
}

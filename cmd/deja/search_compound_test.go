package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Indexing splits a compound — `identifierParts` is why `retry backoff` reaches
// a store that wrote `retry-backoff` — and querying did not, so the same
// difference the other way round found nothing at all (#2125).
func TestAHyphenatedQueryReachesTheWordsSpelledApart(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message": map[string]any{"role": "user", "content": "the retry-backoff helper keeps blowing up under load; " +
			"we raised the pool size"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The premise and the symmetry: the store's own spellings match, and so
	// does the compound spelled apart.
	for _, q := range []string{"retry-backoff", "retry backoff", "blowing up", "pool size"} {
		if out, _ := captureRun(t, "search", q); !strings.Contains(out, "s1") {
			t.Fatalf("%q finds nothing, so this measures nothing", q)
		}
	}
	// The direction that had nothing: a hyphen or an underscore where the
	// transcript had a space.
	for _, q := range []string{"blowing-up", "pool_size"} {
		out, _ := captureRun(t, "search", q)
		if !strings.Contains(out, "s1") {
			t.Errorf("%q finds nothing, though the session says those words apart:\n%s", q, out)
		}
	}
	// The words have to be together the way the compound says: a session that
	// holds both words far apart is not what "blowing-up" asked for, and one
	// that holds neither is not a match at all.
	for _, q := range []string{"vacuum-freeze", "pool-vacuum"} {
		if out, _ := captureRun(t, "search", q); strings.Contains(out, "s1") {
			t.Errorf("%q matched a session that does not say it:\n%s", q, out)
		}
	}
}

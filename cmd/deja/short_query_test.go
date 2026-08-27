package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Very short tokens are dropped and punctuation is trimmed, so these queries
// never reach the index — and the answer advised using fewer words, of one
// word or of none (#828).
func TestQueryWithNothingToLookUpSaysSo(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the hydraulic pump bearing failed"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"a1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"p", "a b c"} {
		out, err := captureRunStderr(t, q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "too short to look up") {
			t.Errorf("%q: %s", q, out)
		}
		if strings.Contains(out, "try fewer words") {
			t.Errorf("%q still advises fewer words:\n%s", q, out)
		}
	}

	// Punctuation has no word in it at all, so length is not what stopped it:
	// that query gets the sentence about words rather than the one about
	// length (#2133).
	out, err := captureRunStderr(t, "...")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to search for") || strings.Contains(out, "too short to look up") {
		t.Errorf("\"...\" is not too short, it holds no word:\n%s", out)
	}

	// A word the store simply does not have keeps the ordinary answer: there
	// is something to look up, it just is not there.
	out, err = captureRunStderr(t, "zzzqqq")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no matches in") {
		t.Errorf("an unknown word lost the ordinary miss message:\n%s", out)
	}

	// The cut is on bytes, so a single Cyrillic or CJK character is long
	// enough — the message must not fire for them.
	for _, q := range []string{"л", "舵"} {
		out, err := captureRunStderr(t, q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "too short to look up") {
			t.Errorf("%q was treated as too short:\n%s", q, out)
		}
	}
}

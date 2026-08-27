package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A query with no word in it is not a query with a short word in it. Emoji are
// four bytes, well over the cut #828 describes, and the tokenizer drops them
// because they are symbols — telling the reader to try longer words sends them
// after something that would never have worked (#2133).
func TestASymbolOnlyQuerySaysWhyItFoundNothing(t *testing.T) {
	symbolQueryStore(t)

	for _, q := range []string{"🔥", "🔥🔥", "✅", "!!!", "→", "❤️", "👨‍👩‍👧"} {
		out, err := captureRunStderr(t, "search", q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "nothing to search for") || !strings.Contains(out, "are not words") {
			t.Errorf("%q: want the sentence about words, got:\n%s", q, out)
		}
		if strings.Contains(out, "too short") {
			t.Errorf("%q is not too short, it holds no word at all:\n%s", q, out)
		}
	}
	// The sentence that is about length keeps the queries it is about.
	for _, q := range []string{"p", "a b"} {
		out, err := captureRunStderr(t, "search", q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "too short") {
			t.Errorf("%q is the short-word case and should still say so:\n%s", q, out)
		}
	}
	// --re does not use the word index at all, so neither sentence is true
	// there: a regex that found nothing is an ordinary miss.
	out, err := captureRunStderr(t, "search", "--re", "🔥")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "nothing to search for") {
		t.Errorf("--re does not go through the word index:\n%s", out)
	}
}

// An emoji written with a joiner or a variation selector used to leave that
// invisible character standing as the whole query, so "❤️" asked for the
// selector and answered with every session that spelled some other emoji the
// same way (#2133).
func TestAnEmojiQueryDoesNotMatchADifferentEmoji(t *testing.T) {
	symbolQueryStore(t)

	out, _ := captureRun(t, "search", "❤️")
	for _, sid := range []string{"s1", "s2"} {
		if strings.Contains(out, sid) {
			t.Errorf("\"❤️\" matched %s, which says a different emoji:\n%s", sid, out)
		}
	}
	// The words beside the emoji still find both sessions, which is the only
	// way either of them was ever reachable.
	for q, want := range map[string]string{"disk": "s2", "bearing": "s1"} {
		if out, _ := captureRun(t, "search", q); !strings.Contains(out, want) {
			t.Errorf("%q lost %s, so this measures nothing:\n%s", q, want, out)
		}
	}
}

// Two sessions whose only emoji are spelled with the invisible characters at
// issue: a variation selector in one, a zero-width joiner in the other.
func symbolQueryStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for sid, text := range map[string]string{
		"s1": "the pump bearing failed ⚠️ again",
		"s2": "the disk is nearly full \U0001F468‍\U0001F469‍\U0001F467 said the family",
	} {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

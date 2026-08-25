package sources

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A tag is a short handle someone searches for. A control byte in one is not a
// tag anyone meant to write, and it reached notes.jsonl and every surface that
// reads it (#1810).
func TestNormalizeTagsDropsControlBytes(t *testing.T) {
	got := NormalizeTags([]string{"ok", "red\x1b[31mALERT\x1b[0m\rrewound", "two\nlines"})
	for _, tag := range got {
		if strings.ContainsAny(tag, "\x1b\r\n") {
			t.Errorf("a stored tag carries a control byte: %q", tag)
		}
	}
	if len(got) != 3 {
		t.Fatalf("tags were dropped rather than cleaned: %q", got)
	}
	if got[0] != "ok" {
		t.Errorf("an ordinary tag was altered: %q", got[0])
	}
	if !strings.Contains(got[1], "alert") {
		t.Errorf("the word the user typed is gone: %q", got[1])
	}
}

// The count has been capped at 8 since tags landed; one tag's length was not
// bounded at all, so a 400-character tag was stored and printed whole.
func TestNormalizeTagsBoundsOneTag(t *testing.T) {
	got := NormalizeTags([]string{strings.Repeat("w", 400)})
	if len(got) != 1 {
		t.Fatalf("the tag was dropped: %q", got)
	}
	if len(got[0]) > maxTagLen {
		t.Errorf("one tag is %d bytes, cap is %d", len(got[0]), maxTagLen)
	}
	// A tag that fits is untouched, so the bound is not paid by ordinary use.
	if same := NormalizeTags([]string{"retry-budget"}); same[0] != "retry-budget" {
		t.Errorf("an ordinary tag was cut: %q", same[0])
	}
}

// A tag with a space in it was never a handle anything could match — the tags
// are folded into "#tag" tokens in the indexed text, and half a tag matches
// nothing. Folding the space to a hyphen makes it one, and makes it the same
// handle as the hyphenated spelling someone else typed.
func TestATagWithASpaceBecomesOneHandle(t *testing.T) {
	got := NormalizeTags([]string{"retry budget", "retry-budget"})
	if len(got) != 1 || got[0] != "retry-budget" {
		t.Errorf("the two spellings did not fold into one handle: %q", got)
	}
	// Multibyte tags survive the cut as valid text, not as half a character.
	long := NormalizeTags([]string{strings.Repeat("резервирование", 8)})[0]
	if !utf8.ValidString(long) {
		t.Errorf("the cut split a character: %q", long)
	}
	if len(long) > maxTagLen {
		t.Errorf("a Cyrillic tag is %d bytes, cap is %d", len(long), maxTagLen)
	}
}

package sources

import (
	"strings"
	"testing"
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

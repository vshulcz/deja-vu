package index

import (
	"strings"
	"testing"
)

// tokenHits answers what the ranker actually asks — does this message carry any
// of these words — without building the message's whole token set. It has to
// tokenise identically, or the ranking changes with the shortcut.
func TestTokenHitsAgreesWithTokens(t *testing.T) {
	texts := []string{
		"The retry budget was capped at three attempts",
		"café vs café: the same word, composed and decomposed",
		"كَتَبَ and हिन्दी carry marks that continue a word",
		"internal/index/fixpair.go:146 _ = writeGob(fixesPath(tmp), kept)",
		"MiXeD CaSe and UPPER CASE and lower",
		"hyphen-joined and under_scored tokens",
		strings.Repeat("a", 70) + " " + strings.Repeat("b", 130),
		"один два три ЧЕТЫРЕ",
		"",
		"a b c",
		"日本語のトークン化",
	}
	for _, text := range texts {
		all := tokens(text)
		want := map[string]bool{}
		for _, tk := range all {
			want[tk] = true
		}
		// Every token the full tokeniser found must be reported.
		got := tokenHits(text, want)
		for _, tk := range all {
			if !got[tk] {
				t.Errorf("tokenHits(%.30q) missed %q", text, tk)
			}
		}
		if len(got) != len(want) {
			t.Errorf("tokenHits(%.30q) found %d tokens, tokens found %d", text, len(got), len(want))
		}
		// And a word the text does not carry is not invented.
		if hit := tokenHits(text, map[string]bool{"quokkabloom": true}); len(hit) != 0 {
			t.Errorf("tokenHits(%.30q) claimed a word the text lacks: %v", text, hit)
		}
	}
}

// The wanted set is what bounds the answer: a token in the text that nobody
// asked about is not reported.
func TestTokenHitsReportsOnlyWhatWasAsked(t *testing.T) {
	got := tokenHits("the retry budget was capped at three", map[string]bool{"budget": true, "scheduler": true})
	if !got["budget"] || got["scheduler"] || len(got) != 1 {
		t.Fatalf("want budget alone, got %v", got)
	}
	if tokenHits("anything at all", nil) != nil {
		t.Error("an empty question got an answer")
	}
}

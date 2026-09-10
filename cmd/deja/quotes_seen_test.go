package main

import (
	"strings"
	"testing"
)

// The block is deduped whole and its header carries the session's id and date,
// so the same sentence from a fork or a subagent transcript passed as news.
// Measured on a real store over eight near-identical nudges — the shape a reader
// produces when they keep saying "carry on": fifteen quoted lines, eleven
// distinct, the worst one four times under four different ids.
func TestASentenceIsNotNewsUnderAnotherId(t *testing.T) {
	body := "deja-vu — you have been here:\n" +
		"- **beta** `aaaa1111` · 2026-09-08\n" +
		"  - Assistant: the kestrel retry budget stays at four\n" +
		"- **beta** `bbbb2222` · 2026-09-09\n" +
		"  - Assistant: the kestrel retry budget stays at four\n" +
		"- **beta** `cccc3333` · 2026-09-10\n" +
		"  - Assistant: the cold path warms the index first\n" +
		"If it helped, say: …\n"

	out, kept, fresh := withoutSeenQuotes(map[string]bool{}, body)
	if !fresh {
		t.Fatal("a block of new quotes was suppressed")
	}
	if n := strings.Count(out, "retry budget stays at four"); n != 1 {
		t.Errorf("the same sentence appears %d times in one block", n)
	}
	if !strings.Contains(out, "cold path warms the index") {
		t.Errorf("a different sentence was dropped with the duplicate:\n%s", out)
	}
	if strings.Contains(out, "bbbb2222") {
		t.Errorf("a session left with nothing to say kept its header:\n%s", out)
	}
	if len(kept) != 2 {
		t.Errorf("remembered %d quotes, want the two distinct ones", len(kept))
	}

	// Said again in a later turn, under ids the first block never mentioned.
	seen := map[string]bool{}
	for _, k := range kept {
		seen[k] = true
	}
	again := "deja-vu — you have been here:\n" +
		"- **beta** `dddd4444` · 2026-09-10\n" +
		"  - Assistant: the kestrel retry budget stays at four\n" +
		"If it helped, say: …\n"
	if _, _, fresh := withoutSeenQuotes(seen, again); fresh {
		t.Error("a sentence the agent has already been shown counted as news")
	}
}

// A block that quotes nothing — the weak pointer — is not a repeat of anything
// and keeps its line.
func TestABlockWithNoQuotesIsLeftAlone(t *testing.T) {
	body := "deja has 3 sessions about kestrel — `deja recall kestrel` opens them.\n"
	out, _, fresh := withoutSeenQuotes(map[string]bool{"q:whatever": true}, body)
	if !fresh || out != body {
		t.Errorf("a block with no quotes was changed or suppressed: fresh=%v\n%s", fresh, out)
	}
}

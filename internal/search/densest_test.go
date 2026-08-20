package search

import "testing"

// The mentions and the conclusion usually sit in one message: a paragraph that
// names the subject while putting it off, then the sentence that settles it.
// Picking the densest line first and asking about a conclusion afterwards hands
// the slot to the paragraph. Measured on a real store, that was 35 of 119
// blocks quoting a weaker line than the same session held.
func TestDensestLinePrefersTheConclusion(t *testing.T) {
	text := "looked at dunlin and at the dunlin retries, not touching either yet\n" +
		"still reading the dunlin docs, will decide about dunlin later\n" +
		"the fix: dunlin retries are capped at four"
	line, hits := densestLine(text, []string{"dunlin"})
	if line != "the fix: dunlin retries are capped at four" {
		t.Errorf("quoted the denser mention instead of the conclusion: %q", line)
	}
	if hits < 1 {
		t.Errorf("the chosen line carries the term, so hits must count it: %d", hits)
	}
}

func TestDensestLineWithoutAnyConclusion(t *testing.T) {
	// Hits count distinct query words, not repeats, so the second line wins on
	// holding both of them rather than on saying "dunlin" twice.
	text := "dunlin is slow\nthe dunlin backoff is slow too"
	line, hits := densestLine(text, []string{"dunlin", "backoff"})
	if line != "the dunlin backoff is slow too" {
		t.Errorf("with nothing settled the line carrying more of the query wins: %q", line)
	}
	if hits != 2 {
		t.Errorf("hits = %d, want 2", hits)
	}
}

package search

import (
	"strings"
	"testing"
)

// Picking the right message is only half of showing the answer: the excerpt is
// a 300-rune window cut out of it. Centring that window on the first word of the
// query to appear put the reader four paragraphs from the passage they searched
// for, in the same message that matched.
func TestSnippetCentresWhereTheWordsMeet(t *testing.T) {
	text := "gift " + strings.Repeat("filler ", 80) + "the gift my dad gave me for the trip"
	if got := Snippet(text, "dad gift"); !strings.Contains(got, "my dad gave me") {
		t.Errorf("excerpt %q does not show where the query words meet", got)
	}
}

// When the words never come close, there is no meeting to point at, and the
// excerpt goes back to the first mention — otherwise a fix could drag the window
// to an arbitrary midpoint that shows neither word.
func TestSnippetFallsBackToTheFirstMentionWhenWordsStayApart(t *testing.T) {
	// The tightest span here starts in the middle, at "gift", and is still far
	// too wide to be a meeting. Pointing the excerpt at it would show a word
	// from the middle of the message and call it the match.
	text := "dad " + strings.Repeat("filler ", 400) + "gift " + strings.Repeat("filler ", 35) + "dad"
	got := Snippet(text, "dad gift")
	if !strings.HasPrefix(got, "dad") {
		t.Errorf("excerpt %q does not start at the first mention", got)
	}
}

// A quoted phrase is still shown where the phrase itself is, not where its
// separate words happen to meet elsewhere.
func TestSnippetKeepsTheWholeQueryMatchFirst(t *testing.T) {
	text := strings.Repeat("filler ", 60) + "dad gift" + strings.Repeat(" filler", 60)
	if got := Snippet(text, "dad gift"); !strings.Contains(got, "dad gift") {
		t.Errorf("excerpt %q lost the exact query match", got)
	}
}

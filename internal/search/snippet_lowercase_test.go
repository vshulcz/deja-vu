package search

import (
	"strings"
	"testing"
)

// The excerpt is positioned by searching the lowercased text and then cutting
// the original. Lowercasing maps rune to rune but not byte to byte — "İ" is two
// bytes and lowercases to one — so a message with enough capital İ before the
// match had its window slide backwards until the match fell outside it.
func TestSnippetSurvivesLowercasingThatChangesByteLength(t *testing.T) {
	text := strings.Repeat("İ ", 400) + "the needle is here"
	if got := Snippet(text, "needle"); !strings.Contains(got, "needle") {
		t.Errorf("excerpt does not contain the match it was cut around: %q", got[:min(60, len(got))])
	}
}

// The ordinary case must keep working: no length-changing runes, match in the
// middle, excerpt centred on it.
func TestSnippetStillCentresOnPlainText(t *testing.T) {
	text := strings.Repeat("filler ", 200) + "the needle is here" + strings.Repeat(" filler", 200)
	if got := Snippet(text, "needle"); !strings.Contains(got, "the needle is here") {
		t.Errorf("excerpt %q lost the match", got)
	}
}

package digest

import "testing"

// A marker fires where it begins a word. As a plain substring it fired inside
// longer words meaning something else, or the opposite (#2734).
func TestMarkersDoNotFireInsideAnotherWord(t *testing.T) {
	inside := []string{
		"Заметки живут в теле релиза, в CHANGELOG.md пустой [Unreleased] блок.",
		"`reviewDecision: REVIEW_REQUIRED` — сам себя одобрить не могу.",
		"Path resolves correctly, so the pollution is in the writer, not the path.",
		"The one genuinely-unfixed item is the NFD form of the accented name.",
		// Both halves of the list, not just the ASCII one: "оказалось" sits
		// inside "показалось", which is the opposite claim.
		"мне показалось, что тест флэйковый, но я не проверял",
	}
	for _, line := range inside {
		if CarriesDecision(line) {
			t.Errorf("a marker fired inside another word: %q", line)
		}
	}

	// The tail stays open: half the markers are Russian verbs, and the
	// inflections are how they are written.
	inflected := []string{
		"решили оставить один индекс на все харнессы",
		"исправили в том же коммите, тест краснеет на откате",
		"вторая нода заработала после смены keepalive",
	}
	for _, line := range inflected {
		if !CarriesDecision(line) {
			t.Errorf("an inflected marker stopped firing: %q", line)
		}
	}
}

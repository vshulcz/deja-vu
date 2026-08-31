package digest

import "testing"

// "теперь" opening a clause starts a plan; in the middle of one it reports a
// state. Which it is was decided by the byte before it, and every byte >= 0x80
// counted as a letter — so Russian punctuation, which is also multi-byte, hid
// the opening and the plan was promoted as an outcome (#2734).
func TestPunctuationDoesNotHideAnOpeningNow(t *testing.T) {
	plans := []string{
		"— Теперь пишу SQL и проверяю выборку.",
		"«Теперь пишу SQL и проверяю выборку».",
		"…Теперь пишу SQL и проверяю выборку.",
	}
	for _, line := range plans {
		if CarriesDecision(line) {
			t.Errorf("a plan promoted as a conclusion: %q", line)
		}
	}

	// Mid-sentence it still reports where something ended up.
	states := []string{
		"прод-пины теперь ложатся на deploy/prod, а не на общий кластер",
		"дамп теперь на двух нодах, обе отвечают",
	}
	for _, line := range states {
		if !CarriesDecision(line) {
			t.Errorf("an outcome read as a plan: %q", line)
		}
	}
}

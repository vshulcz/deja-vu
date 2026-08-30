package digest

import "testing"

// The markers exist to see past status chatter at the few lines that say what a
// session concluded. Two of them were chatter: measured on 85k assistant lines
// from a real store, "зелёный" was the only marker on 543 of 4901 promoted
// lines and "готово" on 221, and reading them they are CI runs and checklist
// ticks (#2734).
func TestGreenCIIsNotAConclusion(t *testing.T) {
	chatter := []string{
		"CI 12/12 зелёный, жду ревьюера.",
		"CI зелёный на #1380 — все проверки прошли.",
		"- ✓ упоминания в вебе — готово",
		"первую попытку я построил копированием готового HOME",
		"«полуготовое задание хуже повторного клика»",
	}
	for _, line := range chatter {
		if CarriesDecision(line) {
			t.Errorf("status chatter promoted as a conclusion: %q", line)
		}
	}

	// What ended up working still counts. "заработал" went in beside those two
	// and stays: of the lines it alone marks, most report an outcome.
	settled := []string{
		"Механизм наконец заработал полностью: настоящие HTTP-запросы, правильные ключи.",
		"Direct SSH к se заработал 3/3 с keepalive-опциями.",
	}
	for _, line := range settled {
		if !CarriesDecision(line) {
			t.Errorf("an outcome read as a passing mention: %q", line)
		}
	}
}

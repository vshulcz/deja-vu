package digest

import "testing"

// The state markers ("теперь", "стало") were added because a decision is as
// often reported as the state something ended up in. The same words open the
// next task, and then the line is a plan. Measured by reading ten lines the
// rule counted as conclusions on a real store, four were plans: "Go-структуры
// готовы. Теперь SQL — 2 SELECT, INSERT, UPDATE" and "PR открыт. Теперь
// главное — сделать так, чтобы деплой не мог соврать".
func TestAPlanIsNotADecision(t *testing.T) {
	for _, line := range []string{
		"Go-структуры готовы. Теперь SQL — 2 SELECT, INSERT, UPDATE + apply-блок",
		"PR открыт. Теперь главное — сделать так, чтобы деплой не мог соврать",
		"теперь нужно прогнать бенч ещё раз",
		"now let's wire the plugin",
		// Mid-sentence plan wording: the clause rule cannot see this one, the
		// phrase list has to.
		"ладно, а теперь давай прогоним бенч",
	} {
		if CarriesDecision(line) {
			t.Errorf("a plan counted as a decision: %q", line)
		}
	}
}

// And a state that really is an outcome still counts, including in the same
// sentence shape.
func TestAStateStillCountsAsADecision(t *testing.T) {
	for _, line := range []string{
		"позиция восстановления теперь точная, дампы 389 МБ ежедневно",
		"PR #810 теперь закрывает #813 и #814",
		"прод-пины теперь ложатся на deploy/prod",
		"the retry budget now lives in config",
	} {
		if !CarriesDecision(line) {
			t.Errorf("an outcome stopped counting as a decision: %q", line)
		}
	}
}

// A line that plans and reports in one breath keeps its decision: the plan
// wording only disqualifies the state marker it sits on.
func TestAPlanBesideAnOutcomeKeepsTheOutcome(t *testing.T) {
	line := "в итоге остановились на 40. теперь давай посмотрим логи"
	if !CarriesDecision(line) {
		t.Errorf("an outcome was lost because a plan followed it: %q", line)
	}
}

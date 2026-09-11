package digest

import "testing"

// A line that opens by saying what is about to happen is a plan, whatever
// decision word sits inside the thing being planned. Counted over 985 distinct
// assistant opening lines from a real store, 54 read as decisions and 7 of them
// were announcements of intent — and a block that leads with one of those is the
// case a model answers "there is no decision on record" to.
func TestAnAnnouncementOfIntentIsNotADecision(t *testing.T) {
	for _, line := range []string{
		"Проверяю, что видео теперь играет дольше трёх секунд.",
		"Проверяю: смотрю, писал ли mpv в терминал раньше, и что теперь этого нет.",
		"I'll look at the git history for the Gemini extension to find the fix.",
		`I'll search the repo for any discussion of a "checkout worker" to see what was actually decided.`,
		"I'm going to stop here rather than give you a diagnosis, because I can't trust the working tree.",
		"Let me check why the exporter retries, then we decide.",
		"Ищу существующий тест на conclusions-блок, добавлю пин на новое поведение.",
	} {
		if CarriesDecision(line) {
			t.Errorf("an announcement of intent reads as a decision: %q", line)
		}
	}
}

// And the outcomes the rule exists for are untouched, including the ones whose
// wording overlaps a plan's.
func TestOutcomesStillCarryTheirDecision(t *testing.T) {
	for _, line := range []string{
		"Both window paths now use event time, so the April root cause is genuinely fixed.",
		"прод-пины теперь ложатся на релиз-ветку, а не на main",
		"we capped the retry budget at three attempts because retrying past that spread the outage",
		"решили оставить single-writer: вторая запись ломала failover",
		"The June 9 finding still holds, and the fix is still in the tree.",
		"оказалось, что таймаут рубил ТСПУ по сигнатуре, а не провайдер",
		// An outcome that happens to name a plan later in the line.
		"Кэш переехал на generation-ключи; теперь давай посмотрим на экспортер.",
	} {
		if !CarriesDecision(line) {
			t.Errorf("an outcome stopped reading as a decision: %q", line)
		}
	}
}

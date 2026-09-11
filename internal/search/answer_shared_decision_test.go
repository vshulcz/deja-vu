package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The "→ " line under a recall hit is the one structured thing the answer adds,
// and it picked its sentence with an English phrase list while every block deja
// injects used the shared recogniser — which knows both languages and tells a
// plan from an outcome. Counted over 1265 distinct assistant lines on a real
// store: the list marks 34, the shared rule 60, and 59 of those are lines the
// list never sees.
func TestTheAnswerLineUsesTheSharedRecogniser(t *testing.T) {
	// Each reply opens with chatter and records its outcome in a later
	// sentence, so picking the sentence and falling through to the whole reply
	// are told apart: the fallback would carry the wanted words too.
	for _, c := range []struct{ reply, want, notWant string }{
		{"Смотрю дальше по логам и жду ответа. Причина в конфиге: `my.aeza.net` не подпадает ни под одно правило.",
			"Причина в конфиге", "Смотрю дальше"},
		{"Проверил оба пути входа и перезагрузил демон. Состояние стало пустым после повторного off.",
			"стало пустым", "Проверил оба пути"},
		{"Долго искал и перебрал три гипотезы. В итоге решили держать single-writer.",
			"решили держать single-writer", "Долго искал"},
		// The English shapes the phrase list holds on its own still work.
		{"Looked at the options and weighed them. We pinned pgx to 5.4.3 for now.",
			"We pinned pgx", "Looked at the options"},
		{"Dug through the handler for a while. Root cause was the stale etag reuse.",
			"Root cause", "Dug through"},
	} {
		got := DecisionText(c.reply)
		if !strings.Contains(got, c.want) {
			t.Errorf("the answer line picked %q, which does not carry %q", got, c.want)
		}
		if strings.Contains(got, c.notWant) {
			t.Errorf("the answer line quoted the whole reply rather than its outcome: %q", got)
		}
	}
}

// And a reply that only announces what it is about to do is not an answer: the
// shared recogniser rejects it, which the phrase list could not see either way.
func TestTheAnswerLineDoesNotQuoteAPlanAsAnOutcome(t *testing.T) {
	msgs := []model.Message{
		{Role: "user", Text: "why does the exporter retry so hard"},
		{Role: "assistant", Text: "Проверяю, что экспортер теперь перестал ретраить без паузы."},
		{Role: "assistant", Text: "Причина найдена: бэкофф считался от нуля, поэтому четвёртая попытка шла сразу."},
	}
	got := AnswerAfter(msgs, 0)
	if strings.Contains(got, "Проверяю") {
		t.Errorf("a plan was attached as the answer: %q", got)
	}
	if !strings.Contains(got, "Причина найдена") {
		t.Errorf("the outcome was not attached: %q", got)
	}
}

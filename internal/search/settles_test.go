package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func quoted(lines []string, want string) bool {
	for _, ln := range lines {
		if strings.Contains(ln, want) {
			return true
		}
	}
	return false
}

// Two slots go to the agent's lines: one for the line with the highest query
// word density, and one for the concluding line that settled the question.
func TestBlockKeepsTheLineThatSettledIt(t *testing.T) {
	// Three lines carry every word of the query; the conclusion carries one of
	// them. By word count alone it would not reach the two slots.
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "что там с pgbouncer"},
		{Role: "assistant", Text: "pgbouncer pool метрики сняли"},
		{Role: "assistant", Text: "pgbouncer pool метрики сравнили"},
		{Role: "assistant", Text: "pgbouncer pool метрики построили"},
		{Role: "assistant", Text: "в итоге pgbouncer остановились на 40"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "pool", "метрики"},
		"что там с pgbouncer")
	if len(lines) == 0 {
		t.Fatal("nothing was quoted at all")
	}
	if !quoted(lines, "остановились на 40") {
		t.Errorf("the line that settled it was dropped for word count: %q", lines)
	}
	if !quoted(lines, "метрики") {
		t.Errorf("the line naming what the session was about was dropped: %q", lines)
	}
}

// The two slots answer different questions: one line says what the session was
// about, the other what it settled, so the line naming the subject keeps its
// slot whatever else is quoted beside it.
func TestBlockKeepsTheSubjectBesideTheConclusion(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "что там с pgbouncer"},
		{Role: "assistant", Text: "в итоге решили: cron переехал на 03:17"},
		{Role: "assistant", Text: "pgbouncer pool держим на 40, pgbouncer перезапущен"},
		{Role: "assistant", Text: "pgbouncer метрики сняли, pgbouncer графики построены"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "решили"}, "что там с pgbouncer")
	if len(lines) == 0 {
		t.Fatal("nothing was quoted at all")
	}
	if !quoted(lines, "pgbouncer") {
		t.Errorf("the subject vanished from the block: %q", lines)
	}
}

// A conclusion rarely repeats all of the question's words: the line that named
// the subject comes first, and the answer follows it immediately.
func TestBlockTakesTheAnswerThatFollowsTheMention(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "что там с pgbouncer"},
		{Role: "assistant", Text: "pgbouncer pool метрики сняли"},
		{Role: "assistant", Text: "в итоге остановились на 40"},
		{Role: "assistant", Text: "pgbouncer pool метрики сравнили"},
		{Role: "assistant", Text: "pgbouncer pool метрики построили"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "pool", "метрики"},
		"что там с pgbouncer")
	if !quoted(lines, "остановились на 40") {
		t.Errorf("the answer following the mention was never a candidate: %q", lines)
	}
}

// It has to follow a line that matched, though: a conclusion from another part
// of the session settles another question.
func TestBlockIgnoresAFarAwayConclusion(t *testing.T) {
	msgs := make([]model.Message, 0, 13)
	msgs = append(msgs, model.Message{Role: "user", Text: "что там с pgbouncer"})
	for range 3 {
		msgs = append(msgs, model.Message{Role: "assistant",
			Text: "pgbouncer pool метрики сняли"})
	}
	for range 8 {
		msgs = append(msgs, model.Message{Role: "assistant",
			Text: "правим совсем другое место"})
	}
	msgs = append(msgs, model.Message{Role: "assistant",
		Text: "в итоге cron переехал на 03:17"})
	_, lines := matchedLinesAsked(model.Session{Messages: msgs},
		[]string{"pgbouncer", "pool", "метрики"},
		"что там с pgbouncer")
	if quoted(lines, "cron") {
		t.Errorf("a conclusion from elsewhere in the session was quoted: %q", lines)
	}
}

// The line taken from beside a match has to be a conclusion. Any next line
// qualifying would fill the second slot with whatever the agent said next.
func TestBlockDoesNotTakeAnOrdinaryNextLine(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "что там с pgbouncer"},
		{Role: "assistant", Text: "pgbouncer pool метрики сняли"},
		{Role: "assistant", Text: "гоню тесты дальше"},
		{Role: "assistant", Text: "pgbouncer pool метрики сравнили"},
		{Role: "assistant", Text: "pgbouncer pool метрики построили"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "pool", "метрики"},
		"что там с pgbouncer")
	if quoted(lines, "гоню тесты") {
		t.Errorf("an ordinary next line took the conclusion slot: %q", lines)
	}
}

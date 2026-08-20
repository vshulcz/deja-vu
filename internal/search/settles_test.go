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

// Two slots go to the agent's lines, and they used to go by word count alone.
// A line repeating the question's words won them from the line that answered
// it, so the block said "looking at pgbouncer" where the session held "in the
// end pgbouncer pool size settled at 40". Measured on a real store: blocks
// carrying a conclusion went from 35 of 100 to 37 with this ordering.
//
// The quoted lines come back in the order they were said, so what is asserted
// here is that the conclusion is among them at all.
func TestBlockKeepsTheLineThatSettledIt(t *testing.T) {
	// Three lines carry every word of the query; the conclusion carries one of
	// them. By word count it cannot reach the two slots, which is how a session
	// that settled something came to be quoted saying anything but that.
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer"},
		{Role: "assistant", Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u0441\u043d\u044f\u043b\u0438"},
		{Role: "assistant", Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u0441\u0440\u0430\u0432\u043d\u0438\u043b\u0438"},
		{Role: "assistant", Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u043f\u043e\u0441\u0442\u0440\u043e\u0438\u043b\u0438"},
		{Role: "assistant", Text: "\u0432 \u0438\u0442\u043e\u0433\u0435 pgbouncer \u043e\u0441\u0442\u0430\u043d\u043e\u0432\u0438\u043b\u0438\u0441\u044c \u043d\u0430 40"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "pool", "\u043c\u0435\u0442\u0440\u0438\u043a\u0438"},
		"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer")
	if len(lines) == 0 {
		t.Fatal("nothing was quoted at all")
	}
	if !quoted(lines, "\u043e\u0441\u0442\u0430\u043d\u043e\u0432\u0438\u043b\u0438\u0441\u044c \u043d\u0430 40") {
		t.Errorf("the line that settled it was dropped for word count: %q", lines)
	}
	if !quoted(lines, "\u043c\u0435\u0442\u0440\u0438\u043a\u0438") {
		t.Errorf("the line naming what the session was about was dropped: %q", lines)
	}
}

// The two slots answer different questions: one line says what the session was
// about, the other what it settled. Filling both by conclusion was measured and
// rejected — it cost an answer of 58 and a block of 142 — so the line naming
// the subject keeps its slot whatever else is quoted beside it.
func TestBlockKeepsTheSubjectBesideTheConclusion(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer"},
		{Role: "assistant", Text: "\u0432 \u0438\u0442\u043e\u0433\u0435 \u0440\u0435\u0448\u0438\u043b\u0438: cron \u043f\u0435\u0440\u0435\u0435\u0445\u0430\u043b \u043d\u0430 03:17"},
		{Role: "assistant", Text: "pgbouncer pool \u0434\u0435\u0440\u0436\u0438\u043c \u043d\u0430 40, pgbouncer \u043f\u0435\u0440\u0435\u0437\u0430\u043f\u0443\u0449\u0435\u043d"},
		{Role: "assistant", Text: "pgbouncer \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u0441\u043d\u044f\u043b\u0438, pgbouncer \u0433\u0440\u0430\u0444\u0438\u043a\u0438 \u043f\u043e\u0441\u0442\u0440\u043e\u0435\u043d\u044b"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "\u0440\u0435\u0448\u0438\u043b\u0438"}, "\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer")
	if len(lines) == 0 {
		t.Fatal("nothing was quoted at all")
	}
	if !quoted(lines, "pgbouncer") {
		t.Errorf("the subject vanished from the block: %q", lines)
	}
}

// A conclusion rarely repeats the question's words: the line that named the
// subject comes first, and the answer follows it saying "in the end we settled
// on 40". Scored line by line, that answer is not a candidate at all. Measured
// on a real store, taking the agent's next lines as candidates for the
// conclusion slot moved blocks carrying a conclusion from 44 of 100 to 49.
func TestBlockTakesTheAnswerThatFollowsTheMention(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer"},
		{Role: "assistant", Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u0441\u043d\u044f\u043b\u0438"},
		{Role: "assistant", Text: "\u0432 \u0438\u0442\u043e\u0433\u0435 \u043e\u0441\u0442\u0430\u043d\u043e\u0432\u0438\u043b\u0438\u0441\u044c \u043d\u0430 40"},
		{Role: "assistant", Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u0441\u0440\u0430\u0432\u043d\u0438\u043b\u0438"},
		{Role: "assistant", Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u043f\u043e\u0441\u0442\u0440\u043e\u0438\u043b\u0438"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "pool", "\u043c\u0435\u0442\u0440\u0438\u043a\u0438"},
		"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer")
	if !quoted(lines, "\u043e\u0441\u0442\u0430\u043d\u043e\u0432\u0438\u043b\u0438\u0441\u044c \u043d\u0430 40") {
		t.Errorf("the answer following the mention was never a candidate: %q", lines)
	}
}

// It has to follow a line that matched, though: a conclusion from another part
// of the session settles another question.
func TestBlockIgnoresAFarAwayConclusion(t *testing.T) {
	msgs := []model.Message{{Role: "user", Text: "\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer"}}
	for i := 0; i < 3; i++ {
		msgs = append(msgs, model.Message{Role: "assistant",
			Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u0441\u043d\u044f\u043b\u0438"})
	}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, model.Message{Role: "assistant",
			Text: "\u043f\u0440\u0430\u0432\u0438\u043c \u0441\u043e\u0432\u0441\u0435\u043c \u0434\u0440\u0443\u0433\u043e\u0435 \u043c\u0435\u0441\u0442\u043e"})
	}
	msgs = append(msgs, model.Message{Role: "assistant",
		Text: "\u0432 \u0438\u0442\u043e\u0433\u0435 cron \u043f\u0435\u0440\u0435\u0435\u0445\u0430\u043b \u043d\u0430 03:17"})
	_, lines := matchedLinesAsked(model.Session{Messages: msgs},
		[]string{"pgbouncer", "pool", "\u043c\u0435\u0442\u0440\u0438\u043a\u0438"},
		"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer")
	if quoted(lines, "cron") {
		t.Errorf("a conclusion from elsewhere in the session was quoted: %q", lines)
	}
}

// The line taken from beside a match has to be a conclusion. Any next line
// qualifying would fill the second slot with whatever the agent said next —
// "running the tests" — which is the noise the slot exists to avoid.
func TestBlockDoesNotTakeAnOrdinaryNextLine(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer"},
		{Role: "assistant", Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u0441\u043d\u044f\u043b\u0438"},
		{Role: "assistant", Text: "\u0433\u043e\u043d\u044e \u0442\u0435\u0441\u0442\u044b \u0434\u0430\u043b\u044c\u0448\u0435"},
		{Role: "assistant", Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u0441\u0440\u0430\u0432\u043d\u0438\u043b\u0438"},
		{Role: "assistant", Text: "pgbouncer pool \u043c\u0435\u0442\u0440\u0438\u043a\u0438 \u043f\u043e\u0441\u0442\u0440\u043e\u0438\u043b\u0438"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "pool", "\u043c\u0435\u0442\u0440\u0438\u043a\u0438"},
		"\u0447\u0442\u043e \u0442\u0430\u043c \u0441 pgbouncer")
	if quoted(lines, "\u0433\u043e\u043d\u044e \u0442\u0435\u0441\u0442\u044b") {
		t.Errorf("an ordinary next line took the conclusion slot: %q", lines)
	}
}

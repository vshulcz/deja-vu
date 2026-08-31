package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An agent run from inside a session has its stdout captured into that
// session's transcript, so the log of the queries it sent lands in an assistant
// message. A later question then matches the record of that question being
// asked — and it outranks the answer, because it repeats every word of the
// query and the answer repeats one (#2067).
func TestTheLogOfAQueryDoesNotAnswerIt(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "какой вариант описания репозитория выбрали"},
		{Role: "assistant", Text: `claude  goprojects/deja-vu  > builder ⚙ deja_recall {"query":"вариант описания репозитория выбрали"}`},
		{Role: "assistant", Text: "в итоге выбрали второй вариант описания — про восстановление контекста"},
	}}
	_, lines := matchedLinesAsked(s, []string{"описания", "репозитория", "вариант"},
		"какой вариант описания репозитория выбрали")
	if len(lines) == 0 {
		t.Fatal("nothing was quoted at all")
	}
	if quoted(lines, "deja_recall") {
		t.Errorf("the record of the question was quoted as its answer: %q", lines)
	}
	if !quoted(lines, "второй вариант") {
		t.Errorf("the line that answered it was not quoted: %q", lines)
	}
}

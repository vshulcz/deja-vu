package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/stats"
)

func TestRepeatQuestionExampleNamesTheMostRepeatedQuestion(t *testing.T) {
	ask := func(text string) model.Session {
		return model.Session{Messages: []model.Message{{Role: "user", Text: text}}}
	}
	ss := []model.Session{
		ask("why does the retry loop drop the last attempt?"),
		ask("Why does the retry loop drop the last attempt"),
		ask("why does the retry loop drop the last attempt?"),
		ask("how do I run the integration tests here?"),
		ask("how do I run the integration tests here"),
		ask("what is the deploy command for staging?"),
	}
	example, n := stats.RepeatQuestionExample(ss)
	if n != 2 {
		t.Fatalf("repeated = %d, want 2", n)
	}
	if !strings.Contains(example, "retry loop") {
		t.Fatalf("example = %q, want the question asked three times", example)
	}
	if _, n := stats.RepeatQuestionExample(ss[5:]); n != 0 {
		t.Fatalf("a question asked once counted as repeated")
	}
}

func TestInstallLeadOpensWithTheRepeatCount(t *testing.T) {
	got := repeatQuestionsText(26, "prepared statement errors behind pgbouncer")
	for _, want := range []string{"26 questions asked more than once", `"prepared statement errors behind pgbouncer"`, "earlier answer first"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lead %q lacks %q", got, want)
		}
	}
	if got := repeatQuestionsText(1, "x"); !strings.HasPrefix(got, "1 question asked") {
		t.Fatalf("singular: %q", got)
	}
	if got := repeatQuestionsText(0, ""); got != "" {
		t.Fatalf("no repeats still spoke: %q", got)
	}
	if got := joinNotesWith("\n  ", "", "b", "", "c"); got != "b\n  c" {
		t.Fatalf("join = %q", got)
	}
}

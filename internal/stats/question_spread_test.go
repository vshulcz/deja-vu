package stats

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The year screen says "N of M questions were asked more than once", so the
// two numbers have to come off one population: the same question twice in one
// session is one question, and what RepeatQuestions refuses to count as a
// question cannot appear in the denominator either.
func TestQuestionSpreadCountsOneQuestionOnce(t *testing.T) {
	q := "why does the scheduler run tasks twice?"
	other := "where does the importer write its watermark?"
	ss := []model.Session{
		{Harness: "claude", Project: "app", ID: "a", Messages: []model.Message{
			{Role: "user", Text: q},
			{Role: "user", Text: q}, // the same question again in one session
			{Role: "assistant", Text: "because the lease is not held?"},
			{Role: "user", Text: "ok?"},         // under the floor
			{Role: "user", Text: "the plan is"}, // no question at all
		}},
		{Harness: "claude", Project: "app", ID: "b", Messages: []model.Message{
			{Role: "user", Text: q},
			{Role: "user", Text: other},
		}},
	}

	distinct, repeated := QuestionSpread(ss)
	if distinct != 2 {
		t.Errorf("distinct questions = %d, want 2 — %q and %q", distinct, q, other)
	}
	if repeated != 1 {
		t.Errorf("repeated questions = %d, want 1 — only %q was asked in both sessions", repeated, q)
	}
	if got := RepeatQuestions(ss); got != repeated {
		t.Errorf("RepeatQuestions says %d and the spread says %d — the screen would show a count out of a smaller total", got, repeated)
	}
}

// Chinese, Japanese and Korean write no whitespace, and the denominator has
// the same floor as the numerator.
func TestQuestionSpreadCountsCJK(t *testing.T) {
	distinct, repeated := QuestionSpread(askedTwice("调度器为什么会重复执行任务？"))
	if distinct != 1 || repeated != 1 {
		t.Errorf("CJK spread = %d distinct, %d repeated; want 1 and 1", distinct, repeated)
	}
	if distinct, repeated = QuestionSpread(askedTwice("好吗？")); distinct != 0 || repeated != 0 {
		t.Errorf("an acknowledgement counted: %d distinct, %d repeated", distinct, repeated)
	}
}

func TestQuestionSpreadIsEmptyWithoutQuestions(t *testing.T) {
	distinct, repeated := QuestionSpread(nil)
	if distinct != 0 || repeated != 0 {
		t.Errorf("no sessions gave %d distinct and %d repeated", distinct, repeated)
	}
}

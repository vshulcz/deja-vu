package stats

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func askedTwice(q string) []model.Session {
	return []model.Session{
		{Harness: "claude", Project: "app", ID: "a", Messages: []model.Message{{Role: "user", Text: q}}},
		{Harness: "claude", Project: "app", ID: "b", Messages: []model.Message{{Role: "user", Text: q}}},
	}
}

// The repeat-questions figure counts questions asked in more than one session.
// It required four whitespace-separated words, and Chinese, Japanese and Korean
// write none, so the figure read zero for anyone working in them.
func TestRepeatQuestionsCountsCJK(t *testing.T) {
	english := RepeatQuestions(askedTwice("why does the scheduler run tasks twice?"))
	if english != 1 {
		t.Fatalf("English repeat questions = %d, want 1 — the probe measured nothing", english)
	}
	if got := RepeatQuestions(askedTwice("调度器为什么会重复执行任务？")); got != english {
		t.Errorf("the same question counts %d times in English and %d in Chinese", english, got)
	}
}

// Short acknowledgements repeat in every session and are not questions, in any
// script.
func TestRepeatQuestionsIgnoresShortAcknowledgements(t *testing.T) {
	for name, q := range map[string]string{
		"english": "ok?",
		"chinese": "好吗？",
	} {
		if got := RepeatQuestions(askedTwice(q)); got != 0 {
			t.Errorf("%s: %q counted as a repeated question %d times", name, q, got)
		}
	}
}

package stats

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"slices"
	"testing"
	"time"
)

// A resume or a fork writes the conversation again under a new session, each
// message keeping the time it was first sent. That copy is not the question
// asked a second time, and it was the bulk of the card's repeat count.
func TestRepeatQuestionsSkipsACopiedConversation(t *testing.T) {
	q := "why does the connection pool exhaust under load?"
	march := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	july := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	session := func(id string, at time.Time) model.Session {
		return model.Session{ID: id, Harness: "claude", Messages: []model.Message{{Role: "user", Text: q, Time: at}}}
	}

	resumed := []model.Session{session("orig", march), session("resume", march)}
	if got := RepeatQuestions(resumed); got != 0 {
		t.Fatalf("a resumed conversation counted as %d repeated question(s)", got)
	}
	if _, repeated := QuestionSpread(resumed); repeated != 0 {
		t.Fatalf("QuestionSpread counted the copy: %d", repeated)
	}

	// Control: the same question asked again months later is the repeat.
	again := slices.Concat(resumed, []model.Session{session("later", july)})
	if got := RepeatQuestions(again); got != 1 {
		t.Fatalf("asked again in July: got %d, want 1", got)
	}
	if _, repeated := QuestionSpread(again); repeated != 1 {
		t.Fatalf("QuestionSpread disagrees with RepeatQuestions: %d", repeated)
	}
}

// A session that asked the question twice is copied with both askings; the
// copy matches on either and still is not a repeat.
func TestRepeatQuestionsSkipsACopyOfATwiceAskedSession(t *testing.T) {
	q := "why does the connection pool exhaust under load?"
	t1 := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	copyOf := func(id string, times ...time.Time) model.Session {
		s := model.Session{ID: id, Harness: "claude"}
		for _, at := range times {
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: q, Time: at})
		}
		return s
	}
	ss := []model.Session{copyOf("orig", t1, t2), copyOf("fork", t2)}
	if got := RepeatQuestions(ss); got != 0 {
		t.Fatalf("a fork holding the second asking counted as %d repeat(s)", got)
	}
}

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

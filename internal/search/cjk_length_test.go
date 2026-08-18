package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func twoSessions(t *testing.T, aside, answer, askedIn, query string) []Hit {
	t.Helper()
	now := time.Now()
	mk := func(id, user, assistant string) model.Session {
		return model.Session{
			Harness: "claude", Project: "app", ID: id, Updated: now,
			Messages: []model.Message{{Role: "user", Text: user}, {Role: "assistant", Text: assistant}},
		}
	}
	hits, err := Run([]model.Session{
		mk("aside", askedIn, aside),
		mk("answer", askedIn, answer),
	}, Options{Query: query})
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

// BM25 normalises by document length so that a long session mentioning a term
// once does not outrank a short one that is about it. Length was counted in
// whitespace-separated words, and Chinese, Japanese and Korean write none, so a
// four-thousand-character aside counted as one word and the normalisation had
// nothing to work with.
func TestLongCJKAsideDoesNotCrowdTheAnswer(t *testing.T) {
	filler := strings.Repeat("这里有很多与问题无关的背景说明和讨论内容", 200)
	hits := twoSessions(t,
		filler+"顺便提一下调度器"+filler,
		"我们把调度器移到了单独的进程",
		"我们来聊聊别的事情", "调度器")
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want both", len(hits))
	}
	if hits[0].Session.ID != "answer" {
		t.Fatalf("top hit is %s, not the session about it", hits[0].Session.ID)
	}
	// The margin is the point, not just the order: before, the aside scored
	// within a third of the answer.
	if ratio := hits[0].Score / hits[1].Score; ratio < 2 {
		t.Errorf("answer scores only %.2fx the passing mention", ratio)
	}
}

// The same shape in English must be unchanged — the fix adds to the count for
// scripts without spaces and must not touch the ones with them.
func TestLongASCIIAsideStillLosesByTheSameMargin(t *testing.T) {
	filler := strings.Repeat("here is a lot of unrelated background discussion and content ", 200)
	hits := twoSessions(t,
		filler+"by the way the scheduler"+filler,
		"we moved the scheduler to its own process",
		"lets talk about something else", "scheduler")
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want both", len(hits))
	}
	if hits[0].Session.ID != "answer" {
		t.Fatalf("top hit is %s, not the session about it", hits[0].Session.ID)
	}
	if ratio := hits[0].Score / hits[1].Score; ratio < 4 {
		t.Errorf("answer scores only %.2fx the passing mention, which is worse than it was", ratio)
	}
}

// Length is words for scripts that write them and characters for the ones that
// do not. Counting characters everywhere would quietly re-scale every English
// document too.
func TestDocumentLengthUnitPerScript(t *testing.T) {
	counts, userCounts := make([]int, 1), make([]int, 1)
	if got := countDocumentWords("we moved the scheduler to its own process", nil, nil, counts, userCounts, false); got != 8 {
		t.Errorf("English length = %d, want its 8 words", got)
	}
	// 我们把调度器移到了单独的进程 is 14 characters and no spaces.
	if got := countDocumentWords("我们把调度器移到了单独的进程", nil, nil, counts, userCounts, false); got != 14 {
		t.Errorf("Chinese length = %d, want its 14 characters", got)
	}
	// A run with both in it is one word plus three characters, not three.
	if got := countDocumentWords("scheduler調度器", nil, nil, counts, userCounts, false); got != 4 {
		t.Errorf("mixed-run length = %d, want 4: the English half counts too", got)
	}
}

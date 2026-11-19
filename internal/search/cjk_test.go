package search

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"strings"
	"testing"
	"time"
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

func cjkSession(now time.Time) model.Session {
	return model.Session{
		Harness: "claude", Project: "app", ID: "s1", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "调度器为什么会重复执行任务"},
			{Role: "assistant", Text: "我们把调度器移到了单独的进程并且把重试次数限制为三次"},
		},
	}
}

// Safe-mode auto-recall skips sessions with too little to say, counted in
// distinct words. Chinese, Japanese and Korean put no separator between words,
// so a whole answer counted as one or two and every such session was judged
// empty and skipped.
func TestCJKSessionReachesAutoRecall(t *testing.T) {
	now := time.Now()
	got := BuildAutoRecall([]model.Session{cjkSession(now)}, AutoRecallOptions{
		Mode: RecallSafe, Now: now, ProjectNames: []string{"app"},
	})
	if got.Sessions == 0 {
		t.Error("a Chinese session never reaches auto-recall")
	}
}

// The bar still has to mean something: a session that really says nothing is
// still skipped, in any script.
func TestEmptySessionIsStillSkipped(t *testing.T) {
	now := time.Now()
	s := model.Session{
		Harness: "claude", Project: "app", ID: "s2", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "好"},
			{Role: "assistant", Text: "好"},
		},
	}
	if got := BuildAutoRecall([]model.Session{s}, AutoRecallOptions{
		Mode: RecallSafe, Now: now, ProjectNames: []string{"app"},
	}); got.Sessions != 0 {
		t.Errorf("a session saying nothing was recalled: %q", got.Text)
	}
}

// Two different Chinese sessions must not read as duplicates of each other:
// the same word set decides that, and counting whole sentences as single words
// made every pair look either identical or unrelated.
func TestTwoCJKSessionsAreNotNearDuplicates(t *testing.T) {
	now := time.Now()
	a := cjkSession(now)
	b := model.Session{
		Harness: "claude", Project: "app", ID: "s3", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "缓存为什么会失效"},
			{Role: "assistant", Text: "我们把缓存的过期时间改成了一个小时并且加了监控"},
		},
	}
	got := BuildAutoRecall([]model.Session{a, b}, AutoRecallOptions{
		Mode: RecallSafe, Now: now, ProjectNames: []string{"app"},
	})
	if got.Sessions != 2 {
		t.Errorf("got %d sessions, want both: %q", got.Sessions, got.Text)
	}
}

// Postings are keyed on folded CJK, so a query in one script reaches a record
// in the other — and the scorer counted on the unfolded pair, so BM25 saw a
// term frequency of zero for a match the postings had already found. The
// cross-script session counted twice and scored nothing, ranking below one that
// matched once (#1605).
func TestACrossScriptMatchScoresLikeAnyOther(t *testing.T) {
	now := time.Now()
	traditional := model.Session{
		Harness: "claude", Project: "app", ID: "trad", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "調度器為什麼會重複執行任務"},
			{Role: "user", Text: "調度器又壞了"},
		},
	}
	simplified := model.Session{
		Harness: "claude", Project: "app", ID: "simp", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "调度器为什么会重复执行任务"},
			{Role: "user", Text: "我们把重试次数限制为三次"},
		},
	}
	// Both directions: whichever side has to be folded is the one that used to
	// score zero.
	for _, q := range []string{"调度器", "調度器"} {
		hits, err := runScored([]model.Session{traditional, simplified}, Options{Query: q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 2 {
			t.Fatalf("%s: %d hits, want both sessions — this measures nothing otherwise", q, len(hits))
		}
		for _, h := range hits {
			if h.Score == 0 {
				t.Errorf("%s: %s counted %d and scored zero", q, h.Session.ID, h.Count)
			}
		}
		// The session that matched twice leads, whichever script the query is
		// written in.
		if hits[0].Session.ID != "trad" {
			t.Errorf("%s: %s leads with count=%d over trad with two matches",
				q, hits[0].Session.ID, hits[0].Count)
		}
	}
}

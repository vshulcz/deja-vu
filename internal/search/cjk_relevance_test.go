package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

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

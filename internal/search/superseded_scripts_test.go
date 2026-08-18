package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A hit is marked as an earlier pass over the same problem when it overlaps
// heavily with a newer session in the same project. Overlap was measured in
// whitespace-separated words, and Chinese, Japanese and Korean write none, so
// two sessions about the same thing shared nothing and the marker never fired.
func TestSupersededMarkerWorksInCJK(t *testing.T) {
	now := time.Now()
	body := "我们把调度器移到了单独的进程并且把重试次数限制为三次这样任务就不会重复执行了"
	older := model.Session{
		Harness: "claude", Project: "app", ID: "older", Updated: now.AddDate(0, 0, -30),
		Messages: []model.Message{{Role: "assistant", Text: body}},
	}
	newer := model.Session{
		Harness: "claude", Project: "app", ID: "newer", Updated: now,
		Messages: []model.Message{{Role: "assistant", Text: body + "后来又调整了一次"}},
	}
	hits, err := Run([]model.Session{older, newer}, Options{Query: "调度器"})
	if err != nil {
		t.Fatal(err)
	}
	marked := false
	for _, h := range hits {
		if h.Session.ID == "older" && h.Superseded != "" {
			marked = true
		}
	}
	if !marked {
		t.Error("the older Chinese session was not marked as an earlier pass")
	}
}

// Two sessions about different things must not be marked, or the label means
// nothing.
func TestSupersededMarkerSkipsUnrelatedCJK(t *testing.T) {
	now := time.Now()
	older := model.Session{
		Harness: "claude", Project: "app", ID: "older", Updated: now.AddDate(0, 0, -30),
		Messages: []model.Message{{Role: "assistant", Text: "我们把调度器移到了单独的进程" + strings.Repeat("并且写了很多说明", 4)}},
	}
	newer := model.Session{
		Harness: "claude", Project: "app", ID: "newer", Updated: now,
		Messages: []model.Message{{Role: "assistant", Text: "调度器的日志格式换成了结构化输出" + strings.Repeat("完全不同的内容在这里", 4)}},
	}
	hits, err := Run([]model.Session{older, newer}, Options{Query: "调度器"})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Session.ID == "older" && h.Superseded != "" {
			t.Error("an unrelated older session was marked as an earlier pass")
		}
	}
}

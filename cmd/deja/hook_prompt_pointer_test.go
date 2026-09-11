package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The pointer's whole content is the topic it names, because the agent has to
// turn that into a recall call. It was built without the query's terms, so
// dejaVuTopic fell through to the session's title: measured against a real
// store, "how is the block" was answered with `"делай задачи в next.md"` — a
// title from a session that merely mentions the subject somewhere.
func TestThePointerNamesWhatWasAskedAbout(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "s1", Project: "org/app",
		Title:   "делай задачи в next.md",
		Updated: time.Now(),
		// The title and the opening line are one thing; the line that matched
		// the question is further down. Without the terms the topic comes from
		// the top of the session, which is the bug.
		Messages: []model.Message{
			{Role: "user", Text: "делай задачи в next.md, там список на сегодня"},
			{Role: "assistant", Text: "открыл список"},
			{Role: "user", Text: "the exporter retry budget keeps hammering the server on failure"},
			{Role: "assistant", Text: "capped it at three attempts"},
		},
	}
	got := weakRecallPointer([]model.Session{s}, []string{"retry", "budget"})
	if !strings.Contains(got, "retry budget") {
		t.Errorf("the pointer does not name what was asked about: %q", got)
	}
	if strings.Contains(got, "next.md") {
		t.Errorf("the pointer named the session's title instead: %q", got)
	}
}

// And it stays a pointer. The matched line is a whole user turn, which on a real
// store reaches a thousand characters: unclipped, the block went from 290 bytes
// to 1234, which is a digest's price for a pointer's content.
func TestThePointerStaysShort(t *testing.T) {
	long := "Новый содержательный разбор — code-grounded review в Commonplace, где хвалят deja-vu " +
		"как низкотрений слой операционного recall и отдельно отмечают редакцию до индексации, " +
		"а ограничения формулируют через лексическое перекрытие поиска."
	s := model.Session{
		Harness: "claude", ID: "s1", Project: "org/app", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: long},
			{Role: "assistant", Text: "прочитал"},
		},
	}
	got := weakRecallPointer([]model.Session{s}, []string{"recall", "разбор"})
	if len(got) > 260 {
		t.Errorf("the pointer is %d bytes, which is a digest's price: %q", len(got), got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("a clipped topic does not say it was clipped: %q", got)
	}
	// Whole words, so the reader can search for what it names.
	if strings.Contains(got, "code-groun\"") || strings.Contains(got, "Commonpl…") {
		t.Errorf("the topic was cut mid-word: %q", got)
	}
}

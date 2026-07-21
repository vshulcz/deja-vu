package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

func TestRenderStatsCardDeterministicAndEscaped(t *testing.T) {
	r := stats.Report{
		TotalSessions: 9,
		TotalMessages: 27,
		Harnesses: []stats.HarnessStats{
			{Harness: "<&>", Sessions: 4}, {Harness: "codex", Sessions: 3},
			{Harness: "a", Sessions: 2},
		},
		TopProjects: []stats.ProjectStats{{Project: "<&>", Sessions: 2}},
		Monthly:     []stats.MonthStats{{Month: "2026-07", Messages: 4}},
		DateRange:   stats.DateRangeStats{Start: "2026-01-01", End: "2026-07-01"},
	}
	one := renderStatsCard(r)
	if one != renderStatsCard(r) {
		t.Fatal("card rendering is not deterministic")
	}
	if strings.Contains(one, "<script") || !strings.Contains(one, "&lt;&amp;&gt;") {
		t.Fatalf("card escaping or script check failed: %s", one)
	}
	decoder := xml.NewDecoder(strings.NewReader(one))
	for {
		if _, err := decoder.Token(); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("card is not XML: %v", err)
		}
	}
	if !strings.Contains(one, `viewBox="0 0 800 420"`) || !strings.Contains(one, ">9</text>") || !strings.Contains(one, ">27</text>") {
		t.Fatalf("card missing fixed layout or hero values")
	}
}

func TestRenderStatsCardHarnessAggregation(t *testing.T) {
	r := stats.Report{Harnesses: []stats.HarnessStats{
		{Harness: "z", Sessions: 1}, {Harness: "y", Sessions: 2}, {Harness: "x", Sessions: 3},
		{Harness: "w", Sessions: 4}, {Harness: "v", Sessions: 5}, {Harness: "u", Sessions: 6}, {Harness: "t", Sessions: 7},
	}}
	card := renderStatsCard(r)
	if !strings.Contains(card, ">other</text>") || !strings.Contains(card, `height="8"`) {
		t.Fatalf("aggregated harness card = %s", card)
	}
}

func TestWriteStatsCardAndStatsFlagConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "card.svg")
	written, err := writeStatsCard(path, stats.Report{Monthly: []stats.MonthStats{{Month: "2026-01"}}})
	if err != nil || written != path {
		t.Fatalf("writeStatsCard = %q, %v", written, err)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.HasPrefix(b, []byte("<?xml")) {
		t.Fatalf("card file = %q, %v", b, err)
	}
	if err := runStats(index.DefaultDir(), []string{"--card", "--json"}); err == nil || !strings.Contains(err.Error(), "choose one output") {
		t.Fatalf("conflict error = %v", err)
	}
	if _, err := writeStatsCard(filepath.Join(path, "nested.svg"), stats.Report{}); err == nil {
		t.Fatal("expected path error")
	}
}

func TestStatsCardCommand(t *testing.T) {
	withStatsStores(t)
	path := filepath.Join(t.TempDir(), "stats.svg")
	out, err := captureRun(t, "stats", "--card", path, "--harness", "claude", "--project", "alpha", "--since", "365000d", "--role", "user")
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(path)
	if !strings.Contains(out, "saved "+abs) || !strings.Contains(out, "![deja]("+filepath.Base(path)+")") {
		t.Fatalf("card output = %q, want saved %q + share snippet", out, abs)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "· agent history") {
		t.Fatalf("card contents = %q, %v", b, err)
	}
}

func TestFilterStatsSessions(t *testing.T) {
	ss := []model.Session{
		{Harness: "claude", Project: "alpha", Messages: []model.Message{{Role: "user"}, {Role: "assistant"}}},
		{Harness: "codex", Project: "beta", Messages: []model.Message{{Role: "user"}}},
	}
	got := stats.Filter(ss, search.Options{Harness: "claude", Role: "user"})
	if len(got) != 1 || len(got[0].Messages) != 1 || got[0].Messages[0].Role != "user" {
		t.Fatalf("filtered sessions = %#v", got)
	}
	if got := stats.Filter(ss, search.Options{Project: "missing", Since: 1}); len(got) != 0 {
		t.Fatalf("missing project filter = %#v", got)
	}
}

func TestStatsHeadlineAndRepeatQuestions(t *testing.T) {
	ss := []model.Session{
		{ID: "one", Messages: []model.Message{{Role: "user", Text: "How do I fix auth?"}, {Role: "user", Text: "How do I fix auth?"}}},
		{ID: "two", Messages: []model.Message{{Role: "user", Text: "how do I fix auth"}}},
		{ID: "three", Messages: []model.Message{{Role: "assistant", Text: "How do I fix auth?"}}},
	}
	if got := stats.RepeatQuestions(ss); got != 1 {
		t.Fatalf("stats.RepeatQuestions = %d, want 1", got)
	}
	if got := statsHeadline(stats.Report{TotalSessions: 1240, RepeatQuestions: 17}); got != "1,240 sessions indexed · 17 questions asked more than once" {
		t.Fatalf("headline = %q", got)
	}
	if got := statsHeadline(stats.Report{}); got != "" {
		t.Fatalf("empty headline = %q", got)
	}
	// Near-repeats with different wording no longer count: the metric is
	// exact-stem only so it stays linear on large corpora.
	if got := stats.RepeatQuestions([]model.Session{{Messages: []model.Message{
		{Role: "user", Text: "<local-command-caveat>"},
		{Role: "user", Text: "   "},
	}}, {Messages: []model.Message{{Role: "user", Text: "fix auth timeout in service now"}}}, {Messages: []model.Message{{Role: "user", Text: "fix auth timeout in service"}}}}); got != 0 {
		t.Fatalf("near-repeat questions = %d, want 0", got)
	}
}

func TestStatsFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--html", "--html"},
		{"--card", "--card"},
		{"--redaction", "--card"},
		{"--harness"},
		{"--project"},
		{"--since"},
		{"--role"},
		{"--since", "not-a-duration"},
		{"--unknown"},
	} {
		if err := runStats(index.DefaultDir(), args); err == nil {
			t.Fatalf("runStats(index.DefaultDir(), %#v) returned nil", args)
		}
	}
}

func TestHandoffsReceivedCountedFromIndex(t *testing.T) {
	ss := []model.Session{
		{ID: "h1", Messages: []model.Message{{Role: "user", Text: "You are picking up work handed off from a claude session (project x)"}}},
		{ID: "n1", Messages: []model.Message{{Role: "user", Text: "ordinary question"}}},
		{ID: "n2", Messages: []model.Message{{Role: "assistant", Text: "picking up work handed off from a"}, {Role: "user", Text: "hi"}}},
	}
	r := stats.Build(ss, time.Now())
	if r.HandoffsIn != 1 {
		t.Fatalf("HandoffsIn = %d, want 1", r.HandoffsIn)
	}
}

package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestRenderStatsCardDeterministicAndEscaped(t *testing.T) {
	r := statsReport{
		TotalSessions: 9,
		TotalMessages: 27,
		Harnesses: []harnessStats{
			{Harness: "<&>", Sessions: 4}, {Harness: "codex", Sessions: 3},
			{Harness: "a", Sessions: 2},
		},
		TopProjects: []projectStats{{Project: "<&>", Sessions: 2}},
		Monthly:     []monthStats{{Month: "2026-07", Messages: 4}},
		DateRange:   dateRangeStats{Start: "2026-01-01", End: "2026-07-01"},
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
	r := statsReport{Harnesses: []harnessStats{
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
	written, err := writeStatsCard(path, statsReport{Monthly: []monthStats{{Month: "2026-01"}}})
	if err != nil || written != path {
		t.Fatalf("writeStatsCard = %q, %v", written, err)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.HasPrefix(b, []byte("<?xml")) {
		t.Fatalf("card file = %q, %v", b, err)
	}
	if err := runStats([]string{"--card", "--json"}); err == nil || !strings.Contains(err.Error(), "choose one output") {
		t.Fatalf("conflict error = %v", err)
	}
	if _, err := writeStatsCard(filepath.Join(path, "nested.svg"), statsReport{}); err == nil {
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
	if strings.TrimSpace(out) != abs {
		t.Fatalf("card output = %q, want %q", out, abs)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), ">deja</text>") {
		t.Fatalf("card contents = %q, %v", b, err)
	}
}

func TestFilterStatsSessions(t *testing.T) {
	ss := []model.Session{
		{Harness: "claude", Project: "alpha", Messages: []model.Message{{Role: "user"}, {Role: "assistant"}}},
		{Harness: "codex", Project: "beta", Messages: []model.Message{{Role: "user"}}},
	}
	got := filterStatsSessions(ss, search.Options{Harness: "claude", Role: "user"})
	if len(got) != 1 || len(got[0].Messages) != 1 || got[0].Messages[0].Role != "user" {
		t.Fatalf("filtered sessions = %#v", got)
	}
	if got := filterStatsSessions(ss, search.Options{Project: "missing", Since: 1}); len(got) != 0 {
		t.Fatalf("missing project filter = %#v", got)
	}
}

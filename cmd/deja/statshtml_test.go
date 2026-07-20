package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

func TestStatsHTMLMetadataEscapingAndPrivacy(t *testing.T) {
	report := statsReport{TotalSessions: 1, TotalMessages: 2, Harnesses: []harnessStats{{Harness: "claude"}}, DateRange: dateRangeStats{Start: "2026-01-01", End: "2026-01-02"}, Monthly: []monthStats{{Month: "2026-01", Messages: 2}}}
	sessions := []model.Session{{Harness: "claude", Project: `<script>alert(1)</script>`, Title: "redacted title", Updated: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Messages: []model.Message{{Role: "user", Text: "secret message body"}}}}
	page, err := newStatsHTMLPage(report, sessions)
	if err != nil {
		t.Fatal(err)
	}
	var rows []htmlSession
	if err := json.Unmarshal([]byte(page.SessionsJSON), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("metadata JSON=%q err=%v", page.SessionsJSON, err)
	}
	if rows[0].Project != `<script>alert(1)</script>` || rows[0].Title != "redacted title" || strings.Contains(string(page.SessionsJSON), "secret message body") {
		t.Fatalf("metadata privacy failed: %q", page.SessionsJSON)
	}
	var rendered strings.Builder
	if err := statsHTMLTemplate.Execute(&rendered, page); err != nil {
		t.Fatal(err)
	}
	output := rendered.String()
	if strings.Contains(output, "<script>alert(1)</script>") || !strings.Contains(output, `\u003cscript\u003e`) || strings.Contains(output, "http://") || strings.Contains(output, "https://") {
		t.Fatalf("HTML escaping/network check failed: %s", output)
	}
	if !strings.Contains(output, "1") || !strings.Contains(output, "2") || !strings.Contains(output, "metadata-only") {
		t.Fatalf("HTML totals/privacy note missing: %s", output)
	}
}

func TestStatsHTMLCapAndWrite(t *testing.T) {
	sessions := make([]model.Session, statsHTMLCap+1)
	for i := range sessions {
		sessions[i] = model.Session{Harness: "h", Project: "p", Updated: time.Date(2020, 1, 1, 0, 0, i, 0, time.UTC)}
	}
	page, err := newStatsHTMLPage(statsReport{}, sessions)
	if err != nil || !page.Truncated || page.SessionCount != statsHTMLCap {
		t.Fatalf("cap page=%#v err=%v", page, err)
	}
	path := filepath.Join(t.TempDir(), "timeline.html")
	written, err := writeStatsHTML(path, statsReport{}, sessions[:1])
	if err != nil || written != path {
		t.Fatalf("write HTML=%q err=%v", written, err)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.HasPrefix(string(b), "<!doctype html>") {
		t.Fatalf("HTML file err=%v content=%q", err, b)
	}
	if _, err := writeStatsHTML(filepath.Join(path, "nested.html"), statsReport{}, nil); err == nil {
		t.Fatal("expected path error")
	}
}

func TestStatsHTMLHelpers(t *testing.T) {
	months := []monthStats{{Messages: 0}, {Messages: 10}}
	if barHeight(0, months) != 4 || barHeight(5, months) != 56 || monthShort("2026-02") != "02" || monthShort("x") != "x" {
		t.Fatal("unexpected HTML helper output")
	}
}

func TestStatsHTMLCommandAndConflicts(t *testing.T) {
	withStatsStores(t)
	path := filepath.Join(t.TempDir(), "timeline.html")
	out, err := captureRun(t, "stats", "--html", path, "--harness", "claude")
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(path)
	if strings.TrimSpace(out) != abs {
		t.Fatalf("HTML output=%q want=%q", out, abs)
	}
	if err := runStats(index.DefaultDir(), []string{"--html", "--json"}); err == nil || !strings.Contains(err.Error(), "choose one output") {
		t.Fatal("expected HTML/JSON conflict")
	}
	if err := runStats(index.DefaultDir(), []string{"--html", "--card"}); err == nil || !strings.Contains(err.Error(), "choose one output") {
		t.Fatal("expected HTML/card conflict")
	}
	for _, args := range [][]string{{"--html", "--html"}, {"--unknown"}} {
		if err := runStats(index.DefaultDir(), args); err == nil {
			t.Fatalf("runStats(index.DefaultDir(), %v) accepted invalid arguments", args)
		}
	}
}

func TestStatsHTMLEdgeBranches(t *testing.T) {
	tmp := t.TempDir()
	// A session with only a start time falls back to it for date and sorting.
	started := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	sessions := []model.Session{
		{ID: "b", Harness: "claude", Started: started, Messages: []model.Message{{Role: "user", Text: "hello"}}},
		{ID: "a", Harness: "claude", Started: started},
	}
	page, err := newStatsHTMLPage(statsReport{}, sessions)
	if err != nil {
		t.Fatal(err)
	}
	if page.SessionCount != 2 || !strings.Contains(string(page.SessionsJSON), "2026-02-03") || !strings.Contains(string(page.SessionsJSON), `"-"`) {
		t.Fatalf("page json=%s", page.SessionsJSON)
	}
	// Write failure: parent path squatted by a file.
	squat := filepath.Join(tmp, "f")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeStatsHTML(filepath.Join(squat, "out.html"), statsReport{}, nil); err == nil {
		t.Fatal("expected write error")
	}
}

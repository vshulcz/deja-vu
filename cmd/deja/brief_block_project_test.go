package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// One block, one project: the lines differ only by their date, and a date
// carrying its year used to cut the project further on the lines that had one
// — or take it away entirely — because each line budgeted on its own (#2128).
func TestBriefRecentBlockShowsOneProjectSpelling(t *testing.T) {
	dir := briefWideProjectAgesStore(t)
	t.Setenv("COLUMNS", "60")

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	lines := recentLines(buf.String())
	if len(lines) < 3 {
		t.Fatalf("want three recent lines, got %d:\n%s", len(lines), buf.String())
	}
	var spellings, dates []string
	for _, l := range lines {
		project, date := briefLineFields(t, visibleText(l))
		spellings = append(spellings, project)
		dates = append(dates, date)
	}
	// The premise: the dates are not all the same width, so a per-line budget
	// has something to differ about. Without this the test passes on any day
	// the fixture's ages happen to render alike.
	if termwidth.Columns(dates[0]) == termwidth.Columns(dates[len(dates)-1]) {
		t.Fatalf("every date is the same width %q, so this measures nothing:\n%s", dates, buf.String())
	}
	for _, s := range spellings[1:] {
		if s != spellings[0] {
			t.Errorf("one project, %d spellings in one block: %q\n%s", len(spellings), spellings, buf.String())
			break
		}
	}
	// The block still fits, and shortening did not take the project with it —
	// the budget that binds here leaves room for a tail (#1592).
	for _, l := range lines {
		if w := termwidth.Columns(visibleText(l)); w > 60 {
			t.Errorf("%d columns: %q", w, visibleText(l))
		}
	}
	if spellings[0] == "" {
		t.Errorf("every line dropped the project, so this measures nothing:\n%s", buf.String())
	}
}

// briefLineFields returns what a recent line prints for the project and the
// date. The project is empty on a line that dropped it, and the date is the
// field the title follows.
func briefLineFields(t *testing.T, text string) (project, date string) {
	t.Helper()
	i := strings.Index(text, "] ")
	if i < 0 {
		t.Fatalf("no harness on a recent line: %q", text)
	}
	// Harness, project and date are separated by " · ", and the title follows
	// the date. Two fields before the title mean a project is there. The title
	// may hold a separator of its own, so only the first two are split off.
	parts := strings.SplitN(text[i+2:], " · ", 3)
	switch len(parts) {
	case 0, 1:
		t.Fatalf("no date on a recent line: %q", text)
		return "", ""
	case 2:
		return "", parts[0]
	default:
		return parts[0], parts[1]
	}
}

// A wide project and three ages that render three ways whatever day it is: a
// session from today, and two old enough to carry a year.
func briefWideProjectAgesStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-work-数据平台-消费者重平衡")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	const question = "为什么 kafka 消费者在预发环境不断重新平衡，日志里全是 rebalance 超时"
	day := 24 * time.Hour
	for i, age := range []time.Duration{2 * time.Hour, 400 * day, 380 * day} {
		sid := string(rune('a'+i)) + "1b2c3d4"
		body, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/work/数据平台/消费者重平衡",
			"timestamp": time.Now().Add(-age).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": question},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), append(body, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

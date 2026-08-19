package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// briefCJKStore is briefStore with Chinese first messages, so the titles the
// `recent` lines carry are twice as wide as they are long.
func briefCJKStore(t *testing.T, ages ...time.Duration) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-w-支付服务")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	titles := []string{
		"重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开彼此的唤醒时间",
		"支付回调重复投递导致订单状态机反复回退需要幂等键来兜底整个链路的写入",
		"消费者重平衡在预发环境反复抖动需要把会话超时和心跳间隔一起调大才稳定",
	}
	for i, age := range ages {
		sid := string(rune('a'+i)) + "1b2c3d4"
		at := time.Now().Add(-age).UTC().Format(time.RFC3339)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/支付服务","timestamp":"` + at +
			`","message":{"role":"user","content":"` + titles[i%len(titles)] + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	return index.DefaultDir()
}

// #1073 budgeted the title against what is already on the line so the `recent`
// lines stop wrapping. The budget is columns and the cut counted runes, so a
// Chinese title of 35 characters passed a 44-rune cap and printed 110 columns
// on an 80-column terminal — and did not shrink when the terminal did, because
// the rune count was under the cap at every width.
func TestBriefRecentLinesFitWithCJKTitles(t *testing.T) {
	day := 24 * time.Hour
	dir := briefCJKStore(t, 12*day, 15*day, 19*day)

	for _, width := range []int{60, 80, 100, 120} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		var buf bytes.Buffer
		if err := runBrief(dir, &buf); err != nil {
			t.Fatal(err)
		}
		lines := recentLines(buf.String())
		if len(lines) < 2 {
			t.Fatalf("want several recent lines, got %d:\n%s", len(lines), buf.String())
		}
		for _, l := range lines {
			if w := termwidth.Columns(visibleText(l)); w > width {
				t.Errorf("COLUMNS=%d: line is %d columns: %q", width, w, visibleText(l))
			}
		}
	}
}

// The lines still grow with the terminal — a fixed conservative cap would pass
// the test above and show the same stub at every width.
func TestBriefCJKTitlesGrowWithTheTerminal(t *testing.T) {
	day := 24 * time.Hour
	dir := briefCJKStore(t, 12*day, 15*day, 19*day)

	width := func(cols string) int {
		t.Setenv("COLUMNS", cols)
		var buf bytes.Buffer
		if err := runBrief(dir, &buf); err != nil {
			t.Fatal(err)
		}
		lines := recentLines(buf.String())
		if len(lines) == 0 {
			t.Fatalf("no recent lines at COLUMNS=%s:\n%s", cols, buf.String())
		}
		return termwidth.Columns(visibleText(lines[0]))
	}
	narrow, wide := width("60"), width("120")
	if wide <= narrow {
		t.Errorf("the line did not grow with the terminal: 60 gave %d, 120 gave %d", narrow, wide)
	}
}

// The cut itself, at the three shapes a title takes.
func TestTrimBriefTitleToBoundsColumns(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		max  int
	}{
		{"latin", "the retry queue stalls on staging and all the workers wake together", 44},
		{"chinese", "重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开彼此的唤醒时间", 44},
		{"mixed", "重试队列 stalls on staging 工作进程同时醒来 needs jitter to spread them", 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := trimBriefTitleTo(tc.in, tc.max)
			// The ellipsis is added past the budget, as it always was.
			if w := termwidth.Columns(strings.TrimSuffix(got, "…")); w > tc.max {
				t.Errorf("trimBriefTitleTo(%q, %d) prints %d columns: %q", tc.in, tc.max, w, got)
			}
			if !strings.HasPrefix(tc.in, strings.TrimSuffix(got, "…")) {
				t.Errorf("the cut is not a prefix of the title: %q", got)
			}
		})
	}
	// A title inside the budget is returned whole, without a mark claiming
	// something was dropped.
	short := "重试队列卡住了"
	if got := trimBriefTitleTo(short, 44); got != short {
		t.Errorf("a title inside the budget was cut: %q", got)
	}
}

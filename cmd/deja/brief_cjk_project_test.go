package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// A project name is the part of a `recent` prefix that grows without bound, and
// a CJK one is two columns per character. At COLUMNS=60 the prefix alone was 58
// columns, the title budget fell to its 12-column floor, and the line ended at
// 71 — a wrapped row, not a ragged end (#1592).
func TestBriefRecentLinesShortenAWideProject(t *testing.T) {
	dir := briefCJKProjectStore(t)

	for _, width := range []int{60, 80} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		var buf bytes.Buffer
		if err := runBrief(dir, &buf); err != nil {
			t.Fatal(err)
		}
		lines := recentLines(buf.String())
		if len(lines) == 0 {
			t.Fatalf("COLUMNS=%d: no recent lines:\n%s", width, buf.String())
		}
		for _, l := range lines {
			text := visibleText(l)
			if w := termwidth.Columns(text); w > width {
				t.Errorf("COLUMNS=%d: %d columns: %q", width, w, text)
			}
			// Shortening must not take the whole title with it: the line is
			// there to name the work, and an empty column names nothing.
			if i := strings.LastIndex(text, " · "); i < 0 || termwidth.Columns(text[i+3:]) < 8 {
				t.Errorf("COLUMNS=%d: nothing readable left of the title: %q", width, text)
			}
		}
	}
}

func briefCJKProjectStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-work-数据平台-消费者重平衡")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	const question = "为什么 kafka 消费者在预发环境不断重新平衡，日志里全是 rebalance 超时"
	for i, age := range []time.Duration{400 * 24 * time.Hour, 380 * 24 * time.Hour, 12 * 24 * time.Hour} {
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

// The longest harness name and a date carrying its year leave 42 columns of a
// 60-column pane, and the two floors — eight for a path tail, twelve for a
// title — do not fit in the remaining eighteen. Review found that combination
// still printing 62–63 columns, so the project goes rather than the line
// running past the edge with an unreadable stub of a path on it (#1592).
func TestBriefProjectGoesWhenTheFloorsCannotFit(t *testing.T) {
	t.Setenv("COLUMNS", "60")

	// `recent    ` + ` [antigravity] ` + ` · ` + `Jul 20 2025` + ` · `
	const restWithLongHarness = 42
	if got := fitBriefProject("work/数据平台/消费者重平衡", restWithLongHarness); got != "" {
		t.Errorf("project = %q, want it dropped: 60 - 42 - 12 - 1 leaves five columns", got)
	}

	// With an ordinary harness there is room, and the tail is what survives.
	const restWithClaude = 36
	got := fitBriefProject("work/数据平台/消费者重平衡", restWithClaude)
	if got == "" {
		t.Fatal("project dropped when there was room for it")
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "重平衡") {
		t.Errorf("project = %q, want the tail of the path", got)
	}
	if w := termwidth.Columns(got); restWithClaude+w+briefRecentTitleFloor+1 > 60 {
		t.Errorf("project = %q (%d columns): the line would still overflow", got, w)
	}

	// A project that already fits is untouched, whatever the floors say.
	if got := fitBriefProject("api", restWithLongHarness); got != "api" {
		t.Errorf("project = %q, want it left alone", got)
	}
}

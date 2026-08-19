package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// seedCJKTitleIndex is seedLongTitleIndex with Chinese titles: same shape, same
// shared file, a title long enough to be trimmed on any bar.
func seedCJKTitleIndex(t *testing.T, titles []string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-cjk")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i, title := range titles {
		sid := fmt.Sprintf("cj%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/cj","timestamp":"2026-07-1` +
			fmt.Sprint(i) + `T10:00:00Z","message":{"role":"user","content":"` + title + `"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w/cj","timestamp":"2026-07-1` +
			fmt.Sprint(i) + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/cj/retry.go"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

var cjkTitles = []string{
	"重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开",
	"重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开二",
	"重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开三",
}

// #1076 bounded the bar in runes, which is the same thing as columns only for
// Latin text. A Chinese title is one rune and two columns per character, so
// the 38-rune cap fit an 80-column bar on paper and printed 98 columns — the
// memory segment ran off the edge exactly as it did before that fix.
func TestStatuslineFitsTheBarWithCJKTitle(t *testing.T) {
	dir := seedCJKTitleIndex(t, cjkTitles)
	// 82 is where the usage numbers start to fit by rune count and not by
	// column count: the segment before them is 58 runes and 77 columns.
	for _, width := range []int{60, 80, 82, 120} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		line := statuslineWith(t, dir, "/anywhere/cj01.jsonl")
		if got := termwidth.Columns(line); got > width {
			t.Errorf("%d-column bar: line prints %d columns: %q", width, got, line)
		}
		// The segment has to arrive whole; a title the terminal cuts loses its
		// closing quote, which is the visible half of #1076.
		if !strings.Contains(line, "”") {
			t.Errorf("%d-column bar: the memory segment lost its closing quote: %q", width, line)
		}
	}
}

// The bar still grows with the terminal — a fixed conservative cap would pass
// the test above and show the same stub on every width.
func TestStatuslineCJKTitleGrowsWithTheBar(t *testing.T) {
	dir := seedCJKTitleIndex(t, cjkTitles)

	t.Setenv("COLUMNS", "60")
	narrow := statuslineWith(t, dir, "/anywhere/cj01.jsonl")
	t.Setenv("COLUMNS", "120")
	wide := statuslineWith(t, dir, "/anywhere/cj01.jsonl")

	if termwidth.Columns(wide) <= termwidth.Columns(narrow) {
		t.Errorf("the line did not grow with the bar: narrow=%d wide=%d",
			termwidth.Columns(narrow), termwidth.Columns(wide))
	}
	// And the trim is real on the narrow bar: the whole title is 28 characters,
	// so a 60-column bar cannot be showing all of it.
	if strings.Contains(narrow, "错开") {
		t.Errorf("60-column bar shows the untrimmed title: %q", narrow)
	}
}

// safeForStatusline is what bounds the title, and its bound is what changed.
func TestSafeForStatuslineBoundsColumns(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		max  int
		want int
	}{
		{"latin under the cap", "retry queue stalls", 38, 18},
		// One short of the cap plus the ellipsis: the cut landed on a space,
		// which is trimmed before the mark is added.
		{"latin over the cap", "the retry queue stalls on staging and the workers wake together", 38, 38},
		{"chinese over the cap", "重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开", 38, 39},
		{"chinese exactly at the cap", "重试队列在预发环境卡住了工作进程同", 34, 34},
		{"kana over the cap", "リトライキューがステージングで詰まりワーカーが同時に起きる", 20, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := termwidth.Columns(safeForStatusline(tc.in, tc.max))
			if got != tc.want {
				t.Errorf("safeForStatusline(%q, %d) prints %d columns, want %d (%q)",
					tc.in, tc.max, got, tc.want, safeForStatusline(tc.in, tc.max))
			}
		})
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// The path column is a column, and a path under a Chinese directory is one
// rune and two columns per character. Counting runes let a 27-rune path fill
// 46 of a 30-column budget, so the counts beside it landed wherever the text
// ended (#604 budgeted the column; this is the same column measured right).
func TestTrimPathToBoundsColumns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		width int
	}{
		{"latin", "…/very/long/directory/name/retry_backoff_handler.go", 30},
		{"chinese", "…/项目/内部服务/重试队列/退避处理器实现文件.go", 30},
		{"mixed", "…/项目/src/内部服务/queue/退避处理器.go", 24},
		{"narrow", "…/项目/内部服务/重试队列/退避处理器实现文件.go", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := trimPathTo(tc.in, tc.width)
			if n := termwidth.Columns(got); n > tc.width {
				t.Errorf("trimPathTo(%q, %d) prints %d columns: %q", tc.in, tc.width, n, got)
			}
			// What comes back has to be a literal tail of the input, mark
			// aside: a cut through a wide character would leave text that is
			// the right width and not the same path.
			if tail := strings.TrimPrefix(got, "…"); !strings.HasSuffix(tc.in, tail) {
				t.Errorf("trimPathTo(%q, %d) = %q, which is not a tail of it", tc.in, tc.width, got)
			}
		})
	}
}

// A path that already fits is not touched, and a wide character is never cut
// in half to reach the budget exactly.
func TestTrimPathToLeavesWhatFits(t *testing.T) {
	short := "…/项目/退避.go"
	if got := trimPathTo(short, 30); got != short {
		t.Errorf("a path inside the budget was trimmed: %q", got)
	}
	// 12 columns of a path whose characters are two columns wide: the cut has
	// to stop at 12, not at 13 with half a character.
	got := trimPathTo("…/项目/内部服务/重试队列/退避处理器.go", 12)
	if n := termwidth.Columns(got); n > 12 {
		t.Errorf("cut to %d columns: %q", n, got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("the cut is not marked: %q", got)
	}
}

// End to end: every row of the table has to end its path in the same column,
// or the counts do not read as a column at all.
func TestFilesTableAlignsCJKPaths(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, rel := range []string{
		"项目/内部服务/重试队列/退避处理器实现文件.go",
		"项目/内部服务/重试队列/工作进程唤醒抖动逻辑.go",
		"src/app/internal/queue/retry_backoff_handler.go",
	} {
		full := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, full)
	}

	root := filepath.Join(tmp, "claude", "proj-cj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	var body strings.Builder
	body.WriteString(`{"type":"user","sessionId":"c1","cwd":` + jsonString(repo) + `,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the retry storm on checkout"}}` + "\n")
	for _, p := range paths {
		body.WriteString(`{"type":"assistant","sessionId":"c1","cwd":` + jsonString(repo) + `,"timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":` + jsonString(p) + `}}]}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(root, "c1.jsonl"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := runFiles(index.DefaultDir(), []string{"retry"}, &buf); err != nil {
		t.Fatal(err)
	}

	var widths []int
	var rows []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		rows = append(rows, line)
		widths = append(widths, termwidth.Columns(line))
	}
	if len(rows) != 3 {
		t.Fatalf("wrong fixture: %d rows in %q", len(rows), buf.String())
	}
	// One of them has to be a CJK path, or the test is about Latin alignment.
	if !strings.Contains(strings.Join(rows, ""), "项目") {
		t.Fatalf("no CJK row in the table: %q", rows)
	}
	// And each row still names a real file: padding to a column budget must
	// not be reached by cutting through a character.
	for _, row := range rows {
		shown := strings.TrimPrefix(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(row), "1")), "…")
		var named bool
		for _, p := range paths {
			if strings.HasSuffix(filepath.ToSlash(p), shown) {
				named = true
				break
			}
		}
		if !named {
			t.Errorf("row names no seeded file: %q", row)
		}
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("row %d is %d columns, row 0 is %d:\n%s", i, w, widths[0], strings.Join(rows, "\n"))
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// halvedStore holds ten sessions deja may serve and twenty the ignore rule
// keeps out, on two different days so a date range gives them away.
func halvedStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	if err := os.MkdirAll(filepath.Dir(policy.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy.Path(), []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	write := func(dir, id, day string) {
		writeClaudeFixture(t, filepath.Join(root, dir, id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/` + strings.TrimPrefix(dir, "-w-") + `","timestamp":"2026-07-` + day + `T10:00:00Z","message":{"role":"user","content":"the widget pipeline keeps stalling"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/` + strings.TrimPrefix(dir, "-w-") + `","timestamp":"2026-07-` + day + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/x/widget-` + id + `.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	for i := 0; i < 10; i++ {
		write("-w-keep", "k"+string(rune('a'+i)), "01")
	}
	for i := 0; i < 20; i++ {
		write("-w-scratch", "s"+string(rune('a'+i)), "02")
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// The brief is the first screen anyone sees, and it counted the whole manifest
// while its own `recent` rows three lines below showed only what recall serves
// — and dated its range to a day no servable session was worked on (#2650).
func TestBriefCountsWhatRecallCanServe(t *testing.T) {
	halvedStore(t)
	out, err := captureRun(t, "brief")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "30 session") {
		t.Fatalf("the brief counts sessions the ignore rule keeps out:\n%s", out)
	}
	if !strings.Contains(out, "10 session") {
		t.Fatalf("the brief does not count what recall can serve:\n%s", out)
	}
	if strings.Contains(out, "Jul 2 2026") {
		t.Fatalf("the range covers a day only ignored sessions were worked on:\n%s", out)
	}
}

// The same rule for the span count, which sat on a screen already reporting the
// filtered session count one line above.
func TestStatsCountsSpansItCouldRestore(t *testing.T) {
	halvedStore(t)
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "10 sessions indexed") {
		t.Fatalf("the fixture changed; this pins nothing:\n%s", out)
	}
	if strings.Contains(out, "30 spans") {
		t.Fatalf("the span count includes the tree the rule keeps out:\n%s", out)
	}
	if !strings.Contains(out, "10 spans") {
		t.Fatalf("the span count is not what recall can serve:\n%s", out)
	}
}

// doctor reports what the index holds, which is the whole store — that number
// is right there and stays.
func TestDoctorStillCountsTheWholeStore(t *testing.T) {
	halvedStore(t)
	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"indexed_sessions": 30`) && !strings.Contains(out, `"indexed_sessions":30`) {
		t.Fatalf("doctor should still report every indexed session:\n%s", out)
	}
}

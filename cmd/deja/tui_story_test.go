package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A session with a few turns, two commands and an edit: the preview tells how
// it went under the answer, with the commands marked by how they ended.
func TestTUIPreviewTellsHowTheSessionWent(t *testing.T) {
	withTempStores(t)
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("PATH", "")
	const id = "d4444444-sched"
	var lines []string
	add := func(typ string, content any) {
		b, _ := json.Marshal(map[string]any{"type": typ, "sessionId": id, "cwd": "/work/payments",
			"timestamp": "2026-03-03T10:00:00Z", "message": map[string]any{"role": typ, "content": content}})
		lines = append(lines, string(b))
	}
	text := func(s string) []map[string]any { return []map[string]any{{"type": "text", "text": s}} }
	tool := func(id, cmd, out string, failed bool) {
		add("assistant", []map[string]any{{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": cmd}}})
		add("user", []map[string]any{{"type": "tool_result", "tool_use_id": id, "is_error": failed, "content": out}})
	}
	add("user", "the scheduler times out under load")
	add("assistant", text("Traced it to a context cancelled one layer too early. Fixed by passing the parent context through."))
	tool("t1", "go test -race ./internal/scheduler/...", "Exit code 1\n--- FAIL: TestLoad", true)
	add("user", "still seeing it after passing the parent context through")
	add("assistant", text("The proxy closes idle connections first. Lowering the keepalive under the proxy timeout fixes it."))
	add("assistant", []map[string]any{{"type": "tool_use", "id": "e1", "name": "Edit",
		"input": map[string]any{"file_path": "/work/payments/internal/pool/pool.go", "old_string": "30 * time.Minute", "new_string": "4 * time.Minute"}}})
	tool("t2", "go test ./...", "ok  payments 1.2s", false)
	add("user", "and the dashboards should show it as well please")
	add("assistant", text("Added a pool gauge to the scheduler dashboard so a closed connection shows up."))
	writeClaudeFixture(t, filepath.Join(root, "payments", id+".jsonl"), id, lines)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	a := newTestTUI(t, dir)
	if len(a.rows) != 1 {
		t.Fatalf("rows = %+v", a.rows)
	}
	st := a.details[sessionKey(a.rows[0].s)].story
	if len(st.runs) != 2 || st.runs[0].state != runFailed || st.runs[1].state != runOK || st.failed != 1 {
		t.Errorf("runs = %+v", st.runs)
	}
	if !reflect.DeepEqual(st.edited, []string{"internal/pool/pool.go"}) {
		t.Errorf("edited = %q", st.edited)
	}
	s := screen(a.frame(140, 44))
	wantOnScreen(t, s, "HOW IT WENT", "› still seeing it after passing", "The proxy closes idle connections",
		"RAN", "2 commands · 1 failed", "✗ go test -race", "✓ go test ./...",
		"EDITED", "internal/pool/pool.go", "Read")
	if strings.Contains(s, "TOUCHED") {
		t.Errorf("the edited list replaces the touched line:\n%s", s)
	}
	// Short, the story gives way and the buttons stay.
	s = screen(a.frame(140, 22))
	wantOnScreen(t, s, "ASKED", "Read")
	if strings.Contains(s, "EDITED") && !strings.Contains(s, "internal/pool") {
		t.Errorf("a heading without its lines:\n%s", s)
	}
}

func TestTUIStoryLayoutHelpers(t *testing.T) {
	if got := storyShare(20, []int{10, 2, 1}); !reflect.DeepEqual(got, []int{10, 2, 1}) {
		t.Errorf("room for all: %v", got)
	}
	// Tight, every section gets a line before any gets a second.
	if got := storyShare(8, []int{10, 4, 4}); !reflect.DeepEqual(got, []int{1, 1, 1}) {
		t.Errorf("tight share = %v", got)
	}
	if got := storyShare(1, []int{3}); got[0] != 0 {
		t.Errorf("no room for a heading and a line: %v", got)
	}
	if got := elide(10, 5); !reflect.DeepEqual(got, []int{0, -1, 7, 8, 9}) {
		t.Errorf("elide = %v", got)
	}
	if got := elide(3, 5); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Errorf("elide short = %v", got)
	}
	if got := elide(9, 2); !reflect.DeepEqual(got, []int{7, 8}) {
		t.Errorf("elide two = %v", got)
	}
	if got := projectRelative("/work/payments/internal/pool.go", "payments"); got != "internal/pool.go" {
		t.Errorf("projectRelative = %q", got)
	}
	if got := projectRelative("/else/x.go", "payments"); got != "/else/x.go" {
		t.Errorf("projectRelative outside = %q", got)
	}
}

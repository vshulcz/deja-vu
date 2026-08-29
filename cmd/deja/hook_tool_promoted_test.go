package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The line before an edit exists to carry the decision rather than point at it.
// A promoted note is the user's own statement about the work — the session-start
// block leads with it and calls it standing — and at the moment of the edit it
// was thrown away for whatever the newest session happened to end with (#2495).
func TestTheEditLineCarriesThePromotedDecision(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	write := func(sid string, minutes int, msgs [][2]any) {
		var lines []string
		for i, m := range msgs {
			rec := map[string]any{"type": m[0], "sessionId": sid,
				"timestamp": now.Add(-time.Duration(minutes-i) * time.Minute).Format(time.RFC3339),
				"cwd":       "/work/app", "message": map[string]any{"role": m[0], "content": m[1]}}
			b, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(b))
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(old string) any {
		return []any{map[string]any{"type": "tool_use", "name": "Edit",
			"input": map[string]any{"file_path": "/work/app/retry.go", "old_string": old, "new_string": "y"}}}
	}
	write("dec", 200, [][2]any{
		{"user", "should the retry budget go up to 10 for the payments client?"},
		{"assistant", edit("return 3")},
		{"assistant", "no: the pool change is what fixed the timeouts, the retry budget stays at 5"},
	})
	for k := 0; k < 6; k++ {
		write(fmt.Sprintf("f%d", k), 150-10*k, [][2]any{
			{"user", fmt.Sprintf("work on retry.go and the payments client (%d), the timeouts are back", k)},
			{"assistant", edit(fmt.Sprintf("x%d", k))},
			{"assistant", fmt.Sprintf("looked at retry.go again (%d)", k)},
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := sources.AppendPromoted("app", "should the retry budget go up to 10 for the payments client?",
		"the retry budget stays at 5; the pool change is what fixed the timeouts",
		"claude:dec", "accepted", now); err != nil {
		t.Fatal(err)
	}

	line := fileHookLine(dir, "/work/app", "/work/app/retry.go")
	if line == "" {
		t.Fatal("the fixture produced no line at all")
	}
	if !strings.Contains(line, "retry budget stays at 5") {
		t.Errorf("the promoted decision about this file is not in the line:\n  %s", line)
	}
}

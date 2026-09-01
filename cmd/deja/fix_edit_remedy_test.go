package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A failing test is answered by a change to a file, and deja must hand that
// over as what it is rather than as a command to paste (#2163).
func TestFixNamesTheFileTheSessionChanged(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i, id := range []string{"e1", "e2"} {
		at := time.Now().Add(-time.Duration(i+1) * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"the pool test keeps failing"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","id":"t` + id + `","input":{"command":"go test ./internal/pool"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t` + id + `","content":"--- FAIL: TestPoolDrainsOnClose (0.09s)"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","id":"x` + id + `","input":{"file_path":"/tmp/app/internal/pool/pool.go","old_string":"close(c)","new_string":"c.drain()"}}]}}`,
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "fix", "--- FAIL: TestPoolDrainsOnClose")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pool.go") {
		t.Errorf("fix did not name the file the sessions changed:\n%s", out)
	}
	if strings.Contains(out, "ran next") {
		t.Errorf("an edit was offered as a command to run:\n%s", out)
	}
}

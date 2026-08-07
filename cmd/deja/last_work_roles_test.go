package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `--role files|command|edit` is documented on `deja last` and accepted by the
// role check, but the listing scanned the index with no role set, and a scan
// without one drops exactly those three record kinds — they are indexed and
// served only when asked for. So the flag that asked for them was the one that
// could never see them, and every store answered "no sessions match role
// files" while `deja search --role files` found them.
func TestLastFindsFileAndCommandWork(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-workspace-demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"type":"user","sessionId":"work-1","timestamp":"2026-07-17T09:00:00Z","message":{"role":"user","content":"fix the pager"}}`,
		`{"type":"assistant","sessionId":"work-1","timestamp":"2026-07-17T09:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/workspace/demo/internal/pager/paginate.go","old_string":"a","new_string":"b"}}]}}`,
		`{"type":"assistant","sessionId":"work-1","timestamp":"2026-07-17T09:00:02Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./internal/pager"}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "session.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, role := range []string{"files", "command", "edit"} {
		out, err := captureRun(t, "last", "10", "--role", role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if !strings.Contains(out, "work-1") {
			t.Errorf("last --role %s does not find the session that has that record: %q", role, out)
		}
	}
}

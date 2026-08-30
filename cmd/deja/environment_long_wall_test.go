package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// The errors that carry a path run long, and the block cuts them from the left
// — so a wall reached the agent as a host and a port with the failure itself
// gone: "connection to server at … port 5432 failed:" and nothing about what
// failed (#2442).
func TestALongWallKeepsWhatNamesIt(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	wall := `psql: error: connection to server at "db.internal.example.com" (10.42.0.7), port 5432 failed: Connection refused`
	for k := 0; k < 4; k++ {
		sid := fmt.Sprintf("s%d", k)
		at := time.Now().Add(-time.Duration(200-20*k) * time.Minute).UTC().Format(time.RFC3339)
		lines := []string{
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -h db.internal"}}]}}`, sid, at),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":%q}]}}`, sid, at, wall),
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	block, _ := environmentBlockFrom(index.DefaultDir(), policy.ActivationAuto)
	if block == "" {
		t.Fatalf("no block on a machine with four sessions on one wall")
	}
	if !strings.Contains(block, "Connection refused") {
		t.Errorf("the block kept the host and dropped the failure:\n%s", block)
	}
	// Still bounded: the block goes into every session start.
	for _, line := range strings.Split(block, "\n") {
		if len(line) > 200 {
			t.Errorf("a %d-byte line in a block injected on every session start:\n%s", len(line), line)
		}
	}
}

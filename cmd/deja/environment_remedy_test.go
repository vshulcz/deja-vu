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

// The block names the walls this machine keeps hitting and then tells the agent
// to check or use an alternative — while deja already knows what was run after
// one of them, and says so through `deja fix` and the tool hook. The one place
// it is silent about the remedy is the block written to change what the agent
// does next (#2440).
func TestTheEnvironmentBlockNamesTheRemedyItKnows(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	fail := `psql: error: connection to server at "db.internal.example.com" (10.42.0.7), port 5432 failed: Connection refused`
	for k := 0; k < 4; k++ {
		var lines []string
		at := func(m int) string {
			return time.Now().Add(-time.Duration(300-40*k-m) * time.Minute).UTC().Format(time.RFC3339)
		}
		sid := fmt.Sprintf("s%d", k)
		lines = append(lines,
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -h db.internal -c 'select 1'"}}]}}`, sid, at(0)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":%q}]}}`, sid, at(1), fail),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"brew services start postgresql@16"}}]}}`, sid, at(2)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"==> Successfully started postgresql@16"}]}}`, sid, at(3)),
		)
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	block, _ := environmentBlockFrom(dir, policy.ActivationAuto)
	if block == "" {
		t.Fatalf("no environment block on a machine with four sessions on one wall")
	}
	if !strings.Contains(block, "psql: error: connection to server") {
		t.Fatalf("the block does not name the wall:\n%s", block)
	}
	if !strings.Contains(block, "brew services start postgresql@16") {
		t.Errorf("the block tells the agent to find its own way past a wall deja knows the way past:\n%s", block)
	}
}

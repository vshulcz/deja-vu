package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The plan hook speaks before an agent walks into a wall, which is the best
// moment deja has. It named the wall and stopped there, while `deja fix`
// answered the same error with the command that got past it — the gap 427
// closed for the environment block, still open on the surface that speaks
// first (#2458).
func TestAPlanFindingNamesTheRemedyDejaKnows(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	wall := "pgbouncer: error: connection refused while starting the migration runner pool"
	at := func(min int) string {
		return time.Now().Add(-time.Duration(min) * time.Minute).UTC().Format(time.RFC3339)
	}
	for k := 0; k < 4; k++ {
		sid := fmt.Sprintf("pool%d", k)
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":"switch the connection pool to pgbouncer for the migrations"}}`, sid, at(400-30*k)),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"pgbouncer -d /etc/pgbouncer.ini"}}]}}`, sid, at(399-30*k)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":%q}]}}`, sid, at(398-30*k), wall),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"brew services start postgresql@16"}}]}}`, sid, at(397-30*k)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"==> Successfully started postgresql@16"}]}}`, sid, at(396-30*k)),
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Enough unrelated history that the plan's words identify something.
	for i := 0; i < 40; i++ {
		sid := fmt.Sprintf("noise%d", i)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":"unrelated question %d about the css build and postcss"}}`, sid, at(200-2*i), i)
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	plan := "1. Switch the connection pool to pgbouncer for the migration runner.\n2. Update the docs."
	findings := planFindings(dir, plan, "")
	if len(findings) == 0 {
		t.Fatalf("the plan walks into a wall four sessions hit and the hook said nothing")
	}
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "pgbouncer") {
		t.Fatalf("the finding does not name the wall:\n%s", joined)
	}
	if !strings.Contains(joined, "brew services start postgresql@16") {
		t.Errorf("the finding names the wall and not the way past it, which deja knows:\n%s", joined)
	}
}

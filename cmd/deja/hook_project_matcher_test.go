package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The trap line asks whether each stored pair is this project's, and it used
// to work out the project's names afresh for every pair. Each working-out
// resolves every worktree root of the repository on disk, so a repository with
// hundreds of worktrees held the tool hook for minutes on a few thousand pairs.
func TestTrapLineWorksOutProjectNamesOnce(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	at := time.Now().Add(-48 * time.Hour)
	for i, name := range []string{"alpha", "beta", "gamma"} {
		for n := range 2 {
			id := fmt.Sprintf("more-%s-%d", name, n)
			rec := func(role, content string, j int) string {
				return fmt.Sprintf(`{"type":%q,"sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":%q,"content":%s}}`,
					role, id, at.Add(time.Duration(10*i+j)*time.Minute).UTC().Format(time.RFC3339), role, content) + "\n"
			}
			body := rec("user", `"the build is broken"`, 0) +
				rec("assistant", fmt.Sprintf("%q", "ran it and got:\npanic: "+name+": connection refused"), 1) +
				rec("assistant", fmt.Sprintf(`[{"type":"tool_use","name":"Bash","input":{"command":%q}}]`, "make reset-"+name), 2) +
				rec("assistant", `"that did it, the build is green now"`, 3)
			if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if n := len(index.ReadFixes(dir)); n < 2 {
		t.Fatalf("seeded %d fix pairs, want several so the per-pair cost shows", n)
	}

	var calls atomic.Int32
	orig := projectNameCandidates
	projectNameCandidates = func(cwd string) []string {
		calls.Add(1)
		return orig(cwd)
	}
	t.Cleanup(func() { projectNameCandidates = orig })

	confirmedCommandTrapLine(dir, "/work/app", "make")
	if n := calls.Load(); n != 1 {
		t.Fatalf("the project's names were worked out %d times for one hook call, want once", n)
	}
}

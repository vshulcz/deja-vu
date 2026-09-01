package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A session started in a subdirectory of a repository belongs to that
// repository: #2039 taught the subdirectory to find the repo, and standing at
// the root still found nothing of what happened inside it — the name is
// guessed from the cwd, and guessing downward is what no list of names can do
// (#2040). What the session touched says where the work was, and those paths
// are already in the index.
func TestTheRepositoryRootRecallsWorkFromItsSubdirectories(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "src", "app")
	sub := filepath.Join(repo, "cmd", "deja")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// A short session, started in the subdirectory, that touched a file under
	// the repository — too little for projectFromPaths to re-project it, which
	// is the case the issue measured.
	at := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), encodedProjectDir(sub))
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","sessionId":"insub","timestamp":"` + at + `","cwd":` + mustJSON(t, sub) +
			`,"message":{"role":"user","content":"the connection pool keeps running dry under load"}}`,
		`{"type":"assistant","sessionId":"insub","timestamp":"` + at + `","cwd":` + mustJSON(t, sub) +
			`,"message":{"role":"assistant","content":"we raised the pool size to 32 and it held"}}`,
		`{"type":"assistant","sessionId":"insub","timestamp":"` + at + `","cwd":` + mustJSON(t, sub) +
			`,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","id":"t1","input":{"file_path":` +
			mustJSON(t, filepath.Join(sub, "pool.go")) + `,"old_string":"16","new_string":"32"}}]}}`,
	}
	if err := os.WriteFile(filepath.Join(store, "insub.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Standing in the subdirectory: the session is its own project and the
	// hook finds it. This is the half #2039 fixed, and it has to keep working.
	if out := hookDigestAt(t, sub); !strings.Contains(out, "pool") {
		t.Fatalf("the subdirectory does not recall its own session:\n%s", out)
	}
	// Standing at the repository root.
	if out := hookDigestAt(t, repo); !strings.Contains(out, "pool") {
		t.Errorf("the repository root recalls nothing that happened inside it:\n%s", out)
	}
}

func hookDigestAt(t *testing.T, cwd string) string {
	t.Helper()
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	withHookStdin(t, hookPayload(t, map[string]string{"source": "startup", "session_id": "s", "cwd": cwd}))
	return captureStdout(t, func() { runHookContextPlain(t) })
}

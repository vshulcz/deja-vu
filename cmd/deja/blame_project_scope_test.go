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

// `deja blame internal/pool/pool.go` in one checkout listed other repos'
// pool.go sessions in among its own. A path names a file in this checkout;
// the others are counted and left to --all-projects.
func TestBlameAnswersFromThisProjectAndCountsTheRest(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	checkout := filepath.Join(tmp, "work", "checkout")
	payments := filepath.Join(tmp, "work", "payments")
	for _, d := range []string{filepath.Join(checkout, "internal", "pool"), payments} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	write := func(id, cwd string) {
		// The project comes from the record's cwd; the directory name only has
		// to be one Windows accepts, which an encoded C:\ path is not.
		dir := filepath.Join(root, "-work-"+filepath.Base(cwd))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,`+
			`"message":{"role":"user","content":"retire idle connections in internal/pool/pool.go before the proxy drops them"}}`, id, at, cwd)
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("here0001", checkout)
	write("there001", payments)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(checkout)

	var runErr error
	out := captureStdout(t, func() { runErr = runBlame(dir, []string{"internal/pool/pool.go"}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !strings.Contains(out, "here0001") || strings.Contains(out, "there001") {
		t.Fatalf("blame in the checkout listed:\n%s", out)
	}

	out = captureStdout(t, func() { runErr = runBlame(dir, []string{"internal/pool/pool.go", "--all-projects"}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !strings.Contains(out, "here0001") || !strings.Contains(out, "there001") {
		t.Fatalf("--all-projects listed:\n%s", out)
	}
}

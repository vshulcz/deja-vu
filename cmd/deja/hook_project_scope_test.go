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

// A session start in /work/api recalled a client's acme/api under "sessions
// from this project": the candidate list carries the checkout's bare directory
// name and RecentProjects matched it as a substring (#2333).
func TestSessionStartStaysInsideItsOwnProject(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write := func(dirName, id, cwd, text string, hoursAgo int) {
		dir := filepath.Join(root, dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,`+
			`"message":{"role":"user","content":%q}}`, id, at, cwd, text)
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("-work-api", "mine", "/work/api", "my own retry budget question", 8)
	write("-clients-acme-api", "acme", "/clients/acme/api", "the acme ledger cutover", 2)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := hookContextFor(t, dir, `{"hook_event_name":"SessionStart","cwd":"/work/api","session_id":"agent1"}`)
	if !strings.Contains(out, "my own retry budget question") {
		t.Fatalf("the checkout's own session is missing, so this measures nothing:\n%s", out)
	}
	if strings.Contains(out, "acme") {
		t.Errorf("a session from another project ending in /api was injected as this project's:\n%s", out)
	}
}

// hookContextFor runs the session-start hook over one payload and returns what
// it wrote.
func hookContextFor(t *testing.T, dir, payload string) string {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = inW.WriteString(payload)
		_ = inW.Close()
	}()
	oldIn, oldOut := os.Stdin, os.Stdout
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin, os.Stdout = inR, outW
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := outR.Read(buf)
			b.Write(buf[:n])
			if rerr != nil {
				break
			}
		}
		done <- b.String()
	}()
	hookErr := runHookContext(dir, false)
	os.Stdin, os.Stdout = oldIn, oldOut
	_ = outW.Close()
	out := <-done
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	return out
}

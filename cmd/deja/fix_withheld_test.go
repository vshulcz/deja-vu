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

// Two sessions answered the same error with a force push. deja never hands
// that over as a fix, and then said "no session on this machine ran a command
// after that error" — false, the store holds the pair.
func TestFixSaysTheRemedyWasWithheldRatherThanThatNothingRan(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	fail := "error: failed to push some refs to 'origin' (non-fast-forward snorbleref)"
	for i, id := range []string{"w1", "w2"} {
		at := time.Now().Add(-time.Duration(5-i) * time.Hour).UTC().Format(time.RFC3339)
		rows := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":"push it"}}`, id, at),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"git push origin main"}}]}}`, id, at),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`, id, at, fail),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"git push -f origin main"}}]}}`, id, at),
		}
		if err := os.WriteFile(filepath.Join(claude, id+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "fix", fail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "git push -f") {
		t.Fatalf("fix handed over the force push: %q", out)
	}
	if strings.Contains(out, "no session on this machine ran a command") {
		t.Errorf("fix denied the pair it withheld: %q", out)
	}
	if !strings.Contains(out, "does not hand those over") {
		t.Errorf("fix does not say the remedy was withheld: %q", out)
	}
}

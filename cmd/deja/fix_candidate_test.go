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

// deja holds the first sighting of an error-and-remedy as a candidate and waits
// for a second before naming it, which is right. It said "no session on this
// machine ran a command after that error" while holding exactly that (#2282).
func TestFixSaysItIsWaitingRatherThanDenyingTheSighting(t *testing.T) {
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

	fail := "undefined: snorblefunc in vendor/blarg/api.go"
	write := func(id string, hoursAgo int) {
		t.Helper()
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		rows := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":"the build is broken"}}`, id, at),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}}]}}`, id, at),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`, id, at, fail),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go mod vendor && go build ./..."}}]}}`, id, at),
		}
		name := filepath.Join(claude, id+".jsonl")
		if err := os.WriteFile(name, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("f1", 5)
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "fix", fail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no session on this machine ran a command") {
		t.Errorf("fix denied the sighting it is holding: %q", out)
	}
	if !strings.Contains(out, "unconfirmed") {
		t.Errorf("fix does not say the sighting is unconfirmed: %q", out)
	}

	// An error nobody has hit still gets the plain answer.
	out, err = captureRun(t, "fix", "undefined: neverseenfunc in nowhere.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no session on this machine ran a command") {
		t.Errorf("an unseen error should still say nothing ran after it: %q", out)
	}

	// And a second sighting names the remedy, as before.
	write("f2", 4)
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "fix", fail)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "go mod vendor") {
		t.Errorf("a confirmed pair was not named: %q", out)
	}
}

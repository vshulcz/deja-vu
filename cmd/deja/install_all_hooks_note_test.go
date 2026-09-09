package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `--all` writes MCP entries and nothing else. That is the flag's job, and it
// is not what the name says: someone repairing a machine runs it, watches every
// harness scroll past as "wrote", and walks away with the hooks exactly as
// broken as they were — which is how the wiring in #3421 went on firing after a
// repair that looked complete.
func TestInstallAllSaysWhereTheHooksAre(t *testing.T) {
	tmp := hermeticEnv(t)
	if err := os.MkdirAll(filepath.Join(tmp, "home", ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := logoWanted
	t.Cleanup(func() { logoWanted = restore })
	logoWanted = func(*os.File) bool { return true }

	out, err := captureRun(t, "install", "--all", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deja install --auto") {
		t.Errorf("--all never said the hooks are elsewhere:\n%s", out)
	}

	// --auto writes them, so the same line there would be a lie.
	out, err = captureRun(t, "install", "--auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hooks are not in this list") {
		t.Errorf("--auto claimed it had left the hooks alone:\n%s", out)
	}
}

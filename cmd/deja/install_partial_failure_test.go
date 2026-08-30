package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The fixture below makes a directory refuse writes, which is a permission bit
// on unix and a suggestion on windows — where the install would simply succeed
// and the test would fail for the wrong reason. Same guard the other
// read-only fixtures in this package use.
func skipWhereModeBitsDoNotRefuse(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the directory mode is not enforced here")
	}
}

// "install finished what it could" returned before the banner and before the
// index build, so a run that wired nine harnesses and failed one said nothing
// about the nine and left them without a store to read (#2721).
func TestAnInstallThatRefusesOneTargetStillReportsTheRest(t *testing.T) {
	skipWhereModeBitsDoNotRefuse(t)
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	// A codex home, so there is a target that can be written beside the one
	// that cannot.
	if err := os.MkdirAll(filepath.Join(tmp, "home", ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	// One config deja cannot write, beside one it can.
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claude, []byte(`{"mcpServers":{}}`), 0o444); err != nil {
		t.Fatal(err)
	}
	unwritable := filepath.Join(home, ".claude")
	if err := os.MkdirAll(unwritable, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unwritable, 0o755) })

	// The banner, which is the screen this is about: the line-by-line output
	// printed its per-target lines before the old return, so only the table
	// could lose them.
	restore := logoWanted
	t.Cleanup(func() { logoWanted = restore })
	logoWanted = func(*os.File) bool { return true }

	out, err := captureRun(t, "install", "--all", "--no-index")
	if err == nil {
		t.Fatal("a target that could not be written was reported as installed")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("the refusal did not name itself: %v", err)
	}
	if !strings.Contains(out, "codex") {
		t.Errorf("the table never named the target that was written:\n%s\nerror: %v", out, err)
	}
	if strings.Contains(out, "claude-code") {
		t.Errorf("the table named a target that refused:\n%s", out)
	}
}

// And the store: a run that wired something ends with a build, because
// installing is the one moment a person has already accepted a wait. The
// refusal was taking that away too.
func TestAnInstallThatRefusesOneTargetStillBuildsTheIndex(t *testing.T) {
	skipWhereModeBitsDoNotRefuse(t)
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"the zonkoshard retry budget"},` +
		`"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{}}`), 0o444); err != nil {
		t.Fatal(err)
	}
	unwritable := filepath.Join(home, ".claude")
	if err := os.MkdirAll(unwritable, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unwritable, 0o755) })

	if _, err := captureRun(t, "install", "--all"); err == nil {
		t.Fatal("a target that could not be written was reported as installed")
	}
	if _, err := os.Stat(filepath.Join(tmp, "index.db", "manifest.gob")); err != nil {
		t.Errorf("an install that wired a harness left it with no index: %v", err)
	}
}

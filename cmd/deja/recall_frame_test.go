package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestFrameRecallWrapsOnlyNonEmpty(t *testing.T) {
	if frameRecall("") != "" || frameRecall("  \n") != "  \n" {
		t.Fatal("empty digests must stay unwrapped")
	}
	out := frameRecall("prior fix: use --force-with-lease")
	if !strings.HasPrefix(out, "<deja-recall>\n") || !strings.HasSuffix(out, "\n</deja-recall>") || !strings.Contains(out, "untrusted") {
		t.Fatalf("framing wrong: %q", out)
	}
}

func TestResumeRefusesUnsafeSessionIDs(t *testing.T) {
	for _, id := range []string{"abc --dangerously-skip-permissions", "x; rm -rf ~", "-flag", `a"b`, "a\nb", ""} {
		if _, _, err := resumeCommand(model.Session{Harness: "claude", ID: id}); err == nil {
			t.Fatalf("id %q accepted", id)
		}
	}
	for _, id := range []string{"ses_0eb8f207", "9a72c3d1-1111-2222-3333-444455556666", "abc.DEF-123"} {
		if _, _, err := resumeCommand(model.Session{Harness: "claude", ID: id}); err != nil {
			t.Fatalf("id %q refused: %v", id, err)
		}
	}
}

func TestInstallBackupAndNewConfigOwnerOnly(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	cfg := filepath.Join(home, ".claude.json")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{"x":{"env":{"API_KEY":"s"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("claude-code", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	assertMode := func(path string, want os.FileMode) {
		t.Helper()
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", path, got, want)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	assertMode(cfg+".bak", 0o600)
	assertMode(cfg, 0o600) // live mode preserved
	// A config created from scratch starts owner-only.
	fresh := filepath.Join(home, ".codex", "config.toml")
	if _, err := installTarget("codex", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	assertMode(fresh, 0o600)
}

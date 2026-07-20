package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
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

// The narration protocol must be present on every agent-facing surface, and
// must carry the only-when-it-helped guard so it cannot become spam.
func TestNarrationProtocolOnAllSurfaces(t *testing.T) {
	if !strings.Contains(guidanceBody, "deja-vu recalled:") || !strings.Contains(guidanceBody, "Never credit recalls that did not help") {
		t.Fatal("guidance missing narration protocol")
	}
	for _, m := range []string{"initialize", "tools/list"} {
		_ = m
	}
	resp, _, _ := handleMCP(index.DefaultDir(), rpcRequest{Method: "tools/list"})
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "deja-vu recalled"); n < 2 {
		t.Fatalf("MCP tool descriptions carry narration %d times, want >=2 (recall + recall_context)", n)
	}
	if !strings.Contains(string(b), "Say nothing about recalls that did not help") {
		t.Fatal("MCP narration missing the no-spam guard")
	}
}

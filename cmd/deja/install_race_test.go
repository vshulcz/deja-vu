package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Claude Code rewrites ~/.claude.json while it runs, and `deja install` is
// often run from inside it. A save that landed between deja's read and its
// rename was put back by the rename: 31 and 116 of 200 runs lost the client's
// change in the hunt's race test (#4561). The edit is redone on the client's
// file now.
func TestInstallKeepsAClientSaveMadeDuringTheEdit(t *testing.T) {
	hermeticEnv(t)
	p := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{\n  \"numStartups\": 0\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := false
	beforeConfigReplace = func(path string) {
		if saved || filepath.Base(path) != filepath.Base(p) {
			return
		}
		saved = true
		// The client's save: read, change, temp file and rename.
		b, _ := os.ReadFile(p)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["numStartups"] = 1
		nb, jerr := json.MarshalIndent(m, "", "  ")
		if jerr != nil {
			t.Fatal(jerr)
		}
		tmp := p + ".client-tmp"
		if err := os.WriteFile(tmp, nb, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, p); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { beforeConfigReplace = func(string) {} })
	if _, err := captureRun(t, "install", "claude-code", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if !saved {
		t.Fatal("the client never saved; the test did not race anything")
	}
	b, _ := os.ReadFile(p)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["numStartups"] != float64(1) {
		t.Errorf("the client's save was lost:\n%s", b)
	}
	if servers, _ := m["mcpServers"].(map[string]any); servers["deja"] == nil {
		t.Errorf("deja's entry is missing:\n%s", b)
	}
}

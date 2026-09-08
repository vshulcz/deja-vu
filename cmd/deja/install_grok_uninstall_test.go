package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Install merges deja's hooks beside the reader's own; uninstall removed the
// whole file, and the reader's entries went with it (#3219).
func TestUninstallGrokAutoLeavesTheReadersHooks(t *testing.T) {
	hermeticEnv(t)
	path := grokHooksPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// An entry from an older deja at another path sits beside the reader's.
	seed := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"/usr/local/bin/other-tool session"},{"type":"command","command":"/old/path/deja hook-context"}]}],"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"/usr/local/bin/lint-on-write"}]}]}}` + "\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reader's hooks file is gone: %v", err)
	}
	if strings.Contains(string(b), "/bin/deja") || strings.Contains(string(b), "/old/path/deja") {
		t.Fatalf("deja's entries stayed:\n%s", b)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"other-tool session", "lint-on-write"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("the reader's %q entry is gone:\n%s", want, b)
		}
	}

	// deja's file alone: it goes, as before.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a file holding only deja's hooks was kept")
	}
}

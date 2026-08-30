package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// An -auto target writes several files and used to report only the last one,
// so a run that rewired the MCP entry said "unchanged" about the hook file
// instead — the wrong word about the wrong path (#2396).
func TestAnAutoTargetReportsTheFileItChanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := installTarget("claude-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	// The premise: with everything in place, there is nothing to do.
	settled, err := installTarget("claude-auto", "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Action != "unchanged" {
		t.Fatalf("a second install still had work to do: %s %s", settled.Action, settled.Path)
	}

	// Take deja out of the MCP config by hand and leave the hooks alone.
	mcpPath := filepath.Join(home, ".claude.json")
	body, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil || servers["deja"] == nil {
		t.Fatalf("install left no deja entry to remove: %s", body)
	}
	delete(servers, "deja")
	body, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcpPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := installTarget("claude-auto", "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action == "unchanged" {
		t.Errorf("install put the entry back and called the run unchanged: %s %s", r.Action, r.Path)
	}
	if r.Path != mcpPath {
		t.Errorf("install named %s, but %s is what it wrote", r.Path, mcpPath)
	}
}

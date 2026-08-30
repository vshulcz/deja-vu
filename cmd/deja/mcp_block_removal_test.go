package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config the reader owns must come back exactly as it was. deja adds the
// `mcpServers` block when the file has none, and on the way out removed only
// its own entry — so the reader was left with an empty block they never wrote,
// and the backup install had promised sat beside it holding the real thing. Of
// the 22 backups a full round leaves, 13 differ from the live file by exactly
// that empty container (#2604).
func TestUninstallTakesBackTheBlockItAdded(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "{\n  \"mine\": \"MINE\"\n}\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "deja") {
		t.Fatalf("install wired nothing here: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reader's config is gone: %v", err)
	}
	if strings.Contains(string(b), "mcpServers") {
		t.Errorf("the block deja added is still there:\n%s", b)
	}
	if !strings.Contains(string(b), "MINE") {
		t.Errorf("the reader's own content is gone:\n%s", b)
	}
	// The snapshot install promised stays: it is a copy of the reader's own
	// file, and deleting it is not deja's call — the rule
	// TestUninstallLeavesNoFileOrDirItCreated has held since #840.
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("the reader's own snapshot was removed: %v", err)
	}
}

// A block the reader wrote themselves stays, empty or not.
func TestUninstallKeepsABlockItDidNotAdd(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "{\n  \"mcpServers\": {\n    \"theirs\": {\n      \"command\": \"x\"\n    }\n  }\n}\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "theirs") || !strings.Contains(string(b), "mcpServers") {
		t.Errorf("the reader's own block did not survive:\n%s", b)
	}
}

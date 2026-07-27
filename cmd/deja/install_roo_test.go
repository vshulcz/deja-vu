package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rooStorage(t *testing.T, home, host string) string {
	t.Helper()
	p := filepath.Join(home, "Library", "Application Support", host, "User", "globalStorage", "rooveterinaryinc.roo-cline")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Roo keeps MCP settings per editor host. Someone running it in both VS Code
// and Cursor would otherwise get recall in one and silence in the other.
func TestInstallRooWritesEveryHostItRunsIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{
		rooStorage(t, home, "Code"),
		rooStorage(t, home, "Cursor"),
	}, string(os.PathListSeparator)))
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, host := range []string{"Code", "Cursor"} {
		p := filepath.Join(home, "Library", "Application Support", host, "User", "globalStorage",
			"rooveterinaryinc.roo-cline", "settings", "mcp_settings.json")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: settings not written: %v", host, err)
		}
		var root struct {
			Servers map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(b, &root); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if _, ok := root.Servers["deja"]; !ok {
			t.Fatalf("%s: no deja server: %s", host, b)
		}
	}
	if _, err := installRoo("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage",
		"rooveterinaryinc.roo-cline", "settings", "mcp_settings.json"))
	if strings.Contains(string(b), "deja") {
		t.Fatalf("uninstall left the server behind: %s", b)
	}
}

// A host Roo has never run in has no storage directory. Creating one there
// leaves settings for an editor that will never read them.
func TestInstallRooSkipsHostsWithoutStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	absent := filepath.Join(home, "Library", "Application Support", "VSCodium", "User", "globalStorage", "rooveterinaryinc.roo-cline")
	t.Setenv("DEJA_ROO_ROOTS", absent)
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("install created storage for a host that does not have Roo: %v", err)
	}
}

// Roo has no hook that could refresh a digest, so what goes into the global
// rules is guidance — text that stays true — not recalled sessions.
func TestRooGuidanceGoesToGlobalRules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGuidance("roo", false); err != nil {
		t.Fatalf("guidance: %v", err)
	}
	p := filepath.Join(home, ".roo", "rules", "deja.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("rules file missing: %v", err)
	}
	if !strings.Contains(string(b), guidanceStart) {
		t.Fatalf("no deja block:\n%s", b)
	}
	// Mode-specific directories sit beside this one; writing into one of them
	// would make recall advice appear in Code mode and vanish in Ask mode.
	if strings.Contains(p, "rules-") {
		t.Fatalf("guidance landed in a mode-specific directory: %s", p)
	}
	if _, err := installGuidance("roo", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), guidanceStart) {
		t.Fatalf("uninstall left the block:\n%s", b)
	}
}

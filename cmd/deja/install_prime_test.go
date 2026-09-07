package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func primeSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("settings are not JSON prime-agent can read: %v\n%s", err, b)
	}
	return root
}

func TestInstallPrimeWiresTheMCPServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	r, err := installPrimeMCP("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".prime", "agent", "settings.json")
	if r.Path != want {
		t.Fatalf("wrote %q, want %q — prime-agent reads its own settings file", r.Path, want)
	}
	servers, _ := primeSettings(t, r.Path)["mcpServers"].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if entry == nil {
		t.Fatalf("no deja server under mcpServers: %v", servers)
	}
	// prime-agent's settings carry stdio and http servers under one key and its
	// docs write the type out, so an entry without it is a guess.
	if entry["type"] != "stdio" {
		t.Fatalf("server entry has no stdio type: %v", entry)
	}
	if entry["command"] == nil || entry["args"] == nil {
		t.Fatalf("server entry is missing command/args: %v", entry)
	}
}

// Someone else's servers and the rest of the settings file have to survive
// both directions.
func TestInstallPrimeKeepsOtherSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".prime", "agent", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{"mcpServers":{"linear":{"type":"http","url":"https://mcp.example.com/mcp"}},"theme":"dark"}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installPrimeMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := primeSettings(t, path)
	servers, _ := root["mcpServers"].(map[string]any)
	if servers["linear"] == nil || servers["deja"] == nil {
		t.Fatalf("install did not sit beside the existing server: %v", root)
	}
	if root["theme"] != "dark" {
		t.Fatalf("an unrelated setting was dropped: %v", root)
	}
	if _, err := installPrimeMCP("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	root = primeSettings(t, path)
	servers, _ = root["mcpServers"].(map[string]any)
	if servers["deja"] != nil {
		t.Fatalf("uninstall left the deja entry: %v", root)
	}
	if servers["linear"] == nil {
		t.Fatalf("uninstall took someone else's server with it: %v", root)
	}
}

func TestInstallPrimeAutoWritesADiscoverableExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := installPrimeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	// prime-agent auto-discovers ~/.prime/agent/extensions/*.ts; anywhere else
	// is inert.
	path := filepath.Join(home, ".prime", "agent", "extensions", "deja.ts")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("extension not at the discovered path: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		`pi.on("before_agent_start"`, // the one event whose return reaches the model
		"hook-context",
		"hook-prompt",
		`pi.on("session_compact"`,
		"hook-precompact",
		"registerCommand",
		"ctx.ui.setStatus",
		`"/usr/local/bin/deja"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("extension missing %q:\n%s", want, src)
		}
	}
	// The MCP half goes in at the same time.
	if primeSettings(t, filepath.Join(home, ".prime", "agent", "settings.json"))["mcpServers"] == nil {
		t.Fatal("prime-auto wrote no MCP server")
	}
}

// tool_call and tool_result are in prime-agent's extension docs and never fire
// on 0.9.1 — measured with a probe extension against a recording endpoint, in
// --print mode, both as an installed extension and through -e. Wiring the fix
// pair to them would be a channel that looks present and says nothing, so it
// stays out until they deliver.
func TestPrimeExtensionDoesNotWireTheEventsThatNeverFire(t *testing.T) {
	src := primeExtensionTS("/bin/deja")
	for _, wrong := range []string{`pi.on("tool_result"`, `pi.on("tool_call"`} {
		if strings.Contains(src, wrong) {
			t.Fatalf("wired to %s, which does not fire on prime-agent 0.9.1:\n%s", wrong, src)
		}
	}
	if strings.Contains(src, "hook-tool-after") {
		t.Fatalf("the fix pair is wired with no event to carry it:\n%s", src)
	}
}

func TestUninstallPrimeAutoRemovesTheExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := installPrimeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installPrimeAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".prime", "agent", "extensions", "deja.ts")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("extension survived uninstall: %v", err)
	}
	if r, err := installPrimeExtension("/usr/local/bin/deja", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("second uninstall = %+v, %v", r, err)
	}
}

func TestPrimeExtensionQuotesTheBinaryPath(t *testing.T) {
	src := primeExtensionTS(`C:\Program Files\deja\deja.exe`)
	if !strings.Contains(src, `const DEJA = "C:\\Program Files\\deja\\deja.exe"`) {
		t.Fatalf("a Windows path was not escaped for TypeScript:\n%s", src)
	}
}

// The same shadowing trap pi hit (#3089): the command handler takes `args`, so
// the search array cannot also be `const args` — prime-agent parses the file as
// TypeScript and would refuse the whole extension.
func TestPrimeCommandDoesNotRedeclareItsArgument(t *testing.T) {
	src := primeExtensionTS("/bin/deja")
	if !strings.Contains(src, "handler: async (args: string") {
		t.Fatalf("the handler no longer takes args, so this guard is stale:\n%s", src)
	}
	if strings.Contains(src, "const args ") || strings.Contains(src, "const args=") {
		t.Fatalf("the search array shadows the handler's args parameter:\n%s", src)
	}
	if !strings.Contains(src, `["search", "--", query]`) {
		t.Fatalf("a query that names a deja flag dies in flag parsing:\n%s", src)
	}
}

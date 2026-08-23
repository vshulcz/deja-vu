package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKimiPlugin(t *testing.T, home string, enabled bool) {
	t.Helper()
	root := filepath.Join(home, "plugins", "managed", "deja")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kimi.plugin.json"), []byte(`{"name":"deja"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	record := `{"version":1,"plugins":[{"id":"deja","root":` + strconvQuote(root) +
		`,"source":"local-path","enabled":` + boolText(enabled) + `,"installedAt":"2026-08-23T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(home, "plugins", "installed.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
}

func strconvQuote(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// A machine that installed the plugin is wired: the plugin declares the same
// MCP server and recalls on every prompt. Reporting it as "not wired" is how
// someone ends up running an install that only pushes the plugin aside.
func TestDoctorCountsTheKimiPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "mcp.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var before bytes.Buffer
	doctorMCP(&before)
	doctorAutoRecall(&before)
	if !strings.Contains(kimiLine(before.String()), "not wired") {
		t.Fatalf("without the plugin kimi should read as not wired:\n%s", before.String())
	}

	writeKimiPlugin(t, home, true)
	var after bytes.Buffer
	doctorMCP(&after)
	doctorAutoRecall(&after)
	for _, want := range []string{"plugin", "the Kimi Code plugin recalls on every prompt"} {
		if !strings.Contains(after.String(), want) {
			t.Fatalf("doctor does not report the plugin (%q missing):\n%s", want, after.String())
		}
	}
	if strings.Contains(kimiLine(after.String()), "not wired") {
		t.Fatalf("kimi still reads as not wired with the plugin installed:\n%s", after.String())
	}
}

// A disabled plugin runs nothing, so it cannot stand in for the wiring.
func TestDoctorIgnoresADisabledKimiPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	writeKimiPlugin(t, home, false)
	if kimiPluginInstalled() {
		t.Fatal("a disabled plugin counted as installed")
	}
}

// The record can outlive the files it points at — a managed copy removed by
// hand, or a home restored from a backup.
func TestKimiPluginNeedsItsManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	writeKimiPlugin(t, home, true)
	if err := os.Remove(filepath.Join(home, "plugins", "managed", "deja", "kimi.plugin.json")); err != nil {
		t.Fatal(err)
	}
	if kimiPluginInstalled() {
		t.Fatal("a record pointing at nothing counted as installed")
	}
}

func kimiLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "kimi") {
			return line
		}
	}
	return ""
}

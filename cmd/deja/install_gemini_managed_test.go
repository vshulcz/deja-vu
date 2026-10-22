package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deja's gallery extension is named deja, so `gemini extensions install`
// puts it in ~/.gemini/extensions/deja, the directory `deja install
// gemini-auto` writes its hooks to. The install replaced the gallery
// manifest (version, MCP server, context file) and the uninstall deleted the
// whole extension (gemini-cli 0.64.0).
func TestGeminiAutoKeepsTheExtensionGeminiInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".gemini", "extensions", "deja")
	seed := map[string]string{
		"gemini-extension.json":          "{\n  \"name\": \"deja\",\n  \"version\": \"0.22.2\",\n  \"contextFileName\": \"GEMINI.md\",\n  \"mcpServers\": {\"deja\": {\"command\": \"deja\", \"args\": [\"mcp\"]}}\n}\n",
		"GEMINI.md":                      "# deja\n",
		".gemini-extension-install.json": "{\n  \"source\": \"/src/deja-vu\",\n  \"type\": \"local\"\n}",
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gemini-extension.json")); string(b) != seed["gemini-extension.json"] {
		t.Fatalf("install rewrote the manifest gemini installed:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "gemini-extension.json.bak")); err == nil {
		t.Fatal("install left a .bak inside the extension")
	}
	geminiExtensionHooks(t, home)

	if _, err := installGeminiAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("uninstall deleted the extension gemini installed: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(entries) != len(seed) {
		t.Fatalf("after uninstall the extension holds %v, want only %d seeded files", names, len(seed))
	}
	for name, body := range seed {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != body {
			t.Fatalf("%s changed: %q", name, b)
		}
	}
}

// An extension linked with `gemini extensions link` is loaded from its source:
// hooks deja writes under ~/.gemini/extensions never run, while doctor read
// them as wired. The install adds no hooks and says why, still wires MCP, and
// doctor reads the file gemini reads.
func TestGeminiAutoLeavesALinkedExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	src := filepath.Join(home, "src", "deja-vu")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "gemini-extension.json"), []byte(`{"name": "deja", "version": "0.22.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".gemini", "extensions", "deja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"source": "` + filepath.ToSlash(src) + `", "type": "link"}`)
	if err := os.WriteFile(filepath.Join(dir, ".gemini-extension-install.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := installTarget("gemini-auto", "/bin/deja", false)
	if err != nil {
		t.Fatalf("install over a linked extension: %v", err)
	}
	if !strings.Contains(res.Note, "gemini extensions link") {
		t.Fatalf("note = %q, want it to name the link", res.Note)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".gemini", "settings.json")); !strings.Contains(string(b), "/bin/deja") {
		t.Fatalf("the MCP half was skipped: settings.json = %s", b)
	}
	for _, d := range []string{dir, src} {
		entries, _ := os.ReadDir(d)
		if len(entries) != 1 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("%s now holds %v", d, names)
		}
	}
	if got, want := geminiHooksPath(), filepath.Join(src, "hooks", "hooks.json"); got != want {
		t.Fatalf("doctor reads %s, gemini reads %s", got, want)
	}

	// What an older deja left beside the link goes on uninstall, the switch it
	// turned on included; the link stays.
	if _, err := enableGeminiHooks(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"hooks": {"BeforeAgent": [{"hooks": [{"type": "command", "command": "/bin/deja hook-prompt"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "hooks", "hooks.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gemini-extension.json"), geminiManifest(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("gemini-auto", "/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".gemini", "settings.json")); strings.Contains(string(b), "hooksConfig") {
		t.Fatalf("uninstall kept the hooksConfig an older deja added, counting deja's own link as another extension: %s", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != ".gemini-extension-install.json" {
		t.Fatalf("uninstall left %v beside the link", entries)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gemini-extension-install.json")); !bytes.Equal(b, meta) {
		t.Fatalf("link record changed: %s", b)
	}
}

// An older deja wrote its stub manifest over the gallery one and kept the
// original as .bak; the next install puts it back. A record gemini could not
// parse is not gemini's install, and a gallery release's own hooks are left.
func TestGeminiAutoRepairsAndRespectsTheGalleryExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".gemini", "extensions", "deja")
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	gallery := "{\"name\": \"deja\", \"version\": \"0.22.2\"}\n"
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gemini-extension-install.json", `{"source": "https://example.com/deja-vu", "type": "git"}`)
	write("gemini-extension.json", string(geminiManifest()))
	write("gemini-extension.json.bak", gallery)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gemini-extension.json")); string(b) != gallery {
		t.Fatalf("manifest not restored: %s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "gemini-extension.json.bak")); err == nil {
		t.Fatal(".bak left inside the extension")
	}

	write(filepath.Join("hooks", "hooks.json"), `{"hooks": {}}`)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	geminiExtensionHooks(t, home)
	if b, _ := os.ReadFile(filepath.Join(dir, "hooks", "hooks.json")); !strings.Contains(string(b), "hook-context") {
		t.Fatalf("an empty hooks file was kept as the extension's: %s", b)
	}

	theirs := `{"hooks": {"BeforeAgent": [{"hooks": [{"type": "command", "name": "lint", "command": "lint-it"}]}]}}`
	write(filepath.Join("hooks", "hooks.json"), theirs)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "hooks", "hooks.json")); string(b) != theirs {
		t.Fatalf("the extension's own hooks were replaced: %s", b)
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(".gemini-extension-install.json", "{")
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gemini-extension.json")); err != nil {
		t.Fatal("a broken install record kept deja from writing the manifest gemini needs")
	}
}

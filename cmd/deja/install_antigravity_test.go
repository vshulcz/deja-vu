package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAntigravityPluginShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installAntigravityPlugin("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(antigravityConfigHome(), "plugins", "deja")
	// Without plugin.json the directory is not a plugin and hooks.json is
	// ignored without a word.
	if _, err := os.ReadFile(filepath.Join(dir, "plugin.json")); err != nil {
		t.Fatalf("plugin.json missing: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
	if err != nil {
		t.Fatalf("hooks.json missing: %v", err)
	}
	var root map[string]map[string][]map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks.json is not the named-hook map antigravity expects: %v", err)
	}
	handlers := root["deja-recall"]["PreInvocation"]
	if len(handlers) != 1 {
		t.Fatalf("PreInvocation handlers = %v", handlers)
	}
	cmd, _ := handlers[0]["command"].(string)
	if !strings.Contains(cmd, "hook-antigravity") || !strings.Contains(cmd, "/bin/deja") {
		t.Fatalf("command = %q", cmd)
	}
	if _, err := installAntigravityPlugin("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plugin survived uninstall: %v", err)
	}
}

func TestAntigravityPluginQuotesExecutablePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// The command is run through sh -c, so a path with a space must survive.
	if _, err := installAntigravityPlugin("/Applications/My Tools/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(antigravityConfigHome(), "plugins", "deja", "hooks.json"))
	var root map[string]map[string][]map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	cmd, _ := root["deja-recall"]["PreInvocation"][0]["command"].(string)
	if !strings.HasPrefix(cmd, `"/Applications/My Tools/deja"`) {
		t.Fatalf("unquoted path would split under sh -c: %q", cmd)
	}
}

func TestHookAntigravityInjectsOnlyOnFirstInvocation(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	// Later turns already carry the digest in their transcript.
	if err := runHookAntigravity(dir, strings.NewReader(`{"invocationNum":2}`), &out); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "{}" {
		t.Fatalf("second invocation injected %q", got)
	}
	// No index: still valid JSON, never a bare error the host would choke on.
	out.Reset()
	if err := runHookAntigravity(dir, strings.NewReader(`{"invocationNum":1}`), &out); err != nil {
		t.Fatalf("hook: %v", err)
	}
	var resp antigravityHookResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("output is not JSON: %q", out.String())
	}
	if len(resp.InjectSteps) != 0 {
		t.Fatalf("injected with no index: %+v", resp)
	}
}

// Antigravity runs the hook from the plugin directory, so recall scoped by
// cwd would come back empty for every project. The payload names the real
// workspace and the hook must use it.
func TestHookAntigravityScopesToWorkspacePath(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	var out bytes.Buffer
	in := `{"invocationNum":1,"workspacePaths":["/some/workspace"]}`
	if err := runHookAntigravity(t.TempDir(), strings.NewReader(in), &out); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if got := os.Getenv("CLAUDE_PROJECT_DIR"); got != "/some/workspace" {
		t.Fatalf("workspace not adopted for scoping: %q", got)
	}
}

func TestHookAntigravitySurvivesGarbageStdin(t *testing.T) {
	var out bytes.Buffer
	if err := runHookAntigravity(t.TempDir(), strings.NewReader("not json at all"), &out); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if !json.Valid(bytes.TrimSpace(out.Bytes())) {
		t.Fatalf("output is not JSON: %q", out.String())
	}
}

func TestAntigravityGuidanceLandsInsideThePlugin(t *testing.T) {
	hermeticEnv(t)
	if err := runInstall(t.TempDir(), []string{"antigravity"}, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(antigravityConfigHome(), "plugins", antigravityPluginName)
	skill := filepath.Join(dir, "skills", "deja-history", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("the skill has to sit inside the plugin, not beside it: %v", err)
	}
	// Antigravity ingests a directory only when plugin.json marks it; without
	// the marker the skill is skipped in silence.
	if _, err := os.Stat(filepath.Join(dir, "plugin.json")); err != nil {
		t.Fatalf("plugin.json must accompany the skill: %v", err)
	}
	// And nothing should be written to the path that reads like a plugin root
	// but is not one.
	if _, err := os.Stat(filepath.Join(antigravityConfigHome(), "skills")); err == nil {
		t.Fatal("guidance was also written beside the plugin, where nothing reads it")
	}
}

func TestAntigravityMarkerIsNotRewritten(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(antigravityConfigHome(), "plugins", antigravityPluginName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := []byte(`{"name":"deja","version":"kept"}` + "\n")
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureAntigravityPluginMarker(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil || string(got) != string(custom) {
		t.Fatalf("an existing marker belongs to install_antigravity, got %q", got)
	}
}

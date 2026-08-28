package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
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

// Antigravity runs the hook in the folder holding hooks.json rather than the
// user's project, so the workspace in the payload is the only thing that says
// which project this is. The scoping used to be checked through the export deja
// wrote; that export is gone (#2185), so this asks the question directly: the
// memory that comes back is the named workspace's.
func TestHookAntigravityScopesToWorkspacePath(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "alpha", "one.jsonl"), "alpha1", []string{
		`{"type":"user","sessionId":"alpha1","timestamp":"` + at +
			`","message":{"role":"user","content":"the alpha work: pgbouncer runs in transaction mode"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "beta1", []string{
		`{"type":"user","sessionId":"beta1","timestamp":"` + at +
			`","message":{"role":"user","content":"the beta work: the kafka consumer keeps rebalancing"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	beta := filepath.Join(base, "tmp", "beta")
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}
	// Neither the environment nor the working directory names a project.
	t.Chdir(base)
	t.Setenv("CLAUDE_PROJECT_DIR", "")

	var out bytes.Buffer
	// Marshalled rather than pasted: a Windows path is full of backslashes,
	// and hand-built JSON turns them into escape sequences the decoder refuses
	// — the payload then names no workspace and the recall comes back empty.
	payload, err := json.Marshal(map[string]any{"invocationNum": 1, "workspacePaths": []string{beta}})
	if err != nil {
		t.Fatal(err)
	}
	in := string(payload)
	if err := runHookAntigravity(dir, strings.NewReader(in), &out); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if !strings.Contains(out.String(), "rebalancing") {
		t.Errorf("the workspace named in the payload did not scope the recall:\n%s", out.String())
	}
	if strings.Contains(out.String(), "transaction mode") {
		t.Errorf("another project's memory came back:\n%s", out.String())
	}
	if got := os.Getenv("CLAUDE_PROJECT_DIR"); got != "" {
		t.Errorf("the door left %q in the environment for the next call in this process", got)
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

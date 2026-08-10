package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gooseConf(t *testing.T, cfg string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cfg, "goose", "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml missing: %v", err)
	}
	return string(b)
}

func TestInstallGooseWritesTheExtension(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if _, err := installGoose("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := gooseConf(t, cfg)
	// Goose keys extensions by name and skips anything not enabled; a stdio
	// entry without cmd is rejected at load.
	for _, want := range []string{"extensions:", "  deja:", "enabled: true", "type: stdio", "cmd: "} {
		if !strings.Contains(conf, want) {
			t.Fatalf("config missing %q:\n%s", want, conf)
		}
	}
	// Windows stdio MCP clients spawn through cmd, so the binary shows up in
	// args there rather than as the command itself.
	if !strings.Contains(conf, "/bin/deja") {
		t.Fatalf("config never names the binary:\n%s", conf)
	}
}

// The exe path lands in cmd (Unix) or args (Windows); either way a YAML
// metacharacter in it must be quoted, or Goose cannot load the extension it was
// just handed.
func TestInstallGooseQuotesThePath(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	exe := "/opt/deja: v#2/deja"
	if _, err := installGoose(exe, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := gooseConf(t, cfg)
	if !strings.Contains(conf, `"/opt/deja: v#2/deja"`) {
		t.Fatalf("exe path is not YAML-quoted, so Goose cannot parse it:\n%s", conf)
	}
	// The raw path on a value line would break the YAML at the ": ".
	if strings.Contains(conf, "- /opt/deja: v#2/deja") || strings.Contains(conf, "cmd: /opt/deja: v#2/deja") {
		t.Fatalf("raw unquoted path written, breaks the YAML:\n%s", conf)
	}
}

func TestInstallGooseKeepsOtherExtensions(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	dir := filepath.Join(cfg, "goose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "GOOSE_PROVIDER: openai\nextensions:\n  memory:\n    enabled: true\n    type: builtin\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGoose("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := gooseConf(t, cfg)
	for _, want := range []string{"GOOSE_PROVIDER: openai", "  memory:", "type: builtin", "  deja:"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("install dropped %q:\n%s", want, conf)
		}
	}
	if _, err := installGoose("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	conf = gooseConf(t, cfg)
	if strings.Contains(conf, "deja") {
		t.Fatalf("uninstall left our entry:\n%s", conf)
	}
	if !strings.Contains(conf, "  memory:") || !strings.Contains(conf, "GOOSE_PROVIDER: openai") {
		t.Fatalf("uninstall took the user's config with it:\n%s", conf)
	}
}

// .goosehints is read once when a session starts, so the file has to exist
// before the first run rather than being written on demand.
func TestInstallGooseAutoWritesHints(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hints := filepath.Join(cfg, "goose", ".goosehints")
	if _, err := os.Stat(hints); err != nil {
		t.Fatalf("hints not written: %v", err)
	}
	if _, err := installGooseAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(hints); !os.IsNotExist(err) {
		t.Fatalf("hints survived uninstall: %v", err)
	}
}

// The hook is what makes plain `goose` recall: it runs before Goose reads the
// hints file, so refreshing it there lands in the same session.
func TestInstallGooseAutoWritesTheHook(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hook := filepath.Join(home, ".agents", "plugins", "deja", "hooks", "hooks.json")
	b, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("hook not written: %v", err)
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}
	groups := root.Hooks["SessionStart"]
	if len(groups) == 0 || len(groups[0].Hooks) == 0 {
		t.Fatalf("no SessionStart hook: %s", b)
	}
	h := groups[0].Hooks[0]
	if h.Type != "command" || !strings.Contains(h.Command, "hook-goose") {
		t.Fatalf("hook = %+v", h)
	}
	// An invalid matcher makes Goose skip the rule without saying so, which is
	// why SessionStart carries none at all.
	if strings.Contains(string(b), "matcher") {
		t.Fatalf("SessionStart rule carries a matcher:\n%s", b)
	}
	if _, err := installGooseAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(hook); !os.IsNotExist(err) {
		t.Fatal("uninstall left Goose running a command that is gone")
	}
}

// Under the wrapper the digest goes to the MOIM file, which Goose re-reads
// every turn; writing the hints too would inject the same text twice.
func TestGooseRecallPathFollowsMOIM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")
	if got := gooseRecallPath(); !strings.HasSuffix(got, ".goosehints") {
		t.Fatalf("without MOIM the target is %q", got)
	}
	moim := filepath.Join(home, "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	if got := gooseRecallPath(); got != moim {
		t.Fatalf("with MOIM set the target is %q, want %q", got, moim)
	}
}

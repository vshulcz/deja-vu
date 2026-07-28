package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Every one of these decides where deja looks for a harness. A wrong path is
// not a crash — it is a harness that silently reports nothing, which is the
// failure mode this project keeps hitting.
func TestHarnessPathsFollowTheirOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{
		"CLINE_DIR", "CLINE_DATA_DIR", "CLINE_SESSION_DATA_DIR",
		"CLINE_MCP_SETTINGS_PATH", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
	} {
		t.Setenv(v, "")
	}

	for name, got := range map[string]string{
		"cline config":   ClineConfigDir(),
		"cline sessions": ClineSessionsDir(),
		"cline mcp":      ClineMCPSettingsPath(),
		"cline plugins":  ClinePluginsDir(),
	} {
		if !strings.HasPrefix(got, home) {
			t.Fatalf("%s = %q, outside the home directory", name, got)
		}
	}
	// Plugins sit beside the data directory, not inside it: a plugin under
	// <data>/plugins is never discovered.
	if strings.HasPrefix(ClinePluginsDir(), ClineConfigDir()) {
		t.Fatalf("plugins dir %q is inside the data dir %q", ClinePluginsDir(), ClineConfigDir())
	}

	// CLINE_DIR moves the whole tree.
	moved := filepath.Join(home, "moved")
	t.Setenv("CLINE_DIR", moved)
	for name, got := range map[string]string{"config": ClineConfigDir(), "plugins": ClinePluginsDir()} {
		if !strings.HasPrefix(got, moved) {
			t.Fatalf("CLINE_DIR ignored by %s: %q", name, got)
		}
	}
	custom := filepath.Join(home, "custom.json")
	t.Setenv("CLINE_MCP_SETTINGS_PATH", custom)
	if ClineMCPSettingsPath() != custom {
		t.Fatalf("CLINE_MCP_SETTINGS_PATH ignored: %q", ClineMCPSettingsPath())
	}
}

func TestGoosePathRootRelocatesEverything(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{"DEJA_GOOSE_ROOT", "DEJA_GOOSE_DB", "XDG_DATA_HOME"} {
		t.Setenv(v, "")
	}
	root := filepath.Join(home, "elsewhere")
	t.Setenv("GOOSE_PATH_ROOT", root)
	if got := GooseDataDir(); got != filepath.Join(root, "data") {
		t.Fatalf("GooseDataDir() = %q", got)
	}
	if got := GooseDB(); !strings.HasPrefix(got, root) {
		t.Fatalf("GooseDB() = %q, ignores GOOSE_PATH_ROOT", got)
	}
	// Legacy .jsonl transcripts live beside the SQLite store; an absent store
	// yields nothing rather than an error the caller must special-case.
	if files := GooseJSONLFiles(); len(files) != 0 {
		t.Fatalf("empty store returned files: %v", files)
	}
	if ss := LoadGoose(); len(ss) != 0 {
		t.Fatalf("empty store returned sessions: %d", len(ss))
	}
}

func TestLoadersTolerateAbsentStores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{
		"DEJA_GROK_DB", "DEJA_GROK_ROOT", "DEJA_HERMES_HOME",
		"DEJA_HERMES_PROFILES_ROOT", "DEJA_KIMI_ROOT", "DEJA_QWEN_ROOT",
		"DEJA_OPENCLAW_ROOT",
	} {
		t.Setenv(v, "")
	}
	if ss := LoadGrokDB(); len(ss) != 0 {
		t.Fatalf("grok db: %d sessions from nothing", len(ss))
	}
	if files := HermesSessionFiles(); len(files) != 0 {
		t.Fatalf("hermes: %v", files)
	}
	if ss := LoadKimi(); len(ss) != 0 {
		t.Fatalf("kimi: %d", len(ss))
	}
	if ss := LoadQwen(); len(ss) != 0 {
		t.Fatalf("qwen: %d", len(ss))
	}
	if files := QwenSessionFiles(); len(files) != 0 {
		t.Fatalf("qwen files: %v", files)
	}
	if ss := LoadOpenClaw(); len(ss) != 0 {
		t.Fatalf("openclaw: %d", len(ss))
	}
	if runtime.GOOS == "windows" {
		return
	}
	// Reading a store deja does not have must not create one.
	if _, err := os.Stat(filepath.Join(home, ".grok")); err == nil {
		t.Fatal("reading an absent store created it")
	}
}

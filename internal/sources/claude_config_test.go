package sources

import (
	"path/filepath"
	"testing"
)

// Claude Code lets users relocate its state directory with CLAUDE_CONFIG_DIR
// (commonly to keep personal and work profiles apart). These tests pin down
// that ClaudeConfigDir/ClaudeJSONPath/ClaudeRoot follow that variable and fall
// back to the default ~/.claude layout when it is unset.
func TestClaudeConfigDirRespectsEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t.Run("unset falls back to ~/.claude", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		t.Setenv("DEJA_CLAUDE_ROOT", "")
		if got, want := ClaudeConfigDir(), filepath.Join(home, ".claude"); got != want {
			t.Fatalf("ClaudeConfigDir()=%q want %q", got, want)
		}
		if got, want := ClaudeJSONPath(), filepath.Join(home, ".claude.json"); got != want {
			t.Fatalf("ClaudeJSONPath()=%q want %q", got, want)
		}
		if got, want := ClaudeRoot(), filepath.Join(home, ".claude", "projects"); got != want {
			t.Fatalf("ClaudeRoot()=%q want %q", got, want)
		}
	})

	t.Run("set relocates config, json and projects", func(t *testing.T) {
		cfg := filepath.Join(home, "profiles", "personal")
		t.Setenv("CLAUDE_CONFIG_DIR", cfg)
		t.Setenv("DEJA_CLAUDE_ROOT", "")
		if got := ClaudeConfigDir(); got != cfg {
			t.Fatalf("ClaudeConfigDir()=%q want %q", got, cfg)
		}
		if got, want := ClaudeJSONPath(), filepath.Join(cfg, ".claude.json"); got != want {
			t.Fatalf("ClaudeJSONPath()=%q want %q", got, want)
		}
		if got, want := ClaudeRoot(), filepath.Join(cfg, "projects"); got != want {
			t.Fatalf("ClaudeRoot()=%q want %q", got, want)
		}
	})

	t.Run("DEJA_CLAUDE_ROOT still wins over CLAUDE_CONFIG_DIR", func(t *testing.T) {
		override := filepath.Join(home, "explicit-projects")
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "profiles", "work"))
		t.Setenv("DEJA_CLAUDE_ROOT", override)
		if got := ClaudeRoot(); got != override {
			t.Fatalf("ClaudeRoot()=%q want %q", got, override)
		}
	})
}

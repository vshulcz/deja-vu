package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// When CLAUDE_CONFIG_DIR points Claude Code at a non-default profile, deja must
// wire the MCP server, SessionStart hook and statusline into that profile
// rather than the default ~/.claude.json and ~/.claude/settings.json.
func TestInstallClaudeHonorsConfigDir(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "custom-claude")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	if _, err := installClaude("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHook("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installStatusline("/bin/deja", false); err != nil {
		t.Fatal(err)
	}

	mcp := filepath.Join(cfg, ".claude.json")
	settings := filepath.Join(cfg, "settings.json")
	for _, p := range []string{mcp, settings} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to be written: %v", p, err)
		}
	}
	if b, err := os.ReadFile(mcp); err != nil || !strings.Contains(string(b), `"deja"`) {
		t.Fatalf("mcp server not in %s: %s (%v)", mcp, b, err)
	}
	if b, err := os.ReadFile(settings); err != nil || !strings.Contains(string(b), "deja hook-context") || !strings.Contains(string(b), "deja statusline") {
		t.Fatalf("hook/statusline not in %s: %s (%v)", settings, b, err)
	}

	// The default ~/.claude location must be left untouched.
	for _, p := range []string{filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude", "settings.json")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("default location %s should not have been written", p)
		}
	}
}

func TestClaudeHookUninstallNeverLeavesNullHooks(t *testing.T) {
	root := map[string]any{}
	root = updateClaudeHook(root, "PreCompact", "/bin/deja hook-precompact", "manual|auto", false)
	root = updateClaudeHook(root, "PreCompact", "/bin/deja hook-precompact", "manual|auto", true)
	root = updateClaudeHook(root, "PreCompact", "/bin/deja hook-precompact", "manual|auto", false)
	entries, _ := root["hooks"].(map[string]any)["PreCompact"].([]any)
	if len(entries) != 1 {
		t.Fatalf("install/uninstall/install left %d entries, want 1: %#v", len(entries), entries)
	}
	for _, e := range entries {
		entry := e.(map[string]any)
		hs, ok := entry["hooks"].([]any)
		if !ok || len(hs) == 0 {
			t.Fatalf("entry with null/empty hooks survived: %#v", entry)
		}
	}
	// Pre-existing damage from older versions heals on the next install.
	damaged := map[string]any{"hooks": map[string]any{"PreCompact": []any{
		map[string]any{"matcher": "manual|auto", "hooks": nil},
		map[string]any{"matcher": "manual|auto", "hooks": []any{map[string]any{"type": "command", "command": "/bin/deja hook-precompact"}}},
	}}}
	healed := updateClaudeHook(damaged, "PreCompact", "/bin/deja hook-precompact", "manual|auto", false)
	entries2, _ := healed["hooks"].(map[string]any)["PreCompact"].([]any)
	if len(entries2) != 1 {
		t.Fatalf("null-hooks entry not healed: %#v", entries2)
	}
}

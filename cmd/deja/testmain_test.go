package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "deja-command-test-")
	if err != nil {
		panic(err)
	}
	stores := map[string]string{
		"HOME":        root,
		"USERPROFILE": root,
		// Neutralize ambient profile overrides so install/read paths resolve
		// under the temp HOME instead of the developer's real agent profiles.
		"CLAUDE_CONFIG_DIR":       "",
		"CODEX_HOME":              "",
		"GEMINI_CLI_HOME":         "",
		"CURSOR_CONFIG_DIR":       "",
		"AIDER_CHAT_HISTORY_FILE": "",
		"XDG_CONFIG_HOME":         "",
		"XDG_DATA_HOME":           "",
		"DEJA_CLAUDE_ROOT":        filepath.Join(root, "claude"),
		"DEJA_CODEX_ROOT":         filepath.Join(root, "codex"),
		"DEJA_OPENCODE_DB":        filepath.Join(root, "opencode.db"),
		"DEJA_AIDER_ROOTS":        filepath.Join(root, "aider"),
		"DEJA_GEMINI_ROOT":        filepath.Join(root, "gemini"),
		"DEJA_CURSOR_ROOT":        filepath.Join(root, "cursor"),
		"DEJA_CURSOR_CLI_ROOT":    filepath.Join(root, "cursor-cli"),
		"DEJA_ANTIGRAVITY_ROOT":   filepath.Join(root, "antigravity"),
		"DEJA_GROK_ROOT":          filepath.Join(root, "grok"),
		"DEJA_QWEN_ROOT":          filepath.Join(root, "qwen"),
	}
	for key, value := range stores {
		if err := os.Setenv(key, value); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

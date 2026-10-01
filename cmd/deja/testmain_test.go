package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/testenv"
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
		"APPDATA":                 filepath.Join(root, "AppData", "Roaming"),
		"LOCALAPPDATA":            filepath.Join(root, "AppData", "Local"),
		// A developer with DEJA_INDEX_DIR exported reads their own index
		// here, and the suite went red on their machine only.
		"DEJA_INDEX_DIR":       "",
		"DEJA_NOTES_FILE":      "",
		"DEJA_SOURCE_INSTANCE": "",
		// Guard: hook tests must never spawn a real detached warmup —
		// os.Executable() inside tests is the test binary itself.
		"DEJA_WARMUP_SENTINEL":  filepath.Join(root, "warmup-guard"),
		"DEJA_CLAUDE_ROOT":      filepath.Join(root, "claude"),
		"DEJA_CODEX_ROOT":       filepath.Join(root, "codex"),
		"DEJA_OPENCODE_DB":      filepath.Join(root, "opencode.db"),
		"DEJA_AIDER_ROOTS":      filepath.Join(root, "aider"),
		"DEJA_GEMINI_ROOT":      filepath.Join(root, "gemini"),
		"DEJA_CURSOR_ROOT":      filepath.Join(root, "cursor"),
		"DEJA_CURSOR_CLI_ROOT":  filepath.Join(root, "cursor-cli"),
		"DEJA_ANTIGRAVITY_ROOT": filepath.Join(root, "antigravity"),
		"DEJA_GROK_ROOT":        filepath.Join(root, "grok"),
		"DEJA_QWEN_ROOT":        filepath.Join(root, "qwen"),
	}
	// The version lookup is the one thing in this package that talks to the
	// internet, and only one test ever replaced it — so a full run asked
	// api.github.com eleven times, left HTTP/2 connections whose cleanup
	// goroutines outlived their tests, and raced the zone tests' writes to
	// time.Local under -race (#2206). A test that wants the real path can put
	// it back for itself.
	doctorLookup = offlineLookup
	// DEJA_NO_HOME_ARGS is how TestNoHomeHelperProcess reaches its own child.
	testenv.Scrub(stores, "DEJA_NO_HOME_ARGS")
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

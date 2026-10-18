package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/testenv"
)

func TestMain(m *testing.M) {
	// A copy of this binary stands in for deja where a test runs a hook line
	// through a real shell (codebuddy_windows_hook_test.go).
	if os.Getenv(hookEchoEnv) == "1" {
		hookEcho()
	}
	// And for deja itself, where a test drives a host's plugin against the
	// real hooks (install_hermes_notes_test.go).
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	openclawGatewayUp = func() bool { return false }
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
		"VSCODE_EXTENSIONS":       "",
		"AIDER_CHAT_HISTORY_FILE": "",
		"AIDER_READ":              "",
		"AIDER_GIT":               "",
		"XDG_CONFIG_HOME":         "",
		"XDG_DATA_HOME":           "",
		"APPDATA":                 filepath.Join(root, "AppData", "Roaming"),
		"LOCALAPPDATA":            filepath.Join(root, "AppData", "Local"),
		// A developer with DEJA_INDEX_DIR exported reads their own index
		// here, and the suite went red on their machine only.
		"DEJA_INDEX_DIR":       "",
		"DEJA_NOTES_FILE":      "",
		"DEJA_SOURCE_INSTANCE": "",
		// Set by grok on the hooks it runs, where hook-context and
		// hook-prompt answer nothing (#4588).
		"GROK_HOOK_EVENT": "",
		// Where `openclaw tui` connects; a gateway on the developer's
		// machine would turn resume's `openclaw chat` into `openclaw tui`.
		"OPENCLAW_GATEWAY_URL":  "",
		"OPENCLAW_GATEWAY_PORT": "",
		// Set by CodeWhale on the hooks it runs; a suite run from inside a
		// CodeWhale session would hand them to hook-codewhale.
		"DEEPSEEK_SESSION_ID":   "",
		"DEEPSEEK_TOOL_NAME":    "",
		"DEEPSEEK_TOOL_ARGS":    "",
		"DEEPSEEK_WORKSPACE":    "",
		"DEEPSEEK_TOOL_CALL_ID": "",
		"DEEPSEEK_TOOL_SUCCESS": "",
		"DEEPSEEK_TOOL_RESULT":  "",
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
		// Claude Code's managed settings win over the user's, and a machine
		// with a real file there would set cleanupPeriodDays for every test.
		"DEJA_CLAUDE_MANAGED_SETTINGS": filepath.Join(root, "no-managed-settings.json"),
		// An opencode on PATH is asked its version, which writes its log under
		// the test's home and decides which plugin shape doctor calls current:
		// three tests went red only where opencode was installed (#4155). "0"
		// reads as no opencode; a test that wants a major sets its own.
		"DEJA_OPENCODE_MAJOR": "0",
		// Copilot's own switch moves install and the session root (#4240).
		"COPILOT_HOME": "",
		// The interactive screen picks its colours and its clipboard from
		// these; a suite run from iTerm or over ssh would otherwise differ.
		"COLORTERM":       "",
		"TERM_PROGRAM":    "",
		"WT_SESSION":      "",
		"SSH_TTY":         "",
		"SSH_CONNECTION":  "",
		"WAYLAND_DISPLAY": "",
		"COLORFGBG":       "",
		"DEJA_THEME":      "",
		"DEJA_TUI":        "",
		// Dates render in the reader's zone (#849) and fixtures write fixed
		// instants, so seven tests went red at UTC-3, UTC-11 or UTC+14 (#5044).
		// A test about zones sets time.Local itself.
		"TZ": "UTC",
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
	time.Local = time.UTC
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

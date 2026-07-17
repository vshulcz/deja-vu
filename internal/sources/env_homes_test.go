package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Each harness that documents a relocation variable gets the same contract:
// the upstream variable moves the default, DEJA_* still wins.
func TestUpstreamHomeVariables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t.Run("codex CODEX_HOME", func(t *testing.T) {
		t.Setenv("DEJA_CODEX_ROOT", "")
		t.Setenv("CODEX_HOME", filepath.Join(home, "codex-profile"))
		if got := CodexRoot(); got != filepath.Join(home, "codex-profile") {
			t.Fatalf("CodexRoot=%q", got)
		}
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(home, "override"))
		if got := CodexRoot(); got != filepath.Join(home, "override") {
			t.Fatalf("DEJA_CODEX_ROOT must win, got %q", got)
		}
	})

	t.Run("gemini GEMINI_CLI_HOME appends .gemini", func(t *testing.T) {
		t.Setenv("DEJA_GEMINI_ROOT", "")
		t.Setenv("GEMINI_CLI_HOME", filepath.Join(home, "ghome"))
		if got := GeminiRoot(); got != filepath.Join(home, "ghome", ".gemini") {
			t.Fatalf("GeminiRoot=%q", got)
		}
		t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(home, "gr"))
		if got := GeminiRoot(); got != filepath.Join(home, "gr") {
			t.Fatalf("DEJA_GEMINI_ROOT must win, got %q", got)
		}
	})

	t.Run("cursor CURSOR_CONFIG_DIR", func(t *testing.T) {
		t.Setenv("DEJA_CURSOR_CLI_ROOT", "")
		t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, "cursor-cfg"))
		if got := CursorCLIRoot(); got != filepath.Join(home, "cursor-cfg") {
			t.Fatalf("CursorCLIRoot=%q", got)
		}
	})

	t.Run("opencode XDG_DATA_HOME linux only", func(t *testing.T) {
		t.Setenv("DEJA_OPENCODE_DB", "")
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
		got := OpencodeDB()
		want := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
		if runtime.GOOS == "linux" {
			want = filepath.Join(home, "xdg", "opencode", "opencode.db")
		}
		if got != want {
			t.Fatalf("OpencodeDB=%q want %q", got, want)
		}
	})

	t.Run("aider AIDER_CHAT_HISTORY_FILE", func(t *testing.T) {
		t.Setenv("DEJA_AIDER_ROOTS", "")
		hist := filepath.Join(home, "elsewhere", "history.md")
		if err := os.MkdirAll(filepath.Dir(hist), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hist, []byte("# aider chat started at 2026-01-01 00:00:00\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AIDER_CHAT_HISTORY_FILE", hist)
		found := false
		for _, f := range AiderFiles() {
			if f == hist {
				found = true
			}
		}
		if !found {
			t.Fatalf("AiderFiles missed AIDER_CHAT_HISTORY_FILE: %v", AiderFiles())
		}
	})

	t.Run("qwen DEJA_QWEN_ROOT", func(t *testing.T) {
		t.Setenv("DEJA_QWEN_ROOT", filepath.Join(home, "qwen-read"))
		if got := QwenRoot(); got != filepath.Join(home, "qwen-read") {
			t.Fatalf("QwenRoot=%q", got)
		}
		if got := QwenConfigDir(); got != filepath.Join(home, ".qwen") {
			t.Fatalf("QwenConfigDir=%q", got)
		}
	})
}

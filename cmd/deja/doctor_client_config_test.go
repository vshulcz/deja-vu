package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Kimi, Qwen, Cursor, Crush, ZCode and Command Code keep deja's hooks in the
// client's own config, which exists whether deja ever wrote to it or not. A
// file holding no deja entry is a machine that was never wired and reads
// missing; stale is for deja's own entry gone wrong (#4275).
func TestDoctorCallsAClientConfigWithoutDejaMissing(t *testing.T) {
	const hook = "/usr/local/bin/deja hook-precompact"
	for _, name := range []string{"kimi", "qwen", "cursor", "crush", "zcode", "commandcode"} {
		t.Run(name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("KIMI_CODE_HOME", "")
			t.Setenv("CURSOR_CONFIG_DIR", "")
			t.Setenv("CRUSH_GLOBAL_CONFIG", "")
			var a autoWiring
			for _, w := range autoWirings() {
				if w.name == name {
					a = w
				}
			}
			path := a.path()
			if !strings.HasPrefix(path, tmp) {
				t.Fatalf("%s config %s is outside the test home", name, path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(s string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			row := func() string {
				t.Helper()
				var buf bytes.Buffer
				doctorAutoRecall(&buf)
				for _, l := range strings.Split(buf.String(), "\n") {
					if strings.HasPrefix(l, "  "+name+" ") {
						return l
					}
				}
				t.Fatalf("no %s row:\n%s", name, buf.String())
				return ""
			}

			// The client's own settings, and an MCP server that is not a hook.
			if name == "kimi" {
				write("default_model = \"luna\"\n")
			} else {
				write(`{"model": "luna", "mcpServers": {"deja": {"command": "/usr/local/bin/deja", "args": ["mcp"]}}}`)
			}
			if state, _ := autoWiringState(a); state != "missing" {
				t.Errorf("a config with no deja hook: state %q, want missing", state)
			}
			if l := row(); !strings.Contains(l, "missing") || strings.Contains(l, "stale") {
				t.Errorf("a config with no deja hook: %q, want missing", l)
			}
			if !nothingWired() {
				t.Error("a config with no deja hook counted as a wired agent")
			}

			// Control: deja's own entry without the hook the row looks for.
			if name == "kimi" {
				write("default_model = \"luna\"\n\n" + kimiHookEntry("PreCompact", hook) + "\n")
			} else {
				write(`{"hooks": {"PreCompact": [{"hooks": [{"type": "command", "command": "` + hook + `"}]}]}}`)
			}
			if state, _ := autoWiringState(a); state != "stale" {
				t.Errorf("deja's entry without the %s call: state %q, want stale", a.marker, state)
			}
			if l := row(); !strings.Contains(l, "stale") {
				t.Errorf("deja's entry without the %s call: %q, want stale", a.marker, l)
			}
			if nothingWired() {
				t.Error("deja's own entry counted as nothing wired")
			}
		})
	}
}

// The brief's "no agent wired yet" line reads the same rows doctor prints. A
// Kimi plugin recalls with no block in config.toml, and Claude Code's hooks
// live outside the table; either is a wired agent beside a client config deja
// never wrote to (#4275).
func TestNothingWiredCountsThePluginAndClaudeHooks(t *testing.T) {
	t.Run("kimi plugin", func(t *testing.T) {
		tmp := hermeticEnv(t)
		home := filepath.Join(tmp, "kimi")
		t.Setenv("KIMI_CODE_HOME", home)
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("default_model = \"luna\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !nothingWired() {
			t.Fatal("control: a bare config.toml and no plugin already counts as wired")
		}
		writeKimiPlugin(t, home, true)
		if nothingWired() {
			t.Error("the Kimi plugin recalls on every prompt, and the brief says no agent is wired")
		}
	})
	// Codex's hooks.json is codex's own file: one holding only the user's
	// hooks is not deja's, whatever codex's trust store says about it.
	for _, trust := range []string{"no config.toml", "untrusted", "trusted"} {
		t.Run("codex user's own hooks only/"+trust, func(t *testing.T) {
			hermeticEnv(t)
			hooks := filepath.Join(sources.CodexHome(), "hooks.json")
			if err := os.MkdirAll(filepath.Dir(hooks), 0o755); err != nil {
				t.Fatal(err)
			}
			own := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/my-notes --start"}]}]}}`
			if err := os.WriteFile(hooks, []byte(own), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := ""
			switch trust {
			case "untrusted":
				cfg = "model = \"luna\"\n"
			case "trusted":
				cfg = "[hooks.state." + strconv.Quote(hooks+":session_start:0:0") + "]\ntrusted_hash = \"sha256:abc\"\n"
			}
			if cfg != "" {
				if err := os.WriteFile(filepath.Join(sources.CodexHome(), "config.toml"), []byte(cfg), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if !nothingWired() {
				t.Error("a codex hooks.json with only the user's own hooks counted as a wired agent")
			}
		})
	}
	// The Codex plugin carries deja's hooks under its own root, so a
	// hooks.json beside it with only the user's hook leaves codex wired.
	t.Run("codex plugin beside the user's hooks", func(t *testing.T) {
		hermeticEnv(t)
		home := sources.CodexHome()
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		own := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/my-notes --start"}]}]}}`
		if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(own), 0o644); err != nil {
			t.Fatal(err)
		}
		if !nothingWired() {
			t.Fatal("control: the user's own codex hook already counts as wired")
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[plugins.\"deja-vu@deja-vu\"]\nenabled = true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if nothingWired() {
			t.Error("the Codex plugin is enabled, and the brief says no agent is wired")
		}
	})
	t.Run("codex deja hooks", func(t *testing.T) {
		hermeticEnv(t)
		if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
			t.Fatalf("install codex-auto: %v", err)
		}
		if nothingWired() {
			t.Error("deja's codex hooks are wired, and the brief says no agent is")
		}
	})
	t.Run("claude hooks", func(t *testing.T) {
		hermeticEnv(t)
		qwen := filepath.Join(sources.QwenConfigDir(), "settings.json")
		if err := os.MkdirAll(filepath.Dir(qwen), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(qwen, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !nothingWired() {
			t.Fatal("control: an empty qwen settings.json already counts as wired")
		}
		if _, err := installClaudeAuto("/usr/local/bin/deja", false); err != nil {
			t.Fatalf("install claude-auto: %v", err)
		}
		if nothingWired() {
			t.Error("Claude Code's hooks are wired, and the brief says no agent is")
		}
	})
}

// A Windows install quotes a path with a space, and TOML escapes the quotes:
// the line has to be read unescaped to see the binary (#4275).
func TestDejaHookInReadsAnEscapedQuotedPath(t *testing.T) {
	line := `command = "\"C:/Program Files/deja/deja.exe\" hook-prompt --plain"` + "\n"
	if !dejaHookIn("[[hooks]]\nevent = \"UserPromptSubmit\"\n" + line) {
		t.Errorf("a hook naming a quoted Windows path is not read as deja's: %s", line)
	}
	if dejaHookIn("[[hooks]]\n" + `command = "\"C:/Program Files/other/tool.exe\" hook-prompt"` + "\n") {
		t.Error("another tool's hook-prompt read as deja's")
	}
}

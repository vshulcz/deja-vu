package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// regressHome points every harness and deja path at a fresh temp home.
func regressHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// goose and other Windows resolvers read APPDATA, not the home directory.
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, ".deja-index"))
	for _, k := range []string{"GOOSE_PATH_ROOT", "GROK_HOME", "TRAE_HOME", "TRAECLI_HOME", "CODEWHALE_HOME",
		"REASONIX_HOME", "REASONIX_STATE_HOME", "KILO_CONFIG_DIR", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(k, "")
	}
	return home
}

func regressWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func regressRun(t *testing.T, uninstall bool, args ...string) error {
	t.Helper()
	var err error
	captureStdout(t, func() { err = runInstall(filepath.Join(homeDir(), ".deja-index"), args, uninstall) })
	return err
}

// A server of the user's own that happens to be named "deja" is replaced on
// install, and the uninstall then deletes the .bak that held it: the original
// entry (a different program, with its env) is gone for good.
func TestUninstallKeepsTheSnapshotOfAForeignDejaEntry(t *testing.T) {
	home := regressHome(t)
	path := filepath.Join(home, ".claude.json")
	orig := "{\n  \"mcpServers\": {\n    \"deja\": {\"command\": \"/usr/local/bin/other-tool\", \"args\": [\"serve\"], \"env\": {\"K\": \"V\"}}\n  }\n}\n"
	regressWrite(t, path, orig)
	if err := regressRun(t, false, "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if err := regressRun(t, true, "claude-code"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	bak, _ := os.ReadFile(path + ".bak")
	if !strings.Contains(string(got), "other-tool") && !strings.Contains(string(bak), "other-tool") {
		t.Fatalf("the user's own \"deja\" server is gone from both the config and its snapshot:\nconfig: %s\nbak: %q", got, bak)
	}
}

// The launcher every hook runs lost its exec bit; doctor says reinstall
// fixes it, but install sees identical bytes and leaves the mode alone.
func TestReinstallRestoresTheLauncherExecBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	regressHome(t)
	if err := regressRun(t, false, "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	launcher := dejaLauncherPath()
	if err := os.Chmod(launcher, 0o644); err != nil {
		t.Fatal(err)
	}
	if claudeHookShell() != "" {
		if note := claudeHookRunNote(claudeHookWiringState().hooks); !strings.Contains(note, "exits 126") {
			t.Errorf("doctor's note for a launcher that cannot run: %q", note)
		}
	}
	if err := regressRun(t, false, "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("launcher still %v after the reinstall doctor recommends; every hook exits 126", fi.Mode())
	}
}

// doctor's "N of M events wired" line tells the reader to run a bare
// `deja install`, which refuses: install needs a target.
func TestDoctorOutOfDateAdviceIsARunnableCommand(t *testing.T) {
	regressHome(t)
	writeClaudeSettings(t, "SessionStart", "UserPromptSubmit")
	var out bytes.Buffer
	doctorHooks(&out)
	m := regexp.MustCompile("events wired — no [^;]*; run `deja ([^`]*)`").FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no out-of-date advice in:\n%s", out.String())
	}
	args := strings.Fields(m[1])
	if args[0] != "install" {
		t.Fatalf("advice is not an install: %q", m[1])
	}
	if err := regressRun(t, false, append(args[1:], "--no-index")...); err != nil {
		t.Fatalf("doctor advised `deja %s`, which fails: %v", m[1], err)
	}
}

func regressAutoState(t *testing.T, name string) string {
	t.Helper()
	for _, a := range autoWirings() {
		if a.name == name {
			s, _ := autoWiringState(a)
			return s
		}
	}
	t.Fatalf("no auto wiring row %q", name)
	return ""
}

// doctor calls auto-recall "stale" (and points at the -auto install) for a
// harness deja never hooked: CodeWhale's own config.toml, or the plain
// codewhale / reasonix MCP install, which writes the same file the hooks row reads.
func TestDoctorCallsAnUnhookedHarnessMissing(t *testing.T) {
	t.Run("codewhale user config", func(t *testing.T) {
		regressHome(t)
		regressWrite(t, codewhaleConfigPath(), "model = \"x\"\n")
		if s := regressAutoState(t, "codewhale"); s != "missing" {
			t.Fatalf("codewhale hooks row = %q for a config.toml deja never touched", s)
		}
	})
	t.Run("plain codewhale install", func(t *testing.T) {
		regressHome(t)
		if err := regressRun(t, false, "codewhale", "--no-index"); err != nil {
			t.Fatal(err)
		}
		if s := regressAutoState(t, "codewhale"); s != "missing" {
			t.Fatalf("codewhale hooks row = %q after the MCP-only target", s)
		}
	})
	t.Run("plain reasonix install", func(t *testing.T) {
		regressHome(t)
		if err := regressRun(t, false, "reasonix", "--no-index"); err != nil {
			t.Fatal(err)
		}
		if s := regressAutoState(t, "reasonix"); s != "missing" {
			t.Fatalf("reasonix hooks row = %q after the MCP-only target", s)
		}
	})
}

// opencode/kilocode uninstall adds an empty "mcp" block to a config deja
// never wrote (and leaves one behind after install, edit, uninstall). Every
// other MCP writer has the #676/#2604 guard; updateOpencodeJSON does not.
func TestOpencodeUninstallLeavesNoEmptyMCPBlock(t *testing.T) {
	const orig = "{\n  \"theme\": \"dark\"\n}\n"
	for _, tc := range []struct{ target, rel string }{
		{"opencode", ".config/opencode/opencode.json"},
		{"kilocode", ".config/kilo/kilo.json"},
	} {
		t.Run(tc.target+" never installed", func(t *testing.T) {
			home := regressHome(t)
			path := filepath.Join(home, tc.rel)
			regressWrite(t, path, orig)
			if err := regressRun(t, true, tc.target); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			if string(got) != orig {
				t.Fatalf("uninstall rewrote a config deja never touched:\n%s", got)
			}
		})
		t.Run(tc.target+" edited after install", func(t *testing.T) {
			home := regressHome(t)
			path := filepath.Join(home, tc.rel)
			regressWrite(t, path, orig)
			if err := regressRun(t, false, tc.target, "--no-index"); err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			b, _ := os.ReadFile(path)
			if err := json.Unmarshal(b, &root); err != nil {
				t.Fatal(err)
			}
			root["userEdit"] = 1.0
			b, _ = json.MarshalIndent(root, "", "  ")
			regressWrite(t, path, string(b)+"\n")
			if err := regressRun(t, true, tc.target); err != nil {
				t.Fatal(err)
			}
			b, _ = os.ReadFile(path)
			root = nil
			if err := json.Unmarshal(b, &root); err != nil {
				t.Fatal(err)
			}
			if _, ok := root["mcp"]; ok {
				t.Fatalf("uninstall left the \"mcp\" block install added:\n%s", b)
			}
		})
	}
}

// `deja uninstall qwen` fails on a settings.json with a comment in it — a
// file `deja install qwen` edits fine — because the qwen-auto status line
// writer parses strict JSON while the hook writer beside it blanks comments.
func TestQwenUninstallReadsCommentedSettings(t *testing.T) {
	regressHome(t)
	path := filepath.Join(sources.QwenConfigDir(), "settings.json")
	regressWrite(t, path, "// mine\n{\n  \"theme\": \"dark\"\n}\n")
	if err := regressRun(t, false, "qwen", "--no-index"); err != nil {
		t.Fatalf("install qwen: %v", err)
	}
	if err := regressRun(t, true, "qwen"); err != nil {
		t.Fatalf("uninstall qwen refused the file install just edited: %v", err)
	}
}

// deja's own goose recipe from an earlier install (another binary path, or
// an older template) is snapshotted as if it were the user's, and the uninstall
// puts it back: mentionsDeja does not recognise gooseRecipe's text.
func TestGooseUninstallDropsDejasOwnOlderRecipe(t *testing.T) {
	regressHome(t)
	recipe := gooseRecipePath()
	regressWrite(t, recipe, gooseRecipe("/old/place/deja"))
	if err := regressRun(t, false, "goose-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if err := regressRun(t, true, "goose-auto"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(recipe); err == nil {
		t.Fatalf("uninstall left deja's own recipe behind, naming a deja binary:\n%s", b)
	}
}

// A second identical install of grok-auto / trae-auto rewrites config.toml,
// reports "updated" and snapshots deja's own file as .bak.
func TestRepeatInstallLeavesTOMLWithStatusLineAlone(t *testing.T) {
	for _, tc := range []struct {
		target string
		path   func() string
	}{
		{"grok-auto", func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
		{"trae-auto", traeConfigPath},
	} {
		t.Run(tc.target, func(t *testing.T) {
			regressHome(t)
			if err := regressRun(t, false, tc.target, "--no-index"); err != nil {
				t.Fatal(err)
			}
			out := captureStdout(t, func() {
				if err := runInstall(filepath.Join(homeDir(), ".deja-index"), []string{tc.target, "--no-index"}, false); err != nil {
					t.Error(err)
				}
			})
			if _, err := os.Stat(tc.path() + ".bak"); err == nil {
				t.Errorf("a repeat install snapshotted deja's own %s", tc.path())
			}
			if strings.Contains(out, "updated "+shortHome(tc.path())) {
				t.Errorf("a repeat install reports a change:\n%s", out)
			}
		})
	}
}

// doctor calls Roo's auto-recall row "wired" on a machine where only codex
// was installed: the row reads the shared ~/.agents skill every harness writes.
func TestDoctorRooNotWiredByAnotherTargetsSkill(t *testing.T) {
	regressHome(t)
	if err := regressRun(t, false, "codex", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if s := regressAutoState(t, "roo"); s == "wired" {
		t.Fatalf("roo row = %q though roo was never installed", s)
	}
}

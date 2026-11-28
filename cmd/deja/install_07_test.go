package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// devinTestHome isolates a test from the machine's real Devin CLI config:
// HOME and the override envs all point at one temporary root.
func devinTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	return home
}

func TestDevinConfigDirFollowsThePlatformConventions(t *testing.T) {
	home := devinTestHome(t)
	for _, c := range []struct {
		name string
		goos string
		xdg  string
		app  string
		want string
	}{
		{"linux default", "linux", "", "", filepath.Join(home, ".config", "devin")},
		{"linux xdg", "linux", "/tmp/xdg-test", "", filepath.Join("/tmp/xdg-test", "devin")},               //nolint:gocritic // the want is joined the way the code joins it
		{"macos xdg honoured", "darwin", "/tmp/xdg-mac", "", filepath.Join("/tmp/xdg-mac", "devin")},       //nolint:gocritic // the want is joined the way the code joins it
		{"windows appdata", "windows", "", "C:\\win\\roaming", filepath.Join("C:\\win\\roaming", "devin")}, //nolint:gocritic // the want is joined the way the code joins it
		{"windows fallback", "windows", "", "", filepath.Join(home, "AppData", "Roaming", "devin")},
	} {
		t.Setenv("XDG_CONFIG_HOME", c.xdg)
		t.Setenv("APPDATA", c.app)
		if got := devinConfigDirFor(c.goos); got != c.want {
			t.Errorf("%s: devinConfigDirFor(%q) = %q, want %q", c.name, c.goos, got, c.want)
		}
	}
}

// installDevinHooks writes every wired event with its matcher through the
// same maintainer claude's install uses, and uninstall takes them back out.
func TestInstallDevinHooksRoundTrip(t *testing.T) {
	devinTestHome(t)
	res, err := installDevinHooks("deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatal("first install should have written config.json")
	}
	raw, err := os.ReadFile(devinConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if uerr := json.Unmarshal(raw, &root); uerr != nil {
		t.Fatal(uerr)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("config has no hooks object: %s", raw)
	}
	for _, h := range devinHookWiring {
		entries, ok := hooks[h.Event].([]any)
		if !ok || len(entries) == 0 {
			t.Errorf("event %s missing from config.json", h.Event)
			continue
		}
		blob, jerr := json.Marshal(entries)
		if jerr != nil {
			t.Fatal(jerr)
		}
		if h.Matcher != "" && !strings.Contains(string(blob), h.Matcher) {
			t.Errorf("event %s lost its matcher %q", h.Event, h.Matcher)
		}
		if !strings.Contains(string(blob), h.Sub) {
			t.Errorf("event %s does not run %q", h.Event, h.Sub)
		}
	}
	seen := map[string]bool{}
	for _, h := range devinHookWiring {
		seen[h.Event] = true
	}
	if len(hooks) != len(seen) {
		t.Errorf("hooks object has %d events, want %d", len(hooks), len(seen))
	}

	res, err = installDevinHooks("deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "unchanged" {
		t.Errorf("second install rewrote what it already wrote: %v", res)
	}
	if _, err := installDevinHooks("deja", true); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(devinConfigPath())
	if strings.Contains(string(raw), "hook-context") {
		t.Errorf("uninstall left deja wiring behind: %s", raw)
	}
}

// installDevinMCP puts the server into mcp_config.json and the shared skill
// into ~/.agents/skills — both come back out on uninstall.
func TestInstallDevinMCPRoundTrip(t *testing.T) {
	devinTestHome(t)
	if _, err := installDevinMCP("deja", false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(devinMCPConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	srvs, _ := root["mcpServers"].(map[string]any)
	deja, ok := srvs["deja"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_config.json has no deja server: %s", raw)
	}
	// The subcommand is the last word whether the command runs the binary
	// directly (posix) or through cmd /c on Windows; flatten both shapes.
	words := strings.Fields(fmt.Sprint(deja["command"]))
	if a, ok := deja["args"].([]any); ok {
		for _, v := range a {
			words = append(words, strings.Fields(fmt.Sprint(v))...)
		}
	}
	if len(words) == 0 || words[len(words)-1] != "mcp" {
		t.Errorf("deja server does not run `deja mcp`: %v", deja)
	}
	if _, err := os.Stat(sharedSkillPath()); err != nil {
		t.Errorf("skill file was not written to %s: %v", sharedSkillPath(), err)
	}
	if _, err := installDevinMCP("deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedSkillPath()); !os.IsNotExist(err) {
		t.Errorf("skill file survived uninstall")
	}
}

// Both install paths must surface a config that cannot be written rather
// than swallow it: a mangled config.json, and an mcp_config.json that is not
// a file at all.
func TestInstallDevinSurfacesConfigErrors(t *testing.T) {
	devinTestHome(t)
	if err := os.MkdirAll(filepath.Dir(devinConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(devinConfigPath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installDevinAuto("deja", false); err == nil {
		t.Error("a config.json that cannot be parsed must fail the install")
	}
	if err := os.Remove(devinConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(devinMCPConfigPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installDevinMCP("deja", false); err == nil {
		t.Error("an mcp_config.json that is a directory must fail the install")
	}
	if _, err := installDevinAuto("deja", false); err == nil {
		t.Error("devin-auto must surface the MCP failure even when hooks wrote fine")
	}
	if err := os.Remove(devinMCPConfigPath()); err != nil {
		t.Fatal(err)
	}
	// The MCP file writing fine is not enough: the shared skill has to land
	// too, and a path that cannot be made is an error, not silence. A plain
	// file where a directory belongs makes MkdirAll fail.
	skillsDir := filepath.Dir(filepath.Dir(sharedSkillPath()))
	if err := os.MkdirAll(filepath.Dir(skillsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillsDir, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installDevinMCP("deja", false); err == nil {
		t.Error("an unwritable skill path must fail the install")
	}
}

// devin-auto is both layers in one entry: hooks plus the MCP server.
func TestInstallDevinAutoWiresBothLayers(t *testing.T) {
	devinTestHome(t)
	if _, err := installDevinAuto("deja", false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{devinConfigPath(), devinMCPConfigPath()} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("devin-auto left %s unwritten: %v", path, err)
		}
	}
	if _, err := installDevinAuto("deja", true); err != nil {
		t.Fatal(err)
	}
}

// A config can end up with deja's entry twice: a hand-edited copy, a merge
// conflict resolved by keeping both sides, an upgrade that half-finished.
// Uninstalling has to take out every one of them — leaving a single copy means
// the harness goes on calling a binary the user removed, which is the failure
// they came to uninstall to avoid.
func TestUninstallRemovesEveryCopyOfItself(t *testing.T) {
	for _, tc := range []struct{ target, rel, doubled string }{
		{"qwen-auto", ".qwen/settings.json",
			`{"hooks":{"UserPromptSubmit":[` +
				`{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt","timeout":60000}]},` +
				`{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt","timeout":60000}]}` +
				`]}}`},
		{"codex-auto", ".codex/hooks.json",
			`{"hooks":{"SessionStart":[` +
				`{"matcher":"startup|resume","hooks":[{"type":"command","command":"/usr/local/bin/deja hook-context","timeout":10}]},` +
				`{"matcher":"startup|resume","hooks":[{"type":"command","command":"/usr/local/bin/deja hook-context","timeout":10}]}` +
				`]}}`},
	} {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.doubled), 0o644); err != nil {
				t.Fatal(err)
			}

			removingWiring = true
			_, err := installTarget(tc.target, "/usr/local/bin/deja", true)
			removingWiring = false
			if err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			left, rerr := os.ReadFile(path)
			if rerr != nil {
				return // the whole file was ours and went with it
			}
			if strings.Contains(string(left), "/usr/local/bin/deja") {
				t.Fatalf("uninstall left a copy of deja behind:\n%s", left)
			}
		})
	}

	// And installing onto a doubled config must collapse it rather than add a
	// third.
	t.Run("install-collapses", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
		path := filepath.Join(home, ".qwen", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		doubled := `{"hooks":{"UserPromptSubmit":[` +
			`{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt","timeout":60000}]},` +
			`{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt","timeout":60000}]}` +
			`]}}`
		if err := os.WriteFile(path, []byte(doubled), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installTarget("qwen-auto", "/usr/local/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(readFile(t, path), "hook-prompt"); n != 1 {
			t.Fatalf("install left %d copies of the hook:\n%s", n, readFile(t, path))
		}
	})
}

// Uninstall gives back the block deja wrote into as it found it: a block the
// reader had, empty, stays, and one deja added to an empty config goes. The
// hook writers, zed, VS Code, grok, prime and amp deleted the reader's, and
// opencode and kilo kept their own (#4562).
func TestUninstallGivesBackTheEmptyBlock(t *testing.T) {
	for _, c := range []struct {
		target, seed string
		path         func() string
	}{
		{"claude-auto", "{\n  \"hooks\": {}\n}\n", func() string { return filepath.Join(sources.ClaudeConfigDir(), "settings.json") }},
		{"codex-auto", "{\n  \"hooks\": {}\n}\n", func() string { return filepath.Join(sources.CodexHome(), "hooks.json") }},
		{"cursor-auto", "{\n  \"hooks\": {}\n}\n", func() string { return filepath.Join(sources.CursorCLIHome(), "hooks.json") }},
		{"commandcode-auto", "{\n  \"hooks\": {}\n}\n", commandCodeSettings},
		{"clawcode-auto", "{\n  \"hooks\": {}\n}\n", clawCodeSettings},
		{"zed", "{\n  \"context_servers\": {}\n}\n", sources.ZedSettingsPath},
		{"vscode", "{\n  \"servers\": {}\n}\n", func() string { return filepath.Join(vsCodeDefaultUserDir(), "mcp.json") }},
		{"grok", "{\n  \"mcp\": {\"servers\": []}\n}\n", func() string { return filepath.Join(sources.GrokHome(), "user-settings.json") }},
		{"prime", "{\n  \"mcpServers\": {}\n}\n", primeSettingsPath},
		{"amp", "{\n  \"amp.mcpServers\": {}\n}\n", sources.AmpSettingsFile},
		{"opencode", "{}\n", func() string { return filepath.Join(opencodeConfigHome(), "opencode", "opencode.json") }},
		{"kilocode", "{}\n", func() string { return filepath.Join(opencodeConfigHome(), "kilo", "kilo.json") }},
		// OpenClaw's config had no mcp key and kept `"mcp": {"servers": {}}`.
		{"openclaw-auto", "{\n  \"agents\": {\"defaults\": {\"model\": \"x\"}}\n}\n", openclawConfigPath},
		{"openclaw-auto", "// mine\n{\n  \"agents\": {\"defaults\": {\"model\": \"x\"}}\n}\n", openclawConfigPath},
		{"openclaw-auto", "{}\n", openclawConfigPath},
		{"openclaw", "{\n  \"agents\": {\"defaults\": {\"model\": \"x\"}}\n}\n", openclawConfigPath},
		// Hooks, server and status line go into one settings.json in three
		// writes, so the write that emptied the hooks block was not the last,
		// and by the last one the block was already gone from the file.
		{"qwen-auto", "{\n  \"hooks\": {}\n}\n", func() string { return filepath.Join(sources.QwenConfigDir(), "settings.json") }},
		{"qwen-auto", "{\n  \"$version\": 4,\n  \"hooks\": {},\n  \"ui\": {}\n}\n", func() string { return filepath.Join(sources.QwenConfigDir(), "settings.json") }},
		{"codebuddy-auto", "{\n  \"hooks\": {}\n}\n", codeBuddySettingsPath},
		{"openclaw-auto", "{\n  \"plugins\": {}\n}\n", openclawConfigPath},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); string(got) == c.seed {
				t.Fatalf("install did not write %s", p)
			}
			if _, err := captureRun(t, "uninstall", c.target); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); string(got) != c.seed {
				t.Errorf("uninstall gave back\n%s\nwant\n%s", got, c.seed)
			}
		})
	}
}

// The snapshot settles only what deja did. An empty block the reader added
// after install, or one they took out, is their change, and giving the
// snapshot back undid it (review of #4562).
func TestUninstallKeepsAnEmptyBlockChangedSinceInstall(t *testing.T) {
	for _, c := range []struct {
		name, seed string
		edit       func(map[string]any)
		has, lacks string
	}{
		{"added", "{\n  \"servers\": {}\n}\n", func(m map[string]any) { m["inputs"] = []any{} }, `"inputs"`, ""},
		{"removed", "{\n  \"servers\": {},\n  \"inputs\": []\n}\n", func(m map[string]any) { delete(m, "inputs") }, "", `"inputs"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			p := filepath.Join(vsCodeDefaultUserDir(), "mcp.json")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "vscode", "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(p)
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			c.edit(m)
			b, _ = json.MarshalIndent(m, "", "  ")
			if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "uninstall", "vscode"); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			if c.has != "" && !strings.Contains(string(got), c.has) {
				t.Errorf("the %s the reader added is gone:\n%s", c.has, got)
			}
			if c.lacks != "" && strings.Contains(string(got), c.lacks) {
				t.Errorf("the %s the reader removed is back:\n%s", c.lacks, got)
			}
			if strings.Contains(string(got), "deja") {
				t.Errorf("deja is still wired:\n%s", got)
			}
		})
	}
}

// An empty block a client saves while the uninstall edits the file is the
// client's: the retry edits its version, and measuring that against the file
// as the run first found it gave the snapshot back over it (review of #5184).
func TestUninstallKeepsAnEmptyBlockAClientSavedMidRun(t *testing.T) {
	for _, c := range []struct {
		target string
		path   func() string
	}{
		{"qwen-auto", func() string { return filepath.Join(sources.QwenConfigDir(), "settings.json") }},
		{"commandcode-auto", commandCodeSettings},
		{"clawcode-auto", clawCodeSettings},
		{"claude-auto", func() string { return filepath.Join(sources.ClaudeConfigDir(), "settings.json") }},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			seed := "{\n  \"hooks\": {}\n}\n"
			if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			saved := false
			beforeConfigReplace = func(path string) {
				if saved || filepath.Base(path) != filepath.Base(p) {
					return
				}
				saved = true
				b, _ := os.ReadFile(p)
				s := strings.Replace(string(b), "{", "{\n  \"clientBlock\": {},", 1)
				_ = os.WriteFile(p+".tmp", []byte(s), 0o644)
				_ = os.Rename(p+".tmp", p)
			}
			t.Cleanup(func() { beforeConfigReplace = func(string) {} })
			if _, err := captureRun(t, "uninstall", c.target); err != nil {
				t.Fatal(err)
			}
			if !saved {
				t.Fatal("the client save never ran")
			}
			got, _ := os.ReadFile(p)
			if !strings.Contains(string(got), "clientBlock") {
				t.Errorf("the block the client saved is gone:\n%s", got)
			}
		})
	}
}

// An empty config the reader created (`touch ~/.codex/config.toml`) came back
// deleted from install plus uninstall, and an empty JSON one came back `{}`
// (#4563). A file that existed comes back with its bytes, none included.
func TestUninstallKeepsAnEmptyConfigTheReaderHad(t *testing.T) {
	for _, c := range []struct {
		target, seed string
		path         func() string
	}{
		{"codex", "", func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"codex-auto", "", func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"hermes", "", func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }},
		{"kimi-auto", "", func() string { return filepath.Join(sources.KimiConfigDir(), "config.toml") }},
		{"goose-auto", "", func() string { return filepath.Join(gooseConfigDir(), "config.yaml") }},
		{"aider", "", func() string { return filepath.Join(homeDir(), ".aider.conf.yml") }},
		{"cursor", "", func() string { return filepath.Join(sources.CursorCLIHome(), "mcp.json") }},
		{"claude-code", "", sources.ClaudeJSONPath},
		{"claude-code", "\n\n", sources.ClaudeJSONPath},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "uninstall", c.target); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("uninstall deleted a config the reader had: %v", err)
			}
			if string(got) != c.seed {
				t.Errorf("uninstall gave back %q, want %q", got, c.seed)
			}
		})
	}
}

// The control: a config deja created goes with the uninstall.
func TestUninstallRemovesTheConfigItCreated(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.CodexHome(), "config.toml")
	if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("config deja created is still there: %v", err)
	}
}

// `deja install --auto` is the second command the README hands someone, and on a
// machine that has never run one of these agents it printed "no known agent
// config directories found" and stopped — a bare line read as a failure with
// nothing after it. Every other empty answer here names what it looked for and
// what to do next (#830); this holds that one to the same shape.
func TestInstallAutoOnAMachineWithNoAgentSaysWhatToDo(t *testing.T) {
	for _, uninstall := range []bool{false, true} {
		out := captureStdout(t, func() { noAgentConfigHere(uninstall) })
		if !strings.Contains(out, "no agent config found on this machine") {
			t.Errorf("uninstall=%v: does not say what happened: %q", uninstall, out)
		}
		// The number is the count of targets, not a hand-written one, or it
		// drifts the first time an agent is added.
		if !strings.Contains(out, "deja sources") {
			t.Errorf("uninstall=%v: does not point at where deja looked: %q", uninstall, out)
		}
		if strings.Contains(out, "all 0 agents") {
			t.Errorf("uninstall=%v: the count came out empty: %q", uninstall, out)
		}
		next := "run this again once an agent has written its first session"
		if uninstall {
			if strings.Contains(out, next) {
				t.Errorf("uninstall offers to wire something: %q", out)
			}
			if !strings.Contains(out, "nothing to remove") {
				t.Errorf("uninstall does not say it removed nothing: %q", out)
			}
			continue
		}
		if !strings.Contains(out, next) {
			t.Errorf("install does not say what to do next: %q", out)
		}
		if !strings.Contains(out, "deja install claude") {
			t.Errorf("install does not give a command that works: %q", out)
		}
	}
}

func dejaMCPEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if entry == nil {
		t.Fatalf("no deja entry left in %s:\n%s", path, b)
	}
	return entry
}

func writeClaudeMCP(t *testing.T, home string, entry map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude.json")
	b, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"deja": entry}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// entryArgs reads back the args of a decoded entry, which JSON hands over as
// []any.
func entryArgs(entry map[string]any) []string {
	raw, _ := entry["args"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// deja owns the command, the args and the type on its own entry. An env the
// reader added, or a disabled flag they set, is theirs — replacing the whole
// entry threw both away, and the note reported a replacement of the command by
// itself while saying nothing about what actually went (#2479).
func TestInstallKeepsWhatTheReaderPutOnDejasEntry(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := writeClaudeMCP(t, home, map[string]any{
		"command":  "/old/bin/deja",
		"args":     []string{"mcp"},
		"env":      map[string]any{"DEJA_INDEX_DIR": "/data/index.db"},
		"disabled": true,
	})

	res, err := installClaude("/new/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	entry := dejaMCPEntry(t, path)
	wantCommand, wantArgs := mcpCommandArgs("/new/bin/deja")
	if entry["command"] != wantCommand {
		t.Errorf("the command was not repointed: %v", entry["command"])
	}
	// Where the harness needs a shell in front of it, the exe is in the args
	// — asserting the exe as the command is asserting a Unix spelling.
	if got := entryArgs(entry); !slices.Equal(got, wantArgs) {
		t.Errorf("the args are not deja's own: %v, want %v", got, wantArgs)
	}
	env, _ := entry["env"].(map[string]any)
	if env["DEJA_INDEX_DIR"] != "/data/index.db" {
		t.Errorf("the reader's env is gone: %v", entry["env"])
	}
	if entry["disabled"] != true {
		t.Errorf("install switched a disabled server back on: %v", entry["disabled"])
	}
	if res.Note == "" {
		t.Errorf("the command really did change and the entry is disabled, and install said nothing")
	}
}

// The same install run twice, and an entry an older deja wrote: the command is
// the same either way, so there is nothing to report.
func TestInstallIsQuietWhenTheCommandDidNotChange(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	command, args := mcpCommandArgs("/bin/deja")
	writeClaudeMCP(t, home, map[string]any{"command": command, "args": args})

	res, err := installClaude("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Note != "" {
		t.Errorf("nothing the reader would call a replacement happened, but install says %q", res.Note)
	}
}

// Only onto an entry that was deja's. A remote-server shape someone put under
// the deja name is replaced whole: keeping its url beside deja's command would
// leave the client two answers to choose between.
func TestInstallDoesNotMergeIntoAnEntryThatIsNotDejas(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := writeClaudeMCP(t, home, map[string]any{"url": "https://example.invalid/mcp"})

	if _, err := installClaude("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	entry := dejaMCPEntry(t, path)
	if _, ok := entry["url"]; ok {
		t.Errorf("the remote shape survived beside deja's own wiring: %v", entry)
	}
	if want, _ := mcpCommandArgs("/bin/deja"); entry["command"] != want {
		t.Errorf("deja is not wired: %v", entry)
	}
}

// The gemini-auto target writes two things — the MCP entry and the hooks
// extension — and its result has to say so, the way every other -auto target
// folds its writes: an uninstall that removed the extension and left
// hooksConfig on said neither (#3185).
func TestGeminiAutoResultNamesTheExtension(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"GitHub"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Another extension runs on the switch, so uninstall leaves it on (#4216).
	other := filepath.Join(sources.GeminiHome(), "extensions", "other", "hooks", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(`{"hooks":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := installTarget("gemini-auto", "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in.Path+" "+in.Note, "extensions") {
		t.Errorf("install did not name the extension: path=%q note=%q", in.Path, in.Note)
	}
	out, err := installTarget("gemini-auto", "/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Path+" "+out.Note, "extensions") || !strings.Contains(out.Note, "hooksConfig") {
		t.Errorf("uninstall did not name the extension or the switch it left on: path=%q note=%q", out.Path, out.Note)
	}
}

// Gemini keeps one flat command namespace and everything deja installs lands in
// it: the MCP server's prompt is `/deja`, and every skill is a command too. A
// file of deja's collided with the prompt first (#3655) and, renamed to
// `deja-search`, collided with deja's own CLI skill — "Skill command
// '/deja-search' was renamed to '/deja-search1'", which is Gemini saying it out
// loud. So Gemini gets no command file of ours, and both names one used to have
// are taken back out (#3665).
func TestGeminiHasNoCommandFileOfItsOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GEMINI_CLI_HOME", "")
	t.Setenv("DEJA_GEMINI_ROOT", "")

	if path := commandFilePath("gemini"); path != "" {
		t.Errorf("command file = %q, want none — the skill is the command there", path)
	}

	// Both files an older deja wrote are dropped by the install, or the clash
	// outlives the fix for everyone who installed before.
	dir := filepath.Join(sources.GeminiHome(), "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []string{filepath.Join(dir, "deja.toml"), filepath.Join(dir, "deja-search.toml")}
	for _, p := range legacy {
		if err := os.WriteFile(p, []byte("description = \"(deja-vu)\"\nprompt = '''\nsearch\n'''\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := installCommandFile("gemini", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	for _, p := range legacy {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a command file deja used to write is still there: %s", p)
		}
	}

	// But a command of the reader's own under either name is theirs, and stays.
	mine := filepath.Join(dir, "deja.toml")
	body := "prompt = \"my own thing\"\n"
	if err := os.WriteFile(mine, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile("gemini", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(mine); err != nil {
		t.Errorf("the reader's own command file was removed: %v", err)
	} else if string(b) != body {
		t.Errorf("the reader's own command file was rewritten: %q", b)
	}
}

// Installing for Gemini turns on hooksConfig.enabled, a switch that belongs to
// the harness rather than to deja. With another extension running on it,
// uninstall leaves it on — and said nothing, while naming every other thing it
// kept (#2487).
func TestUninstallSaysItLeftGeminiHooksOn(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"GitHub"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = home
	// Another extension with hooks of its own is running on the switch.
	other := filepath.Join(sources.GeminiHome(), "extensions", "other", "hooks", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(`{"hooks":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	res, err := installGeminiAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	cfg, _ := root["hooksConfig"].(map[string]any)
	if enabled, _ := cfg["enabled"].(bool); !enabled {
		t.Fatalf("this test is about the switch staying on; it is off:\n%s", b)
	}
	if !strings.Contains(res.Note, "hooksConfig") {
		t.Errorf("uninstall left a harness-wide switch on and its note does not mention it: %q", res.Note)
	}
}

// Nothing to say when deja never turned it on.
func TestUninstallIsQuietWhenGeminiHooksAreOff(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	_ = home
	res, err := installGeminiAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Note, "hooksConfig") {
		t.Errorf("nothing was left on, but uninstall says %q", res.Note)
	}
}

// deja added the switch to a file that had none, and no extension is left to
// run on it: uninstall takes it out and the file comes back byte for byte
// (#4216). One the reader turned on before installing stays.
func TestUninstallTakesBackTheGeminiSwitchItAdded(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // my settings\n  \"security\": {\n    \"auth\": {\n      \"selectedType\": \"gemini-api-key\"\n    }\n  },\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"true\"\n    }\n  },\n  \"ui\": {\n    \"theme\": \"GitHub\"\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if !geminiHooksEnabled() {
		t.Fatal("install did not turn the switch on")
	}
	if _, err := captureRun(t, "uninstall", "gemini"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("settings.json did not come back as it was:\nwant:\n%s\ngot:\n%s", before, after)
	}

	// The reader's own switch, on before deja ever came.
	own := "{\n  \"hooksConfig\": {\n    \"enabled\": true\n  }\n}\n"
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "gemini"); err != nil {
		t.Fatal(err)
	}
	if !geminiHooksEnabled() {
		t.Fatal("uninstall turned off a switch the reader had on before installing")
	}
}

// Plain JSON, the shape most settings files have: install writes the switch in
// front of mcpServers, uninstall drops mcpServers, and the switch ends up the
// last key. Cutting it must not leave the comma before it behind — Gemini
// rejects the file.
func TestUninstallLeavesValidJSONWhenTheSwitchIsLast(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"ui":{"theme":"GitHub"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "gemini"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("uninstall left settings.json Gemini cannot parse (%v):\n%s", err, b)
	}
	if _, ok := root["hooksConfig"]; ok {
		t.Errorf("the switch deja added is still there:\n%s", b)
	}
}

// Hooks the reader wrote into settings.json, or a linked extension whose
// hooks live at its source, also run on the switch: uninstall leaves it on.
func TestUninstallKeepsTheGeminiSwitchOtherHooksNeed(t *testing.T) {
	for name, seed := range map[string]func(t *testing.T, path string){
		"settings hooks": func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte(`{"hooks":{"BeforeTool":[]}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"linked extension": func(t *testing.T, _ string) {
			t.Helper()
			meta := filepath.Join(sources.GeminiHome(), "extensions", "mine", ".gemini-extension-install.json")
			if err := os.MkdirAll(filepath.Dir(meta), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(meta, []byte(`{"source":"/src/mine","type":"link"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.GeminiHome(), "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			seed(t, path)
			if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "uninstall", "gemini"); err != nil {
				t.Fatal(err)
			}
			if !geminiHooksEnabled() {
				t.Fatal("uninstall turned off a switch other hooks still run on")
			}
		})
	}
}

// deja added the switch, the reader then changed it; a reinstall must not
// leave deja believing the object is still its own.
func TestReinstallForgetsASwitchTheReaderChanged(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if !blockWasAdded(path, "hooksConfig") {
		t.Fatal("install did not record the switch it added")
	}
	if err := os.WriteFile(path, []byte(`{"hooksConfig":{"enabled":true,"notifications":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if blockWasAdded(path, "hooksConfig") {
		t.Error("the reader's object is still recorded as deja's")
	}
}

// The cut must leave valid JSON whatever shape the reader gave the file:
// the switch last, first with its comma on the next line, or behind a
// comment.
func TestDisableGeminiHooksKeepsEveryShapeValid(t *testing.T) {
	for _, in := range []string{
		`{"ui":{"theme":"GitHub"},"hooksConfig":{"enabled":true}}`,
		"{\"hooksConfig\":{\"enabled\":true}\n, \"b\":1}",
		`{"hooksConfig":{"enabled":true} /*c*/, "b":1}`,
		"{\"a\":1, // x\n \"hooksConfig\":{\"enabled\":true}\n}",
		`{"hooksConfig":{"enabled":true}}`,
	} {
		hermeticEnv(t)
		path := filepath.Join(sources.GeminiHome(), "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		noteBlockAdded(path, "hooksConfig")
		if err := disableGeminiHooksIfOurs(); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(path)
		var root map[string]any
		if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
			t.Errorf("%q came out as %q, which Gemini cannot parse: %v", in, b, err)
			continue
		}
		if _, ok := root["hooksConfig"]; ok {
			t.Errorf("%q: the switch is still there: %q", in, b)
		}
	}
}

// deja's gallery extension is named deja, so `gemini extensions install`
// puts it in ~/.gemini/extensions/deja, the directory `deja install
// gemini-auto` writes its hooks to. The install replaced the gallery
// manifest (version, MCP server, context file) and the uninstall deleted the
// whole extension (gemini-cli 0.64.0).
func TestGeminiAutoKeepsTheExtensionGeminiInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".gemini", "extensions", "deja")
	seed := map[string]string{
		"gemini-extension.json":          "{\n  \"name\": \"deja\",\n  \"version\": \"0.22.2\",\n  \"contextFileName\": \"GEMINI.md\",\n  \"mcpServers\": {\"deja\": {\"command\": \"deja\", \"args\": [\"mcp\"]}}\n}\n",
		"GEMINI.md":                      "# deja\n",
		".gemini-extension-install.json": "{\n  \"source\": \"/src/deja-vu\",\n  \"type\": \"local\"\n}",
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gemini-extension.json")); string(b) != seed["gemini-extension.json"] {
		t.Fatalf("install rewrote the manifest gemini installed:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "gemini-extension.json.bak")); err == nil {
		t.Fatal("install left a .bak inside the extension")
	}
	geminiExtensionHooks(t, home)

	if _, err := installGeminiAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("uninstall deleted the extension gemini installed: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(entries) != len(seed) {
		t.Fatalf("after uninstall the extension holds %v, want only %d seeded files", names, len(seed))
	}
	for name, body := range seed {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != body {
			t.Fatalf("%s changed: %q", name, b)
		}
	}
}

// An extension linked with `gemini extensions link` is loaded from its source:
// hooks deja writes under ~/.gemini/extensions never run, while doctor read
// them as wired. The install adds no hooks and says why, still wires MCP, and
// doctor reads the file gemini reads.
func TestGeminiAutoLeavesALinkedExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	src := filepath.Join(home, "src", "deja-vu")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "gemini-extension.json"), []byte(`{"name": "deja", "version": "0.22.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".gemini", "extensions", "deja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"source": "` + filepath.ToSlash(src) + `", "type": "link"}`)
	if err := os.WriteFile(filepath.Join(dir, ".gemini-extension-install.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := installTarget("gemini-auto", "/bin/deja", false)
	if err != nil {
		t.Fatalf("install over a linked extension: %v", err)
	}
	if !strings.Contains(res.Note, "gemini extensions link") {
		t.Fatalf("note = %q, want it to name the link", res.Note)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".gemini", "settings.json")); !strings.Contains(string(b), "/bin/deja") {
		t.Fatalf("the MCP half was skipped: settings.json = %s", b)
	}
	for _, d := range []string{dir, src} {
		entries, _ := os.ReadDir(d)
		if len(entries) != 1 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("%s now holds %v", d, names)
		}
	}
	if got, want := geminiHooksPath(), filepath.Join(src, "hooks", "hooks.json"); got != want {
		t.Fatalf("doctor reads %s, gemini reads %s", got, want)
	}

	// What an older deja left beside the link goes on uninstall, the switch it
	// turned on included; the link stays.
	if _, err := enableGeminiHooks(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"hooks": {"BeforeAgent": [{"hooks": [{"type": "command", "command": "/bin/deja hook-prompt"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "hooks", "hooks.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gemini-extension.json"), geminiManifest(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("gemini-auto", "/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".gemini", "settings.json")); strings.Contains(string(b), "hooksConfig") {
		t.Fatalf("uninstall kept the hooksConfig an older deja added, counting deja's own link as another extension: %s", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != ".gemini-extension-install.json" {
		t.Fatalf("uninstall left %v beside the link", entries)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gemini-extension-install.json")); !bytes.Equal(b, meta) {
		t.Fatalf("link record changed: %s", b)
	}
}

// An older deja wrote its stub manifest over the gallery one and kept the
// original as .bak; the next install puts it back. A record gemini could not
// parse is not gemini's install, and a gallery release's own hooks are left.
func TestGeminiAutoRepairsAndRespectsTheGalleryExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".gemini", "extensions", "deja")
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	gallery := "{\"name\": \"deja\", \"version\": \"0.22.2\"}\n"
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gemini-extension-install.json", `{"source": "https://example.com/deja-vu", "type": "git"}`)
	write("gemini-extension.json", string(geminiManifest()))
	write("gemini-extension.json.bak", gallery)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gemini-extension.json")); string(b) != gallery {
		t.Fatalf("manifest not restored: %s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "gemini-extension.json.bak")); err == nil {
		t.Fatal(".bak left inside the extension")
	}

	write(filepath.Join("hooks", "hooks.json"), `{"hooks": {}}`)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	geminiExtensionHooks(t, home)
	if b, _ := os.ReadFile(filepath.Join(dir, "hooks", "hooks.json")); !strings.Contains(string(b), "hook-context") {
		t.Fatalf("an empty hooks file was kept as the extension's: %s", b)
	}

	theirs := `{"hooks": {"BeforeAgent": [{"hooks": [{"type": "command", "name": "lint", "command": "lint-it"}]}]}}`
	write(filepath.Join("hooks", "hooks.json"), theirs)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "hooks", "hooks.json")); string(b) != theirs {
		t.Fatalf("the extension's own hooks were replaced: %s", b)
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(".gemini-extension-install.json", "{")
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gemini-extension.json")); err != nil {
		t.Fatal("a broken install record kept deja from writing the manifest gemini needs")
	}
}

func geminiExtensionHooks(t *testing.T, home string) map[string][]map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".gemini", "extensions", "deja", "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("extension hooks missing: %v", err)
	}
	var root struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}
	// Gemini rejects the file outright when hooks is not an object.
	if root.Hooks == nil {
		t.Fatalf("hooks is not an object: %s", b)
	}
	return root.Hooks
}

func TestInstallGeminiWritesAnExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hooks := geminiExtensionHooks(t, home)
	group := hooks["SessionStart"]
	if len(group) == 0 {
		t.Fatalf("no SessionStart group: %v", hooks)
	}
	inner, _ := group[0]["hooks"].([]any)
	if len(inner) != 1 {
		t.Fatalf("hooks = %v", inner)
	}
	h, _ := inner[0].(map[string]any)
	if cmd, _ := h["command"].(string); !strings.HasPrefix(cmd, hookExeInConfigs("/bin/deja")) {
		t.Fatalf("command = %v", h["command"])
	}
	// Gemini reads timeout in milliseconds — a Claude-style 10 kills the hook.
	if to, _ := h["timeout"].(float64); to < 1000 {
		t.Fatalf("timeout = %v, too small for milliseconds", h["timeout"])
	}
	// The manifest is what makes the directory an extension.
	if _, err := os.Stat(filepath.Join(home, ".gemini", "extensions", "deja", "gemini-extension.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	// Loaded extensions still do nothing until the master switch is on.
	b, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatal(err)
	}
	cfg, _ := settings["hooksConfig"].(map[string]any)
	if enabled, _ := cfg["enabled"].(bool); !enabled {
		t.Fatalf("hooksConfig = %v, hooks stay dormant without it", settings["hooksConfig"])
	}
	// The settings.json hook older versions wrote never fired; installing must
	// clear it rather than leave a dead integration looking installed.
	if hooks, ok := settings["hooks"]; ok && hooks != nil {
		t.Fatalf("stale settings.json hook left behind: %v", hooks)
	}
}

// Gemini tags each hook's systemMessage with the hook's name, or with its
// whole command when it has none — the person read deja's receipt followed by
// "[/Users/…/deja-hook hook-prompt]".
func TestGeminiHooksAreNamedForTheirInfoLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	n := 0
	for event, groups := range geminiExtensionHooks(t, home) {
		for _, g := range groups {
			inner, _ := g["hooks"].([]any)
			for _, raw := range inner {
				h, _ := raw.(map[string]any)
				if h["name"] != "deja" {
					t.Errorf("%s hook %v has name %v, want deja", event, h["command"], h["name"])
				}
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("no hooks written")
	}
}

func TestInstallGeminiUninstallLeavesTheSwitchAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// The reader had it on before deja came.
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gemini", "settings.json"), []byte(`{"hooksConfig":{"enabled":true}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installGeminiAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "extensions", "deja")); !os.IsNotExist(err) {
		t.Fatalf("extension survived uninstall: %v", err)
	}
	// Other extensions may depend on hooksConfig, so it is not ours to switch off.
	b, _ := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	var settings map[string]any
	_ = json.Unmarshal(b, &settings)
	cfg, _ := settings["hooksConfig"].(map[string]any)
	if enabled, _ := cfg["enabled"].(bool); !enabled {
		t.Fatal("uninstall turned hooks off for every other extension too")
	}
}

// Kimi's SessionStart hook runs and its output goes nowhere, so the digest
// there would be work nobody reads. It rides the per-prompt hook instead, held
// to one by --once, and a SessionStart entry must never come back.
func TestKimiPutsTheDigestOnThePromptHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installKimiAuto("/bin/deja", false); err != nil {
		t.Fatalf("kimi: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	if !strings.Contains(cfg, "hook-context --kimi") || strings.Contains(cfg, `command = "/bin/deja hook-context"`) {
		t.Fatalf("SessionStart must use version-gated hook-context --kimi: %s", cfg)
	}
	if !strings.Contains(cfg, "hook-context --plain --once") {
		t.Fatalf("no session digest on the prompt hook: %s", cfg)
	}
	if !strings.Contains(cfg, "hook-prompt --plain") {
		t.Fatalf("no per-prompt recall: %s", cfg)
	}
}

// Qwen was written off as having no hooks twice over: its timeout is in
// milliseconds, so the 10 deja used to write killed the hook instantly, and
// SessionStart was thought to consume nothing. On qwen-code 0.20.0 it does —
// measured against a stub endpoint, a session with only UserPromptSubmit wired
// put a deja block in 0 of 2 requests and one with SessionStart added put it in
// 2 of 2.
func TestQwenHooksUserPromptSubmitInMilliseconds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_QWEN_ROOT", "")
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".qwen", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string  `json:"command"`
				Timeout float64 `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	start := root.Hooks["SessionStart"]
	if len(start) == 0 || len(start[0].Hooks) == 0 {
		t.Fatalf("no SessionStart hook, so a qwen session opens with no digest: %s", b)
	}
	if !strings.HasSuffix(start[0].Hooks[0].Command, "hook-context") {
		t.Fatalf("SessionStart runs %q, want the digest", start[0].Hooks[0].Command)
	}
	groups := root.Hooks["UserPromptSubmit"]
	if len(groups) == 0 || len(groups[0].Hooks) == 0 {
		t.Fatalf("no UserPromptSubmit hook: %s", b)
	}
	h := groups[0].Hooks[0]
	if !strings.HasSuffix(h.Command, "hook-prompt") {
		t.Fatalf("command = %q, want the prompt hook whose reply names UserPromptSubmit", h.Command)
	}
	if h.Timeout < 1000 {
		t.Fatalf("timeout = %v, too small for milliseconds", h.Timeout)
	}
}

// Gemini appends what an AfterTool hook returns to the tool result as
// <hook_context>, and deja was not listening: a gemini session got the project
// digest and the prompt answer, and nothing at the moment a command failed.
// Checked on gemini-cli 0.55.1 — the hook fires, its context reaches the model,
// and a matcher scopes it to the tool that runs commands.
func TestInstallGeminiWiresTheFixPair(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGeminiExtension("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".gemini", "extensions", "deja", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	for event, sub := range map[string]string{
		"AfterTool": "hook-tool-after",
	} {
		entries := root.Hooks[event]
		// The fix pair, and the file line after a read or an edit.
		if len(entries) != 2 {
			t.Fatalf("%s entries = %d, want 2: %s", event, len(entries), b)
		}
		if m := entries[1].Matcher; m != "read_file|replace|write_file|mcp_deja_.*|deja__.*" || !strings.HasSuffix(entries[1].Hooks[0].Command, "hook-tool") {
			t.Errorf("%s second entry = %q %q, want hook-tool on read_file|replace|write_file|mcp_deja_.*|deja__.*", event, m, entries[1].Hooks[0].Command)
		}
		if got := entries[0].Hooks[0].Command; !strings.HasSuffix(got, sub) {
			t.Errorf("%s runs %q, want %s", event, got, sub)
		}
		// Gemini reads the timeout in milliseconds; a Claude-style 10 kills the
		// hook before it can answer.
		if got := entries[0].Hooks[0].Timeout; got < 1000 {
			t.Errorf("%s timeout = %d, too short to be milliseconds", event, got)
		}
	}
	// Scoped to the tool that runs a command, so the hook does not spawn on
	// every read_file and glob.
	if m := root.Hooks["AfterTool"][0].Matcher; m != "run_shell_command" {
		t.Errorf("AfterTool matcher = %q, want the tool that runs commands", m)
	}
	// BeforeTool fires too, and what it returns never reaches the model — a
	// process per command for nothing.
	if len(root.Hooks["BeforeTool"]) != 0 {
		t.Errorf("BeforeTool is wired, and its output is not context: %s", b)
	}
}

// The names and the output field are gemini's own, and recognising them is what
// makes the wiring above say anything at all.
func TestGeminiToolNamesAndOutputAreRead(t *testing.T) {
	if !isCommandTool("run_shell_command") {
		t.Error("run_shell_command is not read as a command tool, so the fix pair never fires on gemini")
	}
	got := toolResponseText(json.RawMessage(`{"llmContent":"make: *** [test] Error 1"}`))
	if !strings.Contains(got, "Error 1") {
		t.Errorf("gemini's llmContent was not read: %q", got)
	}
	// And the frame gemini wraps it in comes off. The "Output:" marker is the
	// one that matters: with it in front, the first line of a build failure
	// stops reading as an error and the fix pair says nothing.
	wrapped := `{"llmContent":"<untrusted_context>\nOutput: ./main.go:12:2: undefined: zorbquuxHelper\nProcess Group PGID: 98883\n</untrusted_context>"}`
	got = toolResponseText(json.RawMessage(wrapped))
	if !strings.HasPrefix(strings.TrimSpace(got), "./main.go:12:2:") {
		t.Errorf("gemini's wrapper was not stripped: %q", got)
	}
}

// deja wrote the session-start recall to `.goosehints` in goose's config
// directory, and goose 1.48 does not read that file. Measured by pointing goose
// at a stub endpoint and reading the request it sent: a project-local
// `.goosehints`, a project `AGENTS.md` and `~/.config/goose/AGENTS.md` all
// arrive; the config-directory `.goosehints`, a `~/.goosehints` and an
// ancestor's do not. So the block existed, was refreshed every session, and
// never reached the model.
func TestGooseRecallGoesWhereGooseReadsIt(t *testing.T) {
	goose := gooseHomeForTest(t)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(goose, "AGENTS.md")); err != nil {
		t.Errorf("nothing at the path goose reads: %v", err)
	}
	if _, err := os.Stat(filepath.Join(goose, ".goosehints")); err == nil {
		t.Error("the file goose does not read was written anyway")
	}
}

// An install that upgrades over an older one has to clear the file deja used to
// write. A stale copy of somebody's history, in a file nothing reads, is the
// worst of both — invisible and wrong.
func TestGooseRecallClearsTheFileItUsedToWrite(t *testing.T) {
	goose := gooseHomeForTest(t)
	retired := filepath.Join(goose, ".goosehints")
	if err := os.MkdirAll(filepath.Dir(retired), 0o755); err != nil {
		t.Fatal(err)
	}
	// What an older deja actually wrote there: the recall frame, whole. The
	// fixture used to be a line of plain prose — which is what the reader's own
	// hints look like — and the code deleted the file on sight, so anyone
	// keeping global hints there lost them on every turn (#3196).
	if err := os.WriteFile(retired, []byte(frameRecall("recall from an older deja\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Errorf("the retired file survived the install: %v", err)
	}
}

// The same file, holding what the reader wrote. deja stopped writing here when
// the block moved to AGENTS.md, so there is nothing of deja's to take out and
// nothing to remove — on install, on uninstall, and on every hook-goose turn,
// which is where it was seen (#3196).
func TestGooseLeavesTheReadersOwnRetiredHints(t *testing.T) {
	goose := gooseHomeForTest(t)
	retired := filepath.Join(goose, ".goosehints")
	if err := os.MkdirAll(filepath.Dir(retired), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "my own hints, written by the user\nalways run the linter first\n"
	if err := os.WriteFile(retired, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { _, err := installGooseAuto("/bin/deja", false); return err },
		dropRetiredGooseHints,
		func() error { _, err := installGooseAuto("/bin/deja", true); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(retired)
		if err != nil {
			t.Fatalf("deja deleted hints it did not write: %v", err)
		}
		if string(b) != mine {
			t.Fatalf("the reader's hints changed:\n%s", b)
		}
	}

	// And a file holding both: deja's block comes out, theirs stays.
	both := mine + "\n" + gooseRecallStart + "\nold recall\n" + gooseRecallEnd + "\n"
	if err := os.WriteFile(retired, []byte(both), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRetiredGooseHints(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(retired)
	if err != nil {
		t.Fatalf("the file went with deja's block: %v", err)
	}
	if !strings.Contains(string(b), "always run the linter first") {
		t.Fatalf("the reader's half went with deja's:\n%s", b)
	}
	if strings.Contains(string(b), gooseRecallStart) {
		t.Fatalf("deja's block stayed:\n%s", b)
	}
}

// The file is the reader's. deja edits its own block and leaves the rest, and
// uninstall takes the block out rather than the file — removing it would delete
// whatever they had written there.
func TestGooseRecallLeavesTheRestOfAgentsAlone(t *testing.T) {
	goose := gooseHomeForTest(t)
	path := filepath.Join(goose, "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# my own notes\n\nalways use pgx, never database/sql\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "always use pgx") {
		t.Errorf("deja overwrote the reader's file:\n%s", b)
	}
	if !strings.Contains(string(b), gooseRecallStart) {
		t.Errorf("no recall block was added:\n%s", b)
	}

	if _, ierr := installGooseAuto("/bin/deja", true); ierr != nil {
		t.Fatal(ierr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("uninstall deleted the reader's file: %v", err)
	}
	if !strings.Contains(string(after), "always use pgx") {
		t.Errorf("uninstall took the reader's lines with it:\n%s", after)
	}
	if strings.Contains(string(after), gooseRecallStart) {
		t.Errorf("deja's block survived uninstall:\n%s", after)
	}
}

// A second refresh replaces the block rather than stacking another one, or a
// long-running install would grow the file a session at a time.
func TestGooseRecallReplacesItsOwnBlock(t *testing.T) {
	gooseHomeForTest(t)
	doc := gooseRecallBlock("", "first")
	doc = gooseRecallBlock(doc, "second")
	if n := strings.Count(doc, gooseRecallStart); n != 1 {
		t.Errorf("%d blocks after a refresh:\n%s", n, doc)
	}
	if strings.Contains(doc, "first") || !strings.Contains(doc, "second") {
		t.Errorf("the block was not replaced:\n%s", doc)
	}
}

// gooseHomeForTest gives the test a home of its own and returns goose's config
// directory under it, as goose resolves it on this OS.
func gooseHomeForTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")
	return gooseConfigDir()
}

// setTestHome points every home a resolver reads at home: HOME, USERPROFILE
// and the APPDATA pair, where goose keeps its config on Windows (#4286). A
// test setting only HOME read the package-wide APPDATA, so one test's goose
// config turned up in another's.
func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
}

// homeConfigPath is rel under home, except a file in goose's config
// directory, which is where gooseConfigDir puts it on this OS.
func homeConfigPath(home, rel string) string {
	if r, ok := strings.CutPrefix(rel, ".config/goose/"); ok {
		return filepath.Join(gooseConfigDir(), filepath.FromSlash(r))
	}
	return filepath.Join(home, filepath.FromSlash(rel))
}

// The hook is the whole of what -auto adds over the plain target, and it was
// written without a word: `deja install goose` followed by `deja install
// goose-auto` printed "unchanged" three times while switching session-start
// recall on, because the result described the config the first target had
// already written.
func TestGooseAutoSaysItWroteTheHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	exe := filepath.Join(home, "bin", "deja")
	if _, err := installTarget("goose", exe, false); err != nil {
		t.Fatal(err)
	}
	res, err := installTarget("goose-auto", exe, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != gooseHookPath() {
		t.Errorf("reported %q, want the hook it just wrote", res.Path)
	}
	if res.Action == "" || res.Action == "unchanged" {
		t.Errorf("action = %q over a file that did not exist", res.Action)
	}
	// And a second run is honestly unchanged, or every session start would
	// report maintenance that did not happen.
	again, err := installTarget("goose-auto", exe, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" {
		t.Errorf("a repeat run reported %q", again.Action)
	}

	// And the other half: the config edited by hand, the hook already right.
	// Reporting the hook blind would say "unchanged" over a config deja just
	// put back.
	config := filepath.Join(gooseConfigDir(), "config.yaml")
	if werr := os.WriteFile(config, []byte("GOOSE_PROVIDER: openai\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	back, err := installTarget("goose-auto", exe, false)
	if err != nil {
		t.Fatal(err)
	}
	if back.Action == "unchanged" {
		t.Errorf("the config was rewritten and the run reported %q", back.Action)
	}
	if back.Path != config {
		t.Errorf("reported %q, want the config it put back", back.Path)
	}
}

// Every other installer wires the binary its caller names. This one called
// os.Executable() instead, so the hook pointed at whatever process happened to
// run the install — the test binary under test, and the repair's own binary
// when a release moved deja somewhere else.
func TestTheGooseHookWiresTheBinaryItWasGiven(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	const want = "/opt/somewhere/else/deja"
	if _, err := installTarget("goose-auto", want, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(gooseHookPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), hookExeInConfigs(want)) {
		t.Errorf("the hook does not run what an install writes:\n%s", b)
	}
	// What the hook reaches: the config names the launcher, and the launcher
	// resolves the binary the caller named (#3422).
	reaches := string(b)
	if p := dejaLauncherPath(); p != "" {
		reaches = readFile(t, p)
	}
	if !strings.Contains(reaches, want) {
		t.Errorf("the hook cannot reach the binary it was given:\n%s", reaches)
	}
	if self, err := os.Executable(); err == nil && strings.Contains(reaches, self) {
		t.Errorf("the hook wired the running process instead:\n%s", reaches)
	}
}

// Goose declares commands inside config.yaml, a file holding provider settings
// someone wrote by hand. Install has to leave every other line of it — and
// every other slash command — exactly where it was.
func TestGooseCommandKeepsTheRestOfConfig(t *testing.T) {
	hermeticEnv(t)
	cfg := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	own := "# my notes\nGOOSE_PROVIDER: anthropic\n\nslash_commands:\n  - command: \"mine\"\n    recipe_path: \"/tmp/mine.yaml\"\n"
	if err := os.WriteFile(cfg, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installGooseCommand("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	for _, want := range []string{"# my notes", "GOOSE_PROVIDER: anthropic", `- command: "mine"`, `- command: "deja"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("config lost %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "slash_commands:") != 1 {
		t.Fatalf("slash_commands duplicated:\n%s", body)
	}
	if _, err := os.Stat(gooseRecipePath()); err != nil {
		t.Fatalf("recipe not written: %v", err)
	}

	// Re-running changes nothing, and uninstall takes back only our entry.
	if r, err := installGooseCommand("/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %#v, %v", r, err)
	}
	// A hand-written entry in the unquoted form must be replaced, not doubled.
	if err := os.WriteFile(cfg, []byte(own+"  - command: deja\n    recipe_path: \"/old.yaml\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGooseCommand("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(cfg)
	if n := strings.Count(string(got), `command: "deja"`) + strings.Count(string(got), "command: deja\n"); n != 1 {
		t.Fatalf("unquoted entry not replaced, %d deja commands:\n%s", n, got)
	}
	if strings.Contains(string(got), "/old.yaml") {
		t.Fatalf("the replaced entry kept its old recipe path:\n%s", got)
	}

	if _, err := installGooseCommand("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(cfg)
	body = string(got)
	if strings.Contains(body, `- command: "deja"`) {
		t.Fatalf("uninstall left our command:\n%s", body)
	}
	if !strings.Contains(body, `- command: "mine"`) || !strings.Contains(body, "# my notes") {
		t.Fatalf("uninstall took someone else's config with it:\n%s", body)
	}
	if _, err := os.Stat(gooseRecipePath()); !os.IsNotExist(err) {
		t.Fatalf("recipe survived uninstall: %v", err)
	}
}

// A config with no slash_commands key at all gets one, and a config that had
// only ours does not keep an empty key behind — Goose rejects a null value.
func TestGooseCommandAddsAndClearsTheKey(t *testing.T) {
	hermeticEnv(t)
	if _, err := installGooseCommand("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(gooseConfigDir(), "config.yaml")
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "slash_commands:\n  - command: \"deja\"") {
		t.Fatalf("key not created:\n%s", b)
	}
	if _, err := installGooseCommand("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cfg)
	if strings.Contains(string(b), "slash_commands:\n\n") || strings.TrimSpace(string(b)) == "slash_commands:" {
		t.Fatalf("empty slash_commands key left behind:\n%q", b)
	}
}

func gooseTestConfig(t *testing.T, cfg string) string {
	t.Helper()
	dir := gooseHomeForTest(t)
	path := filepath.Join(dir, "config.yaml")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readGooseTestConfig(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var gooseDejaKey = regexp.MustCompile(`(?m)^  ["']?deja["']? *:`)

// gooseFlowEntry is deja's entry body as this OS writes it, flow form.
func gooseFlowEntry(exe string) string {
	cmd, args := mcpCommandArgs(exe)
	return "{enabled: true, type: stdio, name: deja, cmd: " + yamlQuote(cmd) + ", args: [" + strings.Join(quoteAll(args), ", ") + "], timeout: 300}"
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = yamlQuote(s)
	}
	return out
}

func gooseBlockEntry(key string) string {
	cmd, args := mcpCommandArgs("/bin/deja")
	s := "  " + key + "\n    enabled: true\n    type: stdio\n    name: deja\n    cmd: " + yamlQuote(cmd) + "\n    args:\n"
	var sSb52 strings.Builder
	for _, a := range args {
		sSb52.WriteString("      - " + yamlQuote(a) + "\n")
	}
	s += sSb52.String()
	return s + "    timeout: 300\n"
}

// goose refuses a mapping that names a key twice, skips the whole config and
// writes a fresh one on its next save. Install matched only a bare `deja:`,
// so any other spelling got a second deja key beside it (#5190).
func TestInstallGooseEditsADejaKeySpelledAnyWay(t *testing.T) {
	const exe = "/bin/deja"
	mine := "  mine:\n    enabled: true\n    type: stdio\n    name: mine\n    cmd: /opt/mine/server\n    args: []\n"
	for _, tc := range []struct{ name, entry string }{
		{"double quoted", gooseBlockEntry(`"deja":`)},
		{"single quoted", gooseBlockEntry(`'deja':`)},
		{"comment after", gooseBlockEntry(`deja:   # memory`)},
		{"space before colon", gooseBlockEntry(`deja :`)},
		{"flow", "  deja: " + gooseFlowEntry(exe) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := gooseTestConfig(t, "GOOSE_PROVIDER: openai\nextensions:\n"+tc.entry+mine)
			if _, err := installGoose(exe, false); err != nil {
				t.Fatalf("install: %v", err)
			}
			got := readGooseTestConfig(t, path)
			if n := len(gooseDejaKey.FindAllString(got, -1)); n != 1 {
				t.Fatalf("%d deja keys after install, goose refuses the file with two:\n%s", n, got)
			}
			if !strings.Contains(got, "  mine:") || !strings.Contains(got, "GOOSE_PROVIDER: openai") {
				t.Fatalf("install dropped the reader's settings:\n%s", got)
			}
			if !doctorGooseWired(path) {
				t.Fatalf("doctor calls the entry not wired:\n%s", got)
			}
			if _, err := installGoose(exe, true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			got = readGooseTestConfig(t, path)
			if gooseDejaKey.MatchString(got) || !strings.Contains(got, "  mine:") {
				t.Fatalf("uninstall left deja or took the reader's entry:\n%s", got)
			}
		})
	}
}

// The reader's keys on deja's entry — the env deja's server runs with, a
// raised timeout, a tool filter — went on every install, the repair an
// upgrade runs included: the entry was rebuilt whole (#5191).
func TestInstallGooseKeepsTheReadersKeysOnDeja(t *testing.T) {
	const exe = "/bin/deja"
	// The entry as goose itself writes it (serde_yaml), at an old path.
	cfg := "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    name: deja\n    description: ''\n    cmd: /old/bin/deja\n    args:\n    - mcp\n" +
		"    envs:\n      DEJA_INDEX_DIR: /srv/idx\n    env_keys: []\n    timeout: 300\n    cwd: null\n    bundled: null\n    available_tools:\n    - deja\n" +
		"  developer:\n    enabled: true\n    type: platform\n    name: developer\nGOOSE_MODEL: gpt-5\n"
	path := gooseTestConfig(t, cfg)
	if _, err := installGoose(exe, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got := readGooseTestConfig(t, path)
	for _, want := range []string{"    description: ''\n", "    envs:\n      DEJA_INDEX_DIR: /srv/idx\n", "    env_keys: []\n", "    timeout: 300\n",
		"    cwd: null\n", "    available_tools:\n    - deja\n", "  developer:\n", "GOOSE_MODEL: gpt-5\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("install dropped %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/old/bin/deja") {
		t.Errorf("install left the old binary:\n%s", got)
	}
	cmd, args := mcpCommandArgs(exe)
	if !strings.Contains(got, cmd) || !strings.Contains(got, args[len(args)-1]) {
		t.Errorf("entry does not run this binary:\n%s", got)
	}
	if n := len(gooseDejaKey.FindAllString(got, -1)); n != 1 {
		t.Errorf("%d deja keys:\n%s", n, got)
	}
	again, err := installGoose(exe, false)
	if err != nil || again.Action != "unchanged" {
		t.Errorf("second install: %v %q, want unchanged", err, again.Action)
	}
}

// goose reads `enabled` as a YAML 1.2 bool and nothing else. `disabled:
// true` is not a field it has, and was taken as off, so install switched off
// a server goose ran; doctor compared the value to "false" and missed
// `False` (#5192).
func TestGooseReadsOnlyEnabledAsItsSwitch(t *testing.T) {
	const exe = "/bin/deja"
	cmd, args := mcpCommandArgs(exe)
	body := "    type: stdio\n    name: deja\n    cmd: " + yamlQuote(cmd) + "\n    args: [" + strings.Join(quoteAll(args), ", ") + "]\n    timeout: 60\n"
	for _, tc := range []struct {
		name, sw string
		off      bool
	}{
		{"disabled true", "    enabled: true\n    disabled: true\n", false},
		{"False", "    enabled: False\n", true},
		{"FALSE", "    enabled: FALSE\n", true},
		{"false", "    enabled: false\n", true},
		{"true", "    enabled: true\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := gooseTestConfig(t, "extensions:\n  deja:\n"+tc.sw+body)
			if said := clientMCPEntryOff("goose") != ""; said != tc.off {
				t.Errorf("doctor says off: %v, goose reads off: %v", said, tc.off)
			}
			res, err := installGoose(exe, false)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			got := readGooseTestConfig(t, path)
			if !strings.Contains(got, tc.sw) {
				t.Errorf("install changed the switch %q:\n%s", tc.sw, got)
			}
			if said := strings.Contains(res.Note, switchedOffNote); said != tc.off {
				t.Errorf("install says switched off: %v, goose reads off: %v", said, tc.off)
			}
		})
	}
}

// goose saves config.yaml through serde_yaml on its first start after
// goose-auto (it registers deja's plugin): quotes go and list items sit at
// their key's indent. Install compared its entries as text, so it rewrote
// both, moved slash_commands to the bottom, and doctor called deja's own
// /deja someone else's (#5193).
func TestInstallGooseKnowsItsEntriesAfterGooseRewritesThem(t *testing.T) {
	const exe = "/bin/deja"
	gooseHomeForTest(t)
	cmd, args := mcpCommandArgs(exe)
	ext := "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    name: deja\n    cmd: " + cmd + "\n    args:\n"
	var extSb181 strings.Builder
	for _, a := range args {
		extSb181.WriteString("    - " + a + "\n")
	}
	ext += extSb181.String()
	ext += "    timeout: 60\n  developer:\n    enabled: true\n    type: platform\n    name: developer\n"
	cfg := ext + "slash_commands:\n- command: deja\n  recipe_path: " + gooseRecipePath() + "\nproviders:\n  openai:\n    enabled: true\nactive_provider: openai\n"
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(gooseConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gooseRecipePath(), []byte(gooseRecipe(exe)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range doctorCommandFiles() {
		if c.name == "goose" {
			if s := c.state(); s != "written" {
				t.Errorf("doctor's /deja row says %q, want written", s)
			}
		}
	}
	if res, err := installGoose(exe, false); err != nil || res.Action != "unchanged" {
		t.Errorf("extension: %v %q, want unchanged", err, res.Action)
	}
	if res, err := installGooseCommand(exe, false); err != nil || res.Action != "unchanged" {
		t.Errorf("slash command: %v %q, want unchanged", err, res.Action)
	}
	if got := readGooseTestConfig(t, path); got != cfg {
		t.Errorf("config rewritten:\n%s", got)
	}
	// Uninstall still finds both.
	if _, err := installGooseCommand(exe, true); err != nil {
		t.Fatal(err)
	}
	if _, err := installGoose(exe, true); err != nil {
		t.Fatal(err)
	}
	got := readGooseTestConfig(t, path)
	if strings.Contains(got, "deja") || !strings.Contains(got, "  developer:") || !strings.Contains(got, "active_provider: openai") {
		t.Errorf("uninstall:\n%s", got)
	}
}

// Review cases: a comment or blank line before the first args item set the
// item indent to column 0, and uninstall stopped at a column-0 comment and
// left half the entry; both made a file goose refuses. A second extensions:
// or slash_commands: key is refused before the in-place edit, as before.
func TestInstallGooseEntryAroundCommentsAndTwoKeys(t *testing.T) {
	const exe = "/bin/deja"
	cfg := "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    cmd: /old/deja\n    args:\n# the subcommand\n\n      - serve\n    envs:\n      A: b\n  mine:\n    enabled: true\n"
	path := gooseTestConfig(t, cfg)
	if _, err := installGoose(exe, false); err != nil {
		t.Fatal(err)
	}
	got := readGooseTestConfig(t, path)
	if regexp.MustCompile(`(?m)^- `).MatchString(got) || !strings.Contains(got, "    envs:\n      A: b\n") {
		t.Fatalf("args item at column 0 or envs lost:\n%s", got)
	}
	if _, err := installGoose(exe, true); err != nil {
		t.Fatal(err)
	}
	got = readGooseTestConfig(t, path)
	if strings.Contains(got, "serve") || strings.Contains(got, "mcp") || strings.Contains(got, "A: b") || !strings.Contains(got, "  mine:\n") {
		t.Fatalf("uninstall left part of the entry:\n%s", got)
	}

	gooseTestConfig(t, "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    cmd: /bin/deja\n    args: [mcp]\nextensions:\n  mine:\n    enabled: true\n")
	if _, err := installGoose(exe, false); err == nil {
		t.Error("two extensions: keys accepted")
	}
	gooseTestConfig(t, "slash_commands:\n- command: deja\n  recipe_path: "+gooseRecipePath()+"\nslash_commands:\n- command: mine\n")
	if _, err := installGooseCommand(exe, false); err == nil {
		t.Error("two slash_commands: keys accepted")
	}
	// A refused command writes nothing: the recipe used to be written first and
	// then stayed on disk with no entry pointing at it (#5296).
	if _, err := os.Stat(gooseRecipePath()); !os.IsNotExist(err) {
		t.Errorf("a refused duplicate key left the recipe behind: %v", err)
	}
	gooseTestConfig(t, "slash_commands: []\n")
	if _, err := installGooseCommand(exe, false); err == nil {
		t.Error("an inline slash_commands: value accepted")
	}
	if _, err := os.Stat(gooseRecipePath()); !os.IsNotExist(err) {
		t.Errorf("a refused inline value left the recipe behind: %v", err)
	}
}

// The recipe is still refreshed when the config already holds the entry: only
// a refusal skips it, not the "unchanged" returns.
func TestInstallGooseCommandRefreshesTheRecipeForAnEntryAlreadyThere(t *testing.T) {
	const oldExe, newExe = "/old/bin/deja", "/new/bin/deja"
	gooseTestConfig(t, "slash_commands:\n- command: \"deja\"\n  recipe_path: \""+gooseRecipePath()+"\"\n")
	if err := os.WriteFile(gooseRecipePath(), []byte(gooseRecipe(oldExe)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGooseCommand(newExe, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(gooseRecipePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != gooseRecipe(newExe) {
		t.Errorf("the recipe still runs the old binary:\n%s", got)
	}
}

// --no-guidance writes no /deja recipe, and install said it had created one
// anyway: the goose-auto line named deja-recipe.yaml whatever the flag was
// (#5589). With the flag the line is gone and so is the file; without it both
// are there.
func TestGooseAutoNoGuidanceDoesNotNameTheRecipe(t *testing.T) {
	hermeticEnv(t)
	out, errOut := captureBoth(t, "install", "goose-auto", "--no-guidance", "--no-index")
	if strings.Contains(out+errOut, "deja-recipe.yaml") {
		t.Errorf("--no-guidance names a recipe it did not write:\n%s%s", out, errOut)
	}
	if _, err := os.Stat(gooseRecipePath()); !os.IsNotExist(err) {
		t.Errorf("--no-guidance wrote the recipe: %v", err)
	}

	hermeticEnv(t)
	out, errOut = captureBoth(t, "install", "goose-auto", "--no-index")
	if !strings.Contains(out+errOut, "also created "+shortHome(gooseRecipePath())) {
		t.Errorf("install no longer names the recipe it wrote:\n%s%s", out, errOut)
	}
	if _, err := os.Stat(gooseRecipePath()); err != nil {
		t.Errorf("install did not write the recipe: %v", err)
	}
}

// goose registers every plugin it finds under `plugins:` in config.yaml, keyed
// by the plugin's directory, on its first start. Uninstall removed deja's
// plugin directory and left that key pointing at nothing (#4270).
func TestGooseAutoUninstallDropsGoosesPluginEntry(t *testing.T) {
	hermeticEnv(t)
	exe := filepath.Join(t.TempDir(), "deja")
	config := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "GOOSE_PROVIDER: openai\n"
	if err := os.WriteFile(config, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("goose-auto", exe, false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Dir(filepath.Dir(gooseHookPath()))
	// What goose 1.46 appends on its first start with the plugin there.
	b, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(homeDir(), ".agents", "plugins", "mine")
	started := string(b) + "plugins:\n  " + plugin + ":\n    enabled: true\n"
	if err := os.WriteFile(config, []byte(started), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("goose-auto", exe, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(config); string(got) != want {
		t.Errorf("config.yaml after uninstall = %q, want %q", got, want)
	}

	// Another plugin's entry stays, and so does the map it is in.
	if _, err := installTarget("goose-auto", exe, false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(config)
	started = string(b) + "plugins:\n  '" + other + "':\n    enabled: false\n  '" + plugin + "':\n    enabled: true\n"
	if err := os.WriteFile(config, []byte(started), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("goose-auto", exe, true); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, config), "GOOSE_PROVIDER: openai\nplugins:\n  '"+other+"':\n    enabled: false\n"; got != want {
		t.Errorf("config.yaml with another plugin = %q, want %q", got, want)
	}
}

func TestGooseHookCoversTheUserPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	action, err := writeGooseHook("/usr/local/bin/deja")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	// -auto adds the hook and nothing else; a caller that cannot tell it what
	// happened reports "unchanged" over a file it just created (#2953).
	if action == "" || action == "unchanged" {
		t.Errorf("writing the hook reported %q", action)
	}
	b, err := os.ReadFile(gooseHookPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v", err)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		entries, ok := doc.Hooks[event]
		if !ok || len(entries) == 0 || len(entries[0].Hooks) == 0 {
			t.Fatalf("no %s hook:\n%s", event, b)
		}
		if !strings.Contains(entries[0].Hooks[0].Command, "hook-goose") {
			t.Errorf("%s runs %q", event, entries[0].Hooks[0].Command)
		}
	}
	if cmd := doc.Hooks["UserPromptSubmit"][0].Hooks[0].Command; !strings.Contains(cmd, "hook-goose-prompt") {
		t.Errorf("the prompt hook runs the session-start path: %q", cmd)
	}
}

// Only the MOIM file is re-read per turn; .goosehints is read once, when the
// session opens. Rewriting the hints file mid-session would replace recall the
// model has already been given with text it will never see.
func TestGoosePromptHookLeavesTheHintsFileAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")

	hints := gooseHintsPath()
	if err := os.MkdirAll(filepath.Dir(hints), 0o755); err != nil {
		t.Fatal(err)
	}
	const opening = "what the session opened with\n"
	if err := os.WriteFile(hints, []byte(opening), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refreshGooseForPrompt(t.TempDir(), []byte(`{"message":"why is the pool exhausted?"}`)); err != nil {
		t.Fatalf("prompt hook: %v", err)
	}
	after, err := os.ReadFile(hints)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != opening {
		t.Fatalf("the hints file was rewritten mid-session:\n%s", after)
	}
}

// A prompt the history has nothing for must leave whatever is already in front
// of the model in place, rather than blanking it.
func TestGoosePromptHookKeepsTheDigestWhenItHasNothingToSay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	moim := filepath.Join(home, "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)

	const digest = "the digest from session start\n"
	if err := os.WriteFile(moim, []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}
	// An empty index has nothing to say about anything.
	if err := refreshGooseForPrompt(t.TempDir(), []byte(`{"message":"pgbouncer pool sizing"}`)); err != nil {
		t.Fatalf("prompt hook: %v", err)
	}
	after, err := os.ReadFile(moim)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != digest {
		t.Fatalf("a silent recall blanked the file:\n%s", after)
	}
}

// `/deja` took no argument. Typing `/deja pgbouncer timeout` answered "Recipe
// /deja: Unexpected positional argument: pgbouncer" — goose does not map
// positionals onto a recipe, so the only working form is a named parameter,
// and a recipe that declares one has to use it or goose refuses the whole
// recipe with "Unnecessary parameter definitions".
//
// Measured against goose 1.48 with a stub endpoint: with the parameter declared
// and referenced, `/deja --query "pgbouncer timeout"` puts the query in front
// of the model, and bare `/deja` still carries the instructions.
func TestTheGooseRecipeTakesAQuery(t *testing.T) {
	r := gooseRecipe("/usr/local/bin/deja")

	if !strings.Contains(r, "key: query") {
		t.Errorf("no query parameter is declared:\n%s", r)
	}
	// Declared and used, or goose rejects the recipe outright.
	if !strings.Contains(r, "{{ query }}") {
		t.Errorf("the parameter is declared and never used:\n%s", r)
	}
	// Optional, or the bare `/deja` a reader types first stops working.
	if !strings.Contains(r, "requirement: optional") {
		t.Errorf("the parameter is not optional:\n%s", r)
	}
	// And the instructions are still there: the query is what to look for, the
	// instructions are how to look.
	if !strings.Contains(r, "instructions: |") {
		t.Errorf("the recipe lost its instructions:\n%s", r)
	}
}

// The parameter block sits after the instructions, not inside them: indented
// under `instructions: |` it would be part of the prose the model reads rather
// than something goose parses.
func TestTheGooseRecipeParameterIsNotSwallowedByTheInstructions(t *testing.T) {
	r := gooseRecipe("/usr/local/bin/deja")
	params := strings.Index(r, "parameters:")
	if params < 0 {
		t.Fatalf("no parameters block:\n%s", r)
	}
	line := r[params:]
	if strings.HasPrefix(line, "  parameters:") {
		t.Errorf("the parameters block is indented into the instructions:\n%s", r)
	}
	for l := range strings.SplitSeq(r, "\n") {
		if strings.TrimSpace(l) == "parameters:" && l != "parameters:" {
			t.Errorf("parameters is written at %q, want the first column", l)
		}
	}
}

// An install and uninstall of goose-auto gives the reader's AGENTS.md back
// byte for byte: the block went in after a blank line and came out without
// it, two newlines longer. Install also said it created a file that was
// there, and the closing line left out the snapshot it took of it (#4269).
func TestGooseAutoRoundTripGivesAGENTSmdBack(t *testing.T) {
	hermeticEnv(t)
	dir := gooseConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hints := gooseHintsPath()
	want := "Always run tests before saying done.\n"
	if err := os.WriteFile(hints, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("GOOSE_PROVIDER: openai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut := captureBoth(t, "install", "goose-auto", "--no-index")
	if strings.Contains(out+errOut, "created "+shortHome(hints)) {
		t.Errorf("install says it created %s, which was there:\n%s%s", shortHome(hints), out, errOut)
	}
	out, errOut = captureBoth(t, "uninstall", "goose-auto")
	got, err := os.ReadFile(hints)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("AGENTS.md after the round trip = %q, want %q", got, want)
	}
	baks, _ := filepath.Glob(filepath.Join(dir, "*.bak"))
	if len(baks) > 0 && !strings.Contains(out+errOut, "kept "+itoa(len(baks))+" snapshot") {
		t.Errorf("%d snapshots beside goose's files, the closing line says:\n%s%s", len(baks), out, errOut)
	}
}

// The block comes out with the separator it went in with, wherever it sits.
func TestGooseRecallBlockComesOutWhole(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	for _, doc := range []string{"one\n", "# notes\n\nkeep this\n", "one\n\n\n"} {
		if err := os.WriteFile(path, []byte(gooseRecallBlock(doc, "recall\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := dropGooseRecallBlock(path); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		// The writer trims the reader's trailing blank lines before its own
		// separator, so those cannot come back; the text and its last newline
		// do.
		if want := strings.TrimRight(doc, "\n") + "\n"; string(got) != want {
			t.Errorf("%q came back as %q, want %q", doc, got, want)
		}
	}
	// Text the reader added after deja's block stays, one blank line above it.
	installed := gooseRecallBlock("one\n", "recall\n") + "\ntwo\n"
	if err := os.WriteFile(path, []byte(installed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropGooseRecallBlock(path); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "one\n\ntwo\n" {
		t.Errorf("block between the reader's lines came out as %q", got)
	}
	// A refresh rewrites the block with the blank line after it gone; the
	// reader's two paragraphs still come back apart, not joined into one.
	refreshed := gooseRecallBlock(installed, "newer recall\n")
	if err := os.WriteFile(path, []byte(refreshed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropGooseRecallBlock(path); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "one\n\ntwo\n" {
		t.Errorf("block between the reader's lines, after a refresh, came out as %q", got)
	}
}

// Goose keeps the slash command and the MCP extension in one config.yaml, and
// each writer removed its block and added it back — which, when the block held
// only deja's entry, took the key with it and re-appended it at the bottom.
// The two keys swapped places on every install, the report said "updated"
// twice with nothing to update, and a config.yaml in a dotfiles repository
// showed a diff after each upgrade's repair (#3689).
func TestInstallGooseTwiceChangesNothingTheSecondTime(t *testing.T) {
	hermeticEnv(t)
	// Through the resolver, not a ~/.config join: goose reads its config from
	// %APPDATA% on Windows.
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	if _, err := captureRun(t, "install", "goose", "--no-index"); err != nil {
		t.Fatalf("install goose: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "goose", "--no-index")
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("a repeat install rewrote config.yaml:\n--- first\n%s\n--- second\n%s", first, second)
	}
	// And it says so: an "updated" nobody can point at a change for is the
	// half of this that a reader sees.
	for _, line := range []string{"goose: command unchanged", "goose: unchanged"} {
		if !strings.Contains(out, line) {
			t.Errorf("a repeat install did not report %q:\n%s", line, out)
		}
	}
}

func gooseConf(t *testing.T, goose string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goose, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml missing: %v", err)
	}
	return string(b)
}

func TestInstallGooseWritesTheExtension(t *testing.T) {
	goose := gooseHomeForTest(t)
	if _, err := installGoose("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := gooseConf(t, goose)
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
	goose := gooseHomeForTest(t)
	exe := "/opt/deja: v#2/deja"
	if _, err := installGoose(exe, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := gooseConf(t, goose)
	if !strings.Contains(conf, `"/opt/deja: v#2/deja"`) {
		t.Fatalf("exe path is not YAML-quoted, so Goose cannot parse it:\n%s", conf)
	}
	// The raw path on a value line would break the YAML at the ": ".
	if strings.Contains(conf, "- /opt/deja: v#2/deja") || strings.Contains(conf, "cmd: /opt/deja: v#2/deja") {
		t.Fatalf("raw unquoted path written, breaks the YAML:\n%s", conf)
	}
}

func TestInstallGooseKeepsOtherExtensions(t *testing.T) {
	goose := gooseHomeForTest(t)
	dir := goose
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
	conf := gooseConf(t, goose)
	for _, want := range []string{"GOOSE_PROVIDER: openai", "  memory:", "type: builtin", "  deja:"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("install dropped %q:\n%s", want, conf)
		}
	}
	if _, err := installGoose("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	conf = gooseConf(t, goose)
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
	goose := gooseHomeForTest(t)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hints := filepath.Join(goose, "AGENTS.md")
	b, err := os.ReadFile(hints)
	if err != nil {
		t.Fatalf("hints not written: %v", err)
	}
	// Inside deja's markers, because the file is the reader's.
	if !strings.Contains(string(b), gooseRecallStart) || !strings.Contains(string(b), gooseRecallEnd) {
		t.Errorf("the recall is not in a marked block:\n%s", b)
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
	gooseHomeForTest(t)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hook := filepath.Join(homeDir(), ".agents", "plugins", "deja", "hooks", "hooks.json")
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
	goose := gooseHomeForTest(t)
	// AGENTS.md in goose's config directory: the `.goosehints` beside it never
	// reached the model on goose 1.48.
	if got := gooseRecallPath(); got != filepath.Join(goose, "AGENTS.md") {
		t.Fatalf("without MOIM the target is %q", got)
	}
	moim := filepath.Join(t.TempDir(), "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	if got := gooseRecallPath(); got != moim {
		t.Fatalf("with MOIM set the target is %q, want %q", got, moim)
	}
}

// The install names the hooks plugin it creates; the uninstall removed it in
// silence (#3208).
func TestUninstallGooseAutoNamesTheHooksItRemoves(t *testing.T) {
	gooseHomeForTest(t)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	res, err := installGooseAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Path+" "+res.Note, "hooks.json") {
		t.Fatalf("uninstall did not name the hooks plugin: path=%q note=%q", res.Path, res.Note)
	}
}

// The hooks plugin is the whole of what goose-auto adds, and uninstall removed
// it without a word: three "unchanged" lines about config.yaml and nothing
// about the directory that had just been deleted. `uninstall codex-auto` says
// "also removed ~/.codex/hooks.json" about the same thing (#3208).
func TestGooseUninstallNamesThePluginItRemoved(t *testing.T) {
	gooseHomeForTest(t)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Dir(filepath.Dir(gooseHookPath()))
	if _, err := os.Stat(plugin); err != nil {
		t.Fatalf("the install wrote no plugin to remove: %v", err)
	}

	r, err := installGooseAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	said := r.Path + " " + r.Action + " " + r.Note
	if !strings.Contains(said, gooseHookPath()) && !strings.Contains(said, shortHome(gooseHookPath())) {
		t.Errorf("uninstall did not name the plugin it deleted: %q", said)
	}
	if !strings.Contains(said, "removed") {
		t.Errorf("uninstall did not say the plugin was removed: %q", said)
	}
	if _, serr := os.Stat(plugin); !os.IsNotExist(serr) {
		t.Errorf("the plugin survived: %v", serr)
	}

	// A second uninstall has nothing to remove and must not claim it did.
	r, err = installGooseAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Path == gooseHookPath() && r.Action == "removed" {
		t.Errorf("a second uninstall reported removing a plugin that was gone: %#v", r)
	}
	if strings.Contains(r.Note, "removed "+shortHome(gooseHookPath())) {
		t.Errorf("a second uninstall carried the removal in its note: %#v", r)
	}
}

// grok-auto discarded the MCP half's result, so it reported GROK.md and the
// hook file while it had also written config.toml and user-settings.json — the
// shape #3185 fixed for gemini-auto (#3220).
func TestGrokAutoNamesBothHalves(t *testing.T) {
	hermeticEnv(t)
	out := captureStdout(t, func() {
		if err := runInstall(index.DefaultDir(), []string{"grok-auto", "--no-index"}, false); err != nil {
			t.Fatal(err)
		}
	})
	// Through ToSlash: the report prints the host's separator, so a literal
	// "hooks/deja.json" passes everywhere and fails on the Windows leg alone.
	slashed := filepath.ToSlash(out)
	for _, want := range []string{"config.toml", "hooks/deja.json"} {
		if !strings.Contains(slashed, want) {
			t.Errorf("the report does not name %s:\n%s", want, out)
		}
	}
}

// grok-auto writes config.toml and user-settings.json through installGrok and
// the hooks file through installGrokAuto; the first result was dropped, so the
// line named only the hooks (#3220).
func TestGrokAutoResultNamesEveryFileItWrites(t *testing.T) {
	hermeticEnv(t)
	res, err := installTarget("grok-auto", "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	all := res.Path + " " + res.Note
	for _, want := range []string{"deja.json", "config.toml"} {
		if !strings.Contains(all, want) {
			t.Fatalf("install did not name %s: path=%q note=%q", want, res.Path, res.Note)
		}
	}
}

// Grok's documented hook events are the same four deja wires for Claude Code,
// and it reads them from a file in ~/.grok/hooks. Getting the shape wrong is
// silent: the file loads, nothing fires, and recall simply never appears.
func TestGrokAutoWritesEveryHookEvent(t *testing.T) {
	hermeticEnv(t)
	r, err := installGrokAuto("/bin/deja", false)
	if err != nil || r.Action != "created" {
		t.Fatalf("install = %#v, %v", r, err)
	}
	b, err := os.ReadFile(grokHooksPath())
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if uerr := json.Unmarshal(b, &root); uerr != nil {
		t.Fatalf("hooks file is not valid JSON: %v\n%s", uerr, b)
	}
	want := map[string]string{
		"SessionStart":     "hook-context",
		"UserPromptSubmit": "hook-prompt",
		"PreCompact":       "hook-precompact",
		"PreToolUse":       "hook-tool",
		"PostToolUse":      "hook-tool-after",
	}
	for event, sub := range want {
		groups, ok := root.Hooks[event]
		if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Fatalf("no %s hook:\n%s", event, b)
		}
		if got := groups[0].Hooks[0].Command; got == "" || groups[0].Hooks[0].Type != "command" {
			t.Fatalf("%s hook is not a command: %q\n%s", event, got, b)
		}
		if !containsSub(groups[0].Hooks[0].Command, sub) {
			t.Errorf("%s runs %q, want it to call %s", event, groups[0].Hooks[0].Command, sub)
		}
	}
	// The point-of-action hook must be scoped, or it fires on every read.
	if m := root.Hooks["PreToolUse"][0].Matcher; m == "" {
		t.Errorf("PreToolUse has no matcher, so it fires on reads too:\n%s", b)
	}

	if r, err = installGrokAuto("/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %#v, %v", r, err)
	}
	if r, err = installGrokAuto("/bin/deja", true); err != nil || r.Action != "removed" {
		t.Fatalf("uninstall = %#v, %v", r, err)
	}
	if _, err := os.Stat(grokHooksPath()); !os.IsNotExist(err) {
		t.Fatalf("hooks file survived uninstall: %v", err)
	}
}

// grokHookEntries reads the groups grok would load for one event.
func grokHookEntries(t *testing.T, event string) []struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"hooks"`
} {
	t.Helper()
	b, err := os.ReadFile(grokHooksPath())
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks file is not valid JSON: %v\n%s", err, b)
	}
	return root.Hooks[event]
}

// Grok's docs name the session sources `startup` and `resume`; 1.0.5 sends
// `new` for a fresh session and `load` for a resumed one. deja copied the
// Claude Code matcher, which matched neither, so this hook had never once
// fired on grok — and it is the hook that starts the index warming, so grok
// was the harness where the first recall of every session waited for a
// rebuild. No matcher takes whatever source grok names next.
func TestGrokSessionHookTakesWhateverSourceGrokNames(t *testing.T) {
	hermeticEnv(t)
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	groups := grokHookEntries(t, "SessionStart")
	if len(groups) == 0 {
		t.Fatal("no SessionStart hook")
	}
	if m := groups[0].Matcher; m != "" {
		t.Errorf("SessionStart still filters on %q, which grok's own sources do not match", m)
	}
}

// A matcher deja got wrong is written once and, without this, never corrected:
// every machine that had already installed deja would keep the broken one.
func TestAnOldGrokMatcherIsCorrectedOnReinstall(t *testing.T) {
	hermeticEnv(t)
	stale := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[` +
		`{"type":"command","command":"/bin/deja hook-context"}]}]}}`
	if err := os.MkdirAll(filepath.Dir(grokHooksPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grokHooksPath(), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	groups := grokHookEntries(t, "SessionStart")
	if len(groups) != 1 {
		t.Fatalf("reinstall did not take over the existing entry: %d groups", len(groups))
	}
	if m := groups[0].Matcher; m != "" {
		t.Errorf("the stale matcher survived a reinstall: %q", m)
	}
}

// The matcher covers every hook in its entry, so one deja shares with someone
// else's hook is not deja's to rewrite.
func TestASharedEntryKeepsItsMatcher(t *testing.T) {
	entry := map[string]any{"matcher": "startup|resume"}
	hooks := []any{
		map[string]any{"type": "command", "command": "/bin/deja hook-context"},
		map[string]any{"type": "command", "command": "/usr/local/bin/notify"},
	}
	adoptMatcher(entry, hooks, "")
	if entry["matcher"] != "startup|resume" {
		t.Errorf("a matcher shared with another hook was rewritten: %v", entry["matcher"])
	}
	adoptMatcher(entry, hooks[:1], "")
	if _, ok := entry["matcher"]; ok {
		t.Errorf("deja's own entry kept a matcher this build no longer wires: %v", entry["matcher"])
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// grok-dev's MCP modal turns a server off by writing "enabled": false on its
// entry in user-settings.json, and its runtime skips the entry. Install
// rebuilt deja's entry with "enabled": true and said nothing (#5196).
func TestGrokInstallKeepsGrokDevsSwitch(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GrokHome(), "user-settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcp": {"servers": [{"id": "deja", "label": "deja", "transport": "stdio", "command": "/bin/deja", "args": ["mcp"], "enabled": false}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "grok", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if got := grokDevDejaEnabled(t, path); got != false {
		t.Errorf("deja's grok-dev entry enabled = %v after install, want the reader's false", got)
	}
	if !strings.Contains(out, "switched off") {
		t.Errorf("install did not say it left the entry off:\n%s", out)
	}

	// The entry keyed "deja" decides over another labelled deja.
	both := `{"mcp": {"servers": [{"id": "deja-2", "label": "deja", "command": "/bin/deja", "args": ["mcp"], "enabled": false}, {"id": "deja", "label": "deja", "command": "/bin/deja", "args": ["mcp"], "enabled": true}]}}`
	if werr := os.WriteFile(path, []byte(both), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if _, cerr := captureRun(t, "install", "grok", "--no-index"); cerr != nil {
		t.Fatal(cerr)
	}
	if got := grokDevDejaEnabled(t, path); got != true {
		t.Errorf("enabled = %v, want the id-keyed entry's true", got)
	}

	// Control: an entry that was on stays on, and nothing is said.
	seed = strings.Replace(seed, `"enabled": false`, `"enabled": true`, 1)
	if werr := os.WriteFile(path, []byte(seed), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err = captureRun(t, "install", "grok", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if got := grokDevDejaEnabled(t, path); got != true {
		t.Errorf("deja's grok-dev entry enabled = %v, want true", got)
	}
	if strings.Contains(out, "switched off") {
		t.Errorf("install called an entry that was on switched off:\n%s", out)
	}
}

func grokDevDejaEnabled(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		MCP struct {
			Servers []map[string]any `json:"servers"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	for _, s := range root.MCP.Servers {
		if s["id"] == "deja" {
			return s["enabled"]
		}
	}
	t.Fatalf("no deja entry in\n%s", b)
	return nil
}

// grok writes config.toml first and the other files after it, so a later file
// it could not write was refused once config.toml already held deja: a
// read-only user-settings.json under `deja install grok`, a read-only hooks
// directory under grok-auto (#5197).
func TestGrokRefusesAnUnwritableLaterFileBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	t.Run("read-only user-settings.json", func(t *testing.T) {
		hermeticEnv(t)
		dir := sources.GrokHome()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		settings := filepath.Join(dir, "user-settings.json")
		if err := os.WriteFile(settings, []byte("{\"model\": \"grok-4\"}\n"), 0o444); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"grok", "grok-auto"} {
			_, err := captureRun(t, "install", target, "--no-index")
			if err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Errorf("%s: install err = %v, want the read-only refusal", target, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
				t.Errorf("%s: config.toml was written by a refused target", target)
				_ = os.Remove(filepath.Join(dir, "config.toml"))
			}
		}

		// A file already holding deja's entry needs no write, and is not refused.
		if err := os.Chmod(settings, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "grok", "--no-index"); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(settings, 0o444); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "install", "grok", "--no-index"); err != nil {
			t.Errorf("a repeat install refused an unchanged read-only user-settings.json: %v\n%s", err, out)
		}
	})
	t.Run("symlinked user-settings.json in a read-only repo", func(t *testing.T) {
		tmp := hermeticEnv(t)
		repo := filepath.Join(tmp, "dotfiles")
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		realPath := filepath.Join(repo, "user-settings.json")
		if err := os.WriteFile(realPath, []byte("{\"model\": \"grok-4\"}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sources.GrokHome(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realPath, filepath.Join(sources.GrokHome(), "user-settings.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(repo, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(repo, 0o755) })
		if _, err := captureRun(t, "install", "grok", "--no-index"); err == nil {
			t.Fatal("install wrote through a link into a read-only directory")
		}
		if _, err := os.Stat(filepath.Join(sources.GrokHome(), "config.toml")); err == nil {
			t.Error("config.toml was written by a refused target")
		}
	})
	t.Run("hooks naming the binary when the launcher cannot be written", func(t *testing.T) {
		hermeticEnv(t)
		bin := filepath.Dir(dejaLauncherPath())
		if bin == "." {
			t.Skip("no launcher on this platform")
		}
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(bin, 0o555)
		t.Cleanup(func() { _ = os.Chmod(bin, 0o755) })
		if _, err := captureRun(t, "install", "grok-auto", "--no-index"); err != nil {
			t.Fatal(err)
		}
		hooks := filepath.Dir(grokHooksPath())
		if err := os.Chmod(hooks, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(hooks, 0o755) })
		if out, err := captureRun(t, "install", "grok-auto", "--no-index"); err != nil {
			t.Errorf("a repeat install refused an unchanged hooks file: %v\n%s", err, out)
		}
	})
	t.Run("read-only hooks directory", func(t *testing.T) {
		hermeticEnv(t)
		hooks := filepath.Dir(grokHooksPath())
		if err := os.MkdirAll(hooks, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(hooks, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(hooks, 0o755) })
		_, err := captureRun(t, "install", "grok-auto", "--no-index")
		if err == nil {
			t.Fatal("install wrote into a read-only hooks directory")
		}
		for _, name := range []string{"config.toml", "user-settings.json"} {
			if _, err := os.Stat(filepath.Join(sources.GrokHome(), name)); err == nil {
				t.Errorf("%s was written by a refused target", name)
			}
		}
	})
}

func grokUserSettings(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".grok", "user-settings.json"))
	if err != nil {
		t.Fatalf("user-settings.json missing: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("user-settings.json: %v", err)
	}
	return root
}

func grokServers(t *testing.T, home string) []any {
	t.Helper()
	root := grokUserSettings(t, home)
	mcp, _ := root["mcp"].(map[string]any)
	servers, _ := mcp["servers"].([]any)
	return servers
}

// config.toml alone left grok-dev users with an integration that wrote a file
// nothing reads.
func TestInstallGrokWritesBothConfigShapes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGrok("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".grok", "config.toml"))
	if err != nil {
		t.Fatalf("config.toml missing: %v", err)
	}
	if !strings.Contains(string(b), "[mcp_servers.deja]") {
		t.Fatalf("config.toml has no deja block:\n%s", b)
	}
	servers := grokServers(t, home)
	if len(servers) != 1 {
		t.Fatalf("user-settings.json has %d servers, want ours", len(servers))
	}
	m, _ := servers[0].(map[string]any)
	// grok-dev keys servers by id and skips anything not enabled.
	for _, k := range []string{"id", "label", "enabled", "transport", "command"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("server entry missing %q: %v", k, m)
		}
	}
	if m["transport"] != "stdio" {
		t.Fatalf("transport = %v", m["transport"])
	}
	if m["enabled"] != true {
		t.Fatalf("server is written disabled: %v", m)
	}
}

func TestInstallGrokKeepsOtherServersAndSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".grok")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"defaultModel":"grok-4","mcp":{"servers":[{"id":"other","label":"other","enabled":true,"transport":"stdio","command":"/bin/other"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "user-settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrok("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := len(grokServers(t, home)); got != 2 {
		t.Fatalf("servers = %d, install dropped the user's own", got)
	}
	if grokUserSettings(t, home)["defaultModel"] != "grok-4" {
		t.Fatal("install rewrote unrelated settings")
	}
	if _, err := installGrok("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	servers := grokServers(t, home)
	if len(servers) != 1 {
		t.Fatalf("uninstall left %d servers, want the user's one", len(servers))
	}
	if m, _ := servers[0].(map[string]any); m["id"] != "other" {
		t.Fatalf("uninstall removed the wrong server: %v", servers)
	}
}

// The maintained CLI dropped GROK.md for AGENTS.md; writing only the old name
// is guidance nothing reads.
func TestGrokGuidanceLandsInBothFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGuidance("grok", false); err != nil {
		t.Fatalf("guidance: %v", err)
	}
	for _, name := range []string{"GROK.md", "AGENTS.md"} {
		b, err := os.ReadFile(filepath.Join(home, ".grok", name))
		if err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
		if !strings.Contains(string(b), guidanceStart) {
			t.Fatalf("%s has no deja block:\n%s", name, b)
		}
	}
	if _, err := installGuidance("grok", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".grok", "AGENTS.md"))
	if strings.Contains(string(b), guidanceStart) {
		t.Fatalf("uninstall left the block in AGENTS.md:\n%s", b)
	}
}

// Grok's install writes two files — config.toml for the CLI and
// user-settings.json for grok-dev — and the result only ever carried one of
// them, so the snapshot beside the other was never counted: the uninstall said
// "kept 1 snapshot" with two of them on disk (#3388).
func TestGrokInstallReportsBothFilesItWrote(t *testing.T) {
	tmp := hermeticEnv(t)
	grok := filepath.Join(tmp, "grok")
	if err := os.MkdirAll(grok, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GROK_ROOT", grok)
	t.Setenv("GROK_HOME", grok)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(grok, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.toml", "model = \"grok-4\"\n[mcp_servers.other]\ncommand = \"x\"\n")
	write("user-settings.json", "{\"theme\":\"dark\"}\n")

	res, err := installGrok("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	named := make([]string, 0, len(res.touched()))
	for _, p := range res.touched() {
		named = append(named, filepath.Base(p))
	}
	joined := strings.Join(named, " ")
	if !strings.Contains(joined, "config.toml") || !strings.Contains(joined, "user-settings.json") {
		t.Errorf("touched = %v, want both files the install wrote", named)
	}
}

// Install merges deja's hooks beside the reader's own; uninstall removed the
// whole file, and the reader's entries went with it (#3219).
func TestUninstallGrokAutoLeavesTheReadersHooks(t *testing.T) {
	hermeticEnv(t)
	path := grokHooksPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// An entry from an older deja at another path sits beside the reader's.
	seed := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"/usr/local/bin/other-tool session"},{"type":"command","command":"/old/path/deja hook-context"}]}],"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"/usr/local/bin/lint-on-write"}]}]}}` + "\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reader's hooks file is gone: %v", err)
	}
	if strings.Contains(string(b), "/bin/deja") || strings.Contains(string(b), "/old/path/deja") {
		t.Fatalf("deja's entries stayed:\n%s", b)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"other-tool session", "lint-on-write"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("the reader's %q entry is gone:\n%s", want, b)
		}
	}

	// deja's file alone: it goes, as before.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a file holding only deja's hooks was kept")
	}
}

// Hermes gives a general plugin no compaction hook, and the memory-provider
// route takes over the reader's memory.provider. The plugin hermes-auto writes
// notices the summary appear in the history pre_api_request and pre_llm_call
// see, and hands deja the turns from the call before it.
func TestHermesPluginCapturesACompactionWithoutTheProvider(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	root := t.TempDir()
	log := filepath.Join(root, "calls")
	fake := filepath.Join(root, "deja")
	script := "#!/bin/sh\nprintf '%s|' \"$*\" >> " + log + "\ncat >> " + log + "\necho >> " + log + "\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "plugins", "deja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "__init__.py"), []byte(hermesPluginPy(fake)), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := "import importlib.util\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugins/deja/__init__.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"hooks = []\n" +
		"class Ctx:\n" +
		"    def register_hook(self, name, fn): hooks.append(name)\n" +
		"    def register_command(self, *a, **k): pass\n" +
		"m.register(Ctx())\n" +
		"assert 'pre_api_request' in hooks and 'post_llm_call' in hooks, hooks\n" +
		"full = [{'role': 'user', 'content': 'fix the flaky retry in queue.go'}, {'role': 'assistant', 'content': 'raised the backoff cap'}]\n" +
		"m.watch(session_id='s1', conversation_history=full)\n" +
		"m.watch(session_id='s1', conversation_history=full + [{'role': 'user', 'content': 'go on'}])\n" +
		"cut = [{'role': 'user', 'content': '[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted', '_compressed_summary': True}]\n" +
		"m.watch(session_id='s1', conversation_history=cut)\n" +
		"m.watch(session_id='s1', conversation_history=cut + [{'role': 'assistant', 'content': 'next'}])\n"
	cmd := exec.CommandContext(t.Context(), py, "-c", probe)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(log)
	calls := strings.Count(string(b), "hook-precompact|")
	if calls != 1 {
		t.Fatalf("hook-precompact ran %d times, want once:\n%s", calls, b)
	}
	for _, want := range []string{`"session_id": "s1"`, `"harness": "hermes"`, "raised the backoff cap", "go on"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the packet payload lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "CONTEXT COMPACTION") {
		t.Errorf("the payload carried the summary instead of the turns before it:\n%s", b)
	}
}

// Hermes runs cron jobs with memory on and tells the plugin and the provider
// the platform is "cron". The same prompt every fire ran hook-context and
// hook-prompt each time and put their answer in front of the job; a person's
// session on the same machine still recalls.
func TestHermesCronRunRecallsNothingOnItsOwn(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	root := t.TempDir()
	log := filepath.Join(root, "calls")
	fake := filepath.Join(root, "deja")
	if werr := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> "+log+"\ncat >/dev/null\necho recalled\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if merr := os.MkdirAll(filepath.Dir(p), 0o755); merr != nil {
			t.Fatal(merr)
		}
		if werr := os.WriteFile(p, []byte(body), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	write("plugins/deja/__init__.py", hermesPluginPy(fake))
	write("agent/__init__.py", "")
	write("agent/memory_provider.py", "class MemoryProvider:\n    pass\n")
	home := filepath.Join(root, "hermes")
	t.Setenv("HERMES_HOME", home)
	hermeticEnv(t)
	t.Setenv("HERMES_HOME", home)
	if _, ierr := installHermesMemoryProvider(fake, false); ierr != nil {
		t.Fatal(ierr)
	}
	b, err := os.ReadFile(filepath.Join(home, "plugins", "deja-memory", "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	write("plugins/deja_memory/__init__.py", string(b))

	run := func(platform string) (string, []string) {
		t.Helper()
		_ = os.Remove(log)
		script := "import importlib.util, sys\n" +
			"sys.path.insert(0, '.')\n" +
			"def load(name, path):\n" +
			"    spec = importlib.util.spec_from_file_location(name, path)\n" +
			"    m = importlib.util.module_from_spec(spec)\n" +
			"    spec.loader.exec_module(m)\n" +
			"    return m\n" +
			"hook = load('dejahook', 'plugins/deja/__init__.py')\n" +
			"hook._provider_active = lambda: False\n" +
			"out = []\n" +
			"for first in (True, False):\n" +
			"    r = hook.recall(session_id='s1', user_message='look up the retry loop', is_first_turn=first, platform='" + platform + "')\n" +
			"    out.append('hook:' + (r or {}).get('context', '').strip())\n" +
			"prov = load('dejamem', 'plugins/deja_memory/__init__.py')\n" +
			"p = [v for v in vars(prov).values() if isinstance(v, type) and v.__name__ == 'DejaMemoryProvider'][0]()\n" +
			"p.initialize('s2', platform='" + platform + "')\n" +
			"out.append('provider:' + p.prefetch('look up the retry loop').strip())\n" +
			"print('|'.join(out))\n"
		cmd := exec.CommandContext(t.Context(), py, "-c", script)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", platform, err, out)
		}
		calls, _ := os.ReadFile(log)
		return strings.TrimSpace(string(out)), strings.Fields(string(calls))
	}

	got, calls := run("cron")
	if got != "hook:|hook:|provider:" || len(calls) != 0 {
		t.Errorf("a cron run recalled: %q, deja ran %v", got, calls)
	}
	// The control: the same calls from a person's session do recall, so the
	// silence above is the platform's doing and not a broken stand.
	got, calls = run("cli")
	if !strings.Contains(got, "hook:recalled") || !strings.Contains(got, "provider:recalled") || len(calls) == 0 {
		t.Errorf("a cli session did not recall: %q, deja ran %v", got, calls)
	}
}

func hermesTestConfig(t *testing.T, cfg string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HERMES_HOME", "")
	t.Setenv("DEJA_HERMES_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	path := filepath.Join(home, ".hermes", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if cfg != "" {
		if err := os.WriteFile(path, []byte(hermesL(cfg)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// hermesL fills in @L@, the command an install from /bin/deja names: the
// launcher where there is one (#5269).
func hermesL(s string) string {
	return strings.ReplaceAll(s, "@L@", hookExeInConfigs("/bin/deja"))
}

// hermesWant is what an install from /bin/deja leaves of an entry: the same
// text where there is no launcher (Windows), and the command moved to the
// launcher where there is one.
func hermesWant(s string) string {
	l := hookExeInConfigs("/bin/deja")
	if l == "/bin/deja" {
		return s
	}
	return strings.NewReplacer(`command: "/bin/deja"`, `command: "`+l+`"`, "command: /bin/deja", `command: "`+l+`"`).Replace(s)
}

var hermesDejaKey = regexp.MustCompile(`(?m)^  ["']?deja["']? *:`)

// Hermes reads `enabled` through PyYAML and its own boolean parser: `no`,
// `off`, `0`, `~` are off, and `disabled:` is not a key it reads. Install
// turned the first back on and switched deja off for the second (#5171).
func TestInstallHermesKeepsTheSwitchHermesReads(t *testing.T) {
	for _, tc := range []struct {
		value string
		off   bool
	}{
		{"enabled: no", true},
		{"enabled: NO", true},
		{"enabled: off", true},
		{`enabled: "0"`, true},
		{"enabled: 0", false},
		{"enabled: ~", false},
		{"enabled:", false},
		{`enabled: "false"`, true},
		{"enabled: False  # paused", true},
		{"enabled: yes", false},
		{"enabled: 1", false},
		{"disabled: true", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			cfg := "mcp_servers:\n  deja:\n    command: @L@\n    args: [mcp]\n    " + tc.value + "\n  other:\n    command: x\n"
			path := hermesTestConfig(t, cfg)
			cfg = hermesL(cfg)
			res, err := installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if string(b) != cfg {
				t.Fatalf("install rewrote an entry that already runs this binary:\n%s", b)
			}
			if said := strings.Contains(res.Note, switchedOffNote); said != tc.off {
				t.Fatalf("switched-off note %v, want %v: %q", said, tc.off, res.Note)
			}
			if note := hermesMCPOffNote(path); (note != "") != tc.off {
				t.Fatalf("doctor off note %q, want off=%v", note, tc.off)
			}
		})
	}
}

func TestHermesSwitchOffMatchesHermes(t *testing.T) {
	// Off only where PyYAML and Hermes' runtime parser both say off; the
	// runtime starts a server for anything else.
	off := []string{"false", "False", "FALSE", "no", "No", "NO", "off", "Off", "OFF", `"false"`, `'no'`, `"off"`, `"0"`, `"OFF"`, "no # paused"}
	on := []string{"true", "True", "yes", "on", "On", "1", "0", "0.0", "~", "null", "", "y", "n", "maybe", `"true"`, `"on"`, `"1"`, "yes # sure"}
	for _, v := range off {
		if !hermesSwitchOff(stripYAMLComment(v)) {
			t.Errorf("%q read as on, Hermes has it off", v)
		}
	}
	for _, v := range on {
		if hermesSwitchOff(stripYAMLComment(v)) {
			t.Errorf("%q read as off, Hermes has it on", v)
		}
	}
}

// A deja key with a comment, quotes or a flow value is still deja's: install
// wrote a second `deja:` above it, which strict YAML readers reject (#5172).
func TestInstallHermesFindsItsKeyHoweverItIsSpelled(t *testing.T) {
	for name, entry := range map[string]string{
		"comment":   "  deja:   # memory\n    command: /bin/deja\n    args: [mcp]\n",
		"quoted":    "  \"deja\":\n    command: /bin/deja\n    args: [mcp]\n",
		"single":    "  'deja' :\n    command: /bin/deja\n    args:\n      - mcp\n",
		"flow":      "  deja: {command: /bin/deja, args: [mcp], timeout: 30}\n",
		"flow off":  "  deja: {command: /bin/deja, args: [mcp], enabled: false}\n",
		"other bin": "  deja:   # memory\n    command: /old/deja\n    args: [mcp]\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := "model: x\nmcp_servers:\n" + entry + "  other:\n    command: x\ntoolsets:\n  - web\n"
			path := hermesTestConfig(t, cfg)
			if !doctorHermesWired(path) {
				t.Fatalf("doctor does not see the entry:\n%s", cfg)
			}
			res, err := installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if n := len(hermesDejaKey.FindAllString(string(b), -1)); n != 1 {
				t.Fatalf("%d deja keys, want one:\n%s", n, b)
			}
			if !strings.Contains(string(b), "\n  other:\n    command: x\n") || !strings.Contains(string(b), "toolsets:\n  - web\n") {
				t.Fatalf("a neighbour changed:\n%s", b)
			}
			if strings.Contains(name, "off") != strings.Contains(res.Note, switchedOffNote) {
				t.Fatalf("switched-off note wrong: %q", res.Note)
			}
			if !strings.Contains(string(b), "/bin/deja") || strings.Contains(string(b), "/old/deja") {
				t.Fatalf("the entry does not run this binary:\n%s", b)
			}
			if _, err := installHermesMCP("/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			b, _ = os.ReadFile(path)
			if want := "model: x\nmcp_servers:\n  other:\n    command: x\ntoolsets:\n  - web\n"; string(b) != want {
				t.Fatalf("uninstall left:\n%s", b)
			}
		})
	}
}

// A flow entry running something else, or deja keyed twice, is refused and
// the file left as it was.
func TestInstallHermesRefusesAnEntryItCannotEditInPlace(t *testing.T) {
	for name, cfg := range map[string]string{
		"flow unread":          "mcp_servers:\n  deja: {command /old/deja}\n",
		"flow key twice":       "mcp_servers:\n  deja: {command: /bin/deja, command: /old/deja, args: [mcp]}\n",
		"nested bracket wrap":  "mcp_servers:\n  deja:\n    command: /old/deja\n    args: [serve, [x]\n    ]\n",
		"quoted wrap":          "mcp_servers:\n  deja:\n    command: \"/old\n    /deja\"\n    args: [mcp]\n",
		"other key wraps":      "mcp_servers:\n  deja:\n    env: {A: 1,\nB: 2}\n    command: /old/deja\n    args: [serve]\n",
		"flow args over lines": "mcp_servers:\n  deja:\n    command: /bin/deja\n    args: [\n      serve\n    ]\n",
		"generated copy last":  "mcp_servers:\n  deja:\n    command: /old/deja\n    args: [mcp]\n    enabled: false\n  deja:\n    command: \"/bin/deja\"\n    args:\n      - mcp\n    enabled: true\n",
		"twice":                "mcp_servers:\n  deja:\n    command: /bin/deja\n  \"deja\":\n    command: /bin/deja\n    enabled: false\n",
		"twice, timeouts":      "mcp_servers:\n  deja:\n    command: /old/deja\n    args: [mcp]\n    timeout: 30\n  deja:\n    command: /old/deja\n    args: [mcp]\n    timeout: 60\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := hermesTestConfig(t, cfg)
			if _, err := installHermesMCP("/bin/deja", false); err == nil {
				t.Fatal("install did not refuse")
			}
			if b, _ := os.ReadFile(path); string(b) != cfg {
				t.Fatalf("a refused install wrote:\n%s", b)
			}
		})
	}
}

// The reader's keys on deja's entry stay through install: rebuilding the
// entry dropped timeouts, env and the tool filter (#5173).
func TestInstallHermesKeepsTheReadersKeysOnItsEntry(t *testing.T) {
	cfg := "mcp_servers:\n  deja:\n    command: /old/deja   # was brew\n    args:\n      - mcp\n      - --x\n" +
		"    timeout: 120\n    env:\n      DEJA_INDEX_DIR: /data/idx\n    tools:\n      include: [search, show]\n    enabled: false\n  other:\n    command: x\n"
	path := hermesTestConfig(t, cfg)
	res, err := installHermesMCP("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "mcp_servers:\n  deja:\n    command: \"/bin/deja\"\n    args:\n      - mcp\n" +
		"    timeout: 120\n    env:\n      DEJA_INDEX_DIR: /data/idx\n    tools:\n      include: [search, show]\n    enabled: false\n  other:\n    command: x\n"
	if want = hermesWant(want); string(b) != want {
		t.Fatalf("install gave\n%s\nwant\n%s", b, want)
	}
	if !strings.Contains(res.Note, switchedOffNote) {
		t.Fatalf("no switched-off note: %q", res.Note)
	}
	// No command or args at all: both go in, the rest stays.
	cfg = "mcp_servers:\n    deja:\n        timeout: 5\n"
	path = hermesTestConfig(t, cfg)
	if _, err := installHermesMCP("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if want := hermesWant("mcp_servers:\n    deja:\n        command: \"/bin/deja\"\n        args:\n            - mcp\n        timeout: 5\n"); string(b) != want {
		t.Fatalf("install gave\n%s\nwant\n%s", b, want)
	}
}

// A plugins: block deja cannot edit is refused before anything is written:
// the MCP entry and the plugin directory went in first, and the refused target
// was left half wired (#5174).
func TestInstallHermesAutoRefusesBeforeWriting(t *testing.T) {
	for name, cfg := range map[string]string{
		"empty flow":     "model: x\nplugins: {}\n",
		"flow two lines": "model: x\nplugins:\n  enabled: [\n    spotify,\n  ]\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := hermesTestConfig(t, cfg)
			if _, err := installHermesAuto("/bin/deja", false); err == nil {
				t.Fatal("install did not refuse")
			}
			if b, _ := os.ReadFile(path); string(b) != cfg {
				t.Fatalf("a refused install wrote config.yaml:\n%s", b)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), "plugins")); err == nil {
				t.Fatal("a refused install wrote the plugin directory")
			}
		})
	}
	// Control: the same plugins switched off by the reader is not edited, so
	// not refused.
	path := hermesTestConfig(t, "plugins:\n  disabled: [deja]\n  enabled: [\n    spotify,\n  ]\n")
	if _, err := installHermesAuto("/bin/deja", false); err != nil {
		t.Fatalf("refused a plugins block it leaves alone: %v", err)
	}
	_ = path
}

// Uninstall says when memory.provider still names the provider it took away
// (#5175), and keeps the key, which is the reader's.
func TestUninstallHermesSaysMemoryProviderIsLeftNamed(t *testing.T) {
	cfg := "memory:\n  provider: deja-memory\n"
	path := hermesTestConfig(t, cfg)
	if _, err := installHermesMemoryProvider("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	res, err := installHermesMemoryProvider("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Note, "hermes memory off") {
		t.Fatalf("no note about memory.provider: %q", res.Note)
	}
	if b, _ := os.ReadFile(path); string(b) != cfg {
		t.Fatalf("uninstall changed the reader's key:\n%s", b)
	}
	// Control: another provider, nothing said.
	hermesTestConfig(t, "memory:\n  provider: mem0\n")
	if _, err := installHermesMemoryProvider("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if res, _ := installHermesMemoryProvider("/bin/deja", true); res.Note != "" {
		t.Fatalf("note for another provider: %q", res.Note)
	}
}

// Shapes the review found: a comment at column 0 inside the entry, args as a
// string or `-mcp`, and the two keys the old installer left (#5172, #5173).
func TestInstallHermesEntryShapesFromReview(t *testing.T) {
	for name, tc := range map[string]struct{ cfg, want, uninstalled string }{
		"column-0 comment": {
			cfg:         "mcp_servers:\n  deja:\n    command: /bin/deja\n#    timeout: 30\n    args: [mcp]\n    enabled: false\n  other:\n    command: x\n",
			want:        "mcp_servers:\n  deja:\n    command: /bin/deja\n#    timeout: 30\n    args: [mcp]\n    enabled: false\n  other:\n    command: x\n",
			uninstalled: "mcp_servers:\n  other:\n    command: x\n",
		},
		"comment in args": {
			cfg:         "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      - serve\n#     - old\n      - mcp\n    timeout: 9\n",
			want:        "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      - mcp\n    timeout: 9\n",
			uninstalled: "mcp_servers:\n",
		},
		"args string": {
			cfg:         "mcp_servers:\n  deja:\n    command: /bin/deja\n    args: mcp\n",
			want:        "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      - mcp\n",
			uninstalled: "mcp_servers:\n",
		},
		"dash glued": {
			cfg:         "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      -mcp\n",
			want:        "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      - mcp\n",
			uninstalled: "mcp_servers:\n",
		},
		"args before command": {
			cfg:         "mcp_servers:\n  deja:\n    args: [old]\n    command: /old/deja\n    timeout: 30\n",
			want:        "mcp_servers:\n  deja:\n    args:\n      - mcp\n    command: \"/bin/deja\"\n    timeout: 30\n",
			uninstalled: "mcp_servers:\n",
		},
		"command on the next line": {
			cfg:         "mcp_servers:\n  deja:\n    command: >-\n      /old/deja\n    args: [mcp]\n",
			want:        "mcp_servers:\n  deja:\n    command: \"/bin/deja\"\n    args: [mcp]\n",
			uninstalled: "mcp_servers:\n",
		},
		"old installer's second key": {
			cfg:         "mcp_servers:\n  deja:\n    command: \"/bin/deja\"\n    args:\n      - mcp\n    enabled: true\n  deja:   # mine\n    command: /old/deja\n    args: [mcp]\n    timeout: 60\n",
			want:        "mcp_servers:\n  deja:   # mine\n    command: \"/bin/deja\"\n    args: [mcp]\n    timeout: 60\n",
			uninstalled: "mcp_servers:\n",
		},
		"two copies of deja's own entry": {
			cfg:         "mcp_servers:\n  deja:\n    command: \"/usr/local/bin/deja\"\n    args:\n      - mcp\n    enabled: true\n  'deja':\n    command: \"/usr/local/bin/deja\"\n    args:\n      - mcp\n    enabled: true\n",
			want:        "mcp_servers:\n  'deja':\n    command: \"/bin/deja\"\n    args:\n      - mcp\n    enabled: true\n",
			uninstalled: "mcp_servers:\n",
		},
		"two identical copies of the reader's": {
			cfg:         "mcp_servers:\n  other:\n    command: x\n  deja:\n    command: /old/deja\n    args: [mcp]\n    timeout: 30\n  deja:\n    command: /old/deja\n    args: [mcp]\n    timeout: 30\n",
			want:        "mcp_servers:\n  other:\n    command: x\n  deja:\n    command: \"/bin/deja\"\n    args: [mcp]\n    timeout: 30\n",
			uninstalled: "mcp_servers:\n  other:\n    command: x\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := hermesTestConfig(t, tc.cfg)
			res, err := installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if tc.want = hermesWant(tc.want); string(b) != tc.want {
				t.Fatalf("install gave\n%s\nwant\n%s", b, tc.want)
			}
			if strings.Contains(tc.cfg, "enabled: false") != strings.Contains(res.Note, switchedOffNote) {
				t.Fatalf("switched-off note wrong: %q", res.Note)
			}
			if _, err := installHermesMCP("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			if b2, _ := os.ReadFile(path); !bytes.Equal(b2, b) {
				t.Fatalf("a second install changed the file:\n%s", b2)
			}
			if _, err := installHermesMCP("/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if b, _ = os.ReadFile(path); string(b) != tc.uninstalled {
				t.Fatalf("uninstall left\n%q\nwant\n%q", b, tc.uninstalled)
			}
		})
	}
}

// Uninstall leaves a file it would break by taking the entry out by indent.
func TestUninstallHermesRefusesAnEntryItWouldCutInTwo(t *testing.T) {
	for name, cfg := range map[string]string{
		"wrapped flow": "mcp_servers:\n  deja:\n    env: {A: 1,\nB: 2}\n    command: /bin/deja\n",
		"alias":        "mcp_servers:\n  deja: &d\n    command: /bin/deja\n  copy: *d\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := hermesTestConfig(t, cfg)
			if _, err := installHermesMCP("/bin/deja", true); err == nil {
				t.Fatal("uninstall did not refuse")
			}
			if b, _ := os.ReadFile(path); string(b) != cfg {
				t.Fatalf("a refused uninstall wrote:\n%s", b)
			}
		})
	}
}

// A one-line entry that needs an edit is written out as a block and edited
// like one: naming the binary, it kept deja mcp on Hermes' stripped env and
// the default index, and install said "unchanged" (#5338).
func TestInstallHermesWritesAOneLineEntryOutAsABlock(t *testing.T) {
	for name, tc := range map[string]struct{ entry, want string }{
		"the binary": {"  deja: {command: /bin/deja, args: [mcp], timeout: 30, env: {A: \"x, y\"}}   # mine\n",
			"  deja: # mine\n    command: \"@L@\"\n    args: [mcp]\n    timeout: 30\n    env: {A: \"x, y\"}\n"},
		"elsewhere": {"  deja: {enabled: no, command: /old/deja, args: [serve]}\n",
			"  deja:\n    enabled: no\n    command: \"@L@\"\n    args:\n      - mcp\n"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := "model: x\nmcp_servers:\n" + tc.entry + "  other:\n    command: x\n"
			path := hermesTestConfig(t, cfg)
			res, err := installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if name == "the binary" && hookExeInConfigs("/bin/deja") == "/bin/deja" {
				// No launcher (Windows): the entry already runs what install writes.
				if string(b) != cfg {
					t.Fatalf("install rewrote an entry that runs this binary:\n%s", b)
				}
				return
			}
			if want := "model: x\nmcp_servers:\n" + hermesL(tc.want) + "  other:\n    command: x\n"; string(b) != want {
				t.Fatalf("install gave\n%s\nwant\n%s", b, want)
			}
			if !strings.Contains(res.Note, hermesFlowRewrittenNote) {
				t.Errorf("install did not say it rewrote the line: %q", res.Note)
			}
			if strings.Contains(name, "elsewhere") != strings.Contains(res.Note, switchedOffNote) {
				t.Errorf("switched-off note wrong: %q", res.Note)
			}
			res, err = installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if again, _ := os.ReadFile(path); !bytes.Equal(again, b) || strings.Contains(res.Note, hermesFlowRewrittenNote) {
				t.Fatalf("a second install changed it again (%q):\n%s", res.Note, again)
			}
		})
	}
}

// The plugin's first turn ran hook-context with nothing on stdin, so the
// session was never stamped live and the deja tool answered that turn with
// the session asking it (#4246).
func TestHermesFirstTurnNamesTheSessionToHookContext(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	root := t.TempDir()
	log := filepath.Join(root, "calls")
	fake := filepath.Join(root, "deja")
	if werr := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s|' \"$*\" >> "+log+"\ncat >> "+log+"\necho >> "+log+"\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	hookDir := filepath.Join(root, "plugins", "deja")
	if merr := os.MkdirAll(hookDir, 0o755); merr != nil {
		t.Fatal(merr)
	}
	if werr := os.WriteFile(filepath.Join(hookDir, "__init__.py"), []byte(hermesPluginPy(fake)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	script := "import importlib.util\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugins/deja/__init__.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"m.recall(session_id='20261001_182154_69936a', user_message='look up the retry loop', is_first_turn=True)\n"
	cmd := exec.CommandContext(t.Context(), py, "-c", script)
	cmd.Dir = root
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("plugin: %v\n%s", cerr, out)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(line, "hook-context") {
			if !strings.Contains(line, `"session_id": "20261001_182154_69936a"`) {
				t.Errorf("hook-context was not told which session is starting: %q", line)
			}
			return
		}
	}
	t.Fatalf("the first turn never ran hook-context:\n%s", b)
}

// A config whose first line is `mcp_servers:` (or that carries a comment or
// trailing spaces on the key) has the block deja must join. Appending a
// second key instead hides the reader's servers: YAML keeps the last one, and
// Hermes loaded deja alone (#4289).
func TestInstallHermesJoinsAnMCPBlockOnTheFirstLine(t *testing.T) {
	for name, cfg := range map[string]string{
		"first line":    "mcp_servers:\n  foo:\n    command: z\n",
		"no newline":    "mcp_servers:\n  foo:\n    command: z",
		"comment":       "model: x\nmcp_servers:  # mine\n  foo:\n    command: z\n",
		"trailing tabs": "model: x\nmcp_servers: \t\n  foo:\n    command: z\n",
		"bare key last": "model: x\nmcp_servers:",
		"comment child": "mcp_servers:\n    # my servers\n  foo:\n    command: z\n",
		"bom":           "\ufeffmcp_servers:\n  foo:\n    command: z\n",
		"document end":  "model: x\n...\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("DEJA_HERMES_HOME", "")
			path := filepath.Join(home, ".hermes", "config.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installHermesMCP("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			got := string(b)
			if n := strings.Count(got, "mcp_servers:"); n != 1 {
				t.Fatalf("%d mcp_servers keys, want one:\n%s", n, got)
			}
			if !strings.Contains(got, "\n  deja:\n") {
				t.Fatalf("deja is not an entry at the servers' indent:\n%s", got)
			}
			if strings.Contains(cfg, "foo:") && !strings.Contains(got, "\n  foo:") {
				t.Fatalf("the reader's server is gone:\n%s", got)
			}
			if !doctorHermesWired(path) {
				t.Fatalf("doctor does not see the entry install wrote:\n%s", got)
			}
			// A second install changes nothing.
			if _, err := installHermesMCP("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			if b2, _ := os.ReadFile(path); string(b2) != got {
				t.Fatalf("a second install changed the file:\nfirst  %q\nsecond %q", got, b2)
			}
			if _, err := installHermesMCP("/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			b, _ = os.ReadFile(path)
			if string(b) != cfg {
				t.Fatalf("uninstall did not give the file back:\nwant %q\ngot  %q", cfg, b)
			}
		})
	}
}

// A key deja cannot join without guessing — written twice, or with the
// servers on its own line — is refused and the file left alone, here and for
// goose's extensions:.
func TestInstallRefusesATopLevelKeyItCannotJoin(t *testing.T) {
	for name, cfg := range map[string]string{
		"twice":  "mcp_servers:\n  foo:\n    command: z\nmodel: x\nmcp_servers:\n  bar:\n    command: y\n",
		"inline": "mcp_servers: {foo: {command: z}}\n",
		"quoted": "\"mcp_servers\":\n  foo:\n    command: z\n",
		"spaced": "mcp_servers :\n  foo:\n    command: z\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("DEJA_HERMES_HOME", "")
			path := filepath.Join(home, ".hermes", "config.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installHermesMCP("/bin/deja", false); err == nil {
				b, _ := os.ReadFile(path)
				t.Fatalf("install went ahead:\n%s", b)
			}
			if b, _ := os.ReadFile(path); string(b) != cfg {
				t.Fatalf("a refused install changed the file:\n%s", b)
			}
		})
	}
}

func TestInstallGooseJoinsACommentedExtensionsKey(t *testing.T) {
	for _, cfg := range []string{
		"extensions: # mine\n  foo:\n    cmd: z\n",
		"extensions: # mine\n    foo:\n        cmd: z\n",
		"extensions:   \n    foo:\n        cmd: z\n",
		"extensions:\n# mine\n  foo:\n    cmd: z\n",
	} {
		t.Run(cfg, func(t *testing.T) { gooseRoundTrip(t, cfg) })
	}
}

func gooseRoundTrip(t *testing.T, cfg string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GOOSE_PATH_ROOT", "")
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGoose("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if n := strings.Count(string(b), "extensions:"); n != 1 {
		t.Fatalf("%d extensions keys, want one:\n%s", n, b)
	}
	// deja's entry sits beside the reader's, at the block's own indent.
	pad := yamlBlockIndent(gooseExtensionsBlock(cfg))
	if !strings.Contains(string(b), "\n"+pad+"deja:\n") || !strings.Contains(string(b), "\n"+pad+"foo:\n") {
		t.Fatalf("deja and foo are not siblings at %q:\n%s", pad, b)
	}
	if _, err := installGoose("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != cfg {
		t.Fatalf("uninstall did not give the file back:\nwant %q\ngot  %q", cfg, b)
	}
}

// Comments under an empty key are the reader's and stay; a key whose name
// merely ends in "extensions:" is not ours to touch.
func TestInstallGooseKeepsWhatItDidNotWrite(t *testing.T) {
	for _, cfg := range []string{
		"extensions:\n  # nothing yet\nGOOSE_MODEL: x\n",
		"slash_commands:\n  # none yet\nGOOSE_MODEL: x\n",
		"my_extensions:\n\nGOOSE_MODEL: x\n",
		"slash_commands: # mine\n  - command: mine\n    recipe_path: /tmp/r.yaml\n",
	} {
		t.Run(cfg, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("GOOSE_PATH_ROOT", "")
			path := filepath.Join(gooseConfigDir(), "config.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, f := range []func(string, bool) (installResult, error){installGoose, installGooseCommand} {
				if _, err := f("/bin/deja", false); err != nil {
					t.Fatal(err)
				}
				if _, err := f("/bin/deja", true); err != nil {
					t.Fatal(err)
				}
				if b, _ := os.ReadFile(path); string(b) != cfg {
					t.Fatalf("install and uninstall did not give the file back:\nwant %q\ngot  %q", cfg, b)
				}
			}
		})
	}
}

// Continue's two lists, and a goose key whose entries follow a comment and a
// blank line: the same key-line rules, so nothing of the reader's is lost or
// hidden.
func TestContinueAndGooseKeysWithCommentsRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, cfg string
		install   func(string, bool) (installResult, error)
		path      func() string
	}{
		{"continue commented key", "name: x\nmcpServers: # c\n  - name: foo\n    command: z\n", installContinue, continueConfigPath},
		{"continue comment-only block", "name: x\nmcpServers:\n  # none\n", installContinue, continueConfigPath},
		{"continue col0 comment then blank", "name: x\nprompts:\n# mine\n\n  - name: p\n    prompt: hi\n", installContinue, continueConfigPath},
		{"goose col0 comment then blank", "extensions:\n# mine\n\n  foo:\n    cmd: z\n", installGoose, func() string { return filepath.Join(gooseConfigDir(), "config.yaml") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("GOOSE_PATH_ROOT", "")
			path := tc.path()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := tc.install("/bin/deja", false); err != nil {
					t.Fatal(err)
				}
			}
			b, _ := os.ReadFile(path)
			if strings.Contains(string(b), "#") && strings.Contains(string(b), "  # none  -") {
				t.Fatalf("an item was glued onto a comment:\n%s", b)
			}
			for _, key := range []string{"\nmcpServers", "\nprompts", "\nextensions"} {
				if n := strings.Count("\n"+string(b), key); n > 1 {
					t.Fatalf("%q written %d times:\n%s", key, n, b)
				}
			}
			if _, err := tc.install("/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(path); string(b) != tc.cfg {
				t.Fatalf("install and uninstall did not give the file back:\nwant %q\ngot  %q", tc.cfg, b)
			}
		})
	}
}

// goose loads nothing from a file with a key twice, so an inline
// slash_commands is refused rather than shadowed.
func TestInstallGooseCommandRefusesAnInlineSlashCommands(t *testing.T) {
	for _, cfg := range []string{"slash_commands: []\n", "slash_commands: ~\n"} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("GOOSE_PATH_ROOT", "")
		path := filepath.Join(gooseConfigDir(), "config.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installGooseCommand("/bin/deja", false); err == nil {
			b, _ := os.ReadFile(path)
			t.Fatalf("%q: install went ahead:\n%s", cfg, b)
		}
		if b, _ := os.ReadFile(path); string(b) != cfg {
			t.Fatalf("%q: a refused install changed the file:\n%s", cfg, b)
		}
	}
}

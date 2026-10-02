package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Every client keeps its own way to turn deja off without removing it: a
// switch on the entry in a YAML or TOML config, a deny list or MCP master
// switch beside the servers, a switch for all hooks, a disable on deja's own
// extension or plugin. Doctor read only the JSON entry flag, so each of these
// left the row `wired` in text and JSON while the client ran nothing (#4466,
// #4468, #4469, #4470).
func TestDoctorReadsTheClientsOwnOffSwitches(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	editJSON := func(t *testing.T, path string, edit func(map[string]any)) {
		t.Helper()
		root := map[string]any{}
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}
		edit(root)
		b, _ := json.MarshalIndent(root, "", "  ")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setJSON := func(path string, keys []string, v any) func(*testing.T) {
		return func(t *testing.T) {
			editJSON(t, path, func(root map[string]any) {
				m := root
				for _, k := range keys[:len(keys)-1] {
					next, _ := m[k].(map[string]any)
					if next == nil {
						next = map[string]any{}
						m[k] = next
					}
					m = next
				}
				m[keys[len(keys)-1]] = v
			})
		}
	}
	replace := func(path func() string, old, new string) func(*testing.T) {
		return func(t *testing.T) {
			b, err := os.ReadFile(path())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), old) {
				t.Fatalf("%s has no %q:\n%s", path(), old, b)
			}
			if err := os.WriteFile(path(), []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	appendTo := func(path func() string, text string) func(*testing.T) {
		return func(t *testing.T) {
			b, _ := os.ReadFile(path())
			if err := os.MkdirAll(filepath.Dir(path()), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path(), append(b, text...), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	prepend := func(path func() string, text string) func(*testing.T) {
		return func(t *testing.T) {
			b, _ := os.ReadFile(path())
			if err := os.WriteFile(path(), append([]byte(text), b...), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	lazy := func(f func() string) func() string { return f }
	codexToml := lazy(func() string { return filepath.Join(sources.CodexHome(), "config.toml") })
	grokToml := lazy(func() string { return filepath.Join(sources.GrokHome(), "config.toml") })
	// Codex runs no hook it has not been shown, so its row is wired only with
	// every event trusted — the control the switch is then measured against.
	trustCodex := func(t *testing.T) {
		var pins strings.Builder
		hooks := filepath.Join(sources.CodexHome(), "hooks.json")
		for _, h := range codexHookWiring {
			pins.WriteString("\n[hooks.state.\"" + hooks + ":" + codexEventKey(h.Event) + ":0:0\"]\ntrusted_hash = \"sha256:abc\"\n")
		}
		appendTo(codexToml, pins.String())(t)
	}

	cases := []struct {
		name, target, section, row, key string
		before                          func(*testing.T)
		off                             func(*testing.T)
	}{
		// #4466: the entry's own switch, in YAML and TOML.
		{name: "goose entry", target: "goose", section: "mcp", row: "goose", key: "extensions.deja.enabled",
			off: replace(func() string { return filepath.Join(gooseConfigDir(), "config.yaml") }, "enabled: true", "enabled: false")},
		{name: "hermes entry", target: "hermes", section: "mcp", row: "hermes", key: "mcp_servers.deja.enabled",
			off: replace(func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }, "enabled: true", "enabled: false")},
		{name: "codex entry", target: "codex", section: "mcp", row: "codex", key: "[mcp_servers.deja] enabled",
			off: appendTo(codexToml, "enabled = false\n")},
		{name: "grok entry", target: "grok", section: "mcp", row: "grok", key: "[mcp_servers.deja] enabled",
			off: appendTo(grokToml, "enabled = false\n")},
		{name: "dsh row", target: "deepseek", section: "mcp", row: "deepseek", key: "mcp-deja",
			off: replace(dshPatchPath, "- id: mcp-deja\n", "- id: mcp-deja\n      disabled: true\n")},
		// #4468: deny lists and MCP master switches outside the entry.
		{name: "gemini mcp disable", target: "gemini", section: "mcp", row: "gemini", key: "mcp-server-enablement.json",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.GeminiHome(), "mcp-server-enablement.json"), []string{"deja", "enabled"}, false)(t)
			}},
		{name: "gemini excluded", target: "gemini", section: "mcp", row: "gemini", key: "mcp.excluded",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.GeminiHome(), "settings.json"), []string{"mcp", "excluded"}, []any{"deja"})(t)
			}},
		{name: "gemini allowed", target: "gemini", section: "mcp", row: "gemini", key: "mcp.allowed",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.GeminiHome(), "settings.json"), []string{"mcp", "allowed"}, []any{"other"})(t)
			}},
		{name: "qwen excluded", target: "qwen", section: "mcp", row: "qwen", key: "mcp.excluded",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), []string{"mcp", "excluded"}, []any{"deja"})(t)
			}},
		{name: "copilot", target: "copilot", section: "mcp", row: "copilot", key: "disabledMcpServers",
			off: func(t *testing.T) {
				setJSON(filepath.Join(filepath.Dir(copilotMCPConfigPath()), "settings.json"), []string{"disabledMcpServers"}, []any{"deja"})(t)
			}},
		{name: "grok disable", target: "grok", section: "mcp", row: "grok", key: "disabled_mcp_servers",
			off: prepend(grokToml, "disabled_mcp_servers = [\"deja\"]\n\n")},
		{name: "omp", target: "omp", section: "mcp", row: "omp", key: "disabledServers",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.OmpConfigDir(), "mcp.json"), []string{"disabledServers"}, []any{"deja"})(t)
			}},
		{name: "gjc", target: "gjc", section: "mcp", row: "gjc", key: "disabledServers",
			off: func(t *testing.T) { setJSON(gjcMCPPath(), []string{"disabledServers"}, []any{"deja"})(t) }},
		{name: "vscode", target: "vscode", section: "mcp", row: "vscode", key: "chat.mcp.access",
			off: func(t *testing.T) {
				setJSON(filepath.Join(filepath.Dir(doctorVSCodeMCPPath()), "settings.json"), []string{"chat.mcp.access"}, "none")(t)
			}},
		{name: "zcode", target: "zcode", section: "mcp", row: "zcode", key: "features.mcp",
			off: func(t *testing.T) { setJSON(zcodeConfigPath(), []string{"features", "mcp"}, false)(t) }},
		{name: "openclaw", target: "openclaw", section: "mcp", row: "openclaw", key: "tools.deny",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), []string{"tools", "deny"}, []any{"bundle-mcp"})(t)
			}},
		{name: "opencode", target: "opencode", section: "mcp", row: "opencode", key: "tools",
			off: func(t *testing.T) {
				setJSON(doctorOpencodeConfigPath(), []string{"tools", "deja*"}, false)(t)
			}},
		{name: "amp", target: "amp", section: "mcp", row: "amp", key: "amp.tools.disable",
			off: func(t *testing.T) {
				setJSON(sources.AmpSettingsFile(), []string{"amp.tools.disable"}, []any{"mcp__deja__*"})(t)
			}},
		{name: "claude project", target: "claude-code", section: "mcp", row: "claude-code", key: "disabledMcpServers",
			off: func(t *testing.T) {
				// The repository's main root, which TestPerProjectListsAreReadForTheDirectoryTheClientKeys pins.
				setJSON(sources.ClaudeJSONPath(), []string{"projects", doctorProjectDir(true), "disabledMcpServers"}, []any{"deja"})(t)
			}},
		{name: "cursor project", target: "cursor", section: "mcp", row: "cursor", key: "mcp-disabled.json",
			off: func(t *testing.T) {
				root := gitRootOf(filepath.Join(cwd, "x"))
				if root == "" {
					root = cwd
				}
				// cursor-agent's slug: every run of non-alphanumerics one dash.
				slug := strings.Trim(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(root, "-"), "-")
				path := filepath.Join(os.Getenv("HOME"), ".cursor", "projects", slug, "mcp-disabled.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`["deja"]`), 0o644); err != nil {
					t.Fatal(err)
				}
			}},
		// #4469: the client's switch for every hook.
		{name: "claude hooks", target: "claude-auto", section: "auto_recall", row: "claude-code", key: "disableAllHooks",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.ClaudeConfigDir(), "settings.json"), []string{"disableAllHooks"}, true)(t)
			}},
		{name: "qwen hooks", target: "qwen-auto", section: "auto_recall", row: "qwen", key: "disableAllHooks",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), []string{"disableAllHooks"}, true)(t)
			}},
		{name: "gemini hooks", target: "gemini-auto", section: "auto_recall", row: "gemini", key: "hooksConfig.enabled",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.GeminiHome(), "settings.json"), []string{"hooksConfig", "enabled"}, false)(t)
			}},
		{name: "codex hooks", target: "codex-auto", section: "auto_recall", row: "codex-hook", key: "[features] hooks",
			before: trustCodex, off: appendTo(codexToml, "\n[features]\nhooks = false\n")},
		{name: "openclaw internal hooks", target: "openclaw-auto", section: "auto_recall", row: "openclaw", key: "hooks.internal.enabled",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), []string{"hooks", "internal", "enabled"}, false)(t)
			}},
		// #4470: deja's own extension or plugin, disabled through the client.
		{name: "gemini extension", target: "gemini-auto", section: "auto_recall", row: "gemini", key: "extension-enablement.json",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.GeminiHome(), "extensions", "extension-enablement.json"),
					[]string{"deja", "overrides"}, []any{"!" + filepath.ToSlash(cwd) + "/*"})(t)
			}},
		{name: "openclaw hook entry", target: "openclaw-auto", section: "auto_recall", row: "openclaw", key: "hooks.internal.entries.deja-recall.enabled",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), []string{"hooks", "internal", "entries", "deja-recall", "enabled"}, false)(t)
			}},
		{name: "openclaw plugin", target: "openclaw-auto", section: "auto_recall", row: "openclaw", key: "plugins.entries",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), []string{"plugins", "entries", "deja", "enabled"}, false)(t)
			}},
		{name: "goose plugin", target: "goose-auto", section: "auto_recall", row: "goose", key: "disabledPlugins",
			off: func(t *testing.T) {
				setJSON(filepath.Join(gooseConfigDir(), "settings.json"), []string{"disabledPlugins"}, []any{"deja"})(t)
			}},
		{name: "pi extension", target: "pi-auto", section: "auto_recall", row: "pi", key: "-extensions/deja.ts",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.PiConfigDir(), "settings.json"), []string{"extensions"}, []any{"-extensions/deja.ts"})(t)
			}},
		{name: "senpi extension", target: "senpi-auto", section: "auto_recall", row: "senpi", key: "-extensions/deja.ts",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.SenpiConfigDir(), "settings.json"), []string{"extensions"}, []any{"-extensions/deja.ts"})(t)
			}},
		{name: "omp extension", target: "omp-auto", section: "auto_recall", row: "omp", key: "disabledExtensions",
			off: appendTo(func() string { return filepath.Join(sources.OmpConfigDir(), "config.yml") },
				"disabledExtensions:\n  - extension-module:deja\n")},
		{name: "gjc extension", target: "gjc-auto", section: "auto_recall", row: "gjc", key: "disabledExtensions",
			off: appendTo(func() string { return filepath.Join(sources.GjcConfigDir(), "config.yml") },
				"disabledExtensions: [extension-module:deja]\n")},
		{name: "cline plugin", target: "cline-auto", section: "auto_recall", row: "cline", key: "disabledPlugins",
			off: func(t *testing.T) {
				setJSON(filepath.Join(sources.ClineConfigDir(), "settings", "global-settings.json"), []string{"disabledPlugins"},
					[]any{filepath.Join(sources.ClinePluginsDir(), "deja", "index.js")})(t)
			}},
		{name: "hermes plugin", target: "hermes-auto", section: "auto_recall", row: "hermes", key: "plugins.enabled",
			off: replace(func() string { return filepath.Join(sources.HermesHome(), "config.yaml") },
				"  enabled:\n    - deja\n", "  enabled: []\n  disabled:\n    - deja\n")},
		{name: "dsh auto row", target: "deepseek-auto", section: "auto_recall", row: "deepseek", key: "deja-auto",
			off: replace(dshPatchPath, "- id: deja-auto\n", "- id: deja-auto\n      disabled: true\n")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			bin := filepath.Join(os.Getenv("HOME"), "bin", "deja")
			if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(c.target, bin, false); err != nil {
				t.Fatal(err)
			}
			if c.before != nil {
				c.before(t)
			}
			row := func() (state string, off bool) {
				t.Helper()
				out, err := captureRun(t, "doctor", "--json")
				if err != nil {
					t.Fatal(err)
				}
				var report map[string]json.RawMessage
				var rows []struct {
					Name        string `json:"name"`
					State       string `json:"state"`
					SwitchedOff bool   `json:"switched_off"`
				}
				if err := json.Unmarshal([]byte(out), &report); err != nil {
					t.Fatalf("doctor --json: %v", err)
				}
				if err := json.Unmarshal(report[c.section], &rows); err != nil {
					t.Fatalf("doctor --json %s: %v", c.section, err)
				}
				for _, r := range rows {
					if r.Name == c.row {
						return r.State, r.SwitchedOff
					}
				}
				t.Fatalf("no %s row in %s:\n%s", c.row, c.section, out)
				return "", false
			}
			if state, off := row(); state != "wired" || off {
				t.Fatalf("installed: %s switched_off=%v, want wired and on", state, off)
			}
			c.off(t)
			if state, off := row(); state != "wired" || !off {
				t.Errorf("after the client's switch: %s switched_off=%v, want wired and switched_off", state, off)
			}
			text, err := captureRun(t, "doctor")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, c.key) {
				t.Errorf("the text report does not name %s:\n%s", c.key, text)
			}
		})
	}
}

// An install over a client that keeps deja off by a switch install does not
// touch said only `unchanged` or `updated`, which reads as working (#4468,
// #4469).
func TestInstallSaysTheClientHasDejaSwitchedOff(t *testing.T) {
	hermeticEnv(t)
	settings := filepath.Join(sources.GeminiHome(), "settings.json")
	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	for path, body := range map[string]string{
		settings: `{"mcp":{"excluded":["deja"]}}`,
		claude:   `{"disableAllHooks":true}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := captureRun(t, "install", "gemini", "claude-auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`mcp.excluded` lists deja", "`disableAllHooks: true`"} {
		if !strings.Contains(out, want) {
			t.Errorf("install does not say %s:\n%s", want, out)
		}
	}
	// Control: with the switches gone, install has nothing to add.
	for _, path := range []string{settings, claude} {
		if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err = captureRun(t, "install", "gemini", "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "switched off") {
		t.Errorf("install calls an enabled client switched off:\n%s", out)
	}
}

// The readers answer "off" only for what the client would read as off.
func TestClientOffReadersStayOnWhenUnsure(t *testing.T) {
	if _, _, found := yamlLookup("plugins:\n  enabled: [\n    deja ]\n", "plugins", "enabled"); found {
		t.Error("a flow list over several lines read as an empty one")
	}
	if _, items, _ := yamlLookup("plugins:\n  enabled:\n  - spotify\n  - deja\n", "plugins", "enabled"); !anyDeja(items) {
		t.Errorf("items at the key's own indent: %q", items)
	}
	if !geminiExtensionEnabled([]string{"!/elsewhere/*"}, "/work/app") {
		t.Error("a rule for another directory turned the extension off")
	}
	if geminiExtensionEnabled([]string{"!/work/*"}, "/work/app") {
		t.Error("a subdirectory rule did not reach /work/app")
	}
	if !geminiExtensionEnabled([]string{"!/work/*", "/work/app/"}, "/work/app") {
		t.Error("the later rule did not win")
	}
	if got := piExtensionExcluded([]string{"!*", "+extensions/deja.ts"}, "/home/x/.pi/agent"); got != "" {
		t.Errorf("a force-include lost to a glob exclude: %q", got)
	}
	if !dshRowDisabled("- insert:\n    - id: mcp-deja\n      disabled: true\n    - id: deja-auto\n", "mcp-deja") ||
		dshRowDisabled("- insert:\n    - id: mcp-deja\n      disabled: true\n    - id: deja-auto\n", "deja-auto") {
		t.Error("a row's switch read for the wrong row")
	}
	if tomlDejaEntriesOff("[mcp_servers.deja]\ncommand = \"deja\"\n\n[other]\nenabled = false\n") {
		t.Error("another table's enabled read as deja's")
	}
	// Text inside a string is not a key, and YAML after a second document
	// marker is one the clients' loaders refuse rather than read.
	for _, y := range []string{
		"extensions:\n  deja:\n    description: \"two\n    enabled: false\"\n",
		"extensions:\n  deja:\n    description: 'two\n    enabled: false'\n",
		"extensions:\n  deja:\n    enabled: true\n---\nextensions:\n  deja:\n    enabled: false\n",
	} {
		if v, _, _ := yamlLookup(y, "extensions", "deja", "enabled"); v == "false" {
			t.Errorf("read as off: %q", y)
		}
	}
	for _, q := range []string{`"""`, `'''`} {
		toml := "[mcp_servers.deja]\ncommand = \"deja\"\nnote = " + q + "\nenabled = false\n" + q + "\n"
		if tomlDejaEntriesOff(toml) {
			t.Errorf("a line inside a %s string read as the switch", q)
		}
		if tomlTableValue("[features]\nnote = "+q+"\nhooks = false\n"+q+"\n", "features", "hooks") != "" {
			t.Errorf("a line inside a %s string read as [features] hooks", q)
		}
	}
}

// `deja install --all` adds the -auto sibling of a wired target, and both
// lines carried the same note about the same file (#4468).
func TestInstallSaysAClientSwitchOnce(t *testing.T) {
	hermeticEnv(t)
	settings := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"mcp":{"excluded":["deja"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "gemini", "gemini-auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "`mcp.excluded` lists deja"); n != 1 {
		t.Errorf("the note is said %d times, want once:\n%s", n, out)
	}
}

// Gemini starts every server when mcp.allowed is empty and compares names
// exactly; qwen reads both lists as `*` and `?` patterns and an empty allow
// list as none. Each case is how the client itself answers.
func TestGeminiAndQwenServerListsReadAsTheClientReadsThem(t *testing.T) {
	cases := []struct {
		name, client, settings, enablement string
		off                                bool
	}{
		{"gemini empty allowed", "gemini", `{"mcp":{"allowed":[]}}`, "", false},
		{"gemini allowed without deja", "gemini", `{"mcp":{"allowed":["other"]}}`, "", true},
		{"gemini excluded in another case", "gemini", `{"mcp":{"excluded":["Deja"]}}`, "", false},
		{"gemini enablement key in another case", "gemini", `{}`, `{"Deja":{"enabled":false}}`, false},
		{"gemini enablement", "gemini", `{}`, `{"deja":{"enabled":false}}`, true},
		{"qwen empty allowed", "qwen", `{"mcp":{"allowed":[]}}`, "", true},
		{"qwen allowed by pattern", "qwen", `{"mcp":{"allowed":["de*"]}}`, "", false},
		{"qwen excluded by pattern", "qwen", `{"mcp":{"excluded":["d?ja"]}}`, "", true},
		{"qwen excluded another pattern", "qwen", `{"mcp":{"excluded":["dej"]}}`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			home := sources.GeminiHome()
			if c.client == "qwen" {
				home = sources.QwenConfigDir()
			}
			write := func(p, body string) {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(home, "settings.json"), c.settings)
			if c.enablement != "" {
				write(filepath.Join(home, "mcp-server-enablement.json"), c.enablement)
			}
			if got := clientMCPDenied(c.client) != ""; got != c.off {
				t.Errorf("switched off = %v, want %v (%q)", got, c.off, clientMCPDenied(c.client))
			}
		})
	}
}

// Each client matches deja's id its own way, and a near miss is not an off
// switch: omp and gjc key extensions as extension-module:<name>, cline lists
// the module file, goose reads its plugin settings from ~/.config/goose
// whatever XDG says and lets a project's list overrule the user's, and every
// list compares exactly.
func TestClientPluginAndServerListsMatchOnlyWhatTheClientMatches(t *testing.T) {
	write := func(t *testing.T, p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userGoose := func() string { return filepath.Join(os.Getenv("HOME"), ".config", "goose", "settings.json") }
	cases := []struct {
		name, client string
		hooks        bool
		setup        func(t *testing.T)
		off          bool
	}{
		{"omp bare name", "omp", true, func(t *testing.T) {
			write(t, filepath.Join(sources.OmpConfigDir(), "config.yml"), "disabledExtensions:\n  - deja\n")
		}, false},
		{"omp module id", "omp", true, func(t *testing.T) {
			write(t, filepath.Join(sources.OmpConfigDir(), "config.yml"), "disabledExtensions:\n  - extension-module:deja\n")
		}, true},
		{"cline bare name", "cline", true, func(t *testing.T) {
			write(t, filepath.Join(sources.ClineConfigDir(), "settings", "global-settings.json"), `{"disabledPlugins":["deja"]}`)
		}, false},
		{"cline plugin dir", "cline", true, func(t *testing.T) {
			b, _ := json.Marshal(map[string]any{"disabledPlugins": []string{filepath.Join(sources.ClinePluginsDir(), "deja")}})
			write(t, filepath.Join(sources.ClineConfigDir(), "settings", "global-settings.json"), string(b))
		}, false},
		{"cline module file", "cline", true, func(t *testing.T) {
			b, _ := json.Marshal(map[string]any{"disabledPlugins": []string{filepath.Join(sources.ClinePluginsDir(), "deja", "index.js")}})
			write(t, filepath.Join(sources.ClineConfigDir(), "settings", "global-settings.json"), string(b))
		}, true},
		{"copilot other case", "copilot", false, func(t *testing.T) {
			write(t, filepath.Join(filepath.Dir(copilotMCPConfigPath()), "settings.json"), `{"disabledMcpServers":["Deja"]}`)
		}, false},
		{"goose under XDG", "goose", true, func(t *testing.T) {
			xdg := filepath.Join(os.Getenv("HOME"), "xdg")
			t.Setenv("XDG_CONFIG_HOME", xdg)
			write(t, filepath.Join(xdg, "goose", "settings.json"), `{"disabledPlugins":["deja"]}`)
		}, false},
		{"goose user file with XDG set", "goose", true, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), "xdg"))
			write(t, userGoose(), `{"disabledPlugins":["deja"]}`)
		}, true},
		{"goose project enables it", "goose", true, func(t *testing.T) {
			write(t, userGoose(), `{"disabledPlugins":["deja"]}`)
			project := filepath.Join(os.Getenv("HOME"), "work")
			write(t, filepath.Join(project, ".config", "goose", "settings.json"), `{"enabledPlugins":["deja"]}`)
			t.Chdir(project)
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			t.Chdir(t.TempDir())
			c.setup(t)
			note := clientMCPDenied(c.client)
			if c.hooks {
				note = clientHooksOff(c.client)
			}
			if got := note != ""; got != c.off {
				t.Errorf("switched off = %v, want %v (%q)", got, c.off, note)
			}
		})
	}
}

// opencode turns its `tools` map into rules and the last key that matches
// decides, in the order the file lists them (#4468).
func TestOpencodeToolsLastMatchingKeyWins(t *testing.T) {
	for body, off := range map[string]bool{
		`{"tools":{"*":false,"deja_deja":true}}`:  false,
		`{"tools":{"deja_deja":true,"*":false}}`:  true,
		`{"tools":{"deja*":false}}`:               true,
		`{"tools":{"deja_[d]eja":false}}`:         false,
		`{"tools":{"deja?deja":false,"x":true}}`:  true,
		`{"tools":{"other_*":false}}`:             false,
		`{"tools":{"deja_*":false,"deja*":true}}`: false,
	} {
		hermeticEnv(t)
		p := doctorOpencodeConfigPath()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := clientMCPDenied("opencode") != ""; got != off {
			t.Errorf("%s: switched off = %v, want %v", body, got, off)
		}
	}
}

// claude-code keys its per-project lists by the repository's main root and
// cursor-agent by the nearest root with a .git, both by the real path; a
// subdirectory's own entry is one neither reads (#4468).
func TestPerProjectListsAreReadForTheDirectoryTheClientKeys(t *testing.T) {
	write := func(t *testing.T, p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claudeOff := func(t *testing.T, dir string) {
		b, _ := json.Marshal(map[string]any{"projects": map[string]any{dir: map[string]any{"disabledMcpServers": []string{"deja"}}}})
		write(t, sources.ClaudeJSONPath(), string(b))
	}
	cursorOff := func(t *testing.T, dir string) {
		write(t, filepath.Join(cursorDataDir(), "projects", cursorProjectSlug(dir), "mcp-disabled.json"), `["deja"]`)
	}
	setup := func(t *testing.T) (main, sub, wt string) {
		hermeticEnv(t)
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		main = filepath.Join(base, "repo")
		sub = filepath.Join(main, "pkg")
		if err := os.MkdirAll(filepath.Join(main, ".git", "worktrees", "wt"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(main, ".git", "worktrees", "wt", "commondir"), "../..\n")
		wt = filepath.Join(base, "wt")
		write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
		return main, sub, wt
	}
	cases := []struct {
		name   string
		client string
		run    func(t *testing.T, main, sub, wt string) string // the dir doctor runs in
		off    bool
	}{
		{"claude subdir entry", "claude-code", func(t *testing.T, main, sub, wt string) string { claudeOff(t, sub); return sub }, false},
		{"claude repo entry from subdir", "claude-code", func(t *testing.T, main, sub, wt string) string { claudeOff(t, main); return sub }, true},
		{"claude main entry from worktree", "claude-code", func(t *testing.T, main, sub, wt string) string { claudeOff(t, main); return wt }, true},
		{"claude worktree entry", "claude-code", func(t *testing.T, main, sub, wt string) string { claudeOff(t, wt); return wt }, false},
		{"cursor subdir entry", "cursor", func(t *testing.T, main, sub, wt string) string { cursorOff(t, sub); return sub }, false},
		{"cursor repo entry from subdir", "cursor", func(t *testing.T, main, sub, wt string) string { cursorOff(t, main); return sub }, true},
		{"cursor worktree entry", "cursor", func(t *testing.T, main, sub, wt string) string { cursorOff(t, wt); return wt }, true},
		{"claude real path through a link", "claude-code", func(t *testing.T, main, sub, wt string) string {
			claudeOff(t, main)
			link := filepath.Join(filepath.Dir(main), "link")
			if err := os.Symlink(main, link); err != nil {
				t.Skip(err)
			}
			return link
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			main, sub, wt := setup(t)
			t.Chdir(c.run(t, main, sub, wt))
			if got := clientMCPDenied(c.client) != ""; got != c.off {
				t.Errorf("switched off = %v, want %v (%q)", got, c.off, clientMCPDenied(c.client))
			}
		})
	}
	// Gemini matches its extension rules against the real path too.
	t.Run("gemini extension rule through a link", func(t *testing.T) {
		main, _, _ := setup(t)
		link := filepath.Join(filepath.Dir(main), "link")
		if err := os.Symlink(main, link); err != nil {
			t.Skip(err)
		}
		b, _ := json.Marshal(map[string]any{"deja": map[string]any{"overrides": []string{"!" + main + "/*"}}})
		write(t, filepath.Join(sources.GeminiHome(), "extensions", "extension-enablement.json"), string(b))
		t.Chdir(link)
		if clientHooksOff("gemini") == "" {
			t.Error("a rule over the real path did not reach a directory entered through a link")
		}
	})
}

// A project's own settings file overrides the user's for the hooks switch in
// claude-code, qwen and gemini, so a user-wide off with the project turning
// hooks back on runs them there (#4469).
func TestProjectSettingsTurnTheHooksSwitchBackOn(t *testing.T) {
	cases := []struct {
		client, user, projectFile, project string
	}{
		{"claude-code", `{"disableAllHooks":true}`, ".claude/settings.json", `{"disableAllHooks":false}`},
		{"claude-code", `{"disableAllHooks":true}`, ".claude/settings.local.json", `{"disableAllHooks":false}`},
		{"qwen", `{"disableAllHooks":true}`, ".qwen/settings.json", `{"disableAllHooks":false}`},
		{"gemini", `{"hooksConfig":{"enabled":false}}`, ".gemini/settings.json", `{"hooksConfig":{"enabled":true}}`},
	}
	for _, c := range cases {
		t.Run(c.client+" "+c.projectFile, func(t *testing.T) {
			hermeticEnv(t)
			dir := map[string]string{"claude-code": sources.ClaudeConfigDir(), "qwen": sources.QwenConfigDir(), "gemini": sources.GeminiHome()}[c.client]
			write := func(p, body string) {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(dir, "settings.json"), c.user)
			project := t.TempDir()
			t.Chdir(project)
			if clientHooksOff(c.client) == "" {
				t.Fatal("control: the user's switch alone does not read as off")
			}
			write(filepath.Join(project, filepath.FromSlash(c.projectFile)), c.project)
			if note := clientHooksOff(c.client); note != "" {
				t.Errorf("the project turned hooks back on and doctor still says %q", note)
			}
		})
	}
}

// Two switches the first pass missed: codex still reads `codex_hooks`, the
// feature's old key, and VS Code's `registry` access blocks a server it has
// no gallery entry for, which deja's never has (#4468, #4469).
func TestCodexLegacyHooksKeyAndVSCodeRegistryAccess(t *testing.T) {
	write := func(t *testing.T, p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("codex_hooks", func(t *testing.T) {
		hermeticEnv(t)
		p := filepath.Join(sources.CodexHome(), "config.toml")
		write(t, p, "[features]\ncodex_hooks = false\n")
		if clientHooksOff("codex-hook") == "" {
			t.Error("`codex_hooks = false` does not read as off")
		}
		write(t, p, "[features]\ncodex_hooks = false\nhooks = true\n")
		if note := clientHooksOff("codex-hook"); note != "" {
			t.Errorf("the current key set on still reads as off: %q", note)
		}
	})
	t.Run("vscode registry", func(t *testing.T) {
		hermeticEnv(t)
		p := filepath.Join(filepath.Dir(doctorVSCodeMCPPath()), "settings.json")
		write(t, p, `{"chat.mcp.access":"registry"}`)
		if clientMCPDenied("vscode") == "" {
			t.Error("`registry` access does not read as off")
		}
		write(t, p, `{"chat.mcp.access":"all"}`)
		if note := clientMCPDenied("vscode"); note != "" {
			t.Errorf("`all` reads as off: %q", note)
		}
	})
}

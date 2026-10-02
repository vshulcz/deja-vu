package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// switchJSON sets one key deep in a JSON config, the way a client's own
// `disable` command or a hand edit leaves it.
func switchJSON(t *testing.T, path string, value any, keys ...string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	m := root
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			t.Fatalf("%s has no %q on the way to %v:\n%s", path, k, keys, b)
		}
		m = next
	}
	m[keys[len(keys)-1]] = value
	out, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSONPath(t *testing.T, path string, keys ...string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, k := range keys {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

func switchText(t *testing.T, path, from, to string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(string(b), from, to, 1)
	if next == string(b) {
		t.Fatalf("%s has no %q to switch:\n%s", path, from, b)
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An install over deja's own entry turned it back on wherever the writer built
// a fresh entry rather than merging into the old one, and said nothing about
// it: the next client start ran deja again for somebody who had switched it
// off (#4467). The plugin registries did the same through the -auto targets,
// openclaw while reporting "unchanged" (#4472). Install keeps the switch where
// the reader put it and says so, the way cursor's entry always has (#2479).
func TestInstallKeepsTheReadersOffSwitch(t *testing.T) {
	for _, tc := range []struct {
		target   string
		off      func(t *testing.T)
		stillOff func(t *testing.T) bool
	}{
		{
			// The control: the shared writer has kept it since #2479.
			target: "cursor",
			off: func(t *testing.T) {
				switchJSON(t, filepath.Join(sources.CursorCLIHome(), "mcp.json"), true, "mcpServers", "deja", "disabled")
			},
			stillOff: func(t *testing.T) bool {
				return readJSONPath(t, filepath.Join(sources.CursorCLIHome(), "mcp.json"), "mcpServers", "deja", "disabled") == true
			},
		},
		{
			target: "prime",
			off:    func(t *testing.T) { switchJSON(t, primeSettingsPath(), false, "mcpServers", "deja", "enabled") },
			stillOff: func(t *testing.T) bool {
				return readJSONPath(t, primeSettingsPath(), "mcpServers", "deja", "enabled") == false
			},
		},
		{
			target: "zed",
			off: func(t *testing.T) {
				switchJSON(t, sources.ZedSettingsPath(), false, zedServerKey, zedServerID, "enabled")
			},
			stillOff: func(t *testing.T) bool {
				return readJSONPath(t, sources.ZedSettingsPath(), zedServerKey, zedServerID, "enabled") == false
			},
		},
		{
			target: "zcode",
			off:    func(t *testing.T) { switchJSON(t, zcodeConfigPath(), false, "mcp", "servers", "deja", "enabled") },
			stillOff: func(t *testing.T) bool {
				return readJSONPath(t, zcodeConfigPath(), "mcp", "servers", "deja", "enabled") == false
			},
		},
		{
			target: "goose",
			off: func(t *testing.T) {
				switchText(t, filepath.Join(gooseConfigDir(), "config.yaml"), "enabled: true", "enabled: false")
			},
			stillOff: func(t *testing.T) bool {
				return strings.Contains(fileText(t, filepath.Join(gooseConfigDir(), "config.yaml")), "enabled: false")
			},
		},
		{
			target: "hermes",
			off: func(t *testing.T) {
				switchText(t, filepath.Join(sources.HermesHome(), "config.yaml"), "enabled: true", "enabled: false")
			},
			stillOff: func(t *testing.T) bool {
				return strings.Contains(fileText(t, filepath.Join(sources.HermesHome(), "config.yaml")), "enabled: false")
			},
		},
		{
			target: "codex",
			off: func(t *testing.T) {
				switchText(t, filepath.Join(sources.CodexHome(), "config.toml"), "[mcp_servers.deja]\n", "[mcp_servers.deja]\nenabled = false\n")
			},
			stillOff: func(t *testing.T) bool {
				return strings.Contains(fileText(t, filepath.Join(sources.CodexHome(), "config.toml")), "enabled = false")
			},
		},
		{
			target: "grok",
			off: func(t *testing.T) {
				switchText(t, filepath.Join(sources.GrokHome(), "config.toml"), "[mcp_servers.deja]\n", "[mcp_servers.deja]\nenabled = false\n")
			},
			stillOff: func(t *testing.T) bool {
				return strings.Contains(fileText(t, filepath.Join(sources.GrokHome(), "config.toml")), "enabled = false")
			},
		},
		{
			target: "deepseek-auto",
			off: func(t *testing.T) {
				path := filepath.Join(sources.DSHHome(), "cordis.patch.yml")
				switchText(t, path, "    - id: mcp-deja\n", "    - id: mcp-deja\n      disabled: true\n")
				switchText(t, path, "    - id: deja-auto\n", "    - id: deja-auto\n      disabled: true\n")
			},
			stillOff: func(t *testing.T) bool {
				return strings.Count(fileText(t, filepath.Join(sources.DSHHome(), "cordis.patch.yml")), "disabled: true") == 2
			},
		},
		{
			// `openclaw plugins disable deja` and `openclaw hooks disable
			// deja-recall`.
			target: "openclaw-auto",
			off: func(t *testing.T) {
				path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
				switchJSON(t, path, false, "plugins", "entries", openclawPluginID, "enabled")
				switchJSON(t, path, false, "hooks", "internal", "entries", openclawHookName, "enabled")
			},
			stillOff: func(t *testing.T) bool {
				path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
				return readJSONPath(t, path, "plugins", "entries", openclawPluginID, "enabled") == false &&
					readJSONPath(t, path, "hooks", "internal", "entries", openclawHookName, "enabled") == false
			},
		},
		{
			// `hermes plugins disable deja` moves the name from one list to
			// the other.
			target: "hermes-auto",
			off: func(t *testing.T) {
				switchText(t, filepath.Join(sources.HermesHome(), "config.yaml"),
					"plugins:\n  enabled:\n    - deja\n", "plugins:\n  enabled: []\n  disabled:\n  - deja\n")
			},
			stillOff: func(t *testing.T) bool {
				return strings.Contains(fileText(t, filepath.Join(sources.HermesHome(), "config.yaml")), "  enabled: []\n  disabled:\n  - deja\n")
			},
		},
		{
			// `reasonix plugin disable deja`.
			target: "reasonix-auto",
			off: func(t *testing.T) {
				switchText(t, reasonixStatePath(), `"enabled": true`, `"enabled": false`)
			},
			stillOff: func(t *testing.T) bool {
				return strings.Contains(fileText(t, reasonixStatePath()), `"enabled": false`)
			},
		},
	} {
		t.Run(tc.target, func(t *testing.T) {
			hermeticEnv(t)
			noReasonixCLI(t)
			if _, err := captureRun(t, "install", tc.target, "--no-index"); err != nil {
				t.Fatalf("install: %v", err)
			}
			// The record of the binaries deja wrote is cached per process,
			// and another test's home may have filled it.
			forgetWrittenExes()
			t.Cleanup(forgetWrittenExes)
			tc.off(t)
			if !tc.stillOff(t) {
				t.Fatal("the fixture did not switch the entry off")
			}
			out, err := captureRun(t, "install", tc.target, "--no-index")
			if err != nil {
				t.Fatalf("second install: %v", err)
			}
			if !tc.stillOff(t) {
				t.Errorf("install turned deja back on over the reader's off switch:\n%s", out)
			}
			if !strings.Contains(out, "switched off") {
				t.Errorf("install left deja off and did not say so:\n%s", out)
			}
		})
	}
}

// gemini's hooksConfig.enabled defaults to true, so a false there is the
// reader turning every hook off. Install turned it on with nothing but "also
// updated", and uninstall left it on, so their own hooks ran from then on
// (#4471, the shape #4431 fixed for zcode).
func TestGeminiAutoGivesBackTheHooksSwitch(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	theirs := "{\n  \"hooksConfig\": {\n    \"enabled\": false\n  }\n}\n"
	writeTestFile(t, path, theirs)
	out, err := captureRun(t, "install", "gemini-auto", "--no-index")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(out, "hooksConfig.enabled") {
		t.Errorf("install turned on hooks the reader had off and did not say so:\n%s", out)
	}
	if out, err = captureRun(t, "uninstall", "gemini-auto"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if got := fileText(t, path); got != theirs {
		t.Errorf("settings.json did not come back as it was.\nbefore:\n%s\nafter:\n%s\nuninstall said:\n%s", theirs, got, out)
	}
	if strings.Contains(out, "left hooksConfig.enabled on") {
		t.Errorf("uninstall says it left the switch on after turning it back off:\n%s", out)
	}
}

// A reader who had openclaw's internal hooks on when deja came, and switched
// them off later, got "uninstall turns it back off" from the next install, and
// the uninstall then turned them on: the record kept the first install's
// "on". A false at install is always the reader's, since deja only ever
// writes true (#4472).
func TestOpenClawUninstallKeepsAnOffTheReaderSetAfterInstall(t *testing.T) {
	for _, spelling := range []string{"json", "jsonc"} {
		t.Run(spelling, func(t *testing.T) {
			hermeticEnv(t)
			path := openclawConfigPath()
			head := ""
			if spelling == "jsonc" {
				head = "// mine\n"
			}
			writeTestFile(t, path, head+"{\n  \"hooks\": {\n    \"internal\": {\n      \"enabled\": true\n    }\n  }\n}\n")
			if _, err := captureRun(t, "install", "openclaw-auto", "--no-index"); err != nil {
				t.Fatalf("install: %v", err)
			}
			// The switch alone, as a hand edit leaves it, comments and all.
			off := strings.Replace(fileText(t, path), "\n      \"enabled\": true", "\n      \"enabled\": false", 1)
			if err := os.WriteFile(path, []byte(off), 0o644); err != nil {
				t.Fatal(err)
			}
			if readJSONPath(t, path, "hooks", "internal", "enabled") != false {
				t.Fatalf("the fixture did not switch internal hooks off:\n%s", fileText(t, path))
			}
			out, err := captureRun(t, "install", "openclaw-auto", "--no-index")
			if err != nil {
				t.Fatalf("second install: %v", err)
			}
			if !strings.Contains(out, "uninstall turns it back off") {
				t.Errorf("install turned internal hooks on and did not say so:\n%s", out)
			}
			if _, err := captureRun(t, "uninstall", "openclaw-auto"); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if got := readJSONPath(t, path, "hooks", "internal", "enabled"); got != false {
				t.Errorf("uninstall left hooks.internal.enabled %v after install said it would turn it back off:\n%s", got, fileText(t, path))
			}
		})
	}
}

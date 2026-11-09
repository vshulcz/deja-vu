package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The desktop's MCP server and its hooks do not live in the same file on a
// 3.17+ build: install writes the server to setting.json (#4429) and a second
// copy of the hooks into config.json (#5960), while the runtime loads only
// config.json. zcodeDoctorPath() names setting.json as soon as it exists, so
// both doctor rows can point at a file the desktop never reads. Each row must
// name the live config only when that file really carries its own wiring.

const zcodeLiveHooks = `"hooks":{"enabled":true,"events":{"SessionStart":[{"hooks":[{"type":"command","command":"/opt/bin/deja.exe hook-context --strict","timeout":30}]}]}}`

func setupZCodeDoctorFixture(t *testing.T, config, setting string, marker bool, version [3]int, versionOK bool) (configPath, settingPath string) {
	t.Helper()
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	configPath = filepath.Join(dir, "config.json")
	settingPath = filepath.Join(dir, "setting.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if setting != "" {
		if err := os.WriteFile(settingPath, []byte(setting), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if marker {
		if err := os.MkdirAll(filepath.Join(dir, "migrations"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "migrations", "settings-v1.json"), []byte(`{"schemaVersion":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := zcodeDesktopVersion
	zcodeDesktopVersion = func() ([3]int, bool) { return version, versionOK }
	t.Cleanup(func() { zcodeDesktopVersion = old })
	return configPath, settingPath
}

func TestZCodeDoctorMCPPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string // "" means no config.json
		setting   string // "" means no setting.json
		marker    bool
		version   [3]int
		versionOK bool
		wantPath  string // "config" or "setting"
		wantWired bool
	}{
		{
			name:     "fresh machine keeps the golden setting.json path",
			wantPath: "setting",
		},
		{
			name:      "migrated 3144 runtime reads setting.json",
			setting:   `{"mcp":{"servers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}},"hooks":{"enabled":true,"events":{}}}`,
			marker:    true,
			wantPath:  "setting",
			wantWired: true,
		},
		{
			// zcodeDoctorPath() already names config.json here: setting.json is
			// absent and config.json is there. No deja server in it, so the row
			// must report missing rather than claim the live file is wired.
			name:    "317 live config without a server stays missing",
			config:  `{"mcp":{"servers":{"docs":{"command":"docs-server"}}},` + zcodeLiveHooks + `}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
		},
		{
			name:    "317 live config with a hand-wired server is reported there",
			config:  `{"mcp":{"servers":{"deja-vu":{"type":"stdio","command":"node","args":["/opt/deja/bin/deja","mcp"]}}},` + zcodeLiveHooks + `}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath:  "config",
			wantWired: true,
		},
		{
			name:    "317 live config with a literal deja key is reported there",
			config:  `{"mcp":{"servers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}},` + zcodeLiveHooks + `}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath:  "config",
			wantWired: true,
		},
		{
			name:    "setting.json present but runtime still uses config.json",
			config:  `{"mcp":{"servers":{"deja-vu":{"command":"node","args":["/opt/deja/bin/deja","mcp"]}}},` + zcodeLiveHooks + `}`,
			setting: `{"mcp":{"servers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}}}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath:  "config",
			wantWired: true,
		},
		{
			// An unparseable setting.json beside an empty config.json: the live
			// check falls back to zcodeDoctorPath(), and doctorZCodeWired takes
			// its substring fallback rather than crashing on the bad JSON.
			name:     "unparseable setting.json with an empty config.json",
			config:   `{}`,
			setting:  `{"mcp": {"servers": {`,
			wantPath: "setting",
		},
		{
			// The live config itself does not parse: the row must fall back to
			// zcodeDoctorPath() instead of naming a file it cannot read.
			name:    "unparseable live config falls back",
			config:  `{"mcp": {"servers": {`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, setting := setupZCodeDoctorFixture(t, tc.config, tc.setting, tc.marker, tc.version, tc.versionOK)
			got := zcodeDoctorMCPPath()
			want := setting
			if tc.wantPath == "config" {
				want = config
			}
			if got != want {
				t.Errorf("zcodeDoctorMCPPath() = %q, want %q", got, want)
			}
			if wired := doctorZCodeWired(got); wired != tc.wantWired {
				t.Errorf("doctorZCodeWired(%q) = %v, want %v", got, wired, tc.wantWired)
			}
		})
	}
}

func TestZCodeDoctorHooksPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string
		setting   string
		marker    bool
		version   [3]int
		versionOK bool
		wantPath  string
		wantRun   bool // zcodeHooksRun on the reported path
	}{
		{
			name:     "fresh machine keeps the golden setting.json path",
			wantPath: "setting",
		},
		{
			name:     "migrated 3144 runtime reads setting.json",
			setting:  `{"mcp":{"servers":{}},` + zcodeLiveHooks + `}`,
			marker:   true,
			wantPath: "setting",
			wantRun:  true,
		},
		{
			name:    "317 live config with deja hooks is reported there",
			config:  `{"mcp":{"servers":{}},` + zcodeLiveHooks + `}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
			wantRun:  true,
		},
		{
			name:    "setting.json present but runtime still uses config.json",
			config:  `{"mcp":{"servers":{}},` + zcodeLiveHooks + `}`,
			setting: `{"mcp":{"servers":{}},"hooks":{"enabled":true,"events":{"SessionStart":[{"hooks":[{"type":"command","command":"/tmp/mine.sh","timeout":10}]}]}}}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
			wantRun:  true,
		},
		{
			// A live config without deja's hooks must not be named as wired:
			// fall back to zcodeDoctorPath() (config.json here, still unwired).
			name:    "317 live config without deja hooks stays on the fallback",
			config:  `{"mcp":{"servers":{}},"hooks":{"enabled":true,"events":{"PreToolUse":[{"hooks":[{"type":"command","command":"/tmp/mine.sh","timeout":10}]}]}}}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
		},
		{
			// An unparseable setting.json beside an empty config.json, the same
			// shape as the MCP row's fallback case: neither row should name a
			// live config that carries no deja wiring.
			name:     "unparseable setting.json with an empty config.json",
			config:   `{}`,
			setting:  `{"mcp": {"servers": {`,
			wantPath: "setting",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, setting := setupZCodeDoctorFixture(t, tc.config, tc.setting, tc.marker, tc.version, tc.versionOK)
			got := zcodeDoctorHooksPath()
			want := setting
			if tc.wantPath == "config" {
				want = config
			}
			if got != want {
				t.Errorf("zcodeDoctorHooksPath() = %q, want %q", got, want)
			}
			b, _ := os.ReadFile(got)
			if run := zcodeHooksRun(b); run != tc.wantRun {
				t.Errorf("zcodeHooksRun(%q) = %v, want %v", got, run, tc.wantRun)
			}
		})
	}
}

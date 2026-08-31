package main

import (
	"github.com/vshulcz/deja-vu/internal/sources"
	"os"
	"path/filepath"
	"testing"
)

// Every writer that can add a container to someone else's config, seeded twice:
// once where the reader has no such block, once where they have one of their own
// with an entry in it. Install, uninstall, and the file has to come back byte
// for byte.
//
// #2604 gave the JSON writers provenance for the block they add and #2672 gave
// it to hermes' plugins — found by a whole-machine round trip somebody happened
// to run. Each fix is pinned on its own writer and nothing pinned the set, which
// is the state the ignore rule was in before #2661 (#2678).
//
// The reader's own entries are written expanded rather than inline: an inline
// object is still re-flowed by the JSON writers (#2640), which is a separate
// complaint and would drown this one.

// zedSettingsRel is Zed's settings file as a path under the test home.
func zedSettingsRel() string {
	rel, err := filepath.Rel(sources.Home(), sources.ZedSettingsPath())
	if err != nil {
		return filepath.Join(".config", "zed", "settings.json")
	}
	return filepath.ToSlash(rel)
}

func TestEveryWriterGivesBackTheBlockItMade(t *testing.T) {
	for _, tc := range []struct {
		name, target, rel, body string
	}{
		{"cline, no block", "cline", ".cline/data/settings/cline_mcp_settings.json", "{\n  \"theme\": \"dark\"\n}\n"},
		{"cline, their own server", "cline", ".cline/data/settings/cline_mcp_settings.json",
			"{\n  \"mcpServers\": {\n    \"theirs\": {\n      \"command\": \"x\"\n    }\n  }\n}\n"},
		{"openclaw, no block", "openclaw", ".openclaw/openclaw.json", "{\n  \"theme\": \"dark\"\n}\n"},
		{"openclaw, their own server", "openclaw", ".openclaw/openclaw.json",
			"{\n  \"mcpServers\": {\n    \"theirs\": {\n      \"command\": \"x\"\n    }\n  }\n}\n"},
		{"pi, no block", "pi", ".pi/agent/mcp.json", "{\n  \"theme\": \"dark\"\n}\n"},
		{"omp, no block", "omp", ".omp/agent/mcp.json", "{\n  \"theme\": \"dark\"\n}\n"},
		{"kimi, no block", "kimi", ".kimi-code/mcp.json", "{\n  \"theme\": \"dark\"\n}\n"},
		// Zed keeps its settings where the platform puts them rather than
		// under ~/.config, so this row asks rather than spells (#2808).
		{"zed, no block", "zed", zedSettingsRel(), "{\n  \"theme\": \"dark\"\n}\n"},
		{"deepseek, no patch list", "deepseek", ".dsh/cordis.patch.yml", "# my patches\n"},
		{"deepseek, their own patch", "deepseek", ".dsh/cordis.patch.yml",
			"# my patches\n- insert:\n    - id: mine\n      name: \"@me/thing\"\n"},
		{"goose, no extensions block", "goose", ".config/goose/config.yaml", "GOOSE_MODEL: gpt-5\n"},
		{"goose, their own extension", "goose", ".config/goose/config.yaml",
			"GOOSE_MODEL: gpt-5\n\nextensions:\n  mine:\n    enabled: true\n"},
		{"hermes, no mcp block", "hermes", ".hermes/config.yaml", "profile: default\n"},
		{"hermes, their own server", "hermes", ".hermes/config.yaml",
			"profile: default\n\nmcp_servers:\n  theirs:\n    command: \"x\"\n"},
		{"codex, no mcp block", "codex", ".codex/config.toml", "[tools]\nweb = true\n"},
		{"grok, no mcp block", "grok", ".grok/config.toml", "[tools]\nweb = true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(tc.target, "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			wired, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(wired) == tc.body {
				t.Fatalf("install changed nothing, so the uninstall proves nothing:\n%s", wired)
			}
			if _, err := installTarget(tc.target, "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			back, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("the reader's config is gone: %v", err)
			}
			if string(back) != tc.body {
				t.Fatalf("the config did not come back as it was:\nwant:\n%s\ngot:\n%s", tc.body, back)
			}
		})
	}
}

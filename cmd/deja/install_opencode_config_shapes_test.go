package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func opencodeV1(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	opencodeVersionMajor = func() int { return 1 }
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// opencode reads tui.json as JSONC: 1.18.35 logs "applying tui config" for a
// file with a comment and a trailing comma. deja decoded it as strict JSON, so
// `install opencode-auto` failed the whole target after writing half of it —
// no skill, no /deja command — and uninstall failed on the same file.
func TestOpencodeAutoTakesACommentedTuiJSON(t *testing.T) {
	dir := opencodeV1(t)
	list := filepath.Join(dir, "tui.json")
	seed := "{\n  // my tui settings\n  \"$schema\": \"https://opencode.ai/tui.json\",\n  \"theme\": \"tokyonight\",\n}\n"
	if err := os.WriteFile(list, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "opencode-auto", "--no-index")
	if err != nil {
		t.Fatalf("install refused a tui.json opencode loads: %v\n%s", err, out)
	}
	if !strings.Contains(out, "./plugins/deja-status.tsx") {
		t.Errorf("install did not say what to add to the commented tui.json:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "commands", "deja.md")); err != nil {
		t.Errorf("install stopped before the /deja command: %v", err)
	}
	if b, _ := os.ReadFile(list); string(b) != seed {
		t.Errorf("install rewrote the commented tui.json:\n%s", b)
	}
	if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
		t.Fatalf("uninstall refused it: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(list); string(b) != seed {
		t.Errorf("uninstall changed the commented tui.json:\n%s", b)
	}
}

// A reader who followed install's note and listed deja's status-line plugin
// in a commented tui.json was left by uninstall with a list entry pointing at
// the file it had just deleted (#5313). Only deja's element goes, as text:
// the comment stays, and so do the reader's own entries.
func TestUninstallTakesDejasEntryOutOfACommentedTuiJSON(t *testing.T) {
	for _, c := range []struct{ seed, want string }{
		{
			"{\n  // my tui settings\n  \"$schema\": \"https://opencode.ai/tui.json\",\n  \"theme\": \"tokyonight\",\n  \"plugin\": [\"./plugins/deja-status.tsx\"]\n}\n",
			"{\n  // my tui settings\n  \"$schema\": \"https://opencode.ai/tui.json\",\n  \"theme\": \"tokyonight\"\n}\n",
		},
		{
			"{\n  // my tui settings\n  \"plugin\": [\n    // mine first\n    \"./plugins/mine.tsx\",\n    \"./plugins/deja-status.tsx\",\n  ],\n  \"theme\": \"tokyonight\"\n}\n",
			"{\n  // my tui settings\n  \"plugin\": [\n    // mine first\n    \"./plugins/mine.tsx\",\n  ],\n  \"theme\": \"tokyonight\"\n}\n",
		},
	} {
		dir := opencodeV1(t)
		list := filepath.Join(dir, "tui.json")
		if err := os.WriteFile(list, []byte(c.seed), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := captureRun(t, "uninstall", "opencode-auto")
		if err != nil {
			t.Fatalf("uninstall refused it: %v\n%s", err, out)
		}
		if strings.Contains(out, "from its plugin list") {
			t.Errorf("uninstall left the entry to the reader:\n%s", out)
		}
		if b, _ := os.ReadFile(list); string(b) != c.want {
			t.Errorf("uninstall left\n%s\nwant\n%s", b, c.want)
		}
	}
}

// A tui.json the reader had is theirs to keep: uninstall deleted every one
// that held nothing but what install found or added — an empty file, `{}`,
// an empty plugin list, opencode's own $schema line.
func TestUninstallKeepsTheReadersOwnTuiJSON(t *testing.T) {
	for _, seed := range []string{
		"",
		"{}\n",
		"{\n  \"plugin\": []\n}\n",
		"{\n  \"$schema\": \"https://opencode.ai/tui.json\"\n}\n",
	} {
		dir := opencodeV1(t)
		list := filepath.Join(dir, "tui.json")
		if err := os.WriteFile(list, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "install", "opencode-auto", "--no-index"); err != nil {
			t.Fatalf("%q: install: %v\n%s", seed, err, out)
		}
		if got := tuiPluginList(t, list); strings.Join(got, ",") != "./plugins/deja-status.tsx" {
			t.Errorf("%q: install listed %v", seed, got)
		}
		if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
			t.Fatalf("%q: uninstall: %v\n%s", seed, err, out)
		}
		b, err := os.ReadFile(list)
		if err != nil {
			t.Errorf("%q: uninstall deleted the reader's tui.json", seed)
			continue
		}
		if string(b) != seed {
			t.Errorf("%q: uninstall left %q", seed, b)
		}
	}
	// The control: a tui.json install created goes with the entry.
	dir := opencodeV1(t)
	if out, err := captureRun(t, "install", "opencode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "tui.json")); err == nil {
		t.Error("uninstall left the tui.json install created")
	}
}

// opencode.jsonc holding plain JSON on one line — `{}`, the $schema line, a
// minified config — was refused by install while the same bytes in
// opencode.json were edited, and an empty `"mcp": {}` came back from the round
// trip split over two lines.
func TestOpencodeJSONCWithPlainJSONIsEdited(t *testing.T) {
	for _, seed := range []string{
		"{}\n",
		"{\"$schema\": \"https://opencode.ai/config.json\"}\n",
		`{"$schema":"https://opencode.ai/config.json","mcp":{"mine":{"type":"local","command":["my-server"]}},"model":"openai/gpt-5"}`,
		"{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {}\n}\n",
	} {
		dir := opencodeV1(t)
		cfg := filepath.Join(dir, "opencode.jsonc")
		if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "install", "opencode-auto", "--no-index"); err != nil {
			t.Errorf("%q: install refused: %v\n%s", seed, err, out)
			continue
		}
		b, _ := os.ReadFile(cfg)
		var root map[string]any
		if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
			t.Fatalf("%q: install wrote a config that does not parse: %v\n%s", seed, err, b)
		}
		if mcp, _ := root["mcp"].(map[string]any); mcp["deja"] == nil {
			t.Errorf("%q: no deja under mcp:\n%s", seed, b)
		}
		if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
			t.Fatalf("%q: uninstall: %v\n%s", seed, err, out)
		}
		if b, _ := os.ReadFile(cfg); string(b) != seed {
			t.Errorf("%q: uninstall left %q", seed, b)
		}
	}
}

// Uninstall on a config deja never wrote leaves its bytes alone. The text
// writer opened a one-line block on the way through and wrote that back, with
// a .bak beside it, though there was no deja entry to take out.
func TestOpencodeUninstallLeavesAConfigWithoutDejaAlone(t *testing.T) {
	for _, seed := range []string{
		"{\n  // mine\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {}\n}\n",
		"{\n  // mine\n  \"mcp\": {\"mine\": {\"type\": \"local\", \"command\": [\"my-server\"]}}\n}\n",
		`{"$schema":"https://opencode.ai/config.json","mcp":{"mine":{"type":"local","command":["x"]}}}`,
	} {
		dir := opencodeV1(t)
		cfg := filepath.Join(dir, "opencode.jsonc")
		if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
			t.Fatalf("%q: uninstall: %v\n%s", seed, err, out)
		}
		if b, _ := os.ReadFile(cfg); string(b) != seed {
			t.Errorf("%q: uninstall rewrote it to %q", seed, b)
		}
		if _, err := os.Stat(cfg + ".bak"); err == nil {
			t.Errorf("%q: uninstall left a snapshot of a file it did not change", seed)
		}
	}
}

// What opencode 1.18.35 makes of an opencode.jsonc holding `{}` on its first
// start: the $schema line with the closing brace after its comma. Install
// refused it as not laid out to add to. And a commented `"mcp": {}` came back
// from the round trip split over two lines.
func TestOpencodeJSONCAsOpencodeWritesIt(t *testing.T) {
	for _, seed := range []string{
		"{\n  \"$schema\": \"https://opencode.ai/config.json\",}",
		"{\n  \"$schema\": \"https://opencode.ai/config.json\",}\n",
		"{\n  // mine\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {}\n}\n",
	} {
		dir := opencodeV1(t)
		cfg := filepath.Join(dir, "opencode.jsonc")
		if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "install", "opencode-auto", "--no-index"); err != nil {
			t.Errorf("%q: install refused: %v\n%s", seed, err, out)
			continue
		}
		b, _ := os.ReadFile(cfg)
		var root map[string]any
		if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
			t.Fatalf("%q: install wrote a config that does not parse: %v\n%s", seed, err, b)
		}
		if mcp, _ := root["mcp"].(map[string]any); mcp["deja"] == nil {
			t.Errorf("%q: no deja under mcp:\n%s", seed, b)
		}
		if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
			t.Fatalf("%q: uninstall: %v\n%s", seed, err, out)
		}
		if b, _ := os.ReadFile(cfg); string(b) != seed {
			t.Errorf("%q: uninstall left %q", seed, b)
		}
	}
}

// OpenClaw's openclaw.json goes through the same text writer once it holds a
// comment. Uninstall split the reader's one-line plugins.entries and
// hooks.internal.entries across lines (#5257).
func TestOpenClawAutoRoundTripKeepsTheReadersOneLineBlocks(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "// user settings\n{\n  // top comment\n" +
		"  \"agents\": {\"defaults\": {\"model\": \"anthropic/x\"}},\n" +
		"  \"plugins\": {\"allow\": [\"my-plugin\"], \"entries\": {\"my-plugin\": {\"enabled\": true}}, \"load\": {\"paths\": [\"/opt/my-plugin\"]}},\n" +
		"  \"hooks\": {\"internal\": {\"enabled\": true, \"entries\": {\"session-memory\": {\"enabled\": true}}}},\n" +
		"  \"mcp\": {\"servers\": {\"zz-mine\": {\"command\": \"my-server\"}}}\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "openclaw-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := captureRun(t, "uninstall", "openclaw-auto"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if got := readFile(t, path); got != seed {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, seed)
	}
}

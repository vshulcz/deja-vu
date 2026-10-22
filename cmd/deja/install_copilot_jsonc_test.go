package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Copilot CLI reads settings.json as JSONC: a `//` line and trailing commas
// load (1.0.98, `copilot config model`). copilot-auto refused such a file
// after it had written the MCP entry and the hook file, so the status line
// and the guidance skill were never written and the run exited 1.
func TestCopilotAutoTakesACommentedSettingsJSON(t *testing.T) {
	home := copilotTestHome(t)
	settings := filepath.Join(home, ".copilot", "settings.json")
	before := "{\n  // my copilot settings\n  \"model\": \"gpt-5\",\n  \"theme\": \"dark\",\n}\n"
	if err := os.WriteFile(settings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatalf("install refused a settings.json Copilot reads: %v\n%s", err, out)
	}
	got := readFile(t, settings)
	if !strings.Contains(got, "// my copilot settings") || !strings.Contains(got, `"theme": "dark",`) {
		t.Errorf("install lost the reader's comment or layout:\n%s", got)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(got)), &root); err != nil {
		t.Fatalf("settings.json no longer reads: %v\n%s", err, got)
	}
	line, _ := root["statusLine"].(map[string]any)
	if cmd, _ := line["command"].(string); !strings.HasSuffix(cmd, " statusline") {
		t.Errorf("no status line was written:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".copilot", "skills", "deja-history", "SKILL.md")); err != nil {
		t.Errorf("the guidance skill was not written: %v", err)
	}
	if out, err := captureRun(t, "uninstall", "copilot-auto"); err != nil {
		t.Fatalf("uninstall refused: %v\n%s", err, out)
	}
	if got := readFile(t, settings); got != before {
		t.Errorf("uninstall did not give settings.json back:\n%s\nwant:\n%s", got, before)
	}
}

// The status line was written while the file was plain JSON, and the reader
// commented it afterwards. Uninstall refused the file and removed deja-hook
// anyway, leaving Copilot a status line that runs a command which is gone.
func TestCopilotUninstallTakesTheStatusLineOutOfACommentedSettingsJSON(t *testing.T) {
	home := copilotTestHome(t)
	settings := filepath.Join(home, ".copilot", "settings.json")
	if err := os.WriteFile(settings, []byte("{\n  \"model\": \"gpt-5\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	commented := strings.Replace(readFile(t, settings), "{\n", "{\n  // pinned model\n", 1)
	if !strings.Contains(commented, "statusLine") {
		t.Fatalf("install wrote no status line to start from:\n%s", commented)
	}
	if err := os.WriteFile(settings, []byte(commented), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "uninstall", "copilot-auto"); err != nil {
		t.Fatalf("uninstall refused: %v\n%s", err, out)
	}
	if got, want := readFile(t, settings), "{\n  // pinned model\n  \"model\": \"gpt-5\"\n}\n"; got != want {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, want)
	}
}

// An older install's hook entries in a commented settings.json are deja's to
// clear, and the install that clears them used to refuse the whole target.
func TestCopilotAutoClearsAnOlderHookFromACommentedSettingsJSON(t *testing.T) {
	home := copilotTestHome(t)
	settings := filepath.Join(home, ".copilot", "settings.json")
	deja := hookExeInConfigs("/usr/local/bin/deja")
	before := "{\n  // my copilot settings\n  \"model\": \"gpt-5\",\n  \"hooks\": {\n" +
		"    \"sessionStart\": [{\"type\": \"command\", \"bash\": \"/usr/bin/theirs start\", \"timeoutSec\": 5}, {\"type\": \"command\", \"bash\": \"" + deja + " hook-context --copilot\", \"timeoutSec\": 10}],\n" +
		"    // deja's, from before the hook file\n" +
		"    \"postToolUse\": [{\"type\": \"command\", \"bash\": \"" + deja + " hook-tool-after --copilot\", \"timeoutSec\": 10}]\n" +
		"  }\n}\n"
	if err := os.WriteFile(settings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatalf("install refused: %v\n%s", err, out)
	}
	got := readFile(t, settings)
	if strings.Contains(got, "hook-context") || strings.Contains(got, "hook-tool-after") {
		t.Errorf("deja's old entries are still in settings.json:\n%s", got)
	}
	if !strings.Contains(got, "/usr/bin/theirs start") || !strings.Contains(got, "// my copilot settings") {
		t.Errorf("install took what was not deja's:\n%s", got)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(got)), &root); err != nil {
		t.Fatalf("settings.json no longer reads: %v\n%s", err, got)
	}
	if cfg, _ := readCopilotHookFile(t); len(cfg.Hooks["sessionStart"]) != 1 {
		t.Errorf("the hook file does not carry the digest")
	}
}

// The same writer serves Qwen's ui.statusLine, one key deeper: a ui block the
// status line was the only thing in goes with it, as it does in plain JSON.
func TestAJSONCStatusLineOneKeyDeepRoundTrips(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	before := "{\n  // mine\n  \"theme\": \"dark\",\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"type": "command", "command": "/opt/deja/deja-hook statusline", "refreshInterval": 1}
	if _, err := installJSONStatusline(path, []string{"ui", "statusLine"}, entry, false); err != nil {
		t.Fatalf("install refused: %v", err)
	}
	got := readFile(t, path)
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(got)), &root); err != nil {
		t.Fatalf("the file no longer reads: %v\n%s", err, got)
	}
	if ui, _ := root["ui"].(map[string]any); ui == nil || ui["statusLine"] == nil {
		t.Fatalf("no ui.statusLine:\n%s", got)
	}
	if _, err := installJSONStatusline(path, []string{"ui", "statusLine"}, entry, true); err != nil {
		t.Fatalf("uninstall refused: %v", err)
	}
	if got := readFile(t, path); got != before {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, before)
	}
}

// Shapes a commented settings file comes in, each installed and taken out
// again: the round trip gives the bytes back, and the status line written in
// between is the one key of that name.
func TestAJSONCStatusLineRoundTripsEveryShape(t *testing.T) {
	hermeticEnv(t)
	entry := map[string]any{"type": "command", "command": "/opt/deja/deja-hook statusline", "refreshInterval": 1}
	for _, tc := range []struct {
		name string
		keys []string
		text string
		want string // after uninstall, when it is not text
	}{
		{"comment only above an empty object", []string{"statusLine"}, "// head\n{}\n", ""},
		// The null is replaced, and uninstall takes deja's line out whole.
		{"null under a comment", []string{"statusLine"}, "{\n  // c\n  \"statusLine\": null\n}\n", "{\n  // c\n}\n"},
		// An empty ui goes with the line, as it does in plain JSON, and
		// leaves no comma behind.
		{"nested, ui the last key", []string{"ui", "statusLine"}, "{ // c\n  \"model\": \"x\",\n  \"ui\": {}\n}\n", "{ // c\n  \"model\": \"x\"\n}\n"},
		{"comment between the last two keys", []string{"statusLine"}, "{\n  \"model\": \"x\",\n  // note\n  \"theme\": \"dark\"\n}\n", ""},
		{"block comment before the null", []string{"statusLine"}, "{\n  \"statusLine\": /* x */ null,\n  \"a\": 1\n}\n", "{\n  \"a\": 1\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(tc.text), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installJSONStatusline(path, tc.keys, entry, false); err != nil {
				t.Fatalf("install refused: %v", err)
			}
			got := readFile(t, path)
			if n := strings.Count(got, `"statusLine"`); n != 1 {
				t.Errorf("%d statusLine keys after install:\n%s", n, got)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
				t.Errorf("not JSON once comments are out: %v\n%s", err, got)
			}
			if _, err := installJSONStatusline(path, tc.keys, entry, true); err != nil {
				t.Fatalf("uninstall refused: %v", err)
			}
			want := tc.want
			if want == "" {
				want = tc.text
			}
			if got := readFile(t, path); got != want {
				t.Errorf("uninstall left:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// A comment in front of an event deja takes out stays, and so do the
// reader's characters in the list it shares with deja.
func TestClearingCopilotHooksKeepsTheReadersComment(t *testing.T) {
	deja := "/opt/deja/deja-hook hook-context --copilot"
	text := "{\n  \"hooks\": {\n    \"a\": [{\"bash\": \"a && b\"}, {\"bash\": \"" + deja + "\"}],\n    /* y */ \"sessionStart\": [{\"bash\": \"" + deja + "\"}],\n    \"preToolUse\": /* z */ [{\"bash\": \"" + deja + "\"}]\n  }\n}\n"
	var was map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(text)), &was); err != nil {
		t.Fatal(err)
	}
	root := map[string]any{"hooks": map[string]any{"a": []any{map[string]any{"bash": "a && b"}}}}
	got, err := dropCopilotHooksJSONC(text, typedOrFatal[map[string]any](t, was["hooks"]), root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/* y */") || strings.Contains(got, "preToolUse") || !strings.Contains(got, "a && b") || strings.Contains(got, deja) {
		t.Errorf("got:\n%s", got)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(got)), &back); err != nil {
		t.Errorf("not JSON: %v\n%s", err, got)
	}
}

// A line comment that ends the line in front of the key taken out keeps its
// newline: without it the closing brace landed inside the comment.
func TestTakingAKeyOutAfterALineCommentKeepsTheBrace(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"{\n  \"tools\": {\"a\": 1, // keep\n    \"deja\": true}\n}", "{\n  \"tools\": {\"a\": 1 // keep\n}\n}"},
		{"{\n  \"tools\": {\"a\": 1, // src/**/\n    \"deja\": true}\n}", "{\n  \"tools\": {\"a\": 1 // src/**/\n}\n}"},
	} {
		got, err := jsoncRemoveKey(tc.text, "tools", "deja", 1)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
	text := "{\"x\": 1, // keep\n  \"mcpServers\": {\"deja\": {\"command\": \"d\"}}}"
	got, err := jsoncSetEntry(text, "mcpServers", "deja", "", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
		t.Errorf("not JSON: %v\n%s", err, got)
	}
}

// A comment between an entry and its trailing comma stays when the entry is
// taken out, and the comma after the comment goes so the object still parses.
func TestTakingAKeyOutWithCommentBeforeCommaLeavesValidJSON(t *testing.T) {
	cases := []struct {
		name    string
		comment string
		text    string
	}{
		{
			name:    "block comment between entry and comma",
			comment: "/* deja's */",
			text:    "{\n  \"mcpServers\": {\n    \"deja\": {\"args\": [\"mcp\"], \"command\": \"/usr/local/bin/deja\"} /* deja's */,\n    \"other\": {\"command\": \"x\"}\n  }\n}",
		},
		{
			name:    "line comment between entry and comma",
			comment: "// deja's",
			text:    "{\n  \"mcpServers\": {\n    \"deja\": {\"args\": [\"mcp\"], \"command\": \"/usr/local/bin/deja\"} // deja's\n,\n    \"other\": {\"command\": \"x\"}\n  }\n}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsoncSetEntry(tc.text, "mcpServers", "deja", "", true, 0)
			if err != nil {
				t.Fatalf("jsoncSetEntry failed: %v", err)
			}
			if !strings.Contains(got, tc.comment) {
				t.Errorf("comment %q was lost:\n%s", tc.comment, got)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
				t.Fatalf("not valid JSONC after removing entry: %v\n%s", err, got)
			}
			servers, ok := root["mcpServers"].(map[string]any)
			if !ok {
				t.Fatalf("mcpServers is missing or not an object: %v", root)
			}
			if _, hasDeja := servers["deja"]; hasDeja {
				t.Errorf("deja was not removed")
			}
			if _, hasOther := servers["other"]; !hasOther {
				t.Errorf("other was lost")
			}
		})
	}
}

// A null ui in a commented file is refused rather than written beside.
func TestAJSONCStatusLineUnderANullParentIsRefused(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	before := "{\n  // c\n  \"ui\": null\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"type": "command", "command": "/opt/deja/deja-hook statusline"}
	if _, err := installJSONStatusline(path, []string{"ui", "statusLine"}, entry, false); err == nil {
		t.Error("a status line went in beside a null ui")
	}
	if got := readFile(t, path); got != before {
		t.Errorf("the file was touched:\n%s", got)
	}
}

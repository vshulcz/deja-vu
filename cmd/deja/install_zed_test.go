package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Zed ships settings.json with a comment header and tolerates trailing commas,
// and it is the file a person edits by hand. Decoding it and writing the result
// back — what the other JSON installers do to a config file of their own —
// fails on the first `//` and would delete every comment if it did not.
const zedRealSettings = `// Zed settings
//
// For information on how to configure Zed, see the Zed
// documentation: https://zed.dev/docs/configuring-zed
{
  "git_panel": {
    "tree_view": true
  },
  "ui_font_size": 16,
  "theme": {
    "mode": "system",
    "light": "One Light",
    "dark": "One Dark",
  },
}
`

func zedSettingsFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// zedDejaEntry decodes the installed entry after stripping the comments Zed
// allows, so the test reads the file the way Zed does.
func zedDejaEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(zedStripForTest(b), &root); err != nil {
		t.Fatalf("the file Zed would read does not parse: %v\n%s", err, b)
	}
	servers, ok := root["context_servers"].(map[string]any)
	if !ok {
		t.Fatalf("no context_servers in\n%s", b)
	}
	entry, ok := servers["deja"].(map[string]any)
	if !ok {
		return nil
	}
	return entry
}

// zedStripForTest is the comment and trailing-comma tolerance Zed has, so the
// assertions above can use encoding/json.
func zedStripForTest(b []byte) []byte {
	var out []byte
	inString, escaped := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		default:
			out = append(out, c)
		}
	}
	// Trailing commas.
	var clean []byte
	for i := 0; i < len(out); i++ {
		if out[i] == ',' {
			j := i + 1
			for j < len(out) && (out[j] == ' ' || out[j] == '\n' || out[j] == '\t' || out[j] == '\r') {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				continue
			}
		}
		clean = append(clean, out[i])
	}
	return clean
}

func TestInstallZedKeepsTheCommentsSomeoneWrote(t *testing.T) {
	path := zedSettingsFile(t, zedRealSettings)

	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"// Zed settings",
		"// documentation: https://zed.dev/docs/configuring-zed",
		`"dark": "One Dark",`,
	} {
		if !strings.Contains(string(got), line) {
			t.Errorf("the file lost %q:\n%s", line, got)
		}
	}
	// And the settings themselves are still there, unreordered.
	if !strings.Contains(string(got), `"ui_font_size": 16`) {
		t.Errorf("a setting was dropped:\n%s", got)
	}
	entry := zedDejaEntry(t, path)
	if entry == nil {
		t.Fatalf("deja was not installed:\n%s", got)
	}
	if entry["command"] == "" {
		t.Errorf("the entry names no command: %v", entry)
	}
}

func TestInstallZedIsIdempotentAndReversible(t *testing.T) {
	path := zedSettingsFile(t, zedRealSettings)

	first, err := installZedMCP(path, "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	afterFirst, _ := os.ReadFile(path)
	second, err := installZedMCP(path, "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	afterSecond, _ := os.ReadFile(path)

	if first.Action == "unchanged" {
		t.Errorf("the first install changed nothing")
	}
	if second.Action != "unchanged" {
		t.Errorf("a second install rewrote the file: %s", second.Action)
	}
	if string(afterFirst) != string(afterSecond) {
		t.Errorf("installing twice is not the same as once:\n%s\n---\n%s", afterFirst, afterSecond)
	}

	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	back, _ := os.ReadFile(path)
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived the uninstall:\n%s", back)
	}
	for _, line := range []string{"// Zed settings", `"ui_font_size": 16`} {
		if !strings.Contains(string(back), line) {
			t.Errorf("uninstall took %q with it:\n%s", line, back)
		}
	}
}

// A settings file that already has other servers keeps them, and deja joins
// them rather than replacing the block.
func TestInstallZedJoinsExistingServers(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    "some-other": {
      "command": "other",
      "args": ["serve"]
    }
  },
  "ui_font_size": 16
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"some-other"`) {
		t.Errorf("another server was dropped:\n%s", got)
	}
	if zedDejaEntry(t, path) == nil {
		t.Errorf("deja was not added beside it:\n%s", got)
	}
	// Uninstalling takes ours and leaves theirs.
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if !strings.Contains(string(back), `"some-other"`) {
		t.Errorf("uninstall took another server with it:\n%s", back)
	}
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived:\n%s", back)
	}
}

// No settings file at all is the first-run case: write one Zed can read.
func TestInstallZedWritesAFileWhenThereIsNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")

	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if zedDejaEntry(t, path) == nil {
		b, _ := os.ReadFile(path)
		t.Fatalf("nothing installed:\n%s", b)
	}
}

// Uninstalling from a machine that never had deja must not create a file, nor
// add an empty block to one (#676).
func TestUninstallZedLeavesAnUntouchedMachineAlone(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "settings.json")
	if _, err := installZedMCP(missing, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("uninstall created a settings file: %v", err)
	}

	path := zedSettingsFile(t, zedRealSettings)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != zedRealSettings {
		t.Errorf("uninstall rewrote a file that never mentioned deja:\n%s", got)
	}
}

// A "deja" key belonging to some other setting is not ours to touch.
func TestInstallZedDoesNotMistakeANestedKeyForOurs(t *testing.T) {
	path := zedSettingsFile(t, `{
  "lsp": {
    "deja": {
      "binary": {
        "path": "/somewhere/else"
      }
    }
  }
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "/somewhere/else") {
		t.Errorf("someone else's deja settings were overwritten:\n%s", got)
	}
	entry := zedDejaEntry(t, path)
	if entry == nil {
		t.Fatalf("the server was not installed:\n%s", got)
	}
	if entry["command"] == "/somewhere/else" {
		t.Errorf("the wrong block was edited: %v", entry)
	}
}

// And a "deja" key inside another server's own configuration is not ours
// either: the search has to stay at the depth it is looking in.
func TestInstallZedDoesNotMistakeAKeyInsideAnotherServer(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    "some-other": {
      "command": "other",
      "env": {
        "deja": {
          "path": "/nope"
        }
      }
    }
  }
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "/nope") {
		t.Errorf("another server's own config was overwritten:\n%s", got)
	}
	entry := zedDejaEntry(t, path)
	if entry == nil {
		t.Fatalf("the server was not installed beside it:\n%s", got)
	}
	if entry["path"] != nil {
		t.Errorf("the nested block was edited instead of ours: %v", entry)
	}
}

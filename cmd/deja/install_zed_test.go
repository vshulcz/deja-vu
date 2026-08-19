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
	// No block at all is a legitimate answer: uninstalling the only server
	// takes the block with it.
	servers, ok := root["context_servers"].(map[string]any)
	if !ok {
		return nil
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
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && (b[i] != '*' || b[i+1] != '/') {
				i++
			}
			i++
			out = append(out, ' ')
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
	if string(back) != zedRealSettings {
		t.Errorf("install then uninstall did not put the file back:\n--- want ---\n%s--- got ---\n%s", zedRealSettings, back)
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

// JSONC has two comment shapes and a settings file may use either anywhere.
// A block comment sitting between a key and its value, or holding a brace,
// used to be read as content: the object end came out wrong and the edit
// landed in the middle of someone's settings.
func TestInstallZedSurvivesBlockComments(t *testing.T) {
	path := zedSettingsFile(t, `/* Zed settings
   { not an object } */
{
  "context_servers": /* which servers the agent may call */ {
    "some-other": {
      /* the wrapper opens a brace here { and never closes it */
      "command": "other"
    }
  },
  "ui_font_size": 16
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, _ := os.ReadFile(path)
	for _, keep := range []string{
		"/* Zed settings",
		"/* which servers the agent may call */",
		"/* the wrapper opens a brace here { and never closes it */",
		`"ui_font_size": 16`,
		`"some-other"`,
	} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("the file lost %q:\n%s", keep, got)
		}
	}
	if zedDejaEntry(t, path) == nil {
		t.Fatalf("deja was not installed:\n%s", got)
	}
	// One block, not two. A scanner that loses its place inside a comment
	// reports no block at all and inserts a second one; the entry is then
	// readable and the settings are still wrong.
	if n := strings.Count(string(got), `"context_servers"`); n != 1 {
		t.Errorf("the file has %d context_servers blocks:\n%s", n, got)
	}

	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived the uninstall:\n%s", back)
	}
	if !strings.Contains(string(back), `"some-other"`) {
		t.Errorf("uninstall took the other server with it:\n%s", back)
	}
}

// Someone annotates the entry deja installed, and the note holds a brace that
// never closes. Replacing or removing that entry needs its end, and a scanner
// that counts braces inside comments finds the wrong one — which is how an
// uninstall takes the settings after it along too.
func TestInstallZedFindsTheEntryEndPastAComment(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    "deja": {
      /* installed by deja — the wrapper opens a brace { on purpose */
      "command": "/old/path/deja",
      "args": ["mcp"]
    },
    "some-other": {
      "command": "other"
    }
  },
  "ui_font_size": 16
}
`)
	// Replacing: the old path goes, everything after the entry stays.
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, _ := os.ReadFile(path)
	entry := zedDejaEntry(t, path)
	if entry == nil {
		t.Fatalf("the entry was lost:\n%s", got)
	}
	// Windows wraps the executable in cmd /c, so what the entry holds is
	// whatever mcpCommandArgs makes of it, not the path as written.
	wantCommand, _ := mcpCommandArgs("/usr/local/bin/deja")
	if entry["command"] != wantCommand {
		t.Errorf("the entry was not updated: %v", entry)
	}
	if strings.Contains(string(got), "/old/path/deja") {
		t.Errorf("the old command survived beside the new one:\n%s", got)
	}
	for _, keep := range []string{`"some-other"`, `"ui_font_size": 16`} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("replacing the entry took %q with it:\n%s", keep, got)
		}
	}

	// Removing: the same end, and the same everything-after.
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived:\n%s", back)
	}
	for _, keep := range []string{`"some-other"`, `"command": "other"`, `"ui_font_size": 16`} {
		if !strings.Contains(string(back), keep) {
			t.Errorf("uninstall took %q with it:\n%s", keep, back)
		}
	}
}

// install then uninstall has to leave the file it was handed, byte for byte.
// Leaving an empty "context_servers": {} behind is a change the reader did not
// ask for and did not make.
func TestInstallZedLeavesNoEmptyBlockBehind(t *testing.T) {
	before := `// Zed settings
{
  "theme": "One Dark",
  "buffer_font_size": 15
}
`
	path := zedSettingsFile(t, before)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if zedDejaEntry(t, path) == nil {
		t.Fatal("deja was not installed")
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if string(back) != before {
		t.Errorf("uninstall did not put the file back:\n--- want ---\n%s--- got ---\n%s", before, back)
	}
}

// But a block that still holds something the reader put there stays, even when
// deja was the only server in it.
func TestInstallZedKeepsABlockThatStillHoldsSomething(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    // the agent talks to these
    "deja": {
      "command": "/old/deja",
      "args": ["mcp"]
    }
  },
  "theme": "One Dark"
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	for _, keep := range []string{`"context_servers"`, "// the agent talks to these", `"theme": "One Dark"`} {
		if !strings.Contains(string(back), keep) {
			t.Errorf("uninstall took %q with it:\n%s", keep, back)
		}
	}
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived:\n%s", back)
	}
}

// Wiring deja into Zed and then asking doctor whether it is wired have to give
// the same answer. Doctor's generic probe falls back to looking for "deja"
// anywhere in a file it cannot parse, and a settings file is full of comments,
// so Zed gets the installer's own scanner.
func TestDoctorReportsZedWiring(t *testing.T) {
	path := zedSettingsFile(t, `// Zed settings
{
  // deja is not installed here — this line only says the word
  "context_servers": {
    "some-other": {
      "command": "other"
    }
  },
  "ui_font_size": 16
}
`)
	if doctorZedWired(path) {
		t.Error("doctor calls Zed wired with no deja entry")
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if !doctorZedWired(path) {
		b, _ := os.ReadFile(path)
		t.Errorf("doctor does not see the entry install just wrote:\n%s", b)
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if doctorZedWired(path) {
		b, _ := os.ReadFile(path)
		t.Errorf("doctor still calls it wired after uninstall:\n%s", b)
	}
	if doctorZedWired(filepath.Join(t.TempDir(), "nothing.json")) {
		t.Error("doctor calls a machine with no settings file wired")
	}
}

// The zed line has to be in doctor's list at all: an install target whose
// result doctor never reports is wiring nobody can check.
func TestDoctorListsZed(t *testing.T) {
	var found bool
	for _, c := range doctorMCPConfigs() {
		if c.name == "zed" {
			found = true
		}
	}
	if !found {
		t.Error("doctor lists no zed row, so `deja install zed` cannot be verified")
	}
}

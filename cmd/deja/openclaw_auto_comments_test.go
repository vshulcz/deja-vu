package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// openclaw-auto writes into the same config the MCP entry does, and it still
// went through the parsed path only — so a `//` line refused the target, on the
// way in and on the way out, which is how a reader ends up with the hook entry
// stuck in the file (#2811).
func TestOpenClawAutoTakesACommentedConfig(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // the ordering here is deliberate\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("a commented config was refused: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "// the ordering here is deliberate") {
		t.Errorf("the comment was dropped:\n%s", b)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := mapAt(hooks, "internal")
	entries, _ := mapAt(internal, "entries")
	if entries[openclawHookName] == nil {
		t.Fatalf("the hook entry was not written:\n%s", b)
	}
	// Without the switch beside it the pack is discovered and never invoked,
	// which is the state the parsed path is careful to avoid.
	if on, _ := internal["enabled"].(bool); !on {
		t.Errorf("the entry went in without the switch that runs it:\n%s", b)
	}

	if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall refused the commented config: %v", err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), openclawHookName) {
		t.Errorf("the hook entry stayed behind:\n%s", b)
	}
	if string(b) != before {
		t.Errorf("the round trip changed the file:\nwant %q\ngot  %q", before, b)
	}
}

// The reader already has the block, their own switch and a hook of their own.
// Deja's entry comes out and nothing else moves: the switch is theirs, and
// deleting it left their hook wired and turned off (#2811).
func TestOpenClawAutoLeavesTheReadersWiringAlone(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine, and I mean it\n  \"hooks\": {\n    \"internal\": {\n      \"entries\": {\n        \"mine\": { \"enabled\": true }\n      },\n      \"enabled\": true\n    }\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("a commented config was refused: %v", err)
	}
	if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall refused the commented config: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("the round trip changed the reader's file:\nwant %q\ngot  %q", before, b)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
}

// The switch written last in its block, with a key in front of it: taking it
// out has to take the comma in front too, or what is left is `{…,}` and every
// later run refuses the file — including across a comment between the two,
// which a backward walk stops at.
func TestOpenClawAutoRemovesASwitchWrittenLast(t *testing.T) {
	for _, before := range []string{
		"{\n  // reader's own block\n  \"hooks\": {\n    \"internal\": {}\n  }\n}\n",
		"{\n  // reader's own block\n  \"hooks\": {\n    \"internal\": {\n      \"timeoutMs\": 500\n    }\n  }\n}\n",
		"{\n  // reader's own block\n  \"hooks\": {\n    \"internal\": {\n      \"timeoutMs\": 500,\n      // the internal hook system\n      \"other\": true\n    }\n  }\n}\n",
		"{\n  \"hooks\": {\n    \"internal\": {\n      \"timeoutMs\": 500, /* keep */ \"other\": true\n    }\n  }\n}\n",
	} {
		t.Run("", func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
				t.Fatalf("a commented config was refused: %v", err)
			}
			if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
				t.Fatalf("uninstall refused the commented config: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
				t.Fatalf("the file no longer parses: %v\n%s", err, b)
			}
			if strings.Contains(string(b), `"enabled"`) {
				t.Errorf("the switch deja turned on stayed behind:\n%s", b)
			}
			if string(b) != before {
				t.Errorf("the round trip changed the file:\nwant %q\ngot  %q", before, b)
			}
		})
	}
}

// A switch the reader set themselves is theirs: deja overwrites it while it is
// installed and leaves it behind on the way out, rather than deleting a setting
// it never wrote.
func TestOpenClawAutoKeepsASwitchTheReaderSet(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"hooks\": {\n    \"internal\": {\n      \"enabled\": false\n    }\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("a commented config was refused: %v", err)
	}
	if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall refused the commented config: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"enabled"`) {
		t.Errorf("the reader's own switch was deleted:\n%s", b)
	}
	// And it is theirs, value and all: an install that switched internal hooks
	// on for someone who had switched them off is not something an uninstall
	// should leave behind.
	if !strings.Contains(string(b), "\"enabled\": false") {
		t.Errorf("the reader had it off and it came back on:\n%s", b)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
}

// A string value equal to a block's name is not that block: the writer read it
// as one and put a second block in beside the reader's, where the decoder takes
// the reader's and deja is silently unwired.
func TestOpenClawAutoIsNotFooledByAValueNamedLikeAKey(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // a value can be spelled like a key\n  \"hooks\": {\n    \"note\": \"internal\",\n    \"internal\": {\n      \"entries\": {\n        \"mine\": { \"enabled\": true }\n      }\n    }\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("install refused the config: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), `"internal"`); n != 2 {
		// The key itself and the reader's string value: one of each, and no
		// second block.
		t.Errorf("%d occurrences of the key, so a second block went in beside it:\n%s", n, b)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := mapAt(hooks, "internal")
	entries, _ := mapAt(internal, "entries")
	if entries[openclawHookName] == nil {
		t.Errorf("the entry did not reach the reader's own block:\n%s", b)
	}
}

// A level holding something that is not an object is refused rather than
// written beside: a second key of the same name loses the decode to the
// reader's value, and deja reports an install that did nothing.
func TestOpenClawAutoRefusesALevelItCannotEdit(t *testing.T) {
	for _, before := range []string{
		"{\n  // not an object\n  \"hooks\": { \"internal\": \"off\" }\n}\n",
		"{\n  // not an object\n  \"hooks\": { \"internal\": { \"entries\": [] } }\n}\n",
	} {
		t.Run("", func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installOpenClawAuto("/bin/deja", false); err == nil {
				t.Error("a level deja cannot edit was written into anyway")
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// The MCP and plugin entries are written before the hook one, so
			// the file does change — what must not is the block deja refused.
			if strings.Count(string(b), `"internal"`) != 1 {
				t.Errorf("a second key was written beside the one deja refused:\n%s", b)
			}
			if !strings.Contains(string(b), "// not an object") {
				t.Errorf("the comment was dropped:\n%s", b)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
				t.Fatalf("the file no longer parses: %v\n%s", err, b)
			}
		})
	}
}

// The population this writer is for: someone who hand-edits an annotated
// config. They move deja's switch to the end of the block and put a note above
// it, and the uninstall has to take the comma in front of it with it — the one
// after it is not there to take (#2811).
func TestOpenClawAutoRemovesASwitchTheReaderMoved(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\n  // mine\n  \"hooks\": {\n    \"internal\": {}\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("a commented config was refused: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"enabled"`) {
		t.Fatalf("the switch was not written, so there is nothing to take out:\n%s", b)
	}
	// The reader tidies: the switch goes last, with a line about it. Written
	// out rather than edited, so the fixture is exactly the shape being tested.
	var entry string
	if i := strings.Index(string(b), `"deja-recall"`); i >= 0 {
		if j := strings.Index(string(b)[i:], "}"); j >= 0 {
			entry = string(b)[i : i+j+1]
		}
	}
	if entry == "" {
		t.Fatalf("could not find the entry deja wrote:\n%s", b)
	}
	// A key of the reader's own between the entries block and the switch, or
	// the entries block takes its own comma with it when it goes and the walk
	// has nothing to find.
	moved := "{\n  // mine\n  \"hooks\": {\n    \"internal\": {\n      \"entries\": {\n        " +
		entry + "\n      },\n      \"timeoutMs\": 500,\n      // deja turned this on\n      \"enabled\": true\n    }\n  }\n}\n"
	if err := os.WriteFile(path, []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall refused the config: %v", err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
	if strings.Contains(string(b), `"enabled"`) {
		t.Errorf("the switch deja turned on stayed behind:\n%s", b)
	}
	if !strings.Contains(string(b), "// mine") {
		t.Errorf("the reader's own comment was dropped:\n%s", b)
	}
	if !strings.Contains(string(b), `"timeoutMs": 500`) {
		t.Errorf("the reader's own key went with it:\n%s", b)
	}
}

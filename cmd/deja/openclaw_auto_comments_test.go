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

// The switch written last in its block: taking it out has to take the comma in
// front of it, or what is left is `{…,}` and every later run refuses the file.
func TestOpenClawAutoRemovesASwitchWrittenLast(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // reader's own block\n  \"hooks\": {\n    \"internal\": {}\n  }\n}\n"
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
	if strings.Contains(string(b), "enabled") {
		t.Errorf("the switch deja turned on stayed behind:\n%s", b)
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

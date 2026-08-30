package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// gemini-auto wrote its MCP entry through the text path and then refused the
// same file with a strict decode, so the target reported itself refused with
// half its wiring written and a .bak beside it — worse than the plain refusal
// it replaced (#2744).
func TestGeminiAutoTakesACommentedSettingsFile(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // my theme\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatalf("a comment refused the target: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "// my theme") || !strings.Contains(got, `"theme": "dark"`) {
		t.Errorf("the reader's file did not survive:\n%s", got)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
		t.Fatalf("what deja wrote is not JSON: %v\n%s", err, got)
	}
	if _, ok := root["mcpServers"].(map[string]any)["deja"]; !ok {
		t.Errorf("the server was not written:\n%s", got)
	}
	cfg, _ := root["hooksConfig"].(map[string]any)
	if enabled, _ := cfg["enabled"].(bool); !enabled {
		t.Errorf("the hook switch was not turned on:\n%s", got)
	}
}

// The switch on a file that already has it, and on one whose block is written
// at another shape: deja reads what is there rather than writing a second key.
func TestTheGeminiHookSwitchReadsWhatIsThere(t *testing.T) {
	for _, c := range []struct {
		name, before string
		wantWrite    bool
	}{
		{"already on", "{\n  // c\n  \"hooksConfig\": {\"enabled\": true}\n}\n", false},
		{"turned off by hand", "{\n  // c\n  \"hooksConfig\": {\"enabled\": false}\n}\n", true},
		{"a block with other keys", "{\n  // c\n  \"hooksConfig\": {\"timeout\": 5}\n}\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.GeminiHome(), "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.before), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := enableGeminiHooks(); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if !strings.Contains(got, "// c") {
				t.Errorf("the comment was dropped:\n%s", got)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
				t.Fatalf("not JSON after the switch: %v\n%s", err, got)
			}
			cfg, _ := root["hooksConfig"].(map[string]any)
			if enabled, _ := cfg["enabled"].(bool); !enabled {
				t.Errorf("the switch is not on:\n%s", got)
			}
			if !c.wantWrite && got != c.before {
				t.Errorf("a file that was already right was rewritten:\n%s", got)
			}
			if c.name == "a block with other keys" {
				if _, ok := cfg["timeout"]; !ok {
					t.Errorf("the reader's own key in that block was dropped:\n%s", got)
				}
			}
		})
	}
}

// And the hook writers that cannot edit a commented file say so, without
// touching it — and say nothing at all when there was nothing of deja's in it
// to change.
func TestASettingsHookOnACommentedFileIsHonest(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.QwenConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	// Removing a hook that is not there changes nothing, so there is nothing
	// to refuse.
	r, err := installSettingsHook(path, "BeforeAgent", "", 10000, "/opt/deja", true)
	if err != nil {
		t.Fatalf("removing a hook that is not there was refused: %v", err)
	}
	if r.Action != "unchanged" {
		t.Errorf("action is %q, want unchanged", r.Action)
	}

	// Writing one cannot be done without losing the comments, and says so.
	if _, err := installSettingsHook(path, "UserPromptSubmit", "", 60000, "/opt/deja", false); err == nil {
		t.Error("a hook was written into a file deja cannot rewrite")
	} else if !strings.Contains(err.Error(), "carries comments") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("the file was touched anyway:\n%s", b)
	}
}

// The shapes that made the flag writer splice through the middle of a value,
// each one leaving a file no parser reads while install said it worked
// (#2745).
func TestTheFlagWriterRefusesWhatItCannotEdit(t *testing.T) {
	for _, c := range []struct{ name, in string }{
		{"a string value that reads like the key", `{"hooksConfig": {"label": "enabled", "timeout": 5}}`},
		{"a value that is an object", `{"hooksConfig": {"enabled": {"deep": true}}}`},
		{"a value that is a list", `{"hooksConfig": {"enabled": [1, 2]}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := jsoncSetFlag(c.in, "hooksConfig", "enabled", true)
			if err != nil {
				return // refusing is an answer
			}
			var v any
			if e := json.Unmarshal([]byte(stripJSONComments(out)), &v); e != nil {
				t.Errorf("what it wrote is not JSON: %v\n%s", e, out)
			}
		})
	}
}

// A block holding something else gets no second key of its own name: the
// reader's value would win and the switch would read back off (#2745).
func TestTheGeminiSwitchRefusesABlockItCannotEdit(t *testing.T) {
	for _, before := range []string{
		"{\n  // c\n  \"hooksConfig\": null\n}\n",
		"{\n  // c\n  \"hooksConfig\": [1]\n}\n",
		"{\n  // c\n  \"hooksConfig\": {\"enabled\": [1]}\n}\n",
	} {
		hermeticEnv(t)
		path := filepath.Join(sources.GeminiHome(), "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := enableGeminiHooks(); err == nil {
			b, _ := os.ReadFile(path)
			t.Errorf("a block deja cannot edit was accepted:\n%s", b)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != before {
			t.Errorf("the file was written anyway:\n%s", b)
		}
	}
}

// A comment after the value is the reader's, and the only byte the text path
// was still eating.
func TestTheFlagWriterKeepsACommentAfterTheValue(t *testing.T) {
	in := "{\n  \"hooksConfig\": {\n    \"enabled\": false // off on purpose\n  }\n}\n"
	out, err := jsoncSetFlag(in, "hooksConfig", "enabled", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "// off on purpose") {
		t.Errorf("the comment was eaten:\n%s", out)
	}
	if !strings.Contains(out, "\"enabled\": true") {
		t.Errorf("the switch was not turned on:\n%s", out)
	}
}

// And the half-write: the hook half of a target goes first when it shares a
// file with the MCP entry, so a refusal leaves the file as it was (#2745).
func TestQwenAutoDoesNotWriteHalfItsWiringIntoACommentedFile(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.QwenConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "qwen-auto", "--no-index"); err == nil {
		t.Fatal("a hook deja cannot write was reported as installed")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("the file was written before the refusal:\n%s", b)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Errorf("a snapshot was left beside a file that was never changed")
	}
}

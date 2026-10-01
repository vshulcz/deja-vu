package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Installing for Gemini turns on hooksConfig.enabled, a switch that belongs to
// the harness rather than to deja. With another extension running on it,
// uninstall leaves it on — and said nothing, while naming every other thing it
// kept (#2487).
func TestUninstallSaysItLeftGeminiHooksOn(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"GitHub"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = home
	// Another extension with hooks of its own is running on the switch.
	other := filepath.Join(sources.GeminiHome(), "extensions", "other", "hooks", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(`{"hooks":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	res, err := installGeminiAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	cfg, _ := root["hooksConfig"].(map[string]any)
	if enabled, _ := cfg["enabled"].(bool); !enabled {
		t.Fatalf("this test is about the switch staying on; it is off:\n%s", b)
	}
	if !strings.Contains(res.Note, "hooksConfig") {
		t.Errorf("uninstall left a harness-wide switch on and its note does not mention it: %q", res.Note)
	}
}

// Nothing to say when deja never turned it on.
func TestUninstallIsQuietWhenGeminiHooksAreOff(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	_ = home
	res, err := installGeminiAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Note, "hooksConfig") {
		t.Errorf("nothing was left on, but uninstall says %q", res.Note)
	}
}

// deja added the switch to a file that had none, and no extension is left to
// run on it: uninstall takes it out and the file comes back byte for byte
// (#4216). One the reader turned on before installing stays.
func TestUninstallTakesBackTheGeminiSwitchItAdded(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // my settings\n  \"security\": {\n    \"auth\": {\n      \"selectedType\": \"gemini-api-key\"\n    }\n  },\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"true\"\n    }\n  },\n  \"ui\": {\n    \"theme\": \"GitHub\"\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if !geminiHooksEnabled() {
		t.Fatal("install did not turn the switch on")
	}
	if _, err := captureRun(t, "uninstall", "gemini"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("settings.json did not come back as it was:\nwant:\n%s\ngot:\n%s", before, after)
	}

	// The reader's own switch, on before deja ever came.
	own := "{\n  \"hooksConfig\": {\n    \"enabled\": true\n  }\n}\n"
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "gemini"); err != nil {
		t.Fatal(err)
	}
	if !geminiHooksEnabled() {
		t.Fatal("uninstall turned off a switch the reader had on before installing")
	}
}

// Plain JSON, the shape most settings files have: install writes the switch in
// front of mcpServers, uninstall drops mcpServers, and the switch ends up the
// last key. Cutting it must not leave the comma before it behind — Gemini
// rejects the file.
func TestUninstallLeavesValidJSONWhenTheSwitchIsLast(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"ui":{"theme":"GitHub"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "gemini"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("uninstall left settings.json Gemini cannot parse (%v):\n%s", err, b)
	}
	if _, ok := root["hooksConfig"]; ok {
		t.Errorf("the switch deja added is still there:\n%s", b)
	}
}

// Hooks the reader wrote into settings.json, or a linked extension whose
// hooks live at its source, also run on the switch: uninstall leaves it on.
func TestUninstallKeepsTheGeminiSwitchOtherHooksNeed(t *testing.T) {
	for name, seed := range map[string]func(t *testing.T, path string){
		"settings hooks": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{"hooks":{"BeforeTool":[]}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"linked extension": func(t *testing.T, path string) {
			meta := filepath.Join(sources.GeminiHome(), "extensions", "mine", ".gemini-extension-install.json")
			if err := os.MkdirAll(filepath.Dir(meta), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(meta, []byte(`{"source":"/src/mine","type":"link"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.GeminiHome(), "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			seed(t, path)
			if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "uninstall", "gemini"); err != nil {
				t.Fatal(err)
			}
			if !geminiHooksEnabled() {
				t.Fatal("uninstall turned off a switch other hooks still run on")
			}
		})
	}
}

// deja added the switch, the reader then changed it; a reinstall must not
// leave deja believing the object is still its own.
func TestReinstallForgetsASwitchTheReaderChanged(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if !blockWasAdded(path, "hooksConfig") {
		t.Fatal("install did not record the switch it added")
	}
	if err := os.WriteFile(path, []byte(`{"hooksConfig":{"enabled":true,"notifications":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if blockWasAdded(path, "hooksConfig") {
		t.Error("the reader's object is still recorded as deja's")
	}
}

// The cut must leave valid JSON whatever shape the reader gave the file:
// the switch last, first with its comma on the next line, or behind a
// comment.
func TestDisableGeminiHooksKeepsEveryShapeValid(t *testing.T) {
	for _, in := range []string{
		`{"ui":{"theme":"GitHub"},"hooksConfig":{"enabled":true}}`,
		"{\"hooksConfig\":{\"enabled\":true}\n, \"b\":1}",
		`{"hooksConfig":{"enabled":true} /*c*/, "b":1}`,
		"{\"a\":1, // x\n \"hooksConfig\":{\"enabled\":true}\n}",
		`{"hooksConfig":{"enabled":true}}`,
	} {
		hermeticEnv(t)
		path := filepath.Join(sources.GeminiHome(), "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		noteBlockAdded(path, "hooksConfig")
		if err := disableGeminiHooksIfOurs(); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(path)
		var root map[string]any
		if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
			t.Errorf("%q came out as %q, which Gemini cannot parse: %v", in, b, err)
			continue
		}
		if _, ok := root["hooksConfig"]; ok {
			t.Errorf("%q: the switch is still there: %q", in, b)
		}
	}
}

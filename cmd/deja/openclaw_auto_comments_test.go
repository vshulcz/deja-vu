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

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// ~/.claude/settings.json is a file people write by hand — permissions, a
// model, an env block — and the hook writer was the one JSON writer still
// going through MarshalIndent. So an install that added its hooks also
// alphabetised the reader's keys, reindented the file and expanded every block
// they had written on one line (#1665, the shape #2641 and #2704 fixed
// everywhere else).
func TestTheSettingsWriterKeepsTheShapeItFound(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{
    "zeta": 1,
    "model": "opus",
    "permissions": {"allow": ["Bash(git:*)"], "deny": []},
    "env": {"DEJA_RECALL": "safe"}
}
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)

	// The reader's order, not the alphabet.
	zeta, model := strings.Index(got, `"zeta"`), strings.Index(got, `"model"`)
	if zeta < 0 || model < 0 || zeta > model {
		t.Errorf("the reader's top-level keys were reordered:\n%s", got)
	}
	// Their indent.
	if !strings.Contains(got, "\n    \"zeta\"") {
		t.Errorf("a four-space file came back at another indent:\n%s", got)
	}
	// And their blocks, on the line they wrote them.
	for _, line := range []string{
		`"permissions": {"allow": ["Bash(git:*)"], "deny": []},`,
		`"env": {"DEJA_RECALL": "safe"}`,
	} {
		if !strings.Contains(got, line) {
			t.Errorf("a block the install never touched was expanded; expected\n%s\ngot:\n%s", line, got)
		}
	}
	// And it is still the file it was, plus deja's hooks.
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, got)
	}
	if root["model"] != "opus" {
		t.Errorf("a value changed: %v", root["model"])
	}
	if _, ok := root["hooks"]; !ok {
		t.Errorf("the hooks were not written:\n%s", got)
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A hook array the reader wrote on one line goes back on one line when deja
// adds its entry to it (#2704), but it was compacted from the marshalled form,
// so the reader's own entry inside it came back with its keys sorted — and an
// uninstall then had nothing to restore it from (#4167).
func TestInstallAndUninstallGiveBackTheReadersOwnHookEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	mine := `[{"matcher": "Bash", "hooks": [{"type": "command", "command": "~/bin/guard.sh"}]}]`
	orig := `{
  "model": "opus",
  "permissions": {"allow": ["Bash(go test:*)"]},
  "hooks": {
    "PreToolUse": ` + mine + `
  }
}
`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := "/opt/deja/deja-hook hook-tool"
	if _, err := installSettingsHookCmd(path, "PreToolUse", "Bash|Edit", 30, cmd, false); err != nil {
		t.Fatal(err)
	}
	installed, _ := os.ReadFile(path)
	if want := `"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "~/bin/guard.sh"}]}, `; !strings.Contains(string(installed), want) {
		t.Errorf("the install rewrote the reader's own entry; want it kept as\n%s\ngot:\n%s", want, installed)
	}
	if !strings.Contains(string(installed), cmd) {
		t.Fatalf("the install did not add its entry:\n%s", installed)
	}
	if _, err := installSettingsHookCmd(path, "PreToolUse", "Bash|Edit", 30, cmd, true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if string(back) != orig {
		t.Errorf("install then uninstall did not give the file back\nwant:\n%s\ngot:\n%s", orig, back)
	}
}

// An entry the reader wrote on one line inside an array laid out over many
// lines is matched by its value, not its position: deja's entry added ahead of
// it moves it along.
func TestAnInlineArrayEntryThatMovedKeepsItsText(t *testing.T) {
	old := []byte(`{
  "list": [
    {"b": 1, "a": 2}
  ]
}
`)
	next := []byte(`{
  "list": [
    {
      "deja": true
    },
    {
      "a": 2,
      "b": 1
    }
  ]
}
`)
	got := string(keepInlineBlocks(old, next))
	if !strings.Contains(got, `{"b": 1, "a": 2}`) {
		t.Errorf("the reader's entry lost its text when it moved:\n%s", got)
	}
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// install adopts a codex entry that already runs deja under another name rather
// than adding a second one. The note saying so named the key through %q over a
// key tomlMCPHeader had already quoted, so it read "\"deja.old\"", and it was
// set without looking at what the write did, so every later run repeated it
// next to "unchanged".
func TestInstallTOMLAdoptNoteNamesTheKeyAsWrittenAndOnlyWhenSomethingChanged(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	// The command's basename is deja, so the second run recognises the entry it
	// wrote; the test binary is deja.test and would not.
	seed := "[mcp_servers.\"deja.old\"]\ncommand = \"/usr/local/bin/deja\"\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	block := "[mcp_servers.deja]\ncommand = \"/usr/local/bin/deja\"\nargs = [\"mcp\", \"--new\"]\n"

	first, err := installTOML(path, block, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Action == "unchanged" {
		t.Fatalf("the first run changed the file but reported %q", first.Action)
	}
	wantNote := `updated "deja.old", which already ran deja, instead of adding a second entry`
	if first.Note != wantNote {
		t.Errorf("note on the run that adopted the entry:\n got %q\nwant %q", first.Note, wantNote)
	}

	second, err := installTOML(path, block, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Action != "unchanged" {
		t.Fatalf("the second run should change nothing, got %q", second.Action)
	}
	if second.Note != "" {
		t.Errorf("a run that changed nothing still said %q", second.Note)
	}
}

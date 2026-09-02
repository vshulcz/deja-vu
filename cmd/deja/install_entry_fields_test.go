package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func dejaMCPEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if entry == nil {
		t.Fatalf("no deja entry left in %s:\n%s", path, b)
	}
	return entry
}

func writeClaudeMCP(t *testing.T, home string, entry map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude.json")
	b, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"deja": entry}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// entryArgs reads back the args of a decoded entry, which JSON hands over as
// []any.
func entryArgs(entry map[string]any) []string {
	raw, _ := entry["args"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// deja owns the command, the args and the type on its own entry. An env the
// reader added, or a disabled flag they set, is theirs — replacing the whole
// entry threw both away, and the note reported a replacement of the command by
// itself while saying nothing about what actually went (#2479).
func TestInstallKeepsWhatTheReaderPutOnDejasEntry(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := writeClaudeMCP(t, home, map[string]any{
		"command":  "/old/bin/deja",
		"args":     []string{"mcp"},
		"env":      map[string]any{"DEJA_INDEX_DIR": "/data/index.db"},
		"disabled": true,
	})

	res, err := installClaude("/new/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	entry := dejaMCPEntry(t, path)
	wantCommand, wantArgs := mcpCommandArgs("/new/bin/deja")
	if entry["command"] != wantCommand {
		t.Errorf("the command was not repointed: %v", entry["command"])
	}
	// Where the harness needs a shell in front of it, the exe is in the args
	// — asserting the exe as the command is asserting a Unix spelling.
	if got := entryArgs(entry); !slices.Equal(got, wantArgs) {
		t.Errorf("the args are not deja's own: %v, want %v", got, wantArgs)
	}
	env, _ := entry["env"].(map[string]any)
	if env["DEJA_INDEX_DIR"] != "/data/index.db" {
		t.Errorf("the reader's env is gone: %v", entry["env"])
	}
	if entry["disabled"] != true {
		t.Errorf("install switched a disabled server back on: %v", entry["disabled"])
	}
	if res.Note == "" {
		t.Errorf("the command really did change and the entry is disabled, and install said nothing")
	}
}

// The same install run twice, and an entry an older deja wrote: the command is
// the same either way, so there is nothing to report.
func TestInstallIsQuietWhenTheCommandDidNotChange(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	command, args := mcpCommandArgs("/bin/deja")
	writeClaudeMCP(t, home, map[string]any{"command": command, "args": args})

	res, err := installClaude("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Note != "" {
		t.Errorf("nothing the reader would call a replacement happened, but install says %q", res.Note)
	}
}

// Only onto an entry that was deja's. A remote-server shape someone put under
// the deja name is replaced whole: keeping its url beside deja's command would
// leave the client two answers to choose between.
func TestInstallDoesNotMergeIntoAnEntryThatIsNotDejas(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := writeClaudeMCP(t, home, map[string]any{"url": "https://example.invalid/mcp"})

	if _, err := installClaude("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	entry := dejaMCPEntry(t, path)
	if _, ok := entry["url"]; ok {
		t.Errorf("the remote shape survived beside deja's own wiring: %v", entry)
	}
	if want, _ := mcpCommandArgs("/bin/deja"); entry["command"] != want {
		t.Errorf("deja is not wired: %v", entry)
	}
}

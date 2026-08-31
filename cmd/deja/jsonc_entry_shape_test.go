package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The text path built the entry itself instead of taking the one its caller
// would have written, so a commented ~/.claude.json got an entry without the
// `type` every other claude entry carries — two writers for one file, agreeing
// on everything but what they write.
func TestBothWritersPutTheSameEntryInClaudesConfig(t *testing.T) {
	entryFor := func(t *testing.T, before string) map[string]any {
		t.Helper()
		hermeticEnv(t)
		path := sources.ClaudeJSONPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
			t.Fatalf("the config no longer parses: %v\n%s", err, b)
		}
		servers, _ := root["mcpServers"].(map[string]any)
		entry, ok := servers["deja"].(map[string]any)
		if !ok {
			t.Fatalf("no deja entry:\n%s", b)
		}
		return entry
	}

	plain := entryFor(t, "{\n  \"mcpServers\": {}\n}\n")
	commented := entryFor(t, "{\n  // mine\n  \"mcpServers\": {}\n}\n")

	for _, key := range []string{"type", "command", "args"} {
		if !sameEntry(plain[key], commented[key]) {
			t.Errorf("%s: the commented config got %#v, the plain one %#v", key, commented[key], plain[key])
		}
	}
	if commented["type"] != "stdio" {
		t.Errorf("the entry claude reads carries no transport: %#v", commented)
	}
}

// And the writers that do not want a type still do not get one.
func TestTheGenericWriterKeepsItsOwnEntryShape(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\n  // mine\n  \"mcpServers\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"type"`) {
		t.Errorf("cursor's entry gained a transport it does not use:\n%s", b)
	}
}

// The block is written at the top of the object with a comma after it, and a
// config whose only line is a comment has no other key for that comma to
// separate: the file stopped being JSON, and every later run refused the
// target with a message pointing at the reader's own comment. The same shape
// #2740 fixed one level down.
func TestABlockWrittenIntoAnEmptyObjectCarriesNoStrayComma(t *testing.T) {
	for _, before := range []string{
		"{\n  // mine\n}\n",
		"{\n  /* mine */\n}\n",
		"{}\n",
	} {
		t.Run(strings.TrimSpace(before), func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var probe map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &probe); err != nil {
				t.Fatalf("the config no longer parses: %v\n%s", err, b)
			}
			// And the run that comes after it still works, which is what the
			// stray comma took away.
			if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
				t.Fatalf("the second run was refused: %v", err)
			}
		})
	}
}

// The same insert in the flag writer, which puts gemini's hook switch in.
func TestAFlagBlockWrittenIntoAnEmptyObjectCarriesNoStrayComma(t *testing.T) {
	for _, before := range []string{"{\n  // mine\n}\n", "{}\n"} {
		got, err := jsoncSetFlag(before, "hooksConfig", "enabled", true)
		if err != nil {
			t.Fatal(err)
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(stripJSONComments(got)), &probe); err != nil {
			t.Errorf("%q: the config no longer parses: %v\n%s", before, err, got)
		}
	}
}

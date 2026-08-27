package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A config can name a deja binary that is not there — a restored backup, a hand
// edit, a machine where deja moved and one file was fixed by hand. The harness
// then starts a server that cannot run, no memory arrives, and doctor reported
// the wiring as healthy: its only check asks whether the exe in deja's own
// record exists, and that one still did (#2216).
func TestDoctorNamesAConfigPointingAtAMissingBinary(t *testing.T) {
	hermeticEnv(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("claude", exe, false); err != nil {
		t.Fatal(err)
	}
	recordWiring([]string{"claude"}, false)

	// The premise: nothing to report while the binary is where the config says.
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "not there") {
		t.Fatalf("a healthy wiring was reported broken:\n%s", out)
	}

	p := sources.ClaudeJSONPath()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Absolute the way this platform spells absolute: the check stays quiet on
	// a relative path, and "/gone/deja" is one on Windows.
	gone := filepath.Join(t.TempDir(), "gone", "deja")
	marshalled, err := json.Marshal(gone)
	if err != nil {
		t.Fatal(err)
	}
	quoted := string(marshalled[1 : len(marshalled)-1])
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), jsonEscaped(t, exe), quoted)), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err = captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, gone) {
		t.Errorf("doctor does not name the binary the config points at:\n%s", out)
	}
	if !strings.Contains(out, p) {
		t.Errorf("doctor does not name the config that points there:\n%s", out)
	}
}

// Where the check must stay quiet, and where it must speak. A bare command is
// the PATH's business; a directory that happens to be named deja is not a
// binary; and the three config shapes deja writes — JSON, TOML, YAML — plus the
// JSONC that will not parse as JSON all have to be read (#2216).
func TestTheMissingBinaryCheckReadsEveryConfigShapeAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "deja")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Both spelled the way this platform spells an absolute path, and escaped
	// for the format they are pasted into: a Windows path is backslashes.
	gone := filepath.Join(dir, "gone", "deja")
	goneJSON := jsonEscaped(t, gone)
	realJSON := jsonEscaped(t, real)
	for _, c := range []struct {
		name string
		body string
		want string
	}{
		{"a bare name is the PATH's", `{"mcpServers":{"deja":{"command":"deja","args":["mcp"]}}}`, ""},
		{"a binary that is there", `{"mcpServers":{"deja":{"command":"` + realJSON + `","args":["mcp"]}}}`, ""},
		// A quoted path in a file that does not parse as JSON: the escapes
		// belong to the format, and reading them raw named a path that exists
		// nowhere — doctor then called a working install broken.
		{"a quoted path in toml", "[mcp_servers.deja]\ncommand = \"" + realJSON + "\"\n", ""},
		// TOML's literal string, where a backslash is a backslash and nothing
		// is escaped — which is how a Windows path is written there.
		{"a literal path in toml", "[mcp_servers.deja]\ncommand = '" + real + "'\n", ""},
		{"a literal path that is gone", "[mcp_servers.deja]\ncommand = '" + gone + "'\n", gone},
		// YAML's double-quoted scalar escapes like JSON does.
		{"a quoted path in yaml", "extensions:\n  deja:\n    cmd: \"" + goneJSON + "\"\n", gone},
		{"no deja entry", `{"mcpServers":{"other":{"command":"/usr/bin/other"}}}`, ""},
		{"a directory named deja", `{"mcpServers":{"deja":{"command":"deja","cwd":"` + goneJSON + `"}}}`, ""},
		{"an unrelated path in toml", "[tool]\ndata_dir = \"" + goneJSON + "\"\n", ""},
		// Both resolve against something this check does not know: the reader's
		// working directory, and a shell that is not running.
		{"a relative path", `{"mcpServers":{"deja":{"command":"./deja"}}}`, ""},
		{"an unexpanded tilde", `{"mcpServers":{"deja":{"command":"~/bin/deja"}}}`, ""},
		{"json", `{"mcpServers":{"deja":{"command":"` + goneJSON + `"}}}`, gone},
		{"toml", "[mcp_servers.deja]\ncommand = \"" + goneJSON + "\"\n", gone},
		{"yaml", "extensions:\n  deja:\n    cmd: " + gone + "\n", gone},
		{"jsonc with comments", "{\n // deja\n \"mcpServers\":{\"deja\":{\"command\":\"" + goneJSON + "\"}}\n}", gone},
	} {
		p := filepath.Join(t.TempDir(), "config")
		if err := os.WriteFile(p, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := dejaCommandMissing(p); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// jsonEscaped is a path as it appears inside a JSON string: on Windows that is
// every backslash doubled, and pasting the raw path instead produces escapes
// the decoder refuses.
func jsonEscaped(t *testing.T, path string) string {
	t.Helper()
	b, err := json.Marshal(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b[1 : len(b)-1])
}

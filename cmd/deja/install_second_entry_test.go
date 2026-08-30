package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// doctor reads past the key at what an entry runs, and calls `deja-vu` deja's
// wiring. install did not: it wrote a second server beside it, so the harness
// started two on every session, one of them possibly pointing at a binary that
// is gone, and said only "updated" (#2712). It now takes the entry over instead
// of adding a sibling (#2269), which answers the same problem without leaving
// two servers behind at all.
func TestInstallAdoptsTheEntryAlreadyRunningDeja(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcpServers":{"deja-vu":{"command":"/old/bin/deja","args":["mcp"],"type":"stdio"}}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "install", "claude-code", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already here") {
		t.Errorf("install did not say it took over the entry it found:\n%s", out)
	}

	servers := claudeMCPServers(t, path)
	if len(servers) != 1 {
		t.Fatalf("the harness is left starting %d deja servers:\n%v", len(servers), servers)
	}
	entry, _ := servers["deja-vu"].(map[string]any)
	if entry == nil {
		t.Fatalf("install renamed the reader's entry instead of updating it:\n%v", servers)
	}
	if cmd, _ := entry["command"].(string); cmd == "/old/bin/deja" {
		t.Errorf("the adopted entry still runs the old binary:\n%v", entry)
	}

	// Not asserted here: what a second run does. install recognises its own
	// entry by the binary's name, and the test binary is called deja.test, so
	// the second run would not know the entry it just wrote. That is the
	// fixture, not the behaviour.
}

func claudeMCPServers(t *testing.T, path string) map[string]any {
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
	return servers
}

// An entry that is not deja's is not reported: the line is about deja's own
// wiring turning up twice, not about what else the reader wired.
func TestInstallSaysNothingAboutSomebodyElsesServer(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcpServers":{"mine":{"command":"/usr/local/bin/mine","args":["serve"]}}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "claude-code", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "also runs deja") {
		t.Errorf("somebody else's server was reported as deja's:\n%s", out)
	}
}

// The name in an argument is not the server. A filesystem server told to serve
// a checkout called deja carries the binary's name in its arguments, and a line
// telling the reader to remove it is deja pointing at a config that is none of
// its business.
func TestInstallDoesNotClaimAServerThatMerelyNamesAPath(t *testing.T) {
	for _, c := range []struct{ name, seed string }{
		{"a path argument", `{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/Users/me/code/deja"]}}}`},
		{"deja run for something else", `{"mcpServers":{"notes":{"command":"deja","args":["--not-mcp"]}}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			path := sources.ClaudeJSONPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := captureRun(t, "install", "claude-code", "--no-index")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "also runs deja") {
				t.Errorf("an entry that only names deja was reported as one:\n%s", out)
			}
		})
	}
}

// Every MCP writer edits the file that would hold the other entry, so every one
// of them has to take it over and say so — the first version told only Claude
// Code's reader, and an earlier one left a second server behind.
func TestEveryMCPWriterAdoptsAnEntryAlreadyRunningDeja(t *testing.T) {
	for _, c := range []struct{ target, file, seed string }{
		{"cursor", ".cursor/mcp.json",
			`{"mcpServers":{"deja-vu":{"command":"/old/bin/deja","args":["mcp"]}}}`},
		{"opencode", ".config/opencode/opencode.json",
			`{"mcp":{"deja-vu":{"type":"local","command":["/old/bin/deja","mcp"]}}}`},
		{"openclaw", ".openclaw/openclaw.json",
			`{"mcp":{"servers":{"deja-vu":{"command":"/old/bin/deja","args":["mcp"]}}}}`},
	} {
		t.Run(c.target, func(t *testing.T) {
			home := hermeticEnv(t)
			path := filepath.Join(home, "home", c.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := captureRun(t, "install", c.target, "--no-index")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "already here") {
				t.Errorf("%s did not say it took the entry over:\n%s", c.target, out)
			}
		})
	}
}

// With two hand-written entries, one is taken over and what is left is named.
func TestTheEntriesLeftBesideTheAdoptedOneAreNamed(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcpServers":{` +
		`"deja-vu":{"command":"/old/bin/deja","args":["mcp"]},` +
		`"deja-old":{"command":"/older/bin/deja","args":["mcp"]}}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "claude-code", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already here") {
		t.Errorf("install did not take over one of them:\n%s", out)
	}
	// The one it did not adopt is still a server the harness will start.
	if !strings.Contains(out, `"deja-vu"`) {
		t.Errorf("the entry left beside the adopted one went unnamed:\n%s", out)
	}
	if !strings.Contains(out, "also runs deja") {
		t.Errorf("nothing said what that entry is:\n%s", out)
	}
}

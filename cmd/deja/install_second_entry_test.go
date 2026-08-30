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
// is gone, and said only "updated" (#2712).
func TestInstallNamesAnotherEntryThatRunsDeja(t *testing.T) {
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
	if !strings.Contains(out, "deja-vu") {
		t.Errorf("the entry already running deja went unmentioned:\n%s", out)
	}
	if !strings.Contains(out, "also runs deja") {
		t.Errorf("nothing said what that entry is:\n%s", out)
	}

	// The reader's file is theirs: deja says what it found and takes nothing
	// out. A second entry can be deliberate — another index, a pinned binary.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if _, ok := servers["deja-vu"]; !ok {
		t.Errorf("install removed an entry it only had to report:\n%s", b)
	}
	if _, ok := servers["deja"]; !ok {
		t.Errorf("install did not write its own entry:\n%s", b)
	}

	// And a second run says the same thing rather than going quiet: the file
	// still holds two servers.
	out, err = captureRun(t, "install", "claude-code", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "also runs deja") {
		t.Errorf("the second install stopped mentioning it:\n%s", out)
	}
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
// of them has to say so — the first version told only Claude Code's reader.
func TestEveryMCPWriterNamesASecondDejaEntry(t *testing.T) {
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
			if !strings.Contains(out, "also runs deja") {
				t.Errorf("%s wrote a second server and said nothing:\n%s", c.target, out)
			}
		})
	}
}

// Two of them read as a sentence.
func TestASecondAndThirdEntryReadAsOneSentence(t *testing.T) {
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
	if !strings.Contains(out, `the entries "deja-old", "deja-vu" also run deja`) {
		t.Errorf("two entries did not read as two:\n%s", out)
	}
	if !strings.Contains(out, "start them alongside") {
		t.Errorf("the sentence did not agree with itself:\n%s", out)
	}
}

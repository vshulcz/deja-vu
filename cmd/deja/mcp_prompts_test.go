package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The slash command, in the only form deja can offer a harness it ships no
// command file into. Gemini and qwen both ask an MCP server for prompts/list on
// startup and put what comes back behind a slash; deja answered -32601, so
// typing the command found nothing.
func TestMCPPromptRunsTheSearch(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "app", "sess-alpha",
		"the frobnicator crash in parser.go", "fixed the frobnicator by widening the guard")

	list := driveMCP(t, `{"jsonrpc":"2.0","id":1,"method":"prompts/list","params":{}}`)
	res, _ := list[0]["result"].(map[string]any)
	prompts, _ := res["prompts"].([]any)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %#v, want the one command", res)
	}
	p, _ := prompts[0].(map[string]any)
	if p["name"] != "deja" {
		t.Errorf("prompt name = %v, want deja", p["name"])
	}
	args, _ := p["arguments"].([]any)
	if len(args) != 1 {
		t.Fatalf("arguments = %#v, want one query", p["arguments"])
	}
	a, _ := args[0].(map[string]any)
	if a["name"] != "query" || a["required"] != true {
		t.Errorf("argument = %#v, want a required query", a)
	}

	// And getting it runs the search rather than telling the model to.
	got := driveMCP(t, `{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"deja","arguments":{"query":"frobnicator"}}}`)
	gres, ok := got[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("prompts/get failed: %#v", got[0])
	}
	msgs, _ := gres["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %#v, want one", gres["messages"])
	}
	m, _ := msgs[0].(map[string]any)
	if m["role"] != "user" {
		t.Errorf("role = %v, want user", m["role"])
	}
	content, _ := m["content"].(map[string]any)
	text, _ := content["text"].(string)
	// Something only the seeded session holds — echoing the query back would
	// pass while the search never ran.
	if !strings.Contains(text, "widening the guard") {
		t.Errorf("the command did not carry the search result: %q", text)
	}
	if strings.Contains(text, "nothing in this machine") {
		t.Errorf("the command answered as if the store were empty: %q", text)
	}
}

// A command typed with nothing after it, and one that names a prompt deja does
// not have: both are the user's mistake and both must say so rather than paste
// an empty message into the conversation.
func TestMCPPromptRefusesWhatItCannotAnswer(t *testing.T) {
	hermeticEnv(t)

	for _, tc := range []struct{ name, req string }{
		{"no query", `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"deja","arguments":{}}}`},
		{"unknown prompt", `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"nope","arguments":{"query":"x"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := driveMCP(t, tc.req)
			if _, ok := resp[0]["error"].(map[string]any); !ok {
				t.Fatalf("answered instead of refusing: %#v", resp[0])
			}
		})
	}
}

// The capability has to be declared or a host never asks: gemini and qwen read
// initialize before they call prompts/list.
func TestMCPDeclaresPrompts(t *testing.T) {
	hermeticEnv(t)
	resp := driveMCP(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	res, _ := resp[0]["result"].(map[string]any)
	caps, _ := res["capabilities"].(map[string]any)
	if _, ok := caps["prompts"]; !ok {
		t.Errorf("capabilities = %#v, want prompts declared", caps)
	}
}

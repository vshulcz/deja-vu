package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every harness names its shell tool differently and declares a different
// argument shape for it. A mock that hardcodes either calls something the
// harness does not have, or calls it wrongly — and the run then looks like a
// harness that ignores tools, which is what it looked like for two of three.
func TestShellToolComesFromWhatTheHarnessDeclared(t *testing.T) {
	for _, tc := range []struct {
		name, tools, wantName, wantArgs string
	}{
		{
			name:     "chat completions, string argument",
			tools:    `[{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}]`,
			wantName: "bash",
			wantArgs: `{"command":"npm run build"}`,
		},
		{
			name:     "responses, a different property name",
			tools:    `[{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}]`,
			wantName: "exec_command",
			wantArgs: `{"cmd":"npm run build"}`,
		},
		{
			name:     "an argv array rather than a string",
			tools:    `[{"type":"function","name":"shell","parameters":{"type":"object","properties":{"command":{"type":"array"}}}}]`,
			wantName: "shell",
			wantArgs: `{"command":["/bin/sh","-c","npm run build"]}`,
		},
		{
			name:     "messages, input_schema",
			tools:    `[{"name":"Bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}]`,
			wantName: "Bash",
			wantArgs: `{"command":"npm run build"}`,
		},
		{
			name:  "nothing that runs a command",
			tools: `[{"name":"read_file"},{"name":"web_search"}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, args := shellTool(json.RawMessage(tc.tools), "npm run build")
			if name != tc.wantName {
				t.Fatalf("tool = %q, want %q", name, tc.wantName)
			}
			if name != "" && args != tc.wantArgs {
				t.Fatalf("arguments = %s, want %s", args, tc.wantArgs)
			}
		})
	}
}

// deja's MCP tools carry the harness's own prefix, so the recall tool cannot be
// named on the command line either.
func TestRecallToolIsFoundUnderAnyPrefix(t *testing.T) {
	for _, tools := range []string{
		`[{"name":"deja__recall","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]`,
		`[{"type":"function","function":{"name":"mcp__deja__recall","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}}]`,
	} {
		name, args := recallTool(json.RawMessage(tools), "billing gateway")
		if !strings.Contains(name, "recall") {
			t.Errorf("no recall tool found in %s", tools)
		}
		if !strings.Contains(args, "billing gateway") {
			t.Errorf("the query did not reach the arguments: %s", args)
		}
	}
	// A harness that exposes deja under one entry rather than one per tool.
	name, _ := recallTool(json.RawMessage(`[{"name":"mcp__deja"}]`), "x")
	if name != "mcp__deja" {
		t.Errorf("the single-entry form was not used: %q", name)
	}
	if name, _ := recallTool(json.RawMessage(`[{"name":"read_file"}]`), "x"); name != "" {
		t.Errorf("a harness without deja's tools returned %q", name)
	}
}

// One tool call per conversation, decided from the request. A flag on the
// server meant the first harness in a sweep took the only call and every later
// one silently got prose instead.
func TestToolCallHappensOncePerConversation(t *testing.T) {
	s := &server{reply: "47", toolCall: "auto", toolArg: "npm run build"}
	tools := `"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}]`

	first := post(t, s, "/v1/chat/completions",
		`{`+tools+`,"messages":[{"role":"user","content":"build it"}]}`)
	if !strings.Contains(first.Body.String(), "tool_calls") {
		t.Fatalf("no tool call on the opening turn:\n%s", first.Body.String())
	}
	// The same conversation, now carrying the result.
	second := post(t, s, "/v1/chat/completions",
		`{`+tools+`,"messages":[{"role":"user","content":"build it"},`+
			`{"role":"assistant","content":null},`+
			`{"role":"tool","tool_call_id":"call_mock","content":"ok"}]}`)
	if strings.Contains(second.Body.String(), "tool_calls") {
		t.Errorf("asked for the same call again, which loops the harness:\n%s",
			second.Body.String())
	}
	// A different conversation still gets one.
	third := post(t, s, "/v1/chat/completions",
		`{`+tools+`,"messages":[{"role":"user","content":"build it again"}]}`)
	if !strings.Contains(third.Body.String(), "tool_calls") {
		t.Errorf("a fresh conversation got no tool call:\n%s", third.Body.String())
	}
}

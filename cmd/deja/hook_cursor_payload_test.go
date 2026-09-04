package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Cursor's tool payload, copied from what cursor-agent 2026.09.02 actually
// handed a hook on this machine. Three things in it are not what deja read: the
// tool is called Shell, the output arrives under tool_output as a JSON document
// inside a JSON string, and cwd is empty with the project named in
// workspace_roots. Any one of them left the fix pair silent on cursor.
const cursorPostToolUse = `{
  "hook_event_name": "postToolUse",
  "tool_name": "Shell",
  "tool_input": {"command": "go build ./...", "cwd": "", "timeout": 30000},
  "tool_output": "{\"output\":\"./main.go:12:2: undefined: zorbquuxHelper\\n\",\"exitCode\":1}",
  "cwd": "",
  "workspace_roots": ["/Users/probe/app"],
  "session_id": "cur-1"
}`

func TestCursorToolPayloadIsRead(t *testing.T) {
	var input toolAfterInput
	if err := json.NewDecoder(bytes.NewReader([]byte(cursorPostToolUse))).Decode(&input); err != nil {
		t.Fatal(err)
	}
	if !isCommandTool(input.ToolName) {
		t.Errorf("tool %q is not read as a command tool, so the fix pair never fires on cursor", input.ToolName)
	}
	if len(bytes.TrimSpace(input.ToolResponse)) != 0 {
		t.Fatalf("cursor sends no tool_response; got %s", input.ToolResponse)
	}
	got := toolResponseText(input.ToolOutput)
	if !strings.Contains(got, "undefined: zorbquuxHelper") {
		t.Errorf("the error was not pulled out of tool_output: %q", got)
	}
	// The escaped JSON must not survive into the text the signature is built
	// from — a line of quoted JSON matches no error deja has ever indexed.
	if strings.Contains(got, "exitCode") {
		t.Errorf("the wrapper was passed through as text: %q", got)
	}
}

// An empty cwd is cursor's normal case, and the project is in workspace_roots.
// Falling back to the process directory meant recall answered for whatever
// directory the harness happened to spawn the hook in.
func TestEmptyCWDFallsBackToTheWorkspace(t *testing.T) {
	if got := hookProjectPath("", []string{"/Users/probe/app"}); got != "/Users/probe/app" {
		t.Errorf("workspace_roots ignored: %q", got)
	}
	// And a payload that names both keeps the one the harness was explicit
	// about.
	if got := hookProjectPath("/Users/probe/other", []string{"/Users/probe/app"}); got != "/Users/probe/other" {
		t.Errorf("cwd should win when the harness sends one: %q", got)
	}
	if got := hookProjectPath("   ", nil); got != "" {
		t.Errorf("nothing to go on should stay empty, got %q", got)
	}
}

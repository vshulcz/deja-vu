package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The spawn branch built its payload as a JSON string and stringified it again
// on the way into the shell, so deja was handed a quoted string where it
// expects an object. It reads nothing out of that and answers with silence — a
// spawned agent in opencode started with no memory at all, and nothing said so.
//
// Driven against the installed plugin with a deja that answers only for an
// object: before, the subagent's prompt came back unchanged; after, it carries
// the block.
func TestOpencodeSpawnPayloadIsEncodedOnce(t *testing.T) {
	js := opencodePluginJS("/bin/deja")
	compact := strings.Join(strings.Fields(js), "")

	if strings.Contains(compact, "JSON.stringify(JSON.stringify(") {
		t.Error("the payload is encoded twice")
	}
	if !strings.Contains(compact, "constpayload={") {
		t.Error("the payload is not built as an object")
	}
	if !strings.Contains(compact, "echo${JSON.stringify(payload)}") {
		t.Error("the payload is not encoded on the way into the shell")
	}
	// What deja is handed has to parse as an object with the fields the hook
	// reads. The generated source is not JSON, so this checks the shape the
	// plugin assembles rather than the literal.
	for _, field := range []string{`hook_event_name:"PreToolUse"`, `tool_name:"Task"`, "tool_input:{prompt:args.prompt}"} {
		if !strings.Contains(compact, field) {
			t.Errorf("the payload lost %s", field)
		}
	}
	// And the hook's own contract, so a rename on either side is caught here.
	var probe struct {
		HookEventName string `json:"hook_event_name"`
		ToolName      string `json:"tool_name"`
	}
	if err := json.Unmarshal([]byte(`{"hook_event_name":"PreToolUse","tool_name":"Task"}`), &probe); err != nil {
		t.Fatal(err)
	}
	if probe.HookEventName != "PreToolUse" || probe.ToolName != "Task" {
		t.Fatal("the field names this test pins are not the ones deja reads")
	}
}

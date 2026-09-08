package sources

import (
	"strings"
	"testing"
	"time"
)

// Zed keeps what a tool returned on the agent message, tool_results keyed by
// tool_use_id; the reader took the ToolUse blocks and not the results, and
// the comment said Zed stores none (#3291). Shape from a real thread.
func TestZedToolResultsAreIndexedAsToolOutput(t *testing.T) {
	msg := `{"Agent":{"content":[{"Text":"Running the tests."},{"ToolUse":{"id":"call_1","name":"terminal","input":{"command":"go test ./...","cd":"."}}}],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"terminal","is_error":false,"content":{"Text":"Command \"go test ./...\" failed with exit code 1.\n\n# app/pkg\npkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"},"output":"…"},
	  "call_0":{"tool_use_id":"call_0","tool_name":"list_directory","is_error":true,"content":{"Text":"Path .} not found in project"},"output":"Path .} not found in project"}}}}`
	at := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	var toolOut []string
	for _, m := range zedWork([]byte(msg), at) {
		if m.Role == RoleToolOutput {
			toolOut = append(toolOut, m.Text)
		}
	}
	if len(toolOut) != 2 {
		t.Fatalf("tool output = %q, want both results", toolOut)
	}
	joined := strings.Join(toolOut, "\n")
	for _, want := range []string{"failed with exit code 1", "undefined: frobnicateWidget", "not found in project"} {
		if !strings.Contains(joined, want) {
			t.Errorf("tool output lacks %q:\n%s", want, joined)
		}
	}
}

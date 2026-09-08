package sources

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Zed does store what a tool returned — the reader's own comment said it did
// not. An Agent message carries `tool_results` keyed by tool_use_id, and on a
// real store of 30 threads that is 3,059 results, 141 of them errors and 98
// carrying "failed with exit code": none of it indexed, so `deja fix` had
// nothing to pair an error with (#3291).
func TestZedIndexesWhatAToolReturned(t *testing.T) {
	body := map[string]any{
		"content": []any{
			map[string]any{"ToolUse": map[string]any{
				"name":  "terminal",
				"input": map[string]any{"command": "go test ./..."},
			}},
		},
		"tool_results": map[string]any{
			// Keyed by tool_use_id, as the store writes it.
			"call_b": map[string]any{
				"tool_use_id": "call_b", "tool_name": "list_directory", "is_error": true,
				"content": map[string]any{"Text": "Path .} not found in project"},
				"output":  "Path .} not found in project",
			},
			"call_a": map[string]any{
				"tool_use_id": "call_a", "tool_name": "terminal", "is_error": true,
				"content": map[string]any{"Text": "go: command failed with exit code 1\nundefined: frobnicateWidget"},
			},
			// Some results carry only `output`.
			"call_c": map[string]any{
				"tool_use_id": "call_c", "tool_name": "read_file",
				"content": map[string]any{"Text": ""},
				"output":  "package parser",
			},
			// And an empty one is nothing to index.
			"call_d": map[string]any{"tool_use_id": "call_d", "tool_name": "read_file"},
		},
	}
	raw, err := json.Marshal(map[string]any{"Agent": body})
	if err != nil {
		t.Fatal(err)
	}

	got := zedWork(raw, time.Unix(1785000000, 0))
	var outputs []string
	commands := 0
	for _, m := range got {
		switch m.Role {
		case RoleToolOutput:
			outputs = append(outputs, m.Text)
		case RoleCommand:
			commands++
		}
	}
	if commands == 0 {
		t.Error("the command record stopped coming out")
	}
	if len(outputs) != 3 {
		t.Fatalf("tool output records = %d, want the three that carry text: %q", len(outputs), outputs)
	}
	// Sorted by tool_use_id, so a rebuild writes the same records in the same
	// order rather than the map's.
	if !strings.Contains(outputs[0], "frobnicateWidget") {
		t.Errorf("results are not in a stable order: %q", outputs)
	}
	joined := strings.Join(outputs, "\n")
	for _, want := range []string{"failed with exit code 1", "not found in project", "package parser"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no record carries %q: %q", want, outputs)
		}
	}
}

// The switch that turns tool output off has to reach here too.
func TestZedToolResultsFollowTheIndexSwitch(t *testing.T) {
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	raw, err := json.Marshal(map[string]any{"Agent": map[string]any{
		"tool_results": map[string]any{"c": map[string]any{
			"tool_name": "terminal", "content": map[string]any{"Text": "failed with exit code 1"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range zedWork(raw, time.Unix(1785000000, 0)) {
		if m.Role == RoleToolOutput {
			t.Errorf("tool output was indexed with the switch off: %q", m.Text)
		}
	}
}

package sources

import (
	"encoding/json"
	"testing"
)

func TestClaudeToolPathsTakesOnlyNamedFiles(t *testing.T) {
	raw := json.RawMessage(`[
	  {"type":"text","text":"editing the reader"},
	  {"type":"tool_use","name":"Read","input":{"file_path":"/repo/internal/index/store_io.go"}},
	  {"type":"tool_use","name":"Edit","input":{"file_path":"/repo/internal/index/store_io.go"}},
	  {"type":"tool_use","name":"Write","input":{"file_path":"/repo/docs/notes.md"}},
	  {"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/index/store_io_test.go"}},
	  {"type":"tool_result","content":"ok"}
	]`)
	got := claudeToolPaths(raw)
	want := "/repo/internal/index/store_io.go\n/repo/docs/notes.md"
	if got != want {
		t.Fatalf("paths =\n%q\nwant\n%q", got, want)
	}
}

// Bash is excluded on purpose: a path inside a shell command is guesswork, and
// guessing at paths is what made the prose-mention approach unusable (#531).
func TestClaudeToolPathsIgnoresEverythingElse(t *testing.T) {
	for _, raw := range []string{
		`[{"type":"tool_use","name":"Bash","input":{"command":"cat internal/index/index.go"}}]`,
		`[{"type":"text","text":"internal/index/index.go is the file"}]`,
		`[{"type":"tool_use","name":"Read","input":{}}]`,
		`"plain string"`, `null`, `[]`, `[1,2]`,
	} {
		if got := claudeToolPaths(json.RawMessage(raw)); got != "" {
			t.Fatalf("%s yielded %q", raw, got)
		}
	}
}

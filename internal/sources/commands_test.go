package sources

import (
	"encoding/json"
	"testing"
)

// Three quarters of command text on a real corpus is navigation that answers
// nothing and matches by accident: `cat internal/index/index.go` looks relevant
// to a question about the index. The selection is the feature.
func TestWorthIndexingKeepsWhatHappened(t *testing.T) {
	for _, c := range []string{
		"go test ./internal/index/ -count=1",
		"golangci-lint run",
		"gh pr checks 558",
		"git commit -m 'fix: x'",
		"make build && deja index --rebuild",
		"docker compose up -d",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped a command worth keeping: %q", c)
		}
	}
	for _, c := range []string{
		"cat internal/index/index.go",
		"ls -la",
		"grep -rn foo internal/",
		"cd /repo && pwd",
		"sed -n 1,40p internal/index/index.go",
		"echo hi",
	} {
		if worthIndexing(c) {
			t.Errorf("kept navigation: %q", c)
		}
	}
}

func TestClaudeCommandsExtractsAndFilters(t *testing.T) {
	raw := json.RawMessage(`[
	  {"type":"text","text":"running it"},
	  {"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/index/"}},
	  {"type":"tool_use","name":"Bash","input":{"command":"ls -la"}},
	  {"type":"tool_use","name":"Read","input":{"file_path":"/x.go"}},
	  {"type":"tool_result","content":"ok"}
	]`)
	got := claudeCommands(raw)
	if len(got) != 1 || got[0] != "$ go test ./internal/index/" {
		t.Fatalf("commands = %#v", got)
	}
}

// The reference parser must agree, or the differential test in #502 is empty.
func TestBothParsersAgreeOnCommands(t *testing.T) {
	body := `[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}}]`
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	a, b := commandsFromContent(v), claudeCommands(json.RawMessage(body))
	if len(a) != len(b) || len(a) != 1 || a[0] != b[0] {
		t.Fatalf("generic=%#v typed=%#v", a, b)
	}
}

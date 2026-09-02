package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The command branch of the per-action hook answered 2 of 120 commands an
// agent actually runs: "you have run this before" needs the whole line, and
// everything an agent runs is a compound unique in its arguments. What a
// command did to this machine last time is keyed on its shape, and says
// something worth acting on (#2924).
func TestTheHookSaysWhatTheCommandBrokeLastTime(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i, id := range []string{"f1", "f2"} {
		at := time.Now().Add(-time.Duration(i+1) * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"check the helm chart"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","id":"t` + id +
				`","input":{"command":"make check --jobs 4 2>&1 | tail -20"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t` + id +
				`","content":"make: *** [analysis-coverage] Error 1"}]}}`,
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	// A different invocation of the same command: different flags, different
	// pipeline, same thing being run.
	line := toolHookText(t, "make check --verbose | grep -i error")
	if !strings.Contains(line, "Error 1") {
		t.Errorf("the hook said nothing about what this command broke:\n%s", line)
	}
	if !strings.Contains(line, "make check") {
		t.Errorf("the line does not name what it is about:\n%s", line)
	}
	// A command with no such history stays silent.
	if line := toolHookText(t, "cargo build --release"); line != "" {
		t.Errorf("a command with no history got a line:\n%s", line)
	}
}

// One session hitting something is an anecdote. The bar is the same as the
// command table's own.
func TestOneFailureIsNotAWarning(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(root, "-tmp-app", "only.jsonl"), "only", []string{
		`{"type":"user","sessionId":"only","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"try the deploy"}}`,
		`{"type":"assistant","sessionId":"only","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","id":"t1","input":{"command":"terraform apply -auto-approve"}}]}}`,
		`{"type":"user","sessionId":"only","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Error: Backend initialization required"}]}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if line := toolHookText(t, "terraform apply"); strings.Contains(line, "Backend initialization") {
		t.Errorf("one sighting was served as a warning:\n%s", line)
	}
}

func toolHookText(t *testing.T, command string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"cwd":        "/tmp/app",
		"session_id": fmt.Sprintf("hook-%d", time.Now().UnixNano()),
		"tool_input": map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runHookTool(index.DefaultDir(), strings.NewReader(string(payload)), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

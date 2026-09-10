package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The one thing deja can say for certain before a command runs. It has been in
// the session-start block for a long time and it is measurably not heard: of
// the ten sessions on this machine that were told a command was missing, nine
// ran it anyway and got the same refusal.
func TestTheHookNamesAProgramThisMachineDoesNotHave(t *testing.T) {
	dir := storeThatKeepsMissing(t, "zonkotool", 3)
	line := commandHookLine(dir, "/work/app", "zonkotool --check ./src")
	if line == "" {
		t.Fatal("the hook said nothing about a command this machine has never had")
	}
	if !strings.Contains(line, "zonkotool") || !strings.Contains(line, "3 sessions") {
		t.Errorf("the line does not name the program and how often: %q", line)
	}
}

// One sighting is a machine that may have changed since. deja does not check
// whether the program is there now, so it must not turn a single refusal into
// an instruction.
func TestOneRefusalIsNotEnough(t *testing.T) {
	dir := storeThatKeepsMissing(t, "zonkotool", 1)
	if line := commandHookLine(dir, "/work/app", "zonkotool --check ./src"); line != "" {
		t.Errorf("one sighting was enough to warn: %q", line)
	}
}

// The wrapper is a program too, and it is the one this machine is missing most.
func TestAMissingWrapperIsFound(t *testing.T) {
	dir := storeThatKeepsMissing(t, "zonkotool", 3)
	line := commandHookLine(dir, "/work/app", "zonkotool 30 go test ./... -count=1")
	if !strings.Contains(line, "zonkotool") {
		t.Errorf("a missing wrapper in front of a real command was not named: %q", line)
	}
}

// storeThatKeepsMissing builds a store where n separate sessions were refused
// the same program.
func storeThatKeepsMissing(t *testing.T, prog string, n int) string {
	t.Helper()
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	for i := 0; i < n; i++ {
		id := "s" + string(rune('a'+i))
		writeClaudeFixture(t, filepath.Join(claudeRoot, "app", id+".jsonl"), id, []string{
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + ts +
				`","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"` + prog + ` --check ./src"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + ts +
				`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"zsh:1: command not found: ` + prog + `"}]}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

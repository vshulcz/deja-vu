package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// Every session-start and per-prompt block leaves a snapshot of its text; the
// point of action left only a count. Read from a real machine: 507 injections
// there and not one whose content could be read back, so the surface with the
// best evidence behind it was the one that could not be audited, replayed, or
// compared against what the agent did next — and `deja log --last` had nothing
// to print for it.
func TestThePointOfActionKeepsWhatItSaid(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := tmp + "/index.db"
	t.Setenv("DEJA_INDEX_DIR", dir)
	commandRunWithAnOutcome(t, "go test ./... -count=1", "a", "b")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash",`+
		`"tool_input":{"command":"go test ./... -count=1"},"session_id":"agent-9","cwd":"/work/app"}`)
	if out == "" {
		t.Fatal("the hook said nothing, so there is no injection to record")
	}

	b, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil {
		t.Fatalf("no injections file: %v", err)
	}
	var kept, into string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var row struct {
			Kind   string `json:"kind"`
			Digest string `json:"digest"`
			Text   string `json:"text"`
			Into   string `json:"into"`
		}
		if json.Unmarshal([]byte(line), &row) != nil || row.Kind != usage.KindTool {
			continue
		}
		kept = row.Digest + row.Text
		into = row.Into
	}
	if kept == "" {
		t.Fatalf("the point-of-action injection left no text:\n%s", b)
	}
	// The line itself, whatever it said: this fixture's is the command's own
	// history, and what matters is that the text reached the file.
	if !strings.Contains(kept, "has run that command") {
		t.Errorf("the snapshot does not hold what was said: %q", kept)
	}
	if into != "agent-9" {
		t.Errorf("the snapshot was recorded into %q, want the agent session", into)
	}
}

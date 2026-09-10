package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// The point of action is the surface with the best evidence behind it, and it
// was the one recorded without a receiver: 492 injections on this machine and
// not one that could be paired with what the agent did next. The per-prompt
// hook has carried the receiving session since #1494; these two now do too.
func TestThePointOfActionRecordsWhoItWentTo(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	commandRunWithAnOutcome(t, "go test ./... -count=1", "a", "b")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash",`+
		`"tool_input":{"command":"go test ./... -count=1"},"session_id":"agent-7","cwd":"/work/app"}`)
	if out == "" {
		t.Fatal("the hook said nothing, so there is no injection to record")
	}
	if into := lastInjectionInto(t, dir); into != "agent-7" {
		t.Errorf("the injection was recorded into %q, want the agent session", into)
	}
}

func lastInjectionInto(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(usage.Path(dir))
	if err != nil {
		t.Fatalf("no usage log: %v", err)
	}
	last := ""
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e struct {
			Kind string `json:"kind"`
			Into string `json:"into"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Kind == usage.KindTool {
			last = e.Into
		}
	}
	return last
}

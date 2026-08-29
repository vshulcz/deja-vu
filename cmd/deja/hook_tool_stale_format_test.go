package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// #777 taught hook-prompt and hook-context to ask for the rebuild an upgrade
// makes necessary; the action hooks were left out. A spawned subagent gets no
// session start and sends no user prompt — install.go says so where it wires
// Task and Agent — so on a machine that has just upgraded, everything an agent
// does runs against an index nothing has asked to rebuild (#2567).
func TestActionHooksAskForTheRebuildAnUpgradeNeeds(t *testing.T) {
	hermeticEnv(t)
	// hermeticEnv suppresses the request; the question here is whether the
	// hooks ask at all, so let it through and count the spawn.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	spawned := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { spawned++; return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })

	for _, tc := range []struct {
		name    string
		payload string
		run     func(dir string, stdin *strings.Reader, out *bytes.Buffer) error
	}{
		{"hook-tool", `{"session_id":"s","cwd":"/w","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
			func(dir string, in *strings.Reader, out *bytes.Buffer) error { return runHookTool(dir, in, out) }},
		{"hook-tool-after", `{"session_id":"s","cwd":"/w","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"stderr":"undefined: snorblefunc"}}`,
			func(dir string, in *strings.Reader, out *bytes.Buffer) error { return runHookToolAfter(dir, in, out) }},
		{"hook-plan", `{"session_id":"s","cwd":"/w","tool_name":"ExitPlanMode","tool_input":{"plan":"Plan: change the pgbouncer pool size."}}`,
			func(dir string, in *strings.Reader, out *bytes.Buffer) error { return runHookPlan(dir, in, out) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "idx")
			writeStaleFormatIndex(t, dir)
			if index.IsCurrentVersion(dir) {
				t.Fatal("fixture claims the current format version")
			}
			spawned = 0
			var out bytes.Buffer
			if err := tc.run(dir, strings.NewReader(tc.payload), &out); err != nil {
				t.Fatal(err)
			}
			// Silence is still the contract: the hook must not answer from an
			// index it cannot read, and must not rebuild inside the action.
			if out.Len() != 0 {
				t.Errorf("answered from a stale-format index: %q", out.String())
			}
			if spawned != 1 {
				t.Errorf("requested %d rebuilds, want 1", spawned)
			}
		})
	}
}

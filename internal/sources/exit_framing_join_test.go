package sources

import (
	"os"
	"strings"
	"testing"
)

// Kilo Code writes two spaces after "terminal", an Antigravity step may lay
// its output out as Stdout:/Stderr:, and a planner row may ask for a command
// too trivial to record (#4530).
func TestExitFramingKiloSpacingAndAntigravityAttribution(t *testing.T) {
	runExitRows(t, []exitRow{
		// kilocode-legacy executeCommandTool: `Command executed in terminal ${workingDirInfo}`
		// where workingDirInfo starts with a space.
		{"kilo double space", "kilocode-task", rooExitTask("Command executed in terminal  within working directory '/tmp/proj'. Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 1\nOutput:\nFAIL"), "$ go test ./...  → exit 1"},
		{"antigravity stdout shape without exit header", "antigravity", antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\n\nStdout:\nThe command exited with code 1.\n"), "$ go test ./..."},
	})
	// A planner asking for two commands, one too trivial to record: the step
	// for the trivial one must not stamp the other.
	t.Run("antigravity unrecorded sibling call", func(t *testing.T) {
		dir := t.TempDir()
		p := antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\n\nThe command exited with code 0.\nOutput:\nM retry.go", "Created At: 2026-09-30T10:00:06Z\n\nThe command exited with code 1.\nOutput:\nFAIL")(t, dir)
		b, _ := os.ReadFile(p)
		body := strings.Replace(string(b), `"tool_calls":[{"name":"run_command"`, `"tool_calls":[{"name":"run_command","args":{"CommandLine":"\"ls\"","Cwd":"\"/tmp/proj\""}},{"name":"run_command"`, 1)
		writeExitFixture(t, p, body)
		got := commandsOf(parseKindForTest(t, "antigravity", p))
		for _, c := range got {
			if c == "$ go test ./...  → exit 0" {
				t.Fatalf("commands = %q: ls's exit landed on go test", got)
			}
		}
	})
}

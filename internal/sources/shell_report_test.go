package sources

import "testing"

// A command that printed nothing comes back as `Output: (empty)`. With the
// report unwrapped that left a tool-output record reading just "(empty)",
// which is no output at all, so none is written. An error beside it is still
// the command's output (#4256).
func TestQuietShellCommandLeavesNoToolOutput(t *testing.T) {
	cases := map[string]int{
		"Command: true\nDirectory: (root)\nOutput: (empty)\nError: (none)\nExit Code: 0": 0,
		"Output: (empty)\nProcess Group PGID: 98714":                                     0,
		"Output: (empty)\nError: spawn sh ENOENT\nExit Code: 1":                          1,
	}
	for report, want := range cases {
		parts := []any{map[string]any{"functionResponse": map[string]any{
			"id": "c1", "name": "run_shell_command", "response": map[string]any{"output": report},
		}}}
		got := 0
		for _, m := range qwenWorkRecords(parts, parseTimeAny(nil)) {
			if m.Role == RoleToolOutput {
				got++
				t.Logf("%q -> %q", report, m.Text)
			}
		}
		if got != want {
			t.Errorf("%q left %d tool-output records, want %d", report, got, want)
		}
	}
}

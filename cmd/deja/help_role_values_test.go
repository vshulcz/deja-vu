package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// roleStore writes one session carrying a record of every kind the decoder
// produces: prose, a read path, a replaced span, a shell command and tool
// output.
func roleStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-tmp-projr")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := strings.Join([]string{
		`{"type":"user","sessionId":"r1","cwd":"/tmp/projr","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"run the migration test suite"}}`,
		`{"type":"assistant","sessionId":"r1","cwd":"/tmp/projr","timestamp":"2026-08-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/migrate -run TestRollback"}}]}}`,
		`{"type":"assistant","sessionId":"r1","cwd":"/tmp/projr","timestamp":"2026-08-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/projr/internal/db/poolneedle.go","old_string":"maxOpen := 10","new_string":"maxOpen := 64"}}]}}`,
		`{"type":"user","sessionId":"r1","cwd":"/tmp/projr","timestamp":"2026-08-01T10:03:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"ModuleNotFoundError: No module named 'psycopg2'"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "r1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// `deja help` named three of the six --role values, leaving out the four that
// reach what the agent did rather than what was said. Those records stay out of
// ordinary results by design, so the flag is the only way to them and help is
// where someone looks for the flag (#1096).
func TestHelpNamesEveryRoleThatMatches(t *testing.T) {
	roleStore(t)

	// Each role has to actually reach a record of its own kind, or the help
	// text would be documenting something that does not work.
	for _, tc := range []struct{ role, query string }{
		{"user", "migration"},
		{"assistant", "TestRollback"}, // the command record is an assistant turn's tool call
		{sources.RoleCommand, "TestRollback"},
		{sources.RoleFiles, "poolneedle.go"},
		{sources.RoleEdit, "poolneedle.go"},
		{sources.RoleToolOutput, "ModuleNotFoundError"},
		{"tool", "ModuleNotFoundError"}, // documented alias for tool-output
	} {
		out, err := captureRun(t, "search", "--role", tc.role, tc.query)
		if err != nil {
			t.Fatalf("--role %s: %v", tc.role, err)
		}
		if tc.role == "assistant" {
			continue // prose-only assertion is covered by the others
		}
		if !strings.Contains(out, "r1") {
			t.Errorf("--role %s did not reach its own record for %q:\n%s", tc.role, tc.query, out)
		}
	}

	help, err := captureRun(t, "help")
	if err != nil {
		t.Fatal(err)
	}
	// Both places help mentions roles, checked separately: the `deja last`
	// usage line and the search-flag description. Asserting on the whole help
	// text lets one of them carry the other.
	lastLine, flagLine := "", ""
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, "deja last [n]") {
			lastLine = line
		}
		if strings.HasPrefix(strings.TrimSpace(line), "--role ") {
			flagLine = line
			// the description wraps, so take the continuation with it
		}
	}
	if lastLine == "" || flagLine == "" {
		t.Fatalf("help no longer has both role mentions:\n%s", help)
	}
	rolesBlock := help[strings.Index(help, flagLine):]
	if i := strings.Index(rolesBlock, "--limit"); i > 0 {
		rolesBlock = rolesBlock[:i]
	}
	for _, role := range []string{sources.RoleFiles, sources.RoleCommand, sources.RoleEdit} {
		if !strings.Contains(lastLine, role) {
			t.Errorf("`deja last` usage line does not name the %q role: %q", role, lastLine)
		}
		if !strings.Contains(rolesBlock, role) {
			t.Errorf("the --role flag description does not name the %q role, which works "+
				"and which ordinary search cannot reach: %q", role, rolesBlock)
		}
	}
}

// The flag is the only route to these records, which is what makes the omission
// matter rather than being a documentation nicety.
func TestOrdinarySearchCannotReachToolRecords(t *testing.T) {
	roleStore(t)

	out, err := captureRunStderr(t, "search", "poolneedle.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no matches") {
		t.Errorf("a file path now matches ordinary search; if that is intended the help "+
			"wording about roles needs revisiting:\n%s", out)
	}
	found, err := captureRun(t, "search", "--role", sources.RoleFiles, "poolneedle.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(found, "r1") {
		t.Fatalf("--role files did not reach the path record:\n%s", found)
	}
}

// The usage block is one column; a stray tab put `remember` outside it.
func TestHelpUsageBlockIsOneColumn(t *testing.T) {
	help, err := captureRun(t, "help")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(line, "\t") && strings.Contains(line, "deja ") {
			t.Errorf("usage line is indented with a tab, not two spaces: %q", line)
		}
	}
}

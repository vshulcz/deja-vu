package sources

import (
	"errors"
	"os/exec"
	"testing"
)

// The row names what sqlite3 said, not its exit code: the first stderr line,
// or nothing when the error carries none (#3190).
func TestExitStderrLineIsTheFirstLineSqliteWrote(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	cmd := exec.Command("sh", "-c", "echo 'Error: in prepare, file is not a database (26)' >&2; echo second >&2; exit 26")
	_, err := cmd.Output()
	if got := ExitStderrLine(err); got != "Error: in prepare, file is not a database (26)" {
		t.Fatalf("line = %q", got)
	}
	if got := ExitStderrLine(errors.New("plain")); got != "" {
		t.Fatalf("a plain error gave %q", got)
	}
	if got := ExitStderrLine(nil); got != "" {
		t.Fatalf("nil gave %q", got)
	}
}

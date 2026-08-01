package index

import "testing"

// The list was nine phrases about things not being found or permitted, and it
// matched 3 of 12 ordinary errors — missing runtime panics, database timeouts,
// auth failures and build failures. One miss was capitalisation alone: curl
// writes "Connection refused" (#729).
func TestIsFrictionCoversOrdinaryErrors(t *testing.T) {
	walls := []string{
		"bash: pytest: command not found",
		"go: module github.com/x/y: no such file or directory",
		"open /etc/secret: permission denied",
		"panic: runtime error: index out of range [5] with length 3",
		"ERROR: canceling statement due to statement timeout",
		"fatal: could not read Username for 'https://github.com'",
		"npm ERR! code ELIFECYCLE",
		"make: *** [build] Error 2",
		"curl: (7) Failed to connect to localhost port 5432: Connection refused",
		"TypeError: Cannot read properties of undefined (reading 'id')",
	}
	for _, l := range walls {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("missed a wall: %q", l)
		}
	}

	// Source is not an error, however much it talks about one — a comment
	// mentioning panic became the top wall once the marker list widened.
	notWalls := []string{
		`echo "App not found: $APP"`,
		`if [ ! -f x ]; then echo "cannot find it"; fi`,
		`print("permission denied")`,
		"// panic: this is a comment about panics",
		"# connection refused happens when the port is closed",
		"   * panic: in a doc comment",
		"    // panic: an indented comment",
		"-- no such file or directory (sql comment)",
		"  3 sessions  bash: pytest: command not found",
	}
	for _, l := range notWalls {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("treated source as a wall: %q", l)
		}
	}
}

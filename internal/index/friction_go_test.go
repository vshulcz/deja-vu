package index

import "testing"

// Three walls this machine hits that no phrase recognised: the two ways the go
// toolchain says there is no module here, and sqlite saying a table is not
// there. Counted in the real stores: the go.mod line 17 times, the pattern
// line 12, the sqlite one 4 — and none of them was ever mined, so `deja fix`
// answered nothing for the sessions that hit them (#3373).
func TestFrictionReadsTheGoModuleAndSqliteWalls(t *testing.T) {
	walls := []string{
		"go: go.mod file not found in current directory or any parent directory; see 'go help modules'",
		"pattern ./...: directory prefix . does not contain main module or its selected dependencies",
		"Error: in prepare, no such table: part",
	}
	for _, w := range walls {
		if _, ok := FrictionLine(w); !ok {
			t.Errorf("not read as friction: %q", w)
		}
	}
	// And the source that talks about them is not a wall: a test asserting on
	// the text, a comment, a line of Go quoting it.
	notWalls := []string{
		`	if !strings.Contains(got[0], "does not contain main module") {`,
		`// go.mod file not found is what the toolchain says when there is no module`,
		`echo "no such table: part"`,
	}
	for _, s := range notWalls {
		if _, ok := FrictionLine(s); ok {
			t.Errorf("source read as friction: %q", s)
		}
	}
}

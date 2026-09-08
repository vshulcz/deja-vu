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

// "no such table" is what sqlite says and also what a person writes about a
// specification. The server's own shape is the difference, the same guard
// "does not exist" has carried since #2431 (review of #3373).
func TestNoSuchTableNeedsTheServersOwnShape(t *testing.T) {
	if _, ok := FrictionLine("Error: in prepare, no such table: part"); !ok {
		t.Error("sqlite saying a table is missing is not read as friction")
	}
	for _, prose := range []string{
		"there is no such table in the spec, so I improvised one",
		"we have no such table yet — add a migration",
	} {
		if _, ok := FrictionLine(prose); ok {
			t.Errorf("a sentence about tables is read as an error: %q", prose)
		}
	}
}

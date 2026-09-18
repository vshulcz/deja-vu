package search

import "testing"

// `path:line` has always been accepted and the number thrown away; the
// line-level answer needs it (#1181).
func TestCutLineSuffixKeepsTheLine(t *testing.T) {
	for _, tc := range []struct {
		in   string
		path string
		line int
	}{
		{"internal/index/fixpair.go:60", "internal/index/fixpair.go", 60},
		{"internal/index/fixpair.go:60:14", "internal/index/fixpair.go", 60},
		{"internal/index/fixpair.go", "internal/index/fixpair.go", 0},
		// A zero is not a line, and the suffix was already stripped before this
		// existed: the path is what is left, which is what blame has always
		// looked up.
		{"pool.go:0", "pool.go", 0},
		{"C:/work/pool.go", "C:/work/pool.go", 0},
		{"weird:name.go", "weird:name.go", 0},
	} {
		path, line := cutLineSuffix(tc.in)
		if path != tc.path || line != tc.line {
			t.Errorf("cutLineSuffix(%q) = %q, %d; want %q, %d", tc.in, path, line, tc.path, tc.line)
		}
	}
	// And the resolver carries it through.
	target, err := ResolveBlamePath("internal/index/fixpair.go:60")
	if err != nil || target.Line != 60 || target.Base != "fixpair.go" {
		t.Fatalf("target = %+v err = %v", target, err)
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"
)

// A run that waits behind another build finishes with the index current and
// nothing printed by the build itself — so the command owes the closing line
// the same run would print when nothing was in flight (#1751).
func TestAnIndexRunThatBuiltNothingStillSaysWhereItStands(t *testing.T) {
	var out bytes.Buffer
	w := &countingWriter{w: &out}
	if _, err := w.Write([]byte("deja: incremental index changed_files=1\n")); err != nil {
		t.Fatal(err)
	}
	if w.n == 0 {
		t.Error("the writer did not count what the build printed")
	}

	quiet := &countingWriter{w: &bytes.Buffer{}}
	if quiet.n != 0 {
		t.Error("a build that printed nothing counted output")
	}
	if line := indexQuietOutcome(true, 760); !strings.Contains(line, "up to date") || !strings.Contains(line, "760") {
		t.Errorf("a silent build over a current index ends with %q", line)
	}
	// A store that changed again while the other build was finishing: the run
	// still says where things stand rather than claiming to be current.
	if line := indexQuietOutcome(false, 760); strings.Contains(line, "up to date") || !strings.Contains(line, "760") {
		t.Errorf("a silent build over a stale index ends with %q", line)
	}
}

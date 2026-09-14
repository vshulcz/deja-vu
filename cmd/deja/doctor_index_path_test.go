package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// A file where the index directory belongs is not a missing index: a build
// refuses to run there rather than deleting it (#3610), so the advice attached
// to `missing` — run `deja warmup` — cannot be followed. Both surfaces name the
// state instead.
func TestDoctorNamesAnIndexPathThatIsAFile(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := os.WriteFile(dir, []byte("notes somebody kept here\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := inspectDoctorIndex(dir, nil)
	if report.State != "path-is-a-file" {
		t.Errorf("doctor --json calls it %q, want path-is-a-file", report.State)
	}

	var out bytes.Buffer
	doctorIndex(&out, report, dir)
	got := out.String()
	if !strings.Contains(got, "is a file, not a directory") {
		t.Errorf("the text report does not say what is at the path:\n%s", got)
	}
	if strings.Contains(got, "deja warmup") {
		t.Errorf("the report tells the reader to run a build that will refuse:\n%s", got)
	}
}

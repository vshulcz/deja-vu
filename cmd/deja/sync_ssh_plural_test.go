package main

import (
	"strings"
	"testing"
)

// The line the first sync against a new machine prints said "exported 1
// records", while the local path one file away counts properly (#2290).
func TestTheSSHExportLineCountsInSingularToo(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{
		{1, "exported 1 record\n"},
		{0, "exported 0 records\n"},
		{4, "exported 4 records\n"},
	} {
		got := sshExportedLine(c.n)
		if got != "deja: "+c.want {
			t.Errorf("%d: %q, want %q", c.n, got, "deja: "+c.want)
		}
		if c.n == 1 && strings.Contains(got, "1 records") {
			t.Errorf("one record is still plural: %q", got)
		}
	}

	// The pull line had the same shape and says it the same way now.
	if got := sshCountLine("imported", 1); got != "deja: imported 1 record\n" {
		t.Errorf("the pull line reads %q", got)
	}
	if got := sshCountLine("imported", 2); got != "deja: imported 2 records\n" {
		t.Errorf("the pull line reads %q", got)
	}
}

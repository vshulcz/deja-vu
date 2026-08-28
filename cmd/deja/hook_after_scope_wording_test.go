package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The lookup behind this line is machine-wide on purpose — an error signature
// is closer to an environment fact than to project history. The sentence said
// "here", which reads as this project, while offering a command from another
// one (#2363).
func TestTheAfterErrorLineSaysWhoseCommandItIs(t *testing.T) {
	when := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	other := fixLine(index.FixPair{
		Error: "panic: quaxbolt overflow", Command: "make acme-widen-cast",
		Key: "claude:acme0", When: when, Project: "clients/acme/api",
	})
	if other == "" {
		t.Fatal("no line for a usable pair, so this measures nothing")
	}
	if strings.Contains(other, "came up here before") {
		t.Errorf("the line still calls another project's history this one's: %q", other)
	}
	if !strings.Contains(other, "clients/acme/api") {
		t.Errorf("the line does not say whose command it offers: %q", other)
	}
	if !strings.Contains(other, "make acme-widen-cast") {
		t.Errorf("the command is gone from the line: %q", other)
	}

	// A pair mined before the project field existed still gets a line — it just
	// cannot name a project.
	old := fixLine(index.FixPair{
		Error: "panic: quaxbolt overflow", Command: "make widen-cast",
		Key: "claude:s0", When: when,
	})
	if old == "" || !strings.Contains(old, "make widen-cast") {
		t.Errorf("a pair without a project lost its line: %q", old)
	}
}

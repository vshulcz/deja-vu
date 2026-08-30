package main

import (
	"strings"
	"testing"
)

// `files` matches by proximity to where a topic was discussed, which is
// deliberate and measured — so a topic nobody said out loud correctly matches
// nothing. What it did was stop there, on the surface most likely to be handed
// a filename, while blame and how both answer it (#2646).
func TestFilesPointsAtTheRecordsServedByRole(t *testing.T) {
	roleServedStore(t)
	out, err := captureRun(t, "files", "widgetpipeline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no sessions mention") {
		t.Fatalf("the fixture no longer misses, so this pins nothing:\n%s", out)
	}
	if !strings.Contains(out, "deja blame") {
		t.Fatalf("nothing says a file on this machine matches it:\n%s", out)
	}
	if !strings.Contains(out, "deja how") {
		t.Fatalf("nothing says a command on this machine matches it:\n%s", out)
	}
}

// A topic this machine never saw is answered as before.
func TestFilesSaysNothingExtraForAnUnknownTopic(t *testing.T) {
	roleServedStore(t)
	out, err := captureRun(t, "files", "zonkomatic")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deja blame") || strings.Contains(out, "deja how") {
		t.Fatalf("a word this machine never saw was pointed somewhere:\n%s", out)
	}
}

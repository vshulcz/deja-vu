package index

import (
	"strings"
	"testing"
)

// The import path comes from whatever the caller passed — a script, an agent
// acting on a repo's instructions. Printed verbatim, a newline in it ended
// deja's error line and started one that reads as deja's own output.
func TestSyncImportErrorKeepsThePathOnOneLine(t *testing.T) {
	skipWindowsPortability(t)
	bad := "/tmp/no\u001bXsuch\ndeja: store is clean\u202edir"
	_, err := Import(t.TempDir(), bad)
	if err == nil {
		t.Fatal("importing a missing directory did not fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no such directory") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(msg, "\n") || strings.ContainsAny(msg, "\u001b\u202e") {
		t.Errorf("the path forged a line in the error: %q", msg)
	}
}

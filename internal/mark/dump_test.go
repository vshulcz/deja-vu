package mark

import (
	"os"
	"testing"
)

// TestDumpReadyGrid is not a test; it is how the Python literal in
// scripts/demo/story.py gets written, since that script cannot import this
// package. Run it with DEJA_DUMP_GRID=1 and paste what it prints.
//
//	DEJA_DUMP_GRID=1 go test ./internal/mark -run TestDumpReadyGrid -v
func TestDumpReadyGrid(t *testing.T) {
	if os.Getenv("DEJA_DUMP_GRID") == "" {
		t.Skip("set DEJA_DUMP_GRID=1 to print the sprite")
	}
	for _, row := range Grid(Ready) {
		t.Logf("    %q,", string(row))
	}
}

package main

import (
	"io"
	"strings"
	"testing"
)

// A whitespace-only path is no path at all: `deja restore "   "` sailed past
// the missing-path refusal because the check only looked for an empty string
// after the loop, and a leading empty argument was silently ignored instead
// of refused (#5937).
func TestRestoreWhitespaceOnlyPathIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"whitespace only", []string{"   "}},
		{"tabs and whitespace", []string{"\t  \t"}},
		{"empty", []string{""}},
		{"empty before a path", []string{"", "main.go"}},
		{"whitespace before a path", []string{"   ", "main.go"}},
		{"whitespace after a path", []string{"main.go", "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runRestore(t.TempDir(), tc.args, io.Discard)
			if err == nil {
				t.Fatalf("runRestore(%v) returned no error", tc.args)
			}
			if !strings.Contains(err.Error(), "usage: deja restore") {
				t.Errorf("runRestore(%v) = %v, want the restore usage error", tc.args, err)
			}
		})
	}
}

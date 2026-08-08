//go:build !windows

package sources

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A FIFO that matched the session glob hung the whole index: the parser's Open
// blocks on a named pipe with no writer and never returns. walkFiles must hand
// back only regular files so one such node in a scanned store cannot freeze
// indexing.
func TestWalkFilesSkipsNamedPipes(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.jsonl")
	if err := os.WriteFile(real, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(dir, "pipe.jsonl")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}

	got := walkFiles(dir, func(p string) bool { return strings.HasSuffix(p, ".jsonl") })

	var sawReal, sawPipe bool
	for _, p := range got {
		if p == real {
			sawReal = true
		}
		if p == pipe {
			sawPipe = true
		}
	}
	if !sawReal {
		t.Errorf("dropped the regular file: %v", got)
	}
	if sawPipe {
		t.Errorf("returned a named pipe the parser would block on: %v", got)
	}
}

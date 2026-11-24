//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestResolvePlanTextRejectsNamedFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "plan.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	// Hold both ends open so a writer does not block and buffered bytes exist.
	rw, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open fifo: %v", err)
	}
	defer func() { _ = rw.Close() }()
	if _, err := rw.WriteString("# FIFO plan\n"); err != nil {
		t.Fatalf("write fifo: %v", err)
	}

	gotCh := make(chan string, 1)
	go func() {
		gotCh <- resolvePlanText(dir, "", fifo)
	}()

	select {
	case got := <-gotCh:
		if got != "" {
			t.Fatalf("resolvePlanText(fifo) = %q, want empty", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("resolvePlanText(fifo) blocked instead of rejecting non-regular file")
	}
}

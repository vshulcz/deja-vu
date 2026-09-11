package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The capture helpers have to drain the pipe while the callback is still
// writing. A windows anonymous pipe buffers 4096 bytes, and `printSources`
// crossed that at twenty-five harnesses — 4316 bytes — so the sequential
// read-after-call in its test deadlocked the whole cmd/deja package on the
// windows leg of main for twelve minutes before the timeout killed it.
//
// This pins the helper rather than the caller: a payload far past any
// platform's buffer must come back whole. A helper that stops draining
// concurrently fails here — verified by making it sequential, which hangs this
// test until the package timeout rather than returning short, because the write
// it blocks in cannot be abandoned.
func TestCaptureStdoutOutlastsAPipeBuffer(t *testing.T) {
	const lines = 20000
	done := make(chan string, 1)
	go func() {
		done <- captureStdout(t, func() {
			for i := range lines {
				fmt.Printf("line %d of a payload no pipe buffer holds\n", i)
			}
		})
	}()
	select {
	case out := <-done:
		if got := strings.Count(out, "\n"); got != lines {
			t.Fatalf("captured %d lines of %d", got, lines)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("captureStdout did not return: the callback is writing into a pipe nobody is reading")
	}
}

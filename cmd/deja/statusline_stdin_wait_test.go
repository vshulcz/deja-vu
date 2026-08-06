package main

import (
	"os"
	"testing"
	"time"
)

// The status bar re-runs on every assistant message. It read stdin with
// ReadAll, which returns when the host closes the pipe — and a host that opens
// stdin and holds it open hung the line for as long as it was allowed to
// (#1074). The hooks have had a bound since #846.
func TestStatuslineDoesNotWaitForeverOnStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Never written to, never closed — the shape a stuck host presents.
	defer func() { _ = w.Close(); _ = r.Close() }()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		readStatuslineInput(r)
		done <- time.Since(start)
	}()
	select {
	case took := <-done:
		if took > 5*time.Second {
			t.Errorf("read took %v with stdin open and empty, want the %v bound", took, hookStdinWait)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the status line is still waiting on stdin after 10s; the bound is %v", hookStdinWait)
	}
}

// And the payload is still read when the host sends one.
func TestStatuslineStillReadsItsPayload(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	go func() {
		_, _ = w.Write([]byte(`{"transcript_path":"/tmp/does-not-exist.jsonl","cwd":"/tmp/x"}`))
		_ = w.Close()
	}()
	in := readStatuslineInput(r)
	if in.TranscriptPath != "/tmp/does-not-exist.jsonl" {
		t.Errorf("payload lost: %#v", in)
	}
}

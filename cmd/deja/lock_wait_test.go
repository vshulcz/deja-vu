package main

import (
	"strings"
	"testing"
	"time"
)

// A command waiting out someone else's rebuild at a terminal keeps a moving
// line with the seconds, then says the index is ready; piped it stays one
// plain line.
func TestWaitLine(t *testing.T) {
	var b strings.Builder
	l := &waitLine{w: &b, live: true}
	l.begin()
	time.Sleep(300 * time.Millisecond)
	l.end()
	out := b.String()
	if strings.Count(out, "\r") < 2 || !strings.Contains(out, "building the index") || !strings.Contains(out, "0s") {
		t.Errorf("no moving line: %q", out)
	}
	if !strings.HasSuffix(out, "\n") || !strings.Contains(out, "✓ the index is ready, waited 0.") {
		t.Errorf("the end of the wait is not said: %q", out)
	}

	b.Reset()
	l = &waitLine{w: &b}
	l.begin()
	l.end()
	if b.String() != "deja: another deja is building the index — waiting for it to finish\n" {
		t.Errorf("piped = %q", b.String())
	}
}

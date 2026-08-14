package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The build display is for terminals. Pipes, hooks and CI must keep the plain
// lines they have always had, with no escape sequences in them.
func TestBuildProgressStaysOutOfPipes(t *testing.T) {
	orig := logoWanted
	t.Cleanup(func() { logoWanted = orig })
	logoWanted = func(*os.File) bool { return false }

	ran := false
	if err := withBuildProgress(func() error { ran = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("the build never ran")
	}
}

// A repaint has to erase exactly what it drew, or the mark walks down the
// screen leaving copies of itself.
func TestBuildProgressRepaintErasesItsOwnHeight(t *testing.T) {
	var buf bytes.Buffer
	p := newBuildProgress(&buf)
	p.Phase("reading sessions", 10)
	p.Advance(5)
	p.paint(0)
	first := p.painted
	if first == 0 {
		t.Fatal("first paint drew nothing")
	}
	if strings.Contains(buf.String(), "\x1b[0J") {
		t.Fatal("the first paint erased a screen it had not drawn yet")
	}
	buf.Reset()
	p.Harness("claude", 29, 40096)
	p.paint(1)
	out := buf.String()
	if !strings.Contains(out, "\x1b[0J") {
		t.Fatal("the repaint did not clear the previous frame")
	}
	if !strings.Contains(out, "\x1b["+itoa(first)+"A") {
		t.Fatalf("the repaint moved the cursor up by the wrong number of lines; drew %d", first)
	}
	if !strings.Contains(ansiRE.ReplaceAllString(out, ""), "claude") {
		t.Fatal("the store that just landed is not in the frame")
	}
	// The frame is the mark's height until the column outgrows it; past that
	// it has to keep growing, or the last stores fall off the bottom.
	for i := 0; i < 20; i++ {
		p.Harness("store"+itoa(i), 1, 1)
	}
	buf.Reset()
	p.paint(2)
	if p.painted <= len(catArt) {
		t.Fatalf("a column of 21 stores still drew only %d lines", p.painted)
	}
	plain := ansiRE.ReplaceAllString(buf.String(), "")
	if !strings.Contains(plain, "store19") {
		t.Fatal("the last store was cut off the bottom of the frame")
	}
}

// finish clears the live area so the greeting that follows is not stacked on
// top of a half-drawn frame.
func TestBuildProgressFinishClearsTheArea(t *testing.T) {
	var buf bytes.Buffer
	p := newBuildProgress(&buf)
	p.Phase("indexing messages", 4)
	p.paint(0)
	buf.Reset()
	p.finish()
	if !strings.Contains(buf.String(), "\x1b[0J") {
		t.Fatal("finish left the frame on screen")
	}
	if p.painted != 0 {
		t.Fatalf("painted = %d after finish, want 0", p.painted)
	}
}

func TestBuildProgressBarTracksTheWork(t *testing.T) {
	p := newBuildProgress(&bytes.Buffer{})
	p.Phase("reading sessions", 100)
	if got := visibleLen(p.bar()); got == 0 {
		t.Fatal("an empty bar rendered nothing")
	}
	p.Advance(100)
	full := p.bar()
	if !strings.Contains(full, "100%") {
		t.Fatalf("a finished phase reads %q", ansiRE.ReplaceAllString(full, ""))
	}
	// Overshooting must not run past the end of the bar.
	p.Advance(500)
	if got, want := visibleLen(p.bar()), visibleLen(full); got != want {
		t.Fatalf("bar width drifted to %d from %d when the count overshot", got, want)
	}
	// A phase with no known total still renders, just without a percentage.
	p.Phase("writing index", 0)
	if strings.Contains(p.bar(), "%") {
		t.Error("an unknown total should not claim a percentage")
	}
}

// Stores with no messages are noise in the column.
func TestBuildProgressSkipsEmptyStores(t *testing.T) {
	p := newBuildProgress(&bytes.Buffer{})
	p.Harness("grok", 0, 0)
	p.Harness("claude", 2, 7)
	if len(p.harness) != 1 || p.harness[0].name != "claude" {
		t.Fatalf("harness lines = %+v", p.harness)
	}
	// The notes pseudo-source narrates under the name users know.
	p.Harness("deja", 1, 1)
	if p.harness[1].name != "notes" {
		t.Fatalf("deja rendered as %q", p.harness[1].name)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

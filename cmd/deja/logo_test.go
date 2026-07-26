package main

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestPrintLogo(t *testing.T) {
	var b bytes.Buffer
	printLogo(&b, brandInfo())
	out := b.String()
	if !strings.Contains(out, "█") || !strings.Contains(out, "deja-vu") || !strings.Contains(out, "memory for coding agents") {
		t.Fatalf("logo output: %q", out)
	}
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != len(loopArt) {
		t.Fatalf("mark should be %d lines, got %d: %q", len(loopArt), n, out)
	}
}

func TestLogoWanted(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if logoWanted(f) {
		t.Fatal("regular file must not want a logo")
	}
	t.Setenv("NO_COLOR", "1")
	if logoWanted(os.Stdout) {
		t.Fatal("NO_COLOR must suppress the logo")
	}
}

// Sixteen harnesses plus a header outgrow the mark, and the column used to be
// cut to the mark's height — the last stores simply vanished from the first
// build greeting.
func TestLogoLinesKeepsAColumnTallerThanTheMark(t *testing.T) {
	info := []string{"deja-vu", "", "header"}
	for i := 0; i < 22; i++ {
		info = append(info, "store"+strconv.Itoa(i))
	}
	out := logoLines(info)
	plain := strings.Join(out, "\n")
	for i := 0; i < 22; i++ {
		if !strings.Contains(plain, "store"+strconv.Itoa(i)) {
			t.Fatalf("store%d was dropped from a %d-line column rendered as %d lines", i, len(info), len(out))
		}
	}
	// The overflow keeps the same left margin as the lines beside the mark.
	var beside, below string
	for _, l := range out {
		if strings.Contains(l, "store0") {
			beside = l
		}
		if strings.Contains(l, "store21") {
			below = l
		}
	}
	// Columns are counted in runes: the mark is drawn with three-byte block
	// glyphs, so a byte offset would compare two different things.
	runeCol := func(line, want string) int {
		return len([]rune(ansiRE.ReplaceAllString(line, "")[:strings.Index(ansiRE.ReplaceAllString(line, ""), want)]))
	}
	if got, want := runeCol(below, "store21"), runeCol(beside, "store0"); got != want {
		t.Errorf("overflow line starts at column %d, the ones beside the mark at %d", got, want)
	}
}

package main

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
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

// The greeting was laid out against a hard column 42 and nothing added it up,
// so `try: deja "something you fixed weeks ago"` reached column 83 on the
// 80-column terminal that is the common case.
func TestLogoLinesFitTheWidth(t *testing.T) {
	info := append(brandInfo(), "", `try: deja "something you fixed weeks ago"`)
	for _, width := range []int{60, 80, 100, 120} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		for _, l := range logoLines(info) {
			if n := visibleLen(l); n > width {
				t.Errorf("COLUMNS=%d: line is %d columns: %q", width, n, ansiRE.ReplaceAllString(l, ""))
			}
		}
	}
}

// A narrow terminal cannot hold the mark and the column side by side, so the
// column goes underneath — but every line still has to be there.
func TestLogoLinesStackWhenNarrow(t *testing.T) {
	t.Setenv("COLUMNS", "60")
	info := firstIndexInfo(index.BuildSummary{
		Messages: 5, Harnesses: 2,
		PerHarness: []index.HarnessCount{
			{Name: "claude", Messages: 2, Sessions: 1},
			{Name: "codex", Messages: 3, Sessions: 2},
		},
	}, `try: deja "something you fixed weeks ago"`)
	plain := ansiRE.ReplaceAllString(strings.Join(logoLines(info), "\n"), "")
	for _, want := range []string{"deja-vu", "memory for coding agents", "· 1 session", "· 2 sessions"} {
		if !strings.Contains(plain, want) {
			t.Errorf("stacked greeting dropped %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "1 sessions") {
		t.Error("a single session must not be counted in the plural")
	}
}

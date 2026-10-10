package search

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `a.txt:99` says "the file has 3 lines" (#3726). `a.txt:0` and `a.txt:abc`
// said nothing and answered about the file as though no line had been asked
// for — the same silence, one case earlier (#3738).

func TestALineSpecThatCannotBeUsedIsSaidOutLoud(t *testing.T) {
	for _, tc := range []struct {
		in   string
		path string
		line int
		note string
	}{
		// Digits trimLineSuffix accepted, that name no line.
		{"a.txt:0", "a.txt", 0, "there is no line 0"},
		{"a.txt:99999999999999999999", "a.txt", 0, "too large to be a line number"},
		// Not digits. The note says the answer is for the whole file, so the
		// file is what is searched: kept whole, `pool.go:abc` matched nothing.
		{"a.txt:abc", "a.txt", 0, `"abc" is not a line number`},
		{"a.txt:-1", "a.txt", 0, `"-1" is not a line number`},
		{"a.txt:+3", "a.txt", 0, `"+3" is not a line number`},
		{"a.txt:2.5", "a.txt", 0, `"2.5" is not a line number`},
	} {
		path, line, note := cutLineSuffix(tc.in)
		if path != tc.path || line != tc.line {
			t.Errorf("cutLineSuffix(%q) = %q, %d; want %q, %d", tc.in, path, line, tc.path, tc.line)
		}
		if !strings.Contains(note, tc.note) {
			t.Errorf("cutLineSuffix(%q) note = %q, want it to contain %q", tc.in, note, tc.note)
		}
	}
}

// A spec that works, and a path with no spec at all, have nothing to say.
func TestAUsableLineSpecSaysNothing(t *testing.T) {
	for _, in := range []string{
		"a.txt:12",
		"a.txt:12:5",
		"a.txt",
		"internal/index/fixpair.go:60",
	} {
		if _, _, note := cutLineSuffix(in); note != "" {
			t.Errorf("cutLineSuffix(%q) note = %q, want silence", in, note)
		}
	}
}

// The note must not fire on a colon that belongs to the filename. It is
// narrow on purpose: the part before the colon has to look like a filename,
// and the part after must hold no separator.
func TestAColonInAFilenameIsNotAMistypedLine(t *testing.T) {
	for _, in := range []string{
		"weird:name.go",
		"notes:draft",
		`C:\work\pool.go`,
		"C:/work/pool.go",
		"a.txt:",
	} {
		path, line, note := cutLineSuffix(in)
		if note != "" {
			t.Errorf("cutLineSuffix(%q) note = %q, want silence", in, note)
		}
		// And the path is still the whole string, as it has always been.
		if path != in || line != 0 {
			t.Errorf("cutLineSuffix(%q) = %q, %d; want the string unchanged", in, path, line)
		}
	}
}

// A file that really is named with the colon is taken as written.
func TestAnExistingFileWithAColonIsTakenAsWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a colon cannot be in a Windows filename")
	}
	dir := t.TempDir()
	name := filepath.Join(dir, "a.txt:abc")
	if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, line, note := cutLineSuffix(name)
	if path != name || line != 0 || note != "" {
		t.Errorf("cutLineSuffix(%q) = %q, %d, %q; want it as written", name, path, line, note)
	}
}

// The resolver carries it, which is how the command gets to say it.
func TestResolveBlamePathCarriesTheNote(t *testing.T) {
	target, err := ResolveBlamePath("a.txt:0")
	if err != nil {
		t.Fatalf("ResolveBlamePath: %v", err)
	}
	if target.Line != 0 || !strings.Contains(target.LineNote, "no line 0") {
		t.Fatalf("target = %+v", target)
	}
	if target.Base != "a.txt" {
		t.Fatalf("base = %q, want the file the answer is about", target.Base)
	}

	clean, err := ResolveBlamePath("a.txt:12")
	if err != nil || clean.Line != 12 || clean.LineNote != "" {
		t.Fatalf("a usable spec should carry no note: %+v err = %v", clean, err)
	}
}

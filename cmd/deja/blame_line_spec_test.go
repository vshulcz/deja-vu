package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A line spec deja cannot use said nothing at all: the file answer printed as
// if no line had been asked for, so `a.txt:0` read as "this file has no
// history" rather than "your `:0` went nowhere" (#3738). Swept eighteen forms
// against a three-line file to find them.
func TestBlameSaysWhenTheLineSpecCannotBeUsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	for _, spec := range []string{
		"a.txt:0",
		"a.txt:-1",
		"a.txt:abc",
		"a.txt:+3",
		"a.txt:2.5",
		// One past what an int holds: the parse fails and the suffix was cut,
		// which is the case a reader cannot see at all from the answer.
		"a.txt:99999999999999999999",
	} {
		t.Run(spec, func(t *testing.T) {
			target, err := search.ResolveBlamePath(spec)
			if err != nil {
				t.Fatal(err)
			}
			if target.Line != 0 {
				t.Fatalf("%q resolved to line %d; this test is about the specs that do not", spec, target.Line)
			}
			if target.LineSpec == "" {
				t.Fatalf("%q carried no spec to report", spec)
			}
			var buf bytes.Buffer
			lineBlame(&buf, t.TempDir(), target, nil)
			out := buf.String()
			if !strings.Contains(out, "is not a line number") {
				t.Fatalf("answer for %q = %q, want it to say the spec is not a line", spec, out)
			}
			// The name is printed once: the spec is part of the path when the
			// trim would not take it, and appending it again read as
			// `a.txt:abc:abc`.
			if strings.Count(out, target.LineSpec) != 2 {
				t.Fatalf("answer for %q = %q, want the spec named once in the header and once in the quote", spec, out)
			}
			if !strings.Contains(out, "whole file") {
				t.Fatalf("answer for %q = %q, want it to say what is answered instead", spec, out)
			}
		})
	}
}

// A file whose own name carries a colon is not a mistake to report.
func TestBlameLeavesAColonInAFileNameAlone(t *testing.T) {
	dir := t.TempDir()
	odd := filepath.Join(dir, "weird:name.txt")
	if err := os.WriteFile(odd, []byte("x\n"), 0o644); err != nil {
		t.Skipf("a colon in a file name is unavailable here: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	target, err := search.ResolveBlamePath("weird:name.txt")
	if err != nil {
		t.Fatal(err)
	}
	if target.LineSpec != "" {
		t.Fatalf("a file that exists reported %q as a line spec", target.LineSpec)
	}
}

// And a real line still answers about the line.
func TestBlameStillAnswersARealLine(t *testing.T) {
	target, err := search.ResolveBlamePath("a.txt:2")
	if err != nil {
		t.Fatal(err)
	}
	if target.Line != 2 || target.LineSpec != "" {
		t.Fatalf("target = %+v, want line 2 and no spec", target)
	}
}

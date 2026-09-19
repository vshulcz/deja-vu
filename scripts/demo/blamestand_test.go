package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The tape asks about a line by number, and the file it asks about is generated
// a few lines above. Adding an import or a comment to that source moves the
// line and the recording then blames a different one — which the GIF would show
// as deja having nothing to say.
func TestTheTapeAsksAboutTheLineTheStandWrites(t *testing.T) {
	want := 0
	for i, ln := range strings.Split(poolSource(blameNewLine), "\n") {
		if strings.TrimSpace(ln) == strings.TrimSpace(blameNewLine) {
			want = i + 1
			break
		}
	}
	if want == 0 {
		t.Fatal("the generated source no longer holds the line the demo asks about")
	}

	b, err := os.ReadFile("blame.tape")
	if err != nil {
		t.Fatal(err)
	}
	asked := regexp.MustCompile(`pool\.go:(\d+)`)
	blamed := regexp.MustCompile(`-L (\d+),(\d+)`)

	found := asked.FindAllStringSubmatch(string(b), -1)
	if len(found) == 0 {
		t.Fatal("blame.tape no longer asks deja about a line")
	}
	for _, m := range found {
		if got, _ := strconv.Atoi(m[1]); got != want {
			t.Errorf("blame.tape asks deja for line %s; the stand writes it at %d", m[1], want)
		}
	}
	// git is asked the same question in the frame above, and a mismatch there
	// is worse than a wrong number: the two commands would answer about
	// different lines and the comparison would be a lie.
	git := blamed.FindAllStringSubmatch(string(b), -1)
	if len(git) == 0 {
		t.Fatal("blame.tape no longer asks git about a line")
	}
	for _, m := range git {
		for _, n := range m[1:] {
			if got, _ := strconv.Atoi(n); got != want {
				t.Errorf("blame.tape asks git for line %s; the stand writes it at %d", n, want)
			}
		}
	}
}

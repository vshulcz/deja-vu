package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# Changelog

## [Unreleased]

### Added
- something not out yet

## [0.18.0] - 2026-08-22

Two new harnesses, and a recall path rebuilt.

### Added
- omp is the nineteenth harness (#1455)

## [0.17.3] - 2026-08-19

### Fixed
- a thing (#1400)
`

func TestSectionStopsAtTheNextVersion(t *testing.T) {
	got := Section(sample, "0.18.0")
	if !strings.HasPrefix(got, "Two new harnesses") {
		t.Fatalf("section does not start with the lede:\n%s", got)
	}
	if strings.Contains(got, "0.17.3") || strings.Contains(got, "a thing") {
		t.Fatalf("section ran into the next release:\n%s", got)
	}
	if strings.Contains(got, "not out yet") {
		t.Fatalf("section picked up Unreleased:\n%s", got)
	}
}

// The tag carries a v; the changelog heading does not.
func TestSectionAcceptsATag(t *testing.T) {
	if Section(sample, "v0.18.0") != Section(sample, "0.18.0") {
		t.Fatal("v-prefixed tag returned a different section")
	}
}

// A release cut before its changelog entry lands must not fail the release.
func TestMissingSectionIsEmptyNotAnError(t *testing.T) {
	if got := Section(sample, "9.9.9"); got != "" {
		t.Fatalf("unknown version returned %q", got)
	}
}

// The real file is what ships, so it is the one that has to parse.
func TestTheRepositoryChangelogHasABodyForEveryRelease(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSections(string(b)); err != nil {
		t.Fatal(err)
	}
	if s := Section(string(b), "0.18.0"); !strings.Contains(s, "###") {
		t.Fatalf("0.18.0 section looks wrong:\n%s", s)
	}
}

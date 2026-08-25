package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docSection is what four contract tests use to say a key must be documented
// *here* rather than anywhere in the file, so how far it reaches is the whole
// value of those tests. It cut at the next heading of the same level only, so a
// `###` section ran past the `##` that ends it: on today's json-output.md
// "### The session object" is the last `###` in the file and its section was
// 374 of 505 lines, through five later commands' output (#1951).
func TestASectionStopsAtTheHeadingThatEndsIt(t *testing.T) {
	doc := strings.Join([]string{
		"# Contract",
		"",
		"## `deja search --json`",
		"",
		"Search says `hits`.",
		"",
		"### The session object",
		"",
		"The session object says `id`.",
		"",
		"## `deja blame --json`",
		"",
		"Blame says `occurrences`.",
		"",
	}, "\n")

	nested := docSection(t, doc, "### The session object")
	if !strings.Contains(nested, "`id`") {
		t.Errorf("the section lost its own content: %q", nested)
	}
	if strings.Contains(nested, "occurrences") {
		t.Errorf("a `###` section reached past the `##` that ends it, into a later command: %q", nested)
	}

	// The parent still holds what is nested under it.
	parent := docSection(t, doc, "## `deja search --json`")
	if !strings.Contains(parent, "`hits`") || !strings.Contains(parent, "`id`") {
		t.Errorf("the parent section dropped its own subsection: %q", parent)
	}
	if strings.Contains(parent, "occurrences") {
		t.Errorf("the parent section reached into the next command: %q", parent)
	}
}

// A heading with nothing under it takes nothing from the section after it. The
// cut used to be skipped when the next heading came first thing, which handed
// back the rest of the file for the emptiest section there is.
func TestAnEmptySectionTakesNothingFromTheNextOne(t *testing.T) {
	doc := "## Empty\n## `deja blame --json`\n\nBlame says `occurrences`.\n"

	if got := docSection(t, doc, "## Empty"); strings.Contains(got, "occurrences") {
		t.Errorf("an empty section swallowed the one after it: %q", got)
	}
}

// Prose that names a heading is not the heading. The match was on the raw text
// anywhere, so a sentence pointing at a section started the section there — mid
// paragraph, and short by everything up to the real heading.
func TestASentenceNamingASectionIsNotTheSection(t *testing.T) {
	doc := "# Contract\n\nSee ## `deja last --json` for the shape.\n\n## `deja last --json`\n\nLast says `id`.\n"

	if got := docSection(t, doc, "## `deja last --json`"); !strings.Contains(got, "`id`") {
		t.Errorf("the section started at a sentence about it, not at the heading: %q", got)
	}
}

// The reach measured on the real document, not a fixture: the session object is
// a subsection of `deja search --json`, so it cannot outlive it.
func TestTheSessionObjectSectionIsNotMostOfTheDocument(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "json-output.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)

	sec := docSection(t, doc, "### The session object")
	if strings.Contains(sec, "## `deja last --json`") {
		t.Errorf("the session-object section runs past `deja search --json` into the commands after it")
	}
	if lines, total := strings.Count(sec, "\n"), strings.Count(doc, "\n"); lines*2 > total {
		t.Errorf("the session-object section is %d of %d lines, which is not a section", lines, total)
	}
}

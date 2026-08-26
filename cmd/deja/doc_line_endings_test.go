package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Windows checkout with the default core.autocrlf hands every text file back
// with CRLF, and the doc-contract tests read a heading as a whole line: with a
// trailing carriage return the line never matches, so eleven of them failed at
// once and none of them said why (#2081).
func TestDocSectionFindsAHeadingInACRLFDocument(t *testing.T) {
	doc := "# Title\r\n\r\n## `deja stats --json`\r\n\r\nseven calendar days.\r\n\r\n## Next\r\n\r\nother.\r\n"
	got := docSection(t, doc, "## `deja stats --json`")
	if !strings.Contains(got, "seven calendar days") {
		t.Errorf("the section is empty or wrong: %q", got)
	}
	if strings.Contains(got, "other.") {
		t.Errorf("the section ran past its heading: %q", got)
	}
}

// And the checked-out file itself: `.gitattributes` keeps docs at LF, so a
// clone on any platform gives these tests the same bytes. Without it the failure
// lands eleven tests away from its cause.
func TestTheDocsAreCheckedOutWithUnixLineEndings(t *testing.T) {
	// Every text file under docs, not the two these tests happen to read: the
	// attribute covers the tree, and the next test to parse a page by line
	// should not have to rediscover this.
	texty := map[string]bool{".md": true, ".html": true, ".xml": true, ".json": true, ".css": true, ".js": true, ".txt": true, ".svg": true}
	checked := 0
	root := filepath.Join("..", "..", "docs")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !texty[strings.ToLower(filepath.Ext(p))] {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked++
		if n := strings.Count(string(b), "\r"); n > 0 {
			rel, _ := filepath.Rel(root, p)
			t.Errorf("docs/%s holds %d carriage returns — check .gitattributes", filepath.ToSlash(rel), n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no text files found under docs, so this pins nothing")
	}
}

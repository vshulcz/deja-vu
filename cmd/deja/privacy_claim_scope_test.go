package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// "nothing leaves the machine" is the claim this project is judged on, and the
// documents that make it have four network paths to account for: `deja
// update`, the doctor version check, `deja sync ssh` and `deja embed`. The
// security model has listed all four since it was written; three shorter
// documents said the absolute version instead — SECURITY.md, the two plugin
// security notes, the getting-started page and the MCPB store listing, each
// found on a different day.
//
// The rule is not "never say it": it is that wherever the sentence appears it
// either carries its exception or names what it is about. A privacy claim
// wider than the code is the one kind of wrong sentence that costs more than
// the feature it describes.
var (
	htmlTag   = regexp.MustCompile(`<[^>]*>`)
	whitespce = regexp.MustCompile(`\s+`)
)

// proseOf drops markup and line breaks so a sentence reads as a reader sees
// it: the claim on the front page carries its exception in an <em> on the next
// line, and a claim in a meta description has no neighbours at all.
func proseOf(text string) string {
	return whitespce.ReplaceAllString(htmlTag.ReplaceAllString(text, " "), " ")
}

// sentenceAround returns the sentence holding prose[from:to].
func sentenceAround(prose string, from, to int) string {
	stop := func(c byte) bool { return c == '.' || c == '!' || c == '?' || c == ';' }
	start := from
	for start > 0 && !stop(prose[start-1]) {
		start--
	}
	end := to
	for end < len(prose) && !stop(prose[end]) {
		end++
	}
	// The CJK full stop is three bytes, so it is matched as a string.
	s := prose[start:end]
	if i := strings.LastIndex(s[:from-start], "。"); i >= 0 {
		s = s[i+len("。"):]
	}
	if i := strings.Index(s, "。"); i >= 0 {
		s = s[:i]
	}
	return s
}

func TestEveryNothingLeavesClaimCarriesItsScope(t *testing.T) {
	root := filepath.Join("..", "..")
	claim := regexp.MustCompile(`(?i)(nothing|nothing else|no data) (ever )?leaves (your|the|this) (machine|laptop|computer)`)
	// What makes the sentence true: a stated exception, or a subject narrow
	// enough to be accurate on its own.
	scopes := []string{
		"unless you ask", "unless asked", "except", "exception",
		"no network path", "indexing and search", "index and its usage sidecar",
		"without being asked", "the file never leaves",
	}
	var checked int
	err := filepath.WalkDir(filepath.Join(root), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".claude", "fixtures", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".md", ".html", ".txt", ".json":
		default:
			return nil
		}
		if strings.Contains(p, "CHANGELOG.md") {
			return nil // a record of what was said, not a claim being made
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		text := proseOf(string(b))
		for _, m := range claim.FindAllStringIndex(text, -1) {
			checked++
			// The sentence it sits in, and only that: a neighbouring
			// sentence about something else passed this once — "recall
			// arrives without being asked", two lines below the claim in
			// docs/llms.txt, read as the exception the claim did not have.
			window := strings.ToLower(sentenceAround(text, m[0], m[1]))
			ok := false
			for _, s := range scopes {
				if strings.Contains(window, s) {
					ok = true
					break
				}
			}
			if !ok {
				rel, _ := filepath.Rel(root, p)
				t.Errorf("%s claims nothing leaves the machine with no exception and no subject: %q",
					filepath.ToSlash(rel), strings.TrimSpace(text[m[0]:m[1]]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Low on purpose: most of these sentences now say what reaches the network
	// instead, so the ones left are the front page's and a handful in prose.
	// The guard is only here to catch the pattern rotting into matching
	// nothing at all.
	if checked < 2 {
		t.Fatalf("found %d such claims — the pattern stopped matching, so this checks nothing", checked)
	}
}

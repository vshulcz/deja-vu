package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// lastmod is what a crawler schedules the next fetch from, so a date that stops
// following the file turns into a page nobody re-reads. It used to be written
// once, when the entry first appeared, and 102 of the 107 URLs advertised a day
// up to nine weeks before their page last changed. `go run ./scripts/genregistry`
// takes the date from git now, and this holds it there.
//
// The comparison allows a week of slack: a squash merge re-dates the commit that
// carries the page, so an exact match would fail on main the day after a docs
// change lands.
func TestSitemapLastmodFollowsThePages(t *testing.T) {
	root := filepath.Join("..", "..")
	if out, err := exec.Command("git", "-C", root, "rev-parse", "--is-shallow-repository").Output(); err != nil {
		t.Skipf("no git history to compare against: %v", err)
	} else if strings.TrimSpace(string(out)) == "true" {
		t.Skip("shallow clone: every file looks like it changed in the tip commit")
	}

	b, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	entry := regexp.MustCompile(`<loc>https://vshulcz\.github\.io/deja-vu/([^<]*)</loc>(?:<lastmod>([^<]*)</lastmod>)?`)
	found := entry.FindAllStringSubmatch(string(b), -1)
	if len(found) < 100 {
		t.Fatalf("only %d URLs matched in docs/sitemap.xml; the pattern is wrong", len(found))
	}

	const slack = 7 * 24 * time.Hour
	for _, m := range found {
		loc, lastmod := m[1], m[2]
		named := loc
		if named == "" {
			named = "/"
		}
		served := loc
		if served == "" || strings.HasSuffix(served, "/") {
			served += "index.html"
		}
		// git wants forward slashes in a pathspec on every platform; only the
		// stat below goes through the OS separator.
		rel := "docs/" + served
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			continue // served from somewhere this test cannot see
		}
		if lastmod == "" {
			t.Errorf("%s has no lastmod — run `go run ./scripts/genregistry`", named)
			continue
		}
		said, err := time.Parse("2006-01-02", lastmod)
		if err != nil {
			t.Errorf("%s has lastmod %q, which is not a date", named, lastmod)
			continue
		}
		out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%as", "--", rel).Output()
		if err != nil {
			t.Fatalf("git log %s: %v", loc, err)
		}
		day := strings.TrimSpace(string(out))
		if day == "" {
			continue // not committed yet
		}
		changed, err := time.Parse("2006-01-02", day)
		if err != nil {
			t.Fatalf("git returned %q for %s", day, loc)
		}
		if changed.Sub(said) > slack {
			t.Errorf("%s says lastmod %s but last changed %s — run `go run ./scripts/genregistry`", named, lastmod, day)
		}
	}
}

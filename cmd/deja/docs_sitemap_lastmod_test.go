package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/docsdate"
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

	dater, err := docsdate.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dater.Close() }()

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
		// The day the page's content last changed, the way genregistry dates
		// it: a commit that only rewrote the sidebar does not count.
		day, err := dater.Day(rel)
		if err != nil {
			t.Fatalf("dating %s: %v", loc, err)
		}
		if day == "" {
			continue
		}
		changed, err := time.Parse("2006-01-02", day)
		if err != nil {
			t.Fatalf("docsdate returned %q for %s", day, loc)
		}
		// Both directions: a date behind the file is the drift this catches, and
		// a date ahead of it is the same lie told the other way round — a page
		// that says it changed tomorrow is one a crawler is told to come back
		// for and finds nothing new.
		if drift := changed.Sub(said); drift > slack || drift < -slack {
			t.Errorf("%s says lastmod %s but last changed %s — run `go run ./scripts/genregistry`", named, lastmod, day)
		}
	}
}

// A URL listed twice is two entries that can disagree about the page, and
// merges that both added the same pages left nineteen of them that way.
func TestSitemapListsEachPageOnce(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, m := range regexp.MustCompile(`<loc>([^<]+)</loc>`).FindAllStringSubmatch(string(b), -1) {
		seen[m[1]]++
	}
	for loc, n := range seen {
		if n > 1 {
			t.Errorf("%s is in docs/sitemap.xml %d times — run `go run ./scripts/genregistry`", loc, n)
		}
	}
}

// The page's structured data and the sitemap answer the same question, so
// they give the same day; dateModified used to be typed once and left there.
func TestGuideDateModifiedIsTheSitemapDay(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	lastmod := map[string]string{}
	entry := regexp.MustCompile(`<loc>https://vshulcz\.github\.io/deja-vu/([^<]*)</loc><lastmod>([^<]*)</lastmod>`)
	for _, m := range entry.FindAllStringSubmatch(string(b), -1) {
		lastmod[m[1]] = m[2]
	}
	modified := regexp.MustCompile(`"dateModified": ?"([^"]*)"`)
	published := regexp.MustCompile(`"datePublished": ?"([^"]*)"`)
	for _, glob := range []string{"guide/*.html", "zh/guide/*.html"} {
		pages, err := filepath.Glob(filepath.Join(root, "docs", filepath.FromSlash(glob)))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pages {
			page, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			m := modified.FindSubmatch(page)
			if m == nil {
				continue
			}
			rel, _ := filepath.Rel(filepath.Join(root, "docs"), p)
			rel = filepath.ToSlash(rel)
			want, ok := lastmod[rel]
			if !ok {
				continue
			}
			if pub := published.FindSubmatch(page); pub != nil && string(pub[1]) > want {
				want = string(pub[1])
			}
			if got := string(m[1]); got != want {
				t.Errorf("%s: dateModified %s, sitemap lastmod %s — run `go run ./scripts/genregistry`", rel, got, want)
			}
		}
	}
}

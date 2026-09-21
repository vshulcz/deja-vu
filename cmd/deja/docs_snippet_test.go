package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// What a result page shows is not what the page says: a title is cut around 60
// characters and a description around 160, and the " — deja-vu" suffix spends
// part of that budget. Every per-agent page once carried the same 78-95
// character title — "X memory: recall of past sessions, from every agent on the
// machine" — so the clause that says what the page is for was the clause that
// got cut, on twenty-two pages at once.
//
// The second rule is that a page tells a crawler one title, not two: sixteen of
// them had an og:title that said something else.
const (
	titleBudget = 60
	descBudget  = 160
	siteSuffix  = " — deja-vu"
)

var (
	titleRE = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	descRE  = regexp.MustCompile(`(?s)<meta name="description" content="(.*?)">`)
	ogTRE   = regexp.MustCompile(`(?s)<meta property="og:title" content="(.*?)">`)
	ogDRE   = regexp.MustCompile(`(?s)<meta property="og:description" content="(.*?)">`)
	twTRE   = regexp.MustCompile(`(?s)<meta name="twitter:title" content="(.*?)">`)
	twDRE   = regexp.MustCompile(`(?s)<meta name="twitter:description" content="(.*?)">`)
)

func TestGuideSnippetsFitWhatSearchShows(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "guide", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 40 {
		t.Fatalf("found %d guide pages, expected the whole guide", len(pages))
	}

	// Third rule: a title belongs to one page. getting-started.html and
	// search.html both said "Search your Claude Code and Codex history", so
	// one of them was named after the other's subject — and two pages under
	// one title is what a doorway page looks like from outside.
	titleOwner := map[string]string{}

	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		name := filepath.Base(p)

		title := first(titleRE, s)
		desc := first(descRE, s)
		if title == "" || desc == "" {
			t.Errorf("%s: no title or no description", name)
			continue
		}
		if n := utf8.RuneCountInString(title); n > titleBudget {
			t.Errorf("%s: title is %d characters, over %d — search cuts it: %q",
				name, n, titleBudget, title)
		}
		if n := utf8.RuneCountInString(desc); n > descBudget {
			t.Errorf("%s: description is %d characters, over %d", name, n, descBudget)
		}

		if other, ok := titleOwner[title]; ok {
			t.Errorf("%s and %s share the title %q — one of them is named after the other's subject",
				other, name, title)
		} else {
			titleOwner[title] = name
		}

		// og:title may carry the title with or without the site suffix; what it
		// may not do is name a different page.
		stem := strings.TrimSuffix(title, siteSuffix)
		for _, got := range []struct{ what, value string }{
			{"og:title", first(ogTRE, s)},
			{"twitter:title", first(twTRE, s)},
		} {
			if got.value != "" && got.value != stem && got.value != title {
				t.Errorf("%s: %s says %q, the title says %q", name, got.what, got.value, stem)
			}
		}
		for _, got := range []struct{ what, value string }{
			{"og:description", first(ogDRE, s)},
			{"twitter:description", first(twDRE, s)},
		} {
			if got.value != "" && got.value != desc {
				t.Errorf("%s: %s does not match the description", name, got.what)
			}
		}
	}
}

// A result page shows a date for a how-to, and a reader choosing between
// results uses it; none of the fifty guide pages carried one, so there was
// nothing to show. And the site declared no name of its own, which is why a
// result was labelled vshulcz.github.io — somebody's personal page rather than
// the project's documentation.
func TestGuidePagesSayWhenAndWhoseTheyAre(t *testing.T) {
	root := filepath.Join("..", "..")
	pages, err := filepath.Glob(filepath.Join(root, "docs", "guide", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	date := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(p)
		for _, field := range []string{"datePublished", "dateModified"} {
			m := regexp.MustCompile(`"` + field + `": ?"([^"]*)"`).FindSubmatch(b)
			if m == nil {
				t.Errorf("%s has no %s, so search has no date to show", name, field)
				continue
			}
			got := string(m[1])
			if !date.MatchString(got) {
				t.Errorf("%s: %s is %q, want YYYY-MM-DD", name, field, got)
			}
			if got > today {
				t.Errorf("%s: %s is %s, which is in the future", name, field, got)
			}
		}
	}

	home, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"@type": "WebSite"`, `"name": "deja-vu"`,
		`"url": "https://vshulcz.github.io/deja-vu/"`} {
		if !strings.Contains(string(home), want) {
			t.Errorf("docs/index.html does not declare %s", want)
		}
	}
}

func first(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

func first(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docs/feed.xml is what WebSub publishes: Google takes no ping and no IndexNow,
// and the feed is the only push it documents. A feed that disagrees with the
// sitemap, or names a page that is not served, announces a 404 — and the failure
// is silent, because a hub reports success for fetching the feed either way.
func TestFeedAgreesWithTheSitemap(t *testing.T) {
	root := filepath.Join("..", "..")

	var sm struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			Lastmod string `xml:"lastmod"`
		} `xml:"url"`
	}
	read(t, filepath.Join(root, "docs", "sitemap.xml"), &sm)

	var feed struct {
		Links []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Updated string `xml:"updated"`
		Entries []struct {
			Title   string `xml:"title"`
			ID      string `xml:"id"`
			Updated string `xml:"updated"`
		} `xml:"entry"`
	}
	read(t, filepath.Join(root, "docs", "feed.xml"), &feed)

	if len(feed.Entries) == 0 {
		t.Fatal("docs/feed.xml has no entries — run `go run ./scripts/genfeed`")
	}

	// A hub needs both: rel=self to know what it is fetching, rel=hub to be
	// the hub at all. Without either, publishing is a no-op that reports success.
	rel := map[string]string{}
	for _, l := range feed.Links {
		rel[l.Rel] = l.Href
	}
	if rel["self"] != "https://vshulcz.github.io/deja-vu/feed.xml" {
		t.Errorf("rel=self is %q, and a hub fetches that URL", rel["self"])
	}
	if rel["hub"] == "" {
		t.Error("the feed names no hub, so nothing can be published from it")
	}

	day := map[string]string{}
	for _, u := range sm.URLs {
		day[strings.TrimSpace(u.Loc)] = strings.TrimSpace(u.Lastmod)
	}
	for _, e := range feed.Entries {
		want, listed := day[e.ID]
		if !listed {
			t.Errorf("%s is in the feed and not in the sitemap, so it may not be served at all", e.ID)
			continue
		}
		if e.Updated != want+"T00:00:00Z" {
			t.Errorf("%s is dated %s in the feed and %s in the sitemap", e.ID, e.Updated, want)
		}
		if e.Title == "" {
			t.Errorf("%s has no title in the feed", e.ID)
		}
		page := strings.TrimPrefix(e.ID, "https://vshulcz.github.io/deja-vu/")
		if page == "" || strings.HasSuffix(page, "/") {
			page += "index.html"
		}
		if _, err := os.Stat(filepath.Join(root, "docs", filepath.FromSlash(page))); err != nil {
			t.Errorf("%s is in the feed and docs/%s is not there", e.ID, page)
		}
	}

	// Newest first is the whole point of the feed; a subscriber reads the top.
	for i := 1; i < len(feed.Entries); i++ {
		if feed.Entries[i-1].Updated < feed.Entries[i].Updated {
			t.Errorf("entry %d is older than the one after it — run `go run ./scripts/genfeed`", i-1)
			break
		}
	}
	if feed.Updated != feed.Entries[0].Updated {
		t.Errorf("the feed says it changed %s and its newest entry says %s", feed.Updated, feed.Entries[0].Updated)
	}

	// The page has to point at the feed or nothing discovers it.
	b, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `type="application/atom+xml"`) {
		t.Error("docs/index.html does not link the feed, so only something told about it finds it")
	}
}

func read(t *testing.T, path string, into any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(b, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// The check above runs feed → sitemap, so a feed missing a page passes it: the
// entries it does carry are all fine. That is how docs/feed.xml went a release
// without a page the sitemap had listed for two days, while still naming a page
// that had been deleted — nothing read the sitemap side. This runs the other
// direction, which is genfeed's whole contract: the feed is the newest forty
// pages the repository serves, newest first, ties broken by URL.
func TestTheFeedIsTheNewestPagesTheSitemapLists(t *testing.T) {
	root := filepath.Join("..", "..")

	var sm struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			Lastmod string `xml:"lastmod"`
		} `xml:"url"`
	}
	read(t, filepath.Join(root, "docs", "sitemap.xml"), &sm)

	type page struct{ loc, day string }
	var served []page
	for _, u := range sm.URLs {
		loc, day := strings.TrimSpace(u.Loc), strings.TrimSpace(u.Lastmod)
		if loc == "" || day == "" {
			continue
		}
		// genfeed keeps a URL only when the file behind it is a page with a
		// title; a redirect stub or a URL with nothing on disk is not one.
		rel := strings.TrimPrefix(loc, "https://vshulcz.github.io/deja-vu/")
		if rel == "" || strings.HasSuffix(rel, "/") {
			rel += "index.html"
		}
		b, err := os.ReadFile(filepath.Join(root, "docs", filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		// The same test genfeed applies: the title's content, not the tag.
		// An empty <title></title> is a page it drops and this would keep.
		m := feedTitle.FindSubmatch(b)
		if m == nil || strings.TrimSpace(string(m[1])) == "" {
			continue
		}
		served = append(served, page{loc, day})
	}
	sort.SliceStable(served, func(i, j int) bool {
		if served[i].day != served[j].day {
			return served[i].day > served[j].day
		}
		return served[i].loc < served[j].loc
	})
	const entries = 40
	if len(served) > entries {
		served = served[:entries]
	}

	var feed struct {
		Entries []struct {
			ID string `xml:"id"`
		} `xml:"entry"`
	}
	read(t, filepath.Join(root, "docs", "feed.xml"), &feed)

	if len(feed.Entries) != len(served) {
		t.Fatalf("the feed carries %d entries and the sitemap serves %d — run `go run ./scripts/genfeed`",
			len(feed.Entries), len(served))
	}
	for i, want := range served {
		if got := feed.Entries[i].ID; got != want.loc {
			t.Errorf("entry %d is %s and the sitemap puts %s (%s) there — run `go run ./scripts/genfeed`",
				i, got, want.loc, want.day)
		}
	}
}

// feedTitle is scripts/genfeed's own title expression.
var feedTitle = regexp.MustCompile(`(?s)<title>(.*?)</title>`)

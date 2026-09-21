// Command genfeed writes docs/feed.xml: the docs pages that changed, newest
// first, as Atom.
//
// It exists for one mechanism. Google takes no ping and no IndexNow, and of the
// four ways it documents for hearing that something changed, the only one that
// is a push rather than a wait is WebSub over an Atom or RSS feed. The feed
// names a hub; publishing to that hub is what tells anything subscribed. It is a
// hint, not a guarantee, and it is also a feed a person can follow.
//
// The sitemap is the source: it already holds the URL set and the day each page
// last changed, so the feed cannot disagree with it.
//
// Run from the repository root:
//
//	go run ./scripts/genfeed
package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	site    = "https://vshulcz.github.io/deja-vu"
	feedURL = site + "/feed.xml"
	// The hub Google's own documentation points WebSub publishers at.
	hub = "https://pubsubhubbub.appspot.com/"
	// A feed of everything is a second sitemap. This one answers "what changed
	// lately", which is the question a subscriber has.
	entries = 40
)

type sitemap struct {
	URLs []struct {
		Loc     string `xml:"loc"`
		Lastmod string `xml:"lastmod"`
	} `xml:"url"`
}

type link struct {
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type entry struct {
	Title   string `xml:"title"`
	ID      string `xml:"id"`
	Link    link   `xml:"link"`
	Updated string `xml:"updated"`
	Summary string `xml:"summary,omitempty"`
}

type feed struct {
	XMLName  xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	Title    string   `xml:"title"`
	Subtitle string   `xml:"subtitle"`
	ID       string   `xml:"id"`
	Links    []link   `xml:"link"`
	Updated  string   `xml:"updated"`
	Author   struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Entries []entry `xml:"entry"`
}

var (
	titleRe = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	descRe  = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genfeed:", err)
		os.Exit(1)
	}
}

func run() error {
	b, err := os.ReadFile(filepath.Join("docs", "sitemap.xml"))
	if err != nil {
		return err
	}
	var sm sitemap
	if err := xml.Unmarshal(b, &sm); err != nil {
		return fmt.Errorf("docs/sitemap.xml: %w", err)
	}

	var all []entry
	for _, u := range sm.URLs {
		loc := strings.TrimSpace(u.Loc)
		day := strings.TrimSpace(u.Lastmod)
		if loc == "" || day == "" {
			continue
		}
		title, summary, err := pageText(loc)
		if err != nil {
			return err
		}
		if title == "" {
			continue // not a page this repository serves
		}
		all = append(all, entry{
			Title:   title,
			ID:      loc,
			Link:    link{Rel: "alternate", Type: "text/html", Href: loc},
			Updated: day + "T00:00:00Z",
			Summary: summary,
		})
	}
	if len(all) == 0 {
		return fmt.Errorf("docs/sitemap.xml named no pages")
	}
	// Newest first, and by URL inside a day so a run twice over the same tree
	// writes the same file.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Updated != all[j].Updated {
			return all[i].Updated > all[j].Updated
		}
		return all[i].ID < all[j].ID
	})
	if len(all) > entries {
		all = all[:entries]
	}

	f := feed{
		Title:    "deja-vu docs",
		Subtitle: "Pages of the deja-vu documentation that changed, newest first.",
		ID:       site + "/",
		Links: []link{
			{Rel: "alternate", Type: "text/html", Href: site + "/"},
			{Rel: "self", Type: "application/atom+xml", Href: feedURL},
			{Rel: "hub", Href: hub},
		},
		Updated: all[0].Updated,
		Entries: all,
	}
	f.Author.Name = "vshulcz"

	out, err := xml.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	out = append([]byte(xml.Header), out...)
	out = append(out, '\n')
	if err := os.WriteFile(filepath.Join("docs", "feed.xml"), out, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote docs/feed.xml with %d of %d pages\n", len(all), len(sm.URLs))
	return nil
}

// pageText reads the title and description straight out of the page, so the feed
// says what the page says rather than a second copy that drifts from it.
func pageText(loc string) (title, summary string, err error) {
	rel := strings.TrimPrefix(loc, site+"/")
	if rel == "" || strings.HasSuffix(rel, "/") {
		rel += "index.html"
	}
	b, err := os.ReadFile(filepath.Join("docs", filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}
	if m := titleRe.FindSubmatch(b); m != nil {
		title = unescape(strings.TrimSpace(string(m[1])))
	}
	if m := descRe.FindSubmatch(b); m != nil {
		summary = unescape(strings.TrimSpace(string(m[1])))
	}
	return title, summary, nil
}

// unescape turns the entities the pages are written with back into text; the
// marshaller escapes what it writes, and leaving them would double-escape.
func unescape(s string) string {
	for from, to := range map[string]string{"&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": `"`, "&#39;": "'", "&middot;": "·", "&larr;": "←", "&nbsp;": " "} {
		s = strings.ReplaceAll(s, from, to)
	}
	return s
}

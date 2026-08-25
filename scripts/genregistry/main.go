// Command genregistry renders the session-format registry into pages a search
// engine can read.
//
// The registry documents where eighteen coding agents keep their history and
// what is in those files. That is the one question people put to a search
// engine in their own words — "where does Claude Code store conversations" —
// and the answer sat in docs/registry as raw .md, which GitHub Pages serves as
// text/markdown: no title, no description, not in the sitemap, invisible.
//
// Run from the repository root:
//
//	go run ./scripts/genregistry
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const site = "https://vshulcz.github.io/deja-vu"

type registry struct {
	Harnesses []struct {
		ID           string   `json:"id"`
		DisplayName  string   `json:"display_name"`
		StorePaths   []string `json:"store_paths"`
		FormatKind   string   `json:"format_kind"`
		LastVerified string   `json:"last_verified"`
	} `json:"harnesses"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	b, err := os.ReadFile(filepath.Join("docs", "registry", "registry.json"))
	if err != nil {
		return err
	}
	var reg registry
	if err := json.Unmarshal(b, &reg); err != nil {
		return err
	}
	name := map[string]string{}
	verified := map[string]string{}
	for _, h := range reg.Harnesses {
		name[h.ID] = h.DisplayName
		verified[h.ID] = h.LastVerified
	}
	// One page is filed under a different name than its registry entry.
	// Everything else matches, and the check below keeps it that way.
	name["claude-code"] = name["claude"]
	verified["claude-code"] = verified["claude"]

	paths, err := filepath.Glob(filepath.Join("docs", "registry", "*.md"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	// Every page lists every other one. This is the reason the registry is
	// worth publishing at all: a page per format that answers "where does this
	// agent keep its history" are each other's best route in, for a reader
	// and for a crawler that would otherwise reach none of them.
	var links []link
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		if id == "README" {
			continue
		}
		label := name[id]
		if label == "" {
			label = id
		}
		links = append(links, link{Slug: id, Label: label})
	}
	// Case-insensitively: "aider", "opencode" and "pi" are lower-case names,
	// and a byte sort files them below "Zed", which is not where a reader
	// looks for them.
	sort.Slice(links, func(i, j int) bool {
		return strings.ToLower(links[i].Label) < strings.ToLower(links[j].Label)
	})
	var written []string
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		body, h1 := renderMarkdown(string(src))
		if h1 == "" {
			return fmt.Errorf("%s has no level-one heading, so the page would have no name", path)
		}
		// A page whose id is not in the registry would be titled "Where
		// some-id stores its history", which is not what anyone types and not
		// what the harness is called. Better to stop than to publish it.
		if id != "README" && name[id] == "" {
			return fmt.Errorf("%s has no display_name in registry.json — add the harness there, or the page ships with a raw id in its title", path)
		}
		page := pageFor(id, name[id], h1)
		out := filepath.Join("docs", "registry", id+".html")
		if err := os.WriteFile(out, []byte(render(page, body, links)), 0o644); err != nil {
			return err
		}
		written = append(written, "registry/"+id+".html")
	}
	if err := writeSitemap(written, verified); err != nil {
		return err
	}
	fmt.Printf("wrote %d registry pages and the sitemap\n", len(written))
	return nil
}

type meta struct{ Title, Description, Heading, Slug string }

type link struct{ Slug, Label string }

// sidebar is the same shape the guide pages use: .doc is a two-column grid of
// aside and article, and content placed directly in it becomes grid cells that
// overlap.
func sidebar(current string, others []link) string {
	var b strings.Builder
	b.WriteString(`<aside><a href="../">&larr; deja-vu</a>`)
	b.WriteString(`<div class="grp">Guide</div>`)
	b.WriteString(`<a href="../guide/getting-started.html">Getting started</a>`)
	b.WriteString(`<a href="../guide/harnesses.html">Harnesses</a>`)
	b.WriteString(`<div class="grp">Session formats</div>`)
	b.WriteString(`<a href="README.html"`)
	if current == "README" {
		b.WriteString(` aria-current="page"`)
	}
	b.WriteString(`>All formats</a>`)
	for _, o := range others {
		b.WriteString(`<a href="` + o.Slug + `.html"`)
		if o.Slug == current {
			b.WriteString(` aria-current="page"`)
		}
		b.WriteString(`>` + html.EscapeString(o.Label) + `</a>`)
	}
	b.WriteString(`</aside>`)
	return b.String()
}

// pageFor writes the title and description in the words someone would type,
// not the words the project uses about itself. "Claude Code session format" is
// what this page is; "where Claude Code stores its history" is what is being
// looked for, and both belong in the title.
func pageFor(id, display, h1 string) meta {
	if display == "" {
		display = id
	}
	if id == "README" {
		return meta{
			Title:       "Where coding agents store their history — session format registry",
			Description: "Where Claude Code, Codex, Cursor, opencode and fourteen more coding agents keep their conversation history on disk, what is inside those files, and how each format drifts.",
			Heading:     h1,
			Slug:        "README",
		}
	}
	return meta{
		Title:       fmt.Sprintf("Where %s stores its history — session format", display),
		Description: fmt.Sprintf("Where %s writes its conversation history on disk, what the files contain, the quirks deja found in them, and when the format was last verified against a real store.", display),
		Heading:     h1,
		Slug:        id,
	}
}

// pageTemplate is rendered by html/template, which escapes per context: an
// attribute, an element body and the inside of a <script> are three different
// escapes, and getting that right by hand is how a heading from a markdown
// file ends up closing the script element on every page the generator writes.
var pageTemplate = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en" class="no-js">
<head>
<script>document.documentElement.className=document.documentElement.className.replace("no-js","js")</script>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="{{.Description}}">
<link rel="canonical" href="{{.URL}}">
<meta property="og:type" content="article">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta property="og:url" content="{{.URL}}">
<meta property="og:image" content="{{.Site}}/assets/og.png">
<meta name="twitter:card" content="summary_large_image">
<link rel="icon" type="image/svg+xml" sizes="16x16" href="../assets/favicon.svg">
<link rel="icon" type="image/svg+xml" sizes="any" href="../assets/icon.svg">
<link rel="stylesheet" href="../assets/site.css">
<meta name="author" content="vshulcz">
<meta name="robots" content="index,follow,max-image-preview:large,max-snippet:-1">
<meta name="theme-color" content="#0d0b16">
<meta name="color-scheme" content="dark light">
<meta property="og:site_name" content="deja-vu">
<meta name="twitter:title" content="{{.Title}}">
<meta name="twitter:description" content="{{.Description}}">
<meta name="twitter:image" content="{{.Site}}/assets/og.png">
<link rel="apple-touch-icon" href="../assets/icon.svg">
<link rel="sitemap" type="application/xml" href="{{.Site}}/sitemap.xml">
<link rel="describedby" type="text/markdown" href="{{.Site}}/llms.txt">
<script type="application/ld+json">{{.Article}}</script>
<script type="application/ld+json">{{.Crumbs}}</script>
</head>
<body>
<div class="glow"></div>
<nav><a class="brand" href="../"><img src="../assets/icon.svg" alt="" width="22" height="22">deja-vu</a><span class="spacer"></span><a class="lnk" href="../">Home</a><a class="lnk" href="../guide/getting-started.html">Docs</a><a class="lnk" href="https://github.com/vshulcz/deja-vu">GitHub</a></nav>
<div class="wrap"><div class="doc">
{{.Sidebar}}
<article>
<h1>{{.Heading}}</h1>
{{.Body}}
<hr>
<p>deja reads this format and {{.Others}} others, and turns what it finds into memory your
agents can search. See the <a href="../guide/harnesses.html">harness matrix</a> for what is
wired where, or <a href="../guide/getting-started.html">install it</a> and search your own
history.</p>
</article>
</div></div>
</body>
</html>
`))

type pageData struct {
	Title, Description, Heading, URL, Site string
	// Others is how many other formats deja reads, spelled out. It was a word
	// typed into the template, so every one of these twenty pages kept saying
	// seventeen after three harnesses landed.
	Others          string
	Article, Crumbs any
	Sidebar, Body   template.HTML
}

// countWord spells a small number the way the pages read it.
func countWord(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven",
		"eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
		"sixteen", "seventeen", "eighteen", "nineteen", "twenty", "twenty-one",
		"twenty-two", "twenty-three", "twenty-four", "twenty-five"}
	if n < 0 || n >= len(words) {
		return fmt.Sprint(n)
	}
	return words[n]
}

func render(m meta, body string, others []link) string {
	url := site + "/registry/" + m.Slug + ".html"
	article := map[string]any{
		"@context": "https://schema.org", "@type": "TechArticle",
		"headline": m.Heading, "description": m.Description, "url": url,
		"inLanguage": "en",
		"author":     map[string]string{"@type": "Person", "name": "vshulcz"},
		"publisher":  map[string]string{"@type": "Person", "name": "vshulcz"},
		"isPartOf":   map[string]string{"@type": "WebSite", "name": "deja-vu", "url": site + "/"},
	}
	crumbs := map[string]any{
		"@context": "https://schema.org", "@type": "BreadcrumbList",
		"itemListElement": []any{
			map[string]any{"@type": "ListItem", "position": 1, "name": "deja-vu", "item": site + "/"},
			map[string]any{"@type": "ListItem", "position": 2, "name": "Harnesses", "item": site + "/guide/harnesses.html"},
			map[string]any{"@type": "ListItem", "position": 3, "name": m.Heading, "item": url},
		},
	}
	var b strings.Builder
	err := pageTemplate.Execute(&b, pageData{
		Title: m.Title, Description: m.Description, Heading: m.Heading,
		URL: url, Site: site, Article: article, Crumbs: crumbs,
		// Every page but this one. The list is one page per harness format —
		// README is skipped when it is built — so this is the count the
		// sentence means.
		Others: countWord(len(others) - 1),
		// The body is HTML this generator built from markdown it escaped on
		// the way through; the sidebar is built from registry names, escaped
		// there. Both are already safe, which is what template.HTML asserts.
		Sidebar: template.HTML(sidebar(m.Slug, others)),
		Body:    template.HTML(body),
	})
	if err != nil {
		panic(err)
	}
	return b.String()
}

// writeSitemap adds the registry pages to the sitemap, keeping every entry
// already in it as it stands. The lastmod and priority on the existing pages
// are hand-set; regenerating the file would quietly drop them.
func writeSitemap(registryPages []string, verified map[string]string) error {
	path := filepath.Join("docs", "sitemap.xml")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(b)
	var add strings.Builder
	for _, p := range registryPages {
		loc := site + "/" + p
		if strings.Contains(text, "<loc>"+loc+"</loc>") {
			continue
		}
		// A format page changes when the harness changes its files, which is
		// rare and unannounced; monthly is what the guide pages use. The date
		// is the registry's own last_verified — the day someone last checked
		// that page against a real store, which is what lastmod means.
		id := strings.TrimSuffix(strings.TrimPrefix(p, "registry/"), ".html")
		lastmod := ""
		if d := verified[id]; d != "" {
			lastmod = "<lastmod>" + d + "</lastmod>"
		}
		add.WriteString("  <url><loc>" + loc + "</loc>" + lastmod + "<changefreq>monthly</changefreq><priority>0.6</priority></url>\n")
	}
	if add.Len() == 0 {
		return nil
	}
	const close = "</urlset>"
	i := strings.LastIndex(text, close)
	if i < 0 {
		return fmt.Errorf("docs/sitemap.xml has no %s", close)
	}
	return os.WriteFile(path, []byte(text[:i]+add.String()+text[i:]), 0o644)
}

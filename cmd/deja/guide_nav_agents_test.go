package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every guide page carries the same sidebar, and the per-agent list inside it
// is written into each page rather than shared at runtime. memory-for-hermes
// was four guides and one count behind the rest — a reader who arrived there
// was told deja has no guide for Roo, Continue, prime-agent or Crush.
//
// TestGuidePagesShareTheirNavigation compares the pages' outer navigation and
// passed throughout, because the per-agent list sits inside a <details> block
// it does not look into.
func TestEveryGuidePageListsEveryPerAgentGuide(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "guide")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var guides, pages []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".html" {
			continue
		}
		pages = append(pages, e.Name())
		if strings.HasPrefix(e.Name(), "memory-for-") {
			guides = append(guides, e.Name())
		}
	}
	if len(guides) == 0 {
		t.Fatal("no per-agent guides on disk, so this checks nothing")
	}

	block := regexp.MustCompile(`(?s)<details class="grpfold"[^>]*>.*?</details>`)
	count := regexp.MustCompile(`Per-agent guides <span class="n">(\d+)</span>`)
	for _, name := range pages {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		nav := block.Find(b)
		if nav == nil {
			continue // a page without the fold is not claiming a list
		}
		for _, g := range guides {
			if !strings.Contains(string(nav), `href="`+g+`"`) {
				t.Errorf("docs/guide/%s does not link %s in its per-agent list", name, g)
			}
		}
		m := count.FindSubmatch(nav)
		if m == nil {
			t.Errorf("docs/guide/%s names no count for the per-agent guides", name)
			continue
		}
		if n, _ := strconv.Atoi(string(m[1])); n != len(guides) {
			t.Errorf("docs/guide/%s says %d per-agent guides; there are %d", name, n, len(guides))
		}
	}
}

// A per-agent page that offers "the other N agents" is counting the registry
// minus itself, in the description a search engine shows. The Hermes page said
// twenty-two for months, and it is repeated five times on that page — the
// meta description, og, twitter, the structured data and the lede.
func TestAPerAgentPageCountsTheOtherAgentsCorrectly(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "docs", "guide")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "the other " + countWordForTest(registryHarnessCount(t, root)-1)
	// Only a number: "the other agents" and "the other half" are prose.
	stale := regexp.MustCompile(`the other (twenty[a-z-]*|thirty[a-z-]*|\d+)`)
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "memory-for-") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range stale.FindAllString(string(b), -1) {
			checked++
			if m != want {
				t.Errorf("docs/guide/%s says %q; the registry has %d harnesses", e.Name(), m, registryHarnessCount(t, root))
			}
		}
	}
	if checked == 0 {
		t.Skip("no page counts the other agents")
	}
}

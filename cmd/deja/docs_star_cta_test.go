package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The star line asks the one thing a reader of these pages can do for the
// project, and it has to be on all of them: the guide and registry pages are
// where search traffic lands, and a new one added without the line is the
// silent way the ask disappears. Four of the seven delete-* pages carried it
// and three did not, which is how this started.
//
// Placement is the other half. The line lives inside <article>, which is the
// content column of the .doc grid; below the grid it stretches the full width
// of the page and stops lining up with the text above it.
func TestEveryGuideAndRegistryPageEndsWithTheStarLine(t *testing.T) {
	const marker = `class="star-cta"`
	root := filepath.Join("..", "..", "docs")
	checked := 0

	for _, dir := range []string{"guide", "registry"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".html" {
				continue
			}
			page := filepath.ToSlash(filepath.Join(dir, e.Name()))
			b, err := os.ReadFile(filepath.Join(root, dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			text := string(b)
			checked++

			switch n := strings.Count(text, marker); n {
			case 1:
			case 0:
				t.Errorf("%s does not ask the reader for a star", page)
				continue
			default:
				t.Errorf("%s asks for a star %d times", page, n)
				continue
			}
			closing := strings.Index(text, "</article>")
			if closing < 0 {
				t.Errorf("%s has no </article>, so the star line has no column to sit in", page)
				continue
			}
			if strings.Index(text, marker) > closing {
				t.Errorf("%s puts the star line after </article>, outside the content column", page)
			}
		}
	}

	// The landing page is built differently — one inline stylesheet, no .doc
	// grid — but it carries the same line.
	index, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), marker) {
		t.Error("the landing page does not ask the reader for a star")
	}

	// Unstyled, the line renders as a paragraph of body text in the middle of
	// the page furniture.
	css, err := os.ReadFile(filepath.Join(root, "assets", "site.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".star-cta{") {
		t.Error("site.css has no .star-cta rule, so the line is unstyled on every guide and registry page")
	}

	if checked < 80 {
		t.Fatalf("only %d pages were checked, so this test proves little", checked)
	}
}

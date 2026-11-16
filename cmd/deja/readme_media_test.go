package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A README image is written as an absolute raw URL, because github.com is not
// the only thing that renders this file. Its own page rewrites a relative
// `src="assets/demo.gif"` to `/vshulcz/deja-vu/raw/main/assets/demo.gif` and
// serves it; a registry listing, a marketplace card, an npm page or anything
// else that renders the markdown on its own domain resolves the same path
// against itself and shows a broken image. Absolute costs nothing on
// github.com — every badge in the file is already remote — and is the only
// form that works everywhere else.
//
// The pages under docs/ are the opposite case and keep their relative paths:
// they are served from docs/ as the site root, where relative is correct, and
// TestEveryImageThePagesAskForExists covers those.
func TestReadmeImagesAreAbsolute(t *testing.T) {
	tag := regexp.MustCompile(`(?:src|srcset)="([^"]+)"`)
	checked := 0
	for _, name := range []string{"README.md", "docs/readme/README.zh.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range tag.FindAllStringSubmatch(string(b), -1) {
			src := m[1]
			checked++
			if strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "data:") {
				continue
			}
			t.Errorf("%s asks for %q, which only resolves on github.com itself — use https://raw.githubusercontent.com/vshulcz/deja-vu/main/%s",
				name, src, strings.TrimPrefix(src, "./"))
		}
	}
	if checked == 0 {
		t.Fatal("no README image was checked, so this test proves nothing")
	}
}

// The other half of the same invariant: an absolute raw URL is only right while
// the file it names is in the tree. A rename that updates the page and not the
// URL is a broken image nothing else catches, because the URL is remote and
// every existing check skips remote images.
func TestReadmeImagesNameFilesThatExist(t *testing.T) {
	const raw = "https://raw.githubusercontent.com/vshulcz/deja-vu/main/"
	tag := regexp.MustCompile(`(?:src|srcset)="([^"]+)"`)
	checked := 0
	for _, name := range []string{"README.md", "docs/readme/README.zh.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range tag.FindAllStringSubmatch(string(b), -1) {
			src := m[1]
			if !strings.HasPrefix(src, raw) {
				continue
			}
			rel := strings.TrimPrefix(src, raw)
			checked++
			if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(rel))); err != nil {
				t.Errorf("%s asks for %s, which is not in the tree", name, rel)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no README image pointed at this repository, so this test proves nothing")
	}
}

func TestPluginAndPackageReadmesParity(t *testing.T) {
	const rawPrefix = "https://raw.githubusercontent.com/vshulcz/deja-vu/main/"
	rawURLRe := regexp.MustCompile(`https://raw\.githubusercontent\.com/vshulcz/deja-vu/main/[^\s)"'>]+`)
	locales := []string{"zh", "zh-TW", "ja", "ko", "es", "pt", "fr", "de", "ru", "tr", "hi"}
	targets := []string{
		"antigravity-plugin/README.md",
		"claude-plugin/README.md",
		"codebuddy-plugin/README.md",
		"codex-plugin/README.md",
		"cursor-plugin/README.md",
		"devin-plugin/README.md",
		"extensions/README.md",
		"npm/README.md",
	}

	root := filepath.Join("..", "..")
	for _, rel := range targets {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		text := string(b)
		for _, lang := range locales {
			want := "docs/readme/README." + lang + ".md"
			if !strings.Contains(text, want) {
				t.Errorf("%s missing switcher link %s", rel, want)
			}
		}
		for _, req := range []string{
			rawPrefix + "assets/demo.gif",
			"## Benchmarks",
			".deja/memory.jsonl",
			"merge=union",
			"deja handoff",
			"Star deja-vu on GitHub",
		} {
			if !strings.Contains(text, req) {
				t.Errorf("%s missing %q", rel, req)
			}
		}
		for _, url := range rawURLRe.FindAllString(text, -1) {
			asset := strings.TrimPrefix(url, rawPrefix)
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(asset))); err != nil {
				t.Errorf("%s references missing asset %s", rel, asset)
			}
		}
	}
}

func TestGuidePagesCarryDemoAndRepoMemory(t *testing.T) {
	root := filepath.Join("..", "..")
	enGuides, err := filepath.Glob(filepath.Join(root, "docs", "guide", "memory-for-*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(enGuides) != 50 {
		t.Fatalf("expected 50 EN memory-for-* guides, got %d", len(enGuides))
	}
	zhGuides, err := filepath.Glob(filepath.Join(root, "docs", "zh", "guide", "memory-for-*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(zhGuides) != 6 {
		t.Fatalf("expected 6 ZH memory-for-* guides, got %d", len(zhGuides))
	}

	pages := make([]string, 0, len(enGuides)+len(zhGuides)+1)
	pages = append(pages, enGuides...)
	pages = append(pages, zhGuides...)
	pages = append(pages, filepath.Join(root, "docs", "ru", "guide", "getting-started.html"))

	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		text := string(b)
		for _, req := range []string{"demo.gif", ".deja/memory.jsonl", "merge=union", "deja handoff"} {
			if !strings.Contains(text, req) {
				t.Errorf("%s missing %q", p, req)
			}
		}
	}
}

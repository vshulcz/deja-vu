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

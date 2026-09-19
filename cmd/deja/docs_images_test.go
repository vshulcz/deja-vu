package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every image a page asks for has to be in the published tree. The site is
// served from docs/, and the repository keeps a second copy of the demo assets
// at the root for the README — so an asset added for a page and committed only
// to the root one renders as a broken image on the site and nowhere else. That
// is what happened to the blame recording: `assets/blame.gif` existed, the page
// asked for `../assets/blame.gif`, and nothing failed.
func TestEveryImageThePagesAskForExists(t *testing.T) {
	root := filepath.Join("..", "..", "docs")
	img := regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	checked := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range img.FindAllStringSubmatch(string(b), -1) {
			src := m[1]
			// Remote and inline images are somebody else's problem.
			if strings.Contains(src, "://") || strings.HasPrefix(src, "data:") {
				continue
			}
			src, _, _ = strings.Cut(src, "?")
			src, _, _ = strings.Cut(src, "#")
			var want string
			if strings.HasPrefix(src, "/") {
				// Absolute on the published site, which is served under /deja-vu/.
				want = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(src, "/"), "deja-vu/")))
			} else {
				want = filepath.Join(filepath.Dir(path), filepath.FromSlash(src))
			}
			checked++
			if _, err := os.Stat(want); err != nil {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s asks for %s, which is not in the published tree", filepath.ToSlash(rel), src)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no page image was checked, so this test proves nothing")
	}
}

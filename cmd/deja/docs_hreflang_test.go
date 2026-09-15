package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A translated page is only a translation to a search engine if both pages say
// so: hreflang declared on one side alone is discarded, and the two pages then
// compete as duplicates of each other. The Chinese pages carried the pair from
// the start; the English ones did not, so the first three translations pointed
// at counterparts that never pointed back.
//
// The same check covers the sitemap, because a page no sitemap lists is not
// offered to anyone at all.
func TestChinesePagesArePairedWithTheirEnglishOnes(t *testing.T) {
	root := filepath.Join("..", "..")
	const site = "https://vshulcz.github.io/deja-vu/"

	sitemap, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}

	var pages []string
	err = filepath.Walk(filepath.Join(root, "docs", "zh"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		pages = append(pages, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("no Chinese pages under docs/zh — this test has lost its subject")
	}

	for _, zh := range pages {
		rel := filepath.ToSlash(strings.TrimPrefix(zh, filepath.Join(root, "docs")+string(filepath.Separator)))
		en := filepath.Join(root, "docs", filepath.FromSlash(strings.TrimPrefix(rel, "zh/")))

		zhURL := site + rel
		enURL := site + strings.TrimPrefix(rel, "zh/")

		zhBody, err := os.ReadFile(zh)
		if err != nil {
			t.Fatal(err)
		}
		enBody, err := os.ReadFile(en)
		if err != nil {
			t.Errorf("%s has no English counterpart at %s: %v", rel, en, err)
			continue
		}

		for _, want := range []struct{ file, link string }{
			{zh, `hreflang="zh-Hans" href="` + zhURL + `"`},
			{zh, `hreflang="en" href="` + enURL + `"`},
			{en, `hreflang="zh-Hans" href="` + zhURL + `"`},
			{en, `hreflang="en" href="` + enURL + `"`},
		} {
			body := zhBody
			if want.file == en {
				body = enBody
			}
			if !strings.Contains(string(body), want.link) {
				t.Errorf("%s does not declare %s — hreflang has to be on both pages or it is ignored",
					strings.TrimPrefix(want.file, root+string(filepath.Separator)), want.link)
			}
		}

		for _, u := range []string{zhURL, enURL} {
			if !strings.Contains(string(sitemap), "<loc>"+u+"</loc>") {
				t.Errorf("docs/sitemap.xml does not list %s", u)
			}
		}
	}
}

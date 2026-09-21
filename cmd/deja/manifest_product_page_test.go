package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const siteRoot = "https://vshulcz.github.io/deja-vu/"

// Every manifest deja ships carries a field a directory renders as the link out
// of its listing: `homepage` in the npm, scoop and plugin manifests, `PackageUrl`
// in the winget locale, `websiteUrl` in the MCP registry entry. They pointed at
// the repository, which answers a different question than the one someone
// clicking from a plugin directory is asking.
//
// The failure this guards against is a page that moved or was never there: the
// field is a string nothing resolves, so a typo ships a 404 into someone else's
// catalogue and nothing here notices.
func TestManifestProductPagesResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	field := regexp.MustCompile(`(?m)^\s*(?:"homepage":\s*"|PackageUrl:\s*|homepage:\s*|"websiteUrl":\s*")([^"\n]+)`)

	skip := map[string]bool{"docs": true, ".github": true, "node_modules": true, "fixtures": true, "testdata": true, "dist": true, ".git": true}
	var checked int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(d.Name()) {
		case ".json", ".yaml", ".yml":
		default:
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range field.FindAllStringSubmatch(string(b), -1) {
			url := strings.TrimSpace(strings.TrimSuffix(m[1], ","))
			if !strings.HasPrefix(url, "http") {
				continue // a path or a template, not a link out
			}
			checked++
			if !strings.HasPrefix(url, siteRoot) {
				t.Errorf("%s points a directory at %s; the field is the product page, which is %s", rel, url, siteRoot)
				continue
			}
			page := strings.TrimPrefix(url, siteRoot)
			if page == "" || strings.HasSuffix(page, "/") {
				page += "index.html"
			}
			if _, err := os.Stat(filepath.Join(root, "docs", filepath.FromSlash(page))); err != nil {
				t.Errorf("%s points at %s, and docs/%s is not there", rel, url, page)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 15 {
		t.Fatalf("only %d product-page fields found; the pattern or the walk is wrong", checked)
	}

	// The registry entry is the one where the field is optional, and it was left
	// out: every published version of io.github.vshulcz/deja-vu carried
	// websiteUrl null, so the mirrors had nothing but the repository to link.
	b, err := os.ReadFile(filepath.Join(root, "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"websiteUrl"`) {
		t.Error("server.json has no websiteUrl, so the registry and its mirrors link the repository and nothing else")
	}
}

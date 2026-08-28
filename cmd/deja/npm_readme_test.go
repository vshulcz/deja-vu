package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// npm chooses the readme it renders on the package page by looking for README*
// at the package root, and with two of them it took the translation: the page
// for dsh-deja served Chinese to everyone, which is a strange thing for an
// English-speaking user to meet on the page that is supposed to sell the
// package. One README at the root, translations in a subdirectory.
func TestPublishedPackagesHaveOneReadmeAtTheRoot(t *testing.T) {
	for _, pkg := range []string{
		filepath.Join("..", "..", "extensions", "dsh"),
		filepath.Join("..", "..", "extensions", "opencode"),
	} {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		var readmes []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(strings.ToUpper(e.Name()), "README") {
				readmes = append(readmes, e.Name())
			}
		}
		if len(readmes) != 1 {
			t.Errorf("%s has %v at its root; npm picks one of them for the package page and it is not necessarily README.md", filepath.Base(pkg), readmes)
		}

		// Whatever the allowlist ships has to exist, or the tarball quietly
		// loses a file that the README links to.
		b, err := os.ReadFile(filepath.Join(pkg, "package.json"))
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		var manifest struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal(b, &manifest); err != nil {
			t.Fatalf("%s package.json: %v", pkg, err)
		}
		for _, f := range manifest.Files {
			if _, err := os.Stat(filepath.Join(pkg, f)); err != nil {
				t.Errorf("%s lists %q in files but it is not there", filepath.Base(pkg), f)
			}
		}
	}
}

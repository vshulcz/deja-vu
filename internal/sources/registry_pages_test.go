package sources

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var lastVerifiedRE = regexp.MustCompile(`\*\*Last verified:\*\* (\S+)`)

// Each harness has a reference page beside registry.json, and both carry the
// date the format was last checked against a real store. Only the registry's
// copy is read by anything, so the pages drifted: three sat ten days behind and
// seven had no date at all, while the pages are what a reader looking up a
// harness actually opens.
func TestRegistryPagesAgreeWithTheRegistry(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	registry := readFormatRegistry(t, filepath.Join(root, "docs", "registry", "registry.json"))
	for _, entry := range registry.Harnesses {
		t.Run(entry.ID, func(t *testing.T) {
			path, ok := registryPagePath(root, entry.ID)
			if !ok {
				t.Skipf("no reference page for %s", entry.ID)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			m := lastVerifiedRE.FindSubmatch(b)
			if m == nil {
				t.Fatalf("%s carries no **Last verified** line; the registry says %s",
					filepath.Base(path), entry.LastVerified)
			}
			if got := string(m[1]); got != entry.LastVerified {
				t.Errorf("%s says %s, registry.json says %s — one of them is telling "+
					"a reader the format was checked when it was not",
					filepath.Base(path), got, entry.LastVerified)
			}
		})
	}
}

// registryPagePath finds the page for an id, which is named after the harness
// rather than keyed: claude is claude-code.md.
func registryPagePath(root, id string) (string, bool) {
	dir := filepath.Join(root, "docs", "registry")
	for _, name := range []string{id + ".md", id + "-code.md", id + "-cli.md"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

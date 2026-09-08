package sources

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mcpToolNameRE is how a page would name a tool a client shows: the prefixed
// form an MCP client prints in its tool list.
var mcpToolNameRE = regexp.MustCompile("mcp__deja__([a-z_]+)")

// deja's MCP server advertises one tool, `deja`, with a mode argument. The dsh
// page still described the shape from before that collapse — "dsh lists
// mcp__deja__recall, recall_context, remember, blame, fix and how itself" —
// so a reader following it looked for five tools their client never shows and
// could not tell whether the install had worked (#3343).
func TestNoRegistryPageNamesAToolTheServerDoesNotAdvertise(t *testing.T) {
	// The .html beside each page is what a reader and a search engine open, and
	// it is generated from the .md — a page fixed in one and not rebuilt in the
	// other keeps the stale claim where it is actually read (review of #3343).
	var files []string
	for _, dir := range []string{
		filepath.Join("..", "..", "docs", "registry"),
		filepath.Join("..", "..", "docs", "guide"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(e.Name(), ".md") || strings.HasSuffix(e.Name(), ".html") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	files = append(files, filepath.Join("..", "..", "README.md"))
	for _, path := range files {
		e := struct{ name string }{filepath.Base(path)}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range mcpToolNameRE.FindAllSubmatch(b, -1) {
			t.Errorf("%s names the tool %q; the server advertises one tool, deja, with a mode", e.name, string(m[0]))
		}
		if bytes.Contains(b, []byte("six tools")) {
			t.Errorf("%s says \"six tools\"; there is one tool with six modes", e.name)
		}
	}
}

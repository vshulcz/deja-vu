package sources

import (
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
	dir := filepath.Join("..", "..", "docs", "registry")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range mcpToolNameRE.FindAllSubmatch(b, -1) {
			t.Errorf("%s names the tool %q; the server advertises one tool, deja, with a mode", e.Name(), string(m[0]))
		}
	}
}

package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The parser table in ARCHITECTURE.md is the first place a reader looks to
// decide whether their tool is read at all, and nothing checked it: CodeWhale
// had a parser, a registry page and a test, and no row here, while the sentence
// above the table still counted the stores it used to hold. A table that is one
// harness short reads as an answer rather than an omission.
func TestTheParserTableHasARowForEveryHarness(t *testing.T) {
	root := filepath.Join("..", "..")

	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.Harnesses) == 0 {
		t.Fatal("no harnesses in registry.json — this checks nothing")
	}

	doc, err := os.ReadFile(filepath.Join(root, "docs", "ARCHITECTURE.md"))
	if err != nil {
		t.Fatal(err)
	}
	arch := string(doc)
	from := strings.Index(arch, "## Source parsers")
	if from < 0 {
		t.Fatal("docs/ARCHITECTURE.md has no source-parser section")
	}
	next := strings.Index(arch[from+1:], "\n## ")
	if next < 0 {
		t.Fatal("the source-parser section runs to the end of the file")
	}
	arch = arch[from : from+1+next]

	for _, h := range reg.Harnesses {
		if !strings.Contains(arch, "| "+h.DisplayName+" |") {
			t.Errorf("the parser table in docs/ARCHITECTURE.md has no row for %s (%s)", h.DisplayName, h.ID)
		}
	}

	// The count in the sentence above the table is the other half: the rows are
	// the harnesses plus deja's own notes.
	rows := strings.Count(arch, "\n| ") - 2 // the header and its rule
	if want := len(reg.Harnesses) + 1; rows != want {
		t.Errorf("the parser table has %d rows, want %d — the harnesses plus deja notes", rows, want)
	}
}

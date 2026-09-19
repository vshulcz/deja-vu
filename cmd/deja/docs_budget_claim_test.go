package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// "capped at 1,536 bytes per prompt and about 4 KB per tool call" is on
// twenty-odd pages, once in the prose and once inside the FAQ block, and every
// copy is hand-written. The numbers are right today; the reason to pin them is
// the four claims that were not (#3779, #3781): a number nobody checks is a
// number that drifts, and here it would drift on every per-harness page at
// once.
func TestTheInjectionBudgetsThePagesQuoteAreTheOnesInTheCode(t *testing.T) {
	root := filepath.Join("..", "..")
	wantPrompt := fmt.Sprintf("%d,%03d bytes per prompt", promptHookBudget/1000, promptHookBudget%1000)
	wantTool := fmt.Sprintf("about %d KB per tool call", recallMCPBudget/1024)

	var pages []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := filepath.Ext(path); ext == ".html" || ext == ".txt" || ext == ".md" {
			pages = append(pages, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	prompt := regexp.MustCompile(`[\d,]+ bytes per prompt`)
	tool := regexp.MustCompile(`about [\d.]+ [KM]B per tool call`)
	quoting := 0
	for _, path := range pages {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		name, _ := filepath.Rel(root, path)
		name = filepath.ToSlash(name)
		for _, got := range prompt.FindAllString(text, -1) {
			quoting++
			if got != wantPrompt {
				t.Errorf("%s says %q; the hook budget is %d bytes (%q)", name, got, promptHookBudget, wantPrompt)
			}
		}
		for _, got := range tool.FindAllString(text, -1) {
			if !strings.EqualFold(got, wantTool) {
				t.Errorf("%s says %q; the tool-call budget is %d bytes (%q)", name, got, recallMCPBudget, wantTool)
			}
		}
	}
	if quoting < 5 {
		t.Fatalf("only %d pages quote the prompt budget; this test is looking for the wrong phrase", quoting)
	}
}

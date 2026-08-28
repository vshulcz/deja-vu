package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Two rules can be in force at once — one door served a digest, another hides
// things from this page — and the page answers both: each row carries the rule
// its digest was served under, the note names the rule holding rows back. The
// per-row half was rendered by the page's own script and pinned by nothing.
func TestThePageCarriesTheRuleEachDigestWasServedUnder(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordDigestPolicy(dir, usage.KindRecall,
		"<deja-recall>\n1. my own connection pool question\n", 1, 400, "local+imported")

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	m := regexp.MustCompile(`(?s)const S=(.*?),R=(.*?),N=(.*?);`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the page no longer embeds a recalls array; this test reads the wrong thing now")
	}
	var recalls []struct {
		Kind   string `json:"kind"`
		Policy string `json:"policy"`
	}
	if err := json.Unmarshal([]byte(m[2]), &recalls); err != nil {
		t.Fatalf("decode recalls: %v", err)
	}
	if len(recalls) != 1 {
		t.Fatalf("recalls = %d, want the one just recorded", len(recalls))
	}
	if recalls[0].Policy != "local+imported" {
		t.Errorf("the row does not say which rule served it: %+v", recalls[0])
	}
	// And the page's own renderer prints it, rather than carrying it unused.
	if !strings.Contains(page, "r.policy") {
		t.Errorf("the page carries the rule but never shows it")
	}
}

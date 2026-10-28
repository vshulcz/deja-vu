package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// nothingCorpus writes sessions that each share one ordinary word with
// "kestrel retention shard glacier" and none that is about it.
func nothingCorpus(t *testing.T) (dir, here string) {
	t.Helper()
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	work := t.TempDir()
	here = filepath.Join(work, "src", "ledgerd")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, role, text string) string {
		b, err := json.Marshal(map[string]any{"type": role, "sessionId": sid, "cwd": here, "timestamp": "2026-09-10T10:00:00Z",
			"message": map[string]any{"role": role, "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for i := range 30 {
		sid := fmt.Sprintf("l%02d", i)
		word := []string{"retention", "shard", "turnstile"}[i%3]
		writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(here), sid+".jsonl"), sid, []string{
			line(sid, "user", fmt.Sprintf("unlock the ledger %s window %d", word, i)),
			line(sid, "assistant", fmt.Sprintf("%s unlock %d is done, the full log of the run is attached above for the record", word, i)),
		})
	}
	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)
	return dir, here
}

func recallPageText(t *testing.T, dir, q string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"mode": "recall", "q": q})
	if err != nil {
		t.Fatal(err)
	}
	text, err := callMCPTool(dir, "deja", b)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// A page no session is about lists its leads one line each: where, when,
// which words, which id. The excerpts under them were most of the page and
// agents almost never followed them; the first lead keeps one.
func TestANothingPageListsEachLeadOnOneLine(t *testing.T) {
	hermeticEnv(t)
	dir, _ := nothingCorpus(t)
	text := recallPageText(t, dir, "kestrel retention shard glacier")
	if !strings.Contains(text, nothingIsAboutThis) || !strings.Contains(text, "treat them as leads") {
		t.Fatalf("the page is not one no session is about, so this measures nothing:\n%s", text)
	}
	leads := regexp.MustCompile(`(?m)^\d+\. \[claude\] \S+ · \d{4}-\d{2}-\d{2} · has [a-z, ]+ · l\d\d$`).FindAllString(text, -1)
	if len(leads) == 0 {
		t.Fatalf("no lead is written as one line with its words and id:\n%s", text)
	}
	if got := strings.Count(text, "\n- "); got != 1 || !regexp.MustCompile(`(?m)^1\. .*\n- `).MatchString(text) {
		t.Errorf("want one excerpt, under the first lead; got %d:\n%s", got, text)
	}
	for _, bad := range []string{"what this session concluded", "files it touched", "call recall again with that id"} {
		if strings.Contains(text, bad) {
			t.Errorf("a lead on a page no session is about still carries %q:\n%s", bad, text)
		}
	}
}

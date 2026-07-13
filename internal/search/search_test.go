package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestSearchRanksAndFilters(t *testing.T) {
	now := time.Now()
	ss := []model.Session{{ID: "a", Harness: "claude", Project: "p", Updated: now, Messages: []model.Message{{Role: "user", Text: "needle needle", Time: now}}}, {ID: "b", Harness: "codex", Project: "p", Updated: now.Add(-24 * time.Hour), Messages: []model.Message{{Role: "assistant", Text: "needle", Time: now}}}}
	hits, err := Run(ss, Options{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Session.ID != "a" || hits[0].Count != 2 {
		t.Fatalf("bad hits: %#v", hits)
	}
	hits, err = Run(ss, Options{Query: "needle", Harness: "codex", Role: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "b" {
		t.Fatalf("bad filter: %#v", hits)
	}
}

func TestPrintPlainWhenNotTTY(t *testing.T) {
	now := time.Now()
	hits := []Hit{{Session: model.Session{ID: "abcdef1234567890", Harness: "opencode", Project: "deja", Updated: now}, Count: 1, Snippets: []string{"hello needle"}}}
	var b bytes.Buffer
	Print(&b, hits, Options{Query: "needle"})
	out := b.String()
	if strings.Contains(out, "\x1b[") || !strings.Contains(out, "[opencode]") || !strings.Contains(out, "1 matches") {
		t.Fatalf("bad plain output: %q", out)
	}
}

func TestSnippetPrefersProseOverToolDump(t *testing.T) {
	text := "netcat output noise needle\n1: package main\n2: func main() {}\nUser asked about needle migration strategy and we concluded use small batches."
	hits, err := Run([]model.Session{{ID: "s", Harness: "claude", Project: "p", Updated: time.Now(), Messages: []model.Message{{Role: "assistant", Text: text}}}}, Options{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || strings.Contains(hits[0].Snippets[0], "1: package") || strings.Contains(hits[0].Snippets[0], "netcat") {
		t.Fatalf("noisy snippet: %#v", hits)
	}
	if !strings.Contains(hits[0].Snippets[0], "migration strategy") {
		t.Fatalf("missing prose snippet: %#v", hits[0].Snippets[0])
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The card is the one thing a reader makes with deja that they might show
// somebody else, and the line printed after it said "paste into a README or
// post". The README half works. The other half does not: the card is an SVG,
// and X, Threads, Reddit, Mastodon and every chat app refuse to render one —
// the person following that sentence finds out only once the post is written.
//
// So the line has to name the conversion, and the page that does it has to
// exist in this repository rather than in a sentence.
func TestTheCardSaysHowToPostIt(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	if err := os.MkdirAll(filepath.Join(root, "-work-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeFixture(t, filepath.Join(root, "-work-app", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"2026-08-20T10:00:00Z",` +
			`"message":{"role":"user","content":"the retry budget keeps blowing up"}}`,
	})
	card := filepath.Join(tmp, "deja-stats.svg")
	out, err := captureRun(t, "stats", "--card", card)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(card); err != nil {
		t.Fatalf("no card was written: %v", err)
	}
	if !strings.Contains(out, "vshulcz.github.io/deja-vu/card/") {
		t.Errorf("the card says nothing about how to post it, and an SVG cannot be posted:\n%s", out)
	}

	// The page the line sends people to.
	page := filepath.Join("..", "..", "docs", "card", "index.html")
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("the line points at a page that is not in the repository: %v", err)
	}
	for _, want := range []string{"toBlob", "image/png", "never leaves your machine"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("docs/card/index.html does not %q — it has to convert locally, or the promise is wrong", want)
		}
	}
}

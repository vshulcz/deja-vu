package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An agent exercising deja from a shell writes deja's own output into its
// transcript, and every line of a blame report names the file it is about. So
// blame ranked its own answer as that file's history: measured on this
// repository's sources, two of eighty-two snippets came back as
// `=== deja blame internal/index/retrieval.go ===` and the report under it.
//
// The same rule the report guard and the fix miner already apply (#2067,
// #2068, #2169).
func TestBlameDoesNotQuoteItsOwnOutput(t *testing.T) {
	now := "2026-01-02T03:04:05Z"
	_ = now
	sessions := []model.Session{{
		ID: "shell", Harness: "claude", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "check the tool"},
			{Role: "tool-output", Text: "=== deja blame internal/index/retrieval.go ===\n" +
				"2026-07-18 · claude · file-00 · have a look at internal/index/retrieval.go"},
		},
	}, {
		ID: "real", Harness: "claude", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "internal/index/retrieval.go keeps losing the idf map between passes"},
			{Role: "assistant", Text: "Fixed: internal/index/retrieval.go now returns it with the ranking."},
		},
	}}
	target, err := ResolveBlamePath("internal/index/retrieval.go")
	if err != nil {
		t.Fatal(err)
	}
	hits := Blame(sessions, target, BlameOptions{All: true})
	for _, h := range hits {
		for _, s := range h.Snippets {
			if strings.Contains(s, "=== deja ") {
				t.Errorf("blame quoted its own output: %q", strings.TrimSpace(s)[:min(len(s), 80)])
			}
		}
		// The report body is still matched — only the command echo is
		// dropped — so such a session can still appear. What it may not do is
		// hand deja's own words back as the file's history, which is the line
		// a reader acts on.
	}
	found := false
	for _, h := range hits {
		if h.Session.ID == "real" {
			found = true
		}
	}
	if !found {
		t.Error("the session that actually discussed the file was dropped")
	}
}

// The report body is left alone: `deja: ` opens deja's own messages to a
// terminal, and it is also how a person writes about deja.
func TestBlameKeepsALineThatMerelyNamesDeja(t *testing.T) {
	if withoutOwnReport("deja: no sessions mention nope.go") == "" {
		t.Error("a line addressed by deja was dropped from matching")
	}
	if got := withoutOwnReport("=== deja blame x.go ===\nkept"); strings.Contains(got, "=== deja") {
		t.Errorf("the command echo survived: %q", got)
	}
}

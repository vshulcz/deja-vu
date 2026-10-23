package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The reading tools answer from the snapshot while a rebuild runs (#1733).
// blame was left out because its path takes the blocking EnsureForSearch — and
// blame is the tool an agent calls before editing a file, so "ask again then"
// means the edit happens without the history (#1784).
func TestBlameReadsWithoutWaitingForARebuild(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"b1","cwd":"/w","timestamp":"2026-08-24T01:00:00Z","message":{"role":"user","content":"zapfizzle editing parser.go here"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The agent-facing path must not be the blocking one.
	hits, _, _, _, err := findBlameHitsStale(dir, search.BlameTarget{Stem: "parser.go", Base: "parser.go"}, search.BlameOptions{All: true}, policy.ActivationMCP, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("blame found nothing on a store that mentions the file")
	}
	_ = tmp
	// And the tool no longer declines while a rebuild is in flight: that is
	// what buildingNowForBlockingTool is for, and blame is not one of those
	// any more. The case has to be there for this to mean anything.
	src, err := os.ReadFile("mcp.go")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for block := range strings.SplitSeq(string(src), "case \"") {
		if !strings.HasPrefix(block, "blame\"") {
			continue
		}
		found = true
		if strings.Contains(block, "buildingNowForBlockingTool") {
			t.Error("blame still declines while a rebuild runs")
		}
	}
	if !found {
		t.Fatal("no blame case in mcp.go, so this proved nothing")
	}
}

// And the answer says so, in the line recall uses for the same state.
func TestBlameSaysWhenItServedASnapshot(t *testing.T) {
	hits := []search.BlameHit{{Session: model.Session{ID: "s", Harness: "claude", Project: "proj"}, Count: 1}}
	body := renderMCPBlame("a.go", "a.go", hits, 0, coverageLine(index.Coverage{Stale: true}))
	if !strings.Contains(body, "refreshing in the background") {
		t.Errorf("a snapshot answer says nothing about the refresh: %s", body)
	}
	if quiet := renderMCPBlame("a.go", "a.go", hits, 0, ""); strings.Contains(quiet, "refresh") {
		t.Errorf("an ordinary answer carries the note: %s", quiet)
	}
}

// The note must not cost a session: the payload is trimmed to its budget on the
// hits alone, and the note is added afterwards.
func TestTheRefreshNoteDoesNotCostASession(t *testing.T) {
	hits := make([]search.BlameHit, 0, 40)
	for i := range 40 {
		hits = append(hits, search.BlameHit{
			Session:  model.Session{ID: "s", Harness: "claude", Project: "proj", Title: strings.Repeat("x", 300)},
			Count:    i,
			Snippets: []string{strings.Repeat("y", 300)},
		})
	}
	quiet := countBlameSessions(blameBodyFor(hits, false))
	noisy := countBlameSessions(blameBodyFor(hits, true))
	if quiet < 2 || noisy != quiet {
		t.Errorf("the note cost %d session(s): %d against %d", quiet-noisy, noisy, quiet)
	}
	// What it does cost is its own length, and no more.
	over := len(blameBodyFor(hits, true)) - blameMCPBudget
	if note := len("(" + coverageLine(index.Coverage{Stale: true}) + ")\n"); over > note {
		t.Errorf("the payload is %d bytes over the budget, more than the note's %d", over, note)
	}
}

// blameBodyFor runs the same trim-then-note sequence blameTextResult does.
func blameBodyFor(hits []search.BlameHit, refreshing bool) string {
	body := renderMCPBlame("a.go", "a.go", hits, 0, "")
	for len(body) > blameMCPBudget && len(hits) > 1 {
		hits = hits[:max(len(hits)*3/4, 1)]
		body = renderMCPBlame("a.go", "a.go", hits, 0, "")
	}
	if refreshing {
		body = renderMCPBlame("a.go", "a.go", hits, 0, coverageLine(index.Coverage{Stale: true}))
	}
	return body
}

// countBlameSessions counts the numbered rows of a blame page.
func countBlameSessions(body string) int {
	n := 0
	for line := range strings.SplitSeq(body, "\n") {
		if blameRowRE.MatchString(line) {
			n++
		}
	}
	return n
}

var blameRowRE = regexp.MustCompile(`^\d+\. \[`)

package main

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	hits, _, _, err := findBlameHitsStale(dir, search.BlameTarget{Stem: "parser.go", Base: "parser.go"}, search.BlameOptions{All: true}, policy.ActivationMCP, nil)
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
	for _, block := range strings.Split(string(src), "case \"") {
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

// And the answer says so: recall prints the sentence in prose, blame answers in
// JSON, so the note goes in the payload rather than nowhere.
func TestBlameSaysWhenItServedASnapshot(t *testing.T) {
	body := string(mustMarshalBlame(nil, 0, true))
	if !strings.Contains(body, "index refresh running in the background") {
		t.Errorf("a snapshot answer says nothing about the refresh: %s", body)
	}
	if quiet := string(mustMarshalBlame(nil, 0, false)); strings.Contains(quiet, "refresh") {
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
	quiet := countBlameSessions(t, blameBodyFor(hits, false))
	noisy := countBlameSessions(t, blameBodyFor(hits, true))
	if noisy != quiet {
		t.Errorf("the note cost %d session(s): %d against %d", quiet-noisy, noisy, quiet)
	}
	// What it does cost is its own length, and no more.
	over := len(blameBodyFor(hits, true)) - blameMCPBudget
	if note := len(`{"note":"index refresh running in the background — the very newest sessions may not appear yet"},`); over > note {
		t.Errorf("the payload is %d bytes over the budget, more than the note's %d", over, note)
	}
}

// blameBodyFor runs the same trim-then-note sequence blameTextResult does.
func blameBodyFor(hits []search.BlameHit, refreshing bool) []byte {
	body := mustMarshalBlame(hits, 0, false)
	for len(body) > blameMCPBudget && len(hits) > 1 {
		hits = hits[:max(len(hits)*3/4, 1)]
		body = mustMarshalBlame(hits, 0, false)
	}
	if refreshing {
		body = mustMarshalBlame(hits, 0, true)
	}
	return body
}

func countBlameSessions(t *testing.T, body []byte) int {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if _, ok := r["session"]; ok {
			n++
		}
	}
	return n
}

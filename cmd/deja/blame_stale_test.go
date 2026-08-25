package main

import (
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
	// any more.
	src, err := os.ReadFile("mcp.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range strings.Split(string(src), "case \"") {
		if !strings.HasPrefix(block, "blame\"") {
			continue
		}
		if strings.Contains(block, "buildingNowForBlockingTool") {
			t.Error("blame still declines while a rebuild runs")
		}
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

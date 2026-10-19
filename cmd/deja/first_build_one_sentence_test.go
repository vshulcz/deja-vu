package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
)

// A search that lands while another process holds the index lock is told to
// serve what is on disk. On a first build there is nothing on disk, and the run
// said two things that cannot both be true:
//
//	deja: answering from the index of <date>, N sessions — refreshing in the background
//	deja: the index is being rebuilt right now — run this again in a moment
//
// That is a new machine's first question, which is the worst run to contradict
// anything on (#3574).
func TestTheFirstBuildDoesNotClaimToAnswerFromAnIndex(t *testing.T) {
	holdTheIndexLock(t)
	var said bytes.Buffer
	if err := ensureForCLISearch(index.DefaultDir(), query.Options{Query: "retry"}, false, &said); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if strings.Contains(said.String(), "answering from") {
		t.Fatalf("an empty store claimed to be answering from an index:\n%s", said.String())
	}
}

// With a store that answers, the line is right and stays: that is the case it
// was written for — an index serving while the refresh runs behind it.
func TestAStoreThatAnswersStillSaysItIsServingWhatItHas(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(tmp, "claude", "app", "one.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"the exporter drops every third retry"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	// A transcript that has appeared since, so there is something to refresh.
	writeClaudeFixture(t, filepath.Join(tmp, "claude", "app", "two.jsonl"), "s2", []string{
		`{"type":"user","sessionId":"s2","timestamp":"2026-09-01T11:00:00Z","message":{"role":"user","content":"the cache key ignored the tenant id"}}`,
	})
	holdTheIndexLock(t)

	var said bytes.Buffer
	if err := ensureForCLISearch(index.DefaultDir(), query.Options{Query: "retry"}, false, &said); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !strings.Contains(said.String(), "answering from") {
		t.Fatalf("a store that can answer said nothing about serving the older view:\n%s", said.String())
	}
}

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// #4624: the cat has a moment beside an empty search and beside a first recall
// that finds something, in a terminal only. logoWanted's own gating is tested
// in logo_devnull_test.go; TestCatMomentsAtTheSearch below covers the call
// sites, and these pin the rendering they show.
func TestCatMomentShowsOnEmptySearch(t *testing.T) {
	var b bytes.Buffer
	mood, line := searchCatMoment(true) // empty search
	if line != "" {
		t.Fatalf("the empty-search cat says %q, which a filter or the trust policy can make untrue", line)
	}
	printLogoMood(&b, nil, mood)
	out := b.String()
	if !strings.Contains(out, "█") && !strings.Contains(out, "▀") && !strings.Contains(out, "▄") {
		t.Fatalf("empty-search cat rendered no sprite: %q", out)
	}
}

// At the search itself: a terminal gets the cat on an empty search and once
// on the first that finds something; --json never does.
func TestCatMomentsAtTheSearch(t *testing.T) {
	withTempStores(t)
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	user := mustMarshal(t, map[string]any{"type": "user", "sessionId": "a1", "cwd": "/work/pay", "timestamp": "2026-03-01T10:00:00Z",
		"message": map[string]any{"role": "user", "content": "the retry reused the idempotency key"}})
	writeClaudeFixture(t, filepath.Join(root, "pay", "a1.jsonl"), "a1", []string{string(user)})
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	// The build greets with a logo of its own; this is about the search.
	index.LastBuild = index.BuildSummary{}
	saved := logoWanted
	logoWanted = func(*os.File) bool { return true }
	t.Cleanup(func() { logoWanted = saved })
	run := func(args ...string) string {
		return captureStdout(t, func() { _ = searchWithOptions(dir, args, "", false) })
	}
	cat := "▀"
	if out := run("zzqqxx", "--json"); strings.Contains(out, cat) {
		t.Errorf("--json drew the cat:\n%s", out)
	}
	if out := run("zzqqxx", "--since", "1d"); !strings.Contains(out, cat) || strings.Contains(out, "matches that") {
		t.Errorf("empty search under a filter:\n%s", out)
	}
	if out := run("idempotency", "--json"); strings.Contains(out, cat) {
		t.Errorf("--json drew the first-recall cat:\n%s", out)
	}
	if out := run("idempotency"); !strings.Contains(out, "found something") {
		t.Errorf("first recall got no cat:\n%s", out)
	}
	if out := run("idempotency"); strings.Contains(out, "found something") {
		t.Errorf("second recall got the cat again:\n%s", out)
	}
}

func TestCatMomentShowsOnFirstRecall(t *testing.T) {
	var b bytes.Buffer
	mood, line := searchCatMoment(false) // a recall that found something
	printLogoMood(&b, []string{line}, mood)
	out := b.String()
	if !strings.Contains(out, "█") && !strings.Contains(out, "▀") && !strings.Contains(out, "▄") {
		t.Fatalf("first-recall cat rendered no sprite: %q", out)
	}
	if !strings.Contains(out, "found") {
		t.Fatalf("first-recall cat should say something was found: %q", out)
	}
}

// The two moments carry different moods, so the empty search does not look like
// a win. (moodReady vs moodSurprised draw the sprite differently.)
func TestCatMomentsUseDistinctMoods(t *testing.T) {
	emptyMood, _ := searchCatMoment(true)
	recallMood, _ := searchCatMoment(false)
	if emptyMood == recallMood {
		t.Fatalf("empty search and first recall should use different moods, both %v", emptyMood)
	}
}

// The ready cat greets only the first recall that finds something on an index,
// not every result list after it.
func TestFirstRecallIsOncePerIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if !firstRecall(dir) {
		t.Fatal("the first recall on a fresh index should get the cat")
	}
	if firstRecall(dir) {
		t.Fatal("a second recall on the same index should not get the cat again")
	}
	if !firstRecall(filepath.Join(t.TempDir(), "other")) {
		t.Fatal("another index has its own first recall")
	}
}

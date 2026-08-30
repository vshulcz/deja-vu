package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storeWithSessions indexes a handful of ordinary local sessions.
func storeWithSessions(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the beta rollout"}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// `--from` is the one filter activeFilters did not know about, so a --from that
// matched nothing sent the reader to rebuild a store that was fine — the
// misread #637 and #1007 closed for the others (#2642).
func TestAFromThatMatchesNothingNamesTheFilter(t *testing.T) {
	storeWithSessions(t)
	out, err := captureRunStderr(t, "last", "--from", "nosuchhost")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no sessions indexed yet") {
		t.Fatalf("a filter that matched nothing was reported as an empty store:\n%s", out)
	}
	if !strings.Contains(out, `from "nosuchhost"`) {
		t.Fatalf("the filter that emptied the listing is not named:\n%s", out)
	}
}

// search does not take --from at all — it is a flag of `deja last`, and the
// dispatcher says so rather than searching for the text. So the listing is the
// only screen where this filter can empty an answer.
func TestSearchRefusesFromAndSaysWhereItBelongs(t *testing.T) {
	storeWithSessions(t)
	_, err := captureRun(t, "--from", "nosuchhost", "beta rollout")
	if err == nil || !strings.Contains(err.Error(), "`deja last`") {
		t.Fatalf("search should send --from to the command that has it, got %v", err)
	}
}

// A listing with no filter at all keeps the advice it had.
func TestAnEmptyStoreStillSaysToIndex(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRunStderr(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no sessions indexed yet") {
		t.Fatalf("an empty store should still say what to do:\n%s", out)
	}
}

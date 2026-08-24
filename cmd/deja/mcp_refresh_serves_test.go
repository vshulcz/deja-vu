package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A refresh in flight is not an empty index: the snapshot on disk is published
// by an atomic swap and is readable throughout. Answering "ask again then" over
// a complete index sends the agent away for the length of every refresh, and an
// agent does not ask again — it concludes there is no history (#1733).
func TestARefreshOverAReadableIndexStillAnswers(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"zeepwock pipeline stalled"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := buildingNowForAgent(dir); got != "" {
		t.Fatalf("the fixture is wrong — a quiet built index already claims to be building: %q", got)
	}

	// A refresh in flight, in both the shapes the code checks for.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := buildingNowForAgent(dir); got != "" {
		t.Errorf("a refresh over a readable index sends the agent away: %q", got)
	}
	st := `{"phase":"reading transcripts","done":2,"total":9,"updated":` + strconv.FormatInt(time.Now().UnixNano(), 10) + `}`
	if err := os.WriteFile(warmupStatusPath(dir), []byte(st), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := buildingNowForAgent(dir); got != "" {
		t.Errorf("a published build status over a readable index sends the agent away: %q", got)
	}

	// And the answer really comes from the index.
	text, _, _, _, err := recallTextResult(dir, "zeepwock", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "zeepwock") {
		t.Errorf("recall answered nothing useful during a refresh:\n%s", text)
	}

	// With nothing to answer from, the sentence is still what an agent gets.
	empty := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := buildingNowForAgent(empty); !strings.Contains(got, "indexing") {
		t.Errorf("a first build stopped saying it is building: %q", got)
	}
}

// blame and remember still reach a blocking index.EnsureForSearch, so for them
// a refresh in flight is a reason to say so — waiting it out inside the call is
// what the sentence was written to avoid (#1306, #1309).
func TestTheTwoBlockingToolsStillDeclineDuringARefresh(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"zeepwock pipeline stalled"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := buildingNowForBlockingTool(dir); got != "" {
		t.Fatalf("a quiet index already declines: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := buildingNowForBlockingTool(dir); !strings.Contains(got, "indexing") {
		t.Errorf("a blocking tool would wait out the refresh instead of saying so: %q", got)
	}
	if got := buildingNowForAgent(dir); got != "" {
		t.Errorf("the reading tools were dragged back into declining: %q", got)
	}
}

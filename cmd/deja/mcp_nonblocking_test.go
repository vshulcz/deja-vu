package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Every MCP tool answers from the current snapshot while a refresh runs; the
// one that still called the blocking index.EnsureForSearch was kept off it by a
// check one lock acquisition earlier, which is not atomic with the call it
// protects (#1804). The invariant is about the source: no tool served inside
// handleMCP may take the blocking path at all.
func TestNoMCPToolTakesTheBlockingIndexPath(t *testing.T) {
	src, err := os.ReadFile("mcp.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(src), "index.EnsureForSearch("); n != 0 {
		t.Errorf("mcp.go still calls the blocking index.EnsureForSearch %d time(s); a tool that takes it waits out a rebuild inside the client's call", n)
	}
	if !strings.Contains(string(src), "index.EnsureForSearchStale(") {
		t.Error("mcp.go no longer reads through the non-blocking path at all")
	}
}

// With the index lock held for longer than any client would wait, remember
// still answers, still writes the note, and says why it is not findable yet.
func TestRememberAnswersWhileTheIndexIsLocked(t *testing.T) {
	dir := seedRecallPage(t)
	unlock := holdIndexLock(t, dir)
	defer unlock()

	done := make(chan string, 1)
	go func() {
		out, err := callMCPTool(dir, "remember", []byte(`{"text":"the wobble pool cap is 12","project":"proj"}`))
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		done <- out
	}()
	select {
	case out := <-done:
		if !strings.Contains(out, "emembered") && !strings.Contains(out, "Saved") {
			t.Errorf("remember answered something else while the index was locked: %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("remember waited on the index lock instead of answering")
	}

	notes := os.Getenv("DEJA_NOTES_FILE")
	body, err := os.ReadFile(notes)
	if err != nil {
		t.Fatalf("the note was not written: %v", err)
	}
	if !strings.Contains(string(body), "wobble pool cap") {
		t.Errorf("the note is missing from %s:\n%s", notes, body)
	}
}

// A read-only index answers every question asked of it. The non-blocking
// Ensure must not read "cannot write the lock file" as "a rebuild is running",
// or remember tells the agent to come back later, forever.
func TestRememberOnAReadOnlyIndexDoesNotPromiseARefresh(t *testing.T) {
	dir := seedRecallPage(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make the index read-only here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere, so the read-only case cannot be built")
	}
	out, err := callMCPTool(dir, "remember", []byte(`{"text":"the wobble pool cap is 12","project":"proj"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "refreshing its index") {
		t.Errorf("a read-only index was reported as a refresh in flight: %q", out)
	}
}

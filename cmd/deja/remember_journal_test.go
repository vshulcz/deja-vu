package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// rememberEvents counts what the journal recorded for the write tool.
func rememberEvents(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(usage.Path(dir))
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var ev struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Kind == usage.KindRemember {
			n++
		}
	}
	return n
}

// `deja log` is where the user sees what an agent did with their store, and the
// comment above the recorder says a write belongs there at least as much as a
// read. It is written on one of the tool's three success paths: when the index
// is busy — a rebuild running, a warmup just asked for — the note reaches the
// disk and the journal never hears about it.
//
// The busy path is not an edge case: the tool asks for a warmup itself, and an
// agent that stores several facts in a row walks straight into it.
func TestRememberIsJournalledEvenWhenTheIndexIsBusy(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := callMCPTool(dir, "remember", json.RawMessage(`{"text":"the pool cap stays at 40 because pgbouncer pools by transaction"}`)); err != nil {
		t.Fatal(err)
	}
	quiet := rememberEvents(t, dir)
	if quiet != 1 {
		t.Fatalf("the quiet path recorded %d events, so the busy one below cannot be compared", quiet)
	}

	// Now the state the tool itself creates when it asks for a warmup.
	sentinel := filepath.Join(dir, "warmup.sentinel")
	if err := os.WriteFile(sentinel, []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := callMCPTool(dir, "remember", json.RawMessage(`{"text":"the retry budget settled at five attempts with jitter"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Saved") {
		t.Fatalf("the busy path did not report the note as saved: %q", out)
	}
	// The note is on disk — that is what "Saved" means — so the journal has to
	// carry it too, or `deja log` under-reports every write made while the
	// index happened to be rebuilding.
	if got := rememberEvents(t, dir); got != quiet+1 {
		t.Errorf("a note saved while the index was busy is missing from the journal: %d events, want %d\n%s",
			got, quiet+1, fmt.Sprintf("tool said: %q", out))
	}

	// And the other side: asking to remember the same fact again stores
	// nothing, so it must not add an event either — a journal that counts
	// repeats over-reports what the agent actually put in the store.
	before := rememberEvents(t, dir)
	again, err := callMCPTool(dir, "remember", json.RawMessage(`{"text":"the retry budget settled at five attempts with jitter"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "Already remembered") {
		t.Fatalf("the fixture no longer repeats a note, so this guards nothing: %q", again)
	}
	if got := rememberEvents(t, dir); got != before {
		t.Errorf("a repeat of a note already stored was journalled as a write: %d events, want %d", got, before)
	}
}

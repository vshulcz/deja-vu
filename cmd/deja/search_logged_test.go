package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// deja log is the audit of what deja did, and three places say a search shows
// up in it: the docs ("`search` and `handoff` are the reader's own commands"),
// the comment over the kind constants, and the empty-log line itself. No path
// has ever written one (#2471).
func TestASearchLandsInTheLog(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := strings.Repeat("the pool checkout dominated the p99 of the orders pipeline ", 20)
	lines := []string{
		`{"type":"user","sessionId":"p1","timestamp":"` + at + `","cwd":"/work/app","message":{"role":"user","content":"why is the p99 high? ` + body + `"}}`,
		`{"type":"assistant","sessionId":"p1","timestamp":"` + at + `","cwd":"/work/app","message":{"role":"assistant","content":"the shared pool is the p99 ` + body + `"}}`,
	}
	if err := os.WriteFile(filepath.Join(store, "p1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	if err := runSearch(dir, []string{"pool", "p99"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := runSearch(dir, []string{"zzzznothinghere"}, ""); err != nil {
		t.Fatal(err)
	}

	var found, empty int
	for _, e := range eventsOf(dir) {
		if e.Kind != usage.KindSearch {
			continue
		}
		if e.Empty {
			empty++
			continue
		}
		found++
		if e.Sessions == 0 || e.Bytes == 0 {
			t.Errorf("a search that matched recorded nothing about it: %#v", e)
		}
	}
	if found != 1 || empty != 1 {
		t.Errorf("two searches, one with hits and one without, left %d matched and %d empty events in the log", found, empty)
	}
}

func eventsOf(dir string) []usage.Event {
	events, _ := usage.EventsCounted(dir, 50)
	return events
}

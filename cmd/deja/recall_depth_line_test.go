package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A recall page ends by offering the only move it knows: more sessions. On a
// store of long sessions that is the wrong move — the answer is inside the
// session already on the page, in one of the matches that did not fit, and
// paging sideways never reaches it. Measured on a live harness over six
// questions whose answer sits in a session of hundreds of matches: three to
// seven tool calls each, all but one of them a re-worded recall rather than a
// read of the session already found, and the figure asked for was in the
// served excerpts twice of six.
func TestARecallPageWithMoreInOneSessionSaysToOpenIt(t *testing.T) {
	hermeticEnv(t)
	marathonCorpus(t)

	got := mcpRecallText(t, "pgbouncer pool")
	if !strings.Contains(got, "recall_context") {
		t.Errorf("the page never names the move that reads the rest of the session:\n%s", head(got))
	}
	if !strings.Contains(got, "marathon") {
		t.Errorf("the page does not name which session to open:\n%s", head(got))
	}
}

// And not on a page whose sessions each matched once: there is nothing more of
// them to read, and the line would be a standing instruction rather than an
// answer about this page.
func TestAThinPageDoesNotSayToOpenAnything(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("thin%02d", i)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"the pgbouncer pool dropped connections once"}}`,
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	got := mcpRecallText(t, "pgbouncer pool")
	if strings.Contains(got, "recall_context") {
		t.Errorf("a page of single matches still tells the agent to open one:\n%s", head(got))
	}
}

// One session that matched many times, plus filler so the page has somewhere
// else to go.
func marathonCorpus(t *testing.T) {
	t.Helper()
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	lines := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		lines = append(lines, `{"type":"user","sessionId":"marathon","timestamp":"`+at+
			`","cwd":"/tmp/app","message":{"role":"user","content":"the pgbouncer pool kept dropping connections, note `+
			fmt.Sprint(i)+`"}}`)
	}
	writeClaudeFixture(t, filepath.Join(root, "-tmp-app", "marathon.jsonl"), "marathon", lines)
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("f%02d", i)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"standup chatter about the pool number ` + fmt.Sprint(i) + `"}}`,
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
}

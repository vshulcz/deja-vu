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

// The session-start block is three pieces from three builders: the project's
// standing decisions, the recalled sessions, and what the machine keeps
// tripping over. 439 and 440 both turned out to be a first piece deciding what
// the second one got, so this holds the composition where all three have
// something to say: every part arrives, and the whole stays small enough to
// sit at the top of a session (#2465).
func TestTheSessionStartBlockCarriesAllThreePieces(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	// The hook reads the project from where it stands, so the sessions have to
	// be recorded for this directory: the store's name is the path with its
	// separators turned into dashes, which is how every harness writes it.
	work := filepath.Join(tmp, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	store := filepath.Join(root, strings.ReplaceAll(work, string(filepath.Separator), "-"))
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	project := strings.TrimPrefix(filepath.ToSlash(work), "/")
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)

	at := func(min int) string {
		return time.Now().Add(-time.Duration(min) * time.Minute).UTC().Format(time.RFC3339)
	}
	wall := "psql: error: connection refused while starting the migration runner pool"
	var lines []string
	for k := 0; k < 4; k++ {
		sid := fmt.Sprintf("w%d", k)
		lines = []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"`+work+`","message":{"role":"user","content":"the migration runner keeps stalling on the pool"}}`, sid, at(300-10*k)),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"`+work+`","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -h db.internal -c 'select 1'"}}]}}`, sid, at(299-10*k)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"`+work+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":%q}]}}`, sid, at(298-10*k), wall),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"`+work+`","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"brew services start postgresql@16"}}]}}`, sid, at(297-10*k)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"`+work+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"==> Successfully started postgresql@16"}]}}`, sid, at(296-10*k)),
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	note := `{"ts":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","project":"` + project + `","kind":"promoted",` +
		`"session":"claude:w0","state":"accepted","title":"the migration runner keeps stalling on the pool",` +
		`"text":"the retry budget stays at 5 after the pool change"}`
	if err := os.WriteFile(notes, []byte(note+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	block := hookContextBlock(t, dir)
	for what, needle := range map[string]string{
		"the standing decision": "retry budget stays at 5",
		"a recalled session":    "recalled from",
		"the wall":              "separate sessions hit",
		"the remedy":            "brew services start postgresql@16",
	} {
		if !strings.Contains(block, needle) {
			t.Errorf("%s is missing from the session start:\n%s", what, block)
		}
	}
	// Three builders, one block, and it opens a session: a page of text here is
	// paid for on every start.
	if len(block) > 8<<10 {
		t.Errorf("the block is %d bytes", len(block))
	}
}

// hookContextBlock runs the session-start hook and returns the context it
// injected.
func hookContextBlock(t *testing.T, dir string) string {
	t.Helper()
	out := captureHookStdout(t, func() {
		if err := runHookContext(dir, true); err != nil {
			t.Fatal(err)
		}
	})
	return out
}

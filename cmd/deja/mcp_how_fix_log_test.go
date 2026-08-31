package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja log` is the record of what an agent was handed, and the two tools that
// hand over commands left no trace in it — the argument #682 made for blame,
// unapplied to the surfaces that answer with something to run (#2858).
func TestHowAndFixOverMCPAreRecorded(t *testing.T) {
	dir := seedHowFixIndex(t)
	for _, c := range []struct {
		tool string
		args string
		kind string
	}{
		{"how", `{"what":"pytest"}`, usage.KindHow},
		{"fix", `{"error":"zsh:1: command not found: timeout"}`, usage.KindFix},
	} {
		t.Run(c.tool, func(t *testing.T) {
			before := len(usage.Snapshots(dir, 100))
			text, err := callMCPTool(dir, c.tool, json.RawMessage(c.args))
			if err != nil {
				t.Fatal(err)
			}
			after := usage.Snapshots(dir, 100)
			if len(after) != before+1 {
				t.Fatalf("snapshots %d -> %d, want one more", before, len(after))
			}
			last := after[0]
			if last.Kind != c.kind {
				t.Errorf("kind = %q, want %q", last.Kind, c.kind)
			}
			if last.Bytes != len(text) {
				t.Errorf("recorded %d bytes, handed over %d", last.Bytes, len(text))
			}
			if last.Digest != text {
				t.Errorf("digest is not what was handed over:\n%q\n%q", last.Digest, text)
			}
			// The count, because a row with none is marked as having found
			// nothing — and these answers served a command.
			if last.Sessions == 0 {
				t.Errorf("an answer that served a command was recorded as finding nothing: %#v", last)
			}
			// And the log agrees with itself: the event a reader sees must
			// not say "(empty result)" over an answer that served something.
			for _, e := range usage.Events(dir, 100) {
				if e.Kind == c.kind && e.FoundNothing() {
					t.Errorf("the log says the answer found nothing: %#v", e)
					break
				}
			}
		})
	}
}

// seedHowFixIndex is a store where both tools have something to answer with: a
// command run in two sessions, and an error a later command settled.
func seedHowFixIndex(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-work-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s2"} {
		lines := []string{
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/api","timestamp":"2026-07-0` +
				id[1:2] + `T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"pytest -q tests/"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/api","timestamp":"2026-07-0` +
				id[1:2] + `T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"zsh:1: command not found: timeout"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/api","timestamp":"2026-07-0` +
				id[1:2] + `T10:00:02Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"brew install coreutils"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/api","timestamp":"2026-07-0` +
				id[1:2] + `T10:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"==> Installing coreutils"}]}}`,
		}
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

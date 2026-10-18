package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A source that went on after the fork is no longer the fork's own copy, so
// since #5336 it stays in recall cut at the fork point. The note before an
// edit still counted it whole, and told the fork its source had edited the
// file when the only edit was in the turns the fork copied (#5341).
func TestAForksEditNoteCountsItsSourceOnlyForEditsAfterTheFork(t *testing.T) {
	for _, tc := range []struct {
		name      string
		laterEdit bool
		want      string
	}{
		{"source edits only in the copied turns", false, ""},
		{"source edits the file again after the fork", true, "ledger.go has been worked on in 2 sessions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			store := filepath.Join(tmp, "claude", "-work-app")
			if err := os.MkdirAll(store, 0o755); err != nil {
				t.Fatal(err)
			}
			t0 := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Millisecond).Add(321 * time.Millisecond)
			rec := func(sid, uuid, role string, at time.Duration, content any) string {
				b, err := json.Marshal(map[string]any{"type": role, "uuid": uuid, "sessionId": sid, "cwd": "/work/app",
					"timestamp": t0.Add(at).Format(time.RFC3339Nano), "message": map[string]any{"role": role, "content": content}})
				if err != nil {
					t.Fatal(err)
				}
				return string(b) + "\n"
			}
			edit := func(old string) any {
				return []any{map[string]any{"type": "tool_use", "name": "Edit",
					"input": map[string]any{"file_path": "/work/app/ledger.go", "old_string": old, "new_string": "y"}}}
			}
			write := func(sid, body string) {
				if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write("d1", rec("d1", "d-1", "user", -time.Hour, "make the ledger append-only")+
				rec("d1", "d-2", "assistant", -time.Hour+time.Minute, edit("v0"))+
				rec("d1", "d-3", "assistant", -time.Hour+2*time.Minute, "We decided ledger.go keeps entries append-only because rewrites lost rows."))
			const source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			const fork = "ffffffff-ffff-4fff-8fff-ffffffffffff"
			turns := func(sid string) string {
				return rec(sid, "a-1", "user", 0, "flush the ledger after every append") +
					rec(sid, "a-2", "assistant", time.Minute, edit("v1")) +
					rec(sid, "a-3", "assistant", 2*time.Minute, "We decided ledger.go flushes after every append because a crash lost the tail.")
			}
			// The source goes on after the fork, so the fork does not hold its end.
			later := rec(source, "s-1", "user", 2*time.Hour, "now check the release notes")
			if tc.laterEdit {
				later += rec(source, "s-2", "assistant", 2*time.Hour+time.Minute, edit("v2"))
			}
			later += rec(source, "s-3", "assistant", 2*time.Hour+2*time.Minute, "We decided ledger.go flushes after every append because a crash lost the tail.")
			write(source, turns(source)+later)
			write(fork, turns(fork)+rec(fork, "f-1", "user", time.Hour, "now rename the flush helper"))
			dir := index.DefaultDir()
			if err := index.Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			line := func(sid string) string {
				in := toolHookInput{SessionID: sid, HookEventName: "PreToolUse", ToolName: "Edit",
					TranscriptPath: filepath.Join(store, sid+".jsonl")}
				in.ToolInput.FilePath = "/work/app/ledger.go"
				return toolHookLine(dir, "/work/app", in)
			}
			if got := line("someone-else"); !strings.Contains(got, "ledger.go has been worked on in 3 sessions") {
				t.Fatalf("the control got no note on three sessions, so this proves nothing: %q", got)
			}
			got := line(fork)
			if tc.want == "" && got != "" {
				t.Errorf("the fork was told about its source's copied edit: %q", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("the source's edit after the fork was not counted: %q, want %q", got, tc.want)
			}
		})
	}
}

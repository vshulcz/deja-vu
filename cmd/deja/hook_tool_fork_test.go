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

// The note before an edit in a forked Claude Code session counted the session
// it was forked from and quoted that session's decision, which the fork already
// holds as its own turns (#5123). The digest, per-prompt recall and MCP recall
// leave the source out since #4549.
func TestAForksEditNoteLeavesOutItsSource(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond).Add(321 * time.Millisecond)
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
	write(source, turns(source))
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
	// Control: another session is told about all three.
	if got := line("someone-else"); !strings.Contains(got, "ledger.go has been worked on in 3 sessions") {
		t.Fatalf("the control got no note on three sessions, so this proves nothing: %q", got)
	}
	// The fork has d1 behind it and its own copy of the source: one session,
	// below the two the note needs.
	if got := line(fork); got != "" {
		t.Errorf("a fork's edit note counted its own source: %q", got)
	}
}

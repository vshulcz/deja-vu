package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// sessions.json holds a key's current window only. After /new, a child
// spawned from the old window resolved to the new one, a session started two
// hours after it (#5307). The spawn call's result in the old window names the
// child's key, which is what the parent is found by.
func TestOpenClawJSONLChildKeepsTheWindowThatSpawnedIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// The spawn result as OpenClaw 2026.7 writes it: JSON text in a toolResult.
	spawn := `{"type":"message","id":"r","timestamp":"2026-07-20T08:59:59Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"sessions_spawn","content":[{"type":"text","text":"{\"status\":\"accepted\",\"childSessionKey\":\"agent:main:subagent:1\",\"runId\":\"r1\"}"}]}}`
	write("p.jsonl", `{"type":"session","version":3,"id":"p","timestamp":"2026-07-20T08:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T08:59:58Z","message":{"role":"user","content":"spawn a helper"}}
`+spawn+"\n")
	kid := write("kid.jsonl", `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:00:01Z","message":{"role":"user","content":"child task"}}
`)
	parentOf := func() string {
		t.Helper()
		got, err := ParseOpenClawFile(kid)
		if err != nil || len(got) != 1 {
			t.Fatalf("kid: %v %v", got, err)
		}
		return got[0].Parent
	}
	write("sessions.json", `{"agent:main:main":{"sessionId":"p"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
	if got := parentOf(); got != "p" {
		t.Fatalf("before /new: parent %q, want p", got)
	}

	// A window from before the child that only mentions its key in a turn
	// did not spawn it.
	write("p0.jsonl", `{"type":"session","version":3,"id":"p0","timestamp":"2026-07-20T08:30:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T08:30:01Z","message":{"role":"user","content":"is agent:main:subagent:1\" a key?"}}
`)
	// /new: a new window under the same key, which mentions the child too.
	write("p2.jsonl", `{"type":"session","version":3,"id":"p2","timestamp":"2026-07-20T11:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T11:00:01Z","message":{"role":"user","content":"what did agent:main:subagent:1\" do?"}}
`)
	write("sessions.json", `{"agent:main:main":{"sessionId":"p2"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
	if got := parentOf(); got != "p" {
		t.Errorf("after /new: parent %q, want p, the window that spawned it", got)
	}

	// The old window archived by a reset is still the spawner.
	if err := os.Rename(filepath.Join(dir, "p.jsonl"), filepath.Join(dir, "p.jsonl.reset.2026-07-20T10-59-00.000Z")); err != nil {
		t.Fatal(err)
	}
	write("sessions.json", `{"agent:main:main":{"sessionId":"p2"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"} }`)
	if got := parentOf(); got != "p" {
		t.Errorf("after a reset archived it: parent %q, want p", got)
	}

	// Nothing names the child: no parent, rather than a window opened after it.
	if err := os.Remove(filepath.Join(dir, "p.jsonl.reset.2026-07-20T10-59-00.000Z")); err != nil {
		t.Fatal(err)
	}
	write("sessions.json", `{"agent:main:main":{"sessionId":"p2"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}  `)
	if got := parentOf(); got != "" {
		t.Errorf("with no spawner on disk: parent %q, want none", got)
	}
}

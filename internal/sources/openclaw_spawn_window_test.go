package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// Resolving a child spawned before a /new read every transcript in the
// directory, again for each such child and again on every sessions.json
// rewrite, which OpenClaw does each turn: 60 children by 300 transcripts took
// 12 s for one parse (#5332). The transcripts are read once between them.
func TestOpenClawSpawnWindowReadsEachTranscriptOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const kids, others = 20, 30
	store := `{"agent:main:main":{"sessionId":"cur"}`
	old := `{"type":"session","version":3,"id":"old","timestamp":"2026-07-20T08:00:00Z"}` + "\n"
	for i := 0; i < kids; i++ {
		old += fmt.Sprintf(`{"type":"message","id":"r%d","timestamp":"2026-07-20T08:10:00Z","message":{"role":"toolResult","toolName":"sessions_spawn","content":[{"type":"text","text":"{\\"childSessionKey\\":\\"agent:main:subagent:%d\\"}"}]}}`+"\n", i, i)
		write(fmt.Sprintf("kid%d.jsonl", i), fmt.Sprintf(`{"type":"session","version":3,"id":"kid%d","timestamp":"2026-07-20T09:00:00Z"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:00:01Z","message":{"role":"user","content":"child task"}}
`, i))
		store += fmt.Sprintf(`,"agent:main:subagent:%d":{"sessionId":"kid%d","spawnedBy":"agent:main:main"}`, i, i)
	}
	write("old.jsonl", old)
	write("cur.jsonl", `{"type":"session","version":3,"id":"cur","timestamp":"2026-07-20T11:00:00Z"}`+"\n")
	for i := 0; i < others; i++ {
		write(fmt.Sprintf("o%d.jsonl", i), fmt.Sprintf(`{"type":"session","version":3,"id":"o%d","timestamp":"2026-07-19T08:00:00Z"}`+"\n", i))
	}
	write("sessions.json", store+"}")
	files := int64(kids + others + 2)

	parse := func(i int) {
		t.Helper()
		got, err := ParseOpenClawFile(filepath.Join(dir, fmt.Sprintf("kid%d.jsonl", i)))
		if err != nil || len(got) != 1 || got[0].Parent != "old" {
			t.Fatalf("kid%d: %+v %v, want parent old", i, got, err)
		}
	}
	before := openclawScanReads.Load()
	parse(0)
	if n := openclawScanReads.Load() - before; n > files {
		t.Errorf("one parse read %d transcripts, want at most the %d in the directory", n, files)
	}
	// OpenClaw rewrites sessions.json every turn; that alone reads nothing again.
	before = openclawScanReads.Load()
	for i := 0; i < kids; i++ {
		now := time.Now().Add(time.Duration(i+1) * time.Second)
		if err := os.Chtimes(filepath.Join(dir, "sessions.json"), now, now); err != nil {
			t.Fatal(err)
		}
		parse(i)
	}
	if n := openclawScanReads.Load() - before; n != 0 {
		t.Errorf("%d parses after sessions.json rewrites read %d transcripts again, want 0", kids, n)
	}
	// A transcript that grew is read again, and only that one, with or
	// without a sessions.json rewrite.
	write("cur.jsonl", `{"type":"session","version":3,"id":"cur","timestamp":"2026-07-20T11:00:00Z"}`+"\n"+`{"type":"message","id":"a","timestamp":"2026-07-20T11:00:01Z","message":{"role":"user","content":"next"}}`+"\n")
	before = openclawScanReads.Load()
	parse(1)
	if n := openclawScanReads.Load() - before; n != 1 {
		t.Errorf("after one transcript changed a parse read %d, want 1", n)
	}

	// A spawn result that lands after the parse, with sessions.json as it
	// was, still moves the child to the window that names it.
	write("mid.jsonl", `{"type":"session","version":3,"id":"mid","timestamp":"2026-07-20T08:30:00Z"}`+"\n")
	parse(2)
	write("mid.jsonl", `{"type":"session","version":3,"id":"mid","timestamp":"2026-07-20T08:30:00Z"}`+"\n"+
		`{"type":"message","id":"r","timestamp":"2026-07-20T08:40:00Z","message":{"role":"toolResult","toolName":"sessions_spawn","content":[{"type":"text","text":"{\\"childSessionKey\\":\\"agent:main:subagent:2\\"}"}]}}`+"\n")
	got, err := ParseOpenClawFile(filepath.Join(dir, "kid2.jsonl"))
	if err != nil || len(got) != 1 || got[0].Parent != "mid" {
		t.Errorf("kid2 after a later spawn result: %+v %v, want parent mid", got, err)
	}
}

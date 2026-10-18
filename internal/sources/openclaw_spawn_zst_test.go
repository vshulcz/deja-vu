package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A spawner window archived compressed was never opened by the lookup, so its
// child got no parent (#5340). It is read like the plain archive, and once.
func TestOpenClawChildKeepsASpawnerArchivedAsZst(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd CLI not installed")
	}
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
	spawn := `{"type":"message","id":"r","timestamp":"2026-07-20T08:59:59Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"sessions_spawn","content":[{"type":"text","text":"{\"status\":\"accepted\",\"childSessionKey\":\"agent:main:subagent:1\",\"runId\":\"r1\"}"}]}}`
	plain := write("p.jsonl.deleted.2026-07-20T10-59-00.000Z", `{"type":"session","version":3,"id":"p","timestamp":"2026-07-20T08:00:00Z","cwd":"/work/app"}
`+spawn+"\n")
	if out, err := exec.Command("zstd", "-q", "--rm", plain).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v: %s", err, out)
	}
	kid := write("kid.jsonl", `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:00:01Z","message":{"role":"user","content":"child task"}}
`)
	write("p2.jsonl", `{"type":"session","version":3,"id":"p2","timestamp":"2026-07-20T11:00:00Z","cwd":"/work/app"}`+"\n")
	store := write("sessions.json", `{"agent:main:main":{"sessionId":"p2"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
	parentOf := func() string {
		t.Helper()
		got, err := ParseOpenClawFile(kid)
		if err != nil || len(got) != 1 {
			t.Fatalf("kid: %v %v", got, err)
		}
		return got[0].Parent
	}
	// Without zstd the archive is left unread and not remembered, so the
	// first parse with zstd installed finds the spawner.
	zstdPin.mu.Lock()
	prevPinned, prevOK := zstdPin.pinned, zstdPin.ok
	zstdPin.pinned, zstdPin.ok = true, false
	zstdPin.mu.Unlock()
	got := parentOf()
	zstdPin.mu.Lock()
	zstdPin.pinned, zstdPin.ok = prevPinned, prevOK
	zstdPin.mu.Unlock()
	if got != "" {
		t.Fatalf("without zstd: parent %q, want none", got)
	}
	if got := parentOf(); got != "p" {
		t.Fatalf("spawner archived as .zst: parent %q, want p", got)
	}
	before := openclawScanReads.Load()
	now := time.Now().Add(time.Second)
	if err := os.Chtimes(store, now, now); err != nil {
		t.Fatal(err)
	}
	if got := parentOf(); got != "p" {
		t.Fatalf("after a sessions.json rewrite: parent %q, want p", got)
	}
	if n := openclawScanReads.Load() - before; n != 0 {
		t.Errorf("a sessions.json rewrite read %d transcripts again, want 0", n)
	}
}

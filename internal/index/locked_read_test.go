package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A rebuild holds the index lock, and rebuilds happen on their own schedule —
// after an upgrade, in the background, on the first run of a new format. The
// read paths a hook uses take that lock without blocking on purpose, because
// waiting would stall the agent for the whole rebuild. Nothing checked either
// half of that: not that the read still answers, and not that it returns at
// all rather than waiting.
func TestReadsAnswerWhileTheIndexLockIsHeld(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z",` +
		`"message":{"role":"user","content":"the quetzalcoatl migration"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Hold it the way a rebuild does.
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("could not take the lock this test is about")
	}
	defer unlock()

	done := make(chan struct{})
	var got int
	go func() {
		defer close(done)
		sessions, _, _, _, rerr := ProjectRelevant(dir, []string{"app"},
			[]string{"quetzalcoatl"}, 2)
		if rerr != nil {
			t.Errorf("read under the lock: %v", rerr)
		}
		got = len(sessions)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the read waited on the lock; a hook doing this stalls the agent " +
			"for the length of a rebuild")
	}
	if got == 0 {
		t.Error("the read returned nothing while a rebuild held the lock, so " +
			"recall goes silent for the length of every rebuild")
	}
}

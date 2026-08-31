package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// A measurement, not a guard: the session-start hook records an empty session
// id on Windows and nowhere else (#2023), and the payload it reads there has
// never been looked at. This reports what arrives, so the branch that fixes it
// starts from the shape rather than from a guess. It fails on purpose — the
// suite prints nothing about a passing test — and does not belong on a
// long-lived branch.
func TestWindowsHookPayloadProbe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the probe is about Windows")
	}
	payload := `{"source":"startup","session_id":"ses_probe","cwd":"C:\\work"}`

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	start := time.Now()
	got := readHookStdin()
	took := time.Since(start)
	os.Stdin = old
	_ = r.Close()

	var input struct {
		Source    string `json:"source"`
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
	}
	unmarshalErr := json.Unmarshal(got, &input)

	direct := readHookPayload(strings.NewReader(payload), hookStdinWait)

	t.Errorf("probe: wait=%v via-stdin bytes=%d took=%v id=%q err=%v raw=%q | via-reader bytes=%d raw=%q",
		hookStdinWait, len(got), took, input.SessionID, unmarshalErr, string(got), len(direct), string(direct))

	// And the hook itself, which is where the empty id is recorded.
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	stale := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "probeterm", []string{
		`{"type":"user","sessionId":"probeterm","timestamp":"` + stale +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	withHookStdin(t, hookPayload(t, map[string]string{"source": "startup", "session_id": "ses_probe", "cwd": cwd}))
	served := captureStdout(t, func() { runHookContextPlain(t) })
	log, readErr := os.ReadFile(usage.SnapshotPath(index.DefaultDir()))
	t.Errorf("probe hook: injected=%d bytes, log err=%v, log=%s", len(served), readErr, log)
}

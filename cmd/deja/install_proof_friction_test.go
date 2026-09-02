package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The proof after `deja install --auto` listed three recent sessions — titles
// the reader typed last week and already knows. What they do not know is the
// error their agents have hit in a dozen separate sessions, which is the one
// thing the index holds that they never kept score of. That is the line the
// install shows now, and it is the same rule `friction` prints by (#2966).
func TestInstallProofNamesTheRecurringError(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// One error across four sessions, one session that is fine.
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("s%d", i)
		writeClaudeFixture(t, filepath.Join(root, id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-07-2` + fmt.Sprint(i) + `T10:00:00Z","message":{"role":"user","content":"deploy it"}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-07-2` + fmt.Sprint(i) + `T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"zsh: command not found: timeout"}]}}`,
		})
	}
	writeClaudeFixture(t, filepath.Join(root, "ok.jsonl"), "ok", []string{
		`{"type":"user","sessionId":"ok","cwd":"/w/p","timestamp":"2026-07-28T10:00:00Z","message":{"role":"user","content":"all green today"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, func() { printInstallProof(index.DefaultDir()) })
	if !strings.Contains(out, "command not found: timeout") || !strings.Contains(out, "4 sessions") {
		t.Errorf("the proof does not name the error this machine keeps hitting:\n%s", out)
	}
	// Above the listing, not below it: the listing is what the reader already
	// knows, and the line is the reason to keep reading.
	if at, list := strings.Index(out, "command not found"), strings.Index(out, "[claude"); at < 0 || list < 0 || at > list {
		t.Errorf("the recurring error sits below the session listing:\n%s", out)
	}
}

// And nothing when nothing recurs — a line that says "this machine keeps
// hitting …" over a single sighting would be the tool inventing a pattern.
func TestInstallProofStaysQuietWithoutARecurringError(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := captureStderr(t, func() { printInstallProof(index.DefaultDir()) })
	if strings.Contains(out, "keeps hitting") || strings.Contains(out, "has hit") {
		t.Errorf("the proof claims a recurring error on a store that has none:\n%s", out)
	}
	if !strings.Contains(out, "deja already knows this machine:") {
		t.Errorf("the listing itself went missing:\n%s", out)
	}
}

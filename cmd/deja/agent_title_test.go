package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A session with no user turn borrows the assistant's opening line (#692), and
// the listing printed it in the place of the reader's own question — an
// assertion nobody made reading like something they said (#1100).
func TestTheListingSaysWhenATitleIsTheAgentsOwnWords(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	asked := `{"type":"user","message":{"role":"user","content":"how do we rotate the vault keys"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"withq","cwd":"/proj"}` + "\n"
	said := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"the vault rotation now runs weekly"}]},"timestamp":"2026-08-01T10:00:00Z","sessionId":"noq","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(asked), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "b.jsonl"), []byte(said), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		switch {
		case strings.Contains(line, "noq"):
			if !strings.Contains(line, "agent: the vault rotation") {
				t.Errorf("the fallback title is not marked as the agent's: %q", line)
			}
		case strings.Contains(line, "withq"):
			if strings.Contains(line, "agent:") {
				t.Errorf("a real question was marked as the agent's: %q", line)
			}
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `ctx` prints `## assistant` and the hook digest prints `- Assistant:`, but a
// recall answer is a bare list of lines — so a model's own past assertion
// arrived as an unattributed fact from the store (#1107).
func TestRecallSaysWhenASessionHasNoHumanTurn(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	said := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"the retry budget is now ten attempts"}]},"timestamp":"2026-08-01T10:00:00Z","sessionId":"agentopened","cwd":"/proj"}` + "\n"
	asked := `{"type":"user","message":{"role":"user","content":"what did we set the retry budget to"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"asked","cwd":"/proj"}` + "\n"
	for name, body := range map[string]string{"a.jsonl": said, "b.jsonl": asked} {
		if err := os.WriteFile(filepath.Join(store, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	answer, err := recallText(dir, "retry budget", "", 5, 8192)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(answer, "\n") {
		switch {
		case strings.Contains(line, "agentopened"):
			if !strings.Contains(line, "agent-opened, no human turn") {
				t.Errorf("an agent-opened session is not marked: %q", line)
			}
		case strings.Contains(line, "· asked ·"):
			if strings.Contains(line, "agent-opened") {
				t.Errorf("a session the reader typed into was marked as the agent's: %q", line)
			}
		}
	}
}

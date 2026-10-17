package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Copilot CLI writes session.start before the first prompt, and a build that
// lands in between (the prompt hook's) stores the file's offset with no
// session. The rest arrived as a tail with no project and the prompt's time as
// the start, and the session dropped out of its project's recall (#5136).
func TestACopilotSessionIndexedBeforeItsFirstPromptMatchesRebuild(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_COPILOT_ROOT": "copilot"})
	const id = "0199cccc-0000-7000-8000-000000000002"
	p := filepath.Join(tmp, "copilot", id, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	head := `{"type":"session.start","timestamp":"2026-07-17T09:00:00Z","data":{"sessionId":"` + id + `","startTime":"2026-07-17T09:00:00Z","context":{"cwd":"/w/proj"}}}` + "\n"
	if err := os.WriteFile(p, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "idx.db")
	parityPass(t, dir, false)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"user.message","timestamp":"2026-07-17T09:00:07Z","data":{"content":"fix the retry loop"}}` + "\n" +
		`{"type":"assistant.message","timestamp":"2026-07-17T09:00:09Z","data":{"content":"capped it at five"}}` + "\n")
	_ = f.Close()
	parityPass(t, dir, false)
	matchesRebuild(t, dir, "copilot", id)
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// recall_frame.go states the rule: recalled transcript text is data an
// attacker may have influenced, so agent-facing output is framed as untrusted.
// `how` is agent-facing and returns the most directly actionable thing deja
// serves — command lines lifted out of transcripts — and it was the one MCP
// answer with no frame and no note at all.
func TestTheHowToolFramesWhatItServes(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"s1","cwd":"/app","timestamp":"2026-08-01T09:00:00Z","message":{"role":"user","content":"how do we deploy"}}
{"type":"assistant","sessionId":"s1","cwd":"/app","timestamp":"2026-08-01T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"helm upgrade zonkomatic --set replicas=9"}}]}}
`
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"zonkomatic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "helm upgrade zonkomatic") {
		t.Fatalf("the command was not served, so there is nothing to frame:\n%s", text)
	}
	if !strings.Contains(text, "untrusted reference data") {
		t.Errorf("a command drawn from a transcript arrives with nothing saying where it came from:\n%s", text)
	}
}

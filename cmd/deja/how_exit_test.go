package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `deja how` counts its own rows over the record log, so it split a command by
// the outcome codex and opencode append to it: "$ make test" and
// "$ make test  → exit 2" as two lines of one screen (#2590).
func TestHowCountsACommandOnceWhateverItExitedWith(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CODEX_ROOT", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	line := func(text string, min int) string {
		return `{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"` + text +
			`\"]}","timestamp":"` + at.Add(time.Duration(min)*time.Minute).Format(time.RFC3339) + `"}`
	}
	_ = line
	// The reader-visible shape is what matters here, so write the records the
	// way the opencode reader produces them: the command line carries the code.
	body := ""
	for i, text := range []string{"$ make test", "$ make test  → exit 2", "$ make test  → exit 2"} {
		body += `{"type":"user","sessionId":"s1","cwd":"/w/app","timestamp":"` +
			at.Add(time.Duration(i)*time.Minute).Format(time.RFC3339) +
			`","message":{"role":"user","content":[{"type":"tool_use","name":"Bash","input":{"command":"` +
			strings.TrimPrefix(text, "$ ") + `"}}]}}` + "\n"
	}
	claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "how", "make test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "make test") {
		t.Fatalf("how found nothing, so this measures nothing:\n%s", out)
	}
	if strings.Contains(out, "exit") {
		t.Errorf("the outcome is on the screen as part of the command:\n%s", out)
	}
	if n := strings.Count(out, "$ make test"); n != 1 {
		t.Errorf("one command on %d rows:\n%s", n, out)
	}
}

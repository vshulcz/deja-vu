package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// writeHowRuns writes one session per command text, so each run counts as a
// separate session the way `how` weighs them.
func writeHowRuns(t *testing.T, texts []string) {
	t.Helper()
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	dir := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, text := range texts {
		id := "s" + string(rune('1'+i))
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` +
			at.Add(time.Duration(i)*time.Hour).Format(time.RFC3339) +
			`","message":{"role":"user","content":[{"type":"tool_use","name":"Bash","input":{"command":"` +
			text + `"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// `deja how` strips the outcome codex and opencode append so one command is
// one row (#2590), and then looked at it no further: a command whose every
// recorded run on this machine failed was offered as the way it is done here,
// in the same shape as one that has always worked (#2624).
func TestHowSaysWhenEveryRecordedRunFailed(t *testing.T) {
	hermeticEnv(t)
	writeHowRuns(t, []string{"npm run build  → exit 127", "npm run build  → exit 127"})
	out, err := captureRun(t, "how", "npm run build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Fatalf("the two runs are no longer one command:\n%s", out)
	}
	if !strings.Contains(out, "every run recorded here failed (exit 127)") {
		t.Fatalf("nothing on the line says the command never once worked:\n%s", out)
	}
}

// A command that sometimes fails is ordinary, and a command deja has no
// outcome for is most of the store: neither earns the line.
func TestHowStaysQuietWhenTheHistoryIsNotAllFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		texts []string
	}{
		{"mixed outcomes", []string{"npm run build  → exit 127", "npm run build  → exit 0"}},
		{"one run has no outcome", []string{"npm run build  → exit 127", "npm run build"}},
		{"no outcome at all", []string{"npm run build", "npm run build"}},
		{"every run worked", []string{"npm run build  → exit 0", "npm run build  → exit 0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			writeHowRuns(t, tc.texts)
			out, err := captureRun(t, "how", "npm run build")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "failed") {
				t.Fatalf("the line claims a failing history it does not have:\n%s", out)
			}
		})
	}
}

// The same note over MCP: the agent reading that answer is the one most likely
// to run the command straight back (#2624).
func TestMCPHowSaysWhenEveryRecordedRunFailed(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-h")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for _, id := range []string{"h1", "h2"} {
		body := `{"type":"assistant","sessionId":"` + id + `","cwd":"/w/h","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"npm run build  \u2192 exit 127"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"npm run build"}`))
	if err != nil {
		t.Fatalf("how: %v", err)
	}
	if !strings.Contains(got, "every run recorded here failed (exit 127)") {
		t.Fatalf("the agent is not told the command never once worked:\n%s", got)
	}
}

package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A child run is where the reading and the searching happen and where the
// answer is said; the parent keeps the launch and a summary. Measured on a real
// store, those files held a quarter of the machine's Claude messages and 387
// assistant turns that read like a settled answer, and none of it was in the
// index (#3009). They come in as the task and the answer; the middle, which is
// the volume, stays out.
func TestASubagentIsReadAsItsTaskAndItsAnswer(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "proj", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(role, text string, minute int) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "parent-1", "isSidechain": true, "agentId": "child-1",
			"timestamp": "2026-08-02T10:" + string(rune('0'+minute/10)) + string(rune('0'+minute%10)) + ":00Z",
			"message":   map[string]any{"role": role, "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	body := line("user", "find where the retry budget is set", 0)
	for i := 1; i <= 20; i++ {
		body += line("assistant", "reading another file, nothing here yet", i)
	}
	body += line("assistant", "the retry budget is set in config/retry.go, five attempts", 21)
	path := filepath.Join(sub, "child-1.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the child as one", len(ss))
	}
	s := ss[0]
	if len(s.Messages) > SubagentTailKept+1 {
		t.Fatalf("the whole child run was indexed: %d messages", len(s.Messages))
	}
	joined := strings.Join(func() []string {
		var out []string
		for _, m := range s.Messages {
			out = append(out, m.Text)
		}
		return out
	}(), "\n")
	if !strings.Contains(joined, "find where the retry budget is set") {
		t.Fatalf("the task it was handed is missing:\n%s", joined)
	}
	if !strings.Contains(joined, "config/retry.go") {
		t.Fatalf("the answer it came back with is missing:\n%s", joined)
	}
	if strings.Count(joined, "nothing here yet") > SubagentTailKept {
		t.Fatalf("the middle came in with it:\n%s", joined)
	}

	// The whole run is still one variable away.
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	full, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 1 || len(full[0].Messages) < 20 {
		t.Fatalf("the opt-in did not take the whole run: %d messages", len(full[0].Messages))
	}
}

// A session that is not a child run keeps every turn — the cut is for the files
// the parent already summarises, not for transcripts in general.
func TestAnOrdinarySessionIsNotCut(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var body string
	for i := 0; i < 12; i++ {
		body += `{"type":"user","sessionId":"s1","timestamp":"2026-08-02T10:00:0` +
			string(rune('0'+i%10)) + `Z","message":{"role":"user","content":"turn ` +
			string(rune('a'+i)) + `"}}` + "\n"
	}
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 12 {
		t.Fatalf("an ordinary session lost turns: %#v", ss)
	}
}

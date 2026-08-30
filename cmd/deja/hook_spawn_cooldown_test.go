package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The project cooldown (#2038) counts servings across agent sessions, which is
// right for a reader working through a run of messages and wrong for a fleet:
// ten agents spawned together are ten readers, each seeing the session once,
// and the one they all need is the one the cooldown has just spent (#2534).
func TestAFleetIsNotSilencedByTheProjectCooldown(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One session holds the answer, which is the ordinary shape of a decision
	// made once.
	writeClaudeFixture(t, filepath.Join(root, "app", "kafka.jsonl"), "kafka", []string{
		`{"type":"user","sessionId":"kafka","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the kafka consumer rebalance keeps flapping on the orders topic"}}`,
		`{"type":"assistant","sessionId":"kafka","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"The fix was to raise session.timeout.ms above the poll interval; nothing else held."}}`,
	})
	for i, top := range []string{"the sidebar layout", "the invoice renderer", "the webpack config",
		"the login throttle", "the avatar uploader", "the cron scheduler"} {
		id := fmt.Sprintf("t%d", i)
		writeClaudeFixture(t, filepath.Join(root, "app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-01T0` + fmt.Sprint(i) + `:00:00Z","message":{"role":"user","content":"work on ` + top + ` today"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-01T0` + fmt.Sprint(i) + `:00:01Z","message":{"role":"assistant","content":"changed ` + top + `"}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "src", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	spawn := func(prompt string) string {
		t.Helper()
		b, err := json.Marshal(prompt)
		if err != nil {
			t.Fatal(err)
		}
		out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Task","tool_input":`+
			`{"prompt":`+string(b)+`,"subagent_type":"general-purpose"},"session_id":"parent"}`)
		if strings.TrimSpace(out) == "" {
			return ""
		}
		var resp struct {
			HookSpecificOutput struct {
				UpdatedInput map[string]json.RawMessage `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("the spawn reply is not JSON: %q", out)
		}
		var extended string
		_ = json.Unmarshal(resp.HookSpecificOutput.UpdatedInput["prompt"], &extended)
		return extended
	}

	first := spawn("Investigate why the kafka consumer rebalance keeps flapping on the orders topic.")
	if !strings.Contains(first, "session.timeout.ms") {
		t.Fatalf("the first agent of the fleet got no memory: %q", first)
	}
	for i, prompt := range []string{
		"Look into the kafka consumer rebalance flapping on the orders topic.",
		"Find out what makes the kafka consumer rebalance flap on the orders topic.",
	} {
		if got := spawn(prompt); !strings.Contains(got, "session.timeout.ms") {
			t.Errorf("agent %d of the fleet was sent out without the answer the first one got: %q", i+2, got)
		}
	}
}

// The reader the cooldown was written for keeps it: a person asking twice in a
// row is the repetition #2038 measured.
func TestAnOrdinaryPromptStillSitsOutTheCooldown(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "app", "kafka.jsonl"), "kafka", []string{
		`{"type":"user","sessionId":"kafka","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the kafka consumer rebalance keeps flapping on the orders topic"}}`,
		`{"type":"assistant","sessionId":"kafka","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"The fix was to raise session.timeout.ms above the poll interval; nothing else held."}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "src", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	ask := func(sid, prompt string) string {
		t.Helper()
		b, err := json.Marshal(prompt)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		payload := `{"prompt":` + string(b) + `,"session_id":"` + sid + `","cwd":"` + cwd + `"}`
		if err := runHookPrompt(index.DefaultDir(), strings.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := ask("one", "why does the kafka consumer rebalance keep flapping on the orders topic"); !strings.Contains(got, "session.timeout.ms") {
		t.Fatalf("the first ask got nothing: %q", got)
	}
	if got := ask("two", "and why does the kafka rebalance keep flapping on the orders topic"); strings.Contains(got, "session.timeout.ms") {
		t.Errorf("the session just served was served again to the next reader: %q", got)
	}
}

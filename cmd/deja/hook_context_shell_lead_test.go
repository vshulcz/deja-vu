package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// pi has no MCP of its own, so the deja tool the digest's lead names is not
// there: following it ends in "Tool recall_context not found". A harness that
// says it has no deja tool gets a lead that names the shell command instead,
// at session start and after a compaction (#4584).
func TestHookContextLeadNamesTheShellWhereThereIsNoTool(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "idx"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the retry loop caps at five attempts"}}`,
	})
	if err := run([]string{"index"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/tmp/p")

	// The control: a harness with the tool still hears about it.
	withHookStdin(t, `{"source":"startup","session_id":"a1"}`)
	out := captureStdout(t, func() { _ = runHookContext(index.DefaultDir(), true) })
	if !strings.Contains(out, "call recall_context") {
		t.Fatalf("the ordinary lead does not name the tool, so this measures nothing: %q", out)
	}

	for _, source := range []string{"startup", "compact"} {
		withHookStdin(t, `{"source":"`+source+`","session_id":"b-`+source+`","deja_shell":true}`)
		out = captureStdout(t, func() { _ = runHookContext(index.DefaultDir(), true) })
		if !strings.Contains(out, "recalled from claude session") {
			t.Fatalf("%s: no digest came back: %q", source, out)
		}
		if strings.Contains(out, "recall_context") {
			t.Errorf("%s: the lead names recall_context to a harness that has no deja tool: %q", source, out)
		}
		if !strings.Contains(out, "deja ctx") {
			t.Errorf("%s: the lead names nothing the agent can run: %q", source, out)
		}
	}
}

// And the pi extension is the harness that says so.
func TestPiExtensionSaysItHasNoDejaTool(t *testing.T) {
	ts := piExtensionTS("/bin/deja")
	if !strings.Contains(ts, `JSON.stringify({ session_id: sessionID(), parent_session_id: parent, cwd: process.cwd(), deja_shell: true })`) {
		t.Errorf("the pi extension does not tell hook-context it has no deja tool:\n%s", ts)
	}
}

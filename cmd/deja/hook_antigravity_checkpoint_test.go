package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Antigravity compacts without an event of its own: it truncates the
// conversation and writes a CHECKPOINT step saying the earlier parts are gone.
// Until that is noticed, the blocks are missing from the context while the
// record of having sent them outlives them — the one state where recall stays
// silent about exactly what the agent no longer has.
func TestACompactedConversationIsToldAgain(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "cpapp", "sess-cp",
		"why does the packer refuse the manifest",
		"it refused until glimcheckneedle was set in the environment")
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	transcript := filepath.Join(tmp, "transcript.jsonl")
	write := func(steps ...string) {
		if err := os.WriteFile(transcript, []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	question := `{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nwhy does the packer refuse the manifest\n</USER_REQUEST>"}`
	write(question)

	call := func(n int) string {
		payload, err := json.Marshal(map[string]any{
			"invocationNum":  n,
			"conversationId": "conv-cp",
			"transcriptPath": transcript,
			"workspacePaths": []string{filepath.Join(os.TempDir(), "cpapp")},
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		var resp antigravityHookResponse
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
			t.Fatalf("the hook did not answer with JSON: %q", out.String())
		}
		if len(resp.InjectSteps) == 0 {
			return ""
		}
		return resp.InjectSteps[0].EphemeralMessage
	}

	if first := call(0); !strings.Contains(first, antigravityLead) {
		t.Fatalf("the conversation did not open with the digest: %q", first)
	}
	// Said once. What comes back on the next call is the question's own
	// answer, which is a different block and the right one to send.
	if again := call(0); strings.Contains(again, antigravityLead) {
		t.Fatalf("the digest went in twice without a compaction: %q", again)
	}

	// Now antigravity truncates the conversation and says so.
	write(question,
		`{"step_index":1,"type":"CHECKPOINT","source":"SYSTEM","created_at":"2026-09-04T12:00:00Z","content":"{{ CHECKPOINT 0 }}\n**The earlier parts of this conversation have been truncated due to its long length.**"}`)

	if after := call(0); !strings.Contains(after, antigravityLead) {
		t.Errorf("what the compaction threw away was not offered again: %q", after)
	}
	// And one compaction is one repeat: the marker is remembered, so the
	// digest does not come back on every call after it.
	if again := call(0); strings.Contains(again, antigravityLead) {
		t.Errorf("the same compaction was answered twice: %q", again)
	}
}

// The marker itself: a conversation with no checkpoint has nothing to forget,
// and the same checkpoint is only acted on once.
func TestACheckpointIsActedOnOnce(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	if forgetOnCheckpoint(dir, "conv-1", "") {
		t.Error("a conversation that has not been compacted was treated as if it had")
	}
	if forgetOnCheckpoint(dir, "", "2026-09-04T12:00:00Z") {
		t.Error("a payload with no conversation id was acted on")
	}
	if !forgetOnCheckpoint(dir, "conv-1", "2026-09-04T12:00:00Z") {
		t.Fatal("the first compaction was not acted on")
	}
	if forgetOnCheckpoint(dir, "conv-1", "2026-09-04T12:00:00Z") {
		t.Error("the same compaction was acted on twice")
	}
	// A later compaction in the same conversation is a new one.
	if !forgetOnCheckpoint(dir, "conv-1", "2026-09-04T13:00:00Z") {
		t.Error("a second compaction was mistaken for the first")
	}
}

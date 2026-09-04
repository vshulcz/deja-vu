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

// Antigravity writes each turn into a transcript and hands the hook its path.
// That file is where the question is: the harness has no per-prompt event, so
// without reading it deja can only ever speak once per conversation.
func TestTheNewestQuestionIsReadFromTheTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nthe first question about the toolbar\n</USER_REQUEST>\n"}`,
		`{"step_index":1,"type":"GENERIC","status":"DONE","content":"Created At: whenever"}`,
		`{"step_index":2,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nwhy does the frobnicator stall\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nlocal time\n</ADDITIONAL_METADATA>\n"}`,
		`{"step_index":3,"type":"EPHEMERAL_MESSAGE","status":"DONE","content":"a block deja injected"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := latestUserRequest(path); got != "why does the frobnicator stall" {
		t.Errorf("latest question = %q", got)
	}
	// The metadata antigravity appends is not the person's words.
	if strings.Contains(latestUserRequest(path), "ADDITIONAL_METADATA") {
		t.Error("the harness's own metadata came back as part of the question")
	}
	// A file that holds no user turn, and one that is not there at all, are
	// both answered with silence rather than with a guess.
	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, []byte(`{"step_index":0,"type":"GENERIC","content":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := latestUserRequest(empty); got != "" {
		t.Errorf("a transcript with no question answered %q", got)
	}
	if got := latestUserRequest(filepath.Join(dir, "missing.jsonl")); got != "" {
		t.Errorf("a missing transcript answered %q", got)
	}
}

// The counter antigravity sends starts at zero and restarts on every turn
// (measured on antigravity-cli 1.1.13). Read as 1-based it put the digest in
// twice per turn, and read as per-conversation it put it in again on every turn
// — so the conversation's own ledger is what decides.
func TestTheDigestLandsOncePerConversation(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	const conv = "conv-42"
	if digestAlreadyInjected(dir, conv) {
		t.Fatal("a fresh conversation already counts as served")
	}
	rememberDigestInjected(dir, conv)
	if !digestAlreadyInjected(dir, conv) {
		t.Error("the digest was not recorded, so the next turn sends it again")
	}
	// Another conversation is another reader.
	if digestAlreadyInjected(dir, "conv-43") {
		t.Error("one conversation's digest counted for another")
	}
	// A payload with no conversation id must not silence the digest for
	// everyone: no id, no record.
	rememberDigestInjected(dir, "")
	if digestAlreadyInjected(dir, "") {
		t.Error("an empty conversation id was recorded")
	}
}

// The whole point of reading the transcript: the second call of a turn answers
// the question rather than repeating the digest.
func TestTheSecondCallAnswersTheQuestion(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "app", "sess-old",
		"why does the frobnicator stall on startup",
		"it stalled because ZORBWAX_LIMIT was unset; exporting ZORBWAX_LIMIT=4 cleared it")
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	transcript := filepath.Join(tmp, "transcript.jsonl")
	line := `{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nwhy does the frobnicator stall on startup\n</USER_REQUEST>\n"}`
	if err := os.WriteFile(transcript, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := map[string]any{
		"invocationNum":  1,
		"conversationId": "conv-1",
		"transcriptPath": transcript,
		"workspacePaths": []string{filepath.Join(os.TempDir(), "app")},
	}
	b, _ := json.Marshal(payload)
	var out bytes.Buffer
	if err := runHookAntigravity(dir, bytes.NewReader(b), &out); err != nil {
		t.Fatal(err)
	}
	var resp antigravityHookResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("the hook did not answer with JSON: %q", out.String())
	}
	if len(resp.InjectSteps) == 0 {
		t.Fatalf("the question went unanswered on the call after the digest: %q", out.String())
	}
	block := resp.InjectSteps[0].EphemeralMessage
	if !strings.Contains(block, "frobnicator") {
		t.Errorf("the block does not carry the answer: %q", block)
	}
	// And it is the question's own answer rather than the project digest
	// again — the digest carries this session too, so asserting on the text
	// alone would pass with the once-per-conversation behaviour this replaces.
	if strings.Contains(block, antigravityLead) {
		t.Errorf("the call after the digest was handed the digest again: %q", block)
	}
}

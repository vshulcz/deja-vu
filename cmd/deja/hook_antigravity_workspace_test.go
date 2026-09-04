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

// An ordinary `agy -p` inside a project sends no workspace at all, and the hook
// runs with its working directory set to the plugin folder — so without another
// source the digest is scoped to a directory nobody works in. Antigravity keeps
// the answer beside the transcript: a cache of which conversation each
// directory last opened.
func TestTheWorkspaceIsFoundWhenThePayloadOmitsIt(t *testing.T) {
	root := t.TempDir()
	product := filepath.Join(root, "antigravity-cli")
	transcript := filepath.Join(product, "brain", "conv-7", ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(product, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	byDir := map[string]string{
		"/Users/probe/app":       "conv-7",
		"/Users/probe/app/inner": "conv-7",
		"/Users/probe/other":     "conv-9",
	}
	b, err := json.Marshal(byDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "last_conversations.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	// The deepest directory that opened this conversation, so a session
	// started in a subdirectory is not filed under its parent. Asked twenty
	// times, because a map is walked in no particular order and "whichever
	// came first" would pass a single call about half the time.
	for i := 0; i < 20; i++ {
		if got := workspaceFromConversation(transcript, "conv-7"); got != "/Users/probe/app/inner" {
			t.Fatalf("workspace = %q on attempt %d", got, i+1)
		}
	}
	// Another conversation is another directory.
	if got := workspaceFromConversation(transcript, "conv-9"); got != "/Users/probe/other" {
		t.Errorf("workspace for the other conversation = %q", got)
	}
	// And what it cannot answer it leaves empty rather than guessing.
	if got := workspaceFromConversation(transcript, "conv-none"); got != "" {
		t.Errorf("an unknown conversation resolved to %q", got)
	}
	if got := workspaceFromConversation("", "conv-7"); got != "" {
		t.Errorf("no transcript path resolved to %q", got)
	}
	if got := workspaceFromConversation(filepath.Join(root, "nope", "brain", "c", "a", "b", "t.jsonl"), "conv-7"); got != "" {
		t.Errorf("a missing cache resolved to %q", got)
	}
}

// And the hook uses it: a payload with no workspace must still recall the
// project the conversation belongs to, not the plugin directory it happens to
// run in.
func TestTheHookRecallsTheProjectWhenThePayloadOmitsIt(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "wsapp", "sess-ws",
		"why does the widget loader stall", "it stalled until zorbwaxneedle was set")
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	product := filepath.Join(tmp, "antigravity-cli")
	transcript := filepath.Join(product, "brain", "conv-ws", ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(product, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// seedClaude files its sessions under /tmp/<project>, which is the path
	// the conversation cache has to name for the two to meet.
	project := filepath.Join(os.TempDir(), "wsapp")
	b, err := json.Marshal(map[string]string{project: "conv-ws"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "last_conversations.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(map[string]any{
		"invocationNum":  0,
		"conversationId": "conv-ws",
		"transcriptPath": transcript,
		"workspacePaths": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "zorbwaxneedle") {
		t.Errorf("the conversation's own project was not recalled:\n%s", out.String())
	}
}

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// qwenForkStore holds a Qwen Code session and the /branch of it, written
// after the build: the branch's SessionStart (source "branch") and first
// prompt come before any build has seen it. A branch is a copy of its
// source's records under its own sessionId, each marked forkedFrom.
func qwenForkStore(t *testing.T) (dir, source, fork, forkPath string) {
	t.Helper()
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "-w-p", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond)
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	record := func(id, from, typ, role, text string, d time.Duration) string {
		r := map[string]any{
			"uuid": fmt.Sprintf("%s-%s-%d", id[:8], typ, d), "sessionId": id, "timestamp": at(d),
			"type": typ, "cwd": "/w/p", "version": "0.20.0",
			"message": map[string]any{"role": role, "parts": []any{map[string]any{"text": text}}},
		}
		if from != "" {
			r["forkedFrom"] = map[string]any{"sessionId": from, "messageUuid": r["uuid"]}
		}
		b := mustMarshal(t, r)
		return string(b) + "\n"
	}
	turn := func(id, from, user, reply string, d time.Duration) string {
		return record(id, from, "user", "user", user, d) + record(id, from, "assistant", "model", reply, d+5*time.Second)
	}
	write := func(id, body string) string {
		t.Helper()
		p := filepath.Join(chats, id+".jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for i := range 20 {
		id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
		d := -time.Duration(i+2) * time.Hour
		write(id, turn(id, "", fmt.Sprintf("rename table%d in the billing schema", i), "done", d))
	}
	prior := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	write(prior, turn(prior, "", "the retry loop in fetch.go spins on HTTP 500 again", "Capped the retry loop in fetch.go at MaxAttempts five.", -time.Hour))
	source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	write(source, turn(source, "", forkQuestion, "Looking at the retry loop in fetch.go now.", 0))

	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	fork = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	forkPath = write(fork, turn(fork, source, forkQuestion, "Looking at the retry loop in fetch.go now.", 0))
	return dir, source, fork, forkPath
}

// A Qwen Code /branch opened on a digest that led with the session it was
// branched from, whose turns it already holds: the hook read no head off a
// Qwen transcript, so the unindexed branch had nothing to match its source by.
func TestAQwenBranchsDigestLeavesOutItsSource(t *testing.T) {
	dir, source, fork, forkPath := qwenForkStore(t)
	digest := func(sid, transcript string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "transcript_path": transcript, "cwd": "/w/p", "hook_event_name": "SessionStart", "source": "branch"})
		withHookStdin(t, string(payload))
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	short := source[:8]
	// The branch first: a session once shown sits out the project's cooldown.
	if out := digest(fork, forkPath); strings.Contains(out, short) {
		t.Errorf("a Qwen branch was handed its own source as history:\n%s", out)
	}
	if out := digest("cccccccc-cccc-4ccc-8ccc-cccccccccccc", ""); !strings.Contains(out, short) {
		t.Errorf("the control was not shown the source, so this proves nothing:\n%s", out)
	}
}

// The per-prompt recall on the branch's first prompt reads the same head.
func TestAQwenBranchsPromptRecallLeavesOutItsSource(t *testing.T) {
	dir, source, fork, forkPath := qwenForkStore(t)
	ask := func(sid, transcript string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "transcript_path": transcript, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	short := source[:8]
	if out := ask(fork, forkPath); strings.Contains(out, short) {
		t.Errorf("a Qwen branch's prompt recall named its own source:\n%s", out)
	}
	if out := ask("cccccccc-cccc-4ccc-8ccc-cccccccccccc", ""); !strings.Contains(out, short) {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

// Once indexed, the branch names its source in the index: the harness said so.
func TestAQwenBranchIsIndexedAsAForkOfItsSource(t *testing.T) {
	dir, source, fork, _ := qwenForkStore(t)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	s, ok, err := index.FindByID(dir, fork)
	if err != nil || !ok {
		t.Fatalf("branch not indexed: %v", err)
	}
	if s.Kind != "fork" || s.Parent != source {
		t.Errorf("branch indexed as kind %q parent %q, want fork of %s", s.Kind, s.Parent, source)
	}
	if src, _, err := index.FindByID(dir, source); err != nil || src.Kind != "" || src.Parent != "" {
		t.Errorf("the source itself is not a fork: kind %q parent %q err %v", src.Kind, src.Parent, err)
	}
}

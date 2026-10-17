package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// copilotForkStore holds a Copilot CLI session and the /fork of it, written
// after the build: the fork's first prompt comes before any build has seen
// it. A fork opens with the source's events under its own session.start.
func copilotForkStore(t *testing.T) (dir, source, fork string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "copilot")
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond)
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	write := func(id, body string) {
		t.Helper()
		p := filepath.Join(root, id, "events.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := func(id string, when string) string {
		return fmt.Sprintf(`{"type":"session.start","data":{"sessionId":%q,"startTime":%q,"context":{"cwd":"/w/p"}},"timestamp":%q}`, id, when, when) + "\n"
	}
	turn := func(user, reply string, d time.Duration) string {
		return fmt.Sprintf(`{"type":"user.message","data":{"content":%q},"timestamp":%q}`, user, at(d)) + "\n" +
			fmt.Sprintf(`{"type":"assistant.message","data":{"content":%q},"timestamp":%q}`, reply, at(d+5*time.Second)) + "\n"
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
		write(id, start(id, at(-time.Duration(i+2)*time.Hour))+turn(fmt.Sprintf("rename table%d in the billing schema", i), "done", -time.Duration(i+2)*time.Hour))
	}
	prior := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	write(prior, start(prior, at(-time.Hour))+turn("the retry loop in fetch.go spins on HTTP 500 again", "Capped the retry loop in fetch.go at MaxAttempts five.", -time.Hour))
	source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	write(source, start(source, at(0))+turn(forkQuestion, "Looking at the retry loop in fetch.go now.", 0))

	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	fork = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	write(fork, start(fork, at(20*time.Minute))+turn(forkQuestion, "Looking at the retry loop in fetch.go now.", 0)+
		fmt.Sprintf(`{"type":"session.info","data":{"infoType":"fork","message":"Forked from \"x\" (%s) as \"x (fork)\" (%s)."},"timestamp":%q}`, source, fork, at(20*time.Minute))+"\n")
	return dir, source, fork
}

// A Copilot CLI fork's first session-start digest listed the session it was
// forked from, whose turns the fork already holds. Copilot's hooks name the
// session and not its file, so the unindexed fork had nothing to be read off
// (#5137).
func TestACopilotForksDigestLeavesOutItsSource(t *testing.T) {
	dir, source, fork := copilotForkStore(t)
	digest := func(sid string) string {
		withHookStdin(t, `{"sessionId":"`+sid+`","source":"resume","cwd":"/w/p"}`)
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	short := source[:8]
	// The fork first: a session once shown sits out the project's cooldown.
	if out := digest(fork); strings.Contains(out, short) {
		t.Errorf("a Copilot fork was handed its own source as history:\n%s", out)
	}
	if out := digest("cccccccc-cccc-4ccc-8ccc-cccccccccccc"); !strings.Contains(out, short) {
		t.Errorf("the control was not shown the source, so this proves nothing:\n%s", out)
	}
}

// The per-prompt recall too: Copilot runs userPromptSubmitted before
// sessionStart, on the fork's first prompt.
func TestACopilotForksPromptRecallLeavesOutItsSource(t *testing.T) {
	dir, source, fork := copilotForkStore(t)
	ask := func(sid string) string {
		payload, _ := json.Marshal(map[string]string{"sessionId": sid, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	short := source[:8]
	if out := ask(fork); strings.Contains(out, short) {
		t.Errorf("a Copilot fork's prompt recall named its own source:\n%s", out)
	}
	if out := ask("cccccccc-cccc-4ccc-8ccc-cccccccccccc"); !strings.Contains(out, short) {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

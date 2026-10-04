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
	"github.com/vshulcz/deja-vu/internal/model"
)

const forkQuestion = "the retry loop in fetch.go spins again on HTTP 500 - what did we do last time?"

// forkStore holds a Claude Code session and a Codex thread that were forked,
// each with the session that settled the question long before. The sources
// asked the same question their forks copied; prior sessions answered it.
// The Claude fork is written after the build when unindexed is set, the way a
// fork's first prompts arrive before any build has seen it.
func forkStore(t *testing.T, unindexed bool) (dir, claudeFork string) {
	t.Helper()
	tmp := hermeticEnv(t)
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond).Add(123 * time.Millisecond)
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claude := filepath.Join(tmp, "claude", "-w-p")
	// Other work, so the question's words are rare enough to identify it.
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("other-%d", i)
		write(filepath.Join(claude, id+".jsonl"),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/p","timestamp":"2026-07-01T09:%02d:00Z","message":{"role":"user","content":"rename table%d and column%d in the billing schema"}}`, id, i, i, i)+"\n")
	}
	write(filepath.Join(claude, "prior-c.jsonl"),
		`{"type":"user","sessionId":"prior-c","cwd":"/w/p","timestamp":"2026-08-04T09:00:00Z","message":{"role":"user","content":"the retry loop in fetch.go spins on HTTP 500 again"}}`+"\n"+
			`{"type":"assistant","sessionId":"prior-c","cwd":"/w/p","timestamp":"2026-08-04T09:02:00Z","message":{"role":"assistant","content":"Capped the retry loop in fetch.go at MaxAttempts five, so an HTTP 500 stops after five tries."}}`+"\n")
	turn := func(id string) string {
		return fmt.Sprintf(`{"type":"user","uuid":"u-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":%q}}`, id, at(0), forkQuestion) + "\n" +
			fmt.Sprintf(`{"type":"assistant","uuid":"a-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"assistant","content":"Looking at the retry loop in fetch.go now."}}`, id, at(5*time.Second)) + "\n"
	}
	const source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	claudeFork = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	write(filepath.Join(claude, source+".jsonl"), turn(source))
	fork := turn(claudeFork) +
		fmt.Sprintf(`{"type":"user","uuid":"u-2","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":"keep going in the branch"}}`, claudeFork, at(20*time.Minute)) + "\n"
	if !unindexed {
		write(filepath.Join(claude, claudeFork+".jsonl"), fork)
	}

	codex := filepath.Join(tmp, "codex", "sessions", "2026", "10", "02")
	write(filepath.Join(codex, "rollout-2026-08-04T09-00-00-prior-x.jsonl"),
		`{"timestamp":"2026-08-04T09:00:00Z","type":"session_meta","payload":{"id":"prior-x","cwd":"/w/q"}}`+"\n"+
			`{"timestamp":"2026-08-04T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"the retry loop in fetch.go spins on HTTP 500 again"}]}}`+"\n"+
			`{"timestamp":"2026-08-04T09:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Capped the retry loop in fetch.go at MaxAttempts five."}]}}`+"\n")
	codexTurn := fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`, at(0), forkQuestion) + "\n"
	write(filepath.Join(codex, "rollout-2026-10-02T07-00-00-source-x.jsonl"),
		fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":"source-x","cwd":"/w/q"}}`, at(0))+"\n"+codexTurn)
	// codex 0.149.0 writes the fork's own session_meta first, naming the
	// thread it came from, then the source's history.
	write(filepath.Join(codex, "rollout-2026-10-02T07-20-00-fork-x.jsonl"),
		fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":"fork-x","forked_from_id":"source-x","cwd":"/w/q"}}`, at(20*time.Minute))+"\n"+
			fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":"source-x","cwd":"/w/q"}}`, at(0))+"\n"+codexTurn)

	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if unindexed {
		write(filepath.Join(claude, claudeFork+".jsonl"), fork)
	}
	return dir, claudeFork
}

// A fork's MCP recall answered with the session it was forked from, whose
// turns are already in the fork (#4549, #4251). Codex names the source in
// forked_from_id; a Claude Code fork opens on the source's own first turn.
func TestAForksRecallLeavesOutItsSource(t *testing.T) {
	dir, fork := forkStore(t, false)
	q := json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`)

	before, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(before, "aaaaaaaa") || !strings.Contains(before, "source-x") {
		t.Fatalf("the fixture never served the sources, so this proves nothing:\n%s", before)
	}

	markSessionLive(dir, fork)
	markSessionLive(dir, "fork-x")
	after, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, src := range []string{"aaaaaaaa", "source-x"} {
		if strings.Contains(after, src) {
			t.Errorf("a fork's recall answered with its own source (%s):\n%s", src, after)
		}
	}
	for _, prior := range []string{"prior-c", "prior-x"} {
		if !strings.Contains(after, prior) {
			t.Errorf("an older session that settled it (%s) is not on the fork's page:\n%s", prior, after)
		}
	}
}

// The per-prompt recall in a fork named the source's own earlier prompt as
// "asked here before", on the fork's first prompt, before any build had seen
// the fork (#4549). The payload names the transcript, whose first records say
// which turn the fork opens with.
func TestAForksPromptRecallLeavesOutItsSource(t *testing.T) {
	dir, fork := forkStore(t, true)
	transcript := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", fork+".jsonl")
	ask := func(sid, path string) string {
		payload, _ := json.Marshal(map[string]string{"session_id": sid, "transcript_path": path, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	// The fork first: what one session is shown sits out the project's
	// cooldown, so asking the control first would hide the source either way.
	if out := ask(fork, transcript); strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the fork was handed its own source as history:\n%s", out)
	}
	// Control: another session asking the same thing is told about the source.
	if out := ask("someone-else", ""); !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

// Claude Code starts a fork with source "fork", and the fork already carries
// the digest its source opened with. A second one led with the source (#4549).
func TestAForkOpensWithoutASecondDigest(t *testing.T) {
	dir, fork := forkStore(t, false)
	run := func(payload string) string {
		withHookStdin(t, payload)
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	if out := run(`{"session_id":"someone-else","source":"startup","cwd":"/w/p"}`); !strings.Contains(out, "retry loop") {
		t.Fatalf("the control got no digest, so this proves nothing:\n%s", out)
	}
	if out := run(`{"session_id":"` + fork + `","source":"fork","cwd":"/w/p"}`); strings.TrimSpace(out) != "" {
		t.Errorf("a fork got a second session-start digest:\n%s", out)
	}
	if !liveSessionIDs(dir)[fork] {
		t.Errorf("the fork was not stamped live")
	}
}

// A Codex fork records its source as its parent, and `deja show` said the
// source had spawned it.
func TestShowSaysAForkWasForked(t *testing.T) {
	var w strings.Builder
	printSpawnEdges(&w, "", model.Session{ID: "fork-x", Kind: "fork", Parent: "01a0fb73-70b2-7572-bae8-690bebe8820f"})
	if got := w.String(); !strings.Contains(got, "forked from 01a0fb73") || strings.Contains(got, "spawned") {
		t.Errorf("a fork reads as spawned:\n%s", got)
	}
}

// The other way round: the source resumed is not the fork. A fork that went on
// past the turns it copied holds work its source never saw, and the source's
// recall left it out because the two open alike (#4549).
func TestASourcesRecallKeepsAForkThatWentOn(t *testing.T) {
	dir, fork := forkStore(t, false)
	q := json.RawMessage(`{"query":"keep going in the branch"}`)
	if before, _ := callMCPTool(dir, "recall", q); !strings.Contains(before, fork[:8]) {
		t.Fatalf("the fixture never served the fork, so this proves nothing:\n%s", before)
	}
	markSessionLive(dir, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	out, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(out, fork[:8]) {
		t.Errorf("the resumed source's recall left out a fork's own later work:\n%s", out)
	}
	// Control: the fork asking still leaves its source out.
	endSessionLive(dir, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	markSessionLive(dir, fork)
	if out, _ := callMCPTool(dir, "recall", json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`)); strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the fork's recall answered with its source:\n%s", out)
	}
}

// A real Claude Code fork opens on records the first 64 KB do not get past —
// a 46 KB attachment ahead of the first user turn — and a fork of a compacted
// session opens on the harness's own preamble, which the index strips before
// it fingerprints anything. Read off the transcript, the fork either had no
// opening or one the index never matched, and its first prompt was answered
// with the source (#4549).
func TestALongHeadedForksPromptRecallLeavesOutItsSource(t *testing.T) {
	tmp := hermeticEnv(t)
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond).Add(316 * time.Millisecond)
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	claude := filepath.Join(tmp, "claude", "-w-p")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(claude, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("other-%d", i)
		write(id+".jsonl", fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/p","timestamp":"2026-07-01T09:%02d:00Z","message":{"role":"user","content":"rename table%d and column%d in the billing schema"}}`, id, i, i, i)+"\n")
	}
	opening := "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\n" + forkQuestion
	session := func(id string, more string) string {
		pad := strings.Repeat("x", 70<<10)
		return fmt.Sprintf(`{"type":"attachment","uuid":"att-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"attachment":{"type":"file","content":%q}}`, id, at(time.Second), pad) + "\n" +
			fmt.Sprintf(`{"type":"user","uuid":"u-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"isCompactSummary":true,"message":{"role":"user","content":%q}}`, id, at(0), opening) + "\n" +
			// The summary is served only when asked for, so the source's own
			// words are what the control is answered with.
			fmt.Sprintf(`{"type":"assistant","uuid":"a-1","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"assistant","content":"Looking at the retry loop in fetch.go that spins on HTTP 500 now."}}`, id, at(5*time.Second)) + "\n" + more
	}
	const source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const fork = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	write(source+".jsonl", session(source, ""))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	write(fork+".jsonl", session(fork, fmt.Sprintf(`{"type":"user","uuid":"u-2","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":%q}}`, fork, at(20*time.Minute), forkQuestion)+"\n"))
	ask := func(sid, path string) string {
		payload, _ := json.Marshal(map[string]string{"session_id": sid, "transcript_path": path, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := ask(fork, filepath.Join(claude, fork+".jsonl")); strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the fork was handed its own source as history:\n%s", out)
	}
	if out := ask("someone-else", ""); !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

// Moving a Claude Code session to the background forks it with
// --fork-session and ends the source, and the source goes on to gain a "No
// response requested." turn and a task notification the fork never sees. The
// fork's recall then answered with the source: its last record was not one the
// fork held (#4251).
func TestABackgroundedForksRecallLeavesOutASourceThatWentOn(t *testing.T) {
	dir, fork := forkStore(t, false)
	const source = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", source+".jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-30 * time.Minute)
	more := fmt.Sprintf(`{"type":"assistant","uuid":"a-2","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"assistant","content":"No response requested."}}`, source, t0.Add(3*time.Minute).Format(time.RFC3339Nano)) + "\n" +
		fmt.Sprintf(`{"type":"user","uuid":"u-3","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":"<task-notification><task-id>b1</task-id><status>completed</status></task-notification>"}}`, source, t0.Add(9*time.Minute).Format(time.RFC3339Nano)) + "\n"
	if err := os.WriteFile(path, append(b, more...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	q := json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`)
	if before, _ := callMCPTool(dir, "recall", q); !strings.Contains(before, "aaaaaaaa") {
		t.Fatalf("the fixture never served the source, so this proves nothing:\n%s", before)
	}
	if err := runHookPrompt(dir, strings.NewReader(`{"session_id":"`+fork+`","hook_event_name":"UserPromptSubmit","cwd":"/w/p","prompt":"keep going in the background"}`), io.Discard); err != nil {
		t.Fatal(err)
	}
	runHookSessionEnd(dir, strings.NewReader(`{"session_id":"`+source+`","hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`))
	out, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if strings.Contains(out, "aaaaaaaa") {
		t.Errorf("a backgrounded fork's recall answered with its own source:\n%s", out)
	}
	if !strings.Contains(out, "prior-c") {
		t.Errorf("the older session that settled it is not on the fork's page:\n%s", out)
	}
}

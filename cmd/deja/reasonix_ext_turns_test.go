package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReasonixExtPutsRecallInTheTurnTail(t *testing.T) {
	calls := fakeRxHooks(t, func(sub string, _ map[string]any) (string, error) {
		switch sub {
		case "hook-context":
			return "<deja-recall>\nDIGEST\n</deja-recall>", nil
		case "hook-prompt":
			return "<deja-recall>\nRECALL\n</deja-recall>", nil
		}
		return "", nil
	})
	h := startFakeRxHost(t)
	h.handshake()
	first := h.intercept("input.receive", map[string]any{"text": "why does the pool run dry", "futureField": 7})
	text := replacementField(t, first, "text")
	if !strings.HasPrefix(text, "why does the pool run dry\n\n") || !strings.Contains(text, "DIGEST") || !strings.Contains(text, "RECALL") {
		t.Fatalf("first turn text = %q, want the question with the digest and the recall after it", text)
	}
	if strings.Index(text, "DIGEST") > strings.Index(text, "RECALL") {
		t.Errorf("the digest should come before the per-prompt recall: %q", text)
	}
	if !strings.Contains(string(first.Replacement), `"futureField":7`) {
		t.Errorf("a payload field deja does not know was dropped: %s", first.Replacement)
	}
	second := replacementField(t, h.intercept("input.receive", map[string]any{"text": "and the retry"}), "text")
	if strings.Contains(second, "DIGEST") || !strings.Contains(second, "RECALL") {
		t.Fatalf("second turn text = %q, want the recall and no second digest", second)
	}
	var contexts, prompts int
	for _, c := range calls() {
		switch c.args[0] {
		case "hook-context":
			contexts++
			if c.args[1] != "--plain" || c.input["deja_once"] != true {
				t.Errorf("hook-context ran as %v with %v, want --plain and deja_once", c.args, c.input)
			}
		case "hook-prompt":
			prompts++
			if c.args[1] != "--plain" || c.input["prompt"] == "" || c.input["session_id"] == "" {
				t.Errorf("hook-prompt ran as %v with %v", c.args, c.input)
			}
		}
	}
	if contexts != 1 || prompts != 2 {
		t.Errorf("hook-context ran %d times and hook-prompt %d, want 1 and 2", contexts, prompts)
	}
}

func TestReasonixExtPassesTheTurnThroughWhenDejaHasNothing(t *testing.T) {
	prevBudget := rxPromptBudget
	rxPromptBudget = 200 * time.Millisecond
	t.Cleanup(func() { rxPromptBudget = prevBudget })
	mode := "silent"
	var mu sync.Mutex
	fakeRxHooks(t, func(string, map[string]any) (string, error) {
		mu.Lock()
		m := mode
		mu.Unlock()
		switch m {
		case "error":
			return "<deja-recall>half</deja-recall>", errors.New("exit status 1")
		case "huge":
			return "<deja-recall>" + strings.Repeat("x", rxInjectionCap) + "</deja-recall>", nil
		case "slow":
			time.Sleep(600 * time.Millisecond)
			return "<deja-recall>late</deja-recall>", nil
		}
		return "", nil
	})
	h := startFakeRxHost(t)
	h.handshake()
	for _, m := range []string{"silent", "error", "huge", "slow"} {
		mu.Lock()
		mode = m
		mu.Unlock()
		start := time.Now()
		r := h.intercept("input.receive", map[string]any{"text": "a question"})
		if r.Decision != "continue" || len(r.Replacement) != 0 {
			t.Errorf("%s hook: answer = %+v, want continue with no replacement", m, r)
		}
		if took := time.Since(start); took > 500*time.Millisecond {
			t.Errorf("%s hook: the turn waited %s, past the %s budget", m, took, rxPromptBudget)
		}
	}
}

func TestReasonixExtCarriesToolNotesIntoTheResult(t *testing.T) {
	calls := fakeRxHooks(t, func(sub string, in map[string]any) (string, error) {
		switch sub {
		case "hook-tool":
			return "<deja-recall>\nNOTE go test\n</deja-recall>", nil
		case "hook-tool-after":
			return "<deja-recall>\nFIX go mod tidy\n</deja-recall>", nil
		}
		return "", nil
	})
	h := startFakeRxHost(t)
	h.handshake()
	call := map[string]any{"name": "bash", "arguments": `{"command":"go test ./..."}`}
	if r := h.intercept("tool.before", call); r.Decision != "continue" {
		t.Fatalf("tool.before = %+v, want continue: deja never changes or stops a call", r)
	}
	after := h.intercept("tool.after", map[string]any{"name": "bash", "arguments": `{"command":"go test ./..."}`,
		"result": "FAIL ./...\n", "isError": true})
	result := replacementField(t, after, "result")
	if !strings.HasPrefix(result, "FAIL ./...\n\n") || !strings.Contains(result, "NOTE go test") || !strings.Contains(result, "FIX go mod tidy") {
		t.Fatalf("result = %q, want the output with the pre-tool note and the fix after it", result)
	}
	if !strings.Contains(string(after.Replacement), `"isError":true`) {
		t.Errorf("the failure flag was not handed back: %s", after.Replacement)
	}
	for _, c := range calls() {
		if c.args[0] == "hook-tool" {
			in, _ := c.input["tool_input"].(map[string]any)
			if c.input["tool_name"] != "Bash" || in["command"] != "go test ./..." {
				t.Errorf("hook-tool got %v, want Claude's Bash shape with the command", c.input)
			}
		}
		if c.args[0] == "hook-tool-after" && c.input["tool_response"] != "FAIL ./...\n" {
			t.Errorf("hook-tool-after got %v, want the failing output", c.input["tool_response"])
		}
	}
	// A call that succeeded, with nothing noted before it, is left alone and
	// asks for no fix.
	before := len(calls())
	ok := h.intercept("tool.after", map[string]any{"name": "bash", "arguments": `{"command":"ls"}`, "result": "a\n"})
	if ok.Decision != "continue" {
		t.Errorf("a clean result was replaced: %+v", ok)
	}
	for _, c := range calls()[before:] {
		if c.args[0] == "hook-tool-after" {
			t.Errorf("hook-tool-after ran for a call that did not fail")
		}
	}
}

func TestReasonixExtMapsFileToolsToTheirPaths(t *testing.T) {
	tool, in := rxHookToolInput("edit_file", `{"path":"internal/a.go","old":"x"}`, "/work")
	if tool != "Edit" || in["file_path"] != filepath.Join("/work", "internal/a.go") {
		t.Errorf("edit_file = %s %v, want Edit with the path resolved against the workspace", tool, in)
	}
	if tool, _ := rxHookToolInput("read_file", `{"path":"a.go"}`, "/work"); tool != "" {
		t.Errorf("read_file mapped to %q; the pre-tool line is for changes, not reads", tool)
	}
}

func TestReasonixExtCarriesContextIntoTheCompactionSummary(t *testing.T) {
	calls := fakeRxHooks(t, func(sub string, _ map[string]any) (string, error) {
		if sub == "hook-context" {
			return "<deja-recall>\nDIGEST\n</deja-recall>", nil
		}
		return "", nil
	})
	h := startFakeRxHost(t)
	h.handshake()
	h.intercept("input.receive", map[string]any{"text": "fix the flaky pool test"})
	messages := json.RawMessage(`[{"role":"user","content":"fix the flaky pool test"},` +
		`{"role":"assistant","tool_calls":[{"id":"c1","name":"bash","arguments":"{\"command\":\"go test ./pool/...\"}"}]},` +
		`{"role":"tool","content":"--- FAIL: TestPool (0.01s)\nFAIL","tool_call_id":"c1","name":"bash"},` +
		`{"role":"assistant","content":"The pool test fails because the idle timeout is zero."}]`)
	r := h.intercept("compaction.prepare", map[string]any{"messages": messages, "guidance": "keep the flags"})
	guidance := replacementField(t, r, "guidance")
	if !strings.HasPrefix(guidance, "keep the flags\n\n") || !strings.Contains(guidance, "go test ./pool/...") || !strings.Contains(guidance, "<deja-recall>") {
		t.Fatalf("guidance = %q, want the host's guidance and deja's framed packet naming the command", guidance)
	}
	var got map[string]json.RawMessage
	_ = json.Unmarshal(r.Replacement, &got)
	var want, have any
	_ = json.Unmarshal(messages, &want)
	_ = json.Unmarshal(got["messages"], &have)
	wb, _ := json.Marshal(want)
	hb, _ := json.Marshal(have)
	if string(wb) != string(hb) {
		t.Errorf("the fold was changed:\n%s\nwant\n%s", hb, wb)
	}
	// The fold took the digest away with it, so the next turn carries it again.
	next := replacementField(t, h.intercept("input.receive", map[string]any{"text": "go on"}), "text")
	if !strings.Contains(next, "DIGEST") {
		t.Errorf("turn after compaction = %q, want the digest again", next)
	}
	contexts := 0
	for _, c := range calls() {
		if c.args[0] == "hook-context" {
			contexts++
		}
	}
	if contexts != 2 {
		t.Errorf("hook-context ran %d times, want once per side of the compaction", contexts)
	}
}

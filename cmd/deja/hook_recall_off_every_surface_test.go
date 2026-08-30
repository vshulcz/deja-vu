package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// The kill switch reached the session-start hook (#2699) and nothing else. It
// fires on every user message through hook-prompt — the wiring for seven
// harnesses and the Kimi plugin — and on every Bash and Edit through
// hook-tool, and the MCP recall tool appends the same environment block a
// machine with the switch set has asked not to see (#2701).
func TestRecallOffReachesEverySurfaceThatInjects(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Four sessions hitting the same missing tool, which is what the
	// environment block is built from, and running the same command, which is
	// what the tool hook answers about. Only the first carries the sentence
	// the per-prompt digest matches on: a phrase every session repeats is not
	// rare enough to rank, and the hook then has nothing to say.
	for i := 1; i <= 4; i++ {
		id := "s" + strconv.Itoa(i)
		day := "2026-08-0" + strconv.Itoa(i)
		opening := "deploy the staging cluster"
		if i == 1 {
			opening = "the zonkoshard retry budget keeps blowing up under load"
		}
		body := `{"type":"user","message":{"role":"user","content":"` + opening + `"},` +
			`"timestamp":"` + day + `T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n" +
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash",` +
			`"input":{"command":"terraform apply --zonkoshard 7"}}]},` +
			`"timestamp":"` + day + `T10:01:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n" +
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result",` +
			`"content":"zsh:1: command not found: zonkotool"}]},` +
			`"timestamp":"` + day + `T10:02:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n" +
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text",` +
			`"text":"decision: cap retries at three"}]},` +
			`"timestamp":"` + day + `T10:03:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Two stores from the same transcripts, one per leg. Every one of these
	// surfaces remembers what it served — the per-prompt hook bans a session
	// it already showed, the environment block stamps when it last went out —
	// so running both legs against one store would silence the second for
	// reasons that have nothing to do with the switch.
	build := func(name string) string {
		dir := filepath.Join(tmp, name)
		if err := index.Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	dir := build("before.db")
	after := build("after.db")
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")
	t.Setenv("DEJA_RECALL", "")

	// A fresh agent session per run: the per-prompt hook bans what it already
	// showed a session, and reusing the id would silence the second run for a
	// reason that has nothing to do with the switch.
	promptPayload := func(agent string) string {
		return `{"session_id":"` + agent + `","prompt":"the zonkoshard retry budget again","cwd":"/proj"}`
	}
	toolPayload := func(agent string) string {
		return `{"session_id":"` + agent + `","tool_name":"Bash",` +
			`"tool_input":{"command":"terraform apply --zonkoshard 7"},"cwd":"/proj"}`
	}
	agent := "before"
	activeStore := func() string {
		if agent == "before" {
			return dir
		}
		return after
	}

	surfaces := []struct {
		name string
		run  func() string
	}{
		{"hook-prompt", func() string {
			var out bytes.Buffer
			if err := runHookPrompt(activeStore(), strings.NewReader(promptPayload(agent)), &out); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}},
		{"hook-tool", func() string {
			var out bytes.Buffer
			if err := runHookTool(activeStore(), strings.NewReader(toolPayload(agent)), &out); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}},
		{"the environment block", func() string {
			return environmentBlock(activeStore(), policy.ActivationMCP)
		}},
	}

	// Each one says something first, or the silence below proves nothing.
	spoke := map[string]bool{}
	for _, s := range surfaces {
		if out := s.run(); strings.TrimSpace(out) != "" {
			spoke[s.name] = true
		} else {
			t.Errorf("%s said nothing before the switch, so the fixture cannot show it stopping", s.name)
		}
	}

	t.Setenv("DEJA_RECALL", "off")
	agent = "after"
	for _, s := range surfaces {
		if !spoke[s.name] {
			continue
		}
		out := s.run()
		for _, tell := range []string{
			"deja-recall",
			"you have been here",
			// The block is unframed text, so the two markers above pass
			// straight over it — which is how this leg was passing with the
			// gate removed.
			"This machine, from deja's index",
			"terraform apply",
			"retry budget",
		} {
			if strings.Contains(out, tell) {
				t.Errorf("%s injected %q with the kill switch set:\n%s", s.name, tell, out)
				break
			}
		}
	}

	// The MCP recall tool is the exception the issue names: the agent asked
	// for that answer, so it still goes out. What must not is the block deja
	// appends to it unasked.
	mcpRecall := func() string {
		// The block goes out once per process, and in a full package run some
		// earlier test has already spent that. Hand it back, or this leg
		// measures the guard rather than the switch.
		environmentMu.Lock()
		environmentSpent = false
		environmentMu.Unlock()
		out, err := callMCPTool(activeStore(), "recall", json.RawMessage(`{"query":"zonkoshard"}`))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	agent = "before"
	t.Setenv("DEJA_RECALL", "")
	if before := mcpRecall(); !strings.Contains(before, "This machine, from deja's index") {
		t.Fatalf("the MCP answer carried no environment block to begin with:\n%s", before)
	}
	agent = "after"
	t.Setenv("DEJA_RECALL", "off")
	answer := mcpRecall()
	if strings.Contains(answer, "This machine, from deja's index") {
		t.Errorf("the MCP answer still carries the environment block:\n%s", answer)
	}
	if !strings.Contains(answer, "zonkoshard") {
		t.Errorf("the answer the agent asked for went silent too:\n%s", answer)
	}
}

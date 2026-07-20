package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// driveMCP feeds line-delimited JSON-RPC requests through serveMCP (the exact
// code path `deja mcp` runs over stdio) and returns the parsed responses in
// order. This exercises the real request/response framing a client sees.
func driveMCP(t *testing.T, requests ...string) []map[string]any {
	t.Helper()
	in := strings.Join(requests, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(in), &out); err != nil {
		t.Fatalf("serveMCP: %v", err)
	}
	var resp []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad response line %q: %v", line, err)
		}
		resp = append(resp, m)
	}
	return resp
}

// callText pulls result.content[0].text out of a tools/call response, asserting
// the {content:[{type:text,text}]} envelope every MCP client parses.
func callText(t *testing.T, resp map[string]any) string {
	t.Helper()
	if e, ok := resp["error"]; ok {
		t.Fatalf("unexpected rpc error: %#v", e)
	}
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result object: %#v", resp)
	}
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content array: %#v", res)
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" {
		t.Fatalf("content[0].type = %v, want text", block["type"])
	}
	return block["text"].(string)
}

// seedClaude writes one claude session (user + assistant line) under the
// DEJA_CLAUDE_ROOT project dir.
func seedClaude(t *testing.T, root, project, id, userText, asstText string) {
	t.Helper()
	dir := filepath.Join(root, "-"+project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(role, text string) string {
		msg := map[string]any{"role": role, "content": text}
		rec := map[string]any{"type": role, "sessionId": id, "cwd": "/tmp/" + project, "timestamp": "2026-01-02T03:04:05Z", "message": msg}
		b, _ := json.Marshal(rec)
		return string(b)
	}
	body := line("user", userText) + "\n" + line("assistant", asstText) + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMCPToolContract exercises the full stdio tool surface a real agent drives:
// every tool (recall, recall_context, blame, remember), the harness filter, the
// limit cap, the result envelope, and the tools/list schema. Hermetic and
// deterministic — no network, no real agent. It is the guardrail that keeps the
// MCP contract that live agents depend on from silently regressing.
func TestMCPToolContract(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	// Two sessions share "frobnicator" (limit test); one names parser.go (blame).
	seedClaude(t, claude, "app", "sess-alpha", "the frobnicator crash in parser.go", "fixed the frobnicator")
	seedClaude(t, claude, "app", "sess-beta", "another frobnicator regression today", "frobnicator again")

	t.Run("tools/list schema", func(t *testing.T) {
		resp := driveMCP(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		tools := resp[0]["result"].(map[string]any)["tools"].([]any)
		got := map[string]bool{}
		for _, ti := range tools {
			tool := ti.(map[string]any)
			name, _ := tool["name"].(string)
			got[name] = true
			schema, ok := tool["inputSchema"].(map[string]any)
			if !ok || schema["type"] != "object" {
				t.Fatalf("tool %q inputSchema not an object: %#v", name, tool["inputSchema"])
			}
			if _, ok := schema["required"].([]any); !ok {
				t.Fatalf("tool %q missing required[]: %#v", name, schema)
			}
		}
		for _, want := range []string{"recall", "recall_context", "blame", "remember"} {
			if !got[want] {
				t.Fatalf("tools/list missing %q; got %v", want, got)
			}
		}
	})

	t.Run("recall envelope and hit", func(t *testing.T) {
		resp := driveMCP(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator","harness":"claude"}}}`)
		text := callText(t, resp[0])
		if !strings.Contains(text, "frobnicator") || !strings.Contains(text, "2 match(es)") {
			t.Fatalf("recall text = %q, want 2 frobnicator matches", text)
		}
	})

	t.Run("harness filter excludes", func(t *testing.T) {
		// codex root is an empty temp dir, so filtering to codex must find nothing.
		resp := driveMCP(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator","harness":"codex"}}}`)
		text := callText(t, resp[0])
		if !strings.Contains(text, "No prior deja sessions matched") {
			t.Fatalf("harness=codex should exclude claude session, got %q", text)
		}
	})

	t.Run("limit caps results", func(t *testing.T) {
		resp := driveMCP(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator","harness":"claude","limit":1}}}`)
		text := callText(t, resp[0])
		if !strings.Contains(text, "1 match(es)") || strings.Contains(text, "2 match(es)") {
			t.Fatalf("limit=1 should cap to one match, got %q", text)
		}
	})

	t.Run("fractional limit is accepted", func(t *testing.T) {
		// JSON has no int/float distinction; a client emitting 1.0 must not blow up
		// the whole call. Schema advertises limit as "number".
		resp := driveMCP(t, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator","harness":"claude","limit":1.0}}}`)
		if e, ok := resp[0]["error"]; ok {
			t.Fatalf("fractional limit errored: %#v", e)
		}
		text := callText(t, resp[0])
		if !strings.Contains(text, "1 match(es)") {
			t.Fatalf("limit 1.0 should cap to one match, got %q", text)
		}
	})

	t.Run("large integer id echoed exactly", func(t *testing.T) {
		// Check raw wire bytes: parsing into map[string]any would itself round the
		// id to float64, so assert on the encoded response string.
		var out bytes.Buffer
		if err := serveMCP(strings.NewReader(`{"jsonrpc":"2.0","id":9007199254740993,"method":"tools/list","params":{}}`+"\n"), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"id":9007199254740993`) {
			t.Fatalf("large id not echoed exactly: %s", out.String())
		}
	})

	t.Run("oversized frame skipped then server keeps serving", func(t *testing.T) {
		big := strings.Repeat("x", mcpMaxFrame+1)
		resp := driveMCP(t,
			`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"recall","arguments":{"query":"nomatch"}}}`,
			big,
			`{"jsonrpc":"2.0","id":12,"method":"tools/list","params":{}}`,
		)
		// Three input frames -> a recall reply, one parse error, and a tools/list
		// reply: the giant middle frame must not tear the session down.
		if len(resp) != 3 {
			t.Fatalf("want 3 responses (recall, parse-error, list), got %d: %#v", len(resp), resp)
		}
		if _, ok := resp[1]["error"]; !ok {
			t.Fatalf("oversized frame should yield a parse error, got %#v", resp[1])
		}
		if _, ok := resp[2]["result"]; !ok {
			t.Fatalf("server should still answer tools/list after oversized frame, got %#v", resp[2])
		}
	})

	t.Run("blame finds file discussion", func(t *testing.T) {
		resp := driveMCP(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"blame","arguments":{"path":"parser.go"}}}`)
		text := callText(t, resp[0])
		var hits []map[string]any
		if err := json.Unmarshal([]byte(text), &hits); err != nil {
			t.Fatalf("blame result not a JSON array: %q (%v)", text, err)
		}
		if len(hits) == 0 {
			t.Fatalf("blame parser.go found nothing; a session names it")
		}
	})

	t.Run("remember then recall round-trips", func(t *testing.T) {
		marker := "zebracricket advisory-lock decision"
		save := driveMCP(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"remember","arguments":{"text":%q,"project":"notes"}}}`, marker))
		if txt := callText(t, save[0]); !strings.Contains(txt, "Remembered") {
			t.Fatalf("remember ack = %q", txt)
		}
		recall := driveMCP(t, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"recall","arguments":{"query":"zebracricket"}}}`)
		if txt := callText(t, recall[0]); !strings.Contains(txt, "zebracricket") {
			t.Fatalf("stored note not recalled: %q", txt)
		}
	})

	t.Run("missing required args error", func(t *testing.T) {
		resp := driveMCP(t,
			`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"blame","arguments":{}}}`,
			`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"remember","arguments":{"text":""}}}`,
		)
		if e, ok := resp[0]["error"].(map[string]any); !ok || !strings.Contains(fmt.Sprint(e["message"]), "path required") {
			t.Fatalf("blame without path should error path required, got %#v", resp[0])
		}
		if e, ok := resp[1]["error"].(map[string]any); !ok || !strings.Contains(fmt.Sprint(e["message"]), "text required") {
			t.Fatalf("remember without text should error text required, got %#v", resp[1])
		}
	})
}

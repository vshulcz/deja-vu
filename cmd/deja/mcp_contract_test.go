package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// driveMCP feeds line-delimited JSON-RPC requests through serveMCP (the exact
// code path `deja mcp` runs over stdio) and returns the parsed responses in
// order. This exercises the real request/response framing a client sees.
func driveMCP(t *testing.T, requests ...string) []map[string]any {
	t.Helper()
	in := strings.Join(requests, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
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
		// One tool, and the capabilities are its modes. The names below are
		// what the agent picks between, so they are what this asserts.
		if !got["deja"] {
			t.Fatalf("tools/list missing the deja tool; got %v", got)
		}
		tool := tools[0].(map[string]any)
		schema := tool["inputSchema"].(map[string]any)
		props := schema["properties"].(map[string]any)
		mode, ok := props["mode"].(map[string]any)
		if !ok {
			t.Fatalf("the tool has no mode: %#v", props)
		}
		modes := map[string]bool{}
		for _, m := range mode["enum"].([]any) {
			modes[m.(string)] = true
		}
		for _, want := range []string{"recall", "context", "blame", "fix", "how", "remember"} {
			if !modes[want] {
				t.Fatalf("mode %q is gone; got %v", want, modes)
			}
		}
	})

	// The names the six tools had still answer, so an agent configured before
	// the dispatcher keeps working.
	t.Run("the old tool names still answer", func(t *testing.T) {
		for _, name := range []string{"recall", "recall_context", "blame", "fix", "how"} {
			args := `{"query":"frobnicator","path":"parser.go","error":"boom","what":"go test"}`
			resp := driveMCP(t, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
			if _, bad := resp[0]["error"]; bad {
				t.Errorf("the %q name stopped answering: %v", name, resp[0])
			}
		}
	})

	// And the dispatcher reaches the same answer the old name did.
	t.Run("a mode answers like the tool it replaced", func(t *testing.T) {
		resp := driveMCP(t, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"deja","arguments":{"mode":"recall","query":"frobnicator","harness":"claude"}}}`)
		text := callText(t, resp[0])
		if !strings.Contains(text, "frobnicator") {
			t.Fatalf("mode recall = %q, want the frobnicator sessions", text)
		}
		bad := driveMCP(t, `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"deja","arguments":{"mode":"teleport","query":"frobnicator"}}}`)
		if _, isErr := bad[0]["error"]; !isErr {
			if text := callText(t, bad[0]); !strings.Contains(text, "not one of") {
				t.Fatalf("an invented mode was not named as one: %v", bad[0])
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
		// One served out of two that matched: the count line names both
		// numbers now, because "1 match(es)" read as "one exists" (#1308).
		if !strings.Contains(text, "(1 of 2 matched)") {
			t.Fatalf("limit=1 should serve one of the two matches, got %q", text)
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
		if !strings.Contains(text, "(1 of 2 matched)") {
			t.Fatalf("limit 1.0 should cap to one match, got %q", text)
		}
	})

	t.Run("large integer id echoed exactly", func(t *testing.T) {
		// Check raw wire bytes: parsing into map[string]any would itself round the
		// id to float64, so assert on the encoded response string.
		var out bytes.Buffer
		if err := serveMCP(index.DefaultDir(), strings.NewReader(`{"jsonrpc":"2.0","id":9007199254740993,"method":"tools/list","params":{}}`+"\n"), &out); err != nil {
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

func TestMCPResourcesAndPagination(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "app", "sess-alpha", "the frobnicator crash in parser.go", "fixed the frobnicator")
	seedClaude(t, claude, "app", "sess-beta", "another frobnicator regression today", "frobnicator again")

	t.Run("resources list and read", func(t *testing.T) {
		resp := driveMCP(t,
			`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}`)
		resources := resp[0]["result"].(map[string]any)["resources"].([]any)
		if len(resources) != 2 {
			t.Fatalf("want 2 resources, got %d: %#v", len(resources), resources)
		}
		first := resources[0].(map[string]any)
		uri, _ := first["uri"].(string)
		if !strings.HasPrefix(uri, "deja://session/claude:") {
			t.Fatalf("uri = %q", uri)
		}
		read := driveMCP(t, `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"`+uri+`"}}`)
		contents := read[0]["result"].(map[string]any)["contents"].([]any)
		text := contents[0].(map[string]any)["text"].(string)
		if !strings.Contains(text, "frobnicator") {
			t.Fatalf("resource text wrong:\n%s", text)
		}
	})

	t.Run("recall offset pages", func(t *testing.T) {
		resp := driveMCP(t,
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator","limit":1}}}`,
			`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator","limit":1,"offset":1}}}`,
			`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator","limit":1,"offset":9}}}`)
		page1 := mcpToolText(t, resp[0])
		page2 := mcpToolText(t, resp[1])
		past := mcpToolText(t, resp[2])
		if !strings.Contains(page1, "offset=1") {
			t.Fatalf("page1 must advertise the next offset:\n%s", page1)
		}
		if !strings.Contains(page2, "matches 2-2 of 2") {
			t.Fatalf("page2 header wrong:\n%s", page2)
		}
		id1, id2 := pageSessionID(page1), pageSessionID(page2)
		if id1 == "" || id2 == "" || id1 == id2 {
			t.Fatalf("pages must serve different sessions: %q vs %q", id1, id2)
		}
		if !strings.Contains(past, "No more matches") {
			t.Fatalf("past-the-end page wrong:\n%s", past)
		}
	})
}

func mcpToolText(t *testing.T, resp map[string]any) string {
	t.Helper()
	content := resp["result"].(map[string]any)["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

func pageSessionID(page string) string {
	for _, id := range []string{"sess-alpha", "sess-beta"} {
		if strings.Contains(page, id) {
			return id
		}
	}
	return ""
}

// What an agent needs back is what was decided, not a restatement of the
// symptom it just described. Recording a real agent session against a synthetic
// corpus, the model said the quiet part out loud: "the recall only preserved
// the incident title, not the exact fix."
func TestRecallReturnsTheDecisionNotOnlyTheQuestion(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "payments", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"prepared statements keep failing behind pgbouncer after the driver upgrade"}}`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"pgbouncer in transaction mode cannot hold those across connections. We pinned pgx to 5.4.3 and left a note to revisit later."}}`,
	})

	got, err := recallText(index.DefaultDir(), "prepared statements pgbouncer", "", 5, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "pinned pgx to 5.4.3") {
		t.Fatalf("recall returned the question without the decision:\n%s", got)
	}
	// The question stays too — the pair is the unit of memory.
	if !strings.Contains(got, "prepared statements keep failing") {
		t.Fatalf("recall dropped the question:\n%s", got)
	}
}

// TestMCPPingAndResourceTemplates covers #1720: neither `ping` nor
// `resources/templates/list` had a case in handleMCP, so both fell through
// to -32601. A host that pings for keepalive reads that error as a stale
// connection and drops the server, and we declare a resources capability,
// so clients ask for its templates as a matter of course.
func TestMCPPingAndResourceTemplates(t *testing.T) {
	hermeticEnv(t)

	resp := driveMCP(t,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/templates/list","params":{}}`,
	)
	if len(resp) != 2 {
		t.Fatalf("got %d responses, want 2: %#v", len(resp), resp)
	}

	for _, r := range resp {
		if e, ok := r["error"]; ok {
			t.Fatalf("response %v carried an error: %#v", r["id"], e)
		}
	}

	ping, ok := resp[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("ping result is not an object: %#v", resp[0])
	}
	if len(ping) != 0 {
		t.Errorf("ping result = %#v, want an empty object", ping)
	}

	templates, ok := resp[1]["result"].(map[string]any)
	if !ok {
		t.Fatalf("resources/templates/list result is not an object: %#v", resp[1])
	}
	list, ok := templates["resourceTemplates"].([]any)
	if !ok {
		t.Fatalf("result has no resourceTemplates array: %#v", templates)
	}
	if len(list) != 0 {
		t.Errorf("resourceTemplates = %#v, want an empty array", list)
	}
}

// TestMCPUndeclaredMethodStillErrors is the control for the case above: the
// fix must add exactly the two spec methods, not turn the default branch
// into a catch-all that answers anything.
func TestMCPUndeclaredMethodStillErrors(t *testing.T) {
	hermeticEnv(t)

	resp := driveMCP(t, `{"jsonrpc":"2.0","id":1,"method":"prompts/list","params":{}}`)
	if len(resp) != 1 {
		t.Fatalf("got %d responses, want 1", len(resp))
	}
	e, ok := resp[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("prompts/list did not error: %#v", resp[0])
	}
	if code, _ := e["code"].(float64); code != -32601 {
		t.Errorf("prompts/list error code = %v, want -32601", e["code"])
	}
}

// TestMCPResourceReadRefusesAnEmptySessionRef covers #1728: FindByPrefix
// matches on strings.HasPrefix, and every id has "" as a prefix, so a URI
// carrying no id at all returned a full session digest — a whole transcript
// the agent never asked for, echoed back under the URI it did send.
func TestMCPResourceReadRefusesAnEmptySessionRef(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "app", "sess-alpha", "the frobnicator crash in parser.go", "fixed the frobnicator")
	seedClaude(t, claude, "app", "sess-beta", "another frobnicator regression today", "frobnicator again")

	refused := []struct {
		name string
		uri  string
	}{
		{name: "no id at all", uri: "deja://session/"},
		{name: "harness prefix only", uri: "deja://session/claude:"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			resp := driveMCP(t, `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"`+tt.uri+`"}}`)
			e, ok := resp[0]["error"].(map[string]any)
			if !ok {
				t.Fatalf("%s was served instead of refused: %#v", tt.uri, resp[0])
			}
			if code, _ := e["code"].(float64); code != -32602 {
				t.Errorf("error code = %v, want -32602", e["code"])
			}
		})
	}

	// Control: a real URI from resources/list must still be served, so the
	// guard cannot be satisfied by refusing everything.
	t.Run("a full uri is still served", func(t *testing.T) {
		list := driveMCP(t, `{"jsonrpc":"2.0","id":2,"method":"resources/list","params":{}}`)
		resources := list[0]["result"].(map[string]any)["resources"].([]any)
		uri := resources[0].(map[string]any)["uri"].(string)

		read := driveMCP(t, `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"`+uri+`"}}`)
		if e, ok := read[0]["error"]; ok {
			t.Fatalf("real uri %q was refused: %#v", uri, e)
		}
		contents := read[0]["result"].(map[string]any)["contents"].([]any)
		if text := contents[0].(map[string]any)["text"].(string); !strings.Contains(text, "frobnicator") {
			t.Fatalf("resource text wrong:\n%s", text)
		}
	})
}

// Command mockmodel stands in for a model so a harness can be driven end to
// end, and records what it was actually sent.
//
// Checking that deja writes the right files says nothing about what a harness
// does with them. Checking against a real model says little either: the answer
// is the model's, the latency is the network's, and a laptop model falls over
// under the load. This serves the three wire protocols the harnesses speak,
// answers instantly with a fixed string, and writes every request it received
// to a log — so a sweep can ask the only question that matters, which is
// whether the memory reached the request.
//
// It also enforces the rule that a strict endpoint enforces: a system message
// may not follow a user message. That is not pedantry. deja's opencode plugin
// appended its recall as a second system block, and every turn against such an
// endpoint failed with "System message must be at the beginning" — found by
// driving the real interface, invisible to every other kind of test.
//
//	go run ./scripts/mockmodel -port 18777 -log /tmp/requests.jsonl
//
// Then point a harness at http://127.0.0.1:18777/v1 and read the log.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const model = "mock-model"

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type request struct {
	Model        string          `json:"model"`
	Stream       bool            `json:"stream"`
	Messages     []message       `json:"messages"`
	Input        json.RawMessage `json:"input"`
	Instructions string          `json:"instructions"`
	System       json.RawMessage `json:"system"`
	// Tools is what the harness says it can do. Every harness names its shell
	// tool differently — Bash, shell, run_commands — so a mock configured with
	// one fixed name calls something two of them do not have, and the whole
	// PreToolUse path silently goes untested. Reading the name out of the
	// request removes that per-harness knowledge.
	Tools json.RawMessage `json:"tools"`
}

// toolNames lists every tool the request declared, in the order given.
func toolNames(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var tools []struct {
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return nil
	}
	var out []string
	for _, t := range tools {
		if t.Name != "" {
			out = append(out, t.Name)
		} else if t.Function.Name != "" {
			out = append(out, t.Function.Name)
		}
	}
	return out
}

// declaredTool is one entry of the request's tools array, in whichever shape
// the wire protocol uses: {type,function:{name,parameters}} on chat
// completions, {type,name,parameters} on responses, {name,input_schema} on
// messages.
type declaredTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Parameters  json.RawMessage `json:"parameters"`
	InputSchema json.RawMessage `json:"input_schema"`
	Function    struct {
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
	} `json:"function"`
	// Tools is the namespace form: codex declares an MCP server as one entry
	// of type "namespace" holding the server's tools, rather than one flat
	// entry per tool. Calling the namespace itself does nothing, which is
	// what made codex the only harness whose MCP arm came back empty.
	Tools []declaredTool `json:"tools"`
}

func (d declaredTool) name() string {
	if d.Name != "" {
		return d.Name
	}
	return d.Function.Name
}

func (d declaredTool) schema() json.RawMessage {
	switch {
	case len(d.Parameters) > 0:
		return d.Parameters
	case len(d.InputSchema) > 0:
		return d.InputSchema
	default:
		return d.Function.Parameters
	}
}

// shellTool picks the tool that runs a command and builds the arguments its own
// schema asks for. Both halves have to come from the request: harnesses differ
// on the name (Bash, bash, exec_command) and on the shape — codex takes a
// string under "cmd", Claude takes one under "command", and another takes an
// argv array. Hardcoding either meant the call was rejected on arrival and the
// PreToolUse hook never ran, which reads like a harness that ignores tools.
func shellTool(raw json.RawMessage, command string) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}
	var tools []declaredTool
	if json.Unmarshal(raw, &tools) != nil {
		return "", ""
	}
	// Longest first so "run_commands" is not matched by "run".
	wanted := []string{"run_commands", "execute_command", "run_terminal_cmd",
		"exec_command", "local_shell", "bash", "shell", "terminal", "exec"}
	best, bestRank := declaredTool{}, len(wanted)
	for _, t := range tools {
		name := strings.ToLower(t.name())
		if name == "" {
			continue
		}
		for rank, w := range wanted {
			if name == w && rank < bestRank {
				best, bestRank = t, rank
			}
		}
	}
	if bestRank == len(wanted) {
		return "", ""
	}
	return best.name(), commandArguments(best.schema(), command)
}

// recallTool picks deja's own recall tool out of what the harness declared.
// The prefix is the harness's: deja__recall in one, mcp__deja__recall in
// another, a single mcp__deja entry in a third.
func recallTool(raw json.RawMessage, query string) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}
	var tools []declaredTool
	if json.Unmarshal(raw, &tools) != nil {
		return "", ""
	}
	var fallback declaredTool
	for _, t := range tools {
		name := strings.ToLower(t.name())
		if !strings.Contains(name, "deja") {
			continue
		}
		if t.Type == "namespace" || len(t.Tools) > 0 {
			if inner, args := recallInNamespace(t, query); inner != "" {
				return inner, args
			}
			continue
		}
		if strings.Contains(name, "recall") && !strings.Contains(name, "context") {
			return t.name(), queryArguments(t.schema(), query)
		}
		if fallback.name() == "" {
			fallback = t
		}
	}
	if fallback.name() == "" {
		return "", ""
	}
	return fallback.name(), queryArguments(fallback.schema(), query)
}

// recallInNamespace returns the name of the recall tool inside a namespace
// entry. The namespace declares no parameters of its own and its tools carry
// plain names, so the call goes to the plain name; both qualified forms —
// namespace.tool and namespace__tool — are rejected by codex's router.
func recallInNamespace(ns declaredTool, query string) (string, string) {
	var first declaredTool
	for _, t := range ns.Tools {
		name := strings.ToLower(t.name())
		if strings.Contains(name, "recall") && !strings.Contains(name, "context") {
			return t.name(), queryArguments(t.schema(), query)
		}
		if first.name() == "" {
			first = t
		}
	}
	if first.name() == "" {
		return "", ""
	}
	return first.name(), queryArguments(first.schema(), query)
}

// queryArguments renders the search text under whichever property the tool's
// schema names for it.
func queryArguments(schema json.RawMessage, query string) string {
	var doc struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	_ = json.Unmarshal(schema, &doc)
	for _, key := range []string{"query", "q", "search", "terms", "text", "prompt"} {
		if _, ok := doc.Properties[key]; ok {
			return `{"` + key + `":` + strconv.Quote(query) + `}`
		}
	}
	// A required property this mock does not know about would make the call
	// fail on arrival; naming the commonest one keeps the failure legible.
	return `{"query":` + strconv.Quote(query) + `}`
}

// commandArguments renders the command under the property the schema names for
// it, as the type the schema asks for.
func commandArguments(schema json.RawMessage, command string) string {
	var doc struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(schema, &doc)
	for _, key := range []string{"command", "cmd", "commands", "script"} {
		prop, ok := doc.Properties[key]
		if !ok {
			continue
		}
		if prop.Type == "array" {
			argv, _ := json.Marshal([]string{"/bin/sh", "-c", command})
			return `{"` + key + `":` + string(argv) + `}`
		}
		return `{"` + key + `":` + strconv.Quote(command) + `}`
	}
	// No property this mock recognises: fall back to the commonest spelling
	// rather than sending nothing, so the failure is visible in the harness's
	// own error instead of as silence.
	return `{"command":` + strconv.Quote(command) + `}`
}

type server struct {
	reply string
	// toolCall makes the first answer a call to the harness's shell tool
	// instead of text. Nothing else exercises what a harness does around a
	// tool — its PreToolUse hooks, its approval flow — and a mock that only
	// ever talks leaves that whole path untested.
	toolCall string
	// toolArg is the command the call asks for. It matters: deja's PreToolUse
	// hook speaks only about commands the history has seen, so a fixed `echo`
	// made every run look like a hook that produces nothing.
	toolArg string
	mu      sync.Mutex
	logw    *os.File
}

// resolveTool returns the tool this request should be answered with: the one
// named on the command line, or — with "auto" — whatever this harness calls
// its shell.
func (s *server) resolveTool(req request) (string, string) {
	if s.toolCall == "" {
		return "", ""
	}
	// Once per conversation, not once per process: a sweep points every
	// harness at one mock, and a process-wide flag meant the first harness
	// took the only tool call and every later one silently got prose — which
	// reads exactly like a harness that refuses to use tools.
	if alreadyCalled(req) {
		return "", ""
	}
	switch s.toolCall {
	case "auto":
		return shellTool(req.Tools, s.toolArg)
	case "recall":
		// The MCP path end to end: whether the server deja installs is
		// actually reachable from inside the harness and answers. Every other
		// check reads what deja put in the request; this one makes the harness
		// come back to deja for it.
		return recallTool(req.Tools, s.toolArg)
	}
	return s.toolCall, commandArguments(nil, s.toolArg)
}

// alreadyCalled reports whether this conversation already carries the result of
// a call, in any of the three protocols' spellings. Asking again would loop.
func alreadyCalled(req request) bool {
	var b strings.Builder
	for _, m := range req.Messages {
		// Chat completions send the result as its own message, whose only
		// marks are the role and a tool_call_id this struct does not keep.
		// Matching on the body alone missed it, and the mock then asked for
		// the same call again on every turn.
		if m.Role == "tool" {
			return true
		}
		b.WriteString(m.Role)
		b.Write(m.Content)
	}
	b.Write(req.Input)
	body := b.String()
	for _, marker := range []string{"tool_result", "function_call_output",
		"call_mock", "toolu_mock", "fc_mock"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func (s *server) record(path string, msgs []message) {
	s.recordWith(path, msgs, nil)
}

// recordWith also stores the tool names the request declared. Without them a
// run that produced no tool call is unreadable: it looks the same whether the
// harness offered nothing, offered something under a name the mock did not
// recognise, or refused the call.
func (s *server) recordWith(path string, msgs []message, tools []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logw == nil {
		return
	}
	rec := struct {
		Path     string    `json:"path"`
		At       time.Time `json:"at"`
		Messages []message `json:"messages"`
		Tools    []string  `json:"tools,omitempty"`
	}{path, time.Now().UTC(), msgs, tools}
	if b, err := json.Marshal(rec); err == nil {
		fmt.Fprintln(s.logw, string(b))
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	b, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprint(len(b)))
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

// sse writes one server-sent event and flushes: a harness reading a stream
// receives nothing until the writer flushes, and several of them only stream.
func sse(w http.ResponseWriter, event string, data any) {
	b, _ := json.Marshal(data)
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *server) models(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   []any{map[string]any{"id": model, "object": "model", "created": time.Now().Unix()}},
	})
}

// systemAfterUser is the rule a strict endpoint enforces and the one deja
// tripped on. Reporting it as a 400 rather than accepting it is the whole
// point: a mock that is more forgiving than production tests nothing.
func systemAfterUser(msgs []message) bool {
	for i, m := range msgs {
		if m.Role == "system" && i > 0 && msgs[i-1].Role != "system" {
			return true
		}
	}
	return false
}

func (s *server) chat(w http.ResponseWriter, r *http.Request, req request) {
	s.recordWith(r.URL.Path, req.Messages, toolNames(req.Tools))
	tool, toolArgs := s.resolveTool(req)
	if systemAfterUser(req.Messages) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "System message must be at the beginning."},
		})
		return
	}
	// One call, on the first turn only, the same rule the responses path
	// follows: the harness sends the result back, and asking again loops.
	// Without this the whole PreToolUse surface — deja's hook-tool, which
	// several harnesses wire — went untested against every harness that
	// speaks chat completions rather than responses.
	if tool != "" {
		call := map[string]any{"id": "call_mock", "type": "function",
			"function": map[string]any{"name": tool,
				"arguments": toolArgs}}
		if !req.Stream {
			writeJSON(w, http.StatusOK, map[string]any{
				"id": "mock", "object": "chat.completion", "created": time.Now().Unix(), "model": model,
				"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls",
					"message": map[string]any{"role": "assistant", "content": nil,
						"tool_calls": []any{call}}}},
				"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		streamed := map[string]any{"index": 0, "id": "call_mock", "type": "function",
			"function": map[string]any{"name": tool,
				"arguments": toolArgs}}
		for _, chunk := range []map[string]any{
			{"delta": map[string]any{"role": "assistant", "tool_calls": []any{streamed}}},
			{"delta": map[string]any{}, "finish_reason": "tool_calls"},
		} {
			body := map[string]any{"id": "mock", "object": "chat.completion.chunk",
				"created": time.Now().Unix(), "model": model}
			choice := map[string]any{"index": 0, "delta": chunk["delta"]}
			choice["finish_reason"] = chunk["finish_reason"]
			body["choices"] = []any{choice}
			sse(w, "", body)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	if !req.Stream {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "mock", "object": "chat.completion", "created": time.Now().Unix(), "model": model,
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": s.reply}}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	base := map[string]any{"id": "mock", "object": "chat.completion.chunk",
		"created": time.Now().Unix(), "model": model}
	for _, delta := range []any{
		map[string]any{"role": "assistant"},
		map[string]any{"content": s.reply},
		map[string]any{},
	} {
		chunk := map[string]any{}
		for k, v := range base {
			chunk[k] = v
		}
		finish := any(nil)
		if len(delta.(map[string]any)) == 0 {
			finish = "stop"
		}
		chunk["choices"] = []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}
		sse(w, "", chunk)
	}
	// The terminator is a literal, not a JSON string: quoting it made a real
	// harness fail with `Type validation failed: Value: "[DONE]"`.
	fmt.Fprint(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *server) responses(w http.ResponseWriter, r *http.Request, req request) {
	tool, toolArgs := s.resolveTool(req)
	s.recordWith(r.URL.Path, []message{
		{Role: "instructions", Content: json.RawMessage(strconv.Quote(req.Instructions))},
		{Role: "input", Content: req.Input},
	}, toolNames(req.Tools))
	output := []any{map[string]any{"id": "msg_mock", "type": "message", "role": "assistant",
		"status":  "completed",
		"content": []any{map[string]any{"type": "output_text", "text": s.reply, "annotations": []any{}}}}}
	// One call per conversation: the harness sends the result back, and asking
	// again would loop. resolveTool decides that from the request.
	if tool != "" {
		output = []any{map[string]any{
			"id": "fc_mock", "type": "function_call", "status": "completed",
			"name": tool, "call_id": "call_mock",
			"arguments": toolArgs,
		}}
	}
	body := map[string]any{
		"id": "resp_mock", "object": "response", "created_at": time.Now().Unix(),
		"status": "completed", "model": model,
		"output": output,
		"usage":  map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	}
	if !req.Stream {
		writeJSON(w, http.StatusOK, body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	inProgress := map[string]any{}
	for k, v := range body {
		inProgress[k] = v
	}
	inProgress["status"] = "in_progress"
	inProgress["output"] = []any{}
	sse(w, "response.created", map[string]any{"type": "response.created", "response": inProgress})
	// A streamed function call is its own event sequence. Streaming text
	// deltas and then completing with a function_call in the output is a
	// response no client can reconcile: codex took it as a disconnected
	// stream and gave up before running anything.
	if item, ok := output[0].(map[string]any); ok && item["type"] == "function_call" {
		sse(w, "response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": 0, "item": item})
		sse(w, "response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": "fc_mock",
			"output_index": 0, "delta": item["arguments"]})
		sse(w, "response.function_call_arguments.done", map[string]any{
			"type": "response.function_call_arguments.done", "item_id": "fc_mock",
			"output_index": 0, "arguments": item["arguments"]})
		sse(w, "response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": 0, "item": item})
		sse(w, "response.completed", map[string]any{"type": "response.completed", "response": body})
		return
	}
	sse(w, "response.output_text.delta", map[string]any{
		"type": "response.output_text.delta", "item_id": "msg_mock",
		"output_index": 0, "content_index": 0, "delta": s.reply})
	sse(w, "response.completed", map[string]any{"type": "response.completed", "response": body})
}

func (s *server) messages(w http.ResponseWriter, r *http.Request, req request) {
	msgs := req.Messages
	if len(req.System) > 0 {
		msgs = append([]message{{Role: "system", Content: req.System}}, msgs...)
	}
	s.recordWith(r.URL.Path, msgs, toolNames(req.Tools))
	body := map[string]any{
		"id": "msg_mock", "type": "message", "role": "assistant", "model": model,
		"content":     []any{map[string]any{"type": "text", "text": s.reply}},
		"stop_reason": "end_turn", "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
	}
	// The tool call, on the first turn only, as on the other two paths. This
	// is what puts deja's PreToolUse hook under test: it is wired for Claude,
	// codex and grok, and of those only Claude runs hooks headless.
	if tool, args := s.resolveTool(req); tool != "" {
		var input map[string]any
		_ = json.Unmarshal([]byte(args), &input)
		body["content"] = []any{map[string]any{
			"type": "tool_use", "id": "toolu_mock", "name": tool,
			"input": input,
		}}
		body["stop_reason"] = "tool_use"
	}
	if !req.Stream {
		writeJSON(w, http.StatusOK, body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	start := map[string]any{}
	for k, v := range body {
		start[k] = v
	}
	start["content"] = []any{}
	sse(w, "message_start", map[string]any{"type": "message_start", "message": start})
	if block, ok := body["content"].([]any); ok && len(block) > 0 {
		if first, ok := block[0].(map[string]any); ok && first["type"] == "tool_use" {
			sse(w, "content_block_start", map[string]any{"type": "content_block_start",
				"index": 0, "content_block": map[string]any{"type": "tool_use",
					"id": first["id"], "name": first["name"], "input": map[string]any{}}})
			raw, _ := json.Marshal(first["input"])
			sse(w, "content_block_delta", map[string]any{"type": "content_block_delta",
				"index": 0, "delta": map[string]any{"type": "input_json_delta",
					"partial_json": string(raw)}})
			sse(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			sse(w, "message_delta", map[string]any{"type": "message_delta",
				"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil},
				"usage": map[string]any{"output_tokens": 1}})
			sse(w, "message_stop", map[string]any{"type": "message_stop"})
			return
		}
	}
	sse(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""}})
	sse(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": s.reply}})
	sse(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	sse(w, "message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 1}})
	sse(w, "message_stop", map[string]any{"type": "message_stop"})
}

func (s *server) post(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case strings.HasSuffix(path, "/responses"):
		s.responses(w, r, req)
	case strings.HasSuffix(path, "/messages"):
		s.messages(w, r, req)
	default:
		s.chat(w, r, req)
	}
}

func main() {
	port := flag.Int("port", 18777, "port to listen on")
	logPath := flag.String("log", "", "write every request here as JSON lines")
	reply := flag.String("reply", "47", "what the model answers")
	toolCall := flag.String("tool-call", "", "answer the first turn by calling this tool (e.g. shell)")
	toolArg := flag.String("tool-arg", "echo mockmodel probe",
		"the command -tool-call asks for; use one the corpus has run to exercise the PreToolUse hook")
	flag.Parse()

	s := &server{reply: *reply, toolCall: *toolCall, toolArg: *toolArg}
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		s.logw = f
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.models(w, r)
			return
		}
		s.post(w, r)
	})
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	fmt.Printf("mockmodel on http://%s/v1 (log %q)\n", addr, *logPath)
	log.Fatal(http.ListenAndServe(addr, mux))
}

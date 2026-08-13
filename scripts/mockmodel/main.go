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
}

type server struct {
	reply string
	mu    sync.Mutex
	logw  *os.File
}

func (s *server) record(path string, msgs []message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logw == nil {
		return
	}
	rec := struct {
		Path     string    `json:"path"`
		At       time.Time `json:"at"`
		Messages []message `json:"messages"`
	}{path, time.Now().UTC(), msgs}
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
	s.record(r.URL.Path, req.Messages)
	if systemAfterUser(req.Messages) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "System message must be at the beginning."},
		})
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
	s.record(r.URL.Path, []message{
		{Role: "instructions", Content: json.RawMessage(strconv.Quote(req.Instructions))},
		{Role: "input", Content: req.Input},
	})
	body := map[string]any{
		"id": "resp_mock", "object": "response", "created_at": time.Now().Unix(),
		"status": "completed", "model": model,
		"output": []any{map[string]any{"id": "msg_mock", "type": "message", "role": "assistant",
			"status":  "completed",
			"content": []any{map[string]any{"type": "output_text", "text": s.reply, "annotations": []any{}}}}},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
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
	s.record(r.URL.Path, msgs)
	body := map[string]any{
		"id": "msg_mock", "type": "message", "role": "assistant", "model": model,
		"content":     []any{map[string]any{"type": "text", "text": s.reply}},
		"stop_reason": "end_turn", "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
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
	flag.Parse()

	s := &server{reply: *reply}
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

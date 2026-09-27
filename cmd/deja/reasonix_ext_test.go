package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A fake Reasonix host on the other end of the pipes: it answers the
// sidecar's own requests (UI publishes, content reads) and hands every other
// frame to the test.
type fakeRxHost struct {
	t       *testing.T
	toExt   *io.PipeWriter
	frames  chan rxFrame
	done    chan error
	mu      sync.Mutex
	publish []map[string]any
	content map[string][]byte
	nextID  int
	dir     string
}

type fakeHookCall struct {
	args  []string
	input map[string]any
}

// fakeRxHooks stands in for the hook children. answer gets the subcommand
// and the decoded payload and returns what the child would have printed.
func fakeRxHooks(t *testing.T, answer func(sub string, in map[string]any) (string, error)) func() []fakeHookCall {
	t.Helper()
	var mu sync.Mutex
	var calls []fakeHookCall
	prev := rxRunHook
	rxRunHook = func(ctx context.Context, _ string, args []string, stdin []byte) (string, error) {
		var in map[string]any
		_ = json.Unmarshal(stdin, &in)
		mu.Lock()
		calls = append(calls, fakeHookCall{args: append([]string(nil), args...), input: in})
		mu.Unlock()
		type answered struct {
			out string
			err error
		}
		ch := make(chan answered, 1)
		go func() {
			out, err := answer(args[0], in)
			ch <- answered{out, err}
		}()
		// The real child is killed at the deadline; the fake is let go of.
		select {
		case a := <-ch:
			return a.out, a.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	t.Cleanup(func() { rxRunHook = prev })
	return func() []fakeHookCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]fakeHookCall(nil), calls...)
	}
}

func startFakeRxHost(t *testing.T) *fakeRxHost {
	t.Helper()
	t.Setenv("REASONIX_HOME", t.TempDir())
	t.Setenv("REASONIX_STATE_HOME", "")
	t.Setenv("DEJA_RECALL", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &fakeRxHost{t: t, toExt: inW, frames: make(chan rxFrame, 64), done: make(chan error, 1), content: map[string][]byte{}, nextID: 100}
	h.dir = filepath.Join(t.TempDir(), "index.db")
	x := newRxExt(h.dir, "deja-under-test", newRxConn(inR, outW))
	go func() {
		h.done <- x.serve()
		_ = outW.Close()
	}()
	go func() {
		r := bufio.NewReader(outR)
		for {
			line, err := r.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) > 0 {
				var f rxFrame
				if json.Unmarshal(line, &f) != nil {
					t.Errorf("sidecar wrote a line that is not JSON: %q", line)
				} else {
					h.handle(f)
				}
			}
			if err != nil {
				close(h.frames)
				return
			}
		}
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return h
}

func (h *fakeRxHost) handle(f rxFrame) {
	switch f.Method {
	case "host/ui/publish":
		var p map[string]any
		_ = json.Unmarshal(f.Params, &p)
		h.mu.Lock()
		h.publish = append(h.publish, p)
		h.mu.Unlock()
		h.write(rxFrame{ID: f.ID, Result: json.RawMessage(`{"accepted":true}`)})
	case "host/content/read":
		var p struct {
			ContentRef string `json:"contentRef"`
			Offset     int64  `json:"offset"`
		}
		_ = json.Unmarshal(f.Params, &p)
		h.mu.Lock()
		data := h.content[p.ContentRef]
		h.mu.Unlock()
		end := p.Offset + 5
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		page := map[string]any{"contentRef": p.ContentRef, "offset": p.Offset, "encoding": "utf8",
			"dataBase64": base64.StdEncoding.EncodeToString(data[p.Offset:end]), "totalBytes": len(data)}
		if end < int64(len(data)) {
			page["nextOffset"] = end
		}
		b, _ := json.Marshal(page)
		h.write(rxFrame{ID: f.ID, Result: b})
	default:
		h.frames <- f
	}
}

func (h *fakeRxHost) write(f rxFrame) {
	f.JSONRPC = "2.0"
	b, _ := json.Marshal(f)
	if _, err := h.toExt.Write(append(b, '\n')); err != nil {
		h.t.Errorf("write to sidecar: %v", err)
	}
}

func (h *fakeRxHost) raw(line string) {
	if _, err := h.toExt.Write([]byte(line + "\n")); err != nil {
		h.t.Errorf("write to sidecar: %v", err)
	}
}

// request sends one request and returns the sidecar's answer to it.
func (h *fakeRxHost) request(method string, params any) rxFrame {
	h.t.Helper()
	h.nextID++
	id := json.RawMessage(fmt.Sprint(h.nextID))
	b, _ := json.Marshal(params)
	h.write(rxFrame{ID: id, Method: method, Params: b})
	return h.await(id)
}

func (h *fakeRxHost) await(id json.RawMessage) rxFrame {
	h.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f, ok := <-h.frames:
			if !ok {
				h.t.Fatalf("sidecar closed its output before answering %s", id)
			}
			if string(f.ID) == string(id) {
				return f
			}
			h.t.Fatalf("unexpected frame while waiting for %s: %+v", id, f)
		case <-deadline:
			h.t.Fatalf("no answer to request %s", id)
		}
	}
}

func (h *fakeRxHost) notify(method string, params any) {
	b, _ := json.Marshal(params)
	h.write(rxFrame{Method: method, Params: b})
}

func (h *fakeRxHost) published() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.publish...)
}

func (h *fakeRxHost) handshake(intercepts ...string) map[string]any {
	h.t.Helper()
	if len(intercepts) == 0 {
		intercepts = reasonixIntercepts
	}
	f := h.request("extension/initialize", map[string]any{
		"protocolVersion": "2", "protocolId": "reasonix.extension.v2",
		"manifest":     map[string]any{"intercepts": intercepts, "capabilities": []string{"interceptors", "ui"}},
		"session":      map[string]any{"sessionId": "boot-1", "workspaceRoot": h.t.TempDir(), "generation": 1},
		"capabilities": map[string]any{"contentRefs": true, "uiHost": "tui", "protocolVersion": "2"},
	})
	if f.Error != nil {
		h.t.Fatalf("handshake refused: %+v", f.Error)
	}
	h.notify("extension/initialized", map[string]any{})
	var res map[string]any
	if err := json.Unmarshal(f.Result, &res); err != nil {
		h.t.Fatal(err)
	}
	return res
}

func (h *fakeRxHost) intercept(event string, payload any) rxInterceptResult {
	h.t.Helper()
	f := h.request("extension/intercept", map[string]any{"event": event, "seq": 1, "payload": payload, "timeoutMillis": 5000})
	if f.Error != nil {
		h.t.Fatalf("%s answered with an error: %+v", event, f.Error)
	}
	var r rxInterceptResult
	if err := json.Unmarshal(f.Result, &r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

func replacementField(t *testing.T, r rxInterceptResult, key string) string {
	t.Helper()
	if r.Decision != "replace" {
		t.Fatalf("decision = %q, want replace", r.Decision)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(r.Replacement, &m); err != nil {
		t.Fatal(err)
	}
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

func TestReasonixExtHandshakeSubscribesWithinTheManifest(t *testing.T) {
	h := startFakeRxHost(t)
	res := h.handshake("input.receive", "tool.after", "permission.decision")
	// The host decodes this strictly: any field outside InitializeResult fails
	// the handshake, so the answer carries exactly these.
	for k := range res {
		switch k {
		case "protocolVersion", "name", "version", "subscriptions", "stateSchemaVersion":
		default:
			t.Errorf("handshake answer carries %q, which the host's InitializeResult does not have", k)
		}
	}
	got, _ := json.Marshal(res["subscriptions"])
	if string(got) != `["input.receive","tool.after"]` {
		t.Errorf("subscriptions = %s, want only what both deja and the manifest name", got)
	}
	if res["protocolVersion"] != "2" || res["name"] != "deja" {
		t.Errorf("identity = %v/%v", res["protocolVersion"], res["name"])
	}
}

func TestReasonixExtRefusesAnotherProtocol(t *testing.T) {
	h := startFakeRxHost(t)
	f := h.request("extension/initialize", map[string]any{"protocolVersion": "3", "protocolId": "reasonix.extension.v2"})
	if f.Error == nil || f.Error.Code != rxDomainErrorCode || !strings.Contains(string(f.Error.Data), "unsupported_version") {
		t.Fatalf("answer = %+v, want the frozen unsupported_version refusal", f.Error)
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sidecar kept serving after refusing the handshake")
	}
}

func TestReasonixExtSurvivesMalformedFrames(t *testing.T) {
	fakeRxHooks(t, func(string, map[string]any) (string, error) { return "", nil })
	h := startFakeRxHost(t)
	h.handshake()
	h.raw("{not json")
	h.raw("")
	h.notify("extension/event", map[string]any{"event": "provider.request", "payload": map[string]any{}})
	f := h.request("extension/provider/catalog", map[string]any{})
	if f.Error == nil || f.Error.Code != -32601 {
		t.Errorf("unknown method answer = %+v, want -32601", f.Error)
	}
	h.nextID++
	id := json.RawMessage(fmt.Sprint(h.nextID))
	h.write(rxFrame{ID: id, Method: "extension/intercept", Params: json.RawMessage(`"not an object"`)})
	if f := h.await(id); f.Error == nil || f.Error.Code != -32602 {
		t.Errorf("unreadable intercept answer = %+v, want -32602", f.Error)
	}
	if r := h.intercept("input.receive", map[string]any{"text": "still here"}); r.Decision != "continue" {
		t.Errorf("after the garbage the sidecar answered %+v", r)
	}
	if r := h.intercept("input.receive", "a string, not an object"); r.Decision != "continue" {
		t.Errorf("a payload of the wrong shape answered %+v, want continue", r)
	}
	f = h.request("extension/shutdown", map[string]any{"timeoutMillis": 1000})
	if f.Error != nil || !strings.Contains(string(f.Result), `"accepted":true`) {
		t.Errorf("shutdown answer = %+v", f)
	}
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sidecar did not stop after shutdown")
	}
}

func TestReasonixExtReadsAnExternalizedPayload(t *testing.T) {
	fakeRxHooks(t, func(sub string, in map[string]any) (string, error) {
		if sub == "hook-prompt" && strings.Contains(fmt.Sprint(in["prompt"]), "long question") {
			return "<deja-recall>\nRECALL\n</deja-recall>", nil
		}
		return "", nil
	})
	h := startFakeRxHost(t)
	h.handshake()
	payload := []byte(`{"text":"a long question that did not fit in the frame"}`)
	sum := sha256.Sum256(payload)
	h.mu.Lock()
	h.content["ref-1"] = payload
	h.mu.Unlock()
	send := func(digest string) rxInterceptResult {
		f := h.request("extension/intercept", map[string]any{"event": "input.receive", "seq": 2, "payload": nil, "timeoutMillis": 5000,
			"externalized": []map[string]any{{"jsonPointer": "/payload", "contentRef": "ref-1", "totalBytes": len(payload), "sha256": digest}}})
		var r rxInterceptResult
		_ = json.Unmarshal(f.Result, &r)
		return r
	}
	if text := replacementField(t, send(hex.EncodeToString(sum[:])), "text"); !strings.HasPrefix(text, "a long question") || !strings.Contains(text, "RECALL") {
		t.Errorf("text = %q, want the paged-back question with recall after it", text)
	}
	if r := send(strings.Repeat("0", 64)); r.Decision != "continue" {
		t.Errorf("content that does not match its hash answered %+v, want continue", r)
	}
}

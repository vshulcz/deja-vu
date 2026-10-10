package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// serveFrames runs the stdio server over a fixed input and returns every
// reply it wrote.
func serveFrames(t *testing.T, frames ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := serveMCP(index.DefaultDir(), strings.NewReader(strings.Join(frames, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var replies []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("reply %q: %v", line, err)
		}
		replies = append(replies, m)
	}
	return replies
}

func rpcErrorCode(m map[string]any) int {
	e, _ := m["error"].(map[string]any)
	code, _ := e["code"].(float64)
	return int(code)
}

// A request with no method is an invalid request, not a method nobody has. An
// id that is not a string or a number is refused rather than echoed, and a
// null id on a request gets an answer instead of the silence a client waits
// on forever. A null-id notification stays unanswered.
func TestMCPRefusesMalformedRequestsAsInvalid(t *testing.T) {
	hermeticEnv(t)
	for _, tc := range []struct {
		frame  string
		code   int
		nullID bool
	}{
		{`{"jsonrpc":"2.0","id":5}`, -32600, false},
		{`{"jsonrpc":"2.0","id":null,"method":"tools/list"}`, -32600, true},
		{`{"jsonrpc":"2.0","id":{"a":1},"method":"tools/list"}`, -32600, true},
		{`{"jsonrpc":"2.0","id":true,"method":"tools/list"}`, -32600, true},
	} {
		replies := serveFrames(t, tc.frame)
		if len(replies) != 1 {
			t.Errorf("%s: %d replies, want 1", tc.frame, len(replies))
			continue
		}
		if got := rpcErrorCode(replies[0]); got != tc.code {
			t.Errorf("%s: code %d, want %d (%v)", tc.frame, got, tc.code, replies[0])
		}
		if tc.nullID && replies[0]["id"] != nil {
			t.Errorf("%s: echoed id %v", tc.frame, replies[0]["id"])
		}
	}
	if replies := serveFrames(t, `{"jsonrpc":"2.0","id":null,"method":"notifications/initialized"}`); len(replies) != 0 {
		t.Errorf("a null-id notification was answered: %v", replies)
	}
}

// A tool that ran and failed is a result marked isError, which the client
// shows the model. Reported as invalid params, it blamed the agent's call for
// a notes file the agent cannot fix by calling differently.
func TestMCPToolFailureIsAResultNotInvalidParams(t *testing.T) {
	tmp := hermeticEnv(t)
	blocker := filepath.Join(tmp, "a-file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(blocker, "notes.jsonl"))
	replies := serveFrames(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remember","arguments":{"text":"the outbox owns retries","project":"payments"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"remember","arguments":{"text":""}}}`,
	)
	if len(replies) != 2 {
		t.Fatalf("replies %v", replies)
	}
	byID := map[float64]map[string]any{}
	for _, r := range replies {
		id, _ := r["id"].(float64)
		byID[id] = r
	}
	res, _ := byID[1]["result"].(map[string]any)
	if res == nil || res["isError"] != true {
		t.Errorf("a failed write answered %v, want a result with isError", byID[1])
	}
	if got := rpcErrorCode(byID[2]); got != -32602 {
		t.Errorf("a missing argument answered %v, want -32602", byID[2])
	}
}

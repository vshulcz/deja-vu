package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// decodeRPCFrames reads the server's replies, one JSON object per line.
func decodeRPCFrames(t *testing.T, out string) []map[string]any {
	t.Helper()
	var frames []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var f map[string]any
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("reply is not JSON: %q", line)
		}
		frames = append(frames, f)
	}
	return frames
}

// A batch is valid JSON, so answering "parse error" tells a client its bytes
// were corrupt when they were not. deja does not run batches; the reply has to
// say that instead (#1795).
func TestABatchIsRefusedAsInvalidNotAsUnparseable(t *testing.T) {
	hermeticEnv(t)
	in := `[{"jsonrpc":"2.0","id":2,"method":"ping","params":{}},{"jsonrpc":"2.0","id":3,"method":"ping","params":{}}]` + "\n"
	var out bytes.Buffer
	if err := serveMCP(t.TempDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	frames := decodeRPCFrames(t, out.String())
	if len(frames) != 1 {
		t.Fatalf("want one reply to a batch, got %d: %s", len(frames), out.String())
	}
	e, ok := frames[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("a batch was served rather than refused: %s", out.String())
	}
	if code, _ := e["code"].(float64); int(code) != -32600 {
		t.Errorf("a batch is refused with code %v, want -32600 invalid request", e["code"])
	}
	if msg, _ := e["message"].(string); !strings.Contains(strings.ToLower(msg), "batch") {
		t.Errorf("the refusal does not say what deja will not do: %q", msg)
	}
}

// A frame that really is not JSON keeps its parse error — the control that the
// change above did not turn every bad frame into "invalid request".
func TestCorruptBytesStillParseError(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	if err := serveMCP(t.TempDir(), strings.NewReader("{not json\n"), &out); err != nil {
		t.Fatal(err)
	}
	frames := decodeRPCFrames(t, out.String())
	if len(frames) != 1 {
		t.Fatalf("want one reply, got %d", len(frames))
	}
	e, _ := frames[0]["error"].(map[string]any)
	if code, _ := e["code"].(float64); int(code) != -32700 {
		t.Errorf("corrupt bytes answered with %v, want -32700 parse error", e["code"])
	}
}

// The version member is the one field that says which protocol the frame
// speaks. Serving "1.0" as if it were "2.0" answers a request nobody made.
func TestAWrongJSONRPCVersionIsRefused(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	in := `{"jsonrpc":"1.0","id":4,"method":"ping","params":{}}` + "\n"
	if err := serveMCP(t.TempDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	frames := decodeRPCFrames(t, out.String())
	if len(frames) != 1 {
		t.Fatalf("want one reply, got %d: %s", len(frames), out.String())
	}
	e, ok := frames[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("jsonrpc 1.0 was served: %s", out.String())
	}
	if code, _ := e["code"].(float64); int(code) != -32600 {
		t.Errorf("jsonrpc 1.0 refused with %v, want -32600", e["code"])
	}
	if id, _ := frames[0]["id"].(float64); int(id) != 4 {
		t.Errorf("the refusal came back with id %v, so a client cannot match it to the request", frames[0]["id"])
	}
}

// A frame that omits the member entirely is still served: clients in the wild
// do it and the request is unambiguous. Pinned so the leniency is deliberate.
func TestAMissingJSONRPCVersionIsStillServed(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	if err := serveMCP(t.TempDir(), strings.NewReader(`{"id":5,"method":"ping","params":{}}`+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	frames := decodeRPCFrames(t, out.String())
	if len(frames) != 1 || frames[0]["error"] != nil {
		t.Fatalf("a frame without the version member was refused: %s", out.String())
	}
}

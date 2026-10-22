package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// A cut payload is salvaged from the raw bytes, and that only looked under
// tool_response at the keys Claude and Codex use. Gemini CLI puts a command's
// output in tool_response.llmContent (and again in returnDisplay), Qwen Code
// puts its report in a top-level error, so a long failing command from either
// came back with nothing (#4314). The whole payloads are the control: the
// index has the pair and the decode path reads both shapes.
func TestToolAfterSalvagesACutGeminiOrQwenPayload(t *testing.T) {
	const knownErr = "panic: sql: database is closed"
	seedFixPair(t, knownErr, "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")

	str := func(s string) string {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	payload := func(shape string, pad int) []byte {
		body := knownErr + "\n" + strings.Repeat("build: checking package\n", pad)
		head := `{"session_id":"` + shape + `","cwd":"/work/app","tool_input":{"command":"make deploy"},"tool_name":"run_shell_command",`
		switch shape {
		case "gemini":
			return []byte(head + `"tool_response":{"llmContent":` + str("<untrusted_context>\nOutput: "+body+"Exit Code: 1\nProcess Group PGID: 4242\n</untrusted_context>") +
				`,"returnDisplay":` + str(body) + `}}`)
		default:
			return []byte(head + `"error":` + str("Command: make deploy\nDirectory: (root)\nOutput: "+body+"Error: (none)\nExit Code: 1\nSignal: (none)\nProcess Group PGID: 4242") + `}`)
		}
	}
	for _, shape := range []string{"gemini", "qwen"} {
		for _, pad := range []int{3, 60000} {
			raw := payload(shape, pad)
			if pad > 3 && len(raw) <= 1<<20 {
				t.Fatalf("%s: the cut payload is %d bytes, under the stdin bound", shape, len(raw))
			}
			var out bytes.Buffer
			if err := runHookToolAfter(dir, bytes.NewReader(raw), &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "what followed it") {
				t.Errorf("%s payload of %d bytes got no fix:\n%q", shape, len(raw), out.String())
			}
		}
	}
}

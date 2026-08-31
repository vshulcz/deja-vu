package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Every MCP answer that carries text out of somebody's transcript says so.
//
// recall_frame.go states the rule — recalled text is data an attacker may have
// influenced, so agent-facing output is framed as untrusted — and it was kept
// by hand, one surface at a time. `how` and `fix` were both missing it (#2844,
// #2847), and they are the two that serve commands an agent may run. A tool
// added later would be missing it in the same silence, so the property is
// pinned here rather than left to the next sweep.
func TestEveryMCPAnswerCarryingTranscriptTextSaysItIsUntrusted(t *testing.T) {
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	const errLine = "ld: symbol(s) not found for architecture arm64"
	// Two sessions, each holding everything the tools below need: prose to
	// recall, a file to blame, an error, and the command that followed it.
	for i, sid := range []string{"s1", "s2"} {
		day := string(rune('1' + i))
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + day + `T09:00:00Z","message":{"role":"user","content":"the zonkomatic build in internal/index/ingest.go keeps breaking"}}
{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + day + `T09:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"` + errLine + `"}]}}
{"type":"assistant","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + day + `T09:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go build -ldflags=-w ./internal/index"}}]}}
`
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ tool, args, must string }{
		{"recall", `{"query":"zonkomatic build"}`, "zonkomatic"},
		{"recall_context", `{"query":"zonkomatic build"}`, "zonkomatic"},
		{"blame", `{"path":"internal/index/ingest.go"}`, "ingest.go"},
		{"how", `{"what":"ldflags"}`, "go build"},
		{"fix", `{"error":"` + errLine + `"}`, "go build"},
	} {
		text, err := callMCPTool(dir, tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Errorf("%s: %v", tc.tool, err)
			continue
		}
		// A tool that answered nothing proves nothing: the fixture has to make
		// each one speak, or this passes for the wrong reason.
		if !strings.Contains(text, tc.must) {
			t.Errorf("%s: served nothing from the fixture, so its framing is untested:\n%s", tc.tool, firstLines(text, 3))
			continue
		}
		if !strings.Contains(text, "untrusted reference data") {
			t.Errorf("%s: hands over transcript text with nothing saying where it came from:\n%s", tc.tool, firstLines(text, 3))
		}
	}

	// The resources surface is the fifth way an agent gets a session out of
	// deja, and it once skipped this wrapper — the comment at the call site
	// says so. It goes through a different entry point than the tools above,
	// which is why it was outside the loop and outside this property.
	answers := driveMCP(t,
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"deja://session/claude:s1"}}`)
	if len(answers) == 0 {
		t.Fatal("the resources surface answered nothing at all")
	}
	res, ok := answers[0]["result"]
	if !ok {
		t.Fatalf("the resource read failed: %v", answers[0])
	}
	// Unwrapped here rather than through resourceText: driveMCP round-trips
	// through JSON, so the contents arrive as []any.
	contents, ok := res.(map[string]any)["contents"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("the resource answered with no contents: %v", res)
	}
	text, _ := contents[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "zonkomatic") {
		t.Fatalf("resource: served nothing from the fixture, so its framing is untested:\n%s", firstLines(text, 3))
	}
	if !strings.Contains(text, "untrusted reference data") {
		t.Errorf("resource: hands over a whole session with nothing saying where it came from:\n%s", firstLines(text, 3))
	}
}

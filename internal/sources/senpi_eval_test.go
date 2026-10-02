package sources

import (
	"strings"
	"testing"
)

// With codemode loaded, Senpi's default, the shell runs only inside an eval
// cell as tool.bash(...): the model's toolCall is `eval` with JS, and what ran
// is on the result, under details.toolCalls[].args. The first cell is the one a
// senpi 2026.10.1-3 session wrote; the second carries a read, an edit and a
// refused edit in the same shape (#4425).
const senpiEvalFixture = `{"type":"session","version":3,"id":"senpi-eval","timestamp":"2026-10-01T23:16:48.020Z","cwd":"/private/tmp/proj-senpi"}
{"type":"message","id":"u1","timestamp":"2026-10-01T23:16:49.000Z","message":{"role":"user","content":[{"type":"text","text":"Check the retry config."}]}}
{"type":"message","id":"a1","timestamp":"2026-10-01T23:16:50.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_524cd669c1bb","name":"eval","arguments":{"language":"js","summary":"reading the retry config","code":"const r = await tool.bash({ command: \"cat retry.cfg && git status --short\" }); r"}}]}}
{"type":"message","id":"r1","timestamp":"2026-10-01T23:16:51.000Z","message":{"role":"toolResult","toolCallId":"call_524cd669c1bb","toolName":"eval","content":[{"type":"text","text":"{\"text\":\"retries = 3\\nmax_backoff_seconds = 30  # cap for the retry loop\\n\\nCommand exited with code 128\"}"}],"details":{"language":"js","toolCallCount":1,"toolCalls":[{"name":"bash","ok":true,"durationMs":530,"callId":"js-ee43","args":{"command":"cat retry.cfg && git status --short"},"resultPreview":"retries = 3"}]},"isError":false}}
{"type":"message","id":"a2","timestamp":"2026-10-01T23:16:52.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_2","name":"eval","arguments":{"language":"js","code":"await tool.read({path:\"retry.cfg\"}); await tool.edit(...)"}}]}}
{"type":"message","id":"r2","timestamp":"2026-10-01T23:16:53.000Z","message":{"role":"toolResult","toolCallId":"call_2","toolName":"eval","content":[{"type":"text","text":"done"}],"details":{"toolCalls":[{"name":"read","ok":true,"args":{"path":"retry.cfg"}},{"name":"edit","ok":true,"args":{"path":"retry.cfg","edits":[{"oldText":"max_backoff_seconds = 30  # cap for the retry loop","newText":"max_backoff_seconds = 120  # cap for the retry loop"}]}},{"name":"edit","ok":false,"args":{"path":"retry.cfg","edits":[{"oldText":"a line the file never held at any point","newText":"what the agent hoped to write there instead"}]}}]},"isError":false}}
`

func TestSenpiEvalCellCallsAreIndexed(t *testing.T) {
	ss, err := ParseSenpiFile(writePiFixture(t, senpiEvalFixture))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	cmds := rolesOf(s, RoleCommand)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "cat retry.cfg && git status --short") {
		t.Errorf("commands = %q, want the one the eval cell ran", cmds)
	}
	files := strings.Join(rolesOf(s, RoleFiles), "\n")
	if !strings.Contains(files, "/private/tmp/proj-senpi/retry.cfg") {
		t.Errorf("files = %q, want retry.cfg resolved against the header's cwd", files)
	}
	edits := strings.Join(rolesOf(s, RoleEdit), "\n")
	if !strings.Contains(edits, "max_backoff_seconds = 30") {
		t.Errorf("edits = %q, want the replaced line", edits)
	}
	if strings.Contains(edits, "never held") {
		t.Errorf("edits = %q: the refused edit changed nothing", edits)
	}
	// The result's text is a JSON string; it is read as the text it wraps.
	out := strings.Join(rolesOf(s, RoleToolOutput), "\n")
	if !strings.Contains(out, "retries = 3\nmax_backoff_seconds") || strings.Contains(out, `{"text"`) {
		t.Errorf("tool output = %q, want the cell's text unwrapped", out)
	}
}

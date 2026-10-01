package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seedCursorStore writes a chat store the way Cursor CLI does: JSON message
// blobs, a protobuf root listing their digests in field 1, and meta naming the
// root as hex-encoded JSON.
func seedCursorStore(t *testing.T, db string, msgs []string) {
	t.Helper()
	var sql strings.Builder
	sql.WriteString("create table blobs (id text primary key, data blob); create table meta (key text primary key, value text);\n")
	var root []byte
	for _, m := range msgs {
		sum := sha256.Sum256([]byte(m))
		root = append(root, 0x0a, 32)
		root = append(root, sum[:]...)
		fmt.Fprintf(&sql, "insert into blobs values ('%x', X'%x');\n", sum, m)
	}
	// A field the walk must skip: Cursor's root carries more than the list.
	root = append(root, 0x12, 3, 'a', 'b', 'c')
	rootID := sha256.Sum256(root)
	fmt.Fprintf(&sql, "insert into blobs values ('%x', X'%x');\n", rootID, root)
	meta := hex.EncodeToString([]byte(fmt.Sprintf(`{"agentId":"a","latestRootBlobId":"%x"}`, rootID)))
	fmt.Fprintf(&sql, "insert into meta values ('0', '%s');\n", meta)
	if out, err := exec.Command("sqlite3", db, sql.String()).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// Cursor's transcript records the calls and none of their results; the chat
// store beside it has them. A failed command read as one that never failed,
// and nothing it printed was searchable (#4187).
func TestCursorToolResultsComeFromTheChatStore(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
	home := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", home)
	id := "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e8a"

	chat := filepath.Join(home, "chats", "wshash", id)
	if err := os.MkdirAll(chat, 0o755); err != nil {
		t.Fatal(err)
	}
	seedCursorStore(t, filepath.Join(chat, "store.db"), []string{
		`{"role":"user","content":[{"type":"text","text":"build it"}]}`,
		// A call the transcript does not carry: pairing by position would
		// hand its result to the next Shell.
		`{"role":"assistant","content":[{"type":"tool-call","toolCallId":"c0","toolName":"GetDynamicTools","args":{"namespace":"deja"}},{"type":"tool-call","toolCallId":"c1","toolName":"Shell","args":{"command":"go vet ./...","description":"vet"}},{"type":"tool-call","toolCallId":"c2","toolName":"Shell","args":{"command":"make build","description":"build"}}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"c0","toolName":"GetDynamicTools","result":{"description":"selfdescneedle indexes past sessions"}}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"c1","toolName":"Shell","result":"Exit code: 1\n\nCommand output:\n\n` + "```" + `\nstorevetneedle: unreachable code\n` + "```" + `\n\nCommand completed in 55 ms.\n\nShell state (cwd, env vars) persists for subsequent calls."}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"c2","toolName":"Shell","result":"Exit code: 0\n\nCommand output:\n\n` + "```" + `\nok\n` + "```" + `"}]}`,
	})

	tdir := filepath.Join(home, "projects", "Users-me-work", "agent-transcripts", id)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"role":"user","message":{"content":[{"type":"text","text":"build it"}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"GetDynamicTools","input":{"namespace":"deja"}},{"type":"tool_use","name":"Shell","input":{"command":"go vet ./...","description":"vet"}},{"type":"tool_use","name":"Shell","input":{"command":"make build","description":"build"}}]}}
`
	p := filepath.Join(tdir, id+".jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseCursorTranscript(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %#v", err, ss)
	}
	var cmds, outs []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			outs = append(outs, m.Text)
		}
	}
	if want := []string{"$ go vet ./...  → exit 1", "$ make build  → exit 0"}; strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
	if len(outs) != 2 || outs[0] != "storevetneedle: unreachable code" || outs[1] != "ok" {
		t.Fatalf("tool output = %q", outs)
	}

	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "selfdescneedle") {
			t.Fatalf("an MCP server's own description was indexed: %q", m.Text)
		}
	}

	// The switch that keeps tool output out of the index still holds.
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	ss, _ = ParseCursorTranscript(p)
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			t.Fatalf("tool output indexed with DEJA_INDEX_TOOL_OUTPUT=0: %q", m.Text)
		}
	}
}

// A transcript with no store beside it parses as it did.
func TestCursorTranscriptWithoutAStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", home)
	tdir := filepath.Join(home, "projects", "Users-me-work", "agent-transcripts", "solo")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(tdir, "solo.jsonl")
	if err := os.WriteFile(p, []byte(`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"make"}}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCursorTranscript(p)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 || ss[0].Messages[0].Text != "$ make" {
		t.Fatalf("parse: %v %#v", err, ss)
	}
}

func TestProtoChildIDsStopsOnABrokenBlob(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	b := append([]byte{0x0a, 32}, sum[:]...)
	b = append(b, 0x0a, 40, 1, 2) // length runs past the end
	if got := protoChildIDs(b); len(got) != 1 || got[0] != hex.EncodeToString(sum[:]) {
		t.Fatalf("children = %q", got)
	}
}

// The store is the chat's latest branch: a call it lost to a rewind must not
// take the result of a later identical call, and the later one keeps its own.
func TestCursorPairingOnlyMovesForward(t *testing.T) {
	key := func(cmd string) string { return cursorCallKey("Shell", map[string]any{"command": cmd}) }
	rs := &cursorToolResults{calls: []cursorToolResult{
		{key: key("make build"), text: "build-2"},
		{key: key("npm test"), text: "test-2"},
	}}
	args := func(cmd string) map[string]any { return map[string]any{"command": cmd} }
	// Transcript: npm test (rewound away), make build, npm test.
	if got, ok := rs.take("Shell", args("npm test")); !ok || got != "test-2" {
		t.Fatalf("first npm test = %q %v", got, ok)
	}
	if _, ok := rs.take("Shell", args("make build")); ok {
		t.Fatal("make build paired backwards, behind a call already paired")
	}
	if _, ok := rs.take("Shell", args("npm test")); ok {
		t.Fatal("the same store call paired twice")
	}
	var none *cursorToolResults
	if _, ok := none.take("Shell", args("x")); ok {
		t.Fatal("a chat with no store paired a call")
	}
}

func TestCursorShellOutputDropsTheFraming(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Exit code: 0\n\nCommand output:\n\n```\nhi\n\n```\n\nCommand completed in 420 ms.\n\nShell state (cwd, env vars) persists for subsequent calls.", "hi"},
		{"Exit code: 1\n\nCommand output:\n\n````\nprinted ```fence```\n````\n\nCommand completed in 3 ms.", "printed ```fence```"},
		{"Exit code: 2\n\nno such file", "no such file"},
		{"plain text", "plain text"},
	} {
		if got := cursorShellOutput(c.in); got != c.want {
			t.Errorf("cursorShellOutput(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

package sources

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Kimi Code's Edit and Write calls, read through the Kimi loader and the
// generic reader. Kimi's wire.jsonl carries no cwd, so paths are shown
// relative to the common directory of the changed files (here both sit in
// the same one).
func TestChangesReadsKimiEditAndWriteCalls(t *testing.T) {
	root := fixtureRoot(t, "kimi", "DEJA_KIMI_ROOT")
	p := filepath.Join(root, "sessions", "wd_demo_0123456789ab", "session_changes01", "agents", "main", "wire.jsonl")
	ss, err := ParseKimiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseKimiFile: %v, %d sessions", err, len(ss))
	}
	changes := Changes(ss[0])
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New)
	}
	if want := "pool.go|MaxConns: 10|MaxConns: 40 pool_test.go||package demo"; strings.Join(got, " ") != want {
		t.Fatalf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
}

// A harness with no edit reader of its own showed none of the code a session
// changed. Copilot CLI's edit and create calls, read with the generic reader:
// the edit and the new file come back relative to the session's directory,
// and a view of a file — a path with content, the shape a write also has —
// does not count as one.
func TestChangesReadsAnotherHarnessesEditCalls(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := copilotFixtureLines()[:4]
	lines = append(lines,
		`{"type":"tool.execution_start","data":{"toolCallId":"t1","toolName":"view","arguments":{"path":"/Users/x/coding/gateway/pool.go"}},"timestamp":"2026-07-20T05:13:59.000Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"t1","result":{"path":"/Users/x/coding/gateway/pool.go","content":"READ ONLY"}},"timestamp":"2026-07-20T05:13:59.100Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"t2","toolName":"edit","arguments":{"path":"/Users/x/coding/gateway/pool.go","old_str":"MaxConns: 10","new_str":"MaxConns: 40"}},"timestamp":"2026-07-20T05:13:59.200Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"t3","toolName":"create","arguments":{"path":"/Users/x/coding/gateway/pool_test.go","file_text":"package gateway"}},"timestamp":"2026-07-20T05:13:59.300Z"}`,
		// Outside any call: a prompt quoting an edit and a patch, and a read
		// result whose object happens to be named like a write.
		`{"type":"system.message","data":{"role":"system","content":"{\"path\":\"/Users/x/coding/gateway/doc.go\",\"old_str\":\"EXAMPLE\",\"new_str\":\"EXAMPLE2\"}"},"timestamp":"2026-07-20T05:13:59.400Z"}`,
		`{"type":"system.message","data":{"role":"system","content":"Edit with *** Begin Patch\n*** Add File: example.go\n+EXAMPLE\n*** End Patch"},"timestamp":"2026-07-20T05:13:59.500Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"t4","result":{"name":"create-user.ts","path":"/Users/x/coding/gateway/create-user.ts","content":"READ ONLY"}},"timestamp":"2026-07-20T05:13:59.600Z"}`,
		`{"type":"assistant.message","data":{"content":"raised the pool"},"timestamp":"2026-07-20T05:14:00.973Z"}`,
	)
	p := writeCopilotFixture(t, root, "7cf44517-55ca-435c-893a-3fde1973a44e", lines)
	changes := Changes(model.Session{Harness: "copilot", ID: "7cf44517-55ca-435c-893a-3fde1973a44e", Path: p})
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New)
	}
	if want := "pool.go|MaxConns: 10|MaxConns: 40 pool_test.go||package gateway"; strings.Join(got, " ") != want {
		t.Fatalf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
}

// Qwen Code writes tool calls as functionCall parts with name and args; its
// edit and write_file calls come back through the generic reader.
func TestChangesReadsQwenEditCalls(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	p := filepath.Join(root, "projects", "-workspace-registry-demo", "chats", "registry-qwen-edit.jsonl")
	copyFixture(t, "fixtures/registry/qwen/projects/-workspace-registry-demo/chats/registry-qwen-edit.jsonl", p)
	changes := Changes(model.Session{Harness: "qwen", ID: "registry-qwen-edit", Path: p})
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New)
	}
	if want := "pool.go|MaxConns: 10|MaxConns: 40 pool_test.go||package demo"; strings.Join(got, " ") != want {
		t.Fatalf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
}

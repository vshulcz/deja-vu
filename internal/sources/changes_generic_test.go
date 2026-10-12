package sources

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A harness with no edit reader of its own showed none of the code a session
// changed. Copilot CLI's edit and create calls, read with the generic reader:
// the edit and the new file come back relative to the session's directory,
// and a view of a file — a path with content, the shape a write also has —
// does not count as one.
func TestChangesReadsAnotherHarnessesEditCalls(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := append(copilotFixtureLines()[:4],
		`{"type":"tool.execution_start","data":{"toolCallId":"t1","toolName":"view","arguments":{"path":"/Users/x/coding/gateway/pool.go"}},"timestamp":"2026-07-20T05:13:59.000Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"t1","result":{"path":"/Users/x/coding/gateway/pool.go","content":"READ ONLY"}},"timestamp":"2026-07-20T05:13:59.100Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"t2","toolName":"edit","arguments":{"path":"/Users/x/coding/gateway/pool.go","old_str":"MaxConns: 10","new_str":"MaxConns: 40"}},"timestamp":"2026-07-20T05:13:59.200Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"t3","toolName":"create","arguments":{"path":"/Users/x/coding/gateway/pool_test.go","file_text":"package gateway"}},"timestamp":"2026-07-20T05:13:59.300Z"}`,
		`{"type":"assistant.message","data":{"content":"raised the pool"},"timestamp":"2026-07-20T05:14:00.973Z"}`,
	)
	p := writeCopilotFixture(t, root, "7cf44517-55ca-435c-893a-3fde1973a44e", lines)
	var got []string
	for _, c := range Changes(model.Session{Harness: "copilot", ID: "7cf44517-55ca-435c-893a-3fde1973a44e", Path: p}) {
		got = append(got, c.Path+"|"+c.Old+"|"+c.New)
	}
	if want := "pool.go|MaxConns: 10|MaxConns: 40 pool_test.go||package gateway"; strings.Join(got, " ") != want {
		t.Fatalf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
}

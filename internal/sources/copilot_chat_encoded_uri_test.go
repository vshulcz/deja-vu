package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// VS Code writes a workspace folder as a URI, and a folder whose name has a
// space in it arrives percent-encoded. Reading it raw named the project
// `my%20app`, which is a project nobody searches for and a project filter
// nothing matches — the shape a contributor hit on `C:\Users\me\my app`
// (#3498, #3505).
func TestCopilotChatDecodesAWorkspaceURI(t *testing.T) {
	for _, tc := range []struct {
		name, uri, want string
	}{
		{name: "a space", uri: "file:///tmp/my%20app", want: "tmp/my app"},
		{name: "parentheses and a dot", uri: "file:///tmp/my%20app%20(v2.1)", want: "tmp/my app (v2.1)"},
		{name: "non-ASCII", uri: "file:///tmp/%D0%BF%D1%80%D0%BE%D0%B5%D0%BA%D1%82", want: "tmp/проект"},
		{name: "nothing to decode", uri: "file:///tmp/plain", want: "tmp/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := copilotChatProjectFromURI(tc.uri); got != tc.want {
				t.Fatalf("project = %q, want %q", got, tc.want)
			}
		})
	}
}

// The same through the file the reader actually opens, so the decode is
// pinned where a session gets its project rather than only in the helper.
func TestACopilotChatSessionUnderAnEncodedFolderKeepsItsProject(t *testing.T) {
	hermeticSourcesEnv(t)
	ws := filepath.Join(t.TempDir(), "workspaceStorage", "a1b2c3d4e5f6")
	if err := os.MkdirAll(filepath.Join(ws, "chatSessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "workspace.json"),
		[]byte(`{"folder":"file:///tmp/my%20app%20(v2.1)"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(ws, "chatSessions", "3b7e1f1b-0000-4000-8000-000000000000.jsonl")
	body := `{"kind":0,"v":{"version":3,"sessionId":"3b7e1f1b-0000-4000-8000-000000000000","creationDate":1763727100000,"customTitle":"fix the retry loop","requests":[],"workingDirectory":"file:///tmp/my%20app%20(v2.1)"}}
{"kind":2,"k":["requests"],"v":[{"requestId":"request_1","timestamp":1763727104742,"message":{"text":"why does the retry loop spin"},"response":[{"value":"because the budget resets on every attempt"}]}]}
`
	if err := os.WriteFile(session, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(session)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	if ss[0].Project != "tmp/my app (v2.1)" {
		t.Fatalf("project = %q — the folder URI was not decoded", ss[0].Project)
	}
}

package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// VS Code keeps which files a chat edited beside the transcript, and deja read
// none of it: a `files` record appeared only when the conversation happened to
// type a path. Measured on this machine — 48 of these state files, their
// entries carrying 145 resources between them (#3381).
func TestACopilotChatNamesTheFilesItEdited(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "workspaceStorage", "abc123")
	chats := filepath.Join(ws, "chatSessions")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "3f2b9c11-0000-4000-8000-abcdefabcdef"
	transcript := `{"version":3,"sessionId":"` + id + `","creationDate":1767225600000,` +
		`"requests":[{"message":{"text":"make the retry budget configurable"},` +
		`"response":[{"value":"done"}]}]}`
	path := filepath.Join(chats, id+".json")
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	edits := filepath.Join(ws, "chatEditingSessions", id)
	if err := os.MkdirAll(edits, 0o755); err != nil {
		t.Fatal(err)
	}
	// The shape this machine's own files carry: workingSet empty, the entries
	// holding the resources.
	state := `{"version":2,"sessionId":"` + id + `","recentSnapshot":{"workingSet":[],` +
		`"entries":[{"resource":{"scheme":"file","path":"/work/app/retry.go"}},` +
		`{"resource":"file:///work/app/retry_test.go"}]}}`
	if err := os.WriteFile(filepath.Join(edits, "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseCopilotChatFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the one transcript", len(ss))
	}
	var files string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			files = m.Text
		}
	}
	for _, want := range []string{"/work/app/retry.go", "/work/app/retry_test.go"} {
		if !strings.Contains(files, want) {
			t.Errorf("the files record does not name %s: %q", want, files)
		}
	}
}

// And a chat with no such state file is what it was: no record invented.
func TestACopilotChatWithoutEditStateIsUnchanged(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "workspaceStorage", "abc123")
	chats := filepath.Join(ws, "chatSessions")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "aaaaaaaa-0000-4000-8000-aaaaaaaaaaaa"
	path := filepath.Join(chats, id+".json")
	transcript := `{"version":3,"sessionId":"` + id + `","creationDate":1767225600000,` +
		`"requests":[{"message":{"text":"why does the retry budget reset"},"response":[{"value":"it does not"}]}]}`
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			t.Errorf("a files record appeared with no edit state: %q", m.Text)
		}
	}
}

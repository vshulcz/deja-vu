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

const copilotChatEditedFilesTestID = "bbbbbbbb-0000-4000-8000-bbbbbbbbbbbb"

func copilotChatEditedFilesText(t *testing.T, state string) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "workspaceStorage", "abc123")
	chats := filepath.Join(ws, "chatSessions")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"version":3,"sessionId":"` + copilotChatEditedFilesTestID +
		`","creationDate":1767225600000,"requests":[{"message":{"text":"edit the files"},` +
		`"response":[{"value":"done"}]}]}`
	path := filepath.Join(chats, copilotChatEditedFilesTestID+".json")
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	edits := filepath.Join(ws, "chatEditingSessions", copilotChatEditedFilesTestID)
	if err := os.MkdirAll(edits, 0o755); err != nil {
		t.Fatal(err)
	}
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
	var records []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			records = append(records, m.Text)
		}
	}
	if len(records) == 0 {
		return ""
	}
	if len(records) != 1 {
		t.Fatalf("files records = %d, want one: %q", len(records), records)
	}
	return records[0]
}

// VS Code writes string resources with URI.toString(), which percent-encodes
// their paths, while UriComponents paths are already decoded.
func TestACopilotChatDecodesThePercentEncodedResource(t *testing.T) {
	state := `{"version":2,"sessionId":"` + copilotChatEditedFilesTestID +
		`","recentSnapshot":{"entries":[` +
		`{"resource":"file:///c%3A/Users/me/my%20app/main.go"},` +
		`{"resource":{"scheme":"file","path":"/c:/Users/me/my app/other.go"}}]}}`
	files := copilotChatEditedFilesText(t, state)

	for _, want := range []string{
		"/c:/Users/me/my app/main.go",
		"/c:/Users/me/my app/other.go",
	} {
		if !strings.Contains(files, want) {
			t.Errorf("the decoded files record does not name %s: %q", want, files)
		}
	}
	for _, escaped := range []string{"%3A", "%20"} {
		if strings.Contains(files, escaped) {
			t.Errorf("the files record kept %s instead of decoding it: %q", escaped, files)
		}
	}
}

// VS Code's older snapshot stored workingSet as ResourceMapDTO pairs, while
// its reader still took the edited files only from entries.
func TestACopilotChatReadsTheOlderWorkingSetShape(t *testing.T) {
	state := `{"version":1,"sessionId":"` + copilotChatEditedFilesTestID +
		`","recentSnapshot":{"requestId":"r1",` +
		`"workingSet":[["file:///work/app/a.go",{"state":0}]],` +
		`"entries":[{"resource":"file:///work/app/b.go"}]}}`
	files := copilotChatEditedFilesText(t, state)

	if !strings.Contains(files, "/work/app/b.go") {
		t.Errorf("the files record does not name the snapshot entry: %q", files)
	}
	if strings.Contains(files, "/work/app/a.go") {
		t.Errorf("the files record misattributes a working-set key as edited: %q", files)
	}
}

// The sidecar can carry an encoded newline inside one URI, while RoleFiles
// uses literal newlines to separate resources.
func TestACopilotChatDropsAResourceThatDecodesToANewline(t *testing.T) {
	state := `{"version":2,"sessionId":"` + copilotChatEditedFilesTestID +
		`","recentSnapshot":{"entries":[` +
		`{"resource":"file:///work/app/x%0Ay.go"},` +
		`{"resource":"file:///work/app/trailing.go%0A"},` +
		`{"resource":"file:///work/app/cr.go%0D"},` +
		`{"resource":"file:///work/app/ok.go"}]}}`
	files := copilotChatEditedFilesText(t, state)
	lines := strings.Split(files, "\n")

	if len(lines) != 1 || lines[0] != "/work/app/ok.go" {
		t.Fatalf("files lines = %q, want only /work/app/ok.go", lines)
	}
}

// URI.toString() keeps a file URI's server in the authority rather than in
// the decoded path.
func TestACopilotChatKeepsAUNCResourceAuthority(t *testing.T) {
	state := `{"version":2,"sessionId":"` + copilotChatEditedFilesTestID +
		`","recentSnapshot":{"entries":[` +
		`{"resource":"file://server/share/proj/f.go"}]}}`
	files := copilotChatEditedFilesText(t, state)

	if files != "//server/share/proj/f.go" {
		t.Fatalf("files = %q, want //server/share/proj/f.go", files)
	}
}

package sources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func copilotChatHostTextSession(t *testing.T, body, ext string) model.Session {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session"+ext)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseCopilotChatFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1 (%#v)", len(sessions), sessions)
	}
	return sessions[0]
}

func copilotChatRoleTexts(messages []model.Message, role string) []string {
	var texts []string
	for _, message := range messages {
		if message.Role == role {
			texts = append(texts, message.Text)
		}
	}
	return texts
}

func TestCopilotChatDoesNotCallVSCodeRequestTextThePersons(t *testing.T) {
	tests := []struct {
		name     string
		fields   string
		wantUser bool
	}{
		{
			name:   "system initiated",
			fields: `,"isSystemInitiated":true`,
		},
		{
			name:   "confirmation",
			fields: `,"confirmation":"Accept"`,
		},
		{
			name:     "ordinary request",
			wantUser: true,
		},
		{
			name:     "false and empty",
			fields:   `,"isSystemInitiated":false,"confirmation":""`,
			wantUser: true,
		},
		{
			name:     "null markers",
			fields:   `,"isSystemInitiated":null,"confirmation":null`,
			wantUser: true,
		},
		{
			name:     "wrong marker types",
			fields:   `,"isSystemInitiated":"true","confirmation":true`,
			wantUser: true,
		},
		{
			name:     "whitespace confirmation",
			fields:   `,"confirmation":"  "`,
			wantUser: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"version":3,"sessionId":"flat","creationDate":1763727100000,"requests":[{` +
				`"timestamp":1763727104742` + tc.fields + `,` +
				`"message":{"text":"request text"},` +
				`"response":[{"value":"assistant reply"}],` +
				`"responseTimestamp":1763727400000}]}`

			session := copilotChatHostTextSession(t, body, ".json")
			messages := session.Messages
			users := copilotChatRoleTexts(messages, "user")
			if tc.wantUser {
				if len(users) != 1 || users[0] != "request text" {
					t.Fatalf("users = %#v, want the ordinary request", users)
				}
			} else if len(users) != 0 {
				t.Fatalf("users = %#v, want none", users)
			}
			assistants := copilotChatRoleTexts(messages, "assistant")
			if len(assistants) != 1 || assistants[0] != "assistant reply" {
				t.Fatalf("assistants = %#v, want the response", assistants)
			}
			if got := session.Started.UnixMilli(); got != 1763727100000 {
				t.Fatalf("started = %d, want 1763727100000", got)
			}
			if got := session.Updated.UnixMilli(); got != 1763727400000 {
				t.Fatalf("updated = %d, want 1763727400000", got)
			}
		})
	}
}

func TestCopilotChatReplaysAHostMarkerBeforeIndexing(t *testing.T) {
	body := `{"kind":0,"v":{"version":3,"sessionId":"replayed","creationDate":1763727100000,` +
		`"requests":[{"timestamp":1763727104742,"message":{"text":"host notification"},` +
		`"response":[{"value":"work completed"}],"responseTimestamp":1763727400000}]}}` + "\n" +
		`{"kind":1,"k":["requests",0,"isSystemInitiated"],"v":true}` + "\n"

	messages := copilotChatHostTextSession(t, body, ".jsonl").Messages
	if users := copilotChatRoleTexts(messages, "user"); len(users) != 0 {
		t.Fatalf("users = %#v, want none after replayed marker", users)
	}
	assistants := copilotChatRoleTexts(messages, "assistant")
	if len(assistants) != 1 || assistants[0] != "work completed" {
		t.Fatalf("assistants = %#v, want the response", assistants)
	}
}

func TestCopilotChatHostRequestKeepsCommandWork(t *testing.T) {
	t.Setenv("DEJA_INDEX_COMMANDS", "1")
	body := `{"version":3,"sessionId":"command","creationDate":1763727100000,"requests":[{` +
		`"timestamp":1763727104742,"isSystemInitiated":true,` +
		`"message":{"text":"terminal completion notification"},` +
		`"response":[{"kind":"toolInvocationSerialized","toolId":"runInTerminal",` +
		`"toolSpecificData":{"kind":"terminal","commandLine":{"original":"go test ./..."}}}],` +
		`"responseTimestamp":1763727400000}]}`

	messages := copilotChatHostTextSession(t, body, ".json").Messages
	if users := copilotChatRoleTexts(messages, "user"); len(users) != 0 {
		t.Fatalf("users = %#v, want none", users)
	}
	commands := copilotChatRoleTexts(messages, RoleCommand)
	if len(commands) != 1 || commands[0] != "$ go test ./..." {
		t.Fatalf("commands = %#v, want the terminal work record", commands)
	}
}

// A confirmation can arrive the same way, so the guard has to see it after the
// log is replayed rather than only on the entry the log opened with.
func TestCopilotChatReplaysAConfirmationBeforeIndexing(t *testing.T) {
	body := `{"kind":0,"v":{"version":3,"sessionId":"confirmed","creationDate":1763727100000,` +
		`"requests":[{"timestamp":1763727104742,"message":{"text":"Accept: \"Run go test?\""},` +
		`"response":[{"value":"ran it"}],"responseTimestamp":1763727400000}]}}` + "\n" +
		`{"kind":1,"k":["requests",0,"confirmation"],"v":"Accept"}` + "\n"

	messages := copilotChatHostTextSession(t, body, ".jsonl").Messages
	if users := copilotChatRoleTexts(messages, "user"); len(users) != 0 {
		t.Fatalf("users = %#v, want none after the replayed confirmation", users)
	}
	assistants := copilotChatRoleTexts(messages, "assistant")
	if len(assistants) != 1 || assistants[0] != "ran it" {
		t.Fatalf("assistants = %#v, want the response", assistants)
	}
}

// Dropping the text does not drop the turn. A background completion is when the
// session was last active, so it still has to move Updated: the second request
// here carries no response, so the only thing that can advance the session is
// the request itself.
func TestCopilotChatHostRequestStillTimesTheSession(t *testing.T) {
	body := `{"version":3,"sessionId":"timing","creationDate":1763727100000,"requests":[` +
		`{"timestamp":1763727104742,"message":{"text":"typed question"},` +
		`"response":[{"value":"answer"}],"responseTimestamp":1763727200000},` +
		`{"timestamp":1763727900000,"isSystemInitiated":true,` +
		`"message":{"text":"terminal finished"}}]}`

	session := copilotChatHostTextSession(t, body, ".json")
	users := copilotChatRoleTexts(session.Messages, "user")
	if len(users) != 1 || users[0] != "typed question" {
		t.Fatalf("users = %#v, want only the typed one", users)
	}
	if got := session.Updated.UnixMilli(); got != 1763727900000 {
		t.Fatalf("updated = %d, want the host turn at 1763727900000", got)
	}
}

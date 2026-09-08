package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Approving a plan writes a USER_INPUT step whose <USER_REQUEST> is empty: the
// IDE's own comment about the document, with the person contributing nothing.
// deja indexed the comment as their words, opening tag and absolute path
// included — two of the 67 real user records on this machine (#3326).
func TestAntigravitySkipsAPlanApprovalWithNoRequestInIt(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "brain", "f017fad5-095f-47ee-abf7-c13b7ce6844a", ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nwhy does the retry loop drop the last attempt\n</USER_REQUEST>"}`,
		`{"step_index":13,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-08T14:45:28Z","content":"Comments on artifact URI: file:///w/api/implementation_plan.md\n\nThe user has approved this document.\n\n\n<USER_REQUEST>\n\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-07-08T17:45:28+03:00.\n</ADDITIONAL_METADATA>"}`,
		`{"step_index":14,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-08T14:47:00Z","content":"Comments on artifact URI: file:///w/api/implementation_plan.md\n\n<USER_REQUEST>\ncap the retries at three\n</USER_REQUEST>"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ParseAntigravityFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("sessions = %d, want 1", len(got))
	}
	var users []string
	for _, m := range got[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 2 {
		t.Fatalf("user turns = %d, want 2 — the approval is the IDE's own record: %q", len(users), users)
	}
	for _, u := range users {
		if strings.Contains(u, "has approved this document") || strings.Contains(u, "USER_REQUEST") ||
			strings.Contains(u, "artifact URI") {
			t.Errorf("a user turn carries the IDE's own text: %q", u)
		}
	}
	if users[1] != "cap the retries at three" {
		t.Errorf("second turn = %q, want the words inside the request tag", users[1])
	}
}

// Whatever the person wrote around the tag stays: a line above it, and a turn
// that carries two request blocks with words between them (review of #3326).
func TestAntigravityKeepsWhatIsWrittenAroundTheRequestTag(t *testing.T) {
	cases := []struct{ in, want string }{
		{"also note: <USER_REQUEST>\nplease fix the bug\n</USER_REQUEST>", "also note:\nplease fix the bug"},
		{"<USER_REQUEST>first</USER_REQUEST> and also <USER_REQUEST>second</USER_REQUEST>", "first\nand also\nsecond"},
		{"Comments on artifact URI: file:///w/api/implementation_plan.md\n\nThe user has approved this document.\n\n\n<USER_REQUEST>\n\n</USER_REQUEST>", ""},
	}
	for _, c := range cases {
		if got := cleanAntigravityUserContent(c.in); got != c.want {
			t.Errorf("cleanAntigravityUserContent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

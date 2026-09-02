package sources

import (
	"os"
	"testing"
)

// The legacy `.jsonl` store holds the same content parts the database does —
// goose changed where a session lives, not what a message is made of. Both
// readers go through gooseParts, and this is the half a real store on this
// machine could not cover: goose has written to sqlite since long before 1.48.
const gooseJSONLWithTools = `{"description":"build","id":"20250724_9","created_at":"2026-07-24T10:00:00Z","updated_at":"2026-07-24T10:00:09Z","working_dir":"/workspace/api","message_count":4}
{"id":"m1","role":"user","created":1784278801,"content":[{"type":"text","text":"the build keeps failing, run it"}]}
{"id":"m2","role":"assistant","created":1784278802,"content":[{"type":"text","text":"Running the build."},{"type":"toolRequest","id":"t1","toolCall":{"status":"success","value":{"name":"Bash","arguments":{"command":"go build ./... && echo built"}}}}]}
{"id":"m3","role":"user","created":1784278803,"content":[{"type":"toolResponse","id":"t1","toolResult":{"status":"success","value":{"resultType":"complete","content":[{"type":"text","text":"undefined: snorblefunc in vendor/blarg/api.go"}],"isError":false}}}]}
{"id":"m4","role":"user","created":1784278804,"content":[{"type":"text","text":"<turn-context>\n<working-directory>/workspace/api</working-directory>\n</turn-context>"}]}
`

func TestTheLegacyGooseFileKeepsCommandsAndOutput(t *testing.T) {
	_, jsonl := gooseFixture(t)
	if err := os.WriteFile(jsonl, []byte(gooseJSONLWithTools), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGooseFile(jsonl)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	var roles []string
	byRole := map[string]string{}
	for _, m := range ss[0].Messages {
		roles = append(roles, m.Role)
		byRole[m.Role] = m.Text
	}
	if byRole[RoleCommand] != "$ go build ./... && echo built" {
		t.Errorf("command = %q (roles %v)", byRole[RoleCommand], roles)
	}
	if byRole[RoleToolOutput] != "undefined: snorblefunc in vendor/blarg/api.go" {
		t.Errorf("tool output = %q (roles %v)", byRole[RoleToolOutput], roles)
	}
	// The turn envelope was the whole of the last message, so nothing of it
	// should have been filed as something the person said.
	for _, m := range ss[0].Messages {
		if m.Role == "user" && len(m.Text) > 0 && m.Text[0] == '<' {
			t.Errorf("the turn envelope was indexed as speech: %q", m.Text)
		}
	}
}

// Resuming past the header must not change what the tail is made of: the
// offset path reads the same parts as a whole read.
func TestAResumedLegacyGooseReadKeepsTheSameParts(t *testing.T) {
	_, jsonl := gooseFixture(t)
	if err := os.WriteFile(jsonl, []byte(gooseJSONLWithTools), 0o644); err != nil {
		t.Fatal(err)
	}
	whole, err := ParseGooseFile(jsonl)
	if err != nil || len(whole) != 1 {
		t.Fatalf("whole read: %v %#v", err, whole)
	}
	// Past the header and the first two messages.
	lines := 0
	offset := 0
	for i, c := range gooseJSONLWithTools {
		if c == '\n' {
			lines++
			if lines == 3 {
				offset = i + 1
				break
			}
		}
	}
	tail, err := ParseGooseFileFromOffset(jsonl, int64(offset))
	if err != nil || len(tail) != 1 {
		t.Fatalf("resumed read: %v %#v", err, tail)
	}
	if tail[0].ID != whole[0].ID || tail[0].Project != whole[0].Project {
		t.Errorf("resumed identity %q/%q, whole %q/%q",
			tail[0].ID, tail[0].Project, whole[0].ID, whole[0].Project)
	}
	found := false
	for _, m := range tail[0].Messages {
		if m.Role == RoleToolOutput {
			found = true
		}
	}
	if !found {
		t.Errorf("the resumed tail lost the tool output: %#v", tail[0].Messages)
	}
}

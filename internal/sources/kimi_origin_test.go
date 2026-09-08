package sources

import (
	"os"
	"testing"
)

// Kimi writes every message it appends under role user and says who wrote it
// in origin.kind: the person, an injected reminder, a hook's wrapped stdout, a
// background task. Only the person's are user turns (#3199).
func TestParseKimiKeepsOnlyThePersonsLinesAsUser(t *testing.T) {
	_, wire := kimiFixture(t)
	body := `{"type":"metadata","protocol_version":"1.4","created_at":1782295200000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"why does glimwrax drop the trailing newline"}],"origin":{"kind":"user"}},"time":1782295201000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"<hook_result hook_event=\"UserPromptSubmit\">\ndeja-vu — you have been here: zorbflax retry fix\n</hook_result>"}],"origin":{"kind":"hook_result","event":"UserPromptSubmit"}},"time":1782295201200}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nAuto permission mode is active.\n</system-reminder>"}],"origin":{"kind":"injection","variant":"permission_mode"}},"time":1782295201300}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"quuxnotify: background task 7 finished"}],"origin":{"kind":"background_task"}},"time":1782295201400}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"older protocol, no origin"}]},"time":1782295201500}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"/review the tokenizer"}],"origin":{"kind":"skill_activation","trigger":"user-slash"}},"time":1782295201600}
{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"text","text":"the tokenizer trims it"}]},"time":1782295202000}
`
	if err := os.WriteFile(wire, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 3 || users[0] != "why does glimwrax drop the trailing newline" || users[1] != "older protocol, no origin" || users[2] != "/review the tokenizer" {
		t.Fatalf("user lines = %q", users)
	}
}

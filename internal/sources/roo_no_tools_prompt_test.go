package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// When the model answers without a tool call, Roo sends an automated user turn
// (formatResponse.noToolsUsed). Roo's own view shows it as an error row; deja
// indexed it as the person's words, 36 of 37 user turns on one live task
// (#4421). Text from the Roo CLI 0.1.17 bundle.
func TestRooNoToolsRetryPromptIsNotTheUsersWords(t *testing.T) {
	const prompt = "[ERROR] You did not use a tool in your previous response! Please retry with a tool use.\n\n" +
		"# Reminder: Instructions for Tool Use\n\nTools are invoked using the platform's native tool calling mechanism.\n\n" +
		"# Next Steps\n\nIf you have completed the user's task, use the attempt_completion tool.\n" +
		"If you require additional information from the user, use the ask_followup_question tool.\n" +
		"Otherwise, if you have not completed the task and do not need additional information, then proceed with the next step of the task.\n" +
		"(This is an automated message, so do not respond to it conversationally.)"
	const env = "<environment_details>\n# VSCode Visible Files\nretry.cfg\n</environment_details>"
	const mine = "why does deja show \"[ERROR] You did not use a tool in your previous response!\" as mine"
	turn := func(role string, texts ...string) map[string]any {
		var blocks []any
		for _, t := range texts {
			blocks = append(blocks, map[string]any{"type": "text", "text": t})
		}
		return map[string]any{"role": role, "content": blocks}
	}
	body, err := json.Marshal([]any{
		turn("user", "<user_message>\nSet retries to 5 in retry.cfg.\n</user_message>", env),
		turn("assistant", "Done, retries is 5."),
		turn("user", prompt, env),
		turn("assistant", "retry.cfg updated."),
		turn("user", prompt),
		turn("user", mine),
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "tasks", "1788845325721")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"kilocode", ParseKiloTask},
		{"cline legacy", parseClineLegacyTask},
	} {
		ss, err := reader.parse(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		var users []string
		for _, m := range ss[0].Messages {
			if m.Role == "user" {
				users = append(users, m.Text)
			}
		}
		// The control: a person quoting the prompt inside a sentence keeps it.
		want := []string{"Set retries to 5 in retry.cfg.", mine}
		if strings.Join(users, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s: user turns = %q, want %q", reader.name, users, want)
		}
	}
}

package sources

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The shapes below are goose 1.48.0's own, read off a real store: a transcript
// was imported with `goose session import` and the row read back, rather than
// guessed from its source.
const (
	gooseToolRequestJSON = `[{"type":"text","text":"Running the build now."},` +
		`{"type":"toolRequest","id":"t1","toolCall":{"status":"success","value":` +
		`{"name":"Bash","arguments":{"command":"go build ./... && echo built"}}}}]`
	gooseToolResponseJSON = `[{"type":"toolResponse","id":"t1","toolResult":{"status":"success","value":` +
		`{"resultType":"complete","content":[{"type":"text","text":"undefined: snorblefunc in vendor/blarg/api.go\nexit status 2"}],` +
		`"isError":false}}}]`
)

// goose stores the command it ran and what the command printed in the same
// column as speech, and only `text` parts were read — so a goose store
// contributed no commands and no tool output at all. `how` had nothing to
// answer from, and friction and the fix pairs never saw the error a build
// printed.
func TestGooseKeepsTheCommandAndWhatItPrinted(t *testing.T) {
	speech, toolOut, commands, _ := gooseParts(gooseToolRequestJSON)
	if speech != "Running the build now." {
		t.Errorf("speech = %q", speech)
	}
	if len(commands) != 1 || commands[0] != "$ go build ./... && echo built" {
		t.Errorf("commands = %q", commands)
	}
	if toolOut != "" {
		t.Errorf("a request is not output: %q", toolOut)
	}

	speech, toolOut, commands, _ = gooseParts(gooseToolResponseJSON)
	if !strings.Contains(toolOut, "undefined: snorblefunc") || !strings.Contains(toolOut, "exit status 2") {
		t.Errorf("tool output = %q", toolOut)
	}
	if speech != "" || len(commands) != 0 {
		t.Errorf("a response is not speech or a command: %q %q", speech, commands)
	}
}

// goose files a tool result under the user's own role, the way Claude Code
// does. Indexed as speech it reads as something the person typed, and friction
// counts it as a person's words rather than a printout.
func TestAGooseToolResultIsNotFiledAsSpeech(t *testing.T) {
	s := gooseSessionFrom(t, "user", gooseToolResponseJSON)
	if len(s.Messages) != 1 {
		t.Fatalf("want one message, got %d: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != RoleToolOutput {
		t.Errorf("role = %q, want %q", s.Messages[0].Role, RoleToolOutput)
	}
}

// The command is its own record, so `how` and the fix pairs can find it, and
// it comes after the speech of the turn that ran it.
func TestAGooseCommandIsItsOwnRecord(t *testing.T) {
	s := gooseSessionFrom(t, "assistant", gooseToolRequestJSON)
	if len(s.Messages) != 2 {
		t.Fatalf("want speech and a command, got %#v", s.Messages)
	}
	if s.Messages[0].Role != "assistant" || s.Messages[1].Role != RoleCommand {
		t.Errorf("roles = %q, %q", s.Messages[0].Role, s.Messages[1].Role)
	}
}

// An editor call names the file it is about to change, which is what blame
// reads. Both spellings, because an extension's tool may take either.
func TestAGooseEditorCallNamesItsFile(t *testing.T) {
	for _, key := range []string{"path", "file_path"} {
		raw := `[{"type":"toolRequest","id":"t2","toolCall":{"status":"success","value":` +
			`{"name":"developer__text_editor","arguments":{"` + key + `":"/w/app/queue.go","command":"str_replace"}}}}]`
		_, _, _, paths := gooseParts(raw)
		if len(paths) != 1 || paths[0] != "/w/app/queue.go" {
			t.Errorf("%s: paths = %q", key, paths)
		}
	}
}

// The error side of a result is the half friction and the fix pairs are built
// on, so it cannot be dropped for not being a success.
func TestAFailedGooseToolStillCarriesWhatWentWrong(t *testing.T) {
	raw := `[{"type":"toolResponse","id":"t3","toolResult":{"status":"error",` +
		`"error":{"code":-32603,"message":"command not found: shellcheck"}}}]`
	_, toolOut, _, _ := gooseParts(raw)
	if !strings.Contains(toolOut, "command not found: shellcheck") {
		t.Errorf("tool output = %q", toolOut)
	}
}

// goose writes the turn envelope — clock, working directory, standing tasks —
// as a message of the user's own role, so every turn on a real store added one
// and deja indexed it as something the person said.
func TestTheGooseTurnEnvelopeIsNotIndexedAsSpeech(t *testing.T) {
	raw := `[{"type":"text","text":"<turn-context>\n<current-time>2026-09-02 01:07:00 +03:00</current-time>\n<working-directory>/private/tmp</working-directory>\n</turn-context>"}]`
	speech, _, _, _ := gooseParts(raw)
	if speech != "" {
		t.Errorf("the envelope was kept as speech: %q", speech)
	}

	// Stripped, not dropped whole: a turn that carries the envelope and real
	// words has to keep the words.
	both := `[{"type":"text","text":"<turn-context>\n<current-time>x</current-time>\n</turn-context>\nraise the pool to 40"}]`
	speech, _, _, _ = gooseParts(both)
	if speech != "raise the pool to 40" {
		t.Errorf("speech = %q, want the words without the envelope", speech)
	}
}

// A row deja can make nothing of stays out rather than arriving empty.
func TestAGooseRowWithNothingToIndexIsSkipped(t *testing.T) {
	for _, raw := range []string{
		`[{"type":"thinking","thinking":"..."}]`,
		`[]`,
		`[{"type":"text","text":"   "}]`,
	} {
		speech, toolOut, commands, paths := gooseParts(raw)
		if speech != "" || toolOut != "" || len(commands) != 0 || len(paths) != 0 {
			t.Errorf("%s produced %q %q %q %q", raw, speech, toolOut, commands, paths)
		}
	}
}

// gooseSessionFrom runs one row through the same appender both readers use.
func gooseSessionFrom(t *testing.T, role, raw string) model.Session {
	t.Helper()
	var s model.Session
	speech, toolOut, commands, paths := gooseParts(raw)
	appendGooseParts(&s, role, time.Unix(0, 0), speech, toolOut, commands, paths)
	return s
}

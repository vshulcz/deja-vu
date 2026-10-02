package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func parseQwenRows(t *testing.T, rows ...string) model.Session {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	dir := filepath.Join(root, "projects", "-w-app", "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseQwenFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	return ss[0]
}

// Qwen Code 0.20 calls its edit tool `edit`; `replace` is the old name it
// still maps to it. Read under the old name only, an edit left no files,
// wrote or edit record, and write_file no wrote record (#4254).
func TestQwenEditAndWriteFileLeaveEditRecords(t *testing.T) {
	s := parseQwenRows(t,
		`{"sessionId":"s1","timestamp":"2026-10-01T15:21:00.629Z","type":"assistant","cwd":"/w/app","message":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"edit","args":{"file_path":"/w/app/retry.go","old_string":"func retry() int { return 3 }","new_string":"func retry() int { return 5 }"}}}]}}`,
		`{"sessionId":"s1","timestamp":"2026-10-01T15:21:02.000Z","type":"assistant","cwd":"/w/app","message":{"role":"model","parts":[{"functionCall":{"id":"call_2","name":"write_file","args":{"file_path":"/w/app/backoff.go","content":"package main\n\nconst backoffSeconds = 2 // wait between upstream retries\n"}}}]}}`,
	)
	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := strings.Join(byRole[RoleFiles], "|"); got != "/w/app/retry.go|/w/app/backoff.go" {
		t.Errorf("files = %q", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 || got[0] != "/w/app/retry.go\nfunc retry() int { return 3 }" {
		t.Errorf("edit = %q, want the replaced span of the edit", got)
	}
	wrote := byRole[RoleWrote]
	if len(wrote) != 2 || !strings.HasPrefix(wrote[0], "/w/app/retry.go\n") || !strings.HasPrefix(wrote[1], "/w/app/backoff.go\n") {
		t.Errorf("wrote = %q, want one record for the edit and one for the new file", wrote)
	}
}

// A failed command's status is in the footer of its result, which Qwen writes
// as its own tool_result record after the call (#4255).
func TestQwenFailedCommandCarriesItsExitCode(t *testing.T) {
	s := parseQwenRows(t,
		`{"sessionId":"s1","timestamp":"2026-10-01T15:18:00.000Z","type":"assistant","cwd":"/w/app","message":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"run_shell_command","args":{"command":"git log --oneline -1 -- retry.go"}}},{"functionCall":{"id":"call_2","name":"run_shell_command","args":{"command":"git status --short"}}}]}}`,
		`{"sessionId":"s1","timestamp":"2026-10-01T15:18:01.000Z","type":"tool_result","cwd":"/w/app","message":{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"run_shell_command","response":{"error":"Command: git log --oneline -1 -- retry.go\nDirectory: (root)\nOutput: fatal: your current branch 'master' does not have any commits yet\nError: (none)\nExit Code: 128\nSignal: 0\nProcess Group PGID: 1157"}}}]},"toolCallResult":{"callId":"call_1","status":"error"}}`,
		`{"sessionId":"s1","timestamp":"2026-10-01T15:18:02.000Z","type":"tool_result","cwd":"/w/app","message":{"role":"user","parts":[{"functionResponse":{"id":"call_2","name":"run_shell_command","response":{"output":"Command: git status --short\nDirectory: (root)\nOutput: (empty)\nError: (none)\nExit Code: 0\nSignal: 0\nProcess Group PGID: 1158"}}}]},"toolCallResult":{"callId":"call_2","status":"success"}}`,
		`{"sessionId":"s1","timestamp":"2026-10-01T15:18:03.000Z","type":"tool_result","cwd":"/w/app","message":{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"run_shell_command","response":{"error":"Exit Code: 2"}}}]}}`,
	)
	var cmds []string
	for _, m := range s.Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	want := []string{"$ git log --oneline -1 -- retry.go  → exit 128", "$ git status --short"}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
}

// Qwen Code names a project folder the way Claude Code does, every character
// outside [A-Za-z0-9] as "-", so a directory named in Cyrillic came back as
// its last ASCII piece and resume could not find it. Every record carries the
// real directory as cwd (#4258).
func TestAQwenSessionInANonASCIIDirectoryKeepsItsProject(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	work := filepath.Join(root, "w", "проект q")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "qwen", "projects", claudeEncodePath(work), "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ee561e79-e05c-4f4f-a8f2-38f08ed7c87b.jsonl")
	line := `{"sessionId":"ee561e79-e05c-4f4f-a8f2-38f08ed7c87b","timestamp":"2026-10-01T15:30:00.000Z","type":"user","cwd":` + jsonString(work) + `,"message":{"role":"user","parts":[{"text":"кэш живёт в «ёлке»"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseQwenFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	if want := "w/проект q"; ss[0].Project != want {
		t.Errorf("project = %q, want %q", ss[0].Project, want)
	}
	if got, recorded := QwenSessionDir(path); got != work || !recorded {
		t.Errorf("session dir = %q, want %q", got, work)
	}

	// A cwd the folder was not named for is not taken for it.
	other := filepath.Join(root, "qwen", "projects", "-w-app", "chats")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(other, "s2.jsonl")
	if err := os.WriteFile(moved, []byte(strings.Replace(line, "ee561e79-e05c-4f4f-a8f2-38f08ed7c87b", "s2", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if ss, _ := ParseQwenFile(moved); len(ss) != 1 || ss[0].Project != "w/app" {
		t.Errorf("a cwd from another folder named the project: %#v", ss)
	}
}

// Qwen 0.20 writes notes after the footer, separated by a blank line: a hint
// once a foreground command has run for half its timeout (60 s by default),
// and an attribution warning on git commit. The status is read from the
// footer itself, not from the last line of the result (#4255).
func TestQwenExitCodeIsReadPastTheNotesAfterTheFooter(t *testing.T) {
	footer := "Command: make test\nDirectory: (root)\nOutput: FAIL\nError: (none)\nExit Code: 2\nSignal: (none)\nProcess Group PGID: 4242"
	cases := map[string]int{
		footer: 2,
		footer + "\n\nNote: this foreground command ran for 75s. Next time you run a similar long-running process (build watchers, dev servers, soak tests, polling loops), pass `is_background: true`.": 2,
		footer + "\n\nAI attribution note skipped: could not analyze the commit diff (shallow clone, missing reflog for --amend, or partial `git diff` failure). Co-authored-by trailer is unaffected.":  2,
		strings.ReplaceAll(footer, "\n", "\r\n") + "\r\n\r\nNote: this foreground command ran for 75s.":                                                                                                  2,
		"FAIL\nok": 0,
		"Command: true\nDirectory: (root)\nOutput: (empty)\nError: (none)\nExit Code: 0\nSignal: (none)\nProcess Group PGID: 7": 0,
		// A line the command printed is not the footer, notes or not.
		"Command: x\nOutput: Exit Code: 3\nExit Code: 3\nok\n\nNote: this foreground command ran for 75s.": 0,
	}
	for out, want := range cases {
		if got := geminiExitCode(out); got != want {
			t.Errorf("geminiExitCode(%q) = %d, want %d", out, got, want)
		}
	}
}

// A result with no footer of its own — a timeout or a cancel, Qwen's or
// Gemini's — is the command's output, and a footer the command printed in it
// is not the status. Neither is a code that sits out of the footer's order or
// does not parse; the note Qwen adds when it cannot save a truncated output
// does not hide the status.
func TestShellExitCodeIsOnlyTheResultsOwnFooter(t *testing.T) {
	cases := map[string]int{
		"Command timed out after 120000ms before it could complete. Below is the output before it timed out:\nstep\nExit Code: 7\nSignal: (none)\nProcess Group PGID: 1\n\nmore":                                                            0,
		"Command was cancelled by user before it could complete. Below is the output before it was cancelled:\nCommand: c\nDirectory: (root)\nOutput: x\nError: (none)\nExit Code: 2\nSignal: (none)\nProcess Group PGID: 99\n\nwaiting...": 0,
		"<untrusted_context>\nCommand was cancelled by user before it could complete. Below is the output before it was cancelled:\nOutput: x\nExit Code: 7\nProcess Group PGID: 1\n\ntail\n</untrusted_context>":                           0,
		"Command: make\nDirectory: (root)\nOutput: x\nError: (none)\nExit Code: 4\nSignal: (none)\nProcess Group PGID: 5\n[Note: Could not save full output to file]":                                                                       4,
		"Command: make\nDirectory: (root)\nOutput: x\nError: (none)\nExit Code: 99999999999999999999\nSignal: (none)\nProcess Group PGID: 5":                                                                                                0,
		"Output: x\nExit Code: 3\nExit Code: 1\nProcess Group PGID: 9":              1,
		"Output: x\nExit Code: 3\nSignal: 9\nSignal: (none)\nProcess Group PGID: 9": 0,
		// Text after a blank line is skipped only on Qwen's own block.
		"Command: make\nDirectory: (root)\nOutput: x\nError: (none)\nExit Code: 6\nSignal: (none)\nProcess Group PGID: 5\n\nSome later note.": 6,
		"Output: x\nExit Code: 6\nProcess Group PGID: 5\n\nSome later output.":                                                                0,
		"Output: x\nExit Code: 6\nProcess Group PGID: 5\n\nAI attribution note skipped: payload exceeded the 30 KB size cap.":                 0,
	}
	for out, want := range cases {
		if got := geminiExitCode(out); got != want {
			t.Errorf("geminiExitCode(%q) = %d, want %d", out, got, want)
		}
	}
}

// deja's own PostToolUseFailure context lands under Qwen's notes, after a
// blank line, in as many paragraphs as it has. On Qwen's block — and on a
// truncated one, which keeps the tail — every paragraph under the footer is
// skipped; anywhere else the footer is the tail or there is none (#4255).
func TestQwenExitCodeSurvivesHookContextUnderTheNotes(t *testing.T) {
	q := func(out, code string) string {
		return "Command: make test\nDirectory: (root)\nOutput: " + out + "\nError: (none)\nExit Code: " + code + "\nSignal: (none)\nProcess Group PGID: 4242"
	}
	recall := "\n\n<deja-recall>\nRecalled history from prior sessions.\ndeja: this error came up before\n</deja-recall>"
	cases := map[string]int{
		q("x", "3") + "\n\nNote: this foreground command ran for 75s. Next time pass `is_background: true`." + recall:                        3,
		q("x", "3") + "\n\nAI attribution note skipped: payload exceeded the 30 KB size cap. Co-authored-by trailer is unaffected." + recall: 3,
		q("x", "3") + "\n\nctx one\n\nctx two": 3,
		"Tool output was too large and has been truncated.\nThe full output has been saved to: /tmp/x.output\n\nTruncated part of the output:\n" + q("first", "1")[:40] + "\n\n---\n... [CONTENT TRUNCATED] ...\n---\n\nlast\nError: (none)\nExit Code: 2\nSignal: (none)\nProcess Group PGID: 7" + recall: 2,
		"Command timed out after 120000ms before it could complete. Below is the output before it timed out:\nstep\nExit Code: 7\nSignal: (none)\nProcess Group PGID: 1\n\nNote: this foreground command ran for 75s.":                                                                                     0,
		"Command was cancelled by user before it could complete. Below is the output before it was cancelled:\nstep\nExit Code: 7\nSignal: (none)\nProcess Group PGID: 1\n\nAI attribution note skipped: x.":                                                                                               0,
	}
	for out, want := range cases {
		if got := geminiExitCode(out); got != want {
			t.Errorf("geminiExitCode(%q) = %d, want %d", out, got, want)
		}
	}
}

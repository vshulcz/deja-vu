package sources

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A Qoder CLI transcript is Claude Code-shaped JSONL under
// <root>/projects/<flattened cwd>/<uuid>.jsonl. The store must attribute it
// to the "qoder" harness — never to "claude" — and discovery, single-file
// parsing and the offset variant must all agree.
func TestParseQoderFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_QODER_ROOT", filepath.Join(root, "qoder"))
	// A suffix nobody can have on disk, for the same reason the Qwen fixture
	// uses one: the decoder resolves an encoded project against the
	// filesystem (#3512).
	unique := encodedFixtureSuffix(t)
	project := filepath.Join(root, "qoder", "projects", "-tmp-deja-vu-"+unique)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "3f2b7c1e-0a5d-4b62-9e84-1c2d3e4f5a6b.jsonl")
	data := `{"type":"user","sessionId":"qo-1","cwd":"/tmp/deja-vu","timestamp":"2026-01-02T03:04:05Z","uuid":"m1","isSidechain":false,"message":{"role":"user","content":[{"type":"text","text":"hello qoder"}]}}
{"type":"assistant","sessionId":"qo-1","cwd":"/tmp/deja-vu","timestamp":"2026-01-02T03:05:05Z","uuid":"m2","parentUuid":"m1","message":{"role":"assistant","content":[{"type":"text","text":"hi from qoder"}]}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseQoderFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("parsed %d sessions, want 1: %#v", len(ss), ss)
	}
	if ss[0].Harness != "qoder" {
		t.Fatalf("harness = %q, want qoder", ss[0].Harness)
	}
	if ss[0].ID != "qo-1" {
		t.Fatalf("session id = %q, want qo-1", ss[0].ID)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %#v, want 2", ss[0].Messages)
	}
	if ss[0].Messages[0].Text != "hello qoder" || ss[0].Messages[1].Text != "hi from qoder" {
		t.Fatalf("message text = %#v", ss[0].Messages)
	}

	// The offset variant reads the same file from the beginning…
	from, err := ParseQoderFileFromOffset(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(from) != 1 || from[0].Harness != "qoder" || len(from[0].Messages) != 2 {
		t.Fatalf("offset parse = %#v", from)
	}
	// …and from the end yields no new messages.
	tail, err := ParseQoderFileFromOffset(path, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range tail {
		if len(s.Messages) != 0 {
			t.Fatalf("offset %d returned messages: %#v", len(data), s.Messages)
		}
	}

	// Discovery: the store lists exactly this transcript, and the registry
	// matcher claims it.
	files := QoderSessionFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("QoderSessionFiles = %v, want [%s]", files, path)
	}
	if !QoderUnderRoot(path) {
		t.Fatalf("QoderUnderRoot(%s) = false", path)
	}

	// End-to-end through the store loader.
	all := LoadQoder()
	if len(all) != 1 || all[0].Harness != "qoder" || all[0].ID != "qo-1" {
		t.Fatalf("LoadQoder = %#v", all)
	}
}

// The Qoder store must not reach outside its own root: a stock Claude
// transcript under ~/.claude stays with the claude harness.
func TestQoderUnderRootRejectsForeignPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_QODER_ROOT", filepath.Join(root, "qoder"))
	if QoderUnderRoot(filepath.Join(root, "claude", "projects", "x", "s.jsonl")) {
		t.Fatal("claimed a path outside the Qoder root")
	}
	if QoderUnderRoot(filepath.Join(root, "qoder", "projects-backup", "x", "s.jsonl")) {
		t.Fatal("claimed a sibling directory sharing the projects prefix")
	}
	if QoderUnderRoot(filepath.Join(root, "qoder", "projects")) {
		t.Fatal("claimed the projects root directory itself")
	}
	if QoderUnderRoot(filepath.Join(root, "qoder", "projects", "x", "meta.json")) {
		t.Fatal("claimed a non-jsonl file under the Qoder root")
	}
	if !QoderUnderRoot(filepath.Join(root, "qoder", "projects", "x", "s.jsonl")) {
		t.Fatal("rejected a path inside the Qoder root")
	}
}

// Mid-file offset reads yield only newly appended turns for incremental indexing.
func TestParseQoderFileFromOffsetMidFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_QODER_ROOT", filepath.Join(root, "qoder"))
	unique := encodedFixtureSuffix(t)
	project := filepath.Join(root, "qoder", "projects", "-workspace-app-"+unique)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "qo-offset.jsonl")
	line1 := `{"type":"user","sessionId":"qo-offset","cwd":"/workspace/app","timestamp":"2026-01-02T03:04:05Z","uuid":"m1","isSidechain":false,"message":{"role":"user","content":[{"type":"text","text":"before mid"}]}}` + "\n"
	line2 := `{"type":"assistant","sessionId":"qo-offset","cwd":"/workspace/app","timestamp":"2026-01-02T03:05:05Z","uuid":"m2","parentUuid":"m1","message":{"role":"assistant","content":[{"type":"text","text":"reply after mid"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line1+line2), 0o644); err != nil {
		t.Fatal(err)
	}
	mid := int64(len(line1))
	got, err := ParseQoderFileFromOffset(path, mid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Harness != "qoder" || got[0].ID != "qo-offset" {
		t.Fatalf("ParseQoderFileFromOffset = %#v", got)
	}
	if len(got[0].Messages) != 1 || got[0].Messages[0].Role != "assistant" || got[0].Messages[0].Text != "reply after mid" {
		t.Fatalf("messages = %#v, want single post-offset assistant turn", got[0].Messages)
	}
}

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

func TestParseQwenFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	// A suffix nobody can have on disk: the decoder resolves an encoded project
	// against the filesystem, so `-tmp-deja-vu` tested the dash fallback only on
	// a machine without `/tmp/deja-vu` (#3512).
	unique := encodedFixtureSuffix(t)
	project := filepath.Join(root, "qwen", "projects", "-tmp-deja-vu-"+unique, "chats")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "session.jsonl")
	data := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"first"},{"text":" question"}]}}
{"type":"assistant","sessionId":"q-1","timestamp":1767337505,"message":{"role":"model","parts":[{"text":"private reasoning","thought":true},{"text":"surface"},{"text":" answer"}]}}
{"type":"system","sessionId":"q-1","timestamp":"2026-01-02T03:06:05Z","message":{"role":"model","parts":[{"text":"skip"}]}}
{"type":"assistant","sessionId":"q-1","timestamp":"2026-01-02T03:07:05Z","message":{"parts":[{"text":"fallback"}]}}
{"type":"assistant",`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseQwenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Project != "vu/"+unique || len(ss[0].Messages) != 3 {
		t.Fatalf("parsed sessions = %#v", ss)
	}
	if ss[0].Messages[0].Text != "first\n question" || ss[0].Messages[1].Text != "surface\n answer" || ss[0].Messages[2].Role != "assistant" {
		t.Fatalf("parsed messages = %#v", ss[0].Messages)
	}
}

// Qwen Code writes every tool result as a type:"tool_result" record whose
// functionResponse carries output or error; the reader kept user and
// assistant records only, so a failing command's error never reached search
// or the fix pairs (#3281). Shapes from a real 0.20.0 store.
func TestQwenToolResultIsIndexedAsToolOutput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	dir := filepath.Join(root, "projects", "-w-app", "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:00.000Z","type":"user","cwd":"/w/app","message":{"role":"user","parts":[{"text":"build the missing package"}]}}`,
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:10.000Z","type":"assistant","cwd":"/w/app","message":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"run_shell_command","args":{"command":"go build ./missingpkg"}}}]}}`,
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:22.504Z","type":"tool_result","cwd":"/w/app","message":{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"run_shell_command","response":{"error":"Command: go build ./missingpkg\nDirectory: (root)\nOutput: go: go.mod file not found in current directory or any parent directory; see 'go help modules'\nError: (none)\nExit Code: 1"}}}]},"toolCallResult":{"callId":"call_1","status":"error"}}`,
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:30.000Z","type":"tool_result","cwd":"/w/app","message":{"role":"user","parts":[{"functionResponse":{"id":"call_2","name":"agent","response":{"output":"Cannot use working_dir: /w is not a git repository."}}}]},"toolCallResult":{"callId":"call_2","status":"success"}}`,
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:40.000Z","type":"assistant","cwd":"/w/app","message":{"role":"model","parts":[{"text":"go.mod is missing; I will init the module."}]}}`,
	}
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseQwenFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var toolOut []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			toolOut = append(toolOut, m.Text)
		}
		if m.Role == "user" && strings.Contains(m.Text, "go.mod file not found") {
			t.Errorf("tool output indexed as the user's words: %q", m.Text)
		}
	}
	if len(toolOut) != 2 || !strings.Contains(toolOut[0], "go.mod file not found") || !strings.Contains(toolOut[1], "not a git repository") {
		t.Fatalf("tool output = %q, want the error and the output of the two results", toolOut)
	}
}

// Qwen files its work as functionCall and functionResponse parts beside the
// text parts, so the reader that kept only text walked past every command a
// session ran.
const qwenWorkSession = `{"type":"user","timestamp":"2026-07-20T05:13:58Z","message":{"role":"user","parts":[{"text":"the build is broken"}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:00Z","message":{"role":"model","parts":[{"text":"looking at it"},{"functionCall":{"name":"run_shell_command","args":{"command":"go vet ./...","working_dir":"/w/app"}}}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:01Z","message":{"role":"user","parts":[{"functionResponse":{"name":"run_shell_command","response":{"output":"pattern ./...: directory prefix . does not contain main module"}}}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:02Z","message":{"role":"model","parts":[{"functionCall":{"name":"read_file","args":{"file_path":"/w/app/go.mod"}}}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:03Z","message":{"role":"model","parts":[{"functionCall":{"name":"replace","args":{"file_path":"/w/app/main.go","old_string":"println(\"hello\")","new_string":"println(\"goodbye\")"}}}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:04Z","message":{"role":"model","parts":[{"functionCall":{"name":"web_fetch","args":{"url":"https://example.invalid"}}}]}}
`

func writeQwenWork(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	dir := filepath.Join(root, "projects", "app", "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(p, []byte(qwenWorkSession), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseQwenEmitsWorkRecords(t *testing.T) {
	ss, err := ParseQwenFile(writeQwenWork(t))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go vet ./..." {
		t.Errorf("commands = %q, want the vet run", got)
	}
	wantFiles := []string{"/w/app/go.mod", "/w/app/main.go"}
	if got := byRole[RoleFiles]; strings.Join(got, "|") != strings.Join(wantFiles, "|") {
		t.Errorf("files = %q, want the read and the replace", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 ||
		got[0] != "/w/app/main.go\nprintln(\"hello\")" ||
		strings.Contains(strings.Join(got, ""), "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 ||
		!strings.Contains(got[0], "does not contain main module") {
		t.Errorf("tool output = %q, want the failure the command hit", got)
	}
	// A fetch is neither a file nor a command on this machine.
	for _, texts := range byRole {
		if strings.Contains(strings.Join(texts, ""), "example.invalid") {
			t.Error("a web fetch became a work record")
		}
	}
}

func TestParseQwenWorkRecordsSwitchOff(t *testing.T) {
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	ss, err := ParseQwenFile(writeQwenWork(t))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v", err)
	}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand, RoleFiles, RoleEdit, RoleToolOutput:
			t.Fatalf("record %q survived its switch", m.Role)
		}
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("want the two spoken turns back, got %d", len(ss[0].Messages))
	}
}

// claudeContentCases are content fields with the shapes the readers branch on:
// every item type, a type key spelled in capitals or given twice, a type that
// is not a string, items that are not objects, and content that is not an
// array at all.
var claudeContentCases = []string{
	`"plain text"`,
	`{"not":"an array"}`,
	`[1, "two", null]`,
	`[{"type":"text","text":"hello"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]`,
	`[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/tmp/app/a.go","old_string":"x := 1","new_string":"x := 2"}}]`,
	`[{"type":"tool_use","id":"t3","name":"Write","input":{"file_path":"/tmp/app/b.go","content":"package b\n"}}]`,
	`[{"type":"tool_use","id":"t4","name":"MultiEdit","input":{"file_path":"/tmp/app/c.go","edits":[{"old_string":"a","new_string":"b"}]}}]`,
	`[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 2\nFAIL"}]`,
	`[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"ok"}]}]`,
	`[{"Type":"tool_use","id":"t5","name":"Bash","input":{"command":"make build"}}]`,
	`[{"TYPE":"tool_result","tool_use_id":"t5","is_error":true,"content":"Exit code 1"}]`,
	`[{"type":"text","type":"tool_use","id":"t6","name":"Bash","input":{"command":"ls -la"}}]`,
	`[{"type":"tool_use","type":"text","text":"twice"}]`,
	`[{"type":7,"id":"t7","name":"Bash","input":{"command":"npm test"}}]`,
	`[{"type":"tool_use","id":"t8","name":"Bash","input":"not an object"}]`,
	`[{"type":"tool_result","tool_use_id":"t8","is_error":"yes","content":"Exit code 3"}]`,
	`[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/app/d.go"}},{"type":"tool_use","name":"NotebookEdit","input":{"notebook_path":"/tmp/n.ipynb","new_source":"x","edit_mode":"replace"}}]`,
}

// Splitting the content once must not change what any reader takes from it:
// the reference is the same content with no item's type known, which skips
// nothing and is how each reader decoded every item before (#5586).
func TestSplitContentReadsWhatEachReaderReadAlone(t *testing.T) {
	for _, raw := range claudeContentCases {
		fresh := func() *claudeContent {
			c := splitClaudeContent(json.RawMessage(raw))
			c.kind, c.known = make([]string, len(c.items)), make([]bool, len(c.items))
			return c
		}
		c := splitClaudeContent(json.RawMessage(raw))
		gotText, gotTool := claudeTextKindIn(c)
		wantText, wantTool := claudeTextKindIn(fresh())
		checks := []struct {
			name      string
			got, want any
		}{
			{"text", []any{gotText, gotTool}, []any{wantText, wantTool}},
			{"paths", claudeToolPathsIn(c), claudeToolPathsIn(fresh())},
			{"edits", claudeEditSpansIn(c), claudeEditSpansIn(fresh())},
			{"wrote", claudeWroteRecordsIn(c), claudeWroteRecordsIn(fresh())},
			{"commands", claudeCommandsIn(c), claudeCommandsIn(fresh())},
			{"outcomes", claudeToolOutcomesIn(c), claudeToolOutcomesIn(fresh())},
		}
		for _, ch := range checks {
			if !reflect.DeepEqual(ch.got, ch.want) {
				t.Errorf("%s of %s = %#v, want %#v", ch.name, raw, ch.got, ch.want)
			}
		}
	}
}

// A megabyte line buffer per transcript was 11.7 GB of a 12k-session cold
// build (#5586). Reading many small transcripts allocates far less than one
// buffer each now.
func TestReadingTranscriptsReusesTheLineBuffer(t *testing.T) {
	if raceEnabled {
		t.Skip("sync.Pool drops buffers on purpose under -race")
	}
	dir := t.TempDir()
	paths := make([]string, 0, 64)
	for i := range 64 {
		p := filepath.Join(dir, fmt.Sprintf("r%d.jsonl", i))
		line := `{"type":"response_item","payload":{"type":"message","role":"user","content":"hi"}}` + "\n"
		if err := os.WriteFile(p, []byte(strings.Repeat(line, 3)), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for _, p := range paths {
		if err := scanJSONLFromOffset(p, 0, func(map[string]any) {}); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / uint64(len(paths)); per > 256<<10 {
		t.Fatalf("%d bytes allocated per transcript of three short lines", per)
	}
}

// Naming the store a kind belongs to is asked per session; it used to build
// the whole registry to answer.
func TestHarnessForKindAllocatesNothing(t *testing.T) {
	if HarnessForKind("codex") != "codex" {
		t.Fatal("codex is not its own store")
	}
	if n := testing.AllocsPerRun(50, func() { _ = HarnessForKind("claude-history") }); n != 0 {
		t.Fatalf("HarnessForKind allocated %v times", n)
	}
}

// readerEnv points every store lookup at a temp home.
func readerEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index"))
	return home
}

// readerFixture copies a registry fixture into dir under the same relative path
// below its harness folder, so path-derived names stay what the reader expects.
func readerFixture(t *testing.T, dir, rel string, mutate func([]byte) []byte) string {
	t.Helper()
	src := filepath.Join("..", "..", "fixtures", "registry", filepath.FromSlash(rel))
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		b = mutate(b)
	}
	dst := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func readerMessages(ss []model.Session) []string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			out = append(out, m.Role+": "+m.Text)
		}
	}
	return out
}

// A flat Command Code transcript has no `session` header: line 1 is the
// first prompt, and an appended tail must not bring it back again.
func TestCommandCodeResumeDoesNotReplayTheFirstPrompt(t *testing.T) {
	readerEnv(t)
	dir := t.TempDir()
	path := readerFixture(t, dir, "commandcode/-workspace-commandcode-demo/registry-commandcode.jsonl", nil)
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	whole, err := ParseCommandCodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := readerMessages(whole)

	// The first pass saw only line 1; the client then appended the rest.
	off := bytes.IndexByte(full, '\n') + 1
	if err = os.WriteFile(path, full[:off], 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := ParseCommandCodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, full, 0o644); err != nil {
		t.Fatal(err)
	}
	if !commandCodeExitResumes(path, int64(off)) {
		t.Fatal("precondition: the kind should accept this tail as a resume")
	}
	tail, err := ParseCommandCodeFileFromOffset(path, int64(off))
	if err != nil {
		t.Fatal(err)
	}
	got := append(readerMessages(first), readerMessages(tail)...)
	if len(got) != len(want) {
		t.Fatalf("first pass + resumed tail = %d messages, full parse = %d\n got: %q\nwant: %q", len(got), len(want), got, want)
	}
}

// The whole-file JSON readers take a store whose first bytes are a UTF-8
// byte order mark, as the line readers do.
func TestWholeFileJSONReadersSkipAByteOrderMark(t *testing.T) {
	readerEnv(t)
	bom := func(b []byte) []byte { return append([]byte("\xef\xbb\xbf"), b...) }
	cases := []struct {
		name  string
		rel   string
		parse func(string) ([]model.Session, error)
	}{
		{"amp", "amp/thread.json", ParseAmpFile},
		{"cline-sdk", "cline/modern/sessions/session_synthetic_01/session_synthetic_01.messages.json", ParseClineFile},
		{"cline-legacy", "cline/legacy/tasks/1767225600000/api_conversation_history.json", ParseClineFile},
		{"roo", "roo/tasks/1767225700000/api_conversation_history.json", ParseRooTask},
		{"kilocode", "kilocode/tasks/registry-kilocode/api_conversation_history.json", ParseKiloTask},
		{"continue", "continue/sessions/8f1c2a3e-0000-4000-8000-000000000000.json", ParseContinueFile},
		{"codewhale", "codewhale/sessions/registry-codewhale.json", ParseCodeWhaleFile},
		{"clawcode", "clawcode/legacy.json", ParseClawCodeFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plain := readerFixture(t, t.TempDir(), c.rel, nil)
			base, err := c.parse(plain)
			if err != nil || len(readerMessages(base)) == 0 {
				t.Fatalf("precondition: plain fixture parses (err=%v, sessions=%d)", err, len(base))
			}
			marked := readerFixture(t, t.TempDir(), c.rel, bom)
			got, err := c.parse(marked)
			if len(readerMessages(got)) != len(readerMessages(base)) {
				t.Errorf("with a UTF-8 BOM: %d sessions / %d messages (err=%v); without: %d / %d",
					len(got), len(readerMessages(got)), err, len(base), len(readerMessages(base)))
			}
		})
	}
}

// grok's updates.jsonl keeps its first update, the user's prompt, behind a
// byte order mark.
func TestGrokKeepsTheFirstUpdateAfterAByteOrderMark(t *testing.T) {
	readerEnv(t)
	rel := "grok/sessions/workspace%2Fregistry-demo/registry-grok/updates.jsonl"
	base, err := ParseGrokFile(readerFixture(t, t.TempDir(), rel, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseGrokFile(readerFixture(t, t.TempDir(), rel, func(b []byte) []byte { return append([]byte("\xef\xbb\xbf"), b...) }))
	if err != nil {
		t.Fatal(err)
	}
	if g, w := readerMessages(got), readerMessages(base); len(g) != len(w) {
		t.Fatalf("with a UTF-8 BOM: %q\nwithout: %q", g, w)
	}
}

// aider's markdown history keeps the session whose header sits behind a byte
// order mark.
func TestAiderKeepsTheFirstSessionAfterAByteOrderMark(t *testing.T) {
	readerEnv(t)
	rel := "aider/.aider.chat.history.md"
	base, err := ParseAiderFile(readerFixture(t, t.TempDir(), rel, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseAiderFile(readerFixture(t, t.TempDir(), rel, func(b []byte) []byte { return append([]byte("\xef\xbb\xbf"), b...) }))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(base) {
		t.Fatalf("with a UTF-8 BOM: %d sessions; without: %d", len(got), len(base))
	}
}

// ---- codex, qwen, copilot

func readerWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readerTexts(ss []model.Session) []string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			out = append(out, m.Role+":"+m.Text)
		}
	}
	return out
}

// A codex session_meta line over 1 MiB (base instructions, AGENTS.md text)
// still names the session an appended tail belongs to.
func TestCodexResumeReadsAHugeSessionMeta(t *testing.T) {
	readerEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-07-17T09-00-00-0198aaaa-0000-7000-8000-000000000001.jsonl")
	big := strings.Repeat("x", 1100*1024)
	head := `{"timestamp":"2026-07-17T09:00:00Z","type":"session_meta","payload":{"id":"0198aaaa-0000-7000-8000-000000000001","cwd":"/work/demo","instructions":"` + big + `"}}` + "\n" +
		`{"timestamp":"2026-07-17T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first question"}]}}` + "\n"
	tail := `{"timestamp":"2026-07-17T09:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second question"}]}}` + "\n"
	readerWrite(t, path, head+tail)

	full, err := ParseCodexRollout(path)
	if err != nil || len(full) != 1 {
		t.Fatalf("full parse: %v %d sessions", err, len(full))
	}
	resumed, err := ParseCodexRolloutFromOffset(path, int64(len(head)))
	if err != nil || len(resumed) != 1 {
		t.Fatalf("resume parse: %v %d sessions", err, len(resumed))
	}
	if resumed[0].ID != full[0].ID || resumed[0].Project != full[0].Project {
		t.Fatalf("appended tail filed under id=%q project=%q, want the session's own id=%q project=%q (session split on resume)",
			resumed[0].ID, resumed[0].Project, full[0].ID, full[0].Project)
	}
}

// The event stream stands in only for rollouts with no roled items. A tail
// that holds only the event of a turn whose response_item comes next must
// not store the turn twice.
func TestCodexEventOnlyTailIsNotStoredTwice(t *testing.T) {
	readerEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-07-17T09-00-00-0198bbbb-0000-7000-8000-000000000002.jsonl")
	l1 := `{"timestamp":"2026-07-17T09:00:00Z","type":"session_meta","payload":{"id":"0198bbbb-0000-7000-8000-000000000002","cwd":"/work/demo"}}` + "\n" +
		`{"timestamp":"2026-07-17T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"run the tests"}]}}` + "\n" +
		`{"timestamp":"2026-07-17T09:00:01.001Z","type":"event_msg","payload":{"type":"user_message","message":"run the tests"}}` + "\n"
	l2 := `{"timestamp":"2026-07-17T09:00:03Z","type":"event_msg","payload":{"type":"agent_message","message":"running go test now"}}` + "\n"
	l3 := `{"timestamp":"2026-07-17T09:00:03.002Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"running go test now"}]}}` + "\n"
	readerWrite(t, path, l1+l2+l3)

	full, _ := ParseCodexRollout(path)
	fullTexts := readerTexts(full)

	// Three passes: the file as each pass saw it, resumed from the last end.
	readerWrite(t, path, l1)
	p1, _ := ParseCodexRollout(path)
	readerWrite(t, path, l1+l2)
	p2, _ := ParseCodexRolloutFromOffset(path, int64(len(l1)))
	readerWrite(t, path, l1+l2+l3)
	p3, _ := ParseCodexRolloutFromOffset(path, int64(len(l1+l2)))
	var got []string //nolint:prealloc // each pass adds an unknown number of texts
	for _, ss := range [][]model.Session{p1, p2, p3} {
		got = append(got, readerTexts(ss)...)
	}
	if len(got) != len(fullTexts) {
		t.Fatalf("incremental passes stored %d messages %q, a full read stores %d %q", len(got), got, len(fullTexts), fullTexts)
	}
}

func readerKind(t *testing.T, name string) FileKind {
	t.Helper()
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			if k.Name == name {
				return k
			}
		}
	}
	t.Fatalf("no kind %q", name)
	return FileKind{}
}

// Qwen files a shell call and its result as two records joined by id. A tail
// whose failed result answers a call stored already is read whole, so the
// command gets its exit as a full read gives it.
func TestQwenTailAnsweringAnEarlierCallIsReadWhole(t *testing.T) {
	home := readerEnv(t)
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(home, "qwen"))
	path := filepath.Join(home, "qwen", "projects", "-w-app", "chats", "s1.jsonl")
	head := `{"sessionId":"s1","timestamp":"2026-10-01T15:17:59.000Z","type":"user","cwd":"/w/app","message":{"role":"user","parts":[{"text":"run the tests"}]}}` + "\n" +
		`{"sessionId":"s1","timestamp":"2026-10-01T15:18:00.000Z","type":"assistant","cwd":"/w/app","message":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"run_shell_command","args":{"command":"make test"}}}]}}` + "\n"
	tail := `{"sessionId":"s1","timestamp":"2026-10-01T15:18:09.000Z","type":"tool_result","cwd":"/w/app","message":{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"run_shell_command","response":{"error":"Command: make test\nDirectory: (root)\nOutput: FAIL\nError: (none)\nExit Code: 2\nSignal: (none)\nProcess Group PGID: 4242"}}}]},"toolCallResult":{"callId":"call_1","status":"error"}}` + "\n"
	readerWrite(t, path, head+tail)

	k := readerKind(t, "qwen")
	full, _ := k.Parse(path, 0)
	if !strings.Contains(strings.Join(readerTexts(full), "|"), "$ make test  → exit 2") {
		t.Fatalf("full read did not mark the exit: %q", readerTexts(full))
	}
	if k.Resumes != nil && !k.Resumes(path, int64(len(head))) {
		t.Skip("kind refuses the resume; the index reads the file whole")
	}
	readerWrite(t, path, head)
	first, _ := k.Parse(path, 0)
	readerWrite(t, path, head+tail)
	second, _ := k.ParseFrom(path, int64(len(head)), 0)
	got := strings.Join(append(readerTexts(first), readerTexts(second)...), "|")
	if !strings.Contains(got, "→ exit 2") {
		t.Fatalf("resumed passes stored %q; the full read marks \"$ make test  → exit 2\" and the resume (allowed, Resumes=nil) loses it", got)
	}
}

// Copilot Chat's agent transcript names each call twice, in toolRequests and
// in tool.execution_start, under one toolCallId: the command is one run.
func TestCopilotAgentCommandIsIndexedOnce(t *testing.T) {
	readerEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ws1", "GitHub.copilot-chat", "transcripts", "7c9e2d2c-0000-4000-8000-0000000000aa.jsonl")
	body := strings.Join([]string{
		`{"type":"session.start","data":{"sessionId":"7c9e2d2c-0000-4000-8000-0000000000aa","startTime":"2026-09-14T05:46:01.086Z"},"timestamp":"2026-09-14T05:46:01.086Z"}`,
		`{"type":"user.message","data":{"content":"build it"},"timestamp":"2026-09-14T05:46:10.000Z"}`,
		`{"type":"assistant.message","data":{"messageId":"m1","content":"Building.","toolRequests":[{"toolCallId":"c1","name":"run_in_terminal","arguments":"{\"command\":\"packer build -only=amd64 .\"}","type":"function"}]},"timestamp":"2026-09-14T05:46:12.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"c1","toolName":"run_in_terminal","arguments":{"command":"packer build -only=amd64 ."}},"timestamp":"2026-09-14T05:46:13.000Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"c1","success":true},"timestamp":"2026-09-14T05:46:20.000Z"}`,
	}, "\n") + "\n"
	readerWrite(t, path, body)
	ss, err := ParseCopilotChatFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	n := 0
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("one call c1 produced %d command records, want 1: %q", n, readerTexts(ss))
	}
}

// ---- sqlite and whole-file JSON stores

func readerSQLite(t *testing.T, db, stmts string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
}

// goose stores whole seconds on both sides of its since clause. A turn
// written in the watermark's own second is read on the next pass, as crush
// and hermes read theirs (#4381, #2075).
func TestGooseSinceKeepsATurnInTheWatermarkSecond(t *testing.T) {
	readerEnv(t)
	db := filepath.Join(t.TempDir(), "sessions.db")
	type turn = struct {
		Session, Role, Text string
		At                  int64
	}
	at := int64(1785166187)
	gooseStore(t, db, []turn{{"g1", "user", "why does pgbouncer time out", at}})

	full, err := ParseGooseDB(db)
	if err != nil || len(full) != 1 {
		t.Fatalf("full parse: %v %d", err, len(full))
	}
	watermark := full[0].Updated // what setStoreLastUpdated stamps

	// The reply lands in the same second the pass above saw.
	gooseStore(t, db, []turn{{"g1", "assistant", "the pool is too small", at}})

	got, err := ParseGooseDBSince(db, watermark)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range got {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "the pool is too small") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("since(%s) returned %d sessions without the reply written in the watermark's second; want the session back whole with it", watermark.UTC().Format(time.RFC3339), len(got))
	}
}

// A Continue session file that does not decode is reported, as cline and roo
// report theirs.
func TestContinueUnreadableSessionIsReported(t *testing.T) {
	readerEnv(t)
	dir := t.TempDir()
	cont := filepath.Join(dir, "sessions", "8f1c2a3e-0000-4000-8000-000000000000.json")
	if err := os.MkdirAll(filepath.Dir(cont), 0o755); err != nil {
		t.Fatal(err)
	}
	// Torn mid-write: the client rewrites the whole document on every turn.
	body := `{"sessionId":"8f1c2a3e-0000-4000-8000-000000000000","title":"retry loop","history":[{"message":{"role":"user","content":"why does the ret`
	if err := os.WriteFile(cont, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseContinueFile(cont)
	if err != nil || len(ss) != 0 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}

	// The sibling for comparison: a torn cline task is reported.
	task := filepath.Join(dir, "tasks", "1767225600000", "api_conversation_history.json")
	if err := os.MkdirAll(filepath.Dir(task), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task, []byte(`[{"role":"user","content":[{"type":"text","text":"why does the ret`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = ParseClineFile(task)

	failed := DiagFailedPaths()
	if _, ok := failed[task]; !ok {
		t.Fatalf("control: cline did not report its torn task either: %v", failed)
	}
	if _, ok := failed[cont]; !ok {
		t.Errorf("continue: unreadable session not in DiagFailedPaths (cline's is: %q); want it reported like cline's", failed[task])
	}
}

var _ = fmt.Sprintf

// ---- pi family, openclaw, reasonix

// readerSplit is what the incremental pass stores for a file whose first n lines
// were indexed by one pass and the rest appended before the next: the prefix
// read whole, then the tail read from its offset when the kind's Resumes lets
// it. resumed is false when the kind sends the file back for a whole read.
func readerSplit(t *testing.T, kind FileKind, path string, lines []string, n int) (stored []model.Message, resumed bool) {
	t.Helper()
	prefix := strings.Join(lines[:n], "\n") + "\n"
	if err := os.WriteFile(path, []byte(prefix), 0o644); err != nil {
		t.Fatal(err)
	}
	pre, err := kind.Parse(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	off := int64(len(prefix))
	if kind.Resumes != nil && !kind.Resumes(path, off) {
		return nil, false
	}
	tail, err := kind.ParseFrom(path, off, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range pre {
		stored = append(stored, s.Messages...)
	}
	for _, s := range tail {
		stored = append(stored, s.Messages...)
	}
	return stored, true
}

func readerRoles(ms []model.Message, roles ...string) []string {
	var out []string
	for _, m := range ms {
		for _, r := range roles {
			if m.Role == r {
				out = append(out, r+" "+m.Text)
			}
		}
	}
	return out
}

func readerWriteLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Reasonix keeps a relocated state root in config.toml under [storage] state,
// and a TOML line may end in a comment after the value.
func TestReasonixStateTakesATrailingComment(t *testing.T) {
	home := readerEnv(t)
	t.Setenv("REASONIX_HOME", filepath.Join(home, ".reasonix"))
	t.Setenv("REASONIX_STATE_HOME", "")
	moved := filepath.Join(home, "rx-state")
	readerWriteLines(t, filepath.Join(home, ".reasonix", "config.toml"), []string{
		"[storage]",
		`state = "` + moved + `"  # moved off the system disk`,
	})
	if got := ReasonixStateRoot(); got != moved {
		t.Fatalf("state root = %q, want %q: a valid TOML trailing comment made deja ignore the relocated store", got, moved)
	}
}

// OpenClaw's apply_patch holds its edit and written records until its result
// says the patch applied. A failed result that lands in the next pass still
// leaves no edit behind, as a whole read leaves none.
func TestOpenClawFailedApplyPatchAcrossAResumeKeepsNoEdit(t *testing.T) {
	home := readerEnv(t)
	root := filepath.Join(home, ".openclaw")
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	t.Setenv("OPENCLAW_STATE_DIR", root)
	path := filepath.Join(root, "agents", "main", "sessions", "oc-s1.jsonl")
	patch := "*** Begin Patch\n*** Update File: /tmp/proj/retry.go\n@@\n-\tfor {\n+\tfor attempt := 0; attempt < 5; attempt++ {\n*** End Patch"
	lines := []string{
		`{"type":"session","version":3,"id":"oc-s1","timestamp":"2026-09-20T10:00:00.000Z","cwd":"/tmp/proj"}`,
		`{"type":"message","id":"u1","timestamp":"2026-09-20T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}`,
		vocabJSON(map[string]any{"type": "message", "id": "a1", "timestamp": "2026-09-20T10:00:01.000Z", "message": map[string]any{"role": "assistant",
			"content": []any{map[string]any{"type": "toolCall", "id": "c1", "name": "apply_patch", "arguments": map[string]any{"input": patch}}}}}),
		`{"type":"message","id":"r1","timestamp":"2026-09-20T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"apply_patch","content":[{"type":"text","text":"Error: patch did not apply: context mismatch in /tmp/proj/retry.go"}],"details":{},"isError":true}}`,
	}
	readerWriteLines(t, path, lines)
	kind := readerKind(t, "openclaw")
	full, err := kind.Parse(path, 0)
	if err != nil || len(full) != 1 {
		t.Fatalf("full parse: %v %d", err, len(full))
	}
	if got := readerRoles(full[0].Messages, RoleEdit, RoleWrote); len(got) != 0 {
		t.Fatalf("whole read of a failed patch kept %q", got)
	}
	stored, resumed := readerSplit(t, kind, path, lines, 3)
	if !resumed {
		return
	}
	if got := readerRoles(stored, RoleEdit, RoleWrote); len(got) != 0 {
		t.Fatalf("after an append the index holds %d edit/wrote records for a patch that failed (a whole read holds 0): %q", len(got), got)
	}
}

func TestAntigravityIncrementalResumeParity(t *testing.T) {
	home := readerEnv(t)
	path := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "00000000-1111-2222-3333-444444444444", ".system_generated", "logs", "transcript.jsonl")
	lines := []string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-10-01T10:00:00Z","content":"<USER_REQUEST>\ninspect and update retry.go\n</USER_REQUEST>"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-01T10:00:05Z","thinking":"let us check retry.go","content":"Reading and updating retry.go.","tool_calls":[{"id":"c1","name":"view_file","args":{"AbsolutePath":"/workspace/app/retry.go"}},{"id":"c2","name":"write_to_file","args":{"TargetFile":"/workspace/app/new.go","CodeContent":"package app\nfunc New() {}\n"}},{"id":"c3","name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/workspace/app"}}]}`,
		`{"step_index":2,"source":"MODEL","type":"VIEW_FILE","status":"DONE","created_at":"2026-10-01T10:00:06Z","content":"File Path: ` + "`file:///workspace/app/retry.go`" + `\nShowing lines 1 to 10"}`,
		`{"step_index":3,"source":"MODEL","type":"CODE_ACTION","status":"DONE","created_at":"2026-10-01T10:00:07Z","content":"Created file ` + "`file:///workspace/app/new.go`" + `"}`,
		`{"step_index":4,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-10-01T10:00:08Z","content":"Task Description: go test ./...\nOutput:\nok"}`,
		`{"step_index":5,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-10-01T10:01:00Z","content":"<USER_REQUEST>\nnow run linter\n</USER_REQUEST>"}`,
		`{"step_index":6,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-01T10:01:05Z","content":"Running linter.","tool_calls":[{"id":"c4","name":"run_command","args":{"CommandLine":"golangci-lint run"}}]}`,
		`{"step_index":7,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-10-01T10:01:06Z","content":"Task Description: golangci-lint run\nOutput:\nok"}`,
	}
	readerWriteLines(t, path, lines)
	kind := readerKind(t, "antigravity")
	if kind.ParseFrom == nil || kind.Resumes == nil {
		t.Fatal("antigravity FileKind must wire ParseFrom and Resumes")
	}
	full, err := kind.Parse(path, 0)
	if err != nil || len(full) != 1 {
		t.Fatalf("full parse: %v (len=%d)", err, len(full))
	}
	want := readerMessages(full)

	// Split both mid-turn (right after PLANNER_RESPONSE at n=2) and between turns (n=5).
	for _, splitAt := range []int{2, 5} {
		stored, resumed := readerSplit(t, kind, path, lines, splitAt)
		if !resumed {
			t.Fatalf("splitAt=%d: expected antigravityResumes=true", splitAt)
		}
		got := readerMessages([]model.Session{{Messages: stored}})
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("splitAt=%d parity mismatch:\ngot:  %q\nwant: %q", splitAt, got, want)
		}
	}

	// If a MODEL step before the tail's first PLANNER_RESPONSE stamps a non-zero exit
	// code on a command from before offset, antigravityResumes must return false.
	failLines := append([]string{}, lines[:2]...)
	failLines = append(failLines, `{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"ERROR","created_at":"2026-10-01T10:00:08Z","content":"Task Description: go test ./...\nThe command exited with code 1.\nOutput:\nFAIL"}`)
	readerWriteLines(t, path, failLines)
	if _, resumed := readerSplit(t, kind, path, failLines, 2); resumed {
		t.Fatal("expected antigravityResumes=false when tail modifies exit code of pre-offset command")
	}
}

func TestCursorTranscriptIncrementalResumeParity(t *testing.T) {
	home := readerEnv(t)
	path := filepath.Join(home, ".cursor", "projects", "Users-alex-workspace-app", "agent-transcripts", "cur-123.jsonl")
	lines := []string{
		`{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>2026-10-01T12:00:00Z</timestamp>\nfix bug in main.go"}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"Checking main.go now."},{"type":"tool_use","name":"Read","input":{"path":"/Users/alex/workspace/app/main.go"}}]}}`,
		`{"role":"user","message":{"content":[{"type":"text","text":"thanks, now add a unit test"}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"Added unit test in main_test.go."}]}}`,
	}
	readerWriteLines(t, path, lines)
	kind := readerKind(t, "cursor")
	if kind.ParseFrom == nil || kind.Resumes == nil {
		t.Fatal("cursor FileKind must wire ParseFrom and Resumes")
	}
	full, err := kind.Parse(path, 0)
	if err != nil || len(full) != 1 {
		t.Fatalf("full parse: %v (len=%d)", err, len(full))
	}
	want := readerMessages(full)

	stored, resumed := readerSplit(t, kind, path, lines, 2)
	if !resumed {
		t.Fatal("expected cursorTranscriptResumes=true")
	}
	got := readerMessages([]model.Session{{Messages: stored}})
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("cursor parity mismatch:\ngot:  %q\nwant: %q", got, want)
	}
	for i, m := range stored {
		if m.Time.IsZero() {
			t.Fatalf("message %d has zero timestamp after incremental resume", i)
		}
	}
}

// clearReasonixEnv keeps the machine's own Reasonix settings out of a test.
func clearReasonixEnv(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	for _, k := range []string{"DEJA_REASONIX_ROOT", "REASONIX_HOME", "REASONIX_STATE_HOME"} {
		t.Setenv(k, "")
	}
}

func writeJSONLines(t *testing.T, path string, lines ...any) {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	writeReasonixFile(t, path, b.String())
}

func writeJSONDoc(t *testing.T, path string, v any) {
	t.Helper()
	j, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	writeReasonixFile(t, path, string(j))
}

func writeReasonixFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func toolCall(id, name string, args map[string]any) map[string]any {
	a, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return map[string]any{"id": id, "name": name, "arguments": string(a)}
}

// The current store: a transcript under projects/<slug>/sessions, its
// `.jsonl.meta` naming the workspace and the clock, createdAt on the turns the
// host stamps, and flat tool_calls whose arguments are a JSON string.
func TestParseReasonixSession(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	path := filepath.Join(root, "projects", "-w-relaylab", "sessions", "20260920-101000.000000000-deepseek-chat.jsonl")
	userAt := time.Date(2026, 9, 20, 10, 10, 5, 0, time.UTC)
	writeJSONLines(t, path,
		map[string]any{"role": "system", "content": "You are Reasonix."},
		map[string]any{"role": "user", "content": "<context>open files</context>\nthe relay drops frames under load", "raw_content": "the relay drops frames under load", "createdAt": userAt.UnixMilli()},
		map[string]any{"role": "user", "content": "[host] resuming after interrupt", "host_authored": true},
		map[string]any{"role": "assistant", "content": "Running the relay tests first.", "tool_calls": []any{
			toolCall("call_00", "bash", map[string]any{"command": "go test ./relay -run TestBackpressure"}),
		}},
		map[string]any{"role": "tool", "tool_call_id": "call_00", "name": "bash", "content": "relay_test.go:41: dropped 3 frames"},
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
			toolCall("call_01", "edit_file", map[string]any{"path": "relay/queue.go", "old_string": "select {\ndefault:", "new_string": "q <- f"}),
		}},
	)
	writeJSONDoc(t, path+".meta", map[string]any{
		"id":             "20260920-101000.000000000-deepseek-chat",
		"created_at":     "2026-09-20T10:10:00Z",
		"updated_at":     "2026-09-20T10:14:00Z",
		"workspace_root": "/w/relaylab",
		"custom_title":   "relay backpressure",
	})

	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.Harness != "reasonix" || s.ID != "20260920-101000.000000000-deepseek-chat" {
		t.Errorf("harness/id = %q/%q", s.Harness, s.ID)
	}
	if s.Project != "w/relaylab" {
		t.Errorf("project = %q, want the workspace from the meta", s.Project)
	}
	if s.Title != "relay backpressure" {
		t.Errorf("title = %q", s.Title)
	}
	if !s.Started.Equal(time.Date(2026, 9, 20, 10, 10, 0, 0, time.UTC)) || !s.Updated.Equal(time.Date(2026, 9, 20, 10, 14, 0, 0, time.UTC)) {
		t.Errorf("started/updated = %v/%v, want the meta's span", s.Started, s.Updated)
	}

	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole["user"]; len(got) != 1 || got[0] != "the relay drops frames under load" {
		t.Errorf("user turns = %q, want the typed prompt alone", got)
	}
	if got := byRole["assistant"]; len(got) != 1 || got[0] != "Running the relay tests first." {
		t.Errorf("assistant turns = %q", got)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./relay -run TestBackpressure" {
		t.Errorf("commands = %q, want the bash call's argument", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 || got[0] != "relay_test.go:41: dropped 3 frames" {
		t.Errorf("tool output = %q", got)
	}
	if got := byRole[RoleFiles]; len(got) != 1 || got[0] != "relay/queue.go" {
		t.Errorf("files = %q", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 || !strings.HasPrefix(got[0], "relay/queue.go\n") {
		t.Errorf("edits = %q, want the replaced span", got)
	}
	for _, texts := range byRole {
		for _, text := range texts {
			if strings.Contains(text, "You are Reasonix") || strings.Contains(text, "[host]") {
				t.Errorf("%q was indexed as something someone said", text)
			}
		}
	}
	if !s.Messages[0].Time.Equal(userAt) {
		t.Errorf("first turn at %v, want its own createdAt %v", s.Messages[0].Time, userAt)
	}
	for i := 1; i < len(s.Messages); i++ {
		if s.Messages[i].Time.Before(s.Messages[i-1].Time) {
			t.Fatalf("record %d goes back in time", i)
		}
	}
}

// v0.x wrote the same line shape with OpenAI's nested tool_calls, the
// workspace in <id>.meta.json and the clock only in <id>.events.jsonl.
func TestParseReasonixLegacySession(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	dir := filepath.Join(root, "sessions")
	path := filepath.Join(dir, "code-scripts-202606200404.jsonl")
	args := mustJSON(t, map[string]any{"command": "npm run build"})
	writeJSONLines(t, path,
		map[string]any{"role": "user", "content": "the build script fails on windows"},
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
			"id": "call_00_a", "type": "function",
			"function": map[string]any{"name": "bash", "arguments": string(args)},
		}}},
		map[string]any{"role": "tool", "tool_call_id": "call_00_a", "name": "bash", "content": "'rm' is not recognized"},
	)
	writeJSONDoc(t, filepath.Join(dir, "code-scripts-202606200404.meta.json"), map[string]any{
		"workspace": "/w/scripts", "summary": "windows build script",
	})
	writeJSONLines(t, filepath.Join(dir, "code-scripts-202606200404.events.jsonl"),
		map[string]any{"id": 1, "type": "user.message", "ts": "2026-06-20T04:04:00Z", "text": "the build script fails on windows"},
		map[string]any{"id": 2, "type": "model.final", "ts": "2026-06-20T04:06:30Z", "usage": map[string]any{"prompt_tokens": 900}, "costUsd": 0.001},
	)

	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.Project != "w/scripts" || s.Title != "windows build script" {
		t.Errorf("project/title = %q/%q, want the v0.x meta's", s.Project, s.Title)
	}
	if !s.Started.Equal(time.Date(2026, 6, 20, 4, 4, 0, 0, time.UTC)) || !s.Updated.Equal(time.Date(2026, 6, 20, 4, 6, 30, 0, time.UTC)) {
		t.Errorf("started/updated = %v/%v, want the event log's span", s.Started, s.Updated)
	}
	var cmds []string
	for _, m := range s.Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ npm run build" {
		t.Errorf("commands = %q, want the nested function's arguments", cmds)
	}
}

// With no sidecar at all the file's own mtime is the clock, so the session
// still sorts and falls inside a --since window.
func TestParseReasonixFallsBackToMtime(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	path := filepath.Join(root, "sessions", "bare.jsonl")
	writeJSONLines(t, path,
		map[string]any{"role": "user", "content": "run it again"},
		map[string]any{"role": "user", "content": "run it again"},
	)
	mtime := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if ss[0].Started.IsZero() || !ss[0].Started.Equal(mtime) {
		t.Errorf("started = %v, want the file's mtime %v", ss[0].Started, mtime)
	}
	if n := len(ss[0].Messages); n != 2 || ss[0].Messages[0].Time.Equal(ss[0].Messages[1].Time) {
		t.Error("two identical turns share one stamp, so the index keeps one of them")
	}
}

// The session directories hold a dozen sidecars beside each transcript, and
// only the transcript is a conversation.
func TestReasonixSessionFilesSkipSidecars(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	global := filepath.Join(root, "sessions")
	project := filepath.Join(root, "projects", "-w-api", "sessions")
	for _, p := range []string{
		filepath.Join(global, "a.jsonl"),
		filepath.Join(global, "a.events.jsonl"),
		filepath.Join(global, "a.events.jsonl.damaged"),
		filepath.Join(global, "a.wire.jsonl"),
		filepath.Join(global, "a.jsonl.meta"),
		filepath.Join(global, "a.jsonl.lock"),
		filepath.Join(global, "a.meta.json"),
		filepath.Join(global, "a.context.json"),
		filepath.Join(global, "subagent-x.jsonl"),
		filepath.Join(global, "subagents", "a", "worker.jsonl"),
		filepath.Join(project, "b.jsonl"),
		filepath.Join(project, "b.guardian.jsonl"),
	} {
		writeReasonixFile(t, p, "{}\n")
	}
	files := ReasonixSessionFiles()
	got := make([]string, 0, len(files))
	for _, p := range files {
		got = append(got, filepath.Base(p))
	}
	slices.Sort(got)
	if strings.Join(got, ",") != "a.jsonl,b.jsonl" {
		t.Fatalf("session files = %v, want the two transcripts", got)
	}
	if !IsReasonixSession(filepath.Join(project, "b.jsonl")) || IsReasonixSession(filepath.Join(project, "b.guardian.jsonl")) {
		t.Error("the registry kind disagrees with the file list")
	}
	if IsReasonixSession(filepath.Join(global, "subagents", "a", "worker.jsonl")) {
		t.Error("a subagent log matches as a transcript")
	}
}

// The state root follows Reasonix's own chain: REASONIX_STATE_HOME, then
// REASONIX_HOME, then a relocation in config.toml, then the OS default.
func TestReasonixStateRootChain(t *testing.T) {
	home := t.TempDir()
	clearReasonixEnv(t, home)
	want := filepath.Join(home, ".reasonix")
	if runtime.GOOS == "windows" {
		want = filepath.Join(home, "AppData", "Roaming", "reasonix")
	}
	if got := ReasonixStateRoot(); got != want {
		t.Errorf("default root = %q, want %q", got, want)
	}

	moved := filepath.Join(home, "big-disk", "reasonix-state")
	cfg := mustJSON(t, moved) // a TOML basic string is a JSON string
	writeReasonixFile(t, filepath.Join(want, "config.toml"),
		"[ui]\nstate = \"ignored\"\n\n[storage]\nstate = "+string(cfg)+"\n")
	if got := ReasonixStateRoot(); got != moved {
		t.Errorf("relocated root = %q, want the [storage] state entry %q", got, moved)
	}

	pinned := filepath.Join(home, "pinned")
	t.Setenv("REASONIX_HOME", pinned)
	if got := ReasonixStateRoot(); got != pinned {
		t.Errorf("REASONIX_HOME root = %q, want %q", got, pinned)
	}
	state := filepath.Join(home, "state")
	t.Setenv("REASONIX_STATE_HOME", state)
	if got := ReasonixStateRoot(); got != state {
		t.Errorf("REASONIX_STATE_HOME root = %q, want %q", got, state)
	}
}

// Reasonix copies legacy sessions into its current store and leaves the
// originals, so a legacy file is read only when the current store lacks its id.
func TestReasonixLegacyRootIsReadWithoutDuplicates(t *testing.T) {
	home := t.TempDir()
	clearReasonixEnv(t, home)
	legacy := filepath.Join(home, "xdg", "reasonix")
	if runtime.GOOS == "windows" {
		legacy = filepath.Join(home, ".reasonix")
	}
	current := ReasonixStateRoot()
	writeReasonixFile(t, filepath.Join(current, "sessions", "imported.jsonl"), "{}\n")
	writeReasonixFile(t, filepath.Join(legacy, "sessions", "imported.jsonl"), "{}\n")
	writeReasonixFile(t, filepath.Join(legacy, "sessions", "old-only.jsonl"), "{}\n")

	files := ReasonixSessionFiles()
	got := make([]string, 0, len(files))
	for _, p := range files {
		rel, _ := filepath.Rel(home, p)
		got = append(got, filepath.ToSlash(rel))
	}
	slices.Sort(got)
	curRel, _ := filepath.Rel(home, filepath.Join(current, "sessions", "imported.jsonl"))
	oldRel, _ := filepath.Rel(home, filepath.Join(legacy, "sessions", "old-only.jsonl"))
	want := []string{filepath.ToSlash(curRel), filepath.ToSlash(oldRel)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("session files = %v, want %v", got, want)
	}

	t.Setenv("REASONIX_HOME", current)
	if roots := ReasonixRoots(); len(roots) != 1 {
		t.Errorf("roots = %v, want only the pinned home", roots)
	}
}

// rxLog writes a 1.x events.frames the way Reasonix's encoder does
// (internal/session/v4_codec.go:59-179): one zstd frame per record behind a
// 12-byte header, records in begin/event/end batches, the end carrying the
// SHA-256 of the records before it. zstd is the CLI, as in the reader.
type rxLog struct {
	t       *testing.T
	dir     string
	buf     bytes.Buffer
	seq     int
	batches int
}

type rxEvent struct {
	kind    string
	payload any
	// external puts the payload in the content store and the log holds a
	// payloadRef, as Reasonix does above 64 KiB.
	external bool
}

func newRxLog(t *testing.T, dir, id string, created time.Time, header map[string]any) *rxLog {
	t.Helper()
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	writeJSONDoc(t, filepath.Join(dir, "manifest.json"), map[string]any{
		"schemaVersion": 4, "codec": reasonixV4Codec, "storageRevision": 3,
		"contentRoot": "../.content-v1", "sessionId": id, "createdAt": created, "writerGeneration": 1,
	})
	if header != nil {
		writeJSONDoc(t, filepath.Join(dir, "header.json"), header)
	}
	return &rxLog{t: t, dir: dir}
}

func (l *rxLog) record(rec map[string]any) []byte {
	l.t.Helper()
	rec["schemaVersion"], rec["codec"] = 4, reasonixV4Codec
	raw, err := json.Marshal(rec)
	if err != nil {
		l.t.Fatal(err)
	}
	cmd := exec.CommandContext(l.t.Context(), "zstd", "-q", "-c")
	cmd.Stdin = bytes.NewReader(raw)
	z, err := cmd.Output()
	if err != nil {
		l.t.Fatalf("zstd: %v", err)
	}
	var h [12]byte
	copy(h[:], reasonixV4FrameMagic)
	binary.BigEndian.PutUint32(h[4:], uint32(len(z)))   //nolint:gosec // test frames are small
	binary.BigEndian.PutUint32(h[8:], uint32(len(raw))) //nolint:gosec // test frames are small
	l.buf.Write(h[:])
	l.buf.Write(z)
	return raw
}

// batch writes a committed batch; open leaves the end off, as a crash or a
// write still in flight does.
func (l *rxLog) batch(at time.Time, evs ...rxEvent) { l.write(at, true, evs) }
func (l *rxLog) open(at time.Time, evs ...rxEvent)  { l.write(at, false, evs) }

func (l *rxLog) write(at time.Time, commit bool, evs []rxEvent) {
	l.t.Helper()
	l.batches++
	commitID := "commit-" + string(rune('a'+l.batches)) //nolint:gosec // a handful of test batches
	digest := sha256.New()
	add := func(raw []byte) { digest.Write(raw); digest.Write([]byte{0}) }
	add(l.record(map[string]any{
		"recordType": "batch/begin", "commitId": commitID, "operationId": "op-" + commitID, "operationHash": "x",
		"firstSeq": l.seq + 1, "eventCount": len(evs), "writerGeneration": 1, "createdAt": at,
	}))
	for _, ev := range evs {
		l.seq++
		body, err := json.Marshal(ev.payload)
		if err != nil {
			l.t.Fatal(err)
		}
		event := map[string]any{"id": "ev" + string(rune('a'+l.seq)), "seq": l.seq, "kind": ev.kind} //nolint:gosec // a handful of test events
		if ev.external {
			sum := sha256.Sum256(body)
			d := hex.EncodeToString(sum[:])
			writeReasonixFile(l.t, filepath.Join(filepath.Dir(l.dir), ".content-v1", "objects", d[:2], d[2:4], d), string(body))
			event["payloadRef"] = map[string]any{"digest": d, "bytes": len(body), "mediaType": "application/json"}
		} else {
			event["payload"] = body // []byte marshals as base64, as Reasonix's does
		}
		add(l.record(map[string]any{"recordType": "batch/event", "event": event}))
	}
	if commit {
		l.record(map[string]any{
			"recordType": "batch/end", "commitId": commitID, "firstSeq": l.seq - len(evs) + 1,
			"eventCount": len(evs), "sha256": hex.EncodeToString(digest.Sum(nil)),
		})
	}
}

func (l *rxLog) save() string {
	l.t.Helper()
	p := filepath.Join(l.dir, "events.frames")
	writeReasonixFile(l.t, p, l.buf.String())
	return p
}

func rxMsg(m map[string]any) rxEvent {
	return rxEvent{kind: "message/complete", payload: map[string]any{"message": m}}
}

const rxSessionContext = "<session-context version=\"1\">\n## Environment\n\n- OS: linux/amd64\n\n## Workspace\n\nCurrent workspace: \"/workspace/demo\"\n</session-context>"

func rxRoles(msgs []modelMessage) map[string][]string {
	out := map[string][]string{}
	for _, m := range msgs {
		out[m.Role] = append(out[m.Role], m.Text)
	}
	return out
}

type modelMessage = struct {
	Role, Text string
	Time       time.Time
}

// The CLI's store: a session directory under projects/<slug>/sessions-v4,
// no header.json, the workspace named only in the host's session-context
// message, and one batch per step of a turn.
func TestParseReasonixV4Session(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	id := "839559938275e9a3ebde5a804aa24edd"
	dir := filepath.Join(root, "projects", "-workspace-demo", "sessions-v4", id)
	t0 := time.Date(2026, 9, 26, 22, 24, 59, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	l := newRxLog(t, dir, id, t0, nil)
	l.batch(at(10),
		rxMsg(map[string]any{"role": "system", "id": "m0", "content": "You are Reasonix, a coding agent."}),
		rxEvent{kind: "session/config", payload: map[string]any{"modelRef": "deepseek/deepseek-chat"}},
	)
	l.batch(at(20), rxEvent{kind: "turn/start", payload: map[string]any{"status": "in_progress"}})
	l.batch(at(30),
		rxMsg(map[string]any{"role": "user", "id": "m1", "origin": "host", "content": rxSessionContext}),
		rxMsg(map[string]any{"role": "user", "id": "m2", "origin": "user", "content": "go build fails in the relay package", "raw_content": "go build fails in the relay package", "createdAt": at(30).UnixMilli()}),
	)
	l.batch(at(40), rxMsg(map[string]any{"role": "assistant", "id": "m3", "content": "draft that gets replaced"}))
	l.batch(at(50), rxEvent{kind: "message/upsert", payload: map[string]any{"message": map[string]any{
		"role": "assistant", "id": "m3", "content": "Building it to see the error.",
		"tool_calls": []any{toolCall("call_1", "bash", map[string]any{"command": "go build ./relay"})},
	}}})
	l.batch(at(60), rxMsg(map[string]any{"role": "tool", "id": "m4", "tool_call_id": "call_1", "name": "bash",
		"content":        "error: exit status 1\nrelay/queue.go:12: undefined: frame",
		"tool_execution": map[string]any{"kind": "shell", "state": "failed", "exitCode": 1}}))
	l.batch(at(65), rxMsg(map[string]any{"role": "assistant", "id": "m5", "content": "an interrupted partial answer"}))
	l.batch(at(66), rxEvent{kind: "message/retract", payload: map[string]any{"messageIds": []string{"m5"}, "reason": "interrupted-turn-cleanup"}})
	l.batch(at(67), rxMsg(map[string]any{"role": "tool", "id": "m6", "local_only": true, "content": "steer the host kept"}))
	l.batch(at(70), rxMsg(map[string]any{"role": "assistant", "id": "m7", "content": "queue.go uses frame before it is declared."}))
	l.batch(at(80), rxEvent{kind: "session/title", payload: map[string]any{"title": "relay build error"}})
	l.open(at(90), rxMsg(map[string]any{"role": "user", "id": "m8", "origin": "user", "content": "a turn still being written"}))
	path := l.save()

	if !IsReasonixSession(path) || !slices.Contains(ReasonixSessionFiles(), path) {
		t.Fatalf("events.frames is not listed: %v", ReasonixSessionFiles())
	}
	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.ID != id || s.Title != "relay build error" || s.Project != "workspace/demo" {
		t.Errorf("id/title/project = %q/%q/%q", s.ID, s.Title, s.Project)
	}
	msgs := make([]modelMessage, 0, len(s.Messages))
	for _, m := range s.Messages {
		msgs = append(msgs, modelMessage{m.Role, m.Text, m.Time})
	}
	by := rxRoles(msgs)
	if got := by["user"]; len(got) != 1 || got[0] != "go build fails in the relay package" {
		t.Errorf("user = %q, want the typed prompt alone (host context and the open batch left out)", got)
	}
	if got := by["assistant"]; strings.Join(got, "|") != "Building it to see the error.|queue.go uses frame before it is declared." {
		t.Errorf("assistant = %q, want the upserted text and no retracted one", got)
	}
	if got := by[RoleCommand]; len(got) != 1 || got[0] != "$ go build ./relay  → exit 1" {
		t.Errorf("commands = %q, want the call with its exit", got)
	}
	if got := by[RoleToolOutput]; len(got) != 1 || !strings.Contains(got[0], "undefined: frame") {
		t.Errorf("tool output = %q", got)
	}
	for _, m := range msgs {
		if strings.Contains(m.Text, "session-context") || strings.Contains(m.Text, "You are Reasonix") || strings.Contains(m.Text, "steer the host") {
			t.Errorf("%q was indexed as something someone said", m.Text)
		}
	}
	if !msgs[0].Time.Equal(at(30)) {
		t.Errorf("first turn at %v, want its createdAt", msgs[0].Time)
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Time.Before(msgs[i-1].Time) {
			t.Fatalf("record %d goes back in time", i)
		}
	}
	if !s.Started.Equal(t0) || !s.Updated.Equal(at(80)) {
		t.Errorf("started/updated = %v/%v, want the manifest and the last commit", s.Started, s.Updated)
	}
	// The context's workspace has the store's slug, so --resume finds the
	// session from there.
	if ws := ReasonixWorkspace(path); filepath.Separator == '/' && ws != "/workspace/demo" {
		t.Errorf("resume dir = %q", ws)
	}
}

// A batch whose checksum does not match is not a commit, and a damaged frame
// ends the read.
func TestReasonixV4SkipsBadBatches(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	dir := filepath.Join(root, "sessions-v4", "s1")
	l := newRxLog(t, dir, "s1", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), nil)
	l.batch(time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC), rxMsg(map[string]any{"role": "user", "id": "u1", "content": "kept"}))
	l.open(time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC), rxMsg(map[string]any{"role": "user", "id": "u2", "content": "tampered"}))
	l.record(map[string]any{"recordType": "batch/end", "commitId": "commit-c", "eventCount": 1, "sha256": strings.Repeat("0", 64)})
	l.batch(time.Date(2026, 9, 1, 0, 0, 3, 0, time.UTC), rxMsg(map[string]any{"role": "user", "id": "u3", "content": "after the bad batch"}))
	l.buf.WriteString("RX4Fgarbage-that-is-no-frame")
	path := l.save()
	ss, _ := ParseReasonixFile(path)
	if len(ss) != 1 || len(ss[0].Messages) != 2 || ss[0].Messages[0].Text != "kept" || ss[0].Messages[1].Text != "after the bad batch" {
		t.Fatalf("sessions = %+v, want the two batches whose checksums hold", ss)
	}
}

// history/replace and legacy/import swap the whole list; a message that
// survives keeps its clock. A payload over 64 KiB is read from .content-v1.
func TestReasonixV4ReplaceAndPayloadRef(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	dir := filepath.Join(root, "projects", "-workspace-demo", "sessions-v4", "s2")
	t0 := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	l := newRxLog(t, dir, "s2", t0, nil)
	l.batch(t0.Add(time.Second),
		rxMsg(map[string]any{"role": "user", "id": "a", "content": "first question"}),
		rxMsg(map[string]any{"role": "assistant", "id": "b", "content": "dropped by the replace"}),
	)
	big := strings.Repeat("stack frame ", 7000)
	l.batch(t0.Add(2*time.Second), rxEvent{kind: "history/replace", external: true, payload: map[string]any{
		"reason": "fresh-history-adopt",
		"messages": []any{
			map[string]any{"role": "user", "id": "a", "content": "first question"},
			map[string]any{"role": "assistant", "id": "c", "content": big},
		},
	}})
	path := l.save()
	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d", err, len(ss))
	}
	m := ss[0].Messages
	if len(m) != 2 || m[0].Text != "first question" || !strings.HasPrefix(m[1].Text, "stack frame stack frame") {
		t.Fatalf("messages = %d (%.40q), want the replaced list with the external payload", len(m), m)
	}
	if !m[0].Time.Equal(t0.Add(time.Second)) || !m[1].Time.Equal(t0.Add(2*time.Second)) {
		t.Errorf("times = %v, %v", m[0].Time, m[1].Time)
	}
}

// The desktop's store is by-id; its header.json names the workspace, and the
// registry does when there is no header.
func TestReasonixV4Desktop(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	store := filepath.Join(root, "desktop-sessions-v5", "by-id")
	t0 := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	l := newRxLog(t, filepath.Join(store, "desktop-a1"), "desktop-a1", t0, map[string]any{"schemaVersion": 1, "sessionId": "desktop-a1", "cwd": "/workspace/app", "origin": "new"})
	l.batch(t0, rxMsg(map[string]any{"role": "user", "id": "u", "origin": "user", "content": "why is the sidebar blank"}))
	withHeader := l.save()
	l = newRxLog(t, filepath.Join(store, "desktop-b2"), "desktop-b2", t0, nil)
	l.batch(t0, rxMsg(map[string]any{"role": "user", "id": "u", "origin": "user", "content": "rename the tab"}))
	noHeader := l.save()
	writeJSONDoc(t, filepath.Join(root, "desktop", "workspace-state-v1.json"), map[string]any{
		"version": 1, "workspaces": map[string]any{"project-x": map[string]any{"id": "project-x", "root": "/workspace/site", "sessionIds": []string{"desktop-b2"}}},
	})
	for p, want := range map[string]string{withHeader: "workspace/app", noHeader: "workspace/site"} {
		ss, err := ParseReasonixFile(p)
		if err != nil || len(ss) != 1 || ss[0].Project != want {
			t.Errorf("%s: %v %+v, want project %q", filepath.Base(filepath.Dir(p)), err, ss, want)
		}
		if ReasonixStore(p) != "desktop" || !IsReasonixSession(p) {
			t.Errorf("%s is not a desktop session", p)
		}
	}
}

// Reasonix copies without deleting. deja reads one copy of each session: the
// desktop's over the CLI's, the CLI's v4 over JSONL — by id, by the source a
// migration names in its manifest, and by the desktop's source mappings. A
// fork names its parent as its source too, and the parent is still read.
func TestReasonixStoresDedupe(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	t0 := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	proj := filepath.Join(root, "projects", "-workspace-demo")
	jsonl := func(name string) string {
		p := filepath.Join(proj, "sessions", name+".jsonl")
		writeJSONLines(t, p, map[string]any{"role": "user", "content": name})
		return p
	}
	v4 := func(store, id string, manifest map[string]any) string {
		l := newRxLog(t, filepath.Join(store, id), id, t0, nil)
		l.batch(t0, rxMsg(map[string]any{"role": "user", "id": "u", "content": id}))
		p := l.save()
		if manifest != nil {
			manifest["codec"], manifest["sessionId"] = reasonixV4Codec, id
			writeJSONDoc(t, filepath.Join(store, id, "manifest.json"), manifest)
		}
		return p
	}
	cli := filepath.Join(proj, "sessions-v4")
	byID := filepath.Join(root, "desktop-sessions-v5", "by-id")

	mirrored := jsonl("20260904-mirror")
	mirror := v4(cli, "20260904-mirror", nil)
	migratedSrc := jsonl("20260904-migrated")
	migrated := v4(cli, "0f0f0f0f", map[string]any{"source": map[string]any{"path": migratedSrc}})
	plain := jsonl("20260904-plain")
	parent := v4(cli, "aaaa1111", nil)
	fork := v4(cli, "bbbb2222", map[string]any{"inheritedEventCount": 1, "source": map[string]any{"path": filepath.Dir(parent)}})
	imported := v4(cli, "cccc3333", nil)
	copied := v4(cli, "dddd4444", nil)
	desktop := v4(byID, "desktop-imported", nil)
	sameID := v4(byID, "dddd4444", nil)
	writeJSONDoc(t, filepath.Join(root, "desktop", "workspace-state-v1.json"), map[string]any{
		"sourceMappings": map[string]any{"k": map[string]any{"path": filepath.Dir(imported), "sessionId": "desktop-imported"}},
	})

	got := ReasonixSessionFiles()
	slices.Sort(got)
	want := []string{mirror, migrated, plain, parent, fork, desktop, sameID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("session files =\n%v\nwant\n%v", got, want)
	}
	for _, p := range []string{mirrored, migratedSrc, imported, copied} {
		if !slices.Contains(ReasonixSidecarFiles(), p) {
			t.Errorf("%s is neither read nor placed", p)
		}
	}
}

// Without zstd a frames file cannot be read, and the harness row says why —
// but only on a machine that has one.
func TestReasonixV4WithoutZstd(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	writeJSONLines(t, filepath.Join(root, "sessions", "plain.jsonl"), map[string]any{"role": "user", "content": "hi"})
	t.Setenv("PATH", t.TempDir())
	if r := SkipReason("reasonix"); r != "" {
		t.Errorf("JSONL-only store: skip reason %q, want none", r)
	}
	dir := filepath.Join(root, "sessions-v4", "s3")
	writeJSONDoc(t, filepath.Join(dir, "manifest.json"), map[string]any{"codec": reasonixV4Codec, "sessionId": "s3"})
	var h [12]byte
	copy(h[:], reasonixV4FrameMagic)
	binary.BigEndian.PutUint32(h[4:], 4)
	binary.BigEndian.PutUint32(h[8:], 4)
	writeReasonixFile(t, filepath.Join(dir, "events.frames"), string(h[:])+"zstd")
	if r := SkipReason("reasonix"); r != "zstd CLI not found" {
		t.Errorf("skip reason = %q", r)
	}
	if _, err := ParseReasonixFile(filepath.Join(dir, "events.frames")); !errors.Is(err, errReasonixNoZstd) {
		t.Errorf("parse error = %v, want the missing tool named", err)
	}
}

// Reasonix's slug: separators and colons to dashes, and a cut with a hash
// past 255 bytes (config/paths.go:567-597).
func TestReasonixWorkspaceSlug(t *testing.T) {
	if got := reasonixWorkspaceSlug("/tmp/rxstand/work"); filepath.Separator == '/' && got != "-tmp-rxstand-work" {
		t.Errorf("slug = %q", got)
	}
	long := reasonixWorkspaceSlug("/" + strings.Repeat("a", 300))
	if len(long) != 255 || !strings.HasPrefix(long, "-aaa") {
		t.Errorf("long slug = %d bytes %q", len(long), long[len(long)-20:])
	}
}

// registry.json is checked against the loader list; the human index next to it
// was not checked against anything, and had drifted six harnesses behind — the
// pages for cline, copilot, hermes, kimi, openclaw and roo existed and nothing
// linked to them. A reference nobody can reach is worse than a missing one,
// because it looks complete.
func TestRegistryIndexLinksEveryHarnessAndEveryPage(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "docs", "registry")

	b, err := os.ReadFile(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"harnesses"`
	}
	if err = json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}

	idx, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	index := string(idx)

	for _, h := range reg.Harnesses {
		if !strings.Contains(index, "["+h.DisplayName+"]") {
			t.Errorf("the registry index has no entry for %s (%s)", h.DisplayName, h.ID)
		}
	}

	pages, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		name := filepath.Base(page)
		if name == "README.md" {
			continue
		}
		if !strings.Contains(index, "("+name+")") {
			t.Errorf("docs/registry/%s exists but nothing links to it", name)
		}
	}
}

// mcpToolNameRE is how a page would name a tool a client shows: the prefixed
// form an MCP client prints in its tool list.
var mcpToolNameRE = regexp.MustCompile("mcp__deja__([a-z_]+)")

// deja's MCP server advertises one tool, `deja`, with a mode argument. The dsh
// page still described the shape from before that collapse — "dsh lists
// mcp__deja__recall, recall_context, remember, blame, fix and how itself" —
// so a reader following it looked for five tools their client never shows and
// could not tell whether the install had worked (#3343).
func TestNoRegistryPageNamesAToolTheServerDoesNotAdvertise(t *testing.T) {
	// The .html beside each page is what a reader and a search engine open, and
	// it is generated from the .md — a page fixed in one and not rebuilt in the
	// other keeps the stale claim where it is actually read (review of #3343).
	var files []string
	for _, dir := range []string{
		filepath.Join("..", "..", "docs", "registry"),
		filepath.Join("..", "..", "docs", "guide"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(e.Name(), ".md") || strings.HasSuffix(e.Name(), ".html") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	files = append(files, filepath.Join("..", "..", "README.md"))
	for _, path := range files {
		e := struct{ name string }{filepath.Base(path)}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range mcpToolNameRE.FindAllSubmatch(b, -1) {
			t.Errorf("%s names the tool %q; the server advertises one tool, deja, with a mode", e.name, string(m[0]))
		}
		if bytes.Contains(b, []byte("six tools")) {
			t.Errorf("%s says \"six tools\"; there is one tool with six modes", e.name)
		}
	}
}

var lastVerifiedRE = regexp.MustCompile(`\*\*Last verified:\*\* (\S+)`)

// Each harness has a reference page beside registry.json, and both carry the
// date the format was last checked against a real store. Only the registry's
// copy is read by anything, so the pages drifted: three sat ten days behind and
// seven had no date at all, while the pages are what a reader looking up a
// harness actually opens.
func TestRegistryPagesAgreeWithTheRegistry(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	registry := readFormatRegistry(t, filepath.Join(root, "docs", "registry", "registry.json"))
	for _, entry := range registry.Harnesses {
		t.Run(entry.ID, func(t *testing.T) {
			path, ok := registryPagePath(root, entry.ID)
			if !ok {
				t.Skipf("no reference page for %s", entry.ID)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			m := lastVerifiedRE.FindSubmatch(b)
			if m == nil {
				t.Fatalf("%s carries no **Last verified** line; the registry says %s",
					filepath.Base(path), entry.LastVerified)
			}
			if got := string(m[1]); got != entry.LastVerified {
				t.Errorf("%s says %s, registry.json says %s — one of them is telling "+
					"a reader the format was checked when it was not",
					filepath.Base(path), got, entry.LastVerified)
			}
		})
	}
}

// registryPagePath finds the page for an id, which is named after the harness
// rather than keyed: claude is claude-code.md.
func registryPagePath(root, id string) (string, bool) {
	dir := filepath.Join(root, "docs", "registry")
	for _, name := range []string{id + ".md", id + "-code.md", id + "-cli.md"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

type formatRegistry struct {
	SchemaVersion int                   `json:"schema_version"`
	Harnesses     []formatRegistryEntry `json:"harnesses"`
}

type formatRegistryEntry struct {
	ID           string   `json:"id"`
	StorePaths   []string `json:"store_paths"`
	FormatKind   string   `json:"format_kind"`
	FixturePaths []string `json:"fixture_paths"`
	ParserSource string   `json:"parser_source"`
	LastVerified string   `json:"last_verified"`
}

func TestFormatRegistryConformance(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{
		"AIDER_CHAT_HISTORY_FILE", "CLAUDE_CONFIG_DIR", "CODEX_HOME",
		"CURSOR_CONFIG_DIR", "DEJA_AIDER_ROOTS", "DEJA_AMP_ROOT", "DEJA_ANTIGRAVITY_ROOT",
		"DEJA_CLAUDE_ROOT", "DEJA_CODEX_ROOT", "DEJA_TRAE_ROOT", "TRAE_HOME", "TRAECLI_HOME", "DEJA_CURSOR_CLI_ROOT",
		"DEJA_CURSOR_ROOT", "DEJA_GEMINI_ROOT", "DEJA_GROK_ROOT", "DEJA_GOOSE_ROOT", "DEJA_GOOSE_DB",
		"DEJA_PI_ROOT", "DEJA_OMP_ROOT", "DEJA_QWEN_ROOT", "DEJA_QODER_ROOT", "DEJA_KIMI_ROOT", "KIMI_CODE_HOME",
		"DEJA_CLINE_ROOT", "DEJA_CLINE_ROOTS", "CLINE_DIR", "CLINE_DATA_DIR",
		"CLINE_SESSION_DATA_DIR", "CLINE_MCP_SETTINGS_PATH", "DEJA_ROO_ROOTS",
		"DEJA_COPILOT_CHAT_ROOTS",
		"DEJA_OPENCLAW_ROOT", "OPENCLAW_STATE_DIR",
		"DEJA_INCLUDE_SUBAGENTS", "DEJA_OPENCODE_DB", "GEMINI_CLI_HOME",
		"GROK_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
		"DEJA_ZED_ROOT", "DEJA_ZED_DB", "FLATPAK_XDG_DATA_HOME",
		"DEJA_REASONIX_ROOT", "REASONIX_HOME", "REASONIX_STATE_HOME",
		"DEJA_MUSE_ROOTS",
		"DEJA_CODEBUDDY_ROOTS", "CODEBUDDY_CONFIG_DIR", "WORKBUDDY_CONFIG_DIR",
		"DEJA_NOTES_FILE",
		"DEJA_JUNIE_ROOT", "JUNIE_HOME", "DEJA_JETBRAINS_ROOT",
		"DEJA_WINDSURF_ROOT", "DEJA_MCODE_ROOT",
		"DEJA_MIMO_DB", "DEJA_MIMO_CONFIG_DIR", "MIMOCODE_HOME", "MIMOCODE_CONFIG_DIR", "MIMOCODE_DB",
		"DEJA_OMNIGENT_ROOT", "DEJA_OMNIGENT_DB", "OMNIGENT_DATA_DIR",
		"DEJA_ORCA_ROOT", "DEJA_ORCA_DB",
		"DEJA_CLAWCODE_ROOT", "DEJA_CLAWCODE_ROOTS", "CLAW_CONFIG_HOME",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "claude"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(home, "gemini"))

	registry := readFormatRegistry(t, filepath.Join(root, "docs", "registry", "registry.json"))
	if registry.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", registry.SchemaVersion)
	}

	registered := make([]string, 0, len(registry.Harnesses))
	seen := map[string]bool{}
	for _, entry := range registry.Harnesses {
		if entry.ID == "" || seen[entry.ID] {
			t.Fatalf("invalid or duplicate harness id %q", entry.ID)
		}
		seen[entry.ID] = true
		registered = append(registered, entry.ID)
		validateRegistryEntry(t, root, entry)

		t.Run(entry.ID, func(t *testing.T) {
			for _, fixture := range entry.FixturePaths {
				fixturePath := filepath.Join(root, filepath.FromSlash(fixture))
				sessions := parseRegistryFixture(t, entry.ID, fixturePath)
				validateRegistrySessions(t, entry.ID, sessions)
			}
		})
	}

	loaders := registryHarnessIDs()
	sort.Strings(registered)
	if strings.Join(registered, ",") != strings.Join(loaders, ",") {
		t.Fatalf("format registry harnesses = %v, source registry = %v", registered, loaders)
	}
}

// registryHarnessIDs lists the real harnesses in the source registry (excluding
// the notes pseudo-source) to compare against the published format registry.
func registryHarnessIDs() []string {
	var ids []string
	for _, h := range Registry() {
		if h.Name == "deja" {
			continue
		}
		ids = append(ids, h.Name)
	}
	sort.Strings(ids)
	return ids
}

func readFormatRegistry(t *testing.T, path string) formatRegistry {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var registry formatRegistry
	if err := json.Unmarshal(b, &registry); err != nil {
		t.Fatal(err)
	}
	return registry
}

func validateRegistryEntry(t *testing.T, root string, entry formatRegistryEntry) {
	t.Helper()
	if len(entry.StorePaths) == 0 || entry.FormatKind == "" || len(entry.FixturePaths) == 0 || entry.ParserSource == "" {
		t.Fatalf("incomplete registry entry for %q: %#v", entry.ID, entry)
	}
	if _, err := time.Parse("2006-01-02", entry.LastVerified); err != nil {
		t.Fatalf("%s last_verified: %v", entry.ID, err)
	}
	paths := append(append([]string(nil), entry.FixturePaths...), entry.ParserSource)
	for _, path := range paths {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("%s path %q: %v", entry.ID, path, err)
		}
	}
}

func parseRegistryFixture(t *testing.T, id, path string) []model.Session {
	t.Helper()
	return parseRegistryFixtureIn(t, id, path, t.TempDir())
}

// parseRegistryFixtureIn is parseRegistryFixture with the working directory
// given rather than made, so the hostile-path test can hand it a directory
// whose name carries a space, a percent octet and a non-ASCII character — the
// shape a store is under on somebody else's machine, and the one the Copilot
// Chat reader was wrong about until a contributor hit it (#3498, #3505).
func parseRegistryFixtureIn(t *testing.T, id, path, work string) []model.Session {
	t.Helper()
	var (
		sessions []model.Session
		err      error
	)
	switch id {
	case "claude":
		sessions, err = ParseClaudeFile(path)
	case "qoder":
		sessions, err = ParseQoderFile(path)
	case "codex":
		sessions, err = ParseCodexRollout(path)
	case "trae":
		sessions, err = ParseTraeRollout(path)
	case "kimi":
		sessions, err = ParseKimiFile(path)
	case "cline":
		sessions, err = ParseClineFile(path)
	case "roo":
		sessions, err = ParseRooTask(path)
	case "cherrystudio":
		sessions, err = ParseCherryStudioFile(path)
	case "kilocode":
		// Two stores, both in a format another harness here already uses: the
		// extension's task files and the CLI's OpenCode-schema database.
		if strings.HasSuffix(path, ".sql") {
			if !SQLite3Available() {
				t.Skip("sqlite3 not installed")
			}
			sql, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			db := filepath.Join(work, "kilo.db")
			if out, runErr := exec.CommandContext(t.Context(), "sqlite3", db, string(sql)).CombinedOutput(); runErr != nil {
				t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
			}
			sessions, err = ParseKiloDB(db)
		} else {
			sessions, err = ParseKiloTask(path)
		}
	case "senpi":
		sessions, err = ParseSenpiFile(path)
	case "gjc":
		sessions, err = ParseGjcFile(path)
	case "kimchi":
		sessions, err = ParseKimchiFile(path)
	case "codewhale":
		sessions, err = ParseCodeWhaleFile(path)
	case "muse":
		sessions, err = ParseMuseFile(path)
	case "mcode":
		sessions, err = ParseMcodeFile(path)
	case "codebuddy":
		sessions, err = ParseCodeBuddyFile(path)
	case "codearts":
		sessions, err = ParseCodeArtsFile(path)
	case "doubao":
		sessions, err = ParseDoubaoDir(path)
	case "reasonix":
		// The 1.x fixture is zstd frames, which only the CLI reads.
		if filepath.Base(path) == "events.frames" && !ZstdAvailable() {
			t.Skip("zstd not installed")
		}
		sessions, err = ParseReasonixFile(path)
	case "clawcode":
		sessions, err = ParseClawCodeFile(path)
	case "commandcode":
		sessions, err = ParseCommandCodeFile(path)
	case "zcode":
		// The transcripts, and the CLI's OpenCode-schema database.
		if strings.HasSuffix(path, ".sql") {
			if !SQLite3Available() {
				t.Skip("sqlite3 not installed")
			}
			sql, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			db := filepath.Join(work, "db.sqlite")
			build := exec.CommandContext(t.Context(), "sqlite3", db)
			build.Stdin = strings.NewReader(string(sql))
			if out, runErr := build.CombinedOutput(); runErr != nil {
				t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
			}
			sessions, err = ParseZCodeDB(db)
		} else {
			sessions, err = ParseZCodeFile(path)
		}
	case "kiro":
		// One store per client: the CLI's pair under cli/ and the IDE's
		// per-workspace session directory.
		if filepath.Base(path) == "messages.jsonl" {
			sessions, err = ParseKiroIDEFile(path)
		} else {
			sessions, err = ParseKiroCLIFile(path)
		}
	case "continue":
		sessions, err = ParseContinueFile(path)
	case "opencode":
		// Two stores under one harness: the database, and the per-session diff
		// files beside it (#3791). The fixture's own name says which it is,
		// the way the reader does.
		if strings.HasSuffix(filepath.Base(path), ".json") {
			sessions, err = ParseOpencodeDiff(path)
			break
		}
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		// Named after the fixture: the 1.x and 2.x fixtures are two stores,
		// and one file holding both schemas is a store neither reader
		// describes.
		db := filepath.Join(work, strings.TrimSuffix(filepath.Base(path), ".sql")+".db")
		if out, runErr := exec.CommandContext(t.Context(), "sqlite3", db, string(sql)).CombinedOutput(); runErr != nil {
			t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
		}
		sessions, err = ParseOpencodeDB(db)
	case "aider":
		sessions, err = ParseAiderFile(path)
	case "amp":
		sessions, err = ParseAmpFile(path)
	case "gemini":
		sessions, err = ParseGeminiFile(path)
	case "cursor":
		sessions, err = ParseCursorTranscript(path)
	case "antigravity":
		sessions, err = ParseAntigravityFile(path)
	case "grok":
		sessions, err = ParseGrokFile(path)
	case "goose":
		if strings.HasSuffix(path, ".sql") {
			if !SQLite3Available() {
				t.Skip("sqlite3 not installed")
			}
			sql, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			db := filepath.Join(work, "goose.db")
			if out, runErr := exec.CommandContext(t.Context(), "sqlite3", db, string(sql)).CombinedOutput(); runErr != nil {
				t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
			}
			sessions, err = ParseGooseDB(db)
		} else {
			sessions, err = ParseGooseFile(path)
		}
	case "openclaw":
		if strings.HasSuffix(path, ".sql") {
			if !SQLite3Available() {
				t.Skip("sqlite3 not installed")
			}
			sql, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			// agents/<agent>/agent/openclaw-agent.sqlite: the agent id is read
			// off the path, so the fixture goes in at the same depth.
			db := filepath.Join(work, "main", "agent", "openclaw-agent.sqlite")
			if mkErr := os.MkdirAll(filepath.Dir(db), 0o755); mkErr != nil {
				t.Fatal(mkErr)
			}
			cmd := exec.CommandContext(t.Context(), "sqlite3", db)
			cmd.Stdin = strings.NewReader(string(sql))
			if out, runErr := cmd.CombinedOutput(); runErr != nil {
				t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
			}
			sessions, err = ParseOpenClawDB(db)
		} else {
			sessions, err = ParseOpenClawFile(path)
		}
	case "crush":
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		// <project>/.crush/crush.db: the project is the directory the store
		// sits under, so the fixture goes in at the same depth or the parser
		// names the wrong one.
		db := filepath.Join(work, "demo", ".crush", "crush.db")
		if mkErr := os.MkdirAll(filepath.Dir(db), 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}
		cmd := exec.CommandContext(t.Context(), "sqlite3", db)
		cmd.Stdin = strings.NewReader(string(sql))
		if out, runErr := cmd.CombinedOutput(); runErr != nil {
			t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
		}
		sessions, err = ParseCrushDB(db)
	case "hermes":
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		db := filepath.Join(work, "state.db")
		if out, runErr := exec.CommandContext(t.Context(), "sqlite3", db, string(sql)).CombinedOutput(); runErr != nil {
			t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
		}
		sessions, err = ParseHermesDB(db)
	case "zed":
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		db := filepath.Join(work, "threads.db")
		// On stdin rather than as an argument: this fixture documents its
		// opaque blob in a leading `--` comment, and the sqlite3 CLI reads a
		// leading `--` as an option.
		build := exec.CommandContext(t.Context(), "sqlite3", db)
		build.Stdin = strings.NewReader(string(sql))
		if out, runErr := build.CombinedOutput(); runErr != nil {
			t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
		}
		// Without zstd the fixture's compressed thread is skipped and its
		// uncompressed one still parses, which is the degradation the harness
		// is documented to have rather than a reason to skip the row.
		sessions, err = ParseZedDB(db)
	case "devin":
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		// cli/sessions.db under the data dir: DevinSessionDir reads the
		// working directory off the same file, so the fixture sits at its
		// real depth.
		db := filepath.Join(work, "cli", "sessions.db")
		if mkErr := os.MkdirAll(filepath.Dir(db), 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}
		build := exec.CommandContext(t.Context(), "sqlite3", db)
		build.Stdin = strings.NewReader(string(sql))
		if out, runErr := build.CombinedOutput(); runErr != nil {
			t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
		}
		sessions, err = ParseDevinDB(db)
	case "qwen":
		sessions, err = ParseQwenFile(path)
	case "pi":
		sessions, err = ParsePiFile(path)
	case "omp":
		sessions, err = ParseOmpFile(path)
	case "prime":
		sessions, err = ParsePrimeFile(path)
	case "copilot":
		sessions, err = ParseCopilotFile(path)
	case "copilot-chat":
		sessions, err = ParseCopilotChatFile(path)
	case "junie":
		sessions, err = ParseJunieFile(path)
	case "jetbrains":
		sessions, err = ParseJetBrainsFile(path)
	case "windsurf":
		sessions, err = ParseWindsurfFile(path)
	case "mimo":
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		db := filepath.Join(work, "mimocode.db")
		build := exec.CommandContext(t.Context(), "sqlite3", db)
		build.Stdin = strings.NewReader(string(sql))
		if out, runErr := build.CombinedOutput(); runErr != nil {
			t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
		}
		sessions, err = ParseMimoDB(db)
	case "omnigent":
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		db := filepath.Join(work, "chat.db")
		build := exec.CommandContext(t.Context(), "sqlite3", db)
		build.Stdin = strings.NewReader(string(sql))
		if out, runErr := build.CombinedOutput(); runErr != nil {
			t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
		}
		sessions, err = ParseOmnigentDB(db)
	case "orca":
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		db := filepath.Join(work, "agent-session-journal.db")
		build := exec.CommandContext(t.Context(), "sqlite3", db)
		build.Stdin = strings.NewReader(string(sql))
		if out, runErr := build.CombinedOutput(); runErr != nil {
			t.Fatalf("create sqlite fixture: %v: %s", runErr, out)
		}
		sessions, err = ParseOrcaDB(db)
	case "deepseek":
		// The fixture is stored raw: the harness writes zstd frames by default,
		// and a registry fixture that needs an external tool to read cannot be
		// checked on a machine that lacks it.
		sessions, err = ParseDeepSeekFile(path)
	default:
		t.Fatalf("no conformance parser for %q", id)
	}
	if err != nil {
		t.Fatalf("parse %s fixture: %v", id, err)
	}
	return sessions
}

// registryFixturesWithCalls are the registry fixtures whose tool calls are
// read into work records.
var registryFixturesWithCalls = map[string]bool{"clawcode": true, "deepseek": true, "continue": true, "trae": true, "muse": true, "codebuddy": true, "devin": true, "junie": true, "jetbrains": true, "kimi": true, "mcode": true, "gemini": true, "omnigent": true, "orca": true}

func validateRegistrySessions(t *testing.T, id string, sessions []model.Session) {
	t.Helper()
	if len(sessions) == 0 {
		t.Fatalf("%s fixture produced no sessions", id)
	}
	for _, session := range sessions {
		if session.Harness != id || session.ID == "" || session.Project == "" || session.Path == "" {
			t.Fatalf("%s fixture produced invalid session: %#v", id, session)
		}
		if session.Started.IsZero() || session.Updated.IsZero() || len(session.Messages) == 0 {
			t.Fatalf("%s fixture produced incomplete session: %#v", id, session)
		}
		for _, message := range session.Messages {
			// tool-output is a role the index stores and the search filters by
			// (retrieval.go: roleToolOutput); a fixture whose harness records
			// what a tool printed should be able to show it. The work records a
			// tool call leaves — files, command, edit, wrote — are allowed only
			// for a fixture known to carry calls, so a parser that starts
			// emitting them from prose still fails here.
			role := message.Role
			work := registryFixturesWithCalls[id] &&
				(role == RoleFiles || role == RoleCommand || role == RoleEdit || role == RoleWrote)
			// A compaction summary is allowed where the harness wrote one; the
			// fixtures that carry one are pinned in compaction_summary_fixtures_test.go.
			if (role != "user" && role != "assistant" && role != "tool-output" && role != RoleSummary && !work) ||
				strings.TrimSpace(message.Text) == "" || message.Time.IsZero() {
				t.Fatalf("%s fixture produced invalid message: %#v", id, message)
			}
		}
	}
}

func TestEveryRegisteredHarnessKindHandlesHostileInputs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	payloads := []struct {
		name string
		ext  string
		data []byte
	}{
		{"empty", ".jsonl", nil},
		{"whitespace", ".jsonl", []byte("   \n\t\r\n  ")},
		{"truncated_json", ".json", []byte("\x00\xff{\"broken\": [1, 2,")},
		{"null_fields", ".jsonl", []byte("{}\n{\"id\":null,\"messages\":null,\"role\":null,\"content\":null,\"timestamp\":null}\n[]\n")},
		{"corrupt_sqlite", ".db", []byte("SQLite format 3\x00corrupt-database-pages-here")},
	}

	dir := t.TempDir()
	paths := make([]string, 0, len(payloads))
	for _, p := range payloads {
		fp := filepath.Join(dir, p.name+p.ext)
		if err := os.WriteFile(fp, p.data, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, fp)
	}

	for _, h := range Registry() {
		for _, k := range h.Kinds {
			for _, fp := range paths {
				_ = k.Match(fp)
				sessions, _ := k.Parse(fp, 0)
				for _, s := range sessions {
					if s.Harness == "" || s.ID == "" {
						t.Fatalf("harness %s kind %s returned session with empty Harness/ID on %s: %#v", h.Name, k.Name, fp, s)
					}
				}
				if k.ParseFrom != nil {
					_, _ = k.ParseFrom(fp, 0, 0)
					_, _ = k.ParseFrom(fp, 2, 1700000000000000000)
				}
				if k.Resumes != nil {
					_ = k.Resumes(fp, 0)
					_ = k.Resumes(fp, 2)
				}
				if k.Sidecar != nil {
					_, _ = k.Sidecar(fp)
				}
			}
		}
	}

	// Real-fixture torn-write & mid-offset slicing across all registered JSON/JSONL/MD/XML/YAML fixtures.
	root := filepath.Clean(filepath.Join("..", ".."))
	reg := readFormatRegistry(t, filepath.Join(root, "docs", "registry", "registry.json"))
	kindByHarness := map[string][]FileKind{}
	for _, h := range Registry() {
		kindByHarness[h.Name] = h.Kinds
	}
	for _, entry := range reg.Harnesses {
		kinds := kindByHarness[entry.ID]
		if len(kinds) == 0 {
			continue
		}
		for _, rel := range entry.FixturePaths {
			srcPath := filepath.Join(root, filepath.FromSlash(rel))
			fi, err := os.Stat(srcPath)
			if err != nil || fi.IsDir() || strings.HasSuffix(srcPath, ".db") || strings.HasSuffix(srcPath, ".vscdb") || strings.HasSuffix(srcPath, ".sqlite") {
				continue
			}
			raw, err := os.ReadFile(srcPath)
			if err != nil || len(raw) < 8 {
				continue
			}
			ext := filepath.Ext(srcPath)
			for _, cut := range []int{1, len(raw) / 4, len(raw) / 2, (3 * len(raw)) / 4, len(raw) - 1} {
				tornPath := filepath.Join(dir, entry.ID+"-torn"+ext)
				if err := os.WriteFile(tornPath, raw[:cut], 0o600); err != nil {
					t.Fatal(err)
				}
				for _, k := range kinds {
					sessions, _ := k.Parse(tornPath, 0)
					for _, s := range sessions {
						if s.Harness == "" || s.ID == "" {
							t.Fatalf("%s (%s) torn at %d/%d returned empty Harness/ID: %#v", entry.ID, k.Name, cut, len(raw), s)
						}
						if !utf8.ValidString(s.Title) || !utf8.ValidString(s.Project) {
							t.Fatalf("%s (%s) torn at %d/%d returned invalid UTF-8 metadata: %#v", entry.ID, k.Name, cut, len(raw), s)
						}
						for _, m := range s.Messages {
							if !utf8.ValidString(m.Text) {
								t.Fatalf("%s (%s) torn at %d/%d returned invalid UTF-8 message: %#v", entry.ID, k.Name, cut, len(raw), m)
							}
						}
					}
					if k.ParseFrom != nil {
						_, _ = k.ParseFrom(tornPath, int64(cut/2), 0)
					}
					if k.Resumes != nil {
						_ = k.Resumes(tornPath, int64(cut/2))
					}
				}
			}
		}
	}
}

func TestRemoteSessionIDTakesTheIdOffRemoteControlURLs(t *testing.T) {
	for in, want := range map[string]string{
		"https://claude.ai/code/session_01AbC":     "session_01AbC",
		"claude.ai/code/session_01AbC/":            "session_01AbC",
		"https://claude.ai/code/session_01AbC?x=1": "session_01AbC",
		" session_01AbC ":                          "session_01AbC", //nolint:gocritic // the padding is the input under test
		"session_":                                 "",
		"https://example.com/code/session_01AbC":   "",
		"4be0c2a1-0000-4000-8000-000000000001":     "",
		"":                                         "",
	} {
		if got := RemoteSessionID(in); got != want {
			t.Errorf("RemoteSessionID(%q) = %q, want %q", in, got, want)
		}
	}
}

func repoMemFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "work", "webhooks")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "notes.jsonl"))
	return root
}

func TestRepoMemoryWritesSortedLinesWithFixedKeys(t *testing.T) {
	root := repoMemFixture(t)
	in := []RepoEntry{
		{Kind: "rejected", Text: "retry with jitter broke idempotency", Date: "2026-10-01", From: "claude 2026-08-03"},
		{Kind: "decision", Text: "the outbox table owns retries", Date: "2026-10-01"},
		{Kind: "gotcha", Text: "staging needs the vpn", Date: "2026-10-02"},
	}
	added, err := AddRepoEntries(root, in)
	if err != nil || len(added) != 3 {
		t.Fatalf("added %d, err %v", len(added), err)
	}
	b, err := os.ReadFile(RepoMemoryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %q", b)
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, `{"id":"`) || !strings.Contains(l, `","kind":"`) {
			t.Errorf("line %d keys out of order: %s", i, l)
		}
		if i > 0 && lines[i-1] > l {
			t.Errorf("lines not sorted by id: %s before %s", lines[i-1], l)
		}
	}
	want := RepoEntryID("rejected", "retry with jitter broke idempotency")
	if !strings.Contains(string(b), `"id":"`+want+`"`) {
		t.Errorf("id %s not in file:\n%s", want, b)
	}
	if got := RepoEntryID("rejected", "  Retry with   jitter broke idempotency "); got != want {
		t.Errorf("id should fold case and spacing: %s vs %s", got, want)
	}
	attrs, err := os.ReadFile(filepath.Join(root, RepoMemoryDir, ".gitattributes"))
	if err != nil || string(attrs) != RepoGitattributes {
		t.Errorf(".gitattributes = %q, %v", attrs, err)
	}
	// The same entry again is not a second line.
	again, err := AddRepoEntries(root, in[:1])
	if err != nil || len(again) != 0 {
		t.Errorf("re-adding: %d added, err %v", len(again), err)
	}
}

func TestRepoMemoryReadmeIsWrittenOnce(t *testing.T) {
	root := repoMemFixture(t)
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "one", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(root, RepoMemoryDir, "README.md")
	b, err := os.ReadFile(readme)
	if err != nil || string(b) != RepoReadme || !strings.Contains(string(b), "https://github.com/vshulcz/deja-vu") {
		t.Fatalf("README = %q, %v", b, err)
	}
	// Edited: kept as the person left it.
	if err := os.WriteFile(readme, []byte("ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "two", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(readme); string(b) != "ours\n" {
		t.Errorf("an edited README was rewritten: %q", b)
	}
	// Deleted, along with the attributes: neither comes back.
	for _, name := range []string{"README.md", ".gitattributes"} {
		if err := os.Remove(filepath.Join(root, RepoMemoryDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "three", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", ".gitattributes"} {
		if _, err := os.Lstat(filepath.Join(root, RepoMemoryDir, name)); err == nil {
			t.Errorf("a deleted %s was recreated", name)
		}
	}
}

func TestRepoMemoryUnionMergeDedupes(t *testing.T) {
	root := repoMemFixture(t)
	a := RepoEntry{Kind: "decision", Text: "pgx, not database/sql", Date: "2026-10-01"}
	b := RepoEntry{Kind: "rejected", Text: "a second connection pool", Date: "2026-10-02"}
	if _, err := AddRepoEntries(root, []RepoEntry{a}); err != nil {
		t.Fatal(err)
	}
	ours, _ := os.ReadFile(RepoMemoryPath(root))
	if _, err := AddRepoEntries(root, []RepoEntry{b}); err != nil {
		t.Fatal(err)
	}
	theirs, _ := os.ReadFile(RepoMemoryPath(root))
	// What a union merge leaves when both sides held a: a twice, b once,
	// out of order.
	merged := string(theirs) + string(ours)
	if err := os.WriteFile(RepoMemoryPath(root), []byte(merged), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, bad, err := ReadRepoMemory(RepoMemoryPath(root))
	if err != nil || bad != 0 || len(entries) != 2 {
		t.Fatalf("entries %v bad %d err %v", entries, bad, err)
	}
	if entries[0].ID > entries[1].ID {
		t.Error("read is not sorted")
	}
	// The next write puts the file back in order with one line each.
	c := RepoEntry{Kind: "gotcha", Text: "tests need docker", Date: "2026-10-03"}
	if _, err := AddRepoEntries(root, []RepoEntry{c}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(RepoMemoryPath(root))
	if n := strings.Count(string(out), "\n"); n != 3 {
		t.Errorf("want 3 lines after rewrite, got %d:\n%s", n, out)
	}
}

func TestRepoMemoryMalformedLineBlocksWrites(t *testing.T) {
	root := repoMemFixture(t)
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "one", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	path := RepoMemoryPath(root)
	b, _ := os.ReadFile(path)
	conflict := "<<<<<<< HEAD\n" + string(b) + "=======\n" + `{"id":"x","kind":"wat","text":"t"}` + "\n>>>>>>> other\n"
	if err := os.WriteFile(path, []byte(conflict), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, bad, err := ReadRepoMemory(path)
	if err != nil || len(entries) != 1 || bad != 4 {
		t.Fatalf("entries %d bad %d err %v", len(entries), bad, err)
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "two", Date: "2026-10-01"}}); !errors.Is(err, ErrRepoMemoryMalformed) {
		t.Fatalf("want ErrRepoMemoryMalformed, got %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != conflict {
		t.Error("a refused write changed the file")
	}
}

func TestRepoMemoryRefusesBadEntriesAndSymlinks(t *testing.T) {
	root := repoMemFixture(t)
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "opinion", Text: "x"}}); err == nil {
		t.Error("unknown kind was written")
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: " "}}); err == nil {
		t.Error("empty text was written")
	}
	if runtime.GOOS == "windows" {
		return
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, RepoMemoryDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "x"}}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("wrote through a symlinked .deja: %v", err)
	}
}

func TestRepoMemorySessionsAreMarkedRepo(t *testing.T) {
	root := repoMemFixture(t)
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "rejected", Text: "retry with\tjitter\nbroke it", Date: "2026-08-03", From: "codex 2026-08-01"}}); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseRepoMemoryFile(RepoMemoryPath(root))
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions %v err %v", ss, err)
	}
	s := ss[0]
	name := ClaudeProjectName(root)
	if s.Harness != "deja" || s.Project != model.RepoProjectPrefix+name || !strings.HasPrefix(s.ID, "deja-repo-"+noteIDProject(name)+"-") {
		t.Errorf("session = %+v", s)
	}
	body := s.Messages[0].Text
	if body != "[rejected] retry with jitter broke it (in .deja/memory.jsonl, from codex 2026-08-01)" {
		t.Errorf("body = %q", body)
	}
	if s.Updated.Format("2006-01-02") != "2026-08-03" {
		t.Errorf("dated %v", s.Updated)
	}
	s.SetSource("")
	if s.Source.Origin != "repo" {
		t.Errorf("origin = %q", s.Source.Origin)
	}
	if model.BareProject(s.Project) != name || model.BareProject("imported:a/b") != "a/b" {
		t.Error("BareProject")
	}
	if !IsRepoMemoryFile(RepoMemoryPath(root)) || IsRepoMemoryFile(filepath.Join(root, "memory.jsonl")) {
		t.Error("IsRepoMemoryFile")
	}
	if KindForPath(RepoMemoryPath(root)) != "deja-repo" {
		t.Errorf("kind = %q", KindForPath(RepoMemoryPath(root)))
	}
}

func TestRepoMemoryReaderCapsWhatARepoCanHand(t *testing.T) {
	long := strings.Repeat("a", RepoTextMax+50)
	var b strings.Builder
	b.WriteString(`{"kind":"decision","text":"` + long + `"}` + "\n")
	b.WriteString(`{"kind":"gotcha","text":"no id, no date"}` + "\n")
	b.WriteString("\n")
	entries, bad, err := readRepoMemory(strings.NewReader(b.String()))
	if err != nil || bad != 0 || len(entries) != 2 {
		t.Fatalf("entries %d bad %d err %v", len(entries), bad, err)
	}
	for _, e := range entries {
		if e.ID == "" {
			t.Error("missing id not filled")
		}
		if n := len([]rune(e.Text)); n > RepoTextMax+1 {
			t.Errorf("text not capped: %d runes", n)
		}
	}
	var many strings.Builder
	for i := range repoEntriesMax + 20 {
		many.WriteString(`{"kind":"decision","text":"entry ` + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + `-` + itoa(i) + `"}` + "\n")
	}
	entries, _, _ = readRepoMemory(strings.NewReader(many.String()))
	if len(entries) != repoEntriesMax {
		t.Errorf("read %d entries, cap is %d", len(entries), repoEntriesMax)
	}
}

func TestRepoMemoryFilesRemembersCheckouts(t *testing.T) {
	root := repoMemFixture(t)
	if got := RepoMemoryFiles(); len(got) != 0 {
		t.Fatalf("files before any entry: %v", got)
	}
	if _, err := AddRepoEntries(root, []RepoEntry{{Kind: "decision", Text: "x", Date: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	// Remembered by the write, and read from anywhere afterwards.
	t.Chdir(t.TempDir())
	got := RepoMemoryFiles()
	if len(got) != 1 || filepath.Base(filepath.Dir(got[0])) != RepoMemoryDir {
		t.Fatalf("files = %v", got)
	}
	ss := LoadRepoMemory()
	if len(ss) != 1 {
		t.Errorf("LoadRepoMemory = %d sessions", len(ss))
	}
	// A checkout deleted later drops out without an error.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if got := RepoMemoryFiles(); len(got) != 0 {
		t.Errorf("deleted checkout still listed: %v", got)
	}
}

func TestRemoveRepoEntries(t *testing.T) {
	root := repoMemFixture(t)
	added, err := AddRepoEntries(root, []RepoEntry{
		{Kind: "decision", Text: "keep sqlite for local cache", Date: "2026-10-01"},
		{Kind: "rejected", Text: "dropped boltdb lock contention", Date: "2026-10-02"},
	})
	if err != nil || len(added) != 2 {
		t.Fatalf("AddRepoEntries: %d, %v", len(added), err)
	}
	removed, err := RemoveRepoEntries(root, []string{added[0].ID})
	if err != nil || len(removed) != 1 || removed[0].ID != added[0].ID {
		t.Fatalf("RemoveRepoEntries: %v, %v", removed, err)
	}
	left, bad, err := ReadRepoMemory(RepoMemoryPath(root))
	if err != nil || bad != 0 || len(left) != 1 || left[0].ID != added[1].ID {
		t.Fatalf("ReadRepoMemory after remove: %v, bad=%d, err=%v", left, bad, err)
	}
}

func TestEvolveRepoEntriesAndAutoToggle(t *testing.T) {
	root := repoMemFixture(t)
	keepFile := filepath.Join(root, "internal", "outbox.go")
	goneFile := filepath.Join(root, "internal", "legacy.go")
	if err := os.MkdirAll(filepath.Dir(keepFile), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{keepFile, goneFile} {
		if err := os.WriteFile(p, []byte("package internal\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	added, updated, pruned, err := EvolveRepoEntries(root, []RepoEntry{
		{Kind: "decision", Text: "outbox owns retry scheduling", Files: []string{"internal/outbox.go"}, Sessions: 1, Date: "2026-10-01"},
		{Kind: "rejected", Text: "legacy polling loop caused lock contention", Files: []string{"internal/legacy.go"}, Sessions: 1, Date: "2026-10-01"},
	}, 50)
	if err != nil || added != 2 || updated != 0 || pruned != 0 {
		t.Fatalf("initial evolve: +%d ~%d -%d err=%v", added, updated, pruned, err)
	}

	// Delete legacy.go and reinforce the outbox entry (even under a different kind).
	if rmErr := os.Remove(goneFile); rmErr != nil {
		t.Fatal(rmErr)
	}
	added, updated, pruned, err = EvolveRepoEntries(root, []RepoEntry{
		{Kind: "gotcha", Text: "outbox owns retry scheduling", Files: []string{"internal/outbox.go"}, Commit: "1489a47b1", Sessions: 4, Date: "2026-10-02"},
	}, 50)
	if err != nil || added != 0 || updated != 1 || pruned != 1 {
		t.Fatalf("second evolve: +%d ~%d -%d err=%v", added, updated, pruned, err)
	}

	entries, bad, err := ReadRepoMemory(RepoMemoryPath(root))
	if err != nil || bad != 0 || len(entries) != 1 {
		t.Fatalf("entries after prune+reinforce: %+v bad=%d err=%v", entries, bad, err)
	}
	if entries[0].Commit != "1489a47b1" || entries[0].Sessions != 4 || len(entries[0].Files) != 1 || entries[0].Files[0] != "internal/outbox.go" {
		t.Errorf("reinforced entry = %+v", entries[0])
	}

	ss, err := ParseRepoMemoryFile(RepoMemoryPath(root))
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 || ss[0].Messages[1].Role != "edit" {
		t.Fatalf("ParseRepoMemoryFile messages = %+v, err=%v", ss, err)
	}

	if RepoAutoDisabled(root) {
		t.Error("auto sync disabled by default")
	}
	if err := SetRepoAutoDisabled(root, true); err != nil || !RepoAutoDisabled(root) {
		t.Fatalf("SetRepoAutoDisabled(true): disabled=%v err=%v", RepoAutoDisabled(root), err)
	}
	if err := SetRepoAutoDisabled(root, false); err != nil || RepoAutoDisabled(root) {
		t.Fatalf("SetRepoAutoDisabled(false): disabled=%v err=%v", RepoAutoDisabled(root), err)
	}
}

// The folder name blanks every character that is not a letter or digit to
// "-", and the resolver tried only "/" and a literal "-" in its place. A
// directory with "_", "." or a space in its name never resolved, so a Qwen
// transcript that records no cwd could not be resumed and a Claude project
// kept its encoded name (#4402).
func TestResolveEncodedPathMatchesABlankedCharacter(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"my_app", "app.v2", "two words", "a-b_c"} {
		dir := filepath.Join(tmp, "src_x", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := resolveEncodedPath(claudeEncodePath(dir)); got != dir {
			t.Errorf("%q resolved to %q", dir, got)
		}
	}
}

// Siblings that encode the same resolve the same way every time: the name
// spelled with the hyphen itself first, then the first in name order
// (os.ReadDir sorts).
func TestResolveEncodedPathPicksTheSameSiblingEveryTime(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"my_app", "my.app", "my app"} {
		if err := os.MkdirAll(filepath.Join(tmp, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	enc := claudeEncodePath(filepath.Join(tmp, "my_app"))
	if got, want := resolveEncodedPath(enc), filepath.Join(tmp, "my app"); got != want {
		t.Errorf("without my-app: %q, want the first in name order %q", got, want)
	}
	if err := os.Mkdir(filepath.Join(tmp, "my-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := resolveEncodedPath(enc), filepath.Join(tmp, "my-app"); got != want {
		t.Errorf("with my-app: %q, want the hyphen spelling %q", got, want)
	}
}

// A line longer than the chunk the walk reads back is still handed over
// whole, and the walk stops where fn says.
func TestEachLineBeforeReadsLongLinesWhole(t *testing.T) {
	long := strings.Repeat("x", 70<<10)
	body := "first\n" + long + "\nthird\n"
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	eachLineBefore(p, int64(len(body)), func(line []byte) bool {
		got = append(got, string(line))
		return true
	})
	if len(got) != 3 || got[0] != "third" || got[1] != long || got[2] != "first" {
		t.Fatalf("got %d lines, first %.10q", len(got), got)
	}

	got = nil
	eachLineBefore(p, int64(len("first\n")+len(long)+1), func(line []byte) bool {
		got = append(got, string(line))
		return false
	})
	if len(got) != 1 || got[0] != long {
		t.Fatalf("stopped walk got %d lines", len(got))
	}
}

// tailFile writes head and tail as one transcript and returns the offset
// between them, where the stored part ends.
func tailFile(t *testing.T, name, head, tail string) (string, int64) {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(head+tail), 0o644); err != nil {
		t.Fatal(err)
	}
	return p, int64(len(head))
}

// A file that is gone, or an offset at its start, walks nothing.
func TestEachLineWalksStopAtAMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.jsonl")
	called := false
	eachLineBefore(missing, 10, func([]byte) bool { called = true; return true })
	eachLineFrom(missing, 0, func([]byte) bool { called = true; return true })
	if called {
		t.Error("a missing file handed over a line")
	}
	p, _ := tailFile(t, "t.jsonl", "a\nb\n", "")
	var got []string
	eachLineFrom(p, 0, func(line []byte) bool { got = append(got, string(line)); return false })
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("stopped walk from the start got %q, want only a", got)
	}
	if decodeJSONLine([]byte(`[1]`)) != nil || decodeJSONLine([]byte(`{"n":1}`))["n"] != json.Number("1") {
		t.Error("decodeJSONLine wants an object, numbers kept as json.Number")
	}
}

// The answering rule only knows the calls the tail makes. A failed exit for
// a call stored already sends the file back; one for a call in the same tail,
// a clean exit, or a line that does not decode does not (#4443).
func TestCodexAndCopilotResumesOnlyOnAFailureForAStoredCall(t *testing.T) {
	codexCall := func(id string) string {
		return `{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"` + id + `","arguments":"{\"cmd\":\"go test ./...\"}"}}` + "\n"
	}
	codexOut := func(id, code string) string {
		return `{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"` + id + `","output":"Process exited with code ` + code + `\nOutput:\nFAIL\n"}}` + "\n"
	}
	copilotCall := func(id string) string {
		return `{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"` + id + `","arguments":{"command":"go vet ./..."}}}` + "\n"
	}
	copilotOut := func(id, code string) string {
		return `{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"` + id + `","success":true,"result":{"content":"vet\n<shellId: 0 completed with exit code ` + code + `>"}}}` + "\n"
	}
	for _, c := range []struct {
		name       string
		resumes    func(string, int64) bool
		head, tail string
		want       bool
	}{
		{"codex failed exit for a stored call", codexResumes, codexCall("c1"), codexOut("c1", "2"), false},
		{"codex clean exit for a stored call", codexResumes, codexCall("c1"), codexOut("c1", "0"), true},
		{"codex call and failure both in the tail", codexResumes, "", codexCall("c2") + codexOut("c2", "1"), true},
		{"codex half-written answer", codexResumes, codexCall("c1"), `{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","out` + "\n", true},
		{"codex failure then more", codexResumes, codexCall("c1"), codexOut("c1", "1") + codexCall("c3") + codexOut("c3", "0"), false},
		{"copilot failed exit for a stored call", copilotResumes, copilotCall("k1"), copilotOut("k1", "1"), false},
		{"copilot clean exit for a stored call", copilotResumes, copilotCall("k1"), copilotOut("k1", "0"), true},
		{"copilot call and failure both in the tail", copilotResumes, "", copilotCall("k2") + copilotOut("k2", "1"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			head := `{"type":"session_meta"}` + "\n" + c.head
			p, off := tailFile(t, "t.jsonl", head, c.tail)
			if got := c.resumes(p, off); got != c.want {
				t.Errorf("resumes = %v, want %v", got, c.want)
			}
			if !c.resumes(p, 0) {
				t.Error("a read from the start resumes nothing, so it must say yes")
			}
		})
	}
}

// grok joins a reply's chunks by promptId and a prompt's by promptIndex. The
// tail is read on its own unless its first chunk carries the key the stored
// part ended on (#4445).
func TestGrokResumesUnlessTheTailJoinsTheLastChunk(t *testing.T) {
	user := func(i, text string) string {
		return `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"` + text + `"},"_meta":{"promptIndex":` + i + `}}}}` + "\n"
	}
	agent := func(id, text string) string {
		return `{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"` + text + `"}},"_meta":{"promptId":"` + id + `"}}}` + "\n"
	}
	tool := `{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t1","title":"bash"}}}` + "\n"
	for _, c := range []struct {
		name, head, tail string
		want             bool
	}{
		{"reply goes on past a tool", user("0", "find it") + agent("p1", "first "), tool + agent("p1", "answer"), false},
		{"next prompt", user("0", "find it") + agent("p1", "done"), user("1", "thanks") + agent("p2", "ok"), true},
		{"empty chunk is not the reply's", user("0", "find it") + agent("p1", "done"), agent("p1", "") + user("1", "next"), true},
		{"chunk without a prompt id", agent("", "loose"), agent("", "more"), true},
		{"nothing stored to join", tool, agent("p1", "answer"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, off := tailFile(t, "updates.jsonl", c.head, c.tail)
			if got := GrokResumes(p, off); got != c.want {
				t.Errorf("GrokResumes = %v, want %v", got, c.want)
			}
		})
	}
	if p, _ := tailFile(t, "updates.jsonl", agent("p1", "a"), agent("p1", "b")); !GrokResumes(p, 0) {
		t.Error("offset 0 must resume")
	}
}

// kiro-cli streams a reply as AssistantMessage records under one message_id.
func TestKiroCLIResumesUnlessTheTailContinuesTheReply(t *testing.T) {
	prompt := `{"version":"v1","kind":"Prompt","data":{"message_id":"m1","content":[{"kind":"text","data":"fix the retry loop"}],"meta":{"timestamp":1790848800}}}` + "\n"
	reply := func(id, text string) string {
		return `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"` + id + `","content":[{"kind":"text","data":"` + text + `"}]}}` + "\n"
	}
	results := `{"version":"v1","kind":"ToolResults","data":{"message_id":"m3","content":[]}}` + "\n"
	for _, c := range []struct {
		name, head, tail string
		want             bool
	}{
		{"reply goes on", prompt + reply("m2", "Checking"), reply("m2", " the loop."), false},
		{"reply goes on past a result", prompt + reply("m2", "Checking"), results + `{"version":"v1"}` + "\n" + reply("m2", " more"), false},
		{"next reply", prompt + reply("m2", "Done."), reply("m4", "Next."), true},
		{"next prompt", prompt + reply("m2", "Done."), prompt, true},
		{"stored part ends on a prompt", prompt, reply("m2", "Checking"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, off := tailFile(t, "s.jsonl", c.head, c.tail)
			if got := KiroCLIResumes(p, off); got != c.want {
				t.Errorf("KiroCLIResumes = %v, want %v", got, c.want)
			}
		})
	}
}

// A record without a stamp takes the last stamped one's before the offset;
// with none stamped, or nothing before the offset, there is none to take.
func TestKiroCLITimeBefore(t *testing.T) {
	stamped := `{"version":"v1","kind":"Prompt","data":{"message_id":"m1","content":[{"kind":"text","data":"q"}],"meta":{"timestamp":1790848800}}}` + "\n"
	bare := `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m2","content":[{"kind":"text","data":"a"}]}}` + "\n"
	p, off := tailFile(t, "s.jsonl", stamped+bare, bare)
	if got := kiroCLITimeBefore(p, off); got.Unix() != 1790848800 {
		t.Errorf("time before = %v, want the prompt's stamp", got)
	}
	p, off = tailFile(t, "s.jsonl", bare+bare, bare)
	if got := kiroCLITimeBefore(p, off); !got.IsZero() {
		t.Errorf("unstamped records gave %v, want none", got)
	}
	if got := kiroCLITimeBefore(p, 0); !got.IsZero() {
		t.Errorf("offset 0 gave %v, want none", got)
	}
}

// A read under LimitReads stops at the end the pass recorded, so a line
// written while the pass ran is left for the next one (#4442). Holds nest:
// releasing the inner one keeps the outer bound.
func TestLimitReadsHoldsTheEndUntilTheLastRelease(t *testing.T) {
	first := `{"n":1}` + "\n"
	p, end := tailFile(t, "t.jsonl", first, `{"n":2}`+"\n")
	count := func() int {
		n := 0
		if err := scanJSONLFromOffset(p, 0, func(map[string]any) { n++ }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	outer := LimitReads(map[string]int64{p: end})
	inner := LimitReads(map[string]int64{p: end})
	if got := count(); got != 1 {
		t.Errorf("held read saw %d lines, want 1", got)
	}
	inner()
	if got := count(); got != 1 {
		t.Errorf("after the inner release a read saw %d lines, want the outer bound's 1", got)
	}
	if err := scanJSONLFromOffset(p, end+5, func(map[string]any) { t.Error("a read past the bound got a line") }); err != nil {
		t.Fatal(err)
	}
	outer()
	if got := count(); got != 2 {
		t.Errorf("released read saw %d lines, want 2", got)
	}
}

// With codemode loaded, Senpi's default, the shell runs only inside an eval
// cell as tool.bash(...): the model's toolCall is `eval` with JS, and what ran
// is on the result, under details.toolCalls[].args. The first cell is the one a
// senpi 2026.10.1-3 session wrote; the second carries a read, an edit and a
// refused edit in the same shape (#4425).
const senpiEvalFixture = `{"type":"session","version":3,"id":"senpi-eval","timestamp":"2026-10-01T23:16:48.020Z","cwd":"/private/tmp/proj-senpi"}
{"type":"message","id":"u1","timestamp":"2026-10-01T23:16:49.000Z","message":{"role":"user","content":[{"type":"text","text":"Check the retry config."}]}}
{"type":"message","id":"a1","timestamp":"2026-10-01T23:16:50.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_524cd669c1bb","name":"eval","arguments":{"language":"js","summary":"reading the retry config","code":"const r = await tool.bash({ command: \"cat retry.cfg && git status --short\" }); r"}}]}}
{"type":"message","id":"r1","timestamp":"2026-10-01T23:16:51.000Z","message":{"role":"toolResult","toolCallId":"call_524cd669c1bb","toolName":"eval","content":[{"type":"text","text":"{\"text\":\"retries = 3\\nmax_backoff_seconds = 30  # cap for the retry loop\\n\\nCommand exited with code 128\"}"}],"details":{"language":"js","toolCallCount":1,"toolCalls":[{"name":"bash","ok":true,"durationMs":530,"callId":"js-ee43","args":{"command":"cat retry.cfg && git status --short"},"resultPreview":"retries = 3"}]},"isError":false}}
{"type":"message","id":"a2","timestamp":"2026-10-01T23:16:52.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_2","name":"eval","arguments":{"language":"js","code":"await tool.read({path:\"retry.cfg\"}); await tool.edit(...)"}}]}}
{"type":"message","id":"r2","timestamp":"2026-10-01T23:16:53.000Z","message":{"role":"toolResult","toolCallId":"call_2","toolName":"eval","content":[{"type":"text","text":"done"}],"details":{"toolCalls":[{"name":"read","ok":true,"args":{"path":"retry.cfg"}},{"name":"edit","ok":true,"args":{"path":"retry.cfg","edits":[{"oldText":"max_backoff_seconds = 30  # cap for the retry loop","newText":"max_backoff_seconds = 120  # cap for the retry loop"}]}},{"name":"edit","ok":false,"args":{"path":"retry.cfg","edits":[{"oldText":"a line the file never held at any point","newText":"what the agent hoped to write there instead"}]}}]},"isError":false}}
`

func TestSenpiEvalCellCallsAreIndexed(t *testing.T) {
	ss, err := ParseSenpiFile(writePiFixture(t, senpiEvalFixture))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	cmds := rolesOf(s, RoleCommand)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "cat retry.cfg && git status --short") {
		t.Errorf("commands = %q, want the one the eval cell ran", cmds)
	}
	files := strings.Join(rolesOf(s, RoleFiles), "\n")
	if !strings.Contains(files, "/private/tmp/proj-senpi/retry.cfg") {
		t.Errorf("files = %q, want retry.cfg resolved against the header's cwd", files)
	}
	edits := strings.Join(rolesOf(s, RoleEdit), "\n")
	if !strings.Contains(edits, "max_backoff_seconds = 30") {
		t.Errorf("edits = %q, want the replaced line", edits)
	}
	if strings.Contains(edits, "never held") {
		t.Errorf("edits = %q: the refused edit changed nothing", edits)
	}
	// The result's text is a JSON string; it is read as the text it wraps.
	out := strings.Join(rolesOf(s, RoleToolOutput), "\n")
	if !strings.Contains(out, "retries = 3\nmax_backoff_seconds") || strings.Contains(out, `{"text"`) {
		t.Errorf("tool output = %q, want the cell's text unwrapped", out)
	}
}

// Senpi names a session folder after its cwd with every / turned into -, so
// .../sg/my-app and .../sg/my/app share one folder name. The header records
// the real cwd, and that is what names the project: decoding the folder picked
// my/app whenever that directory existed (#4427). pi names its folders the
// same way.
func TestSenpiProjectComesFromTheHeaderCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_SENPI_ROOT", filepath.Join(root, "sessions"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	t.Setenv("DEJA_KIMCHI_ROOT", filepath.Join(root, "kimchi-sessions"))
	cwd := filepath.Join(root, "sg", "my-app")
	for _, d := range []string{cwd, filepath.Join(root, "sg", "my", "app")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	quoted := mustJSON(t, cwd)
	head := `{"type":"session","version":3,"id":"b1000000-0000-7000-8000-000000000001","timestamp":"2026-10-01T23:00:00Z","cwd":` + string(quoted) + `}` + "\n"
	first := `{"type":"message","id":"u1","timestamp":"2026-10-01T23:00:01Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n"
	tail := `{"type":"message","id":"a1","timestamp":"2026-10-01T23:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"capped at five"}]}}` + "\n"
	for _, c := range []struct {
		root  string
		parse func(string, int64) ([]model.Session, error)
	}{
		{filepath.Join(root, "sessions"), ParseSenpiFileFromOffset},
		{filepath.Join(root, "pi-sessions"), ParsePiFileFromOffset},
		// Kimchi took the header's cwd already, then encoded it and decoded
		// it again, which is the same guess.
		{filepath.Join(root, "kimchi-sessions"), ParseKimchiFileFromOffset},
	} {
		dir := filepath.Join(c.root, "--"+pathToProjectKey(cwd)[1:]+"--")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "2026-10-01T23-00-00-000Z_b1000000.jsonl")
		if err := os.WriteFile(path, []byte(head+first+tail), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, off := range []int64{0, int64(len(head) + len(first))} {
			ss, err := c.parse(path, off)
			if err != nil || len(ss) != 1 {
				t.Fatalf("%s at %d: %v, %d sessions", path, off, err, len(ss))
			}
			if ss[0].Project != "sg/my-app" {
				t.Errorf("%s read at %d: project = %q, want sg/my-app from the header", filepath.Base(c.root), off, ss[0].Project)
			}
		}
	}
}

// "Start the thing that is not running" is the commonest answer to a refused
// connection, and it was the one remedy the command allowlist did not carry —
// while `brew install`, two words away, was carried (#2373).
func TestServiceCommandsAreIndexed(t *testing.T) {
	for _, cmd := range []string{
		"brew services start postgresql",
		"brew services restart redis",
		"systemctl restart postgresql",
		"sudo systemctl start nginx",
		"systemctl --user restart syncthing",
		"service postgresql restart",
		"launchctl load ~/Library/LaunchAgents/redis.plist",
		"launchctl kickstart -k gui/501/homebrew.mxcl.postgresql",
	} {
		if !worthIndexing(cmd) {
			t.Errorf("a remedy deja fix exists to name is not indexed: %q", cmd)
		}
	}
}

// The list stays an allowlist: navigation and one-shot surgery on a number that
// changes every run are not remedies another session can repeat.
func TestTheAllowlistStaysNarrow(t *testing.T) {
	for _, cmd := range []string{
		"kill -9 4711",
		"lsof -ti :5432",
		"cd services",
		"ls /etc/service",
		"cat service.log",
	} {
		if worthIndexing(cmd) {
			t.Errorf("the allowlist let in something that is not a remedy: %q", cmd)
		}
	}
}

func execSQLFixture(t *testing.T, db, script string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
}

func TestOpencodeV1PopulatesAgentModelTokensAndToolStats(t *testing.T) {
	db := filepath.Join(t.TempDir(), "opencode.db")
	execSQLFixture(t, db, `
create table session (id text primary key, parent_id text, title text, directory text, time_created int, time_updated int);
create table message (id text primary key, session_id text, time_created int, time_updated int, data text);
create table part (id text primary key, message_id text, session_id text, time_created int, time_updated int, data text);

insert into session values ('ses_root', null, 'Root task', '/work/app', 1700000000000, 1700000010000);
insert into session values ('ses_child', 'ses_root', 'Child task', '/work/app', 1700000002000, 1700000009000);

insert into message values ('m1', 'ses_root', 1700000001000, 1700000001000,
	'{"role":"user","agent":"sisyphus","model":{"providerID":"anthropic","modelID":"claude-opus-4-6"}}');
insert into part values ('p1', 'm1', 'ses_root', 1700000001000, 1700000001000,
	'{"type":"text","text":"audit the retry queue"}');

insert into message values ('m2', 'ses_root', 1700000003000, 1700000005000,
	'{"role":"assistant","agent":"sisyphus","providerID":"anthropic","modelID":"claude-opus-4-6","tokens":{"input":120,"output":45,"reasoning":15,"cache":{"read":300}}}');
insert into part values ('p2', 'm2', 'ses_root', 1700000003000, 1700000003000,
	'{"type":"tool","tool":"read","state":{"status":"completed","input":{"filePath":"/work/app/queue.go"}}}');
insert into part values ('p3', 'm2', 'ses_root', 1700000004000, 1700000004000,
	'{"type":"tool","tool":"glob","state":{"status":"completed","input":{"pattern":"*.go"}}}');
insert into part values ('p4', 'm2', 'ses_root', 1700000004500, 1700000004500,
	'{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"go test ./..."},"output":"FAIL","metadata":{"exit":1}}}');
insert into part values ('p5', 'm2', 'ses_root', 1700000004800, 1700000004800,
	'{"type":"tool","tool":"edit","state":{"status":"error","input":{"filePath":"/work/app/queue.go","oldString":"a","newString":"b"}}}');
insert into part values ('p6', 'm2', 'ses_root', 1700000005000, 1700000005000,
	'{"type":"text","text":"fixed the queue"}');

insert into message values ('m3', 'ses_child', 1700000006000, 1700000008000,
	'{"role":"assistant","agent":"explore","providerID":"openai","modelID":"gpt-5.4","tokens":{"input":50,"output":20,"reasoning":5,"cache":{"read":10}}}');
insert into part values ('p7', 'm3', 'ses_child', 1700000006000, 1700000008000,
	'{"type":"text","text":"found the caller"}');
`)

	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]model.Session{}
	for _, s := range ss {
		byID[s.ID] = s
	}
	root, ok := byID["ses_root"]
	if !ok {
		t.Fatalf("missing ses_root in %+v", ss)
	}
	if root.Agent != "sisyphus" {
		t.Errorf("root.Agent = %q, want sisyphus", root.Agent)
	}
	wantModel := &model.Model{Provider: "anthropic", ID: "claude-opus-4-6"}
	if !reflect.DeepEqual(root.Model, wantModel) {
		t.Errorf("root.Model = %+v, want %+v", root.Model, wantModel)
	}
	// m2 has 5 parts but its token usage must be counted once per message.
	wantTokens := &model.Tokens{Input: 120, Output: 45, Reasoning: 15, CacheRead: 300}
	if !reflect.DeepEqual(root.Tokens, wantTokens) {
		t.Errorf("root.Tokens = %+v, want %+v", root.Tokens, wantTokens)
	}
	wantTools := map[string]int{"read": 1, "glob": 1, "bash": 1, "edit": 1}
	if !reflect.DeepEqual(root.ToolCalls, wantTools) {
		t.Errorf("root.ToolCalls = %+v, want %+v", root.ToolCalls, wantTools)
	}
	if root.ToolErrors != 2 {
		t.Errorf("root.ToolErrors = %d, want 2 (bash exit 1 + edit status error)", root.ToolErrors)
	}

	child, ok := byID["ses_child"]
	if !ok {
		t.Fatalf("missing ses_child in %+v", ss)
	}
	if child.Kind != "subagent" || child.Parent != "ses_root" || child.Agent != "explore" {
		t.Errorf("child lineage = (%q, %q, %q), want (subagent, ses_root, explore)", child.Kind, child.Parent, child.Agent)
	}
	if !reflect.DeepEqual(child.Model, &model.Model{Provider: "openai", ID: "gpt-5.4"}) {
		t.Errorf("child.Model = %+v", child.Model)
	}
	if !reflect.DeepEqual(child.Tokens, &model.Tokens{Input: 50, Output: 20, Reasoning: 5, CacheRead: 10}) {
		t.Errorf("child.Tokens = %+v", child.Tokens)
	}
}

func TestOpencodeV2PopulatesAgentModelTokensAndToolStats(t *testing.T) {
	db := filepath.Join(t.TempDir(), "opencode.db")
	execSQLFixture(t, db, `
create table session_v2 (id text primary key, parent_id text, title text, directory text, model text, time_created int, time_updated int);
create table session_message (id text primary key, session_id text, type text, seq int, time_created int, time_updated int, data text);

insert into session_v2 values ('s_v2', 's_parent', 'V2 subagent', '/work/v2', '{"providerID":"anthropic","modelID":"claude-sonnet-4-6"}', 1700000000000, 1700000010000);
insert into session_message values ('sm1', 's_v2', 'user', 0, 1700000001000, 1700000001000,
	'{"text":"check v2 store"}');
insert into session_message values ('sm2', 's_v2', 'assistant', 1, 1700000002000, 1700000004000,
	'{"agent":"librarian","tokens":{"input":80,"output":30,"reasoning":10,"cache":{"read":20}},"content":[{"type":"tool","name":"grep","state":{"status":"completed","input":{"pattern":"foo"}}},{"type":"tool","name":"shell","state":{"status":"completed","input":{"command":"go test ./..."},"structured":{"exit":1},"content":[{"type":"text","text":"FAIL"}]}},{"type":"text","text":"checked"}]}');
`)

	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Kind != "subagent" || s.Parent != "s_parent" || s.Agent != "librarian" {
		t.Errorf("lineage = (%q, %q, %q), want (subagent, s_parent, librarian)", s.Kind, s.Parent, s.Agent)
	}
	if !reflect.DeepEqual(s.Model, &model.Model{Provider: "anthropic", ID: "claude-sonnet-4-6"}) {
		t.Errorf("s.Model = %+v", s.Model)
	}
	if !reflect.DeepEqual(s.Tokens, &model.Tokens{Input: 80, Output: 30, Reasoning: 10, CacheRead: 20}) {
		t.Errorf("s.Tokens = %+v", s.Tokens)
	}
	if !reflect.DeepEqual(s.ToolCalls, map[string]int{"grep": 1, "shell": 1}) {
		t.Errorf("s.ToolCalls = %+v", s.ToolCalls)
	}
	if s.ToolErrors != 1 {
		t.Errorf("s.ToolErrors = %d, want 1", s.ToolErrors)
	}
}

func TestClaudeParserDeduplicatesMultiBlockUsageAndCountsTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	lines := []string{
		`{"type":"user","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"find the handler"}}`,
		// Two JSONL lines for the same API call (requestId req_1 / message.id msg_1), repeating usage.
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","requestId":"req_1","timestamp":"2026-01-02T03:04:06Z","message":{"id":"msg_1","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":100,"output_tokens":40,"cache_read_input_tokens":500,"output_tokens_details":{"thinking_tokens":12}},"content":[{"type":"text","text":"looking"}]}}`,
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","requestId":"req_1","timestamp":"2026-01-02T03:04:07Z","message":{"id":"msg_1","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":100,"output_tokens":40,"cache_read_input_tokens":500,"output_tokens_details":{"thinking_tokens":12}},"content":[{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"/w/a.go"}},{"type":"tool_use","id":"tu_2","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","timestamp":"2026-01-02T03:04:08Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_2","is_error":true,"content":"Exit code 1\nFAIL"}]}}`,
		// Second API call (req_2).
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","requestId":"req_2","timestamp":"2026-01-02T03:04:09Z","message":{"id":"msg_2","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":200},"content":[{"type":"text","text":"done"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseClaudeFile = %+v, %v", ss, err)
	}
	s := ss[0]
	if s.ID != "sub1" || s.Kind != "sidechain" || s.Parent != "s1" || s.Agent != "Explore" {
		t.Errorf("identity = (%q, %q, %q, %q)", s.ID, s.Kind, s.Parent, s.Agent)
	}
	if !reflect.DeepEqual(s.Model, &model.Model{Provider: "anthropic", ID: "claude-opus-4-6"}) {
		t.Errorf("s.Model = %+v", s.Model)
	}
	wantTokens := &model.Tokens{Input: 150, Output: 60, Reasoning: 12, CacheRead: 700}
	if !reflect.DeepEqual(s.Tokens, wantTokens) {
		t.Errorf("s.Tokens = %+v, want %+v", s.Tokens, wantTokens)
	}
	wantTools := map[string]int{"Read": 1, "Bash": 1}
	if !reflect.DeepEqual(s.ToolCalls, wantTools) {
		t.Errorf("s.ToolCalls = %+v, want %+v", s.ToolCalls, wantTools)
	}
	if s.ToolErrors != 1 {
		t.Errorf("s.ToolErrors = %d, want 1", s.ToolErrors)
	}
}

func TestCodexRolloutPopulatesAgentModelTokensAndToolStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-01-02T03-04-05-child-1.jsonl")
	lines := []string{
		`{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"id":"child-1","cwd":"/work/codex","model_provider":"openai","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-1","depth":1}}}}}`,
		`{"timestamp":"2026-01-02T03:04:06Z","type":"turn_context","payload":{"model":"gpt-5.4","collaboration_mode":{"mode":"code","settings":{"model":"gpt-5.4"}}}}`,
		`{"timestamp":"2026-01-02T03:04:07Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"run tests"}]}}`,
		`{"timestamp":"2026-01-02T03:04:08Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go test ./...\"}"}}`,
		`{"timestamp":"2026-01-02T03:04:09Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Process exited with code 1\nOutput:\nFAIL"}}`,
		`{"timestamp":"2026-01-02T03:04:10Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c2","input":"*** Begin Patch\n*** Update File: a.go\n@@\n-old\n+new\n*** End Patch"}}`,
		// Duplicate total_tokens event is ignored; only distinct totals accumulate.
		`{"timestamp":"2026-01-02T03:04:11Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1000},"last_token_usage":{"input_tokens":800,"cached_input_tokens":600,"output_tokens":150,"reasoning_output_tokens":50}}}}`,
		`{"timestamp":"2026-01-02T03:04:11Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1000},"last_token_usage":{"input_tokens":800,"cached_input_tokens":600,"output_tokens":150,"reasoning_output_tokens":50}}}}`,
		`{"timestamp":"2026-01-02T03:04:12Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"patched a.go"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseCodexRollout = %+v, %v", ss, err)
	}
	s := ss[0]
	if s.ID != "child-1" || s.Kind != "subagent" || s.Parent != "parent-1" || s.Agent != "code" {
		t.Errorf("lineage = (%q, %q, %q, %q)", s.ID, s.Kind, s.Parent, s.Agent)
	}
	if !reflect.DeepEqual(s.Model, &model.Model{Provider: "openai", ID: "gpt-5.4"}) {
		t.Errorf("s.Model = %+v", s.Model)
	}
	wantTokens := &model.Tokens{Input: 800, Output: 150, Reasoning: 50, CacheRead: 600}
	if !reflect.DeepEqual(s.Tokens, wantTokens) {
		t.Errorf("s.Tokens = %+v, want %+v", s.Tokens, wantTokens)
	}
	wantTools := map[string]int{"exec_command": 1, "apply_patch": 1}
	if !reflect.DeepEqual(s.ToolCalls, wantTools) {
		t.Errorf("s.ToolCalls = %+v, want %+v", s.ToolCalls, wantTools)
	}
	if s.ToolErrors != 1 {
		t.Errorf("s.ToolErrors = %d, want 1", s.ToolErrors)
	}
}

// A command that printed nothing comes back as `Output: (empty)`. With the
// report unwrapped that left a tool-output record reading just "(empty)",
// which is no output at all, so none is written. An error beside it is still
// the command's output (#4256).
func TestQuietShellCommandLeavesNoToolOutput(t *testing.T) {
	cases := map[string]int{
		"Command: true\nDirectory: (root)\nOutput: (empty)\nError: (none)\nExit Code: 0": 0,
		"Output: (empty)\nProcess Group PGID: 98714":                                     0,
		"Output: (empty)\nError: spawn sh ENOENT\nExit Code: 1":                          1,
	}
	for report, want := range cases {
		parts := []any{map[string]any{"functionResponse": map[string]any{
			"id": "c1", "name": "run_shell_command", "response": map[string]any{"output": report},
		}}}
		got := 0
		for _, m := range qwenWorkRecords(parts, parseTimeAny(nil)) {
			if m.Role == RoleToolOutput {
				got++
				t.Logf("%q -> %q", report, m.Text)
			}
		}
		if got != want {
			t.Errorf("%q left %d tool-output records, want %d", report, got, want)
		}
	}
}

// Cline rewrites taskHistory.json on every turn of any task. Only a change to
// the fields the reader takes from a task's own entry moves its fingerprint,
// or each turn would re-read every task the extension ever ran.
func TestClineVSCodeSidecarFollowsTheTasksOwnEntry(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "tasks", "1767300000000", "api_conversation_history.json")
	history := filepath.Join(root, "state", "taskHistory.json")
	if err := os.MkdirAll(filepath.Dir(history), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	write := func(body string) (int64, int64) {
		t.Helper()
		at = at.Add(time.Second)
		if err := os.WriteFile(history, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(history, at, at); err != nil {
			t.Fatal(err)
		}
		return clineVSCodeSidecar(transcript)
	}
	_, base := write(`[{"id":"1767300000000","ts":1767300000000,"task":"fix the retry loop","tokensIn":1},{"id":"2","task":"other"}]`)
	if base == 0 {
		t.Fatal("the task's entry left no fingerprint")
	}
	if _, got := write(`[{"id":"1767300000000","ts":1767300000000,"task":"fix the retry loop","tokensIn":9},{"id":"2","task":"other, renamed"}]`); got != base {
		t.Error("a token count and another task's rename moved this task's fingerprint")
	}
	if _, got := write(`[{"id":"1767300000000","ts":1767300000000,"task":"cap the retry at three","tokensIn":9}]`); got == base {
		t.Error("a rename of this task left its fingerprint as it was")
	}
}

// The reader takes the first entry under a task's id, so the fingerprint has
// to come from that one too, or a change to it is never read.
func TestClineVSCodeSidecarHashesTheEntryTheReaderTakes(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "tasks", "1767300000000", "api_conversation_history.json")
	history := filepath.Join(root, "state", "taskHistory.json")
	if err := os.MkdirAll(filepath.Dir(history), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	write := func(body string) int64 {
		t.Helper()
		at = at.Add(time.Second)
		if err := os.WriteFile(history, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(history, at, at); err != nil {
			t.Fatal(err)
		}
		_, stamp := clineVSCodeSidecar(transcript)
		return stamp
	}
	base := write(`[{"id":"1767300000000","task":"fix the retry loop"},{"id":"1767300000000","task":"stale copy"}]`)
	if write(`[{"id":"1767300000000","task":"cap the retry at three"},{"id":"1767300000000","task":"stale copy"}]`) == base {
		t.Error("a rename of the entry the reader takes left the fingerprint as it was")
	}
}

// Each sidecar is fingerprinted from the file the reader opens beside the
// transcript. A missing one counts as nothing, a directory in its place too,
// an unchanged one keeps the fingerprint and a rewrite moves it (#4446).
func TestSidecarsFingerprintTheFileBesideTheTranscript(t *testing.T) {
	root := t.TempDir()
	kimiState := filepath.Join(root, "kimi", "state.json")
	for _, c := range []struct {
		name       string
		transcript string
		sidecar    string
		stat       func(string) (int64, int64)
	}{
		{"grok summary", filepath.Join(root, "grok", "s1", "updates.jsonl"), filepath.Join(root, "grok", "s1", "summary.json"), besideSidecar("summary.json")},
		{"cline sdk manifest", filepath.Join(root, "cline", "s2", "s2.messages.json"), filepath.Join(root, "cline", "s2", "s2.json"), clineSDKSidecar},
		{"reasonix meta", filepath.Join(root, "reasonix", "s3.jsonl"), filepath.Join(root, "reasonix", "s3.meta.json"), reasonixSidecar},
		{"kimi state", filepath.Join(root, "kimi", "sessions", "s4", "wire.jsonl"), kimiState, kimiSidecar},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Dir(c.sidecar), 0o755); err != nil {
				t.Fatal(err)
			}
			if size, stamp := c.stat(c.transcript); size != 0 || stamp != 0 {
				t.Fatalf("missing sidecar = %d/%d, want nothing", size, stamp)
			}
			if err := os.Mkdir(c.sidecar, 0o755); err != nil {
				t.Fatal(err)
			}
			if size, stamp := c.stat(c.transcript); size != 0 || stamp != 0 {
				t.Fatalf("a directory in the sidecar's place = %d/%d, want nothing", size, stamp)
			}
			if err := os.Remove(c.sidecar); err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Hour)
			write := func(body string) {
				t.Helper()
				at = at.Add(time.Second)
				if err := os.WriteFile(c.sidecar, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(c.sidecar, at, at); err != nil {
					t.Fatal(err)
				}
			}
			write(`{"title":"fix the retry loop"}`)
			size, stamp := c.stat(c.transcript)
			if size == 0 || stamp == 0 {
				t.Fatalf("sidecar = %d/%d, want its size and time", size, stamp)
			}
			if s2, st2 := c.stat(c.transcript); s2 != size || st2 != stamp {
				t.Error("an unchanged sidecar moved the fingerprint")
			}
			write(`{"title":"cap the retry at three"}`)
			if s2, st2 := c.stat(c.transcript); s2 == size && st2 == stamp {
				t.Error("a renamed session left the fingerprint as it was")
			}
		})
	}
}

// No taskHistory.json, or one without this task, fingerprints nothing.
func TestClineVSCodeSidecarWithoutTheTasksEntry(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "tasks", "1767300000000", "api_conversation_history.json")
	if size, stamp := clineVSCodeSidecar(transcript); size != 0 || stamp != 0 {
		t.Errorf("no history = %d/%d, want nothing", size, stamp)
	}
	history := filepath.Join(root, "state", "taskHistory.json")
	if err := os.MkdirAll(filepath.Dir(history), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history, []byte(`[{"id":"other","task":"something else"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if size, stamp := clineVSCodeSidecar(transcript); size != 0 || stamp != 0 {
		t.Errorf("history without the task = %d/%d, want nothing", size, stamp)
	}
}

// A Cline prompt becomes the session's stored title, which every surface shows
// and sync carries to other machines. It was cut at 120 bytes, so a prompt in
// any non-ASCII language kept a broken byte forever (#1319). The padding moves
// the cut off whichever boundary it happened to land on.
func TestClineTitleCutsOnRuneBoundaries(t *testing.T) {
	for _, pad := range []string{"", "a", "ab"} {
		got := firstLineTrim(pad + strings.Repeat("ошибка подключения ", 20))
		if !utf8.ValidString(got) {
			t.Errorf("pad %q: the stored title is not valid UTF-8: %q", pad, got)
		}
		if len(got) > 120 {
			t.Errorf("pad %q: the title ran past its bound at %d bytes", pad, len(got))
		}
	}
}

// A short prompt is untouched, and the first line is still what names it.
func TestClineTitleKeepsShortPrompts(t *testing.T) {
	if got := firstLineTrim("fix the retry queue\nand then deploy"); got != "fix the retry queue" {
		t.Errorf("title = %q", got)
	}
}

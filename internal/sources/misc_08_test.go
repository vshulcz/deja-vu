package sources

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
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
	"strconv"
	"strings"
	"testing"
	"time"
)

// The records below are shaped from botmux's TRAE reader and its tests
// (src/services/traex-transcript.ts, test/traex-transcript.test.ts) and from
// agentsview's traex fixture; none is copied from a real session.

const traeMeta = `{"timestamp":"2026-10-01T09:00:00.000Z","type":"session_meta","payload":{"id":"019fbcca-0000-7000-8000-000000000001","timestamp":"2026-10-01T09:00:00.000Z","cwd":"/work/api","originator":"codex-tui","cli_version":"0.201.4","thread_source":"user","model_provider":"trae"}}`

func writeTraeRollout(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, "rollout-2026-10-01T09-00-00-019fbcca-0000-7000-8000-000000000001.jsonl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func parseTraeOne(t *testing.T, p string) model.Session {
	t.Helper()
	ss, err := ParseTraeRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	if ss[0].Harness != "trae" {
		t.Fatalf("harness = %q, want trae", ss[0].Harness)
	}
	return ss[0]
}

func textsOf(s model.Session, role string) []string {
	var out []string
	for _, m := range s.Messages {
		if m.Role == role {
			out = append(out, m.Text)
		}
	}
	return out
}

// TRAE writes its runtime injections as response_item messages under the user
// role. Only user_message confirms a person typed something; read as Codex,
// the notice below became a thing the person said.
func TestTraeInjectedUserRecordsAreNotThePersons(t *testing.T) {
	p := writeTraeRollout(t, t.TempDir(),
		traeMeta,
		`{"timestamp":"2026-10-01T09:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Warning: runtime-generated process limit notice"}]}}`,
		`{"timestamp":"2026-10-01T09:00:01.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the retry loop never back off"}]}}`,
		`{"timestamp":"2026-10-01T09:00:01.200Z","type":"event_msg","payload":{"type":"user_message","message":"why does the retry loop never back off","images":[],"local_images":[],"text_elements":[]}}`,
		`{"timestamp":"2026-10-01T09:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The delay is reset on every attempt."}]}}`,
		`{"timestamp":"2026-10-01T09:00:02.010Z","type":"event_msg","payload":{"type":"agent_message","message":"The delay is reset on every attempt.","phase":null}}`,
		`{"timestamp":"2026-10-01T09:00:03.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","last_agent_message":"The delay is reset on every attempt."}}`,
	)
	s := parseTraeOne(t, p)
	if got, want := textsOf(s, "user"), []string{"why does the retry loop never back off"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("user turns = %q, want %q", got, want)
	}
	if got, want := textsOf(s, "assistant"), []string{"The delay is reset on every attempt."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assistant turns = %q, want %q", got, want)
	}

	// Control: the same file read as Codex keeps the injected record, so the
	// assertion above is about TRAE's rule and not about the fixture.
	cs, err := ParseCodexRollout(p)
	if err != nil || len(cs) != 1 {
		t.Fatalf("codex read: %v, %d sessions", err, len(cs))
	}
	if !strings.Contains(strings.Join(textsOf(cs[0], "user"), "\n"), "process limit notice") {
		t.Fatalf("control: codex rule no longer keeps role=user response items: %q", textsOf(cs[0], "user"))
	}
}

// 0.201.4 confirms a prompt with item_completed/UserMessage and can carry the
// answer only as item_completed/AgentMessage. The older user_message can
// mirror the same prompt, with or without a turn id, and is read once.
func TestTraeItemCompletedDialect(t *testing.T) {
	p := writeTraeRollout(t, t.TempDir(),
		traeMeta,
		// The mirror in history_mutation is the model-visible copy, not proof.
		`{"timestamp":"2026-10-01T09:00:00.999Z","type":"history_mutation","payload":{"turn_id":"turn-1","operation":"append","items":[{"type":"message","id":"msg-user","role":"user","content":[{"type":"input_text","text":"<environment_context>runtime context</environment_context>"}]}]}}`,
		`{"timestamp":"2026-10-01T09:00:01.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"019fbcca-0000-7000-8000-000000000001","turn_id":"turn-1","item":{"type":"UserMessage","id":"msg-user","content":[{"type":"text","text":"rename the "},{"type":"text","text":"config loader"}]},"completed_at_ms":1790845201000}}`,
		`{"timestamp":"2026-10-01T09:00:01.001Z","type":"event_msg","payload":{"type":"user_message","turn_id":"turn-1","message":"rename the config loader"}}`,
		`{"timestamp":"2026-10-01T09:00:02.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"AgentMessage","id":"msg-agent","content":[{"type":"Text","text":"Renamed it to LoadConfig."}]}}}`,
		`{"timestamp":"2026-10-01T09:00:03.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","last_agent_message":"Renamed it to LoadConfig."}}`,
		// A legacy record without a turn id, then its item mirror.
		`{"timestamp":"2026-10-01T09:01:00.000Z","type":"event_msg","payload":{"type":"user_message","message":"now run the tests"}}`,
		`{"timestamp":"2026-10-01T09:01:00.001Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-2","item":{"type":"UserMessage","id":"msg-user-2","content":[{"type":"text","text":"now run the tests"}]}}}`,
		`{"timestamp":"2026-10-01T09:01:01.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-2","item":{"type":"AgentMessage","id":"msg-agent-2","content":[{"type":"output_text","text":"All green."}]}}}`,
		// The same words in a later turn are a second prompt, not a mirror.
		`{"timestamp":"2026-10-01T09:02:00.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-3","item":{"type":"UserMessage","id":"msg-user-3","content":[{"type":"text","text":"now run the tests"}]}}}`,
		`{"timestamp":"2026-10-01T09:02:00.001Z","type":"event_msg","payload":{"type":"user_message","turn_id":"turn-3","message":"now run the tests"}}`,
	)
	s := parseTraeOne(t, p)
	wantUser := []string{"rename the config loader", "now run the tests", "now run the tests"}
	if got := textsOf(s, "user"); !reflect.DeepEqual(got, wantUser) {
		t.Fatalf("user turns = %q, want %q", got, wantUser)
	}
	wantAsst := []string{"Renamed it to LoadConfig.", "All green."}
	if got := textsOf(s, "assistant"); !reflect.DeepEqual(got, wantAsst) {
		t.Fatalf("assistant turns = %q, want %q", got, wantAsst)
	}
	// In order: the answers come from a fallback source and are merged by time.
	order := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		order = append(order, m.Role)
	}
	if got, want := strings.Join(order, ","), "user,assistant,user,assistant,user"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
}

// Tool calls and results ride in history_mutation appends: the shell is exec
// with an argv, an output is a block list, and a replace re-sends history.
func TestTraeHistoryMutationTools(t *testing.T) {
	p := writeTraeRollout(t, t.TempDir(),
		traeMeta,
		`{"timestamp":"2026-10-01T09:00:01.000Z","type":"event_msg","payload":{"type":"user_message","turn_id":"turn-1","message":"inspect it"}}`,
		`{"timestamp":"2026-10-01T09:00:02.000Z","type":"history_mutation","payload":{"version":1,"commit_id":"commit-1","turn_id":"turn-1","operation":"append","items":[`+
			`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Inspect the repository"}]},`+
			`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"exec","arguments":"{\"command\":[\"bash\",\"-lc\",\"go test ./retry\"]}"},`+
			`{"type":"function_call_output","id":"fco_1","call_id":"call_1","output":[{"type":"input_text","text":"--- FAIL: TestBackoff\n"},{"type":"input_image","image_url":"data:image/png;base64,hidden"}]},`+
			`{"type":"custom_tool_call","id":"fc_2","call_id":"call_2","name":"apply_patch","input":"*** Begin Patch\n*** Update File: retry/backoff.go\n@@\n-\tdelay = base\n+\tdelay *= 2\n*** End Patch\n"},`+
			`{"type":"custom_tool_call_output","id":"fco_2","call_id":"call_2","output":[{"type":"output_text","text":"Done!"}]},`+
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Doubled the delay."}]}]}}`,
		// The same call again as a response_item, and a replace of history.
		`{"timestamp":"2026-10-01T09:00:02.100Z","type":"response_item","payload":{"type":"function_call","call_id":"call_1","name":"exec","arguments":"{\"command\":[\"bash\",\"-lc\",\"go test ./retry\"]}"}}`,
		`{"timestamp":"2026-10-01T09:00:02.200Z","type":"history_mutation","payload":{"turn_id":"turn-1","operation":"replace","items":[{"type":"function_call","call_id":"call_9","name":"exec","arguments":"{\"command\":[\"bash\",\"-lc\",\"go vet ./...\"]}"}]}}`,
	)
	s := parseTraeOne(t, p)
	if got, want := textsOf(s, RoleCommand), []string{"$ go test ./retry"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	if got := strings.Join(textsOf(s, RoleToolOutput), "\n"); !strings.Contains(got, "FAIL: TestBackoff") || strings.Contains(got, "base64") {
		t.Fatalf("tool output = %q", got)
	}
	if got := strings.Join(textsOf(s, RoleFiles), "\n"); !strings.Contains(got, "retry/backoff.go") {
		t.Fatalf("files = %q, want the patched file", got)
	}
	if got, want := textsOf(s, "assistant"), []string{"Doubled the delay."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assistant = %q, want %q", got, want)
	}
}

// The store sits under ~/.trae/cli, archived sessions included, and is read as
// trae, never as codex.
func TestTraeStoreIsItsOwnHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TRAE_HOME", "")
	t.Setenv("DEJA_TRAE_ROOT", "")
	t.Setenv("TRAECLI_HOME", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("DEJA_CODEX_ROOT", "")
	t.Setenv("DEJA_XCODE_CODEX_ROOT", "")
	turn := `{"timestamp":"2026-10-01T09:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"hello trae"}}`
	root := filepath.Join(home, ".trae", "cli")
	if got := TraeRoot(); got != root {
		t.Fatalf("TraeRoot = %q, want %q", got, root)
	}
	live := writeTraeRollout(t, filepath.Join(root, "sessions", "2026", "10", "01"), traeMeta, turn)
	archived := filepath.Join(root, "archived_sessions", "rollout-2026-09-30T09-00-00-019fbcca-0000-7000-8000-000000000002.jsonl")
	if err := os.MkdirAll(filepath.Dir(archived), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archived, []byte(strings.Replace(traeMeta, "000000000001", "000000000002", 1)+"\n"+turn+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss := LoadTrae()
	if len(ss) != 2 {
		t.Fatalf("sessions = %d, want 2", len(ss))
	}
	for _, s := range ss {
		if s.Harness != "trae" {
			t.Fatalf("harness = %q", s.Harness)
		}
	}
	if n := len(LoadCodex()); n != 0 {
		t.Fatalf("codex read %d TRAE sessions", n)
	}
	kind := ""
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			if k.Match(live) {
				kind = k.Name
			}
		}
	}
	if kind != "trae" {
		t.Fatalf("kind for %s = %q, want trae", live, kind)
	}

	// TRAE_HOME moves the store, DEJA_TRAE_ROOT overrides the cli directory.
	t.Setenv("TRAE_HOME", filepath.Join(home, "elsewhere"))
	if got, want := TraeRoot(), filepath.Join(home, "elsewhere", "cli"); got != want {
		t.Fatalf("TraeRoot with TRAE_HOME = %q, want %q", got, want)
	}
	t.Setenv("DEJA_TRAE_ROOT", filepath.Join(home, "override"))
	if got, want := TraeRoot(), filepath.Join(home, "override"); got != want {
		t.Fatalf("TraeRoot with DEJA_TRAE_ROOT = %q, want %q", got, want)
	}
}

// An append that holds the second record of a prompt whose first was read
// before the offset is read whole; one that starts a new prompt is not.
func TestTraeResumesWholeOnSplitMirror(t *testing.T) {
	dir := t.TempDir()
	head := []string{
		traeMeta,
		`{"timestamp":"2026-10-01T09:00:01.000Z","type":"event_msg","payload":{"type":"user_message","turn_id":"turn-1","message":"rename the config loader"}}`,
	}
	p := writeTraeRollout(t, dir, head...)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	offset := fi.Size()
	appendLine := func(line string) {
		f, openErr := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer f.Close()
		if _, werr := f.WriteString(line + "\n"); werr != nil {
			t.Fatal(werr)
		}
	}
	appendLine(`{"timestamp":"2026-10-01T09:00:01.001Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"UserMessage","id":"m1","content":[{"type":"text","text":"rename the config loader"}]}}}`)
	if traeResumes(p, offset) {
		t.Fatal("a tail holding the mirror of a prompt before the offset resumed on its own")
	}

	// Control: a new prompt with both of its records in the tail resumes.
	p2 := writeTraeRollout(t, filepath.Join(dir, "b"), head...)
	p = p2
	appendLine(`{"timestamp":"2026-10-01T09:05:00.000Z","type":"event_msg","payload":{"type":"user_message","turn_id":"turn-2","message":"now run the tests"}}`)
	appendLine(`{"timestamp":"2026-10-01T09:05:00.001Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-2","item":{"type":"UserMessage","id":"m2","content":[{"type":"text","text":"now run the tests"}]}}}`)
	if !traeResumes(p, offset) {
		t.Fatal("a tail with a whole new prompt was sent back for a full read")
	}
	ss, err := ParseTraeRolloutFromOffset(p, offset)
	if err != nil || len(ss) != 1 {
		t.Fatalf("tail read: %v, %d sessions", err, len(ss))
	}
	if got, want := textsOf(ss[0], "user"), []string{"now run the tests"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tail user turns = %q, want %q", got, want)
	}
}

// TRAECLI_HOME is TRAE CLI's own state directory: `TRAECLI_HOME=x traex
// archive <id>` moves the rollout to x/archived_sessions, and `traex doctor`
// reports it beside TRAE_HOME (traecli 0.207.1). DEJA_TRAE_ROOT still wins.
func TestTraeRootFollowsTraecliHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TRAE_HOME", filepath.Join(home, "th"))
	t.Setenv("DEJA_TRAE_ROOT", "")
	cli := filepath.Join(home, "state")
	t.Setenv("TRAECLI_HOME", cli)
	if got := TraeRoot(); got != cli {
		t.Fatalf("TraeRoot with TRAECLI_HOME = %q, want %q", got, cli)
	}
	turn := `{"timestamp":"2026-10-01T09:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"hello trae"}}`
	writeTraeRollout(t, filepath.Join(cli, "archived_sessions"), traeMeta, turn)
	if n := len(LoadTrae()); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
	t.Setenv("DEJA_TRAE_ROOT", filepath.Join(home, "override"))
	if got, want := TraeRoot(), filepath.Join(home, "override"); got != want {
		t.Fatalf("TraeRoot with DEJA_TRAE_ROOT = %q, want %q", got, want)
	}
}

// TRAE's binary ships Claude Code's tool set (Bash, Edit, Write, Read) next to
// Codex's, so a call can arrive under either name. Both must leave the
// command, the edited span and the file in the index.
func TestTraeClaudeStyleTools(t *testing.T) {
	p := writeTraeRollout(t, t.TempDir(),
		traeMeta,
		`{"timestamp":"2026-10-01T09:00:01.000Z","type":"event_msg","payload":{"type":"user_message","turn_id":"turn-1","message":"fix the retry"}}`,
		`{"timestamp":"2026-10-01T09:00:02.000Z","type":"response_item","payload":{"type":"function_call","call_id":"call_1","name":"Bash","arguments":"{\"command\":\"go test ./retry\"}"}}`,
		`{"timestamp":"2026-10-01T09:00:03.000Z","type":"response_item","payload":{"type":"function_call","call_id":"call_2","name":"Edit","arguments":"{\"file_path\":\"/repo/retry/backoff.go\",\"old_string\":\"delay = base\",\"new_string\":\"delay *= 2\"}"}}`,
		`{"timestamp":"2026-10-01T09:00:04.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_2","output":"The file was edited."}}`,
	)
	s := parseTraeOne(t, p)
	if got, want := textsOf(s, RoleCommand), []string{"$ go test ./retry"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	if got := strings.Join(textsOf(s, RoleFiles), "\n"); !strings.Contains(got, "retry/backoff.go") {
		t.Fatalf("files = %q, want the edited file", got)
	}
	if got := strings.Join(textsOf(s, RoleEdit), "\n"); !strings.Contains(got, "delay = base") {
		t.Fatalf("edits = %q, want the replaced span", got)
	}
}

// traecli 0.208 writes no user_message and no item_completed UserMessage: the
// prompt is only in history_mutation's display_completions, next to the
// model-visible copy in items. Shaped from a 0.208.1-alpha.5 exec rollout.
func TestTraeDisplayCompletionsCarryThePrompt(t *testing.T) {
	lines := []string{
		traeMeta,
		`{"timestamp":"2026-10-01T09:00:00.500Z","type":"history_mutation","payload":{"version":1,"turn_id":"turn-1","operation":"append","items":[{"type":"message","id":"msg-env","role":"user","content":[{"type":"input_text","text":"<environment_context>runtime context</environment_context>"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["environments.environment_context"]}}]}}`,
		`{"timestamp":"2026-10-01T09:00:01.000Z","type":"history_mutation","payload":{"display_completions":[{"thread_id":"019fbcca-0000-7000-8000-000000000001","turn_id":"turn-1","item":{"type":"UserMessage","id":"msg-user","content":[{"type":"text","text":"why does the retry loop never back off","text_elements":[]}]},"started_at_ms":1790845201000,"completed_at_ms":1790845201000}],"version":1,"turn_id":"turn-1","item_metadata":[{"user_input_order":0,"item_index":0}],"operation":"append","items":[{"type":"message","id":"msg-user","role":"user","content":[{"type":"input_text","text":"why does the retry loop never back off"}]}]}}`,
		`{"timestamp":"2026-10-01T09:00:02.000Z","type":"history_mutation","payload":{"display_completions":[{"turn_id":"turn-1","item":{"type":"AgentMessage","id":"msg-a","content":[{"type":"Text","text":"The delay is reset on every attempt."}],"phase":"final_answer"}}],"version":1,"turn_id":"turn-1","operation":"append","items":[{"type":"message","id":"msg-a","role":"assistant","content":[{"type":"output_text","text":"The delay is reset on every attempt."}],"phase":"final_answer"}]}}`,
		`{"timestamp":"2026-10-01T09:00:03.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","last_agent_message":"The delay is reset on every attempt."}}`,
	}
	s := parseTraeOne(t, writeTraeRollout(t, t.TempDir(), lines...))
	if got, want := textsOf(s, "user"), []string{"why does the retry loop never back off"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("user turns = %q, want %q", got, want)
	}
	if got, want := textsOf(s, "assistant"), []string{"The delay is reset on every attempt."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assistant turns = %q, want %q", got, want)
	}

	// The same prompt again from item_completed, as 0.201.4 wrote it, is one
	// prompt, not two.
	both := lines[:3:3]
	both = append(both, `{"timestamp":"2026-10-01T09:00:01.001Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"UserMessage","id":"msg-user","content":[{"type":"text","text":"why does the retry loop never back off"}]}}}`)
	both = append(both, lines[3:]...)
	s = parseTraeOne(t, writeTraeRollout(t, t.TempDir(), both...))
	if got := textsOf(s, "user"); len(got) != 1 {
		t.Fatalf("user turns with item_completed too = %q, want one", got)
	}
}

// A cline or roo task is one JSON document: if it will not unmarshal, nothing
// from that file is indexed. Counting it as one malformed *line* said "1 line
// could not be read" about three thousand turns, while the bucket that fits —
// a path deja could not read, with the parser's words beside it — was right
// there (#2232).
func TestAWholeTaskThatWillNotParseIsReportedAsAPath(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("[")
	for i := range 200 {
		fmt.Fprintf(&b, `{"ts":%d,"type":"say","say":"text","text":"turn %d about the pool"},`, 1782900000+i, i)
	}
	truncated := strings.TrimSuffix(b.String(), ",") // no closing bracket

	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	modern := filepath.Join(sessions, "abc.messages.json")
	if err := os.WriteFile(modern, []byte(truncated), 0o644); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(dir, "tasks", "123")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(task, "api_conversation_history.json")
	if err := os.WriteFile(legacy, []byte(truncated), 0o644); err != nil {
		t.Fatal(err)
	}
	rooTask := filepath.Join(dir, "roo", "tasks", "456")
	if err := os.MkdirAll(rooTask, 0o755); err != nil {
		t.Fatal(err)
	}
	roo := filepath.Join(rooTask, "api_conversation_history.json")
	if err := os.WriteFile(roo, []byte(truncated), 0o644); err != nil {
		t.Fatal(err)
	}

	DiagSnapshot() // start from nothing
	if _, err := ParseClineFile(modern); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseClineFile(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRooTask(roo); err != nil {
		t.Fatal(err)
	}
	malformed, failed := DiagSnapshot()

	for _, p := range []string{modern, legacy, roo} {
		if n := malformed[p]; n != 0 {
			t.Errorf("%s: counted as %d malformed line(s); it is a whole file", filepath.Base(p), n)
		}
		if failed[p] == "" {
			t.Errorf("%s: not reported as a path deja could not read", filepath.Base(p))
			continue
		}
		// The parser's own words travel with it, so `doctor --json` says what
		// was wrong rather than only that something was.
		if !strings.Contains(failed[p], "JSON") && !strings.Contains(failed[p], "json") {
			t.Errorf("%s: the recorded reason says nothing about the parse: %q", filepath.Base(p), failed[p])
		}
	}
}

// A commit that only adds lines has no replaced side, so what a session wrote
// is the only evidence those lines were ever in one. Three readers counted
// their replace tool as an edit and their write tool as a path only, so a file
// created wholesale left nothing to attribute from (#595, #3810).
func TestWholeFileWritesAreRecordedAsWritten(t *testing.T) {
	const line = "// Acquire waits for a free connection or the context's deadline."
	const second = "func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {"
	h, ok := HashWrittenLine(line)
	if !ok {
		t.Fatal("the fixture line is too short to be evidence")
	}
	h2, ok := HashWrittenLine(second)
	if !ok {
		t.Fatal("the fixture line is too short to be evidence")
	}
	want := strconv.FormatUint(h, 16) + " " + strconv.FormatUint(h2, 16)

	t.Run("copilot creates a file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "session-state", "s1")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"session.start","sessionId":"s1","timestamp":"2026-09-20T09:00:00Z","cwd":"/w/app"}
{"type":"tool.execution_start","timestamp":"2026-09-20T09:00:01Z","data":{"toolName":"create","arguments":{"path":"/w/app/internal/pool/pool.go","file_text":"` + line + `\n` + second + `\n"}}}
`
		p := filepath.Join(dir, "events.jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseCopilotFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%v %#v", err, ss)
		}
		assertWroteHashes(t, ss[0].Messages, "/w/app/internal/pool/pool.go", want)
	})

	t.Run("cursor writes a file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "projects", "Users-me-app", "agent-transcripts", "s-write")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"role":"user","message":{"content":[{"type":"text","text":"add the pool"}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"path":"/w/app/internal/pool/pool.go","contents":"` + line + `\n` + second + `\n"}}]}}
`
		p := filepath.Join(dir, "s-write.jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseCursorTranscript(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%v %#v", err, ss)
		}
		assertWroteHashes(t, ss[0].Messages, "/w/app/internal/pool/pool.go", want)
	})

	t.Run("kimi writes a file", func(t *testing.T) {
		_, wire := kimiFixture(t)
		body := kimiWireHead + `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t1","name":"Write","args":{"path":"/w/proj/internal/pool/pool.go","content":"` + line + `\n` + second + `\n"}},"time":1782295203900}
`
		if err := os.WriteFile(wire, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseKimiFile(wire)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%v %#v", err, ss)
		}
		assertWroteHashes(t, ss[0].Messages, "/w/proj/internal/pool/pool.go", want)
	})
}

// assertWroteHashes pins the record to exactly the lines of the fixture: a
// record holding anything else would attribute a line the session never wrote.
func assertWroteHashes(t *testing.T, msgs []model.Message, path, want string) {
	t.Helper()
	var wrote []string
	for _, m := range msgs {
		if m.Role == RoleWrote {
			wrote = append(wrote, m.Text)
		}
	}
	if len(wrote) != 1 {
		t.Fatalf("wrote = %q, want one record", wrote)
	}
	got, rest, ok := strings.Cut(wrote[0], "\n")
	if !ok || got != path {
		t.Fatalf("the record is filed under %q", got)
	}
	if strings.Join(strings.Fields(rest), " ") != want {
		t.Fatalf("hashes = %q, want the written lines %q", rest, want)
	}
}

const (
	packageID      = "vshulcz.deja-vu"
	manifestSchema = "1.12.0"
)

var (
	semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	hashRE   = regexp.MustCompile(`^[A-Fa-f0-9]{64}$`)
)

type scoopDownload struct {
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

type scoopManifest struct {
	Version      string                   `json:"version"`
	Description  string                   `json:"description"`
	Homepage     string                   `json:"homepage"`
	License      string                   `json:"license"`
	Architecture map[string]scoopDownload `json:"architecture"`
	Bin          string                   `json:"bin"`
	Checkver     string                   `json:"checkver"`
	Autoupdate   struct {
		Architecture map[string]scoopDownload `json:"architecture"`
	} `json:"autoupdate"`
}

func TestScoopManifest(t *testing.T) {
	b, err := os.ReadFile("../../packaging/scoop/deja-vu.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest scoopManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	if !semverRE.MatchString(manifest.Version) {
		t.Errorf("version = %q, want stable semantic version", manifest.Version)
	}
	if manifest.Description == "" || manifest.Homepage != "https://vshulcz.github.io/deja-vu/" || manifest.License != "MIT" {
		t.Error("description, homepage, or license is missing")
	}
	if manifest.Bin != "deja.exe" || manifest.Checkver != "github" {
		t.Errorf("bin/checkver = %q/%q", manifest.Bin, manifest.Checkver)
	}
	for architecture, assetArchitecture := range map[string]string{"64bit": "amd64", "arm64": "arm64"} {
		download, ok := manifest.Architecture[architecture]
		if !ok {
			t.Errorf("missing %s download", architecture)
			continue
		}
		wantURL := releaseURL(manifest.Version, assetArchitecture)
		if download.URL != wantURL || !hashRE.MatchString(download.Hash) {
			t.Errorf("%s download = %#v, want URL %q and SHA-256", architecture, download, wantURL)
		}
		update, ok := manifest.Autoupdate.Architecture[architecture]
		wantUpdate := releaseURL("$version", assetArchitecture)
		if !ok || update.URL != wantUpdate {
			t.Errorf("%s autoupdate URL = %q, want %q", architecture, update.URL, wantUpdate)
		}
	}
}

func TestWinGetManifestSet(t *testing.T) {
	files := []struct {
		path         string
		manifestType string
	}{
		{"../../packaging/winget/vshulcz.deja-vu.yaml", "version"},
		{"../../packaging/winget/vshulcz.deja-vu.locale.en-US.yaml", "defaultLocale"},
		{"../../packaging/winget/vshulcz.deja-vu.installer.yaml", "installer"},
	}
	var version string
	for _, file := range files {
		values := topLevelYAML(t, file.path)
		if values["PackageIdentifier"] != packageID || values["ManifestType"] != file.manifestType || values["ManifestVersion"] != manifestSchema {
			t.Errorf("%s has inconsistent identity or manifest metadata: %#v", file.path, values)
		}
		if version == "" {
			version = values["PackageVersion"]
		}
		if values["PackageVersion"] != version {
			t.Errorf("%s version = %q, want %q", file.path, values["PackageVersion"], version)
		}
	}
	if !semverRE.MatchString(version) {
		t.Fatalf("PackageVersion = %q, want stable semantic version", version)
	}

	locale := topLevelYAML(t, files[1].path)
	for _, key := range []string{"PackageLocale", "Publisher", "PackageName", "License", "ShortDescription"} {
		if locale[key] == "" {
			t.Errorf("defaultLocale is missing %s", key)
		}
	}

	b, err := os.ReadFile(files[2].path)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(b)
	for architecture, assetArchitecture := range map[string]string{"x64": "amd64", "arm64": "arm64"} {
		if !strings.Contains(installer, "- Architecture: "+architecture+"\n") || !strings.Contains(installer, "  InstallerUrl: "+releaseURL(version, assetArchitecture)+"\n") {
			t.Errorf("installer is missing %s release URL", architecture)
		}
	}
	if strings.Count(installer, "  InstallerSha256: ") != 2 || strings.Count(installer, "- RelativeFilePath: deja.exe\n") != 1 {
		t.Error("installer must have two hashes and one portable deja.exe entry")
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(installer, "\r\n", "\n"), "\n") {
		if hash, ok := strings.CutPrefix(line, "  InstallerSha256: "); ok && !hashRE.MatchString(hash) {
			t.Errorf("invalid InstallerSha256 %q", hash)
		}
	}
}

func topLevelYAML(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") {
			continue
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func releaseURL(version, architecture string) string {
	return "https://github.com/vshulcz/deja-vu/releases/download/v" + version + "/deja-vu_" + version + "_windows_" + architecture + ".zip"
}

// The Windsurf fixtures are encrypted protobuf, which no reviewer can read.
// This file is their readable source: each trajectory is described below,
// encoded with the field numbers from #308 and sealed the way the language
// server seals one, with a fixed nonce so the bytes are reproducible.
// TestWindsurfFixturesMatchSource fails when a committed .pb drifts from it;
// DEJA_WRITE_WINDSURF_FIXTURES=1 rewrites them.

const (
	windsurfFixtureChat  = "c4f3a2b1-7e6d-4c5b-9a8f-1e2d3c4b5a69"
	windsurfFixtureError = "8a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
)

func windsurfFixtureDir() string {
	return filepath.Join("..", "..", "fixtures", "registry", "windsurf")
}

// A minimal protobuf encoder, the inverse of pbWalk.
type pbMsg []byte

func (m pbMsg) key(num, wire uint64) pbMsg { return binary.AppendUvarint(m, num<<3|wire) }

func (m pbMsg) varint(num, v uint64) pbMsg {
	return binary.AppendUvarint(m.key(num, pbVarint), v)
}

func (m pbMsg) bytes(num uint64, b []byte) pbMsg {
	m = binary.AppendUvarint(m.key(num, pbBytes), uint64(len(b)))
	return append(m, b...)
}

func (m pbMsg) str(num uint64, s string) pbMsg { return m.bytes(num, []byte(s)) }

func (m pbMsg) fixed32(num uint64, v uint32) pbMsg {
	return binary.LittleEndian.AppendUint32(m.key(num, pbFixed32), v)
}

func (m pbMsg) fixed64(num uint64, v uint64) pbMsg {
	return binary.LittleEndian.AppendUint64(m.key(num, pbFixed64), v)
}

func pbStamp(t time.Time) pbMsg {
	m := pbMsg(nil).varint(1, uint64(t.Unix())) //nolint:gosec // fixture times are after 1970
	if t.Nanosecond() != 0 {
		m = m.varint(2, uint64(t.Nanosecond())) //nolint:gosec // nanoseconds are never negative
	}
	return m
}

func windsurfStepMeta(created, completed time.Time) pbMsg {
	m := pbMsg(nil)
	if !created.IsZero() {
		m = m.bytes(1, pbStamp(created))
	}
	if !completed.IsZero() {
		m = m.bytes(8, pbStamp(completed))
	}
	return m
}

func windsurfUserStep(at time.Time, userResponse, query string) pbMsg {
	in := pbMsg(nil)
	if query != "" {
		in = in.str(1, query)
	}
	if userResponse != "" {
		in = in.str(2, userResponse)
	}
	return pbMsg(nil).bytes(5, windsurfStepMeta(at, time.Time{})).bytes(19, in)
}

func windsurfPlannerStep(created, completed time.Time, response, thinking, modified string) pbMsg {
	p := pbMsg(nil).str(1, response)
	if thinking != "" {
		p = p.str(3, thinking)
	}
	if modified != "" {
		p = p.str(8, modified)
	}
	return pbMsg(nil).bytes(5, windsurfStepMeta(created, completed)).bytes(20, p)
}

func windsurfMeta(created time.Time, workspaces ...[2]string) pbMsg {
	m := pbMsg(nil)
	for _, ws := range workspaces {
		w := pbMsg(nil).str(1, ws[0])
		if ws[1] != "" {
			w = w.str(2, ws[1])
		}
		m = m.bytes(1, w.str(4, "main"))
	}
	return m.bytes(2, pbStamp(created))
}

func windsurfSeal(plain []byte, nonce byte) []byte {
	block, err := aes.NewCipher(windsurfKey)
	if err != nil {
		panic(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	// A nonce of one repeated byte made the committed .pb files start with
	// 01 01 01 01, the MMDF mailbox magic, and file scanners stopped there.
	sum := sha256.Sum256([]byte{nonce})
	n := sum[:windsurfNonce]
	return gcm.Seal(n, n, plain, nil)
}

// windsurfChatTrajectory is a finished conversation: a renamed title, a git
// root above the opened folder, thinking that must stay out, a tool step and
// unknown fields of every wire type, and a reply only in modified_response.
func windsurfChatTrajectory() []byte {
	t0 := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	steps := []pbMsg{
		windsurfUserStep(t0.Add(5*time.Second), "why does GET /notes return duplicates after the cursor", ""),
		windsurfPlannerStep(t0.Add(6*time.Second), t0.Add(20*time.Second),
			"The cursor compares created_at, which ties for notes saved in the same second; filter on (created_at, id) instead.",
			"private chain of thought", ""),
		// A tool call deja does not read, beside fields this reader has never
		// seen.
		pbMsg(nil).bytes(5, windsurfStepMeta(t0.Add(21*time.Second), time.Time{})).
			bytes(31, pbMsg(nil).str(1, "run_command").str(2, "go test ./notes")).
			varint(40, 7).fixed32(41, 0xdeadbeef).fixed64(42, 1),
		windsurfUserStep(t0.Add(40*time.Second), "add a test for the tie", ""),
		windsurfPlannerStep(t0.Add(41*time.Second), t0.Add(55*time.Second), "", "",
			"Added TestListCursorTie, which saves two notes in one second and pages through them."),
	}
	m := pbMsg(nil).str(1, "0d9e8f7a-6b5c-4d3e-2f1a-0b9c8d7e6f5a")
	for _, st := range steps {
		m = m.bytes(2, st)
	}
	m = m.varint(4, 1).str(6, windsurfFixtureChat).
		bytes(7, windsurfMeta(t0, [2]string{"file:///home/dev/src/notes-api/server", "file:///home/dev/src/notes-api"})).
		varint(8, 2).str(10, "Cursor pagination duplicates")
	return windsurfSeal(m, 1)
}

// windsurfErrorTrajectory is the shape the live check in #308 produced: the
// service refused the model twice, so there are two prompts, steps holding
// only metadata and an error, and no planner response at all.
func windsurfErrorTrajectory() []byte {
	t0 := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	prompt := "Do not use tools or edit files. Reply exactly with: DEJA_SYNTHETIC_ASSISTANT_01"
	errStep := func(at time.Time) pbMsg {
		return pbMsg(nil).bytes(5, windsurfStepMeta(at, at)).
			bytes(24, pbMsg(nil).str(1, "permission_denied: We're currently facing high demand for this model."))
	}
	m := pbMsg(nil).str(1, "5e4d3c2b-1a09-4f8e-9d7c-6b5a4f3e2d1c").
		bytes(2, windsurfUserStep(t0.Add(time.Second), prompt, "")).
		bytes(2, errStep(t0.Add(3*time.Second))).
		bytes(2, windsurfUserStep(t0.Add(30*time.Second), prompt, "")).
		bytes(2, errStep(t0.Add(32*time.Second))).
		bytes(2, pbMsg(nil).bytes(5, windsurfStepMeta(t0.Add(33*time.Second), time.Time{}))).
		str(6, windsurfFixtureError).
		bytes(7, windsurfMeta(t0, [2]string{"file:///tmp/deja-windsurf-synthetic", ""}))
	return windsurfSeal(m, 2)
}

func TestWindsurfFixturesMatchSource(t *testing.T) {
	for name, want := range map[string][]byte{
		windsurfFixtureChat:  windsurfChatTrajectory(),
		windsurfFixtureError: windsurfErrorTrajectory(),
	} {
		path := filepath.Join(windsurfFixtureDir(), "cascade", name+".pb")
		if os.Getenv("DEJA_WRITE_WINDSURF_FIXTURES") == "1" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s differs from its source in this file; rerun with DEJA_WRITE_WINDSURF_FIXTURES=1", path)
		}
	}
}

// windsurfStore copies the committed fixtures into a fresh root and points
// DEJA_WINDSURF_ROOT at it, so a test can add files beside them.
func windsurfStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DEJA_WINDSURF_ROOT", root)
	dir := filepath.Join(root, "cascade")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{windsurfFixtureChat, windsurfFixtureError} {
		b, err := os.ReadFile(filepath.Join(windsurfFixtureDir(), "cascade", id+".pb"))
		if err != nil {
			t.Fatal(err)
		}
		writeWindsurf(t, root, id, b)
	}
	return root
}

func writeWindsurf(t *testing.T, root, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(root, "cascade", name+".pb")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWindsurfReadsPromptsAndReplies(t *testing.T) {
	root := windsurfStore(t)
	p := filepath.Join(root, "cascade", windsurfFixtureChat+".pb")
	if !isWindsurfTrajectory(p) {
		t.Fatalf("%s not matched", p)
	}
	ss, err := ParseWindsurfFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	s := ss[0]
	if s.ID != windsurfFixtureChat || s.Harness != "windsurf" || s.Title != "Cursor pagination duplicates" {
		t.Fatalf("session = %q %q %q", s.ID, s.Harness, s.Title)
	}
	// The git root, not the subfolder Cascade was opened on.
	if want := projectName("/home/dev/src/notes-api"); s.Project != want {
		t.Fatalf("project = %q, want %q", s.Project, want)
	}
	got := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		got = append(got, m.Role+": "+m.Text)
	}
	want := []string{
		"user: why does GET /notes return duplicates after the cursor",
		"assistant: The cursor compares created_at, which ties for notes saved in the same second; filter on (created_at, id) instead.",
		"user: add a test for the tie",
		// response is empty on this step, so modified_response stands in.
		"assistant: Added TestListCursorTie, which saves two notes in one second and pages through them.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages:\n%s", strings.Join(got, "\n"))
	}
	t0 := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	if !s.Started.Equal(t0) || !s.Updated.Equal(t0.Add(55*time.Second)) {
		t.Fatalf("started %v updated %v", s.Started, s.Updated)
	}
	if !s.Messages[0].Time.Equal(t0.Add(5*time.Second)) || !s.Messages[1].Time.Equal(t0.Add(20*time.Second)) {
		t.Fatalf("message times %v %v", s.Messages[0].Time, s.Messages[1].Time)
	}
	for _, m := range s.Messages {
		if strings.Contains(m.Text, "chain of thought") || strings.Contains(m.Text, "go test") {
			t.Fatalf("thinking or a tool call was indexed: %q", m.Text)
		}
	}
}

func TestWindsurfErrorOnlyTrajectoryKeepsItsPrompts(t *testing.T) {
	root := windsurfStore(t)
	ss, err := ParseWindsurfFile(filepath.Join(root, "cascade", windsurfFixtureError+".pb"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	s := ss[0]
	if len(s.Messages) != 2 || s.Messages[0].Role != "user" || s.Messages[1].Role != "user" {
		t.Fatalf("messages = %#v", s.Messages)
	}
	if !strings.HasPrefix(s.Title, "Do not use tools") || s.Project != projectName("/tmp/deja-windsurf-synthetic") {
		t.Fatalf("title %q project %q", s.Title, s.Project)
	}
}

// A tampered trajectory is skipped and counted; it costs only itself, and the
// count is what doctor and the index run report.
func TestWindsurfTamperedTrajectoryIsSkippedAndCounted(t *testing.T) {
	root := windsurfStore(t)
	if got := SkipReason("windsurf"); got != "" {
		t.Fatalf("clean store reports %q", got)
	}
	b := windsurfChatTrajectory()
	b[len(b)-1] ^= 0x01
	bad := writeWindsurf(t, root, "0f0e0d0c-tampered", b)
	if ss, err := ParseWindsurfFile(bad); len(ss) != 0 || err != nil {
		t.Fatalf("tampered tag: %d sessions, err %v", len(ss), err)
	}
	if got := DiagFailedPaths()[bad]; got != errWindsurfAuth.Error() {
		t.Fatalf("tampered file recorded as %q, want the authentication failure", got)
	}
	// A file too short to hold a nonce and a tag is refused the same way.
	writeWindsurf(t, root, "short", []byte("not a trajectory"))
	ss := LoadWindsurf()
	if len(ss) != 2 {
		t.Fatalf("loaded %d sessions, want the two good ones", len(ss))
	}
	if n := WindsurfAuthFailures(); n != 2 {
		t.Fatalf("auth failures = %d, want 2", n)
	}
	if got := SkipReason("windsurf"); got != "2 trajectories failed authentication" {
		t.Fatalf("skip reason = %q", got)
	}
}

// Authenticated bytes that are not a trajectory are a decode error, not an
// authentication failure, and do not stop the store.
func TestWindsurfMalformedTrajectoryDoesNotStopTheStore(t *testing.T) {
	root := windsurfStore(t)
	whole := pbMsg(nil).str(6, "x").bytes(2, pbMsg(nil).bytes(19, pbMsg(nil).str(2, "hello")))
	truncated := windsurfSeal(whole[:len(whole)-3], 3)
	p := writeWindsurf(t, root, "truncated", truncated)
	if ss, err := ParseWindsurfFile(p); len(ss) != 0 || err != nil {
		t.Fatalf("truncated: %d sessions, err %v", len(ss), err)
	}
	if got := DiagFailedPaths()[p]; !strings.HasPrefix(got, "decode trajectory") {
		t.Fatalf("truncated file recorded as %q, want a decode error", got)
	}
	group := windsurfSeal(pbMsg(nil).key(9, 3), 4)
	writeWindsurf(t, root, "group", group)
	if ss := LoadWindsurf(); len(ss) != 2 {
		t.Fatalf("loaded %d sessions, want the two good ones", len(ss))
	}
	if n := WindsurfAuthFailures(); n != 0 {
		t.Fatalf("auth failures = %d, want 0", n)
	}
}

func TestWindsurfFallbacks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_WINDSURF_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "cascade"), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// No cascade_id, no git root, a Windows workspace, and a prompt only in
	// query.
	m := pbMsg(nil).
		bytes(2, windsurfUserStep(at, "", "rename the flag")).
		bytes(7, windsurfMeta(at, [2]string{"file:///C:/Users/dev/src/cli%20tool", ""}))
	p := writeWindsurf(t, root, "named-by-file", windsurfSeal(m, 5))
	ss, err := ParseWindsurfFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	s := ss[0]
	if s.ID != "named-by-file" || s.Title != "rename the flag" || s.Messages[0].Text != "rename the flag" {
		t.Fatalf("session = %q %q %#v", s.ID, s.Title, s.Messages)
	}
	if s.Project != "src/cli tool" {
		t.Fatalf("project = %q, want the path the file URI names", s.Project)
	}
	// Nothing typed and nothing answered is no session.
	empty := writeWindsurf(t, root, "empty", windsurfSeal(pbMsg(nil).str(6, "e"), 6))
	if ss, err := ParseWindsurfFile(empty); err != nil || len(ss) != 0 {
		t.Fatalf("empty trajectory: %d sessions, err %v", len(ss), err)
	}
	// implicit/ and brain/ are not conversations.
	if isWindsurfTrajectory(filepath.Join(root, "implicit", "x.pb")) {
		t.Fatal("implicit trajectory matched")
	}
}

func TestWindsurfStringsComeOutAsText(t *testing.T) {
	if got := pbString([]byte("a\x00b\xff")); got != "ab�" {
		t.Fatalf("pbString = %q", got)
	}
}

// WorkBuddy AI, the edition workbuddy.ai serves, keeps its home in
// ~/.workbuddy-ai (dataFolderName in the app's cli/product.json); the
// workbuddy.cn edition keeps ~/.workbuddy. Both stores are read.
func TestWorkBuddyAIStoreIsRead(t *testing.T) {
	home := codeBuddyEnv(t)
	intl := filepath.Join(home, ".workbuddy-ai", "projects", "repo", "s1.jsonl")
	writeCodeBuddyFile(t, intl, "{}\n")

	if got := CodeBuddySessionFiles(); len(got) != 1 || got[0] != intl {
		t.Fatalf("session files = %v, want [%s]", got, intl)
	}
	if !IsWorkBuddyTranscript(intl) || !IsCodeBuddyTranscript(intl) {
		t.Fatalf("%s not recognised as WorkBuddy's", intl)
	}
	if got := WorkBuddyConfigDir(); got != filepath.Join(home, ".workbuddy-ai") {
		t.Fatalf("WorkBuddyConfigDir = %q with only ~/.workbuddy-ai on disk", got)
	}

	cn := filepath.Join(home, ".workbuddy", "projects", "repo", "s2.jsonl")
	writeCodeBuddyFile(t, cn, "{}\n")
	if got := CodeBuddySessionFiles(); len(got) != 2 {
		t.Fatalf("both editions: session files = %v", got)
	}

	// WORKBUDDY_CONFIG_DIR names the one home, as it does for the app.
	moved := filepath.Join(home, "wb")
	t.Setenv("WORKBUDDY_CONFIG_DIR", moved)
	if err := os.MkdirAll(filepath.Join(moved, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CodeBuddyRoots(); len(got) != 2 || got[1] != filepath.Join(moved, "projects") {
		t.Fatalf("WORKBUDDY_CONFIG_DIR roots = %v", got)
	}
}

// WorkBuddy AI creates ~/.workbuddy/device-id and logs on its first launch,
// next to its real home ~/.workbuddy-ai. The home deja wires is the one
// holding the app's database, not the first directory that exists (#4724).
func TestWorkBuddyConfigDirSkipsTheOtherEditionsStrayDir(t *testing.T) {
	home := codeBuddyEnv(t)
	writeCodeBuddyFile(t, filepath.Join(home, ".workbuddy", "device-id"), "0f00\n")
	writeCodeBuddyFile(t, filepath.Join(home, ".workbuddy-ai", "workbuddy.db"), "")
	if got, want := WorkBuddyConfigDir(), filepath.Join(home, ".workbuddy-ai"); got != want {
		t.Fatalf("WorkBuddyConfigDir = %q, want %q", got, want)
	}

	// Both editions in use: the first one, as before.
	writeCodeBuddyFile(t, filepath.Join(home, ".workbuddy", "workbuddy.db"), "")
	if got, want := WorkBuddyConfigDir(), filepath.Join(home, ".workbuddy"); got != want {
		t.Fatalf("both editions: WorkBuddyConfigDir = %q, want %q", got, want)
	}
}

// The written side goes wherever the replaced side already is, and it has to
// arrive under the key each harness actually uses: Copilot calls it new_str,
// and reading new_string there records nothing while looking like it works.
func TestEveryDialectReadsItsOwnWrittenKey(t *testing.T) {
	const written = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	want, ok := HashWrittenLine(written)
	if !ok {
		t.Fatal("the line is not evidence")
	}
	for _, c := range []struct {
		name    string
		dialect toolDialect
		call    map[string]any
	}{
		{"claude", claudeDialect, map[string]any{
			"type": "tool_use", "name": "Edit",
			"input": map[string]any{"file_path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
		{"cursor", cursorDialect, map[string]any{
			"type": "tool_use", "name": "StrReplace",
			"input": map[string]any{"path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
		{"copilot", copilotDialect, map[string]any{
			"type": "tool_use", "name": "edit",
			"input": map[string]any{"path": "/w/pool.go", "old_str": "x", "new_str": written},
		}},
		{"qwen", qwenDialect, map[string]any{
			"type": "tool_use", "name": "replace",
			"input": map[string]any{"file_path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
		{"kimi", kimiDialect, map[string]any{
			"type": "tool_use", "name": "Edit",
			"input": map[string]any{"path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
		{"codewhale", codeWhaleDialect, map[string]any{
			"type": "tool_use", "name": "str_replace",
			"input": map[string]any{"path": "/w/pool.go", "old_string": "x", "new_string": written},
		}},
	} {
		got := wroteRecordsIn([]any{c.call}, c.dialect)
		if len(got) == 0 {
			t.Errorf("%s: the call wrote a line and nothing was recorded", c.name)
			continue
		}
		if _, has := WroteRecordHas(got[0], want); !has {
			t.Errorf("%s: the record does not hold the written line: %q", c.name, got[0])
		}
		if strings.Contains(got[0], "default_pool_size") {
			t.Errorf("%s: the record carries the text rather than a hash", c.name)
		}
	}
}

// Codex has no Edit tool: every change it makes is an apply_patch, and the
// payload carries both sides. On the store this was read against there was
// exactly one such call, which is why the reader is pinned by a fixture rather
// than by that store.
func TestCodexRecordsBothSidesOfAPatch(t *testing.T) {
	const removed = "cfg.MaxConns = 10 // the number from before this change"
	const added = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	payload := map[string]any{
		"name": "apply_patch",
		"input": "*** Begin Patch\n*** Update File: internal/pool/pool.go\n@@\n-" + removed +
			"\n+" + added + "\n*** End Patch",
	}
	var s model.Session
	codexPatch(&s, payload, "/w", time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC))

	var gotEdit, gotWrote string
	for _, m := range s.Messages {
		switch m.Role {
		case RoleEdit:
			gotEdit = m.Text
		case RoleWrote:
			gotWrote = m.Text
		}
	}
	if !strings.Contains(gotEdit, removed) {
		t.Errorf("the replaced span is not what the patch removed: %q", gotEdit)
	}
	h, _ := HashWrittenLine(added)
	path, has := WroteRecordHas(gotWrote, h)
	if !has {
		t.Errorf("the written record does not hold the added line: %q", gotWrote)
	}
	// Paths in a Codex patch are relative to the session's cwd, and blame
	// compares the tail of the path it is given. The record keeps the host's
	// separator, and recordNamesFile folds them before comparing, so this
	// folds them too rather than pinning the reader to one platform.
	if filepath.ToSlash(path) != "/w/internal/pool/pool.go" {
		t.Errorf("the record is filed under %q", path)
	}
	if old, _ := HashWrittenLine(removed); func() bool { _, ok := WroteRecordHas(gotWrote, old); return ok }() {
		t.Error("a removed line was hashed as written")
	}
}

func TestTheWrittenSideIsHashedNotStored(t *testing.T) {
	const line = "raised default_pool_size to 40 in pgbouncer.ini"
	rec := WroteRecord("/w/pool.go", line+"\n}\n\treturn nil\n"+line)
	if rec == "" {
		t.Fatal("a write of a line long enough to be evidence produced no record")
	}
	path, rest, ok := strings.Cut(rec, "\n")
	if !ok || path != "/w/pool.go" {
		t.Fatalf("record does not start with its path: %q", rec)
	}
	// None of the text itself: the written side is the one place a secret an
	// agent typed into a file would otherwise land in a second store.
	if strings.Contains(rest, "pool_size") || strings.Contains(rest, "pgbouncer") {
		t.Errorf("the record carries the written text: %q", rest)
	}
	// One hash: the short lines are below the floor and the repeat is a repeat.
	if got := len(strings.Fields(rest)); got != 1 {
		t.Errorf("record holds %d hashes, want 1: %q", got, rest)
	}

	h, ok := HashWrittenLine(line)
	if !ok {
		t.Fatal("the line is not hashable")
	}
	gotPath, has := WroteRecordHas(rec, h)
	if !has || gotPath != "/w/pool.go" {
		t.Errorf("the record does not answer for the line it holds: %q %v", gotPath, has)
	}
	other, _ := HashWrittenLine("something else entirely, long enough to count")
	if _, has := WroteRecordHas(rec, other); has {
		t.Error("the record answers for a line nobody wrote")
	}
}

func TestAShortLineIsNotEvidence(t *testing.T) {
	// `}` and `return nil` are in every commit and in every session, so a match
	// on them attributes a line to whichever session happened to be indexed.
	for _, line := range []string{"}", "\treturn nil", "if err != nil {", "package main"} {
		if _, ok := HashWrittenLine(line); ok {
			t.Errorf("%q counts as evidence; it is %d runes folded", line, len([]rune(WrittenLineKey(line))))
		}
	}
	// And the floor is on the folded form, so indentation cannot lift a line
	// over it.
	long := strings.Repeat(" ", 40) + "short"
	if _, ok := HashWrittenLine(long); ok {
		t.Error("whitespace lifted a short line over the floor")
	}
}

func TestTheWrittenKeyIsTheSameOnBothSides(t *testing.T) {
	// A written line and the git diff line it becomes differ in leading
	// whitespace and nothing else; if the two normalisations drift, attribution
	// silently stops working and looks like a ranking problem.
	written := "\t\tif err := sources.StoresSelectionError(); err != nil {"
	diff := "                if err := sources.StoresSelectionError(); err != nil {"
	a, ok1 := HashWrittenLine(written)
	b, ok2 := HashWrittenLine(diff)
	if !ok1 || !ok2 || a != b {
		t.Fatalf("the same line hashes differently: %v %v %d %d", ok1, ok2, a, b)
	}
}

func TestClaudeWritesAreRecordedForEveryToolShape(t *testing.T) {
	const newLine = "pool, err := pgxpool.NewWithConfig(ctx, cfg)"
	const contentLine = "// Package pool holds the database pool and its configuration."
	const multiLine = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	raw := `[
	  {"type":"tool_use","input":{"file_path":"/w/pool.go","old_string":"x","new_string":"` + newLine + `"}},
	  {"type":"tool_use","input":{"file_path":"/w/doc.go","content":"` + contentLine + `"}},
	  {"type":"tool_use","input":{"file_path":"/w/cfg.go","edits":[{"old_string":"y","new_string":"` + multiLine + `"}]}}
	]`
	got := claudeWroteRecords([]byte(raw))
	if len(got) != 3 {
		t.Fatalf("got %d records, want one per call: %q", len(got), got)
	}
	for i, want := range []string{newLine, contentLine, multiLine} {
		h, ok := HashWrittenLine(want)
		if !ok {
			t.Fatalf("%q is not evidence", want)
		}
		if _, has := WroteRecordHas(got[i], h); !has {
			t.Errorf("record %d does not hold the line the call wrote: %q", i, got[i])
		}
	}

	// The reference parser has to agree with the decoder, the way the replaced
	// side already does: a store read through one and not the other attributes
	// on some machines and not others.
	var content []any
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatal(err)
	}
	ref := wroteRecordsFromContent(content)
	if strings.Join(ref, "|") != strings.Join(got, "|") {
		t.Errorf("the two parsers disagree:\n decoder %q\n reference %q", got, ref)
	}
}

func TestTheAddedSideOfAPatchIsRecorded(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: internal/pool/pool.go
@@
-	cfg.MaxConns = 10
+	cfg.MaxConns = int32(size) // default_pool_size, from the ini
*** End Patch`
	got := addedLinesOfPatch(patch)
	if len(got) != 1 {
		t.Fatalf("got %d records: %q", len(got), got)
	}
	h, _ := HashWrittenLine("cfg.MaxConns = int32(size) // default_pool_size, from the ini")
	path, has := WroteRecordHas(got[0], h)
	if !has {
		t.Errorf("the added line is not in the record: %q", got[0])
	}
	if path != "internal/pool/pool.go" {
		t.Errorf("record is about %q", path)
	}
	// And the removed side is not: it is the other rule's evidence, and a hash
	// of it would attribute a line that no longer exists.
	old, _ := HashWrittenLine("cfg.MaxConns = 10 and some more text to clear the floor")
	if _, has := WroteRecordHas(got[0], old); has {
		t.Error("a removed line was hashed as written")
	}
}

func TestTheWrittenSideCanBeTurnedOff(t *testing.T) {
	t.Setenv("DEJA_INDEX_WRITES", "0")
	if IndexWrites() {
		t.Error("DEJA_INDEX_WRITES=0 leaves the written side on")
	}
	t.Setenv("DEJA_INDEX_WRITES", "")
	if !IndexWrites() {
		t.Error("the written side is off by default; attribution nobody enables attributes nothing")
	}
}

// Before the current runtime ZCode kept each conversation as
// ~/.zcode/v2/sessions/<workspaceHash>/<taskId>.json, {meta, messages}, the
// shape the runtime's own restore-legacy-sessions skill scans. Those stay on
// disk until restored by hand, and deja read neither directory (#4432).
func TestZCodeReadsLegacySessionSnapshots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent"))
	t.Setenv("DEJA_ZCODE_DB", filepath.Join(home, "absent.sqlite"))
	dir := filepath.Join(home, ".zcode", "v2", "sessions", "5f0c1e9a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snap := `{"meta":{"taskId":"task-1","acpSessionId":"acp-9","workspacePath":"/private/tmp/proj","provider":"glm","title":"fix the retry loop","createdAt":1790000000000,"updatedAt":1790000060000},` +
		`"messages":[{"role":"user","content":"fix the retry loop, it never stops","timestamp":1790000000000},` +
		`{"role":"assistant","content":"capped it at five attempts with a 120 second backoff","timestamp":1790000060000}]}`
	live := filepath.Join(dir, "task-1.json")
	gone := filepath.Join(dir, "task-2.deleted.json")
	for p, body := range map[string]string{live: snap, gone: snap} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := ZCodeSessionFiles()
	if len(files) != 1 || files[0] != live {
		t.Fatalf("ZCodeSessionFiles = %v, want only %s: ZCode skips .deleted.json", files, live)
	}
	ss := LoadZCode()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the one snapshot", len(ss))
	}
	s := ss[0]
	// acpSessionId || taskId, the id the restore skill gives the session.
	if s.ID != "acp-9" || s.Harness != "zcode" || s.Project != "tmp/proj" || s.Title != "fix the retry loop" {
		t.Errorf("session = %s %s %q %q, want zcode acp-9 in tmp/proj titled from meta", s.Harness, s.ID, s.Project, s.Title)
	}
	if len(s.Messages) != 2 || s.Messages[1].Role != "assistant" || s.Messages[1].Time.IsZero() {
		t.Errorf("messages = %+v", s.Messages)
	}

	// Restored into the CLI database, the same conversation is read from
	// there, and the snapshot is not a second copy of it.
	if !SQLite3Available() {
		return
	}
	db := filepath.Join(home, "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	seed := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, time_created integer, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('acp-9', '/private/tmp/proj', 1790000000000, 1790000060000);
insert into message values ('m1', 'acp-9', 1790000000000, '{"role":"user","time":{"created":1790000000000}}');
insert into part values ('p1', 'm1', '{"type":"text","text":"fix the retry loop, it never stops"}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, seed).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	n := 0
	for _, s := range LoadZCode() {
		if s.ID == "acp-9" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("acp-9 read %d times once restored, want once", n)
	}
}

// The fingerprint asks every snapshot for its id on every pass once the CLI
// database exists, so the id comes off the meta at the head of the file, not
// from decoding a conversation that can run to megabytes (#4448).
func TestZCodeSnapshotIDIsReadOffTheMeta(t *testing.T) {
	dir := t.TempDir()
	head := filepath.Join(dir, "task-1.json")
	if err := os.WriteFile(head, []byte(`{"meta":{"taskId":"task-1","acpSessionId":"acp-1"},"messages":[{"role":"user","content":"fix the re`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := zcodeLegacyID(head); got != "acp-1" {
		t.Errorf("meta first: id %q, want acp-1", got)
	}
	tail := filepath.Join(dir, "task-2.json")
	if err := os.WriteFile(tail, []byte(`{"messages":[{"role":"user","content":"fix it"}],"Meta":{"taskId":"task-2"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := zcodeLegacyID(tail); got != "task-2" {
		t.Errorf("meta last: id %q, want task-2", got)
	}
}

// A snapshot with no meta is read under its file name, and one that is not a
// JSON object, or breaks off before its meta, has no id.
func TestZCodeSnapshotIDWithoutAReadableMeta(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct{ body, want string }{
		"task-3.json": {`{"messages":[{"role":"user","content":"fix it"}],"title":"x"}`, "task-3"},
		"task-4.json": {`[{"meta":{"taskId":"task-4"}}]`, ""},
		"task-5.json": {`{"messages":[{"role":"user","content":"fix`, ""},
		"task-6.json": {`{"messages"`, ""},
		"task-7.json": {``, ""},
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := zcodeLegacyID(p); got != c.want {
			t.Errorf("%s: id %q, want %q", name, got, c.want)
		}
	}
	if got := zcodeLegacyID(filepath.Join(dir, "gone.json")); got != "" {
		t.Errorf("missing snapshot: id %q, want none", got)
	}
}

// The fingerprint says whether the CLI database holds the session a snapshot
// was restored as, so restoring one re-reads it and the reader drops it
// (#4448). The id is cached per file state: a rewrite is decoded again.
func TestZCodeLegacySidecarFollowsTheRestore(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "task-1.json")
	other := filepath.Join(dir, "task-2.json")
	for p, body := range map[string]string{
		snap:  `{"meta":{"taskId":"task-1","acpSessionId":"acp-9"},"messages":[]}`,
		other: `{"meta":{"taskId":"task-2"},"messages":[]}`,
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(dir, "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	if size, stamp := zcodeLegacySidecar(snap); size != 0 || stamp != 0 {
		t.Errorf("no database = %d/%d, want nothing", size, stamp)
	}
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not available")
	}
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, `create table session (id text primary key); insert into session values ('acp-9');`).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	if size, stamp := zcodeLegacySidecar(snap); size != 1 || stamp != 1 {
		t.Errorf("restored snapshot = %d/%d, want 1/1", size, stamp)
	}
	if size, stamp := zcodeLegacySidecar(snap); size != 1 || stamp != 1 {
		t.Errorf("restored snapshot read again = %d/%d, want 1/1", size, stamp)
	}
	if size, stamp := zcodeLegacySidecar(other); size != 0 || stamp != 0 {
		t.Errorf("snapshot not restored = %d/%d, want nothing", size, stamp)
	}
	if err := os.WriteFile(snap, []byte(`{"meta":{"taskId":"task-1","acpSessionId":"acp-10"},"messages":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(snap, later, later); err != nil {
		t.Fatal(err)
	}
	if size, stamp := zcodeLegacySidecar(snap); size != 0 || stamp != 0 {
		t.Errorf("snapshot rewritten under a new id = %d/%d, want nothing", size, stamp)
	}
}

// ZCode's CLI keeps a SQLite store beside the transcripts and it went unread
// while no sample of the schema was in hand. The schema is OpenCode's — the
// same reader Kilo's CLI uses — attested by zcode-stats 0.8.0, which reads the
// live database and queries `session(directory, task_type, …)`,
// `message(id, session_id, data)` and `part(data)` (#3675).
//
// Skipped without sqlite3, like every other database store here.
func TestZCodeReadsTheCLIDatabase(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not installed")
	}
	home := t.TempDir()
	db := filepath.Join(home, "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent"))

	schema := `create table session (id text primary key, directory text, time_created any, time_updated any);
create table message (id text primary key, session_id text, time_created any, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('zc-1', '/work/api', '2026-09-17T09:00:00Z', '2026-09-17T09:00:02Z');
insert into message values ('m-user', 'zc-1', 1789650001000, '{"role":"user","time":{"created":"2026-09-17T09:00:01Z"}}');
insert into message values ('m-assistant', 'zc-1', 1789650002000, '{"role":"assistant","time":{"created":"2026-09-17T09:00:02Z"}}');
insert into part values ('p-user', 'm-user', '{"type":"text","text":"why is the glm stream cut short","time":{"start":"2026-09-17T09:00:01Z"}}');
insert into part values ('p-assistant', 'm-assistant', '{"type":"text","text":"the proxy closed on an idle timeout","time":{"start":"2026-09-17T09:00:02Z"}}');
`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss := LoadZCode()
	if len(ss) == 0 {
		t.Fatal("the CLI database was not read")
	}
	for _, s := range ss {
		if s.Harness != "zcode" {
			t.Fatalf("harness = %q, want zcode", s.Harness)
		}
	}
	found := false
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "idle timeout") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the reply from the database is missing")
	}
	// And the file list names it, or an incremental ingest would never see it.
	named := false
	for _, p := range ZCodeSessionFiles() {
		if p == db {
			named = true
		}
	}
	if !named {
		t.Errorf("ZCodeSessionFiles does not name the database: %v", ZCodeSessionFiles())
	}
}

// ZCode's CLI database is OpenCode's schema, but its tool parts carry Claude
// Code's names and arguments: Bash {command}, Read {file_path}, Edit
// {file_path, old_string, new_string}, Write {file_path, content}. The reader
// matched only opencode's lowercase read/bash/apply_patch, so a ZCode session
// was text alone. The parts are the ones the ZCode 3.14.4 runtime wrote, the
// first Edit refused because the file had not been read yet (#4428).
func TestZCodeToolPartsAreIndexed(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not installed")
	}
	home := t.TempDir()
	db := filepath.Join(home, "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent"))

	schema := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, time_created integer, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('sess_1', '/private/tmp/proj-zcode', 1790897586000, 1790897657000);
insert into message values ('m1', 'sess_1', 1790897586942, '{"role":"user","time":{"created":1790897586942}}');
insert into message values ('m2', 'sess_1', 1790897587684, '{"role":"assistant","time":{"created":1790897587684}}');
insert into part values ('p1', 'm1', '{"type":"text","text":"Fix the retry loop in retry.cfg.","time":{"start":1790897586942}}');
insert into part values ('p2', 'm2', '{"type":"tool","callID":"call_27e245a42ff9","declarationIndex":0,"tool":"Bash","state":{"status":"completed","input":{"command":"cat retry.cfg && git status --short"},"output":"retries = 3\nmax_backoff_seconds = 30  # cap for the retry loop","title":"Bash","time":{"start":1790897587741,"end":1790897587773}}}');
insert into part values ('p3', 'm2', '{"type":"tool","callID":"call_f2a9b5330b2d","declarationIndex":0,"tool":"Edit","state":{"status":"error","input":{"file_path":"/tmp/proj-zcode/retry.cfg","old_string":"max_backoff_seconds = 30  # refused before the read","new_string":"max_backoff_seconds = 120  # refused before the read"},"error":"File has not been read yet. Read it first before writing to it.","time":{"start":1790897616631,"end":1790897616631}}}');
insert into part values ('p4', 'm2', '{"type":"tool","callID":"call_85d449f3279f","declarationIndex":0,"tool":"Read","state":{"status":"completed","input":{"file_path":"/tmp/proj-zcode/retry.cfg"},"output":"1\tretries = 3","title":"Read","time":{"start":1790897650138,"end":1790897650207}}}');
insert into part values ('p5', 'm2', '{"type":"tool","callID":"call_094947cf9a78","declarationIndex":0,"tool":"Edit","state":{"status":"completed","input":{"file_path":"/tmp/proj-zcode/retry.cfg","old_string":"max_backoff_seconds = 30  # cap for the retry loop","new_string":"max_backoff_seconds = 120  # cap for the retry loop"},"output":"The file has been updated successfully.","title":"Edit","time":{"start":1790897654455,"end":1790897654465}}}');
insert into part values ('p6', 'm2', '{"type":"tool","callID":"call_w","declarationIndex":0,"tool":"Write","state":{"status":"completed","input":{"file_path":"/tmp/proj-zcode/NOTES.md","content":"backoff doubles on every failed attempt\n"},"title":"Write","time":{"start":1790897655000,"end":1790897655010}}}');
`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss, err := ParseZCodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if cmds := rolesOf(s, RoleCommand); len(cmds) != 1 || !strings.Contains(cmds[0], "git status --short") {
		t.Errorf("commands = %q, want the Bash call", cmds)
	}
	if out := strings.Join(rolesOf(s, RoleToolOutput), "\n"); !strings.Contains(out, "max_backoff_seconds = 30") {
		t.Errorf("tool output = %q, want what cat printed", out)
	}
	if files := strings.Join(rolesOf(s, RoleFiles), "\n"); !strings.Contains(files, "/tmp/proj-zcode/retry.cfg") {
		t.Errorf("files = %q, want the Read's file", files)
	}
	edits := strings.Join(rolesOf(s, RoleEdit), "\n")
	if !strings.Contains(edits, "/tmp/proj-zcode/retry.cfg\nmax_backoff_seconds = 30  # cap for the retry loop") {
		t.Errorf("edits = %q, want the replaced line", edits)
	}
	if strings.Contains(edits, "refused") {
		t.Errorf("edits = %q: the refused Edit changed nothing", edits)
	}
	wrote := rolesOf(s, RoleWrote)
	if !wroteHas(t, wrote, "/tmp/proj-zcode/NOTES.md", "backoff doubles on every failed attempt") {
		t.Errorf("wrote = %q, want the Write's content", wrote)
	}
	if !wroteHas(t, wrote, "/tmp/proj-zcode/retry.cfg", "max_backoff_seconds = 120  # cap for the retry loop") {
		t.Errorf("wrote = %q, want the Edit's new text", wrote)
	}
}

// ZCode heads its output with "Exit code N" only for a failed run with a
// non-zero code (isBashProviderErrorStatus); a clean run's output that opens
// "Exit code 0" is the command's own and stays whole.
func TestZCodeExitZeroLineIsOutput(t *testing.T) {
	db := vocabSQL(t, `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created integer, data text);
create table part(id text, message_id text, data text);
insert into part values ('p1','m1','{"type":"tool","tool":"Bash","callID":"p1","state":{"status":"completed","input":{"command":"make check"},"output":"Exit code 0\nall checks passed","time":{"start":1790000002000}}}');
insert into message values ('m1','s1',1790000001000,'{"role":"assistant","time":{"created":1790000001000}}');
insert into session values ('s1','/tmp/proj',1790000000000,1790000100000);
`)
	ss := parseKindForTest(t, "zcode-db", db)
	if len(ss) != 1 {
		t.Fatalf("%d sessions", len(ss))
	}
	got := [2]string{strings.Join(rolesOf(ss[0], RoleCommand), "|"), strings.Join(rolesOf(ss[0], RoleToolOutput), "|")}
	want := [2]string{"$ make check", "Exit code 0\nall checks passed"}
	if got != want {
		t.Errorf("command, output = %q, want %q", got, want)
	}
}

func TestZstdMissingBeforeRead(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	path := filepath.Join(t.TempDir(), "rollout-missing.jsonl.zst")
	_, err := ParseCodexRollout(path)
	if !errors.Is(err, errNoZstd) {
		t.Fatalf("ParseCodexRollout = %v, want errNoZstd before reading the missing file", err)
	}
	if !strings.Contains(err.Error(), "codex") || !strings.Contains(err.Error(), filepath.Base(path)) {
		t.Fatalf("missing-tool error must name the harness and file: %v", err)
	}
}

// A pass asks for zstd once, not once per rollout, and a rebuild nested in a
// pass must not unpin the outer pass's answer when it ends (#5105).
func TestPinZstdHoldsForThePass(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	unpin := PinZstd()
	name := "zstd"
	if runtime.GOOS == "windows" {
		name = "zstd.bat"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ZstdAvailable() {
		t.Fatal("zstd installed mid-pass changed the pinned answer")
	}
	PinZstd()()
	if ZstdAvailable() {
		t.Fatal("a nested pin unpinned the outer pass")
	}
	unpin()
	if !ZstdAvailable() {
		t.Fatal("after the pass, ZstdAvailable did not see zstd")
	}
}

// The index learns which rollouts wait for zstd from this side channel.
func TestZstdMissingIsRecorded(t *testing.T) {
	DiagSnapshot()
	t.Cleanup(func() { DiagSnapshot() })
	t.Setenv("PATH", t.TempDir())
	path := filepath.Join(t.TempDir(), "rollout-missing.jsonl.zst")
	if _, err := ParseCodexRollout(path); err == nil {
		t.Fatal("rollout parsed without zstd")
	}
	if !DiagNoZstdPaths()[path] {
		t.Fatal("rollout left unread for want of zstd was not recorded")
	}
	DiagSnapshot()
	if len(DiagNoZstdPaths()) != 0 {
		t.Fatal("DiagSnapshot did not start the next pass's record over")
	}
}

// noTempDir points every temporary-directory variable at a file, so anything
// that writes a temporary file fails instead of leaving one.
func noTempDir(t *testing.T) {
	t.Helper()
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, blocked)
	}
}

// A compressed transcript was decompressed into $TMPDIR before parsing: the
// unredacted plaintext, left there by a pass killed mid-parse (#5052). Both
// readers now parse it from memory and need no temporary directory at all.
func TestCompressedTranscriptsNeedNoTempFile(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	rollout := writeCodexRollout(t, filepath.Join(t.TempDir(), "sessions"), true)

	sessions := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(sessions, "cccc3333.jsonl.deleted.1788000001")
	if err := os.WriteFile(plain, []byte(openclawArchiveBody("cccc3333", "we settled on one shard")), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "zstd", "-q", "--rm", plain).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v: %s", err, out)
	}

	noTempDir(t)
	ss, err := ParseCodexRolloutFromOffset(rollout, 0)
	if err != nil || len(ss) != 1 || ss[0].ID != "thread-7" || len(ss[0].Messages) != 2 || ss[0].Path != rollout {
		t.Fatalf("codex rollout: %+v, %v", ss, err)
	}
	ss, err = ParseOpenClawFile(plain + ".zst")
	if err != nil || len(ss) != 1 || ss[0].ID != "cccc3333" || ss[0].Path != plain+".zst" {
		t.Fatalf("openclaw archive: %+v, %v", ss, err)
	}
}

// zstdFrame compresses one chunk as a frame of its own, the way dsh appends
// its log.
func zstdFrame(t *testing.T, chunk string) []byte {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-c")
	cmd.Stdin = strings.NewReader(chunk)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("zstd: %v", err)
	}
	return out
}

// tornLog is body one frame per line, then one more frame cut short: what a
// log looks like when its writer died mid-frame, or when it is read mid-append.
func tornLog(t *testing.T, body, tail string) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, line := range strings.SplitAfter(body, "\n") {
		if line != "" {
			b.Write(zstdFrame(t, line))
		}
	}
	last := zstdFrame(t, tail)
	b.Write(last[:len(last)-7])
	return b.Bytes()
}

func hasMessage(s model.Session, role, text string) bool {
	for _, m := range s.Messages {
		if m.Role == role && m.Text == text {
			return true
		}
	}
	return false
}

// One torn frame at the end of a dsh log dropped every complete frame before
// it, and doctor called the whole store unreadable (#4294).
func TestParseDeepSeekFileKeepsTheFramesBeforeATornOne(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	tail := `{"type":"assistant/message","seq":50,"time":1787320264000,"data":{"message":{"role":"assistant","content":[{"type":"text","text":"and 60 on the replica"}]}}}` + "\n"
	dir := filepath.Join(t.TempDir(), "--work-pgbouncer-lab--", "session-torn")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl.zstd")
	if err := os.WriteFile(path, tornLog(t, deepSeekLog, tail), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Errorf("a torn tail failed the whole file: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("the complete frames were not kept: %+v", ss)
	}
	// By role and text, not by position: records other parsers add beside the
	// prose must not move what this checks.
	if !hasMessage(ss[0], "user", "сколько коннектов держим на шард?") ||
		!hasMessage(ss[0], "assistant", "держим 40 на шард") {
		t.Errorf("the turns before the torn frame are missing: %+v", ss[0].Messages)
	}
	if hasMessage(ss[0], "assistant", "and 60 on the replica") {
		t.Errorf("the torn frame's half-written line was read as a turn")
	}
	// The one file is named, not the store.
	if DiagMalformedCounts()[path] == 0 {
		t.Errorf("the torn file is not named in the ingest diagnostics")
	}
}

// The Codex .zst reader goes through the same CLI and had the same rule.
func TestCodexKeepsTheFramesBeforeATornOne(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-thread-7.jsonl.zst")
	tail := `{"timestamp":"2026-07-17T09:00:03.000Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"and the retry"}]}}` + "\n"
	if err := os.WriteFile(path, tornLog(t, codexRolloutFixture, tail), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(path)
	if err != nil {
		t.Errorf("a torn tail failed the whole rollout: %v", err)
	}
	if len(ss) != 1 || !hasMessage(ss[0], "assistant", "the advisory lock was never released") {
		t.Fatalf("the complete frames were not kept: %+v", ss)
	}
	if DiagMalformedCounts()[path] == 0 {
		t.Errorf("the torn file is not named in the ingest diagnostics")
	}
}

// A damaged rollout is scanned as it streams and then again from the salvage,
// and only the second reading counts: one malformed line and the torn frame.
func TestCodexCountsADamagedRolloutOnce(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	path := filepath.Join(t.TempDir(), "rollout-thread-7.jsonl.zst")
	tail := `{"timestamp":"2026-07-17T09:00:03.000Z","type":"response_item","payload":{"role":"user"}}` + "\n"
	if err := os.WriteFile(path, tornLog(t, codexRolloutFixture+"not json\n", tail), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCodexRollout(path); err != nil {
		t.Fatal(err)
	}
	if got := DiagMalformedCounts()[path]; got != 2 {
		t.Errorf("counted %d unusable items, want the malformed line and the torn frame", got)
	}
}

// Abandoning the first pass of a read puts back only what that pass counted, not
// what an earlier read of the same file left there.
func TestCodexKeepsTheCountOfAnEarlierRead(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	path := filepath.Join(t.TempDir(), "rollout-thread-7.jsonl.zst")
	tail := `{"timestamp":"2026-07-17T09:00:03.000Z","type":"response_item","payload":{"role":"user"}}` + "\n"
	if err := os.WriteFile(path, tornLog(t, codexRolloutFixture, tail), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := ParseCodexRollout(path); err != nil {
			t.Fatal(err)
		}
	}
	if got := DiagMalformedCounts()[path]; got != 2 {
		t.Errorf("counted %d torn frames over two reads, want 2", got)
	}
}

// A corrupt frame in the middle is not a torn tail: zstd stops at it, and the
// complete frames after it are as good as the ones before. Each frame is
// decoded on its own then, and only the bad one is lost.
func TestParseDeepSeekFileKeepsTheFramesAfterACorruptOne(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	var b bytes.Buffer
	for _, line := range strings.SplitAfter(deepSeekLog, "\n") {
		if line == "" {
			continue
		}
		frame := zstdFrame(t, line)
		if strings.Contains(line, `"tool/result"`) {
			// A byte flipped inside the block, before the checksum.
			frame[len(frame)-6] ^= 0xff
		}
		b.Write(frame)
	}
	dir := filepath.Join(t.TempDir(), "--work-pgbouncer-lab--", "session-corrupt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl.zstd")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Errorf("one corrupt frame failed the whole file: %v", err)
	}
	if len(ss) != 1 || !hasMessage(ss[0], "assistant", "сейчас посмотрю конфиг") {
		t.Fatalf("the frames before the corrupt one were not kept: %+v", ss)
	}
	if !hasMessage(ss[0], "assistant", "держим 40 на шард") {
		t.Errorf("the frames after the corrupt one were dropped: %+v", ss[0].Messages)
	}
	if hasMessage(ss[0], "tool-output", "pgbouncer pool_size = 40") {
		t.Errorf("the corrupt frame was read")
	}
	if DiagMalformedCounts()[path] != 1 {
		t.Errorf("malformed = %d, want the one corrupt frame", DiagMalformedCounts()[path])
	}
}

// OpenClaw's compressed archives go through the same reader.
func TestOpenClawKeepsTheFramesBeforeATornOne(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	archive := filepath.Join(sessions, "dddd4444.jsonl.deleted.1788000003.zst")
	tail := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"and the replica"}]},"timestamp":"2026-08-02T10:02:00Z"}` + "\n"
	body := tornLog(t, openclawArchiveBody("dddd4444", "we settled on one shard"), tail)
	if err := os.WriteFile(archive, body, 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseOpenClawFile(archive)
	if err != nil {
		t.Errorf("a torn tail failed the whole archive: %v", err)
	}
	if len(ss) != 1 || !hasMessage(ss[0], "user", "we settled on one shard") {
		t.Fatalf("the complete frames were not kept: %+v", ss)
	}
	if DiagMalformedCounts()[archive] == 0 {
		t.Errorf("the torn archive is not named in the ingest diagnostics")
	}
}

// A corrupt frame header, not just a corrupt block, desynced the split: the
// frames after it were lost with it, and a raw block whose size grew past the
// next frame made zstd say "premature end" and spill that frame's bytes into
// the output as if the file were torn.
func TestZstdDecodeFileResyncsAfterACorruptHeader(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	a, b, c := zstdFrame(t, "aaaa line one\n"), zstdFrame(t, "bbbb line two\n"), zstdFrame(t, "cccc line three\n")
	for name, corrupt := range map[string]func([]byte){
		"magic":      func(f []byte) { f[0] ^= 0xff },
		"block size": func(f []byte) { f[7] ^= 0x40 },
	} {
		bad := append([]byte{}, b...)
		corrupt(bad)
		path := filepath.Join(t.TempDir(), "session.jsonl.zstd")
		got, err := zstdDecodeFile(path, "deepseek", bytes.Join([][]byte{a, bad, c}, nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != "aaaa line one\ncccc line three\n" {
			t.Errorf("%s: decoded %q, want the frames on both sides of the bad one", name, got)
		}
		if DiagMalformedCounts()[path] != 1 {
			t.Errorf("%s: malformed = %d, want 1", name, DiagMalformedCounts()[path])
		}
	}
}

// The splitter reads lengths from untrusted headers: none of these may loop,
// panic, or claim more bytes than it was given.
func TestZstdFrameLenOnHostileHeaders(t *testing.T) {
	magic := []byte{0x28, 0xB5, 0x2F, 0xFD}
	frame := func(b ...byte) []byte { return append(append([]byte{}, magic...), b...) }
	cases := map[string][]byte{
		"empty":               nil,
		"magic only":          magic,
		"truncated header":    frame(0xC0, 0x00, 0x01),
		"huge content size":   frame(0xE0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01, 0x00, 0x00),
		"dictionary id":       frame(0x23, 0x01, 0x02, 0x03, 0x04, 0x05, 0x01, 0x00, 0x00),
		"checksum, cut":       frame(0x24, 0x05, 0x01, 0x00, 0x00, 0xAA),
		"zero-length block":   frame(0x20, 0x00, 0x01, 0x00, 0x00),
		"block past the end":  frame(0x20, 0x00, 0xF9, 0xFF, 0x0F, 0x00),
		"reserved block type": frame(0x20, 0x00, 0x07, 0x00, 0x00),
		"never-last blocks":   frame(0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00),
		"skippable, huge":     {0x50, 0x2A, 0x4D, 0x18, 0xFF, 0xFF, 0xFF, 0xFF, 0x00},
		"skippable, empty":    {0x5F, 0x2A, 0x4D, 0x18, 0x00, 0x00, 0x00, 0x00},
	}
	want := map[string]int{"zero-length block": 9, "skippable, empty": 8, "dictionary id": 13, "huge content size": 16}
	for name, raw := range cases {
		if n := zstdFrameLen(raw); n != want[name] {
			t.Errorf("%s: zstdFrameLen = %d, want %d", name, n, want[name])
		}
		frames, rest := zstdSplitFrames(raw)
		n := len(rest)
		for _, f := range frames {
			n += len(f)
		}
		if n != len(raw) {
			t.Errorf("%s: split covers %d of %d bytes", name, n, len(raw))
		}
	}
}

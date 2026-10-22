package sources

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
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

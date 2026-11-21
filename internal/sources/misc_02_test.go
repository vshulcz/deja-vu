package sources

import (
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Codex has no Read or Edit tool: it reads files with shell commands and makes
// every change through apply_patch. So the file records come out of the patch,
// and so does the span `deja restore` hands back.
func TestCodexRolloutWorkRecords(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-abc.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"fix the greeting"}]}}`,
		`{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go test ./...\",\"workdir\":\"/w/app\"}"}}`,
		`{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Chunk ID: 5e9c5d\nWall time: 0.1 seconds\nProcess exited with code 2\nOriginal token count: 25\nOutput:\nFAIL\tgithub.com/x/app\n"}}`,
		`{"timestamp":"2026-07-31T00:00:04Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c2","arguments":"{\"cmd\":\"ls -la\"}"}}`,
		`{"timestamp":"2026-07-31T00:00:05Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c3","input":"*** Begin Patch\n*** Update File: main.go\n@@\n-\tprintln(\"hello\")\n+\tprintln(\"goodbye\")\n*** Add File: notes.txt\n+alpha\n*** End Patch\n"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	// The outcome arrives in a separate record joined by call_id; without the
	// join the command reads as though it succeeded.
	if len(byRole[RoleCommand]) != 1 || byRole[RoleCommand][0] != "$ go test ./...  → exit 2" {
		t.Errorf("commands = %q, want the meaningful one annotated with its exit", byRole[RoleCommand])
	}
	// Codex frames every result with a chunk id, a wall time and a token count.
	if len(byRole[RoleToolOutput]) != 1 || strings.Contains(byRole[RoleToolOutput][0], "Chunk ID") ||
		!strings.Contains(byRole[RoleToolOutput][0], "FAIL\tgithub.com/x/app") {
		t.Errorf("tool output = %q, want the body without the framing", byRole[RoleToolOutput])
	}
	files := strings.Join(byRole[RoleFiles], "\n")
	for _, want := range []string{"/w/app/main.go", "/w/app/notes.txt"} {
		if !strings.Contains(files, want) {
			t.Errorf("files = %q, want %q resolved against the session cwd", files, want)
		}
	}
	if len(byRole[RoleEdit]) != 1 || !strings.Contains(byRole[RoleEdit][0], `println("hello")`) ||
		strings.Contains(byRole[RoleEdit][0], "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", byRole[RoleEdit])
	}
}

func TestCodexWorkRecordsRespectTheirSwitches(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-off.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"off","cwd":"/w/app"}}`,
		`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"keep one message"}]}}`,
		`{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go build ./...\"}"}}`,
		`{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Process exited with code 0\nOutput:\nok\n"}}`,
		`{"timestamp":"2026-07-31T00:00:04Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch\n"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleFiles, RoleEdit, RoleCommand, RoleToolOutput:
			t.Fatalf("a switched-off record was still written: %s %q", m.Role, m.Text)
		}
	}
}

// A rollout record without a call_id is not an error. Asserting the id bare
// turned one into a panic that took the whole parse of the session down, and
// no test caught it because every call in the local corpus had an id.
func TestCodexOutputWithoutCallIDDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-nc.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"nc","cwd":"/w"}}`,
		`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"function_call_output","output":"Process exited with code 1\nOutput:\nboom\n"}}`,
		`{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go test ./...\"}"}}`,
		`{"timestamp":"2026-07-31T00:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":123,"output":"Exit code: 2\nOutput:\nno\n"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	// The output still lands; only the exit annotation is lost, which is the
	// right degradation for a record that names no call.
	var out int
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			out++
		}
	}
	if out != 2 {
		t.Fatalf("tool output records = %d, want 2", out)
	}
}

// One rollout file is one thread; a session groups every thread that branched
// from it. Keying on the SessionId merges the whole fork tree into a single
// deja session — measured by a contributor at 811 rollouts collapsing into 74
// (#635).
func TestCodexRolloutKeysOnThreadNotSession(t *testing.T) {
	dir := t.TempDir()
	ids := make([]string, 0, 3)
	for _, tid := range []string{"thread-a", "thread-b", "thread-c"} {
		p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-"+tid+".jsonl")
		lines := []string{
			`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"SHARED","id":"` + tid +
				`","forked_from_id":"thread-a","cwd":"/w"}}`,
			`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"work in ` + tid + `"}]}}`,
		}
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseCodexRollout(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 {
			t.Fatalf("sessions = %d", len(ss))
		}
		ids = append(ids, ss[0].ID)
	}
	seen := map[string]bool{}
	for i, id := range ids {
		if id == "SHARED" {
			t.Fatalf("thread %d took the shared SessionId as its identity", i)
		}
		if seen[id] {
			t.Fatalf("two threads share the id %q", id)
		}
		seen[id] = true
	}
	// The filename carries the ThreadId, so the two agree — but on purpose,
	// not by accident.
	if ids[1] != "thread-b" {
		t.Fatalf("id = %q, want the ThreadId", ids[1])
	}
}

// Codex writes its preamble under its own role and again as a user turn.
func TestCodexDropsHarnessAuthoredRoles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-dev.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"s","id":"dev","cwd":"/w"}}`,
		`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"text","text":"You are the Codex CLI, a terminal assistant"}]}}`,
		`{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"message","role":"system","content":[{"type":"text","text":"system framing"}]}}`,
		`{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"the actual question"}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("messages = %#v", ss)
	}
	if ss[0].Messages[0].Text != "the actual question" {
		t.Fatalf("kept %q", ss[0].Messages[0].Text)
	}
}

func TestHarnessAuthored(t *testing.T) {
	for _, r := range []string{"developer", "system"} {
		if !HarnessAuthored(r) {
			t.Errorf("%q is the harness talking to the model", r)
		}
	}
	for _, r := range []string{"user", "assistant", "tool", ""} {
		if HarnessAuthored(r) {
			t.Errorf("%q is not harness plumbing", r)
		}
	}
}

// An appended rollout is parsed from an offset, so the session_meta line is
// never read again. Without re-reading the head the id falls back to the
// filename — which matches the real ThreadId in 0 of 28 rollouts here — and
// one session becomes two, undoing #635 for the sessions still in use.
func TestCodexIncrementalParseKeepsTheThreadID(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-thread-x.jsonl")
	head := `{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"SESS","id":"thread-x","cwd":"/w/app"}}` + "\n" +
		`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	full, err := ParseCodexRollout(p)
	if err != nil || len(full) != 1 {
		t.Fatalf("full parse: %v %#v", err, full)
	}
	extra := `{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"second"}]}}` + "\n"
	if err = os.WriteFile(p, []byte(head+extra), 0o644); err != nil {
		t.Fatal(err)
	}
	inc, err := ParseCodexRolloutFromOffset(p, int64(len(head)))
	if err != nil || len(inc) != 1 {
		t.Fatalf("offset parse: %v %#v", err, inc)
	}
	if inc[0].ID != full[0].ID {
		t.Fatalf("append split the session: %q then %q", full[0].ID, inc[0].ID)
	}
	if inc[0].ID != "thread-x" {
		t.Fatalf("id = %q, want the ThreadId", inc[0].ID)
	}
	// The project comes from the same record and must survive the same way.
	if inc[0].Project != full[0].Project {
		t.Fatalf("project changed on append: %q then %q", full[0].Project, inc[0].Project)
	}
}

// A present-but-malformed id is a shape deja does not understand. Falling back
// to the SessionId there would silently reintroduce the collapse #635 fixes,
// so the filename-derived id stands instead.
func TestCodexMalformedThreadIDDoesNotCollapse(t *testing.T) {
	dir := t.TempDir()
	for _, shape := range []string{`123`, `null`, `{"x":1}`, `""`, `[]`, `true`} {
		p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-shape.jsonl")
		body := `{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"SHARED","id":` + shape + `,"cwd":"/w"}}` + "\n" +
			`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"x"}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseCodexRollout(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("id %s: %v %#v", shape, err, ss)
		}
		if ss[0].ID == "SHARED" {
			t.Errorf("id %s collapsed onto the SessionId", shape)
		}
	}
	// Absent is different from malformed: an old rollout with only a SessionId
	// keeps the identity it already has in existing indexes.
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-old.jsonl")
	body := `{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"LEGACY","cwd":"/w"}}` + "\n" +
		`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"x"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatal(err)
	}
	if ss[0].ID != "LEGACY" {
		t.Fatalf("id = %q, want the SessionId when no ThreadId exists", ss[0].ID)
	}
}

// Codex writes every turn twice: once as a response_item carrying a role, and
// once as an event_msg with none. Reading the second and defaulting it to
// "user" stored each assistant answer a second time as something the person
// said — 40 mis-roled and 28 duplicated across 25 of 28 rollouts on a real
// store, 35% of indexed codex messages, and `--role user` returned the
// agent's own words.
func TestCodexIgnoresTheDuplicateEventStream(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-27T10-38-00-dup.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-27T10:38:00Z","type":"session_meta","payload":{"session_id":"s","id":"dup","cwd":"/w"}}`,
		`{"timestamp":"2026-07-27T10:38:07Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}`,
		`{"timestamp":"2026-07-27T10:38:07Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"timestamp":"2026-07-27T10:38:09Z","type":"event_msg","payload":{"type":"agent_message","message":"What would you like to work on?"}}`,
		`{"timestamp":"2026-07-27T10:38:09Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"text","text":"What would you like to work on?"}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %d, want the one turn and the one reply: %#v", len(ss[0].Messages), ss[0].Messages)
	}
	for _, m := range ss[0].Messages {
		if m.Role == "user" && strings.Contains(m.Text, "would you like") {
			t.Fatal("the agent's answer is stored as something the person said")
		}
	}
	if ss[0].Messages[0].Role != "user" || ss[0].Messages[1].Role != "assistant" {
		t.Fatalf("roles = %q, %q", ss[0].Messages[0].Role, ss[0].Messages[1].Role)
	}
}

// history.jsonl repeats the prompts of sessions whose rollout deja already
// read, with a coarser timestamp, so the ingest de-duplicator never collapsed
// them and the same question appeared twice in one session.
func TestCodexHistorySkipsSessionsThatHaveARollout(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions", "2026", "07", "27")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := []string{
		`{"timestamp":"2026-07-27T10:38:00Z","type":"session_meta","payload":{"session_id":"s1","id":"s1","cwd":"/w"}}`,
		`{"timestamp":"2026-07-27T10:38:07Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"the question"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(sessions, "rollout-2026-07-27T10-38-00-s1.jsonl"), []byte(strings.Join(rollout, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hist := `{"session_id":"s1","ts":1785500000,"text":"the question"}` + "\n" +
		`{"session_id":"orphan","ts":1785500001,"text":"a session with no rollout"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(hist), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CODEX_ROOT", dir)
	ss := LoadCodex()
	byID := map[string]int{}
	for _, s := range ss {
		byID[s.ID] += len(s.Messages)
	}
	if byID["s1"] != 1 {
		t.Fatalf("session s1 has %d messages, want 1 — history repeated the rollout", byID["s1"])
	}
	// A session with no rollout is the only reason to read history at all.
	if byID["orphan"] != 1 {
		t.Fatalf("orphan history entry lost: %v", byID)
	}
}

func TestCodexReadsCLIAndXcodeRootsAsOneHarness(t *testing.T) {
	home := t.TempDir()
	cli := filepath.Join(home, "cli-codex")
	xcode := filepath.Join(home, "xcode-codex")
	writeRollout := func(root, id, cwd string) string {
		dir := filepath.Join(root, "sessions", "2026", "09", "10")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "rollout-"+id+".jsonl")
		body := `{"timestamp":"2026-09-10T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `"}}
{"timestamp":"2026-09-10T10:01:00Z","payload":{"role":"user","content":"` + id + `"}}
`
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cliPath := writeRollout(cli, "cli-id", "/projects/cli")
	xcodePath := writeRollout(xcode, "xcode-id", "/projects/xcode")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("DEJA_CODEX_ROOT", cli)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", xcode)

	got := LoadCodex()
	if len(got) != 2 || got[0].Harness != "codex" || got[1].Harness != "codex" {
		t.Fatalf("LoadCodex=%#v, want both roots as codex sessions", got)
	}
	byPath := map[string]model.Session{}
	for _, session := range got {
		byPath[session.Path] = session
	}
	if byPath[cliPath].ID != "cli-id" || byPath[xcodePath].ID != "xcode-id" || byPath[xcodePath].Project != "projects/xcode" {
		t.Fatalf("sessions by path=%#v", byPath)
	}
	if len(CodexFiles()) != 2 {
		t.Fatalf("CodexFiles=%v, want both rollout files", CodexFiles())
	}
	if k := KindForPath(xcodePath); k != "codex" {
		t.Fatalf("KindForPath(Xcode)=%q, want codex", k)
	}
	if k, ok := KindForPathKind(xcodePath); !ok || k.ParseFrom == nil {
		t.Fatalf("KindForPathKind(Xcode)=%#v, want incremental parser", k)
	}
}

func TestCodexEqualRootsDoNotDuplicateAndAbsentXcodeIsEmpty(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", root+string(filepath.Separator)+".")
	if got := CodexRoots(); len(got) != 1 || got[0] != filepath.Clean(root) {
		t.Fatalf("CodexRoots=%v, want one cleaned root", got)
	}
	if got := CodexFiles(); len(got) != 0 {
		t.Fatalf("CodexFiles absent=%v, want empty", got)
	}
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(t.TempDir(), "missing-xcode"))
	if got := CodexFiles(); len(got) != 0 {
		t.Fatalf("CodexFiles with absent Xcode root=%v, want empty", got)
	}
}

func TestCodexRegistryMatchingIsPathBoundarySafe(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(t.TempDir(), "xcode"))
	valid := filepath.Join(root, "sessions", "2026", "rollout-valid.jsonl")
	for _, path := range []string{valid, filepath.Join(root+"-sibling", "sessions", "2026", "rollout-wrong.jsonl")} {
		want := path == valid
		if got := KindForPath(path); (got == "codex") != want {
			t.Fatalf("KindForPath(%q)=%q, want codex=%v", path, got, want)
		}
	}
	historySibling := root + "-sibling/history.jsonl"
	if got := KindForPath(historySibling); got == "codex-history" {
		t.Fatalf("KindForPath(%q) matched history outside root", historySibling)
	}
}

func TestCodexSidecarsAreRootAwareAndLeaveUnknownRolloutsVisible(t *testing.T) {
	home := t.TempDir()
	cli := filepath.Join(home, "cli")
	xcode := filepath.Join(home, "xcode")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CODEX_ROOT", cli)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", xcode)
	paths := []string{
		filepath.Join(cli, "version.json"),
		filepath.Join(xcode, "version.json"),
		filepath.Join(xcode, "models_cache.json"),
		filepath.Join(xcode, "plugins", "state.json"),
		filepath.Join(xcode, "cache", "assistant.json"),
		filepath.Join(xcode, "restored", "rollout-unknown.jsonl"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := CodexSidecarFiles()
	seen := make(map[string]bool, len(got))
	for _, path := range got {
		seen[path] = true
	}
	for _, path := range paths[:5] {
		if !seen[path] {
			t.Errorf("known sidecar %q was not classified", path)
		}
	}
	if seen[paths[5]] {
		t.Errorf("unknown rollout %q was classified as a sidecar", paths[5])
	}
}

// An older rollout carries its turns only as events, and there the payload
// type is the only thing naming the speaker. Reading it as "user" regardless
// is what stored the agent's answers as the person's.
func TestCodexEventStreamNamesTheSpeaker(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-27T10-38-00-old.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-27T10:38:00Z","type":"session_meta","payload":{"session_id":"old","id":"old","cwd":"/w"}}`,
		`{"timestamp":"2026-07-27T10:38:07Z","type":"event_msg","payload":{"type":"user_message","message":"the question"}}`,
		`{"timestamp":"2026-07-27T10:38:09Z","type":"event_msg","payload":{"type":"agent_message","message":"the answer"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("a rollout with only events must still be readable, got %d messages", len(ss[0].Messages))
	}
	byText := map[string]string{}
	for _, m := range ss[0].Messages {
		byText[m.Text] = m.Role
	}
	if byText["the question"] != "user" {
		t.Errorf("question role = %q", byText["the question"])
	}
	if byText["the answer"] != "assistant" {
		t.Errorf("answer role = %q — the agent's words stored as the person's", byText["the answer"])
	}
}

// A forked rollout opens with its own session_meta and then carries the
// parent's, inherited with the history it was branched from. Letting the later
// record win filed the child's turns under the parent: `deja show <child>`
// answered "no session matches", and the parent came back holding both halves
// (#3933).
func TestCodexForkKeepsItsOwnThreadID(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-06-01T02-00-00-child.jsonl")
	body := `{"timestamp":"2026-06-01T02:00:00Z","type":"session_meta","payload":{"id":"child","forked_from_id":"parent","cwd":"/w/child"}}` + "\n" +
		`{"timestamp":"2026-06-01T01:00:00Z","type":"session_meta","payload":{"id":"parent","cwd":"/w/parent"}}` + "\n" +
		`{"timestamp":"2026-06-01T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"child question"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %#v", err, ss)
	}
	if ss[0].ID != "child" {
		t.Errorf("id = %q, want the rollout's own thread", ss[0].ID)
	}
	if ss[0].Project != "w/child" {
		t.Errorf("project = %q, want the child's own cwd", ss[0].Project)
	}
}

// The same rollout on append: the inherited parent record sits in the new bytes
// this time, and the head has already settled the identity. A session that
// changes id between the first read and the second splits in two (#3933).
func TestCodexForkKeepsItsIdentityOnAppend(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-06-01T02-00-00-child.jsonl")
	head := `{"timestamp":"2026-06-01T02:00:00Z","type":"session_meta","payload":{"id":"child","forked_from_id":"parent","cwd":"/w/child"}}` + "\n"
	if err := os.WriteFile(p, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	extra := `{"timestamp":"2026-06-01T01:00:00Z","type":"session_meta","payload":{"id":"parent","cwd":"/w/parent"}}` + "\n" +
		`{"timestamp":"2026-06-01T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"child question"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(head+extra), 0o644); err != nil {
		t.Fatal(err)
	}
	inc, err := ParseCodexRolloutFromOffset(p, int64(len(head)))
	if err != nil || len(inc) != 1 {
		t.Fatalf("offset parse: %v %#v", err, inc)
	}
	if inc[0].ID != "child" {
		t.Errorf("append moved the session to %q", inc[0].ID)
	}
}

// A patch names its files relative to the session's directory, and on an append
// the record that says which directory that is sits before the offset. Without
// carrying it in, the appended patch resolved to a bare relative path and blame
// had nothing to match against.
func TestCodexAppendResolvesPatchPathsAgainstTheHeadCWD(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-06-01T02-00-00-child.jsonl")
	head := `{"timestamp":"2026-06-01T02:00:00Z","type":"session_meta","payload":{"id":"child","cwd":"/w/child"}}` + "\n"
	if err := os.WriteFile(p, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: app/main.go\n-old\n+new\n*** End Patch"
	line := `{"timestamp":"2026-06-01T02:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":` +
		strconv.Quote(patch) + "}}\n"
	if err := os.WriteFile(p, []byte(head+line), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRolloutFromOffset(p, int64(len(head)))
	if err != nil || len(ss) != 1 {
		t.Fatalf("offset parse: %v %#v", err, ss)
	}
	want := "/w/child/app/main.go"
	found := false
	for _, msg := range ss[0].Messages {
		if msg.Role == RoleFiles && strings.Contains(msg.Text, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("appended patch recorded %#v, want a file resolved to %q", ss[0].Messages, want)
	}
}

// A sub-agent's rollout names the thread that spawned it, and recall reads the
// edge to keep a live session's sub-agents off its own page (#4547). The
// append path re-reads the head for it, as it does for the id.
func TestCodexSubAgentNamesItsParent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-10-02T07-01-00-child.jsonl")
	head := `{"timestamp":"2026-10-02T07:01:00Z","type":"session_meta","payload":{"id":"child","cwd":"/w","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent","depth":1}}}}}` + "\n"
	turn := `{"timestamp":"2026-10-02T07:01:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"find the retry fix"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(head+turn), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %#v", err, ss)
	}
	if ss[0].Kind != "subagent" || ss[0].Parent != "parent" {
		t.Errorf("kind %q parent %q, want subagent of parent", ss[0].Kind, ss[0].Parent)
	}
	inc, err := ParseCodexRolloutFromOffset(p, int64(len(head)))
	if err != nil || len(inc) != 1 {
		t.Fatalf("offset parse: %v %#v", err, inc)
	}
	if inc[0].Kind != "subagent" || inc[0].Parent != "parent" {
		t.Errorf("append: kind %q parent %q, want subagent of parent", inc[0].Kind, inc[0].Parent)
	}

	// A thread a person started says "cli" there and has no parent.
	plain := filepath.Join(dir, "rollout-2026-10-02T07-00-00-top.jsonl")
	if err := os.WriteFile(plain, []byte(`{"timestamp":"2026-10-02T07:00:00Z","type":"session_meta","payload":{"id":"top","cwd":"/w","source":"cli"}}`+"\n"+turn), 0o644); err != nil {
		t.Fatal(err)
	}
	if ss, err := ParseCodexRollout(plain); err != nil || len(ss) != 1 || ss[0].Kind != "" || ss[0].Parent != "" {
		t.Errorf("a top-level thread read as spawned: %v %#v", err, ss)
	}
}

// A fork names the thread it was forked from in its own session_meta, and the
// fork is not told about its source as if it were earlier work (#4549).
func TestCodexForkNamesItsSource(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-06-01T02-00-00-child.jsonl")
	body := `{"timestamp":"2026-06-01T02:00:00Z","type":"session_meta","payload":{"id":"child","forked_from_id":"parent","cwd":"/w/child"}}` + "\n" +
		`{"timestamp":"2026-06-01T01:00:00Z","type":"session_meta","payload":{"id":"parent","cwd":"/w/parent"}}` + "\n" +
		`{"timestamp":"2026-06-01T02:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"child question"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %#v", err, ss)
	}
	if ss[0].Kind != "fork" || ss[0].Parent != "parent" {
		t.Errorf("kind %q parent %q, want a fork of parent", ss[0].Kind, ss[0].Parent)
	}
	head, err := TranscriptHead(p, "child")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head.Kind != "fork" || head.Parent != "parent" {
		t.Errorf("head: kind %q parent %q, want a fork of parent", head.Kind, head.Parent)
	}
	if _, err := TranscriptHead(p, "someone-else"); err == nil {
		t.Error("a transcript was read as another session's")
	}
}

// A rollout read first with nothing but its session_meta held no session, so
// the next read starts past the meta line. It still starts the session at the
// meta's time, as a read from the first byte does.
func TestCodexTailAfterAMetaOnlyReadStartsAtTheMeta(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-2026-06-01T10-00-00-019a0000-0000-7000-8000-000000000001.jsonl")
	meta := `{"timestamp":"2026-06-01T10:00:00.000Z","type":"session_meta","payload":{"id":"019a0000-0000-7000-8000-000000000001","cwd":"/w/app"}}` + "\n"
	turn := `{"timestamp":"2026-06-01T10:00:07.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the pool deadlock"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(meta+turn), 0o644); err != nil {
		t.Fatal(err)
	}
	whole, err := ParseCodexRollout(p)
	if err != nil || len(whole) != 1 {
		t.Fatalf("whole read: %v %d sessions", err, len(whole))
	}
	tail, err := ParseCodexRolloutFromOffset(p, int64(len(meta)))
	if err != nil || len(tail) != 1 {
		t.Fatalf("tail read: %v %d sessions", err, len(tail))
	}
	if !tail[0].Started.Equal(whole[0].Started) {
		t.Errorf("tail starts %v, the whole read %v", tail[0].Started, whole[0].Started)
	}
}

func TestCodexSubAgentCodeModeAndSendMessage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-2026-10-30T10-00-00-019a0000-0000-7000-8000-000000000002.jsonl")
	lines := []string{
		`{"timestamp":"2026-10-30T10:00:00Z","type":"session_meta","payload":{"id":"019a0000-0000-7000-8000-000000000002","cwd":"/tmp/demo","source":{"subagent":{"thread_spawn":{"parent_thread_id":"019a0000-0000-7000-8000-000000000001","depth":1,"agent_path":"/root/worker_a","agent_nickname":"WorkerA","agent_role":"explorer"}}}}}`,
		`{"timestamp":"2026-10-30T10:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_1","name":"exec","input":"text(await tools.exec_command({cmd: \"grep -rn TODO src | wc -l\"}));"}}`,
		`{"timestamp":"2026-10-30T10:00:02Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_1","output":[{"type":"input_text","text":"42"}]}}`,
		`{"timestamp":"2026-10-30T10:00:02Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_test","name":"exec","input":"text(await tools.exec_command({cmd: \"go test ./...\"}));"}}`,
		`{"timestamp":"2026-10-30T10:00:03Z","type":"response_item","payload":{"type":"function_call","name":"send_message","arguments":"{\"target\":\"/root\",\"message\":\"Found 42 TODOs across src/.\"}","call_id":"call_2"}}`,
		`{"timestamp":"2026-10-30T10:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_2","output":"{\"status\":\"sent\"}"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseCodexRollout: err=%v len=%d", err, len(ss))
	}
	s := ss[0]
	if s.Kind != "subagent" || s.Parent != "019a0000-0000-7000-8000-000000000001" {
		t.Fatalf("kind=%q parent=%q", s.Kind, s.Parent)
	}
	var gotCmd, gotOut, gotMsg bool
	for _, m := range s.Messages {
		switch {
		case m.Role == RoleCommand && m.Text == "$ go test ./...":
			gotCmd = true
		case m.Role == RoleToolOutput && m.Text == "42":
			gotOut = true
		case m.Role == "assistant" && m.Text == "Found 42 TODOs across src/.":
			gotMsg = true
		}
	}
	if !gotCmd || !gotOut || !gotMsg {
		t.Fatalf("gotCmd=%v gotOut=%v gotMsg=%v messages=%#v", gotCmd, gotOut, gotMsg, s.Messages)
	}
}

// The allowlist enumerated `go test|build|vet|run` and stopped, so every `go`
// subcommand that changes the build's inputs was dropped — and those are the
// remedies. `deja fix` could see the failure and never the thing that repaired
// it (#1635).
func TestWorthIndexingKeepsTheCommandsThatFixABuild(t *testing.T) {
	for _, c := range []string{
		"go mod tidy",
		"go mod vendor",
		"go mod download",
		"go get ./...",
		"go get github.com/x/y@latest",
		"go install ./cmd/deja",
		"go generate ./...",
		"go work sync",
		"go clean -modcache",
		// brew belongs here for the same reason, and by the same measurement:
		// `brew install jq` was in the eleven-command fixture and was dropped
		// with the rest. "the tool is missing" is an error people paste, and
		// the remedy is an install.
		"brew install jq",
		"brew upgrade",
		"cd repo && go mod tidy",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped the remedy: %q", c)
		}
	}
	// The controls: navigation stays dropped, and a meaningful name inside a
	// navigation command's argument is still not a command.
	for _, c := range []string{
		"cat go.mod | grep module",
		"ls go.sum",
		"echo 'go mod tidy'",
		"grep 'brew install' notes.md",
	} {
		if worthIndexing(c) {
			t.Errorf("kept navigation: %q", c)
		}
	}
}

// A repository's own scripts are its checks and its fixes, and none of them
// were indexed, so the failure hint never had a pair to show for them.
func TestWorthIndexingKeepsProjectScripts(t *testing.T) {
	for _, c := range []string{
		"./scripts/check.sh",
		"./scripts/render.sh --theme dark",
		"bash scripts/check.sh",
		"sh ./hack/verify.sh",
		"python3 tools/gen_feed.py",
		"cd repo && ./bin/test",
		"./configure",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped the project script: %q", c)
		}
	}
	// And through the reader: a Claude transcript that ran the script keeps it.
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s1.jsonl")
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-09-02T09:59:00Z","cwd":"/work/app","message":{"role":"user","content":"run the checks"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-09-02T10:00:00Z","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"./scripts/check.sh"}}]}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err = %v", len(ss), err)
	}
	kept := false
	for _, m := range ss[0].Messages {
		kept = kept || (m.Role == RoleCommand && strings.Contains(m.Text, "./scripts/check.sh"))
	}
	if !kept {
		t.Error("the transcript's ./scripts/check.sh run was not kept as a command")
	}
	for _, c := range []string{
		"bash /tmp/probe.sh",
		"cat ./scripts/check.sh",
		"grep -n foo ./scripts/check.sh",
		"echo ./scripts/check.sh",
		"ls scripts/",
		"./scripts/check.sh <<EOF\nx\nEOF",
	} {
		if worthIndexing(c) {
			t.Errorf("kept: %q", c)
		}
	}
}

// An install pointed at a mirror is the remedy to an index that refused the
// package, and `pip3`, `pip config`, conda and apt were dropped (#5599).
func TestWorthIndexingKeepsTheMirrorInstalls(t *testing.T) {
	for _, c := range []string{
		"pip3 install --index-url https://mirror.example.internal/simple vllm",
		"pip config set global.index-url https://mirror.example.internal/simple",
		"pip download torch -d wheels",
		"conda install -c https://mirror.example.internal/conda pytorch",
		"conda config --add channels https://mirror.example.internal/conda",
		"mamba install numpy",
		"sudo apt-get install -y libssl-dev",
		"apt install build-essential",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped the remedy: %q", c)
		}
	}
	for _, c := range []string{
		"grep 'pip3 install' README.md",
		"echo 'apt install foo'",
	} {
		if worthIndexing(c) {
			t.Errorf("kept navigation: %q", c)
		}
	}
}

// Three quarters of command text on a real corpus is navigation that answers
// nothing and matches by accident: `cat internal/index/index.go` looks relevant
// to a question about the index. The selection is the feature.
func TestWorthIndexingKeepsWhatHappened(t *testing.T) {
	for _, c := range []string{
		"go test ./internal/index/ -count=1",
		"golangci-lint run",
		"gh pr checks 558",
		"git commit -m 'fix: x'",
		"make build && deja index --rebuild",
		"docker compose up -d",
		// Build/test toolchains beyond the Go/JS/Python core (#1098 sweep):
		// dropping these lost the command history of whole ecosystems.
		"bazel test //services:api_test",
		"deno test spec.ts",
		"jest --watch",
		"vitest run",
		"tox -e py311",
		"sbt compile",
		"ninja -C build",
		"meson compile",
		"cabal test",
		"stack build",
		"zig build test",
		"nix build .#pkg",
		"ctest --output-on-failure",
		"tsc --noEmit",
		"rake spec",
		"phpunit tests/",
		"composer install",
		"clang -O2 main.c",
		"gcc -o app app.c",
		"rustc main.rs",
		// Compound commands: a leading cd/export/source must not sink the real
		// work chained after it — the `cd repo && build` shape is everywhere.
		"cd services/api && go test ./...",
		"cd frontend && npm run build",
		"export CGO_ENABLED=0 && go build ./...",
		"source .venv/bin/activate && pytest -q",
		"cd a && cd b && bazel test //x:y",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped a command worth keeping: %q", c)
		}
	}
	for _, c := range []string{
		"cat internal/index/index.go",
		"ls -la",
		"grep -rn foo internal/",
		"cd /repo && pwd",
		"sed -n 1,40p internal/index/index.go",
		"echo hi",
		// A meaningful tool name inside a navigation command stays dropped —
		// the trivial prefix wins.
		"cat stack.yaml",
		"which gcc",
		"cd /nix/store",
		// A meaningful command name inside a nav command's ARGUMENT is not a
		// chained segment, so it stays dropped.
		"grep 'go test' logs.txt",
		"cat go.mod | grep module",
		"echo 'run npm build'",
		"ls && cd /tmp",
	} {
		if worthIndexing(c) {
			t.Errorf("kept navigation: %q", c)
		}
	}
}

func TestClaudeCommandsExtractsAndFilters(t *testing.T) {
	raw := json.RawMessage(`[
	  {"type":"text","text":"running it"},
	  {"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/index/"}},
	  {"type":"tool_use","name":"Bash","input":{"command":"ls -la"}},
	  {"type":"tool_use","name":"Read","input":{"file_path":"/x.go"}},
	  {"type":"tool_result","content":"ok"}
	]`)
	got := claudeCommands(raw)
	if len(got) != 1 || got[0].Text != "$ go test ./internal/index/" {
		t.Fatalf("commands = %#v", got)
	}
}

// The reference parser must agree, or the differential test in #502 is empty.
func TestBothParsersAgreeOnCommands(t *testing.T) {
	body := `[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}}]`
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	a, b := commandsFromContent(v), claudeCommands(json.RawMessage(body))
	if len(a) != len(b) || len(a) != 1 || a[0] != b[0].Text {
		t.Fatalf("generic=%#v typed=%#v", a, b)
	}
}

// deja's own files follow an absolute XDG_CONFIG_HOME and ignore a relative
// one, as the XDG spec says; the exclude list and aider's project list read
// a relative one from the working directory (#5052).
func TestConfigDirIgnoresARelativeXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "relcfg")
	want := filepath.Join(home, ".config", "deja")
	for name, got := range map[string]string{
		"ConfigDir":         ConfigDir(),
		"ExcludePath":       filepath.Dir(ExcludePath()),
		"AiderProjectsPath": filepath.Dir(AiderProjectsPath()),
	} {
		if got != want {
			t.Errorf("%s under %q, want %q", name, got, want)
		}
	}
	abs := filepath.Join(home, "cfg")
	t.Setenv("XDG_CONFIG_HOME", abs)
	if got := ConfigDir(); got != filepath.Join(abs, "deja") {
		t.Errorf("an absolute XDG_CONFIG_HOME was not followed: %q", got)
	}
}

func writeContinueStore(t *testing.T, root string, session any, list any) string {
	t.Helper()
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "8f1c2a3e-0000-4000-8000-000000000000.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if list != nil {
		lb, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sessions.json"), lb, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// Continue runs in VS Code and JetBrains and writes one document per session,
// with the date and the workspace in a list beside it — the document itself
// carries neither (#3062).
func TestContinueReadsASessionAndItsListEntry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	sid := "8f1c2a3e-0000-4000-8000-000000000000"
	path := writeContinueStore(t, root,
		map[string]any{
			"sessionId": sid,
			"history": []any{
				map[string]any{"message": map[string]any{"role": "user", "content": "the retry loop drops the last attempt"}},
				map[string]any{"message": map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "text", "text": "the bound is < where it needs <="},
					map[string]any{"type": "text", "text": "changed it and added a test"},
				}}},
				// System prompts are configuration, and a tool result arrives
				// under the assistant item that asked for it.
				map[string]any{"message": map[string]any{"role": "system", "content": "you are Continue"}},
			},
		},
		[]any{map[string]any{
			"sessionId": sid, "title": "retry loop drops the last attempt",
			"dateCreated": "2026-08-02T10:00:00.000Z", "workspaceDirectory": "/w/api",
		}})

	if files := ContinueSessionFiles(); len(files) != 1 || files[0] != path {
		t.Fatalf("session files = %#v, want just the session document", files)
	}
	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	s := ss[0]
	if s.Harness != "continue" || s.ID != sid {
		t.Fatalf("session = %s/%s, want continue/%s", s.Harness, s.ID, sid)
	}
	// The same project name every path-encoded harness gets: the last two
	// segments, which is what tells /w/api from /other/api.
	if s.Project != "w/api" {
		t.Fatalf("project = %q, want the workspace's own name", s.Project)
	}
	if s.Title != "retry loop drops the last attempt" {
		t.Fatalf("title = %q, want the list entry's", s.Title)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the user turn and the assistant turn: %#v", len(s.Messages), s.Messages)
	}
	if !strings.Contains(s.Messages[1].Text, "needs <=") || !strings.Contains(s.Messages[1].Text, "added a test") {
		t.Fatalf("the array-of-parts content did not come through: %q", s.Messages[1].Text)
	}
	// The list is the only date there is.
	want := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	if !s.Messages[0].Time.Equal(want) {
		t.Fatalf("first turn at %s, want the session's dateCreated %s", s.Messages[0].Time, want)
	}
	if !s.Messages[1].Time.After(s.Messages[0].Time) {
		t.Fatalf("the turns are not in order: %s then %s", s.Messages[0].Time, s.Messages[1].Time)
	}
}

// sessions.json sits in the same directory and is the index of the others, not
// a conversation; reading it as one would file every session's title as a
// session of its own.
func TestContinueSkipsTheListFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	writeContinueStore(t, root,
		map[string]any{"sessionId": "s1", "history": []any{
			map[string]any{"message": map[string]any{"role": "user", "content": "hello"}},
		}},
		[]any{map[string]any{"sessionId": "s1", "dateCreated": "2026-08-02T10:00:00.000Z"}})
	for _, f := range ContinueSessionFiles() {
		if filepath.Base(f) == "sessions.json" {
			t.Fatalf("the list file was offered as a session: %s", f)
		}
	}
}

// Without a list entry there is still a session: the file's mtime is its date,
// and the project falls back to the harness name rather than to the empty
// string, which reads as "no project" everywhere downstream.
func TestContinueWithoutAListEntry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	path := writeContinueStore(t, root,
		map[string]any{"sessionId": "8f1c2a3e-0000-4000-8000-000000000000", "history": []any{
			map[string]any{"message": map[string]any{"role": "user", "content": "no list beside me"}},
		}}, nil)
	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	if ss[0].Project != "continue" {
		t.Fatalf("project = %q, want the fallback", ss[0].Project)
	}
	if ss[0].Messages[0].Time.IsZero() {
		t.Fatal("the turn has no time, so it sorts as the beginning of the epoch")
	}
}

// CONTINUE_GLOBAL_DIR is Continue's own override, and a machine that sets it
// keeps its sessions nowhere else.
func TestContinueHonoursItsOwnGlobalDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CONTINUE_ROOT", "")
	elsewhere := filepath.Join(home, "elsewhere")
	t.Setenv("CONTINUE_GLOBAL_DIR", elsewhere)
	if got := ContinueRoot(); got != elsewhere {
		t.Fatalf("ContinueRoot = %q, want %q", got, elsewhere)
	}
}

// Continue stores no time per turn, and a fork copies the whole history under a
// fresh dateCreated: 17 items one second apart put the last turn 16 s past the
// moment the file was written. No turn may be dated after the file's mtime,
// and the order stays (#4376).
func TestContinueTurnsNeverPassTheFileMtime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	sid := "8f1c2a3e-0000-4000-8000-000000000000"
	created := time.Date(2026, 10, 1, 20, 33, 0, 0, time.UTC)
	history := make([]any, 0, 17)
	for i := range 17 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		history = append(history, map[string]any{"message": map[string]any{"role": role, "content": fmt.Sprintf("turn %d", i)}})
	}
	path := writeContinueStore(t, root,
		map[string]any{"sessionId": sid, "history": history},
		[]any{map[string]any{"sessionId": sid, "dateCreated": created.Format(time.RFC3339Nano), "workspaceDirectory": "/w/api"}})
	written := created.Add(2 * time.Second)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	s := ss[0]
	if !s.Started.Equal(created) {
		t.Fatalf("started = %v, want dateCreated %v", s.Started, created)
	}
	if s.Updated.After(written) {
		t.Fatalf("updated = %v, after the file was written at %v", s.Updated, written)
	}
	for i := 1; i < len(s.Messages); i++ {
		if !s.Messages[i].Time.After(s.Messages[i-1].Time) {
			t.Fatalf("turn %d at %v is not after turn %d at %v", i, s.Messages[i].Time, i-1, s.Messages[i-1].Time)
		}
	}
}

// With no sessions.json entry the start is the mtime itself, so every session
// ran past it, and the clamp put every turn on that one instant: the order was
// left to the slice, and the same words typed twice were one message to the
// index's dedupe. Such a session ends at the mtime instead, a second a turn.
func TestContinueTurnsWithoutADateStayApart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	sid := "8f1c2a3e-0000-4000-8000-000000000000"
	history := make([]any, 0, 4)
	for _, text := range []string{"continue", "done one", "continue", "done two"} {
		role := "user"
		if strings.HasPrefix(text, "done") {
			role = "assistant"
		}
		history = append(history, map[string]any{"message": map[string]any{"role": role, "content": text}})
	}
	path := writeContinueStore(t, root, map[string]any{"sessionId": sid, "history": history}, nil)
	written := time.Date(2026, 10, 1, 20, 33, 0, 0, time.UTC)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	s := ss[0]
	if !s.Updated.Equal(written) {
		t.Errorf("updated = %v, want the mtime %v", s.Updated, written)
	}
	for i := 1; i < len(s.Messages); i++ {
		if !s.Messages[i].Time.After(s.Messages[i-1].Time) {
			t.Fatalf("turn %d at %v is not after turn %d at %v", i, s.Messages[i].Time, i-1, s.Messages[i-1].Time)
		}
	}
}

// Continue's placeholders — "New Session" (NEW_SESSION_TITLE) and "Untitled
// Session" (the CLI's DEFAULT_SESSION_TITLE) — and a title that is only the
// first user line are no titles: left empty, the index derives one and looks
// past a greeting (#3274).
func TestContinuePlaceholderTitleIsLeftToTheIndex(t *testing.T) {
	for _, tc := range []struct {
		title, first, want string
	}{
		{"New Session", "fix the flaky retry test", ""},
		{"Untitled Session", "fix the flaky retry test", ""},
		{"hi", "hi", ""},
		{"Explaining how ContinueRoot picks its directory", "where does ContinueRoot look", "Explaining how ContinueRoot picks its directory"},
	} {
		root := t.TempDir()
		sid := "8f1c2a3e-0000-4000-8000-000000000000"
		path := writeContinueStore(t, root, map[string]any{
			"sessionId": sid, "title": tc.title, "workspaceDirectory": "/w/api",
			"history": []map[string]any{
				{"message": map[string]any{"role": "user", "content": tc.first}},
				{"message": map[string]any{"role": "assistant", "content": "on it"}},
			},
		}, nil)
		ss, err := ParseContinueFile(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%q: parse: %v, %d sessions", tc.title, err, len(ss))
		}
		if ss[0].Title != tc.want {
			t.Errorf("title %q with first line %q → %q, want %q", tc.title, tc.first, ss[0].Title, tc.want)
		}
	}
}

// Continue keeps each tool call on the assistant item: the call in
// message.toolCalls and its arguments, status and output in toolCallStates.
// Shape from the session file cn 1.5.47 wrote (#4373).
func TestContinueIndexesToolCallsFromToolCallStates(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	state := func(name, status string, args map[string]any, out string) map[string]any {
		return map[string]any{
			"toolCallId": "call_" + name, "status": status, "parsedArgs": args,
			"toolCall": map[string]any{"id": "call_" + name, "type": "function",
				"function": map[string]any{"name": name, "arguments": "{}"}},
			"output": []any{map[string]any{"name": "Tool Result", "content": out}},
		}
	}
	item := func(st map[string]any) map[string]any {
		return map[string]any{
			"message":        map[string]any{"role": "assistant", "content": "", "toolCalls": []any{st["toolCall"]}},
			"toolCallStates": []any{st},
		}
	}
	path := writeContinueStore(t, root, map[string]any{
		"sessionId":          "8f1c2a3e-0000-4000-8000-000000000000",
		"workspaceDirectory": "/w/api",
		"history": []any{
			map[string]any{"message": map[string]any{"role": "user", "content": "fix the retry loop"}},
			item(state("Bash", "done", map[string]any{"command": "git status --short && cat retry.cfg"}, "?? retry.cfg\nretry = 3\n")),
			item(state("Read", "done", map[string]any{"filepath": "/w/api/retry.cfg"}, "retry = 3\n")),
			item(state("Edit", "done", map[string]any{"file_path": "/w/api/retry.cfg", "old_string": "retry = 3", "new_string": "retry = 5 # upstream handles five fine"}, "edited")),
			item(state("Write", "done", map[string]any{"filepath": "/w/api/backoff.txt", "content": "backoff = exponential with jitter for the retry loop\n"}, "Successfully created file")),
			// A call Continue marked errored changed nothing: its output is the
			// failure, and it leaves no edit behind.
			item(state("Edit", "errored", map[string]any{"file_path": "/w/api/limits.cfg", "old_string": "cap = 1", "new_string": "cap = 2"}, "You must use the Read tool first")),
		},
	}, nil)

	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	got := map[string][]string{}
	for _, m := range ss[0].Messages {
		got[m.Role] = append(got[m.Role], m.Text)
	}
	if c := got[RoleCommand]; len(c) != 1 || c[0] != "$ git status --short && cat retry.cfg" {
		t.Fatalf("commands = %q", c)
	}
	files := strings.Join(got[RoleFiles], "\n")
	for _, p := range []string{"/w/api/retry.cfg", "/w/api/backoff.txt", "/w/api/limits.cfg"} {
		if !strings.Contains(files, p) {
			t.Fatalf("files = %q, want %s", files, p)
		}
	}
	if e := got[RoleEdit]; len(e) != 1 || e[0] != "/w/api/retry.cfg\nretry = 3" {
		t.Fatalf("edit spans = %q, want only the done Edit's old side", e)
	}
	if w := got[RoleWrote]; len(w) != 2 || !strings.HasPrefix(w[0], "/w/api/retry.cfg\n") || !strings.HasPrefix(w[1], "/w/api/backoff.txt\n") {
		t.Fatalf("wrote = %q, want the Edit's new side and the Write", w)
	}
	out := strings.Join(got[RoleToolOutput], "\n")
	if !strings.Contains(out, "retry = 3") || !strings.Contains(out, "You must use the Read tool first") {
		t.Fatalf("tool output = %q", out)
	}
}

// Feed every registry fixture through its own parser after three kinds of
// damage a real store shows: a tail cut mid-line, bytes that are not UTF-8, and
// a NUL inside a value. What comes back has to be text — the display layer
// sanitises, but a consumer that forwards Text without it (export, sync, a new
// surface) inherits whatever the parser kept (#1740).
func TestCorruptFixturesAcrossParsers(t *testing.T) {
	root := filepath.Join("..", "..", "fixtures", "registry")
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".jsonl") || strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".xml") || strings.HasSuffix(p, ".pb")) {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	damage := map[string]func([]byte) []byte{
		"truncated": func(b []byte) []byte { return b[:len(b)*6/10] },
		"badutf8":   func(b []byte) []byte { return append(append([]byte{}, b...), 0xff, 0xfe, '\n') },
		"nul":       func(b []byte) []byte { return []byte(strings.Replace(string(b), "a", "\x00", 1)) },
	}
	tmp := t.TempDir()
	checked := 0
	// Which harnesses this sweep actually reached: a fixture layout that stops
	// matching would otherwise turn the whole sweep into a silent pass.
	covered := map[string]bool{}
	for _, src := range files {
		orig, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		for name, f := range damage {
			dst := filepath.Join(tmp, name+"-"+filepath.Base(src))
			if err := os.WriteFile(dst, f(orig), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, h := range Registry() {
				for _, k := range h.Kinds {
					if k.Parse == nil {
						continue
					}
					// Match is path-shaped; call the parser directly on the
					// damaged copy of a fixture the same harness owns.
					if !strings.Contains(src, string(filepath.Separator)+harnessDir(h.Name)+string(filepath.Separator)) {
						continue
					}
					checked++
					covered[h.Name] = true
					ss, err := k.Parse(dst, 0)
					_ = err
					for _, s := range ss {
						// Metadata reaches a reader the same way the body does:
						// a project name is printed on every row.
						fields := []struct{ what, text string }{
							{"project", s.Project}, {"id", s.ID}, {"title", s.Title},
						}
						for _, m := range s.Messages {
							fields = append(fields, struct{ what, text string }{"text", m.Text})
						}
						for _, f := range fields {
							if !utf8.ValidString(f.text) {
								t.Errorf("%s %s: %s parsed invalid utf-8 into %s out of %s", h.Name, k.Name, name, f.what, filepath.Base(src))
							}
							if strings.ContainsRune(f.text, 0) {
								t.Errorf("%s %s: %s parsed a NUL into %s out of %s", h.Name, k.Name, name, f.what, filepath.Base(src))
							}
						}
					}
				}
			}
		}
	}
	t.Logf("parsed %d damaged fixture/kind pairs across %d harnesses", checked, len(covered))
	var missed []string
	for _, h := range Registry() {
		switch h.Name {
		case "deja", "opencode", "cursor", "zed", "hermes", "crush", "mimo", "teleagent", "omnigent", "orca", "doubao":
			// Database-backed or synthesised stores: their fixture is a SQL
			// script or nothing at all, so there is no transcript to damage
			// here. Their read paths are covered by their own tests.
			continue
		}
		if !covered[h.Name] {
			missed = append(missed, h.Name)
		}
	}
	if len(missed) > 0 {
		t.Errorf("the sweep reached no fixture for %v — it is passing without testing them", missed)
	}
}

func harnessDir(name string) string {
	switch name {
	case "claude":
		return "claude-code"
	case "deepseek":
		return "deepseek"
	}
	return name
}

// allocatedBy is how many bytes fn allocates.
func allocatedBy(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// Counting the corpus must not copy it: the dedupe set used the text itself
// as its key, a second copy of every message held at once, and a full build
// counts three times.
func TestCountMessagesDoesNotCopyTheText(t *testing.T) {
	const msgs, size = 400, 64 << 10
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	ss := []model.Session{{Harness: "claude", ID: "s1"}}
	for i := range msgs {
		text := strings.Repeat(string(rune('a'+i%26)), size-1) + string(rune('A'+i/26))
		ss[0].Messages = append(ss[0].Messages, model.Message{Role: "user", Text: text, Time: at.Add(time.Duration(i) * time.Second)})
	}
	// A twin of one message is still counted once.
	ss[0].Messages = append(ss[0].Messages, ss[0].Messages[7])
	var n int
	got := allocatedBy(func() { n = CountMessages(ss) })
	if n != msgs {
		t.Fatalf("CountMessages = %d, want %d", n, msgs)
	}
	text := uint64(msgs * size)
	if got > text/20 {
		t.Fatalf("CountMessages allocated %d bytes over %d bytes of text; the dedupe set holds a copy of it", got, text)
	}
}

func hermeticSourcesEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_AIDER_ROOTS", "")
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "cursor-user"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "antigravity"))
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	return tmp
}

func TestOpencodeLoadersWithFakeSQLiteAndMalformedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script sqlite3 fixture is unix-only")
	}
	tmp := hermeticSourcesEnv(t)
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bin, "sqlite3")
	// One object per line, the framing sqlite3 emits for a json_object
	// projection in its default output mode.
	out := `{"id":"s1","directory":"proj","time_created":"2026-01-02T03:00:00Z","time_updated":"2026-01-02T03:01:00Z","role":"user","text":"fake sqlite needle","pt":"2026-01-02T03:00:30Z","mt":"2026-01-02T03:00:00Z"}
{"id":"","text":"skipped"}
{"id":"s1","directory":"proj","role":"assistant","text":"","pt":1767337200000}`
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$*\" in *count*) printf '1|1\\n' ;; *badjson*) printf '{' ;; *scalar*) printf '42' ;; *) printf '%s' '"+out+"' ;; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	db := OpencodeDB()
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Resolved, not SQLite3Available: the fake answers these loaders' queries
	// and nothing else, so it would fail the probe.
	if p, err := exec.LookPath("sqlite3"); err != nil || p != script {
		t.Fatal("fake sqlite3 not found")
	}
	for name, load := range map[string]func() []model.Session{
		"all":      LoadOpencode,
		"matching": func() []model.Session { return LoadOpencodeMatching("needle'quoted") },
		"recent":   func() []model.Session { return LoadOpencodeRecent(1) },
		"since":    func() []model.Session { return LoadOpencodeSince(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) },
		"prefix":   func() []model.Session { return LoadOpencodePrefix("s") },
	} {
		got := load()
		if len(got) != 1 || got[0].ID != "s1" || len(got[0].Messages) != 1 {
			t.Fatalf("%s loader = %#v", name, got)
		}
	}
	if got := LoadOpencodeSince(time.Time{}); len(got) != 1 {
		t.Fatalf("zero since loader = %#v", got)
	}
	if s, m, err := OpencodeCounts(); err != nil || s != 1 || m != 1 {
		t.Fatalf("counts=%d %d err=%v", s, m, err)
	}
	if _, err := ParseOpencodeDBWhere(db, "badjson", 0); err == nil {
		t.Fatal("bad sqlite json returned nil error")
	}
	if _, err := ParseOpencodeDBWhere(db, "scalar", 0); err == nil || !strings.Contains(err.Error(), "bad sqlite json") {
		t.Fatalf("scalar sqlite json err=%v", err)
	}
	missing := filepath.Join(tmp, "missing.db")
	if got, err := ParseOpencodeDB(missing); err != nil || got != nil {
		t.Fatalf("missing db got=%#v err=%v", got, err)
	}
}

func TestGeminiBranchesAndLoadGemini(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	root := GeminiRoot()
	chats := filepath.Join(root, "tmp", "pid", "chats", "parent")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tmp", "pid", ".project_root"), []byte(filepath.Join(tmp, "workspace", "gem-proj")), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(chats, "s.json")
	doc := `{"sessionId":"s","startTime":"bad","lastUpdated":"2026-01-02T03:04:05Z","messages":[{"id":"m1","type":"model","content":[{"text":"a"},{"text":"b"}]},{"id":"m2","type":"user","content":{"not":"text"}}]}`
	if err := os.WriteFile(jsonPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	for i, content := range []string{"not json", `{"messages":[]}`, `{"sessionId":"empty","messages":[]}`} {
		p := filepath.Join(chats, fmt.Sprintf("skip%d.json", i))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := ParseGeminiFile(p); err != nil || got != nil {
			t.Fatalf("checkpoint skip got=%#v err=%v", got, err)
		}
	}
	jsonlPath := filepath.Join(chats, "s.jsonl")
	if err := os.WriteFile(jsonlPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseGeminiFile(jsonlPath); err != nil || got != nil {
		t.Fatalf("jsonl without metadata got=%#v err=%v", got, err)
	}
	ss := LoadGemini()
	if len(ss) != 1 || ss[0].Project != "workspace/gem-proj" || ss[0].Messages[0].Text != "a\nb" {
		t.Fatalf("LoadGemini=%#v", ss)
	}
	if got := geminiContentText(json.RawMessage(`123`)); got != "" {
		t.Fatalf("numeric content = %q", got)
	}
}

func TestCursorDiscoveryAndErrorBranches(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	userRoot := CursorUserRoot()
	global := filepath.Join(userRoot, "globalStorage", "state.vscdb")
	workspace := filepath.Join(userRoot, "workspaceStorage", "w1", "state.vscdb")
	for _, p := range []string{global, workspace} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("not sqlite"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := CursorDBs(); len(got) != 2 {
		t.Fatalf("CursorDBs=%v", got)
	}
	transcript := filepath.Join(CursorCLIRoot(), "projects", "plain", "agent-transcripts", "s", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"role":"user","message":{"content":"hi"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadCursor(); len(got) != 1 || got[0].Project != "plain" {
		t.Fatalf("LoadCursor=%#v", got)
	}
	if _, err := exec.LookPath("sqlite3"); err == nil {
		if _, err := ParseCursorDBSince(global, time.Now()); err == nil {
			t.Fatal("bad cursor db returned nil error")
		}
	}
	if got, err := ParseCursorDBSince(filepath.Join(tmp, "missing.vscdb"), time.Time{}); err != nil || got != nil {
		t.Fatalf("zero since missing got=%#v err=%v", got, err)
	}
}

func TestAiderClaudeAntigravityAndUtilityEdges(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	homeHist := filepath.Join(tmp, "home", aiderHistoryName)
	if err := os.MkdirAll(filepath.Dir(homeHist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homeHist, []byte("# aider chat started at 2026-01-01 00:00:00\n#### q\na\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadAider(); len(got) != 1 || got[0].Harness != "aider" {
		t.Fatalf("LoadAider=%#v", got)
	}
	if _, err := ParseAiderFile(filepath.Join(tmp, "missing.md")); err == nil {
		t.Fatal("missing aider file returned nil error")
	}
	if runtime.GOOS != "windows" {
		unreadable := filepath.Join(tmp, "no-read.jsonl")
		if err := os.WriteFile(unreadable, []byte("{}\n"), 0o000); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(unreadable, 0o644) }()
		if _, err := ParseAntigravityFile(unreadable); err == nil {
			t.Fatal("unreadable antigravity file returned nil error")
		}
	}
	if ClaudeProjectName(string(filepath.Separator)) != "-" || ClaudeProjectDirBase(string(filepath.Separator)) != "" || ResolveEncodedPath("not-encoded") != "" {
		t.Fatal("claude exported helpers failed edge cases")
	}
	if got := parseTimeAny(json.Number("bad")); !got.IsZero() {
		t.Fatalf("bad json number time = %v", got)
	}
	// A fractional epoch is a valid Number that Int64 rejects; it must still
	// date the turn rather than fall to the zero time (#UseNumber float loss).
	if got := parseTimeAny(json.Number("1777629600.5")); got.Unix() != 1777629600 {
		t.Fatalf("fractional epoch time = %v (want unix 1777629600)", got)
	}
	// A stringified epoch is not RFC3339 but is still a timestamp.
	if got := parseTimeAny("1777629600"); got.Unix() != 1777629600 {
		t.Fatalf("stringified epoch time = %v (want unix 1777629600)", got)
	}
	// RFC3339 that dropped its zone or seconds, or used a space instead of the
	// T, is still a timestamp — parse it rather than lose the whole date.
	for _, s := range []string{
		"2026-05-01T10:00:00",
		"2026-05-01T10:00Z",
		"2026-05-01 10:00:00",
	} {
		if got := parseTimeAny(s); got.IsZero() || got.Year() != 2026 || got.Month() != 5 {
			t.Fatalf("lenient timestamp %q = %v (want 2026-05)", s, got)
		}
	}
	// The claude fast path reads timestamps through claudeTime; it must accept
	// the same variants, not only whole-number epochs and RFC3339 strings.
	if got := claudeTime(json.RawMessage(`"1777629600"`)); got.Unix() != 1777629600 {
		t.Fatalf("claudeTime stringified epoch = %v", got)
	}
	if got := claudeTime(json.RawMessage(`1777629600.5`)); got.Unix() != 1777629600 {
		t.Fatalf("claudeTime fractional epoch = %v", got)
	}
	if got := claudeTime(json.RawMessage(`"2026-05-01T10:00Z"`)); got.IsZero() || got.Year() != 2026 {
		t.Fatalf("claudeTime seconds-less RFC3339 = %v", got)
	}
	if got := parseNestedTime(map[string]any{"time": "not-map"}, "time", "created"); !got.IsZero() {
		t.Fatalf("non-map nested time = %v", got)
	}
	if got := textFromContent(123); got != "" {
		t.Fatalf("numeric content = %q", got)
	}
	files := walkFiles(filepath.Join(tmp, "missing"), func(string) bool { return true })
	if files != nil {
		t.Fatalf("walk missing = %v", files)
	}
	parsed := parseFiles([]string{"b", "a"}, func(p string) ([]model.Session, error) {
		if p == "a" {
			return nil, os.ErrNotExist
		}
		return []model.Session{{ID: p}}, nil
	})
	if len(parsed) != 1 || parsed[0].ID != "b" {
		t.Fatalf("parseFiles error ignore/order = %#v", parsed)
	}
}

func TestSourceRemainingDiscoveryBranches(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	if ClaudeFileWanted("plain.txt") || !ClaudeFileWanted("keep.jsonl") {
		t.Fatal("ClaudeFileWanted suffix branch failed")
	}
	sub := filepath.Join("parent", "subagents", "child.jsonl")
	// A child run is read by default now — as its task and its answer, cut by
	// the parser — so the path is wanted (#3009).
	if !ClaudeFileWanted(sub) || !IsSubagentPath(sub) {
		t.Fatal("subagent transcripts are read by default")
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if !ClaudeFileWanted(sub) {
		t.Fatal("subagent include override failed")
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if ClaudeFileWanted(sub) {
		t.Fatal("the opt-out no longer skips subagent transcripts")
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	if got := itoa(0); got != "0" {
		t.Fatalf("itoa(0)=%q", got)
	}

	t.Setenv("DEJA_CURSOR_ROOT", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	xdgCursor := filepath.Join(tmp, "xdg", "Cursor", "User")
	if err := os.MkdirAll(xdgCursor, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CursorUserRoot(); runtime.GOOS != "darwin" && runtime.GOOS != "windows" && got != xdgCursor {
		t.Fatalf("CursorUserRoot xdg=%q want %q", got, xdgCursor)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "missing-xdg"))
	if got := CursorUserRoot(); !strings.Contains(got, filepath.Join(".config", "Cursor", "User")) && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Fatalf("CursorUserRoot fallback=%q", got)
	}

	ag := filepath.Join(os.Getenv("DEJA_ANTIGRAVITY_ROOT"), "brain", "badid", ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(ag), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ag, []byte(`{"source":"USER_EXPLICIT","content":"<USER_REQUEST> </USER_REQUEST>"}`+"\n"+`{"source":"MODEL","content":"no timestamp"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadAntigravity(); len(got) != 1 || got[0].Messages[0].Time != got[0].Started {
		t.Fatalf("LoadAntigravity fallback time=%#v", got)
	}
	if got, err := ParseAntigravityFile(string(filepath.Separator)); err != nil || got != nil {
		t.Fatalf("bad antigravity id got=%#v err=%v", got, err)
	}

	emptyJSONL := filepath.Join(tmp, "empty.jsonl")
	if err := os.WriteFile(emptyJSONL, []byte(`{"sessionId":"s","startTime":"2026-01-01T00:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := parseGeminiJSONL(emptyJSONL); err != nil || got != nil {
		t.Fatalf("empty gemini jsonl=%#v err=%v", got, err)
	}
	if got := geminiProjectFromRegistry("missing"); got != "" {
		t.Fatalf("missing registry project=%q", got)
	}
}

func TestCursorAndCodexRemainingParseBranches(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(tmp, "state.vscdb")
	long := strings.Repeat("z", 70*1024)
	schema := `create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:fallback', json('{"name":"Fallback","createdAt":0,"lastUpdatedAt":1752600100000}')),
 ('bubbleId:fallback:bad', json('{"type":1,"text":"","timestamp":1752600001000}')),
 ('bubbleId:wrong', json('{"type":1,"text":"skip","timestamp":1752600001000}')),
 ('bubbleId:fallback:b1', json('{"type":1.0,"text":"` + long + `","timestamp":0,"workspaceProjectDir":"projdir"}'));
`
	cmd := exec.CommandContext(t.Context(), "sqlite3", db, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss, err := ParseCursorDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("cursor fallback ss=%#v err=%v", ss, err)
	}
	// The parser no longer pre-truncates at 64 KiB: redaction runs at ingest on
	// the full message and caps it there, so the 70 KiB body passes through whole.
	if ss[0].ID != "fallback" || len(ss[0].Messages[0].Text) != 70*1024 || ss[0].Messages[0].Time != ss[0].Started {
		t.Fatalf("cursor fallback wrong: %#v", ss[0])
	}
	if _, err := cursorQuery(db, "select 1 as x where 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := cursorQuery(db, "select bad syntax"); err == nil {
		t.Fatal("bad cursor query returned nil")
	}

	hist := filepath.Join(tmp, "history.jsonl")
	if err := os.WriteFile(hist, []byte(`{"session_id":"","text":""}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseCodexHistory(hist); err != nil || got != nil {
		t.Fatalf("empty codex history=%#v err=%v", got, err)
	}
	roll := filepath.Join(tmp, "rollout-empty.jsonl")
	if err := os.WriteFile(roll, []byte(`{"timestamp":"2026-01-01T00:00:00Z"}`+"\n"+`{"payload":{"content":[]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseCodexRollout(roll); err != nil || got != nil {
		t.Fatalf("empty codex rollout=%#v err=%v", got, err)
	}
}

func TestMoreSourceEdgeBranches(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	hist := filepath.Join(tmp, "aider", ".aider.chat.history.md")
	if err := os.MkdirAll(filepath.Dir(hist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hist, []byte("preamble ignored\n# aider chat started at 2026-01-01 00:00:00\n```\ncode\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_AIDER_ROOTS", strings.Join([]string{"", filepath.Dir(hist), filepath.Dir(hist), hist}, string(os.PathListSeparator)))
	if files := AiderFiles(); len(files) != 1 {
		t.Fatalf("AiderFiles duplicate/error branches=%v", files)
	}
	if ss, err := ParseAiderFile(hist); err != nil || len(ss) != 1 || ss[0].Messages[0].Role != "assistant" {
		t.Fatalf("aider fence-first ss=%#v err=%v", ss, err)
	}
	// A single line larger than any fixed scanner cap must be read, not drop
	// every session after it. The huge user line parses and a later session
	// still appears.
	huge := filepath.Join(tmp, "huge-line.md")
	content := "# aider chat started at 2026-01-01 00:00:00\n#### " + strings.Repeat("x", 9*1024*1024) + "\n# aider chat started at 2026-01-02 00:00:00\n#### later question\n"
	if err := os.WriteFile(huge, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(huge)
	if err != nil {
		t.Fatalf("aider huge line errored: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("aider huge line dropped sessions: got %d, want 2", len(ss))
	}

	claude := filepath.Join(tmp, "claude.jsonl")
	if err := os.WriteFile(claude, []byte(`{"type":"system"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseClaudeFile(claude); err != nil || got != nil {
		t.Fatalf("claude no messages=%#v err=%v", got, err)
	}
	sub := filepath.Join(tmp, "project", "subagents", "child.jsonl")
	if err := os.MkdirAll(filepath.Dir(sub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sub, []byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseClaudeFile(sub); err != nil || len(got) != 1 {
		t.Fatalf("claude subagent dir=%#v err=%v", got, err)
	}
	if decodeProjectBase("single") != "single" || decodeProjectBase("---") != "---" || ResolveEncodedPath("-"+strings.Repeat("a-", 25)) != "" {
		t.Fatal("decodeProjectBase edge branches failed")
	}

	roll := filepath.Join(tmp, "rollout-msg.jsonl")
	if err := os.WriteFile(roll, []byte(`{"payload":{"message":"fallback message"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseCodexRollout(roll); err != nil || len(got) != 1 || got[0].Messages[0].Role != "user" {
		t.Fatalf("codex message fallback=%#v err=%v", got, err)
	}

	if got := textFromContent([]any{map[string]any{"text": "a"}, map[string]any{"text": "b"}}); got != "a\nb" {
		t.Fatalf("textFromContent newline=%q", got)
	}
	if got := parseFiles(nil, ParseClaudeFile); got != nil {
		t.Fatalf("parseFiles nil=%#v", got)
	}
	if got := cursorTranscriptProject(string(filepath.Separator)); got != "-" {
		t.Fatalf("cursorTranscriptProject root=%q", got)
	}
	if got := cursorTranscriptProject(filepath.Join("projects", "one")); got != "one" {
		t.Fatalf("cursorTranscriptProject one=%q", got)
	}
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "missing-gemini"))
	if got := GeminiChatFiles(); got != nil {
		t.Fatalf("missing GeminiChatFiles=%v", got)
	}
	if got, err := ParseGeminiFile(filepath.Join(tmp, "missing.json")); err == nil || got != nil {
		t.Fatalf("missing gemini file=%#v err=%v", got, err)
	}
	if got := geminiContentText(nil); got != "" {
		t.Fatalf("empty gemini content=%q", got)
	}
	if err := os.MkdirAll(GeminiRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(GeminiRoot(), "projects.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := geminiProjectFromRegistry("id"); got != "" {
		t.Fatalf("bad registry=%q", got)
	}
}

func TestOpencodeHasSessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticSourcesEnv(t)

	// Missing DB.
	has, err := OpencodeHasSessions()
	if err != nil || has {
		t.Fatalf("missing db: has=%v err=%v", has, err)
	}

	// Empty 0-byte DB.
	zeroDB := filepath.Join(tmp, "zero.db")
	if writeErr := os.WriteFile(zeroDB, nil, 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	t.Setenv("DEJA_OPENCODE_DB", zeroDB)
	has, err = OpencodeHasSessions()
	if err != nil || has {
		t.Fatalf("zero db: has=%v err=%v", has, err)
	}

	// v1 schema with empty session table.
	v1Empty := filepath.Join(tmp, "v1-empty.db")
	if out, cmdErr := exec.CommandContext(t.Context(), "sqlite3", v1Empty, "create table session(id text primary key);").CombinedOutput(); cmdErr != nil {
		t.Fatalf("sqlite v1 empty: %v %s", cmdErr, out)
	}
	t.Setenv("DEJA_OPENCODE_DB", v1Empty)
	has, err = OpencodeHasSessions()
	if err != nil || has {
		t.Fatalf("v1 empty: has=%v err=%v", has, err)
	}

	// v1 schema with 1 session row.
	v1Full := filepath.Join(tmp, "v1-full.db")
	if out, cmdErr := exec.CommandContext(t.Context(), "sqlite3", v1Full, "create table session(id text primary key); insert into session values('s1');").CombinedOutput(); cmdErr != nil {
		t.Fatalf("sqlite v1 full: %v %s", cmdErr, out)
	}
	t.Setenv("DEJA_OPENCODE_DB", v1Full)
	has, err = OpencodeHasSessions()
	if err != nil || !has {
		t.Fatalf("v1 full: has=%v err=%v", has, err)
	}

	// v2 schema non-empty and empty.
	v2Full := opencodeV2Fixture(t)
	t.Setenv("DEJA_OPENCODE_DB", v2Full)
	has, err = OpencodeHasSessions()
	if err != nil || !has {
		t.Fatalf("v2 full: has=%v err=%v", has, err)
	}

	v2Empty := filepath.Join(tmp, "v2-empty.db")
	if out, cmdErr := exec.CommandContext(t.Context(), "sqlite3", v2Empty, "create table session_v2(id text primary key);").CombinedOutput(); cmdErr != nil {
		t.Fatalf("sqlite v2 empty: %v %s", cmdErr, out)
	}
	t.Setenv("DEJA_OPENCODE_DB", v2Empty)
	has, err = OpencodeHasSessions()
	if err != nil || has {
		t.Fatalf("v2 empty: has=%v err=%v", has, err)
	}
}

// hermeticEnv sets up an isolated HOME/USERPROFILE plus the DEJA_* overrides
// this package reads, mirroring hermeticSourcesEnv in coverage_extra_test.go
// but returning the temp root for callers that need to build fixtures inside it.
func hermeticEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	// NotesFile and the config resolvers follow an absolute XDG_* on every
	// platform, so an exported one sent a test's writes into the developer's
	// own directories (#4405).
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "home", ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "home", ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "home", ".cache"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_AIDER_ROOTS", "")
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "cursor-user"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "antigravity"))
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))
	return tmp
}

// A developer who exports XDG_DATA_HOME had a test's notes written into their
// real data directory: NotesFile prefers an absolute XDG_DATA_HOME on every
// platform, and hermeticEnv moved only HOME (#4405).
func TestHermeticEnvKeepsXDGDirsUnderTheTempRoot(t *testing.T) {
	outside := t.TempDir()
	for _, v := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, filepath.Join(outside, v))
	}
	tmp := hermeticEnv(t)
	if !strings.HasPrefix(NotesFile(), tmp+string(filepath.Separator)) {
		t.Errorf("NotesFile = %s, want it under the temp root %s", NotesFile(), tmp)
	}
	for _, v := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		if got := os.Getenv(v); strings.HasPrefix(got, outside) {
			t.Errorf("%s = %s, still the exported one", v, got)
		}
	}
}

// --- codex.go: CodexFiles (0%) ---

func TestCodexFilesListsRolloutsAndHistory(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	rollDir := filepath.Join(root, "sessions", "2026", "01", "02")
	if err := os.MkdirAll(rollDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roll := filepath.Join(rollDir, "rollout-2026-01-02T03-04-05-r.jsonl")
	if err := os.WriteFile(roll, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(rollDir, "notes.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CODEX_ROOT", root)

	// Without history.jsonl present, only the rollout should be listed.
	files := CodexFiles()
	if len(files) != 1 || files[0] != roll {
		t.Fatalf("CodexFiles without history = %v, want [%s]", files, roll)
	}

	hist := filepath.Join(root, "history.jsonl")
	if err := os.WriteFile(hist, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files = CodexFiles()
	if len(files) != 2 {
		t.Fatalf("CodexFiles with history = %v, want 2 entries", files)
	}
	foundRoll, foundHist := false, false
	for _, f := range files {
		if f == roll {
			foundRoll = true
		}
		if f == hist {
			foundHist = true
		}
	}
	if !foundRoll || !foundHist {
		t.Fatalf("CodexFiles missing entries = %v", files)
	}
}

// --- grok.go: LoadGrok and assorted branches (0%/86%/85%/90%/86%/60%/80%) ---

func grokFixtureTree(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "grok")
	t.Setenv("DEJA_GROK_ROOT", root)
	group := url.PathEscape("/work/needle-project")
	dir := filepath.Join(root, "sessions", group, "load-grok-session")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "updates.jsonl")
}

func TestLoadGrokDiscoversAndParses(t *testing.T) {
	updates := grokFixtureTree(t)
	lines := `{"timestamp":1,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}` + "\n"
	if err := os.WriteFile(updates, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss := LoadGrok()
	if len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("LoadGrok = %#v", ss)
	}
}

func TestParseGrokFileFallbacksAndDefaultKind(t *testing.T) {
	updates := grokFixtureTree(t)
	// No summary.json at all: id falls back to the session dir name, cwd
	// falls back to grokCWDFromPath (encoded group segment), title falls
	// back from empty generated_title to session_summary.
	lines := `{"timestamp":1,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"},"_meta":{"promptIndex":0}}}}` + "\n" +
		// contains the literal user_message_chunk marker in an unrelated field
		// so the byte-level prefilter matches, but the real sessionUpdate kind
		// does not - exercising the callback's default: return branch.
		`{"timestamp":2,"note":"user_message_chunk","params":{"update":{"sessionUpdate":"tool_call","content":{"type":"text","text":"tool noise"}}}}` + "\n" +
		// matching kind but empty rendered text - exercises the text=="" return.
		`{"timestamp":3,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{}}}}` + "\n" +
		// assistant chunk without a promptId - grokMessageKey returns "".
		`{"timestamp":4,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}}}}` + "\n"
	if err := os.WriteFile(updates, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(updates)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("ParseGrokFile = %#v", ss)
	}
	s := ss[0]
	if s.ID != "load-grok-session" {
		t.Fatalf("fallback id = %q", s.ID)
	}
	if s.Project != "work/needle-project" {
		t.Fatalf("fallback cwd/project = %q", s.Project)
	}
	// Speech plus the tool call; only the empty-text event is filtered (#1321).
	if len(s.Messages) != 3 {
		t.Fatalf("messages = %#v, want 3 (only empty text filtered)", s.Messages)
	}
}

func TestParseGrokFileTitleFallsBackToSummary(t *testing.T) {
	updates := grokFixtureTree(t)
	summary := `{"info":{"id":"x","cwd":"/work/needle-project"},"session_summary":"fallback summary"}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(updates), "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(updates, []byte(`{"timestamp":1,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(updates)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseGrokFile = %#v err=%v", ss, err)
	}
	if ss[0].Title != "fallback summary" {
		t.Fatalf("title = %q, want fallback summary", ss[0].Title)
	}
}

func TestGrokContentTextTypeMismatchReturnsEmpty(t *testing.T) {
	// block.Text != "" but block.Type is neither "" nor "text": grokContentText
	// must reject it rather than returning the text of an unknown block kind.
	got := grokContentText([]byte(`{"type":"image","text":"ignored"}`))
	if got != "" {
		t.Fatalf("grokContentText type mismatch = %q, want empty", got)
	}
}

func TestScanGrokUpdatesOpenError(t *testing.T) {
	if err := scanGrokUpdates(filepath.Join(t.TempDir(), "missing.jsonl"), func(grokUpdateEvent) {}); err == nil {
		t.Fatal("scanGrokUpdates on missing file returned nil error")
	}
}

func TestScanGrokUpdatesReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reading a directory as a file is unix-only")
	}
	dir := t.TempDir()
	if err := scanGrokUpdates(dir, func(grokUpdateEvent) {}); err == nil {
		t.Fatal("scanGrokUpdates on a directory returned nil error")
	}
}

func TestGrokCWDForSessionEmptyPath(t *testing.T) {
	if GrokCWDForSession("") != "" {
		t.Fatal("GrokCWDForSession(\"\") should be empty")
	}
}

func TestGrokCWDForSessionPrefersSummary(t *testing.T) {
	updates := grokFixtureTree(t)
	summary := `{"info":{"cwd":"/work/from-summary"}}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(updates), "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := GrokCWDForSession(updates); got != "/work/from-summary" {
		t.Fatalf("GrokCWDForSession = %q, want /work/from-summary", got)
	}
}

func TestGrokCWDFromPathEdgeCases(t *testing.T) {
	if grokCWDFromPath("") != "" {
		t.Fatal("grokCWDFromPath(\"\") should be empty")
	}
	// A group segment that is not valid percent-encoding must not panic and
	// must resolve to "".
	bad := filepath.Join(t.TempDir(), "sessions", "%zz-bad-escape", "s", "updates.jsonl")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := grokCWDFromPath(bad); got != "" {
		t.Fatalf("grokCWDFromPath bad escape = %q, want empty", got)
	}
}

// --- claude.go: decodeProjectBase (87%), ClaudeProjectDirBase (80%) ---

func TestDecodeProjectBaseSingleResolvedSegment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on unix-style single-segment absolute path resolution")
	}
	if _, err := os.Stat(string(filepath.Separator) + "tmp"); err != nil {
		t.Skip("/tmp not present on this system")
	}
	if got := decodeProjectBase("-tmp"); got != "tmp" {
		t.Fatalf("decodeProjectBase(-tmp) = %q, want tmp", got)
	}
}

func TestClaudeProjectDirBaseReturnsBase(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	path := filepath.Join(root, "-tmp-demo-project", "session.jsonl")
	if got := ClaudeProjectDirBase(path); got != "-tmp-demo-project" {
		t.Fatalf("ClaudeProjectDirBase = %q, want -tmp-demo-project", got)
	}
}

// --- antigravity.go: AntigravityRoots (90%) ---

func TestAntigravityRootsBadGlobPattern(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", "")
	// An unmatched '[' in HOME makes the antigravity glob pattern invalid,
	// so filepath.Glob returns ErrBadPattern and AntigravityRoots must
	// swallow it as nil rather than propagating.
	badHome := filepath.Join(tmp, "wei[rd-home")
	t.Setenv("HOME", badHome)
	t.Setenv("USERPROFILE", badHome)
	if got := AntigravityRoots(); got != nil {
		t.Fatalf("AntigravityRoots with bad glob = %v, want nil", got)
	}
}

// --- gemini.go: GeminiChatFiles (92%), geminiProjectFromRegistry (90%) ---

func TestGeminiChatFilesSkipsNonDirEntries(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	tmpDir := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain file sitting next to the per-session directories must be
	// skipped instead of treated as a project id.
	if err := os.WriteFile(filepath.Join(tmpDir, "not-a-dir.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	chats := filepath.Join(tmpDir, "pid", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(chats, "s.json")
	if err := os.WriteFile(sessionFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := GeminiChatFiles()
	if len(got) != 1 || got[0] != sessionFile {
		t.Fatalf("GeminiChatFiles = %v, want [%s]", got, sessionFile)
	}
}

func TestGeminiProjectFromRegistryNoMatch(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := `{"projects":{"/work/other":"other-id"}}`
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := geminiProjectFromRegistry("does-not-exist"); got != "" {
		t.Fatalf("geminiProjectFromRegistry no match = %q, want empty", got)
	}
}

// --- cursor.go: numberVal (80%), CursorDBs/CursorTranscripts (91%/92%),
// ParseCursorTranscript (94%), cursorQuery (91%) ---

func TestNumberValFloat64Case(t *testing.T) {
	n, ok := numberVal(float64(42))
	if !ok || n != 42 {
		t.Fatalf("numberVal(float64(42)) = %d,%v", n, ok)
	}
	if n, ok := numberVal("not a number"); ok || n != 0 {
		t.Fatalf("numberVal(unsupported) = %d,%v, want 0,false", n, ok)
	}
}

func TestCursorDBsSkipsNonDirWorkspaceEntries(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "cursor-user")
	t.Setenv("DEJA_CURSOR_ROOT", root)
	ws := filepath.Join(root, "workspaceStorage")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain file among the workspace directories must be skipped.
	if err := os.WriteFile(filepath.Join(ws, "not-a-dir"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved := filepath.Join(ws, "w1", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolved, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CursorDBs()
	if len(got) != 1 || got[0] != resolved {
		t.Fatalf("CursorDBs = %v, want [%s]", got, resolved)
	}
}

func TestCursorTranscriptsSkipsNonJSONLFiles(t *testing.T) {
	tmp := hermeticEnv(t)
	cliRoot := filepath.Join(tmp, "cursor-cli")
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cliRoot)
	dir := filepath.Join(cliRoot, "projects", "p", "agent-transcripts", "s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CursorTranscripts()
	if len(got) != 1 || got[0] != transcript {
		t.Fatalf("CursorTranscripts = %v, want [%s]", got, transcript)
	}
}

func TestParseCursorTranscriptSkipsNonMapMessage(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "s.jsonl")
	lines := `{"role":"user","message":"not an object"}` + "\n" +
		`{"role":"user","message":{"content":"hi"}}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCursorTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 || ss[0].Messages[0].Text != "hi" {
		t.Fatalf("ParseCursorTranscript = %#v", ss)
	}
}

func TestParseCursorDBNoComposers(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "state.vscdb")
	// A DB file that exists and is non-empty but has no composerData rows:
	// cursorQuery succeeds with zero rows, exercising the len(composers)==0
	// early return.
	schema := `create table cursorDiskKV (key text primary key, value text);`
	cmd := exec.CommandContext(t.Context(), "sqlite3", db, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss, err := ParseCursorDB(db)
	if err != nil || ss != nil {
		t.Fatalf("ParseCursorDB empty composers = %#v err=%v", ss, err)
	}
}

func TestCursorQueryBadJSONOutput(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bin, "sqlite3")
	// Emits syntactically-invalid JSON so the decoder fails after the
	// initial (non-error) invocation, exercising cursorQuery's decode error.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '[not valid json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := cursorQuery(filepath.Join(tmp, "x.db"), "select 1"); err == nil {
		t.Fatal("cursorQuery with malformed JSON returned nil error")
	}
}

// crushJoinRecords builds one session whose rows are, in order, the calls and
// results given as {kind, id, tool, payload, isError}.
func crushJoinRecords(t *testing.T, session string, rows [][]any) []string {
	t.Helper()
	sql := "insert into sessions values ('" + session + "',null,'retry',2,0,0,0.0,1784282499,1784282400,null,null);\n"
	var sqlSb13 strings.Builder
	for i, r := range rows {
		kind, _ := r[0].(string)
		id, _ := r[1].(string)
		tool, _ := r[2].(string)
		payload, _ := r[3].(string)
		isErr, _ := r[4].(bool)
		at := int64(1784282400 + i)
		if kind == "call" {
			sqlSb13.WriteString(crushInsert(t, "m"+itoa(i), session, "assistant", at, []any{
				map[string]any{"type": "tool_call", "data": map[string]any{"id": id, "name": tool, "input": payload, "finished": true}}}))
		} else {
			sqlSb13.WriteString(crushInsert(t, "m"+itoa(i), session, "tool", at, []any{
				map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": id, "name": tool, "content": payload, "is_error": isErr}}}))
		}
	}
	sql += sqlSb13.String()
	ss, err := ParseCrushDB(crushStore(t, "proj", sql))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range ss {
		for _, m := range s.Messages {
			switch m.Role {
			case RoleToolOutput:
			case RoleWrote:
				got = append(got, "wrote")
			default:
				got = append(got, m.Role+" "+m.Text)
			}
		}
	}
	return got
}

// A result answers the one call before it. A provider that numbers its calls
// per turn hands out the same id again, and a later refusal or failure under
// that id must not reach back to an earlier call that went through.
func TestCrushJoinReusedCallID(t *testing.T) {
	edit := `{"file_path":"/tmp/proj/retry.go","old_string":"\tfor {","new_string":"\tfor i := 0; i < 3; i++ {"}`
	got := crushJoinRecords(t, "reused", [][]any{
		{"call", "call_0", "edit", edit, false},
		{"result", "call_0", "edit", "Content replaced in file: /tmp/proj/retry.go", false},
		{"call", "call_0", "bash", `{"command":"go test ./retry"}`, false},
		{"result", "call_0", "bash", "ok  retry 0.01s\n\n<cwd>/tmp/proj</cwd>", false},
		{"call", "call_0", "edit", edit, false},
		{"result", "call_0", "edit", "you must read the file before editing it. Use the View tool first", true},
		{"call", "call_0", "bash", `{"command":"go vet ./retry"}`, false},
		{"result", "call_0", "bash", "boom\nExit code 1\n\n<cwd>/tmp/proj</cwd>", false},
	})
	want := []string{
		"files /tmp/proj/retry.go", "edit /tmp/proj/retry.go\n\tfor {", "wrote",
		"command go test ./retry",
		"files /tmp/proj/retry.go",
		"command go vet ./retry  → exit 1",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("records =\n%q\nwant\n%q", got, want)
	}
}

// Crush appends its <cwd> tag after everything the command printed; a tag in
// the command's own output is output, and the exit line still sits above the
// appended one.
func TestCrushExitWhenOutputHoldsACwdTag(t *testing.T) {
	got := crushJoinRecords(t, "cwdtag", [][]any{
		{"call", "c1", "bash", `{"command":"make check"}`, false},
		{"result", "c1", "bash", "<cwd>/old</cwd> from last run\ncat: more.txt: No such file\nExit code 1\n\n<cwd>/tmp/proj</cwd>", false},
	})
	want := []string{"command make check  → exit 1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("records = %q, want %q", got, want)
	}
}

// A call the user denied never ran: crush stores "User denied permission" with
// is_error as its result. It left a command record as if it had run, and a
// denied edit its files record (#4575). A call that ran and failed keeps both.
func TestCrushDeniedCallLeavesNoRecord(t *testing.T) {
	edit := `{"file_path":"/tmp/proj/retry.go","old_string":"\tfor {","new_string":"\tfor i := 0; i < 3; i++ {"}`
	got := crushJoinRecords(t, "denied", [][]any{
		{"call", "c1", "bash", `{"command":"git push --force origin main"}`, false},
		{"result", "c1", "bash", "User denied permission", true},
		{"call", "c2", "edit", edit, false},
		{"result", "c2", "edit", "User denied permission", true},
		{"call", "c3", "view", `{"file_path":"/etc/hosts"}`, false},
		{"result", "c3", "view", "User denied permission", true},
		{"call", "c4", "bash", `{"command":"go vet ./retry"}`, false},
		{"result", "c4", "bash", "boom\nExit code 1\n\n<cwd>/tmp/proj</cwd>", false},
		{"call", "c5", "edit", edit, false},
		{"result", "c5", "edit", "old_string not found in file", true},
	})
	want := []string{
		"command go vet ./retry  → exit 1",
		"files /tmp/proj/retry.go",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("records =\n%q\nwant\n%q", got, want)
	}
}

// lsp_replace_symbol's call carries only what it wrote. Its result's metadata,
// ReplaceSymbolResponseMetadata {file_path, old_content, new_content, action},
// holds the file before and after, which is where the replaced symbol is
// (Crush internal/agent/tools/lsp_replace_symbol.go). A failed call and an
// insert after a symbol replaced nothing (#595).
func TestCrushReplaceSymbolRecordsTheReplacedSymbol(t *testing.T) {
	const (
		head   = "package retry\n\nimport \"time\"\n\n"
		before = "func backoffJitter(attempt int) time.Duration {\n\treturn time.Duration(attempt) * time.Second\n}"
		after  = "func backoffJitter(attempt int) time.Duration {\n\treturn time.Duration(rand.Int63n(int64(attempt+1)))\n}"
		tail   = "\n\nfunc keep() {}\n"
	)
	meta := func(path, old, nw, action string) string {
		return vocabJSON(map[string]any{"file_path": path, "old_content": old, "new_content": nw, "action": action})
	}
	call := func(id, path, action string) string {
		return crushInsert(t, "a_"+id, "s1", "assistant", 1784282401, []any{
			map[string]any{"type": "tool_call", "data": map[string]any{"id": "c_" + id, "name": "lsp_replace_symbol",
				"input": vocabJSON(map[string]any{"symbol": "backoffJitter", "file_path": path, "replacement": after, "action": action}), "finished": true}}})
	}
	result := func(id, content, metadata string, isErr bool) string {
		return crushInsert(t, "r_"+id, "s1", "tool", 1784282402, []any{
			map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": "c_" + id, "name": "lsp_replace_symbol",
				"content": content, "metadata": metadata, "is_error": isErr}}})
	}
	sql := "insert into sessions values ('s1',null,'retry',2,0,0,0.0,1784282405,1784282400,null,null);\n" +
		call("1", "/tmp/proj/jitter.go", "replace") +
		result("1", "Replaced symbol 'backoffJitter' in /tmp/proj/jitter.go (lines 5-7)\n<cwd>/tmp/proj</cwd>",
			meta("/tmp/proj/jitter.go", head+before+tail, head+after+tail, "replace"), false) +
		call("2", "/tmp/proj/refused.go", "replace") +
		result("2", "symbol not found", meta("/tmp/proj/refused.go", head+before+tail, head+after+tail, "replace"), true) +
		call("3", "/tmp/proj/added.go", "add_after") +
		result("3", "Inserted after symbol 'backoffJitter' in /tmp/proj/added.go (after line 7)",
			meta("/tmp/proj/added.go", head+before+tail, head+before+"\n"+after+tail, "add_after"), false) +
		call("4", "/tmp/proj/plain.go", "replace") +
		result("4", "Replaced symbol 'backoffJitter'", meta("/tmp/proj/plain.go", head+before+tail, head+after+tail, "replace"), false)
	ss, err := ParseCrushDB(crushStore(t, "proj", sql))
	if err != nil {
		t.Fatal(err)
	}
	// The summary names the lines, so the whole symbol is the span; with no
	// range, the lines the two contents do not share stand in.
	vocabCheck(t, ss, []string{
		vocabEdit("/tmp/proj/jitter.go", before),
		vocabWrote("/tmp/proj/jitter.go", after),
		vocabEdit("/tmp/proj/plain.go", "\treturn time.Duration(attempt) * time.Second"),
	}, []string{
		vocabEdit("/tmp/proj/refused.go", "\treturn time.Duration(attempt) * time.Second"),
	})
	if got := vocabRoles(ss, RoleEdit); len(got) != 2 {
		t.Fatalf("edits = %q, want the two replaced symbols", got)
	}
}

const crushSchema = `create table sessions (
  id text primary key, parent_session_id text, title text not null,
  message_count integer not null default 0, prompt_tokens integer not null default 0,
  completion_tokens integer not null default 0, cost real not null default 0.0,
  updated_at integer not null, created_at integer not null,
  summary_message_id text, todos text);
create table messages (
  id text primary key, session_id text not null, role text not null,
  parts text not null default '[]', model text,
  created_at integer not null, updated_at integer not null,
  finished_at integer, provider text, is_summary_message integer default 0 not null);
`

// crushStore builds a store where Crush puts one: under the project it belongs
// to. The depth is the attribution, so a helper that flattened it would test a
// path the parser never sees.
func crushStore(t *testing.T, project, sql string) string {
	t.Helper()
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), project, ".crush", "crush.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(crushSchema + sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build store: %v: %s", err, out)
	}
	return db
}

func crushParts(t *testing.T, parts any) string {
	t.Helper()
	b, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	// Through the JSON encoder rather than pasted: a hand-written literal with
	// a quote or a backslash in it is a broken SQL string, not a test.
	return "'" + strings.ReplaceAll(string(b), "'", "''") + "'"
}

func crushInsert(t *testing.T, id, session, role string, at int64, parts any) string {
	t.Helper()
	return "insert into messages values ('" + id + "','" + session + "','" + role + "'," +
		crushParts(t, parts) + ",'m'," + itoa(int(at)) + "," + itoa(int(at)) + ",null,'p',0);\n"
}

// The whole read, end to end: speech under the speaker, the shell command as a
// command record, what the tool printed as tool output, and the edited path as
// a file record. Each of the four is a different surface — search, `how`,
// error matching and `blame` — and a parser that drops one is silent there.
func TestCrushReadsEveryRoleOutOfOneSession(t *testing.T) {
	sql := "insert into sessions values ('s1',null,'ship the fix',4,0,0,0.0,1784282403,1784282400,null,null);\n"
	sql += crushInsert(t, "m1", "s1", "user", 1784282400, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "run the build"}},
		map[string]any{"type": "finish", "data": map[string]any{"reason": "stop"}},
	})
	sql += crushInsert(t, "m2", "s1", "assistant", 1784282401, []any{
		map[string]any{"type": "tool_call", "data": map[string]any{
			"id": "call_0", "name": "bash",
			"input": `{"command": "go test ./internal/zibblex", "description": "run the build"}`,
		}},
	})
	sql += crushInsert(t, "m3", "s1", "tool", 1784282402, []any{
		map[string]any{"type": "tool_result", "data": map[string]any{
			"tool_call_id": "call_0", "name": "bash",
			"content": "ok  zibblex 0.4s\n\n<cwd>/workspace/demo</cwd>",
		}},
	})
	sql += crushInsert(t, "m4", "s1", "assistant", 1784282403, []any{
		map[string]any{"type": "tool_call", "data": map[string]any{
			"id": "call_1", "name": "edit",
			"input": `{"file_path": "/workspace/demo/zibblex.go", "old_string": "a", "new_string": "b"}`,
		}},
	})

	db := crushStore(t, "demo", sql)
	ss, err := ParseCrushDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "crush" || s.ID != "s1" || s.Title != "ship the fix" {
		t.Fatalf("session header: %#v", s)
	}
	// The project is the directory the store sits under, not ".crush".
	if want := filepath.Base(filepath.Dir(CrushProjectDir(db))) + "/demo"; s.Project != want {
		t.Fatalf("project = %q, want %q", s.Project, want)
	}
	got := map[string]string{}
	for _, m := range s.Messages {
		got[m.Role] = m.Text
	}
	for _, want := range []struct{ role, text string }{
		{"user", "run the build"},
		{RoleCommand, "go test ./internal/zibblex"},
		{RoleToolOutput, "ok  zibblex 0.4s"},
		{RoleFiles, "/workspace/demo/zibblex.go"},
	} {
		if strings.TrimSpace(got[want.role]) != want.text {
			t.Errorf("%s = %q, want %q (all: %#v)", want.role, got[want.role], want.text, got)
		}
	}
	// The <cwd> tag is the same directory on every tool result in the store;
	// left in, a search for the project name matches every one of them.
	if strings.Contains(got[RoleToolOutput], "cwd") {
		t.Errorf("the cwd tag reached the index: %q", got[RoleToolOutput])
	}
}

// Crush's schema comments both stamp columns as milliseconds and the release
// writes whole seconds. Read as milliseconds a seconds row lands in 1970, and
// read as seconds a millisecond row lands in the year 58000 — which takes the
// top of every recency surface and keeps it.
func TestCrushReadsSecondsAndMillisecondStamps(t *testing.T) {
	const wantYear = 2026
	for _, unit := range []struct {
		name string
		at   int64
	}{{"seconds", 1784282400}, {"milliseconds", 1784282400000}} {
		t.Run(unit.name, func(t *testing.T) {
			sql := "insert into sessions values ('s1',null,'t',1,0,0,0.0," + itoa(int(unit.at)) + "," + itoa(int(unit.at)) + ",null,null);\n"
			sql += crushInsert(t, "m1", "s1", "user", unit.at, []any{
				map[string]any{"type": "text", "data": map[string]any{"text": "hello"}},
			})
			ss, err := ParseCrushDB(crushStore(t, "demo", sql))
			if err != nil || len(ss) != 1 {
				t.Fatalf("parse: %v %d", err, len(ss))
			}
			if y := ss[0].Messages[0].Time.Year(); y != wantYear {
				t.Fatalf("%s row dated %d, want %d", unit.name, y, wantYear)
			}
		})
	}
}

// A since-pass has to come back with the session that moved and skip the one
// that did not; the watermark and the row are in different units, which is the
// bug this guards (#2064).
func TestCrushSincePassSkipsWhatDidNotMove(t *testing.T) {
	old, recent := int64(1684282400), int64(1784282400)
	sql := "insert into sessions values ('old',null,'old',1,0,0,0.0," + itoa(int(old)) + "," + itoa(int(old)) + ",null,null);\n"
	sql += "insert into sessions values ('new',null,'new',1,0,0,0.0," + itoa(int(recent)) + "," + itoa(int(recent)) + ",null,null);\n"
	sql += crushInsert(t, "m1", "old", "user", old, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "stale turn"}},
	})
	sql += crushInsert(t, "m2", "new", "user", recent, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "fresh turn"}},
	})
	db := crushStore(t, "demo", sql)

	all, err := ParseCrushDB(db)
	if err != nil || len(all) != 2 {
		t.Fatalf("full pass: %v, %d sessions", err, len(all))
	}
	since, err := ParseCrushDBSince(db, time.Unix(recent-60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].ID != "new" {
		t.Fatalf("since pass = %#v, want only the session that moved", since)
	}
}

// An edit, a multiedit and a write carry both sides of the change in the
// call's input, and restore and blame read them from the edit and wrote
// records. A Crush session that kept only the path had nothing for either
// (#4377).
func TestCrushEditsAndWritesLeaveEditAndWroteRecords(t *testing.T) {
	sql := "insert into sessions values ('s1',null,'retry',3,0,0,0.0,1784282403,1784282400,null,null);\n"
	call := func(id, name, input string) []any {
		return []any{map[string]any{"type": "tool_call", "data": map[string]any{"id": id, "name": name, "input": input}}}
	}
	sql += crushInsert(t, "m1", "s1", "assistant", 1784282400, call("c1", "edit",
		`{"file_path": "/w/retry.cfg", "old_string": "retry = 3", "new_string": "retry = 5 # five retries for the upstream"}`))
	sql += crushInsert(t, "m2", "s1", "assistant", 1784282401, call("c2", "multiedit",
		`{"file_path": "/w/limits.cfg", "edits": [{"old_string": "max = 1", "new_string": "max = 2 # two parallel uploads at most"}, {"old_string": "min = 0", "new_string": "min = 1 # keep one warm connection", "replace_all": true}]}`))
	sql += crushInsert(t, "m3", "s1", "assistant", 1784282402, call("c3", "write",
		`{"file_path": "/w/backoff.txt", "content": "backoff = exponential with jitter for the retry loop\nshort\n"}`))
	// A view names a file and changes nothing: it must not read as an edit.
	sql += crushInsert(t, "m4", "s1", "assistant", 1784282403, call("c4", "view",
		`{"file_path": "/w/notes.txt", "old_string": "not an edit"}`))

	ss, err := ParseCrushDB(crushStore(t, "demo", sql))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var edits, wrote []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleEdit:
			edits = append(edits, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		}
	}
	wantEdits := []string{"/w/retry.cfg\nretry = 3", "/w/limits.cfg\nmax = 1", "/w/limits.cfg\nmin = 0"}
	if strings.Join(edits, "|") != strings.Join(wantEdits, "|") {
		t.Errorf("edit records = %q, want %q", edits, wantEdits)
	}
	wantWrote := []string{
		WroteRecord("/w/retry.cfg", "retry = 5 # five retries for the upstream"),
		WroteRecord("/w/limits.cfg", "max = 2 # two parallel uploads at most"),
		WroteRecord("/w/limits.cfg", "min = 1 # keep one warm connection"),
		WroteRecord("/w/backoff.txt", "backoff = exponential with jitter for the retry loop\nshort\n"),
	}
	for _, w := range wantWrote {
		if w == "" {
			t.Fatal("a wanted wrote record is empty: the fixture's lines are too short to be evidence")
		}
	}
	if strings.Join(wrote, "|") != strings.Join(wantWrote, "|") {
		t.Errorf("wrote records = %q, want %q", wrote, wantWrote)
	}
}

// Crush spawns subagent sessions, and the row naming the parent is the only
// place that edge exists. Without it a subagent's turns look like a separate
// piece of work by someone else.
func TestCrushKeepsTheSubagentEdge(t *testing.T) {
	sql := "insert into sessions values ('parent',null,'top',1,0,0,0.0,1784282400,1784282400,null,null);\n"
	sql += "insert into sessions values ('child','parent','sub',1,0,0,0.0,1784282401,1784282401,null,null);\n"
	sql += crushInsert(t, "m1", "parent", "user", 1784282400, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "delegate it"}},
	})
	sql += crushInsert(t, "m2", "child", "assistant", 1784282401, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "on it"}},
	})
	ss, err := ParseCrushDB(crushStore(t, "demo", sql))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		switch s.ID {
		case "parent":
			if s.Kind == "subagent" || s.Parent != "" {
				t.Errorf("the top-level session was filed as a subagent: %#v", s)
			}
		case "child":
			if s.Kind != "subagent" || s.Parent != "parent" {
				t.Errorf("subagent edge lost: kind=%q parent=%q", s.Kind, s.Parent)
			}
		}
	}
}

// The parts column is JSON inside a database, so the corruption sweep over file
// fixtures never reaches it. A NUL or a bad byte that survives here reaches
// every consumer that forwards Text without the display layer's sanitiser
// (#1740).
func TestCrushDamagedPartsDoNotReachTheIndex(t *testing.T) {
	sql := "insert into sessions values ('s1',null,'t',3,0,0,0.0,1784282400,1784282400,null,null);\n"
	// A real NUL and a lone surrogate escape, both of which a store can hold
	// and neither of which is text.
	sql += "insert into messages values ('m1','s1','user','[{\"type\":\"text\",\"data\":{\"text\":\"before\\u0000after\"}}]','m',1784282400,1784282400,null,'p',0);\n"
	sql += "insert into messages values ('m2','s1','assistant','[{\"type\":\"text\",\"data\":{\"text\":\"lone \\ud800 half\"}}]','m',1784282401,1784282401,null,'p',0);\n"
	// And a parts column that is not JSON at all: a half-written row is
	// ordinary, and it must cost that message rather than the session.
	sql += "insert into messages values ('m3','s1','assistant','[{\"type\":\"tex','m',1784282402,1784282402,null,'p',0);\n"
	sql += crushInsert(t, "m4", "s1", "assistant", 1784282403, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "the readable turn"}},
	})

	ss, err := ParseCrushDB(crushStore(t, "demo", sql))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1 — a damaged row cost the session", len(ss))
	}
	readable := false
	for _, m := range ss[0].Messages {
		if !utf8.ValidString(m.Text) {
			t.Errorf("invalid utf-8 reached the index: %q", m.Text)
		}
		if strings.ContainsRune(m.Text, 0) {
			t.Errorf("a NUL reached the index: %q", m.Text)
		}
		if m.Text == "the readable turn" {
			readable = true
		}
	}
	if !readable {
		t.Error("the undamaged turn did not come back")
	}
}

// The registry is what makes the stores findable — nothing under the data home
// holds a transcript. An entry for a project that has since been deleted is
// ordinary, and must not become an error or an empty row.
func TestCrushDBsComeFromTheRegistryAndSkipWhatIsGone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CRUSH_ROOT", root)

	live := filepath.Join(t.TempDir(), "live", ".crush")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "crush.db"), []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	// One with data_dir spelled out, one with only a path — Crush writes both
	// — and one that no longer exists.
	byPath := filepath.Join(t.TempDir(), "bypath")
	if err := os.MkdirAll(filepath.Join(byPath, ".crush"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(byPath, ".crush", "crush.db"), []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"projects": []any{
		map[string]any{"path": filepath.Dir(live), "data_dir": live},
		map[string]any{"path": byPath},
		map[string]any{"path": filepath.Join(root, "gone"), "data_dir": filepath.Join(root, "gone", ".crush")},
	}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	got := CrushDBs()
	if len(got) != 2 {
		t.Fatalf("CrushDBs() = %v, want the two stores that exist", got)
	}
	if got[0] != filepath.Join(live, "crush.db") {
		t.Errorf("data_dir entry: %q", got[0])
	}
	if got[1] != filepath.Join(byPath, ".crush", "crush.db") {
		t.Errorf("path-only entry did not fall back to <project>/.crush: %q", got[1])
	}

	// No registry at all is a machine without Crush, not an error.
	t.Setenv("DEJA_CRUSH_ROOT", filepath.Join(root, "nowhere"))
	if got := CrushDBs(); len(got) != 0 {
		t.Fatalf("a machine with no registry listed %v", got)
	}
}

// The layout list covers the pieces a store may leave out — a zone, seconds, a
// T where a space is — because losing one loses the whole stamp: the session
// sorts as "-", `deja last` never shows it and no `--since` window contains it.
// A date with no time at all was the one shape still missing.
func TestADateWithNoTimeKeepsItsDate(t *testing.T) {
	got := parseTimeAny("2026-09-06")
	if got.IsZero() {
		t.Fatal("a date-only stamp lost the whole date")
	}
	if y, m, d := got.Date(); y != 2026 || m != time.September || d != 6 {
		t.Errorf("parsed %s, want 2026-09-06", got.Format(time.RFC3339))
	}
	if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("a date with no time got a clock reading: %s", got.Format(time.RFC3339))
	}
	// And the shapes that are not a stamp at all still return zero, so a
	// session is not dated from a version string or a path fragment. A bare
	// number is left out of this list on purpose: the field is always a
	// timestamp, and a store that stringifies the epoch writes one.
	for _, in := range []string{"", "2026-09", "v1.2.3", "internal/index", "September 6"} {
		if got := parseTimeAny(in); !got.IsZero() {
			t.Errorf("%q was read as %s", in, got.Format(time.RFC3339))
		}
	}
}

// Harnesses change their schemas — goose and hermes both did while deja was
// being written. When that happens the query fails, and the only acceptable
// outcome is an error the caller can report: silently returning nothing would
// look exactly like "you have no history".
func TestDBParsersReportSchemaFailuresRatherThanEmptiness(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()

	// A database that exists but has none of the tables we query.
	wrong := filepath.Join(dir, "wrong.db")
	if out, err := exec.CommandContext(t.Context(), "sqlite3", wrong, "CREATE TABLE unrelated (id TEXT);").CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"goose":    func(p string) ([]model.Session, error) { return ParseGooseDBSince(p, time.Time{}) },
		"hermes":   func(p string) ([]model.Session, error) { return ParseHermesDBSince(p, time.Time{}) },
		"grok":     func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) },
		"opencode": ParseOpencodeNewest,
	} {
		ss, err := parse(wrong)
		if err == nil && len(ss) > 0 {
			t.Fatalf("%s: invented %d sessions from a foreign schema", name, len(ss))
		}
		if err == nil {
			t.Logf("%s: reports no sessions rather than an error on a foreign schema", name)
		}
	}

	// A file that is not a database at all.
	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte("this is not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"goose":  func(p string) ([]model.Session, error) { return ParseGooseDBSince(p, time.Time{}) },
		"hermes": func(p string) ([]model.Session, error) { return ParseHermesDBSince(p, time.Time{}) },
		"grok":   func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) },
	} {
		if ss, err := parse(garbage); err == nil && len(ss) > 0 {
			t.Fatalf("%s: parsed %d sessions out of a non-database", name, len(ss))
		}
	}

	// An empty file is how a store looks before its harness has run once:
	// nothing to read, and nothing to complain about either.
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"goose":    func(p string) ([]model.Session, error) { return ParseGooseDBSince(p, time.Time{}) },
		"hermes":   func(p string) ([]model.Session, error) { return ParseHermesDBSince(p, time.Time{}) },
		"grok":     func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) },
		"opencode": ParseOpencodeNewest,
	} {
		ss, err := parse(empty)
		if err != nil {
			t.Fatalf("%s: an empty store errored: %v", name, err)
		}
		if len(ss) != 0 {
			t.Fatalf("%s: %d sessions from an empty store", name, len(ss))
		}
	}

	// And a store that is simply not there must not be created by reading it.
	absent := filepath.Join(dir, "absent.db")
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"goose":    func(p string) ([]model.Session, error) { return ParseGooseDBSince(p, time.Time{}) },
		"grok":     func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) },
		"opencode": ParseOpencodeNewest,
	} {
		if _, err := parse(absent); err != nil {
			t.Fatalf("%s: absent store errored: %v", name, err)
		}
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("reading an absent store created it — sqlite3 does that unless stopped")
	}
}

// The escape helper is the one that cost two bugs in a day, both times by
// being read as "quote". It escapes; the caller quotes.
func TestSQLEscapeDoublesQuotesWithoutAddingThem(t *testing.T) {
	for in, want := range map[string]string{
		"plain":            "plain",
		"o'brien":          "o''brien",
		"''":               "''''",
		"2026-01-01T00:00": "2026-01-01T00:00",
		"":                 "",
	} {
		if got := sqlEscape(in); got != want {
			t.Fatalf("sqlEscape(%q) = %q, want %q", in, got, want)
		}
	}
	// The point of the name: no quotes are added, so a caller that forgets
	// them builds a broken query rather than a working one.
	if got := sqlEscape("value"); got == "'value'" {
		t.Fatal("sqlEscape now quotes; the call sites double-quote and every query breaks")
	}
}

func seedDB(t *testing.T, name, schema string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), name)
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	return db
}

// The incremental path asks each SQLite-backed harness for what changed since
// a timestamp. Getting that filter wrong either reindexes everything on every
// pass or silently misses new messages.
func TestGooseSinceFilterBoundsWhatItReturns(t *testing.T) {
	db := seedDB(t, "sessions.db", `
CREATE TABLE sessions (id TEXT PRIMARY KEY, name TEXT, description TEXT, working_dir TEXT, created_at TEXT, updated_at TEXT, session_type TEXT);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, role TEXT, content_json TEXT, created_timestamp INTEGER);
INSERT INTO sessions VALUES ('s1','n','desc','/w','2026-01-01T00:00:00Z','2026-01-01T00:00:10Z','user');
INSERT INTO messages VALUES (1,'s1','user','[{"type":"text","text":"OLDMESSAGE"}]',1767225600);
INSERT INTO messages VALUES (2,'s1','user','[{"type":"text","text":"NEWMESSAGE"}]',1798761600);
`)
	all, err := ParseGooseDBSince(db, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || len(all[0].Messages) != 2 {
		t.Fatalf("zero time should return everything, got %+v", all)
	}
	cut := time.Unix(1790000000, 0)
	since, err := ParseGooseDBSince(db, cut)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range since {
		for _, m := range s.Messages {
			if m.Text == "OLDMESSAGE" {
				t.Fatalf("since filter returned a message from before the cut: %+v", s.Messages)
			}
		}
	}
}

// doctor asks one question of the opencode store, and answering it by reading
// a multi-gigabyte database cost 6.5 seconds of an 8 second report.
func TestOpencodeNewestReadsOneSession(t *testing.T) {
	db := seedDB(t, "opencode.db", `
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, time_created INTEGER, time_updated INTEGER);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT, time_created INTEGER);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT);
INSERT INTO session VALUES ('old','/w',1000,1000);
INSERT INTO session VALUES ('new','/w',2000,2000);
INSERT INTO message VALUES ('m1','old','{"role":"user","time":{"created":1000}}',1000);
INSERT INTO message VALUES ('m2','new','{"role":"user","time":{"created":2000}}',2000);
INSERT INTO part VALUES ('p1','m1','{"type":"text","text":"OLDSESSION"}');
INSERT INTO part VALUES ('p2','m2','{"type":"text","text":"NEWSESSION"}');
`)
	ss, err := ParseOpencodeNewest(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "new" {
		t.Fatalf("got %+v, want only the newest session", ss)
	}
	if len(ss[0].Messages) == 0 {
		t.Fatal("newest session came back empty; doctor would call the store broken")
	}
	// A store that is not there answers with nothing rather than an error,
	// and must not be created by the attempt.
	if ss, err := ParseOpencodeNewest(filepath.Join(t.TempDir(), "absent.db")); err != nil || ss != nil {
		t.Fatalf("absent store: %v %v", ss, err)
	}
}

// KindForPath decides which parser owns a file, and two harnesses writing the
// same filename is how roo history ended up filed under cline.
func TestKindForPathAttributesFilesToTheirHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", "")
	t.Setenv("DEJA_CLINE_ROOTS", "")
	cli := filepath.Join(home, ".vscode-mock", "global-storage")
	t.Setenv("DEJA_ROO_CLI_ROOT", cli)
	// Roots are only considered when they exist: a path under a store the
	// user does not have belongs to nobody.
	if err := os.MkdirAll(filepath.Join(cli, "tasks", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "claude"))

	for path, want := range map[string]string{
		filepath.Join(cli, "tasks", "1", "api_conversation_history.json"): "roo",
		filepath.Join(home, "claude", "project", "s.jsonl"):               "claude",
		filepath.Join(home, "nothing", "random.txt"):                      "",
	} {
		if got := KindForPath(path); got != want {
			t.Fatalf("KindForPath(%s) = %q, want %q", path, got, want)
		}
	}
}

// Timestamps arrive in whatever shape a harness happens to write. An
// unparseable one must degrade to "no time" rather than to a wrong time: a
// session dated 0001-01-01 sorts before everything and never surfaces.
func TestGrokDBTimeAcceptsTheShapesItSees(t *testing.T) {
	for in, wantZero := range map[string]bool{
		"2026-07-27T15:06:24.715Z": false,
		"2026-07-27T15:06:24Z":     false,
		"2026-07-27 15:06:24":      false,
		"":                         true,
		"not a time":               true,
	} {
		got := grokDBTime(in)
		if got.IsZero() != wantZero {
			t.Fatalf("grokDBTime(%q) = %v, zero=%v want zero=%v", in, got, got.IsZero(), wantZero)
		}
	}
}

// Message bodies differ per role: a user turn carries a plain string, an
// assistant turn an array of typed blocks. Reading only one shape loses half
// the conversation.
func TestGrokMessageTextHandlesBothShapes(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"plain string": {`{"role":"user","content":"hello there"}`, "hello there"},
		"text blocks":  {`{"role":"assistant","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}`, "first\nsecond"},
		"tool only":    {`{"role":"assistant","content":[{"type":"tool_use","id":"t1"}]}`, ""},
		"garbage":      {`not json`, ""},
	} {
		if got := grokMessageText(tc.body); got != tc.want {
			t.Fatalf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// Malformed and half-typed records of every shape the new decoders touch.
func TestCodexAndCursorSurviveJunk(t *testing.T) {
	dir := t.TempDir()
	codex := []string{
		`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"j","cwd":"/w"}}`,
		`{"timestamp":"x","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"not json"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":null}"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":123}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","output":null}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","output":"Output:\n"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":""}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n-orphan removal with no file header\n*** End Patch"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Update File: \n-x"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":123}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"keep one"}]}}`,
	}
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-junk.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(codex, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCodexRollout(p); err != nil {
		t.Fatal(err)
	}

	cdir := filepath.Join(dir, "projects", "Users-me-app", "agent-transcripts", "junk")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	cursor := []string{
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":null}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":123}}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"StrReplace","input":{"path":"/a","old_string":null}}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":null}}]}}`,
		`{"role":"assistant","message":{"content":"a plain string, not a list"}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"StrReplace","input":{"path":"/a","edits":"not a list"}}]}}`,
		`{"role":"user","message":{"content":[{"type":"text","text":"keep one"}]}}`,
	}
	cp := filepath.Join(cdir, "junk.jsonl")
	if err := os.WriteFile(cp, []byte(strings.Join(cursor, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCursorTranscript(cp); err != nil {
		t.Fatal(err)
	}
}

// dsh moved its session log to session.v3.jsonl(.zstd), and a session begun
// before that can keep a header-only file under the old name beside the new
// log. Discovery and the kind the incremental index matches on must both accept
// every name: a store of v3 logs otherwise reads as empty, or indexes on a
// rebuild and then never again.
func TestDeepSeekAcceptsEveryLogName(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	root := filepath.Join(tmp, "dsh", "sessions")
	t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)

	var kind FileKind
	for _, h := range Registry() {
		if h.Name == "deepseek" {
			kind = h.Kinds[0]
		}
	}
	if kind.Match == nil {
		t.Fatal("no deepseek kind in the registry")
	}

	workspace := filepath.Join(root, "--work-pgbouncer-lab--")
	cases := []struct {
		dir, name string
		log       bool
	}{
		{"session-a", "session.jsonl", true},
		{"session-b", "session.jsonl.zstd", true},
		{"session-c", "session.v3.jsonl", true},
		{"session-d", "session.v3.jsonl.zstd", true},
		{"session-d", "session.lock", false},
		{"session-e", "session.v3.jsonl.bak", false},
	}
	for _, c := range cases {
		dir := filepath.Join(workspace, c.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, c.name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	found := map[string]bool{}
	for _, f := range DeepSeekSessionFiles() {
		found[filepath.Join(filepath.Base(filepath.Dir(f)), filepath.Base(f))] = true
	}
	for _, c := range cases {
		path := filepath.Join(workspace, c.dir, c.name)
		if got := found[filepath.Join(c.dir, c.name)]; got != c.log {
			t.Errorf("DeepSeekSessionFiles found %s = %v, want %v", c.name, got, c.log)
		}
		if got := kind.Match(path); got != c.log {
			t.Errorf("deepseek kind matches %s = %v, want %v", c.name, got, c.log)
		}
	}
}

func TestParseDeepSeekFileReadsAV3Log(t *testing.T) {
	root := t.TempDir()
	raw := writeDeepSeekSession(t, root, "v3", deepSeekLog, false)
	v3 := filepath.Join(filepath.Dir(raw), "session.v3.jsonl")
	if err := os.Rename(raw, v3); err != nil {
		t.Fatal(err)
	}
	paths := []string{v3}
	if ZstdAvailable() {
		framed := v3 + ".zstd"
		if err := exec.CommandContext(t.Context(), "zstd", "-q", "-k", "-o", framed, v3).Run(); err != nil {
			t.Fatalf("zstd: %v", err)
		}
		paths = append(paths, framed)
	}
	for _, path := range paths {
		ss, err := ParseDeepSeekFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || len(ss[0].Messages) != 5 {
			t.Fatalf("%s read as %+v", filepath.Base(path), ss)
		}
		if ss[0].ID != "eaf5c9ac-0e47-4d2f-b982-8bae306062d1" || ss[0].Project != "work/pgbouncer-lab" {
			t.Errorf("%s: id %q, project %q", filepath.Base(path), ss[0].ID, ss[0].Project)
		}
	}
}

// Every line here is a shape dsh 0.1.1-rc.2 wrote while a local model answered
// through it: the question, the two kinds of text a plugin splices into the
// same event, a tool call and its result, and the complete assistant message
// that supersedes the deltas streamed before it.
const deepSeekLog = `{"type":"session","version":0,"id":"session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab","delegationDepth":0}
{"type":"permission/preset","seq":0,"time":1787320263520,"data":{"preset":"workspace-write"}}
{"type":"session/title","seq":10,"time":1787320263581,"data":{"title":"pgbouncer pool size","source":{"kind":"fallback"}}}
{"type":"user/message","seq":7,"time":1787320263580,"data":{"content":[{"type":"text","text":"сколько коннектов держим на шард?"}],"source":{"kind":"user"},"role":"user"}}
{"type":"user/message","seq":8,"time":1787320263580,"data":{"content":[{"type":"text","text":"Current runtime context. This snapshot supersedes earlier ones."}],"source":{"kind":"plugin","plugin":"@deepseek-ai/dsh-system-prompt"},"role":"user"}}
{"type":"user/message","seq":9,"time":1787320263580,"data":{"content":[{"type":"text","text":"<system-reminder>A skill is a reusable set of instructions.</system-reminder>"}],"source":{"kind":"skill-catalog"},"role":"user"}}
{"type":"assistant/chunk","seq":12,"time":1787320263585,"data":{"turn":1,"step":1,"chunk":{"type":"text-delta","text":"сей"}}}
{"type":"assistant/chunk","seq":13,"time":1787320263586,"data":{"turn":1,"step":1,"chunk":{"type":"text-delta","text":"час посмотрю"}}}
{"type":"assistant/message","seq":20,"time":1787320263700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"reasoning","text":"надо открыть конфиг"},{"type":"text","text":"сейчас посмотрю конфиг"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"tool/call","seq":21,"time":1787320263710,"data":{"turn":1,"step":1,"callId":"c1","name":"read","arguments":"{\"file_path\":\"/work/pgbouncer-lab/notes.txt\"}"}}
{"type":"tool/result","seq":22,"time":1787320263720,"data":{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"c1"},"content":[{"type":"tool-result","toolCallId":"c1","content":[{"type":"text","text":"pgbouncer pool_size = 40"}]}]}}}
{"type":"assistant/message","seq":40,"time":1787320263900,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"text","text":"держим 40 на шард"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"step/end","seq":41,"time":1787320263901,"data":{"turn":1,"step":2}}
{"type":"turn/end","seq":42,"time":1787320263902,"data":{"turn":1}}
`

// A run killed mid-answer never writes the complete message, and a run of three
// or more consecutive deltas is stored as one packed row rather than as the
// events themselves. Both together are what an interrupted session looks like.
const deepSeekInterrupted = `{"type":"session","version":0,"id":"session-81f6aa2b-d16e-47a8-9841-581fb5f0007b","createdAt":1787325263519,"cwd":"/work/pgbouncer-lab"}
{"type":"user/message","seq":7,"time":1787325263580,"data":{"content":[{"type":"text","text":"расскажи про пул соединений"}],"source":{"kind":"user"},"role":"user"}}
{"type":"reasoning-chunks","seq0":15,"time0":1787325263600,"data":{"turn":1,"step":1,"index":0,"dt":[76,76],"texts":["надо","подумать","как"]}}
{"type":"text-chunks","seq0":18,"time0":1787325263700,"data":{"turn":1,"step":1,"index":0,"dt":[79,75],"texts":["пул ","держит ","соединения"]}}
{"type":"assistant/chunk","seq":25,"time":1787325263800,"data":{"turn":1,"step":1,"chunk":{"type":"text-delta","text":" открытыми"}}}
`

func writeDeepSeekSession(t *testing.T, root, id, body string, compress bool) string {
	t.Helper()
	dir := filepath.Join(root, "--work-pgbouncer-lab--", "session-"+id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !compress {
		return path
	}
	out := path + ".zstd"
	if err := exec.CommandContext(t.Context(), "zstd", "-q", "-o", out, path).Run(); err != nil {
		t.Fatalf("zstd: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseDeepSeekFile(t *testing.T) {
	root := t.TempDir()
	// The directory is named after an older id than the header carries: a
	// resumed session keeps its directory and writes a new header.
	path := writeDeepSeekSession(t, root, "stale-directory-name", deepSeekLog, false)

	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "deepseek" {
		t.Errorf("harness = %q", s.Harness)
	}
	if s.ID != "eaf5c9ac-0e47-4d2f-b982-8bae306062d1" {
		t.Errorf("id = %q; the header's id wins over the directory name", s.ID)
	}
	if s.Project != "work/pgbouncer-lab" {
		t.Errorf("project = %q; it comes from the header's cwd", s.Project)
	}
	if s.Title != "pgbouncer pool size" {
		t.Errorf("title = %q", s.Title)
	}

	roles := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
	}
	want := []string{"user", "assistant", "files", "tool-output", "assistant"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v:\n%+v", roles, want, s.Messages)
	}
	// The complete message replaces what was streamed of it; without that the
	// same answer lands twice, once in pieces.
	if s.Messages[1].Text != "сейчас посмотрю конфиг" {
		t.Errorf("answer = %q; the complete message supersedes the deltas", s.Messages[1].Text)
	}
	if strings.Contains(s.Messages[1].Text, "надо открыть конфиг") {
		t.Error("the model's reasoning was recalled as something it said")
	}
	if s.Messages[2].Text != "/work/pgbouncer-lab/notes.txt" {
		t.Errorf("files = %q; the read call names the file", s.Messages[2].Text)
	}
	if s.Messages[3].Text != "pgbouncer pool_size = 40" {
		t.Errorf("tool output = %q", s.Messages[3].Text)
	}
	if s.Messages[4].Text != "держим 40 на шард" {
		t.Errorf("final answer = %q", s.Messages[4].Text)
	}
	for i, m := range s.Messages {
		if m.Time.IsZero() {
			t.Errorf("message %d has no time: %+v", i, m)
		}
	}
}

// What a plugin splices into the session is the harness describing itself: the
// sandbox policy, the skill catalogue. It arrives as user/message like a
// person's turn and must not be recalled as one.
func TestParseDeepSeekFileSkipsWhatPluginsSpliced(t *testing.T) {
	root := t.TempDir()
	path := writeDeepSeekSession(t, root, "plugins", deepSeekLog, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "system-reminder") || strings.Contains(m.Text, "runtime context") {
			t.Errorf("a plugin's own text was recalled as a turn: %q", m.Text)
		}
	}
}

func TestParseDeepSeekFileKeepsAnInterruptedAnswer(t *testing.T) {
	root := t.TempDir()
	path := writeDeepSeekSession(t, root, "interrupted", deepSeekInterrupted, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the question and what was streamed:\n%+v", len(s.Messages), s.Messages)
	}
	// Three of the four pieces were stored as one packed row; a reader that
	// knows only the delta events keeps the last word and loses the answer.
	if s.Messages[1].Text != "пул держит соединения открытыми" {
		t.Errorf("streamed answer = %q", s.Messages[1].Text)
	}
	if strings.Contains(s.Messages[1].Text, "надо подумать") {
		t.Error("a packed run of reasoning deltas was read as speech")
	}
}

func TestParseDeepSeekFileReadsZstdFrames(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	path := writeDeepSeekSession(t, root, "compressed", deepSeekLog, true)
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 5 {
		t.Fatalf("compressed session read as %+v", ss)
	}
	if ss[0].Messages[4].Text != "держим 40 на шард" {
		t.Errorf("answer = %q", ss[0].Messages[4].Text)
	}
}

func TestDeepSeekSessionFilesFindsBothEncodings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)
	writeDeepSeekSession(t, root, "raw", deepSeekLog, false)
	files := DeepSeekSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if got := LoadDeepSeek(); len(got) != 1 {
		t.Fatalf("LoadDeepSeek = %d sessions", len(got))
	}
}

// dsh takes an empty DSH_HOME as unset and expands a leading ~, so deja has to
// read the same two values the same way or it looks for sessions and profiles
// in a directory dsh never writes (#4390).
func TestDSHHomeReadsDSHHomeTheWayDshDoes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, c := range []struct{ value, want string }{
		{"", filepath.Join(home, ".dsh")},
		{"~", home},
		{"~/.dsh-alt", filepath.Join(home, ".dsh-alt")},
		{filepath.Join(home, "elsewhere"), filepath.Join(home, "elsewhere")},
	} {
		t.Setenv("DSH_HOME", c.value)
		if got := DSHHome(); got != c.want {
			t.Errorf("DSH_HOME=%q: DSHHome = %q, want %q", c.value, got, c.want)
		}
	}
}

// A subagent or subagent_fork run is a log of its own whose header names the
// parent the way dsh 0.1.1-rc.2 writes it, with the "session-" prefix.
func TestDeepSeekSubagentNamesItsParent(t *testing.T) {
	root := t.TempDir()
	child := strings.Replace(deepSeekLog,
		`"id":"session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab","delegationDepth":0}`,
		`"id":"286f51ca-3e5c-437d-8e6b-c9d5cfb5e4d3","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab","parentSession":"session-c559c777-a703-4cfa-9f2e-f4b9ebf675cc","origin":"subagent","delegationDepth":1}`, 1)
	if child == deepSeekLog {
		t.Fatal("the fixture header moved; this test no longer writes a sub-agent")
	}
	for _, c := range []struct {
		name, body, kind, parent string
	}{
		{"child", child, "subagent", "c559c777-a703-4cfa-9f2e-f4b9ebf675cc"},
		{"main", deepSeekLog, "", ""},
	} {
		ss, err := ParseDeepSeekFile(writeDeepSeekSession(t, root, c.name, c.body, false))
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %v %#v", c.name, err, ss)
		}
		if ss[0].Kind != c.kind || ss[0].Parent != c.parent {
			t.Errorf("%s: kind/parent = %q/%q, want %q/%q", c.name, ss[0].Kind, ss[0].Parent, c.kind, c.parent)
		}
	}
}

// dsh names a session twice: a stand-in it cuts out of the opening message, and
// the real one when its title model answers. The log is append-only, so the
// last event is the name the harness is showing — deja kept the first, and
// listed every dsh session under a sentence cut mid-phrase (#2551).
func TestDeepSeekTakesTheTitleTheHarnessSettledOn(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-t1","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab"}
{"type":"session/title","seq":10,"time":1787320263581,"data":{"title":"how many connections do","source":{"kind":"fallback"}}}
{"type":"user/message","seq":7,"time":1787320263580,"data":{"content":[{"type":"text","text":"how many connections do we hold per shard?"}],"source":{"kind":"user"},"role":"user"}}
{"type":"assistant/message","seq":20,"time":1787320263700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"forty, and the pool is the reason"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"session/title","seq":21,"time":1787320263800,"data":{"title":"connections per shard","source":{"kind":"provider","provider":"session-title-first-prompt-llm"}}}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "session-t1", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("parsed %d sessions, want 1", len(ss))
	}
	if ss[0].Title != "connections per shard" {
		t.Errorf("title = %q, want the one the harness settled on", ss[0].Title)
	}
}

// And a session the title model never answered for keeps the stand-in rather
// than losing its name: it is still what dsh shows.
func TestDeepSeekKeepsTheOnlyTitleItWasGiven(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-t2","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab"}
{"type":"session/title","seq":10,"time":1787320263581,"data":{"title":"how many connections do","source":{"kind":"fallback"}}}
{"type":"user/message","seq":7,"time":1787320263580,"data":{"content":[{"type":"text","text":"how many connections do we hold per shard?"}],"source":{"kind":"user"},"role":"user"}}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "session-t2", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Title != "how many connections do" {
		t.Fatalf("title = %q", ss[0].Title)
	}
}

// dsh writes every tool call twice: as a tool-call block inside the
// assistant/message and as a tool/call event. Shapes from dsh 0.1.1-rc.2: bash,
// edit and write from the fs tools, and str_replace_editor, which names its
// file `path` and its spans old_str/new_str/file_text (#4291).
const deepSeekToolLog = `{"type":"session","version":0,"id":"session-11111111-2222-3333-4444-555555555555","createdAt":1790874400000,"cwd":"/tmp/proj","delegationDepth":0}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"fix the retry loop"}],"source":{"kind":"user"},"role":"user","id":"u1"}}
{"type":"assistant/message","seq":18,"time":1790874400200,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"tool-call","id":"call_1","name":"bash","arguments":"{\"command\": \"go test ./retry/...\", \"description\": \"run the tests\"}"}],"source":{"kind":"model"},"id":"a1"}}}
{"type":"tool/call","seq":19,"time":1790874400200,"data":{"turn":1,"step":1,"callId":"call_1","name":"bash","arguments":"{\"command\": \"go test ./retry/...\", \"description\": \"run the tests\"}"}}
{"type":"tool/result","seq":20,"time":1790874400300,"data":{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call_1"},"content":[{"type":"tool-result","toolCallId":"call_1","content":[{"type":"text","text":"ok  \texample.com/proj/retry\t0.01s"}],"isError":false}],"role":"user","id":"t1"}}}
{"type":"assistant/message","seq":21,"time":1790874400400,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"tool-call","id":"call_2","name":"edit","arguments":"{\"file_path\": \"/tmp/proj/retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}],"source":{"kind":"model"},"id":"a2"}}}
{"type":"tool/call","seq":22,"time":1790874400400,"data":{"turn":1,"step":2,"callId":"call_2","name":"edit","arguments":"{\"file_path\": \"/tmp/proj/retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}}
{"type":"tool/result","seq":23,"time":1790874400500,"data":{"turn":1,"step":2,"message":{"source":{"kind":"tool","callId":"call_2"},"content":[{"type":"tool-result","toolCallId":"call_2","content":[{"type":"text","text":"The file /tmp/proj/retry.go has been updated successfully."}],"isError":false}],"role":"user","id":"t2"}}}
{"type":"assistant/message","seq":24,"time":1790874400600,"data":{"turn":1,"step":3,"message":{"role":"assistant","content":[{"type":"tool-call","id":"call_3","name":"write","arguments":"{\"file_path\": \"/tmp/proj/PLAN.md\", \"content\": \"Back off twice as long on every retry, five tries at most.\\n\"}"},{"type":"tool-call","id":"call_4","name":"str_replace_editor","arguments":"{\"command\": \"str_replace\", \"path\": \"/tmp/proj/backoff.go\", \"old_str\": \"delay *= 2\", \"new_str\": \"delay = min(delay*2, maxDelay) // capped backoff\"}"}],"source":{"kind":"model"},"id":"a3"}}}
{"type":"tool/call","seq":25,"time":1790874400600,"data":{"turn":1,"step":3,"callId":"call_3","name":"write","arguments":"{\"file_path\": \"/tmp/proj/PLAN.md\", \"content\": \"Back off twice as long on every retry, five tries at most.\\n\"}"}}
{"type":"tool/call","seq":26,"time":1790874400600,"data":{"turn":1,"step":3,"callId":"call_4","name":"str_replace_editor","arguments":"{\"command\": \"str_replace\", \"path\": \"/tmp/proj/backoff.go\", \"old_str\": \"delay *= 2\", \"new_str\": \"delay = min(delay*2, maxDelay) // capped backoff\"}"}}
{"type":"tool/result","seq":27,"time":1790874400650,"data":{"turn":1,"step":3,"message":{"source":{"kind":"tool","callId":"call_3"},"content":[{"type":"tool-result","toolCallId":"call_3","content":[{"type":"text","text":"<path>/tmp/proj/PLAN.md</path>\n<type>file</type>\n<content>\nCreated file\n</content>"}],"isError":false}],"role":"user","id":"t3"}}}
{"type":"tool/result","seq":28,"time":1790874400660,"data":{"turn":1,"step":3,"message":{"source":{"kind":"tool","callId":"call_4"},"content":[{"type":"tool-result","toolCallId":"call_4","content":[{"type":"text","text":"The file /tmp/proj/backoff.go has been edited."}],"isError":false}],"role":"user","id":"t4"}}}
{"type":"assistant/message","seq":27,"time":1790874400700,"data":{"turn":1,"step":4,"message":{"role":"assistant","content":[{"type":"text","text":"Bounded the retry loop."}],"source":{"kind":"model"},"id":"a4"}}}
`

func TestParseDeepSeekFileKeepsToolCalls(t *testing.T) {
	path := writeDeepSeekSession(t, t.TempDir(), "tools", deepSeekToolLog, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
		if m.Time.IsZero() {
			t.Errorf("%s record has no time: %q", m.Role, m.Text)
		}
	}
	// Each call is written twice; it must be counted once.
	if got := byRole[RoleCommand]; strings.Join(got, "|") != "$ go test ./retry/...  → exit 0" {
		t.Errorf("commands = %q, want the one bash call", got)
	}
	files := strings.Join(byRole[RoleFiles], "\n")
	for _, p := range []string{"/tmp/proj/retry.go", "/tmp/proj/PLAN.md", "/tmp/proj/backoff.go"} {
		if strings.Count(files, p) != 1 {
			t.Errorf("files = %q, want %s once", files, p)
		}
	}
	edits := byRole[RoleEdit]
	want := []string{"/tmp/proj/retry.go\nfor {", "/tmp/proj/backoff.go\ndelay *= 2"}
	if strings.Join(edits, "|") != strings.Join(want, "|") {
		t.Errorf("edits = %q, want %q", edits, want)
	}
	wrote := strings.Join(byRole[RoleWrote], "\n")
	for _, p := range []string{"/tmp/proj/retry.go", "/tmp/proj/PLAN.md", "/tmp/proj/backoff.go"} {
		if strings.Count(wrote, p+"\n") != 1 {
			t.Errorf("wrote = %q, want one record for %s", wrote, p)
		}
	}
	if len(byRole["assistant"]) != 1 || len(byRole["tool-output"]) != 4 {
		t.Errorf("prose changed: %+v", byRole)
	}
}

// A dsh tool takes a path relative to the session's directory as readily as an
// absolute one. Stored as "retry.go", the edit was out of reach of
// `deja restore /tmp/proj/retry.go` and of blame.
func TestParseDeepSeekFileResolvesARelativePathAgainstTheSessionCwd(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-rel","createdAt":1790874400000,"cwd":"/tmp/proj"}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"bound the retry loop"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":22,"time":1790874400400,"data":{"callId":"call_2","name":"edit","arguments":"{\"file_path\": \"retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}}
{"type":"tool/result","seq":23,"time":1790874400500,"data":{"message":{"source":{"kind":"tool","callId":"call_2"},"content":[{"type":"tool-result","toolCallId":"call_2","content":[{"type":"text","text":"The file retry.go has been updated successfully."}],"isError":false}]}}}
`
	path := writeDeepSeekSession(t, t.TempDir(), "rel", log, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := strings.Join(byRole[RoleFiles], "|"); got != "/tmp/proj/retry.go" {
		t.Errorf("files = %q, want the path resolved against the session cwd", got)
	}
	if got := strings.Join(byRole[RoleEdit], "|"); got != "/tmp/proj/retry.go\nfor {" {
		t.Errorf("edits = %q", got)
	}
}

// What dsh's result says decides what the call is credited with: an edit that
// failed or was denied changed nothing, a call with no result may not have run,
// and a bash result carries its exit status as a trailing marker rather than as
// an error, so the status comes from the marker (#4291).
func TestParseDeepSeekFileCreditsWhatTheResultConfirms(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-res","createdAt":1790874400000,"cwd":"/tmp/proj"}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"fix the retry loop"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":10,"time":1790874400200,"data":{"callId":"ok","name":"bash","arguments":"{\"command\": \"go test ./retry/...\"}"}}
{"type":"tool/result","seq":11,"time":1790874400210,"data":{"message":{"source":{"kind":"tool","callId":"ok"},"content":[{"type":"tool-result","toolCallId":"ok","content":[{"type":"text","text":"ok  \texample.com/proj/retry\t0.01s"}],"isError":false}]}}}
{"type":"tool/call","seq":12,"time":1790874400220,"data":{"callId":"bad","name":"bash","arguments":"{\"command\": \"go vet ./retry/...\"}"}}
{"type":"tool/result","seq":13,"time":1790874400230,"data":{"message":{"source":{"kind":"tool","callId":"bad"},"content":[{"type":"tool-result","toolCallId":"bad","content":[{"type":"text","text":"retry.go:3: unreachable code\n[exit code: 1]"}],"isError":false}]}}}
{"type":"tool/call","seq":14,"time":1790874400240,"data":{"callId":"denied","name":"edit","arguments":"{\"file_path\": \"/tmp/proj/denied.go\", \"old_string\": \"return nil\", \"new_string\": \"return fmt.Errorf(\\\"retry budget exhausted after %d attempts\\\", n)\"}"}}
{"type":"tool/result","seq":15,"time":1790874400250,"data":{"message":{"source":{"kind":"tool","callId":"denied"},"content":[{"type":"tool-result","toolCallId":"denied","content":[{"type":"text","text":"Error: rejected"}],"isError":true}]}}}
{"type":"tool/call","seq":16,"time":1790874400260,"data":{"callId":"lost","name":"write","arguments":"{\"file_path\": \"/tmp/proj/lost.md\", \"content\": \"Back off twice as long on every retry, five tries at most.\\n\"}"}}
{"type":"tool/call","seq":18,"time":1790874400280,"data":{"callId":"good","name":"edit","arguments":"{\"file_path\": \"/tmp/proj/retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}}
{"type":"tool/result","seq":19,"time":1790874400290,"data":{"message":{"source":{"kind":"tool","callId":"good"},"content":[{"type":"tool-result","toolCallId":"good","content":[{"type":"text","text":"The file /tmp/proj/retry.go has been updated successfully."}],"isError":false}]}}}
`
	path := writeDeepSeekSession(t, t.TempDir(), "res", log, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	wantCmds := "$ go test ./retry/...  → exit 0|$ go vet ./retry/...  → exit 1"
	if got := strings.Join(byRole[RoleCommand], "|"); got != wantCmds {
		t.Errorf("commands = %q, want %q", got, wantCmds)
	}
	edits := strings.Join(byRole[RoleEdit], "|")
	wrote := strings.Join(byRole[RoleWrote], "|")
	if edits != "/tmp/proj/retry.go\nfor {" {
		t.Errorf("edits = %q, want only the edit whose result came back clean", edits)
	}
	if strings.Contains(wrote, "denied.go") || strings.Contains(wrote, "lost.md") || !strings.Contains(wrote, "/tmp/proj/retry.go\n") {
		t.Errorf("wrote = %q, want only the confirmed edit", wrote)
	}
}

// str_replace_editor's view takes a directory as well as a file, and the call
// cannot tell which; the result can. A listing is not a file the session read.
func TestParseDeepSeekFileKeepsADirectoryViewOutOfFiles(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-view","createdAt":1790874400000,"cwd":"/tmp/proj"}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"look around"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":10,"time":1790874400200,"data":{"callId":"dir","name":"str_replace_editor","arguments":"{\"command\": \"view\", \"path\": \"/tmp/proj\"}"}}
{"type":"tool/result","seq":11,"time":1790874400210,"data":{"message":{"source":{"kind":"tool","callId":"dir"},"content":[{"type":"tool-result","toolCallId":"dir","content":[{"type":"text","text":"Here're the files and directories up to 2 levels deep in /tmp/proj, excluding hidden items:\nd\t/tmp/proj\nf\t/tmp/proj/retry.go\n"}],"isError":false}]}}}
{"type":"tool/call","seq":12,"time":1790874400220,"data":{"callId":"file","name":"str_replace_editor","arguments":"{\"command\": \"view\", \"path\": \"/tmp/proj/retry.go\"}"}}
{"type":"tool/result","seq":13,"time":1790874400230,"data":{"message":{"source":{"kind":"tool","callId":"file"},"content":[{"type":"tool-result","toolCallId":"file","content":[{"type":"text","text":"Here's the content of /tmp/proj/retry.go with line numbers (which has a total of 1 lines):\n     1  package retry\n"}],"isError":false}]}}}
{"type":"tool/call","seq":14,"time":1790874400240,"data":{"callId":"img","name":"read_image","arguments":"{\"file_path\": \"/tmp/proj/backoff.png\"}"}}
{"type":"tool/result","seq":15,"time":1790874400250,"data":{"message":{"source":{"kind":"tool","callId":"img"},"content":[{"type":"tool-result","toolCallId":"img","content":[{"type":"text","text":"image"}],"isError":false}]}}}
`
	path := writeDeepSeekSession(t, t.TempDir(), "view", log, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var files []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			files = append(files, m.Text)
		}
	}
	if got := strings.Join(files, "|"); got != "/tmp/proj/retry.go|/tmp/proj/backoff.png" {
		t.Errorf("files = %q, want the viewed file and the image, not the directory", got)
	}
}

// The exit status is read off the end of dsh's own result text. A long result
// goes through dsh's spill policy (on at 50 KB by default), which appends a
// "(Omitted N bytes. …)" notice after the marker, and a clean run's output can
// end in a bracketed line of its own; the persistent-shell bash ends a timeout
// or a dead shell with a reset notice instead of a marker (#4291).
func TestDeepSeekExitCodeReadsDshsMarkers(t *testing.T) {
	spill := "\n\n(Omitted 81234 bytes. Full formatted result stored at: /home/u/.dsh/spill/session-1/call_9-bash.txt. Use read with offset/limit, or grep this path to search within it.)"
	reset := "The persistent bash shell was reset; the next bash call starts from the workspace with a fresh current directory and environment."
	cases := []struct {
		name, text, code string
		ok               bool
	}{
		{"clean", "ok  \tretry\t0.01s", "0", true},
		{"nonzero", "FAIL retry\n[exit code: 1]", "1", true},
		{"marker mid-output", "[exit code: 3]\nthen it went on\n", "0", true},
		{"spilled failure", "--- FAIL: TestRetry\n[exit code: 1]" + spill, "1", true},
		{"spilled clean", "PASS\nok retry" + spill, "0", true},
		{"spill notice alone", strings.TrimLeft(spill, "\n"), "", false},
		{"truncated clean", "PASS\n[output truncated; full output: /tmp/x]", "0", true},
		{"json array", "[]", "0", true},
		{"ninja", "[2/2] Linking CXX executable app", "0", true},
		{"timed out", "partial\n[timed out after 120000ms]", "", false},
		{"killed", "partial\n[killed by signal: SIGKILL]", "", false},
		{"sandbox", "touch: /etc/x: Operation not permitted\n[sandbox: file access denied under workspace-write mode]", "", false},
		{"persistent timeout", "Your command timed out after 300 seconds or experienced an OOM error. Below is partial output:\npartial\n" + reset, "", false},
		{"persistent shell died", "partial\n[shell exited: code 1]\n" + reset, "", false},
	}
	for _, c := range cases {
		code, ok := deepSeekExitCode(c.text)
		if code != c.code || ok != c.ok {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, code, ok, c.code, c.ok)
		}
	}
}

// A path with a leading separator or a drive letter is rooted on every host.
// Go's filepath.IsAbs is false for /tmp/proj/retry.go on Windows and for
// C:\proj\retry.go everywhere else, so the reader joined such a path onto the
// session's cwd and stored one that does not exist (#4438).
func TestResolveToolPathKeepsARootedPath(t *testing.T) {
	for _, c := range []struct{ p, cwd, want string }{
		{"/tmp/proj/retry.go", "/tmp/proj", "/tmp/proj/retry.go"},
		{`\tmp\proj\retry.go`, "/tmp/proj", `\tmp\proj\retry.go`},
		{`C:\proj\retry.go`, `C:\proj`, `C:\proj\retry.go`},
		{"C:/proj/retry.go", `C:\proj`, "C:/proj/retry.go"},
		{`\\srv\share\retry.go`, `C:\proj`, `\\srv\share\retry.go`},
		{"retry.go", "/tmp/proj", "/tmp/proj/retry.go"},
		{"./sub/retry.go", "/tmp/proj", "/tmp/proj/sub/retry.go"},
		{"retry.go", "", "retry.go"},
		{"", "/tmp/proj", ""},
	} {
		if got := resolveToolPath(c.p, c.cwd); got != c.want {
			t.Errorf("resolveToolPath(%q, %q) = %q, want %q", c.p, c.cwd, got, c.want)
		}
	}
}

// The same rule end to end: a session recorded on Windows and read anywhere
// keeps its drive-letter paths as written (#4438).
func TestParseDeepSeekFileKeepsADriveLetterPath(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-win","createdAt":1790874400000,"cwd":"C:\\proj"}
{"type":"user/message","seq":7,"time":1790874400100,"data":{"content":[{"type":"text","text":"bound the retry loop"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":22,"time":1790874400400,"data":{"callId":"call_2","name":"edit","arguments":"{\"file_path\": \"C:\\\\proj\\\\retry.go\", \"old_string\": \"for {\", \"new_string\": \"for attempt := 0; attempt < maxAttempts; attempt++ {\"}"}}
{"type":"tool/result","seq":23,"time":1790874400500,"data":{"message":{"source":{"kind":"tool","callId":"call_2"},"content":[{"type":"tool-result","toolCallId":"call_2","content":[{"type":"text","text":"The file has been updated successfully."}],"isError":false}]}}}
`
	path := writeDeepSeekSession(t, t.TempDir(), "win", log, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var files []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles {
			files = append(files, m.Text)
		}
	}
	if got := strings.Join(files, "|"); got != `C:\proj\retry.go` {
		t.Errorf("files = %q, want C:\\proj\\retry.go as written", got)
	}
}

// A Windows workspace path has no "/" to fold, so the key was the whole path
// and the session sat in a project of its own (#3217).
func TestPathToProjectKeyFoldsAWindowsPath(t *testing.T) {
	if got, want := pathToProjectKey(`C:\Users\qa\work\zorbex`), pathToProjectKey("C:/Users/qa/work/zorbex"); got != want {
		t.Fatalf("backslash form = %q, slash form = %q", got, want)
	}
	if got := claudeProjectName(pathToProjectKey(`C:\Users\qa\work\zorbex`)); got != "work/zorbex" {
		t.Fatalf("project = %q, want work/zorbex", got)
	}
}

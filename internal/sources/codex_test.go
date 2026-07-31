package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, want := range []string{filepath.Join("/w/app", "main.go"), filepath.Join("/w/app", "notes.txt")} {
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
	var ids []string
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

// Codex writes its preamble under its own role as well as as a user turn.
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
	if err := os.WriteFile(p, []byte(head+extra), 0o644); err != nil {
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

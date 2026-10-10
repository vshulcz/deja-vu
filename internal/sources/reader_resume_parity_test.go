package sources

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

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
	off := strings.IndexByte(string(full), '\n') + 1
	if err := os.WriteFile(path, full[:off], 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := ParseCommandCodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, full, 0o644); err != nil {
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
	var got []string
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
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
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
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
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

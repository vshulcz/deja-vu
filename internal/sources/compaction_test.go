package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// VS Code Copilot Chat's PreCompact names the extension's transcript for the
// chat, in whichever profile it lives; the layout says whose it is, and the
// workspace comes from workspace.json beside the storage directory.
func TestCompactionSessionReadsAVSCodeTranscript(t *testing.T) {
	ws := t.TempDir()
	storage := filepath.Join(t.TempDir(), "workspaceStorage", "abc")
	const id = "7c9e2d2c-0000-4000-8000-000000000000"
	path := filepath.Join(storage, "GitHub.copilot-chat", "transcripts", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + filepath.ToSlash(ws)
	if !strings.HasPrefix(filepath.ToSlash(ws), "/") {
		uri = "file:///" + filepath.ToSlash(ws)
	}
	if err := os.WriteFile(filepath.Join(storage, "workspace.json"), []byte(`{"folder":"`+uri+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session.start","data":{"sessionId":"` + id + `","copilotVersion":"0.68.0"},"id":"e1","timestamp":"2026-10-07T10:00:00.000Z"}
{"type":"user.message","data":{"content":"the packer build fails to generate a manifest"},"id":"e2","timestamp":"2026-10-07T10:00:01.000Z"}
{"type":"assistant.message","data":{"content":"The manifest step needs the source name; I will pass it explicitly."},"id":"e3","timestamp":"2026-10-07T10:00:02.000Z"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionSession("", id, path, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "copilot-chat" || len(got.Session.Messages) == 0 || filepath.Clean(got.Workspace) != filepath.Clean(ws) {
		t.Errorf("harness=%q messages=%d workspace=%q, want copilot-chat, some, %q", got.Harness, len(got.Session.Messages), got.Workspace, ws)
	}
	if _, err := ReadCompactionSession("", "another-chat", path, ""); !errors.Is(err, ErrTranscriptIdentity) {
		t.Errorf("a transcript was read for a session it is not: %v", err)
	}
}

// Grok and Reasonix keep an events.jsonl as well; only Copilot CLI's, under
// session-state/<id>/, is read as Copilot's.
func TestOnlyCopilotsEventLogIsCopilots(t *testing.T) {
	for path, want := range map[string]string{
		filepath.Join("h", ".copilot", "session-state", "abc", "events.jsonl"):      "copilot",
		filepath.Join("h", "ws", "GitHub.copilot-chat", "transcripts", "abc.jsonl"): "copilot-chat",
		filepath.Join("h", ".grok", "sessions", "abc", "events.jsonl"):              "",
		filepath.Join("h", "events.jsonl"):                                          "",
	} {
		if got := compactionHarnessByLayout(path); got != want {
			t.Errorf("compactionHarnessByLayout(%s) = %q, want %q", path, got, want)
		}
	}
}

// Records as Gemini CLI 0.60 writes them, taken from a real run against a
// stub API: a failing go test, a resume, go vet, then a compression.
func gTurn(id, typ string, content any) map[string]any {
	return map[string]any{"id": id, "timestamp": "2026-10-03T11:49:22.587Z", "type": typ, "content": content}
}

func gText(s string) []any { return []any{map[string]any{"text": s}} }

func gShell(id, command, output string) map[string]any {
	m := gTurn(id, "gemini", "")
	m["toolCalls"] = []any{map[string]any{
		"id": "call-" + id, "name": "run_shell_command", "args": map[string]any{"command": command},
		"result": []any{map[string]any{"functionResponse": map[string]any{
			"id": "call-" + id, "name": "run_shell_command",
			"response": map[string]any{"output": "<untrusted_context>\nOutput: " + output + "\n</untrusted_context>"},
		}}},
		"status": "success",
	}}
	return m
}

// geminiCall is the same turn the way a snapshot lists it: history-shaped, the
// call in the content and no toolCalls.
func gCall(id, command string) map[string]any {
	return gTurn(id, "gemini", []any{map[string]any{"functionCall": map[string]any{"name": "run_shell_command", "args": map[string]any{"command": command}}}})
}

func gSet(turns ...map[string]any) map[string]any {
	list := make([]any, len(turns))
	for i, t := range turns {
		list[i] = t
	}
	return map[string]any{"$set": map[string]any{"messages": list}}
}

var (
	geminiHeader = map[string]any{"sessionId": "g-1", "projectHash": "h", "startTime": "2026-10-03T11:49:20Z", "lastUpdated": "2026-10-03T11:49:20Z", "kind": "main"}
	geminiCtx    = gTurn("ctx", "user", gText("<session_context>\nThis is the Gemini CLI."))
	geminiAsk    = gTurn("u1", "user", gText("fix the parser test"))
	geminiTest   = gShell("g1", "go test ./parser/...", "--- FAIL: TestParseSeed (0.00s)\n    parse_test.go:7: want 3, got 4\nFAIL\nExit Code: 1")
	geminiSaid   = gTurn("g2", "gemini", "The parser test fails: want 3, got 4. I decided to fix parse.go next.")
	geminiVetAsk = gTurn("u2", "user", gText("run vet"))
	geminiVet    = gShell("g3", "go vet ./...", "(empty)")
	geminiResume = []map[string]any{
		geminiHeader, gSet(geminiCtx), {"$set": map[string]any{"sessionId": "g-1"}},
		gSet(geminiCtx, geminiAsk, gCall("g1", "go test ./parser/..."), geminiSaid),
	}
	geminiSummary = gTurn("s1", "user", gText("<state_snapshot><overall_goal>fix parser</overall_goal></state_snapshot>"))
	geminiAck     = gTurn("s2", "gemini", "Got it. Thanks for the additional context!")
)

func writeGeminiChat(t *testing.T, records ...map[string]any) (path, workspace string) {
	t.Helper()
	root := t.TempDir()
	workspace = filepath.Join(root, "proj")
	idDir := filepath.Join(root, "gemini", "tmp", "proj")
	if err := os.MkdirAll(filepath.Join(idDir, "chats"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, ".project_root"), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	path = filepath.Join(idDir, "chats", "session-2026-10-03T11-49-g1.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, workspace
}

func geminiSessionText(tr CompactionTranscript) string {
	var b strings.Builder
	for _, m := range tr.Session.Messages {
		b.WriteString(m.Role + ": " + m.Text + "\n")
	}
	return b.String()
}

func TestGeminiCompactionIsFoundWithTheTurnsBeforeIt(t *testing.T) {
	records := append([]map[string]any{geminiHeader, gSet(geminiCtx), geminiAsk, geminiTest, geminiSaid}, geminiResume...)
	records = append(records, geminiVetAsk, geminiVet,
		map[string]any{"$set": map[string]any{"sessionId": "g-1"}},
		gSet(geminiCtx, geminiSummary, geminiAck, gTurn("k1", "user", gText("run vet")), gCall("k2", "go vet ./...")),
		gTurn("g4", "gemini", "go vet is clean."))
	path, workspace := writeGeminiChat(t, records...)
	tr, found, err := ReadGeminiCompaction(path, "g-1")
	if err != nil || !found {
		t.Fatalf("compaction not found: found=%v err=%v", found, err)
	}
	text := geminiSessionText(tr)
	for _, want := range []string{"fix the parser test", "$ go test ./parser/...  → exit 1", "want 3, got 4", "$ go vet ./...", "run vet"} {
		if !strings.Contains(text, want) {
			t.Errorf("captured session lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"state_snapshot", "go vet is clean"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("captured session holds %q from after the compaction:\n%s", unwanted, text)
		}
	}
	if tr.Workspace != workspace || tr.Harness != "gemini" {
		t.Fatalf("identity: workspace %q harness %q", tr.Workspace, tr.Harness)
	}
	// The same compaction read again after more turns is the same capture.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"id":"g5","type":"gemini","content":"later"}` + "\n")
	_ = f.Close()
	again, found, err := ReadGeminiCompaction(path, "g-1")
	if err != nil || !found || again.Fingerprint != tr.Fingerprint {
		t.Fatalf("a later turn changed the capture: %v %v %q vs %q", found, err, again.Fingerprint, tr.Fingerprint)
	}
	if _, _, err := ReadGeminiCompaction(path, "g-2"); err == nil {
		t.Fatal("another session id read this transcript")
	}
}

func TestGeminiRewritesThatAreNotCompactions(t *testing.T) {
	base := []map[string]any{geminiHeader, gSet(geminiCtx), geminiAsk, geminiTest, geminiSaid}
	cases := map[string][]map[string]any{
		// A resume writes the opening context alone, then the history back.
		"resume": append(append([]map[string]any{}, base...), geminiResume...),
		// Masking rewrites what a turn says and keeps every turn.
		"masking": append(append([]map[string]any{}, base...), gSet(geminiCtx, geminiAsk, gCall("g1", "go test ./parser/..."), geminiSaid)),
		// A rewind cuts the end.
		"rewind": append(append([]map[string]any{}, base...), map[string]any{"$rewindTo": "g2"}),
	}
	for name, records := range cases {
		path, _ := writeGeminiChat(t, records...)
		if _, found, err := ReadGeminiCompaction(path, "g-1"); err != nil || found {
			t.Errorf("%s read as a compaction: found=%v err=%v", name, found, err)
		}
	}
}

// Later builds append the summary turns and then a $patch that takes the old
// ones out (services/chatRecordingService.ts updateMessagesFromHistory).
func TestGeminiPatchCompactionStopsBeforeTheSummaryTurns(t *testing.T) {
	path, _ := writeGeminiChat(t, geminiHeader, geminiAsk, geminiTest, geminiSaid, geminiSummary, geminiAck,
		map[string]any{"$patch": map[string]any{"removeIds": []string{"u1", "g1", "g2"}, "orderIds": []string{"s1", "s2"}}})
	tr, found, err := ReadGeminiCompaction(path, "g-1")
	if err != nil || !found {
		t.Fatalf("patch compaction not found: found=%v err=%v", found, err)
	}
	text := geminiSessionText(tr)
	if !strings.Contains(text, "go test ./parser/...") || strings.Contains(text, "state_snapshot") || strings.Contains(text, "Got it") {
		t.Fatalf("boundary misplaced:\n%s", text)
	}
}

// A resume used to cost a session every command before it: the snapshot it
// writes lists the call in the turn's content, not in toolCalls.
func TestGeminiResumeKeepsTheCommandsBeforeIt(t *testing.T) {
	records := append([]map[string]any{geminiHeader, gSet(geminiCtx), geminiAsk, geminiTest, geminiSaid}, geminiResume...)
	path, _ := writeGeminiChat(t, records...)
	ss, err := ParseGeminiFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	var text strings.Builder
	for _, m := range ss[0].Messages {
		text.WriteString(m.Role + ": " + m.Text + "\n")
	}
	if !strings.Contains(text.String(), "command: $ go test ./parser/...  → exit 1") {
		t.Fatalf("the resume dropped the command:\n%s", text.String())
	}
}

// A ZCode compaction adds a compact_summary message and keeps the turns before
// it in the CLI database (3.14.4 stand). The capture is the session as it
// stood before the newest summary, and the same summary keeps its fingerprint.
func TestZCodeCompactionReadsTheTurnsBeforeTheSummary(t *testing.T) {
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
insert into session values ('sess_1', '/work/app', 1790000000000, 1790000090000);
insert into message values ('m1', 'sess_1', 1790000001000, '{"role":"user","semantics":{"kind":"user_prompt"}}');
insert into message values ('m2', 'sess_1', 1790000002000, '{"role":"assistant","semantics":{"kind":"assistant_response"}}');
insert into message values ('m3', 'sess_1', 1790000050000, '{"role":"user","summary":{"title":"Compact summary"},"semantics":{"kind":"compact_summary"}}');
insert into message values ('m4', 'sess_1', 1790000060000, '{"role":"user","semantics":{"kind":"user_prompt"}}');
insert into part values ('p1', 'm1', '{"type":"text","text":"fix the parser test"}');
insert into part values ('p2', 'm2', '{"type":"text","text":"the parser test fails on escapes"}');
insert into part values ('p3', 'm3', '{"type":"text","text":"summary of the work so far","synthetic":true}');
insert into part values ('p4', 'm4', '{"type":"text","text":"carry on after the summary"}');
`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	got, found, err := ReadZCodeCompaction("sess_1")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	texts := make([]string, 0, len(got.Session.Messages))
	for _, m := range got.Session.Messages {
		texts = append(texts, m.Text)
	}
	all := strings.Join(texts, "\n")
	if !strings.Contains(all, "fix the parser test") || strings.Contains(all, "carry on after the summary") {
		t.Errorf("captured %q, want the turns before the summary only", all)
	}
	if got.Workspace != "/work/app" {
		t.Errorf("workspace %q", got.Workspace)
	}
	again, _, _ := ReadZCodeCompaction("sess_1")
	if again.Fingerprint != got.Fingerprint {
		t.Error("the same summary read twice changed its fingerprint")
	}
	if _, found, _ := ReadZCodeCompaction("sess_other"); found {
		t.Error("a session with no summary was reported compacted")
	}
}

func TestZCodeHookTranscriptIsTheTempFile(t *testing.T) {
	if !ZCodeHookTranscript(filepath.Join(os.TempDir(), "zcode-claude-hook-8rQtnE", "transcript.jsonl")) {
		t.Error("ZCode's hook transcript not recognised")
	}
	if ZCodeHookTranscript(filepath.Join("work", "projects", "x.jsonl")) || ZCodeHookTranscript("") {
		t.Error("another transcript taken for ZCode's")
	}
}

// CodeWhale saves the history before a compaction and the summary beside it
// once written; the hooks' session id is not the saved one, so the session is
// the one in the workspace with a summary written since the hook session
// began, and a history with no summary is a failed attempt.
func TestCodeWhaleCompactionReadsTheSavedHistory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	t.Setenv("CODEWHALE_HOME", filepath.Join(root, "..", "home"))
	work := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "s1.json"), `{"metadata":{"id":"s1","workspace":`+quoteJSON(work)+`,"created_at":"2026-10-07T10:00:00Z"},"messages":[]}`)
	write(filepath.Join(root, "s2.json"), `{"metadata":{"id":"s2","workspace":"/elsewhere","created_at":"2026-10-07T10:00:00Z"},"messages":[]}`)
	arts := filepath.Join(root, "s1", "artifacts")
	write(filepath.Join(arts, "context-transfer-a.json"), `[{"role":"user","content":[{"type":"text","text":"fix the tokenizer test"}]},{"role":"assistant","content":[{"type":"text","text":"looking"}]}]`)
	write(filepath.Join(root, "s2", "artifacts", "context-transfer-b.json"), `[{"role":"user","content":[{"type":"text","text":"another workspace"}]}]`)
	write(filepath.Join(root, "s2", "artifacts", "context-transfer-b.md"), "summary")
	since := time.Now().Add(-time.Minute)
	if _, found, _ := ReadCodeWhaleCompaction(work, since); found {
		t.Fatal("a history with no summary was taken for a compaction")
	}
	write(filepath.Join(arts, "context-transfer-a.md"), "summary of the work")
	got, found, err := ReadCodeWhaleCompaction(work, since)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if got.Session.ID != "s1" || len(got.Session.Messages) == 0 || got.Session.Messages[0].Text != "fix the tokenizer test" {
		t.Errorf("captured %s %+v", got.Session.ID, got.Session.Messages)
	}
	if _, found, _ := ReadCodeWhaleCompaction(work, time.Now().Add(time.Minute)); found {
		t.Error("a compaction from before the hook session began was taken")
	}
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func writePiSession(t *testing.T, root, id, cwd string) string {
	t.Helper()
	dir := filepath.Join(root, "--w-proj--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-10-07T09-00-00-000Z_"+id+".jsonl")
	header := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T09:00:00.000Z"`
	if cwd != "" {
		header += `,"cwd":"` + cwd + `"`
	}
	lines := []string{
		header + `}`,
		`{"type":"message","id":"m1","timestamp":"2026-10-07T09:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}]}}`,
		`{"type":"message","id":"m2","timestamp":"2026-10-07T09:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"go test ./parser/..."}}]}}`,
		`{"type":"message","id":"m3","timestamp":"2026-10-07T09:00:03.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"--- FAIL: TestParseSeed\nwant 3, got 4"}],"isError":true}}`,
		`{"type":"compaction","id":"k1","timestamp":"2026-10-07T09:00:04.000Z","summary":"working on the parser","firstKeptEntryId":"m3"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The registry finds the reader for the file the host names, and the session
// is the one that reader returns under the host's id.
func TestCompactionSessionReadsTheNamedFileWithItsOwnReader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "pi")
	t.Setenv("DEJA_PI_ROOT", root)
	path := writePiSession(t, root, "pi-1", "/w/proj")
	tr, err := ReadCompactionSession("", "pi-1", path, "")
	if err != nil {
		t.Fatal(err)
	}
	text := storeSessionText(tr)
	if tr.Harness != "pi" || tr.Workspace != "/w/proj" || tr.Path != path || !strings.Contains(text, "go test ./parser/...") || !strings.Contains(text, "want 3, got 4") {
		t.Fatalf("harness %q workspace %q path %q\n%s", tr.Harness, tr.Workspace, tr.Path, text)
	}
	if _, err := ReadCompactionSession("", "pi-2", path, ""); !errors.Is(err, ErrTranscriptIdentity) {
		t.Fatalf("another session's file: %v", err)
	}
	if _, err := ReadCompactionSession("kimi", "pi-1", path, ""); err == nil {
		t.Fatal("a file read under a harness it does not belong to")
	}
}

// A host that names only the session and its harness: the store's file that
// names the id is the one read.
func TestCompactionSessionFindsTheSessionByID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "pi")
	t.Setenv("DEJA_PI_ROOT", root)
	writePiSession(t, root, "pi-other", "/w/other")
	path := writePiSession(t, root, "pi-1", "/w/proj")
	tr, err := ReadCompactionSession("pi", "pi-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Path != path || tr.Session.ID != "pi-1" {
		t.Fatalf("read %s as %s", tr.Path, tr.Session.ID)
	}
	if _, err := ReadCompactionSession("pi", "pi-missing", "", ""); err == nil {
		t.Fatal("a session the store does not hold was captured")
	}
	if _, err := ReadCompactionSession("", "pi-1", "", ""); !errors.Is(err, ErrUnsupportedCompactionTranscript) {
		t.Fatalf("neither file nor harness: %v", err)
	}
}

// A session that records no directory takes the hook's, but only when the
// project it was filed under is that directory's.
func TestCompactionSessionWorkspaceHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "pi")
	t.Setenv("DEJA_PI_ROOT", root)
	path := writePiSession(t, root, "pi-1", "")
	tr, err := ReadCompactionSession("pi", "pi-1", path, "/elsewhere/entirely")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Workspace != "" {
		t.Fatalf("took an unrelated hint: %q", tr.Workspace)
	}
	hint := "/x/" + strings.TrimPrefix(tr.Session.Project, "/")
	if tr, _ := ReadCompactionSession("pi", "pi-1", path, hint); tr.Workspace != hint {
		t.Fatalf("project %q, hint %q, workspace %q", tr.Session.Project, hint, tr.Workspace)
	}
}

func storeSessionText(tr CompactionTranscript) string {
	var b strings.Builder
	for _, m := range tr.Session.Messages {
		b.WriteString(m.Role + ": " + m.Text + "\n")
	}
	return b.String()
}

// opencode and Kilo CLI keep every message in SQLite and filter the compacted
// ones out only of what they send the model, so the session read by id still
// has the turns being summarised.
func TestCompactionStoreReadsOneSessionByID(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	for _, harness := range []string{"opencode", "kilocode"} {
		db := filepath.Join(t.TempDir(), harness+".db")
		script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_a','/w/proj','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into session values('ses_b','/w/other','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','ses_a',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"fix the parser test"}');
insert into message values('m2','ses_a',1767409201000,'{"role":"assistant"}');
insert into part values('p2','m2','{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./parser/..."},"output":"--- FAIL: TestParseSeed\nwant 3, got 4","metadata":{"exit":1}},"time":{"start":"2026-01-02T03:00:01Z"}}');
insert into message values('m3','ses_b',1767409202000,'{"role":"user"}');
insert into part values('p3','m3','{"type":"text","text":"someone else"}');`
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
			t.Fatalf("sqlite setup: %v %s", err, out)
		}
		t.Setenv("DEJA_OPENCODE_DB", db)
		t.Setenv("DEJA_KILO_DB", db)
		tr, err := ReadCompactionStore(harness, "ses_a")
		if err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		text := storeSessionText(tr)
		if tr.Harness != harness || tr.Workspace != "/w/proj" || !strings.Contains(text, "go test ./parser/...  → exit 1") || strings.Contains(text, "someone else") {
			t.Fatalf("%s: harness %q workspace %q\n%s", harness, tr.Harness, tr.Workspace, text)
		}
		if _, err := ReadCompactionStore(harness, "ses_missing"); !errors.Is(err, ErrTranscriptIdentity) {
			t.Fatalf("%s: a session the store lacks: %v", harness, err)
		}
	}
	if _, err := ReadCompactionStore("claude", "ses_a"); !errors.Is(err, ErrUnsupportedCompactionTranscript) {
		t.Fatalf("a harness without a store: %v", err)
	}
}

// Hermes hands its memory provider the turns it compresses, in the shape it
// keeps in state.db; they read as the store's rows do.
func TestCompactionMessagesReadHermesTurns(t *testing.T) {
	result := mustJSON(t, map[string]any{"output": "--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL", "exit_code": 1})
	msgs := mustJSON(t, []map[string]any{
		{"role": "user", "content": "fix the parser test"},
		{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "terminal", "arguments": `{"command":"go test ./parser/..."}`}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": string(result)},
		{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "It fails: want 3, got 4."}}},
	})
	tr, err := ReadCompactionMessages("hermes", "h-1", "/w/proj", msgs)
	if err != nil {
		t.Fatal(err)
	}
	text := storeSessionText(tr)
	for _, want := range []string{"user: fix the parser test", "go test ./parser/...  → exit 1", "want 3, got 4", "assistant: It fails"} {
		if !strings.Contains(text, want) {
			t.Errorf("hermes turns lack %q:\n%s", want, text)
		}
	}
	if tr.Session.ID != "h-1" || tr.Workspace != "/w/proj" {
		t.Fatalf("identity: %q %q", tr.Session.ID, tr.Workspace)
	}
	if _, err := ReadCompactionMessages("hermes", "h-1", "/w/proj", json.RawMessage(`[]`)); err == nil {
		t.Fatal("no turns made a capture")
	}
	if _, err := ReadCompactionMessages("opencode", "h-1", "/w/proj", msgs); err == nil {
		t.Fatal("turns from a host that does not send them were read")
	}
}

// Each registry fixture below carries the summary its harness writes when it
// compacts, in the shape that harness writes it. The reader has to index it
// under the summary role: dropped, it is the only record of the turns the
// compaction took away; read as speech, it is words nobody said (#4801).
var compactionSummaryFixtures = []struct {
	host, fixture, marker string
}{
	{"codex", "codex/rollout-2026-07-17T09-00-00-registry-codex.jsonl", "CODEX-SUMMARY"},
	{"copilot", "copilot/7cf44517-55ca-435c-893a-3fde1973a44e/events.jsonl", "COPILOT-SUMMARY"},
	{"copilot-chat", "copilot-chat/workspaceStorage/a1b2c3d4e5f6/chatSessions/3b7e1f1b-0000-4000-8000-000000000000.jsonl", "COPILOT-CHAT-SUMMARY"},
	{"kimi", "kimi/sessions/wd_demo_0123456789ab/session_fixture01/agents/main/wire.jsonl", "KIMI-SUMMARY"},
	{"qwen", "qwen/projects/-workspace-registry-demo/chats/registry-qwen.jsonl", "QWEN-SUMMARY"},
	{"pi", "pi/session.jsonl", "PI-SUMMARY"},
	{"pi", "pi/session.jsonl", "PI-BRANCH-SUMMARY"},
	{"omp", "omp/session.jsonl", "OMP-SUMMARY"},
	{"prime", "prime/session.jsonl", "PRIME-SUMMARY"},
	{"senpi", "senpi/-workspace-senpi-demo/session.jsonl", "SENPI-SUMMARY"},
	{"gjc", "gjc/-workspace-gjc-demo/session.jsonl", "GJC-SUMMARY"},
	{"kimchi", "kimchi/--workspace-kimchi-demo--/session.jsonl", "KIMCHI-SUMMARY"},
	{"openclaw", "openclaw/agents/main/sessions/a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d.jsonl", "OPENCLAW-SUMMARY"},
	{"openclaw", "openclaw/agent/openclaw-agent.sql", "OPENCLAW-SUMMARY"},
	{"roo", "roo/tasks/1767225700000/api_conversation_history.json", "ROO-SUMMARY"},
	{"kilocode", "kilocode/tasks/registry-kilocode/api_conversation_history.json", "KILO-SUMMARY"},
	{"zcode", "zcode/cli-db.sql", "ZCODE-SUMMARY"},
	{"codewhale", "codewhale/sessions/registry-codewhale.json", "CODEWHALE-SUMMARY"},
	{"crush", "crush/crush.sql", "CRUSH-SUMMARY"},
	{"antigravity", "antigravity/brain/registry-antigravity/.system_generated/logs/transcript.jsonl", "ANTIGRAVITY-SUMMARY"},
	{"mcode", "mcode/v2/sessions/2026/10/20/02-57-27-477-session_bXZzXzBmMWUyZDNjNGI1YTY5Nzg4Nzk2YTViNGMzZDJlMWYw/messages.jsonl", "MCODE-SUMMARY"},
}

func TestRegistryFixturesIndexCompactionSummaries(t *testing.T) {
	root := filepath.Join("..", "..", "fixtures", "registry")
	for _, c := range compactionSummaryFixtures {
		t.Run(c.host+"/"+c.marker, func(t *testing.T) {
			ss := parseRegistryFixture(t, c.host, filepath.Join(root, filepath.FromSlash(c.fixture)))
			var summaries, elsewhere int
			for _, s := range ss {
				for _, m := range s.Messages {
					if !strings.Contains(m.Text, c.marker) {
						continue
					}
					if m.Role == RoleSummary {
						summaries++
					} else {
						elsewhere++
					}
				}
			}
			if summaries != 1 || elsewhere != 0 {
				t.Errorf("%s: summary indexed %d times under its role and %d times under another, want once and never", c.host, summaries, elsewhere)
			}
		})
	}
}

// Three readers dropped the summary their harness writes when it compacts, or
// read it as the assistant's speech (#4795). Each shape here is the one the
// harness wrote on a stand: Zed 1.22's thread enum, the Cline CLI 3.0.69
// compaction file, Continue cn 1.5.47's rewritten session.
func roles(ms []model.Message) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		out[m.Text] = m.Role
	}
	return out
}

func TestZedCompactionSummaryIsKeptUnderItsRole(t *testing.T) {
	zedHome(t)
	body := `{"version":"0.3.0","title":"t","updated_at":"2026-10-07T09:00:02Z","messages":[` +
		`{"User":{"id":"u1","content":[{"Text":"why does the exporter drop retries"}]}},` +
		`{"Agent":{"content":[{"Text":"the backoff counts from zero"}],"tool_results":{}}},` +
		`{"Compaction":{"Summary":"ZED-SUMMARY the exporter retry budget was fixed"}},` +
		`{"Compaction":{"ProviderNative":{"provider":"anthropic","items":[{"opaque":true}]}}},` +
		`{"User":{"id":"u2","content":[{"Text":"continue"}]}}]}`
	sql := zedSchema + `
insert into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values
 ('c1','t','2026-10-07T09:00:02+00:00','json','` + body + `','/w/p','2026-10-07T09:00:00+00:00');`
	ss, err := ParseZedDB(zedTestDB(t, sql))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	got := roles(ss[0].Messages)
	if got["ZED-SUMMARY the exporter retry budget was fixed"] != RoleSummary {
		t.Errorf("summary = %q, want the summary role: %v", got["ZED-SUMMARY the exporter retry budget was fixed"], got)
	}
	if got["why does the exporter drop retries"] != "user" || got["continue"] != "user" {
		t.Errorf("the turns around it moved: %v", got)
	}
	if len(ss[0].Messages) != 4 {
		t.Errorf("messages = %d, want 4 (the provider-native compaction has no text): %v", len(ss[0].Messages), got)
	}
}

func TestClineCompactionSummaryIsReadFromItsFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions", "1791367873575_myzey")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"1791367873575_myzey.json": `{"session_id":"1791367873575_myzey","cwd":"/w/proj","started_at":"2026-10-07T10:11:00Z"}`,
		"1791367873575_myzey.messages.json": `{"agent":"lead","messages":[` +
			`{"role":"user","content":[{"type":"text","text":"probe the stub"}],"ts":1791367860000},` +
			`{"role":"assistant","content":[{"type":"text","text":"probed"}],"ts":1791367861000},` +
			`{"role":"user","content":[{"type":"text","text":"and again"}],"ts":1791367880000}]}`,
		"1791367873575_myzey.compaction.json": `{"version":1,"updated_at":"2026-10-07T10:11:16.387Z","conversation_id":"1791367873575_myzey","source_message_count":14,"messages":[` +
			`{"role":"user","content":[{"type":"text","text":"Context summary:\n\nSTUB-SUMMARY-5: the conversation so far was about probes."}],` +
			`"metadata":{"kind":"compaction_summary","displayRole":"system","summary":"STUB-SUMMARY-5: the conversation so far was about probes.","generatedAt":1791367876386}},` +
			`{"role":"user","content":[{"type":"text","text":"and again"}]}]}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ss, err := ParseClineFile(filepath.Join(dir, "1791367873575_myzey.messages.json"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	order := make([]string, 0, len(ss[0].Messages))
	for _, m := range ss[0].Messages {
		order = append(order, m.Role+":"+m.Text)
	}
	want := "user:probe the stub|assistant:probed|" + RoleSummary + ":STUB-SUMMARY-5: the conversation so far was about probes.|user:and again"
	if strings.Join(order, "|") != want {
		t.Errorf("messages = %q\nwant       %q", strings.Join(order, "|"), want)
	}
	// The file changes without the transcript, so it has to move the
	// fingerprint that re-reads the session.
	before, _ := clineSDKSidecar(filepath.Join(dir, "1791367873575_myzey.messages.json"))
	if err := os.WriteFile(filepath.Join(dir, "1791367873575_myzey.compaction.json"), []byte(files["1791367873575_myzey.compaction.json"]+" "), 0o644); err != nil {
		t.Fatal(err)
	}
	if after, _ := clineSDKSidecar(filepath.Join(dir, "1791367873575_myzey.messages.json")); after == before {
		t.Error("a new compaction file does not re-read the session")
	}
}

func TestContinueConversationSummaryIsNotTheAssistant(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "632143e2.json")
	body := `{"sessionId":"632143e2","title":"Untitled Session","workspaceDirectory":"/w/proj","history":[` +
		`{"message":{"role":"assistant","content":"CN-SUMMARY the file was read"},"contextItems":[],"conversationSummary":"CN-SUMMARY the file was read"},` +
		`{"message":{"role":"user","content":"next question"},"contextItems":[]}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	got := roles(ss[0].Messages)
	if got["CN-SUMMARY the file was read"] != RoleSummary || got["next question"] != "user" || len(ss[0].Messages) != 2 {
		t.Errorf("messages = %v, want the summary under its own role once", got)
	}
}

// The edges of the summary shapes the registry fixtures do not carry.

func writeShape(t *testing.T, rel, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func roleSequence(ss []model.Session) string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			out = append(out, m.Role+":"+m.Text)
		}
	}
	return strings.Join(out, "|")
}

// An older rollout speaks only in events; its compaction summary must not make
// the reader take the roled stream as present and drop every turn.
func TestCodexSummaryKeepsAnEventOnlyRollout(t *testing.T) {
	p := writeShape(t, "sessions/rollout-2026-01-01T00-00-00-ev.jsonl",
		`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"ev","cwd":"/w/p"}}
{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"why is it slow"}}
{"timestamp":"2026-01-01T00:00:02Z","type":"compacted","payload":{"message":"S1 it was the index"}}
{"timestamp":"2026-01-01T00:00:03Z","type":"event_msg","payload":{"type":"agent_message","message":"rebuilt the index"}}
{"timestamp":"2026-01-01T00:00:04Z","type":"compacted","payload":{"message":"","replacement_history":[{"type":"compaction","encrypted_content":"x"}]}}
`)
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "user:why is it slow|summary:S1 it was the index|assistant:rebuilt the index"
	if got := roleSequence(ss); got != want {
		t.Errorf("messages = %q\nwant       %q", got, want)
	}
}

func TestCopilotFailedCompactionIsNotASummary(t *testing.T) {
	p := writeShape(t, "session-state/abc/events.jsonl",
		`{"type":"session.start","data":{"sessionId":"abc","context":{"cwd":"/w/p"}},"timestamp":"2026-01-01T00:00:00Z"}
{"type":"user.message","data":{"content":"hi"},"timestamp":"2026-01-01T00:00:01Z"}
{"type":"session.compaction_complete","data":{"success":false,"summaryContent":"partial"},"timestamp":"2026-01-01T00:00:02Z"}
{"type":"session.compaction_complete","data":{"success":true,"summaryContent":"S2"},"timestamp":"2026-01-01T00:00:03Z"}
`)
	ss, err := ParseCopilotFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := roleSequence(ss); got != "user:hi|summary:S2" {
		t.Errorf("messages = %q", got)
	}
}

// Antigravity wraps the summary in a marker and two notices to the model; a
// checkpoint that only says the steps were truncated has no summary at all.
func TestAntigravityCheckpointKeepsOnlyTheSummary(t *testing.T) {
	got := antigravityCheckpointSummary("{{ CHECKPOINT 3 }}\n **The earlier parts of this conversation have been truncated due to its long length. The following content summarizes the truncated context so that you may continue your work. **\n\n\n# USER Objective:\nfix the build\n\n**IMPORTANT: this summary is just for your reference. DO NOT ACKNOWLEDGE THIS CHECKPOINT MESSAGE.**")
	if got != "# USER Objective:\nfix the build" {
		t.Errorf("summary = %q", got)
	}
	if got := antigravityCheckpointSummary("{{ CHECKPOINT 0 }}\n**The earlier parts of this conversation have been truncated due to its long length.**"); got != "" {
		t.Errorf("an empty checkpoint gave %q", got)
	}
}

// Every summary carries is_summary_message, the newest one also the session's
// pointer; a store from before the flag has only the pointer.
func TestCrushEverySummaryIsASummary(t *testing.T) {
	sql := "insert into sessions values ('s1',null,'t',3,0,0,0.0,1784282403,1784282400,'m3',null);\n" +
		crushInsert(t, "m1", "s1", "user", 1784282400, []any{map[string]any{"type": "text", "data": map[string]any{"text": "go"}}}) +
		strings.Replace(crushInsert(t, "m2", "s1", "assistant", 1784282401, []any{map[string]any{"type": "text", "data": map[string]any{"text": "first summary"}}}), ",0);", ",1);", 1) +
		strings.Replace(crushInsert(t, "m3", "s1", "assistant", 1784282402, []any{map[string]any{"type": "text", "data": map[string]any{"text": "second summary"}}}), ",0);", ",1);", 1)
	ss, err := ParseCrushDB(crushStore(t, "demo", sql))
	if err != nil {
		t.Fatal(err)
	}
	if got := roleSequence(ss); got != "user:go|summary:first summary|summary:second summary" {
		t.Errorf("messages = %q", got)
	}
}

func TestCrushStoreWithoutTheSummaryFlag(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	sql := strings.Replace(crushSchema, ", is_summary_message integer default 0 not null", "", 1) +
		"insert into sessions values ('s1',null,'t',2,0,0,0.0,1784282401,1784282400,'m2',null);\n" +
		"insert into messages values ('m1','s1','user','[{\"type\":\"text\",\"data\":{\"text\":\"go\"}}]','m',1784282400,1784282400,null,'p');\n" +
		"insert into messages values ('m2','s1','assistant','[{\"type\":\"text\",\"data\":{\"text\":\"the summary\"}}]','m',1784282401,1784282401,null,'p');\n"
	db := filepath.Join(t.TempDir(), "demo", ".crush", "crush.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(t.Context(), "sqlite3", db)
	build.Stdin = strings.NewReader(sql)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build store: %v: %s", err, out)
	}
	ss, err := ParseCrushDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := roleSequence(ss); got != "user:go|summary:the summary" {
		t.Errorf("messages = %q", got)
	}
}

// A manual microcompact writes the same record with the old history trimmed
// and no summary; its first entry is an ordinary turn.
func TestQwenMicrocompactIsNotASummary(t *testing.T) {
	trimmed := map[string]any{"compressedHistory": []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "an old question"}}},
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": "an old answer"}}},
	}}
	if got := qwenCompressionSummary(trimmed); got != "" {
		t.Errorf("microcompact read as summary %q", got)
	}
	older := map[string]any{"compressedHistory": []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "<state_snapshot>S</state_snapshot>"}}},
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": qwenSummaryAck}}},
	}}
	if got := qwenCompressionSummary(older); got != "<state_snapshot>S</state_snapshot>" {
		t.Errorf("summary without the trailer = %q", got)
	}
}

func TestKimiOlderCompactionRecordCarriesAMessage(t *testing.T) {
	p := writeShape(t, "sessions/wd/session_x/agents/main/wire.jsonl",
		`{"type":"metadata","protocol_version":"1.3","created_at":1782295200000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"go"}]},"time":1782295201000}
{"type":"context.apply_compaction","summary":{"role":"user","content":[{"type":"text","text":"S3"}]},"count":2,"time":1782295202000}
`)
	ss, err := ParseKimiFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := roleSequence(ss); got != "user:go|summary:S3" {
		t.Errorf("messages = %q", got)
	}
}

// The summary text, without what each harness wraps around it for the model.
func TestRegistrySummariesLeaveTheWrappingOut(t *testing.T) {
	root := filepath.Join("..", "..", "fixtures", "registry")
	for _, c := range []struct{ host, fixture, want string }{
		{"codewhale", "codewhale/sessions/registry-codewhale.json", "CODEWHALE-SUMMARY the deadline check ran before the attempt was counted"},
		{"roo", "roo/tasks/1767225700000/api_conversation_history.json", "ROO-SUMMARY the flaky test came from a stale timeout in the retry loop"},
		{"kilocode", "kilocode/tasks/registry-kilocode/api_conversation_history.json", "KILO-SUMMARY the migration hung on an advisory lock that was never released"},
		{"qwen", "qwen/projects/-workspace-registry-demo/chats/registry-qwen.jsonl", "<state_snapshot>QWEN-SUMMARY the sample parser fixture was found</state_snapshot>"},
		{"zcode", "zcode/cli-db.sql", "ZCODE-SUMMARY the stream was cut short by the proxy idle timeout"},
		{"antigravity", "antigravity/brain/registry-antigravity/.system_generated/logs/transcript.jsonl", "# USER Objective:\nANTIGRAVITY-SUMMARY inspect the transcript\n\n# User Requests\nThe following were user requests from the truncated conversation in chronological order:\n1. inspect the antigravity transcript"},
	} {
		t.Run(c.host, func(t *testing.T) {
			ss := parseRegistryFixture(t, c.host, filepath.Join(root, filepath.FromSlash(c.fixture)))
			var got []string
			for _, s := range ss {
				for _, m := range s.Messages {
					if m.Role == RoleSummary {
						got = append(got, m.Text)
					}
					if strings.Contains(m.Text, "Sliding window truncation") {
						t.Errorf("the truncation marker was indexed as %s", m.Role)
					}
				}
			}
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("summaries = %q\nwant        %q", got, c.want)
			}
		})
	}
}

func TestReadCompactionTranscriptClaudeUsesNativeCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	body := strings.Join([]string{
		`{"type":"user","sessionId":"claude-1","timestamp":"2026-09-10T10:00:00Z","message":{"role":"user","content":"fix the retry"}}`,
		`{"type":"assistant","sessionId":"claude-1","timestamp":"2026-09-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"I will inspect it."},{"type":"tool_use","id":"read-1","name":"Read","input":{"file_path":"retry.go"}},{"type":"tool_use","id":"edit-1","name":"Edit","input":{"file_path":"retry.go"}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "claude" || got.Session.ID != "claude-1" || got.Session.Path != path {
		t.Fatalf("identity=%#v", got)
	}
	if got.Truncated || got.TailOffset != 0 || got.SourceSize != int64(len(body)) || got.Fingerprint == "" || got.HeaderFingerprint == "" {
		t.Fatalf("bad bounded metadata: %#v", got)
	}
	if len(got.ToolCalls) != 2 || got.ToolCalls[0].ID != "read-1" || got.ToolCalls[1].ID != "edit-1" || got.LastToolID != "edit-1" {
		t.Fatalf("native calls=%#v", got.ToolCalls)
	}
	if got.ToolCalls[0].StartOffset >= got.ToolCalls[0].EndOffset {
		t.Fatalf("invalid offsets: %#v", got.ToolCalls[0])
	}
	if !MatchesCompactionBoundary(got, got.SourceSize, got.BoundaryFingerprint) {
		t.Fatal("capture should match its own source boundary")
	}
}

func TestReadCompactionTranscriptClaudeRecognizesFileHistoryPreamble(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	body := strings.Join([]string{
		`{"type":"file-history-snapshot","sessionId":"claude-1","cwd":"/work/widget","message":{"content":"snapshot"}}`,
		// A normal user string used to make the raw-call decoder fail before it
		// validated identity or cwd.
		`{"type":"user","sessionId":"claude-1","cwd":"/work/widget","message":{"role":"user","content":"fix it"}}`,
		`{"type":"assistant","sessionId":"claude-1","cwd":"/work/widget","message":{"role":"assistant","content":[{"type":"tool_use","id":"same-call","name":"Read"}]}}`,
		// Claude may replay a record while streaming. It remains one native call.
		`{"type":"assistant","sessionId":"claude-1","cwd":"/work/widget","message":{"role":"assistant","content":[{"type":"tool_use","id":"same-call","name":"Read"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "claude" || got.Workspace != "/work/widget" || got.Session.Project != "work/widget" {
		t.Fatalf("preamble attribution=%#v", got)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "same-call" {
		t.Fatalf("replayed calls=%#v", got.ToolCalls)
	}
}

func TestReadCompactionTranscriptMarksMalformedWindowUnmeasured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	body := strings.Join([]string{
		`{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"start"}}`,
		`not-json`,
		`{"type":"assistant","sessionId":"claude-1","message":{"role":"assistant","content":"finished"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MetricComplete || !got.Truncated {
		t.Fatalf("malformed capture must be partial and unmeasured: %#v", got)
	}
}

func TestReadCompactionTranscriptRejectsForeignSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	line := `{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCompactionTranscript(path, "someone-else")
	if !errors.Is(err, ErrTranscriptIdentity) {
		t.Fatalf("err=%v, want identity mismatch", err)
	}
}

// The session id pins identity; the directory is where the session is now. A
// session that moves between worktrees used to be refused outright — 6 of 20
// large transcripts on one machine had two cwds in their last 4 MiB, and they
// are the long sessions a compaction packet is for (#4031).
func TestReadCompactionTranscriptFollowsTheSessionAcrossDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	body := strings.Join([]string{
		`{"type":"user","sessionId":"claude-1","cwd":"/work/widget","message":{"role":"user","content":"start here"}}`,
		`{"type":"assistant","sessionId":"claude-1","cwd":"/work/widget","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","sessionId":"claude-1","cwd":"/work/widget-wt","message":{"role":"user","content":"now in the worktree"}}`,
		`{"type":"assistant","sessionId":"claude-1","cwd":"/work/widget-wt","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go vet ./..."}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatalf("a session that changed directory was refused: %v", err)
	}
	if got.Workspace != "/work/widget-wt" {
		t.Fatalf("workspace %q, want the newest record's directory", got.Workspace)
	}
	if len(got.Session.Messages) == 0 {
		t.Fatalf("no messages captured: %#v", got.Session)
	}
}

func TestReadCompactionTranscriptAcceptsStableFinalRecordWithoutNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	line := `{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"hello"}}`
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Session.Messages) != 1 || got.Session.Messages[0].Text != "hello" {
		t.Fatalf("final record was dropped: %#v", got.Session)
	}
}

func TestReadCompactionTranscriptDoesNotNeedTemporaryDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	line := `{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	if _, err := ReadCompactionTranscript(path, "claude-1"); err != nil {
		t.Fatalf("bounded in-memory capture depended on TMPDIR: %v", err)
	}
}

func TestReadCompactionTranscriptRejectsForeignClaudeStringContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	body := strings.Join([]string{
		`{"type":"file-history-snapshot","sessionId":"claude-1","cwd":"/work/widget"}`,
		`{"type":"user","sessionId":"someone-else","cwd":"/work/widget","message":{"role":"user","content":"plain text"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCompactionTranscript(path, "claude-1")
	if !errors.Is(err, ErrTranscriptIdentity) {
		t.Fatalf("err=%v, want identity mismatch", err)
	}
}

func TestReadCompactionTranscriptCodexSkipsDuplicatedEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-test.jsonl")
	body := strings.Join([]string{
		`{"timestamp":"2026-09-10T10:00:00Z","type":"session_meta","payload":{"id":"thread-1","session_id":"session-1","cwd":"/work/app"}}`,
		`{"timestamp":"2026-09-10T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix it"}]}}`,
		`{"timestamp":"2026-09-10T10:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call-1","arguments":"ignored"}}`,
		`{"timestamp":"2026-09-10T10:00:03Z","type":"event_msg","payload":{"type":"function_call","name":"exec_command","call_id":"call-1"}}`,
		`{"timestamp":"2026-09-10T10:00:04Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"call-2","input":"*** Begin Patch"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "codex" || len(got.ToolCalls) != 2 {
		t.Fatalf("capture=%#v", got)
	}
	if got.ToolCalls[0].ID != "call-1" || got.ToolCalls[1].ID != "call-2" {
		t.Fatalf("calls=%#v", got.ToolCalls)
	}
}

func TestReadCompactionTranscriptBoundsLargeClaudeTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err = f.WriteString(`{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"old"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("x", 700)
	for i := range 7000 {
		if _, err = fmt.Fprintf(f, `{"type":"assistant","sessionId":"claude-1","message":{"role":"assistant","content":"%s-%d"}}`+"\n", padding, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.WriteString(`{"type":"assistant","sessionId":"claude-1","message":{"role":"assistant","content":[{"type":"tool_use","id":"latest","name":"Read"}]}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || got.TailOffset == 0 || got.SourceSize <= CompactionTailBytes || len(got.ToolCalls) == 0 || got.LastToolID != "latest" {
		t.Fatalf("large capture=%#v", got)
	}
	if got.ToolCalls[len(got.ToolCalls)-1].StartOffset < got.TailOffset {
		t.Fatalf("tool offset before bounded tail: %#v", got.ToolCalls)
	}
}

func TestReadCompactionTranscriptKeepsTailRecordAfterHeaderOverlap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	header := `{"type":"user","sessionId":"claude-1","message":{"role":"user","content":"start"}}` + "\n"
	call := `{"type":"assistant","sessionId":"claude-1","message":{"role":"assistant","content":[{"type":"tool_use","id":"overlap","name":"Read"}]}}` + "\n"
	// The tail begins inside the header, then starts exactly at the first
	// assistant record after the overlap is removed.
	total := CompactionTailBytes + 80
	padding := strings.Repeat("\n", int(total)-len(header)-len(call))
	if err := os.WriteFile(path, []byte(header+call+padding), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCompactionTranscript(path, "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "overlap" {
		t.Fatalf("tail lost first complete record after header overlap: %#v", got.ToolCalls)
	}
}

func TestReadCompactionTranscriptRejectsOversizedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "too-wide.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(CompactionTailBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCompactionTranscript(path, "claude-1")
	if !errors.Is(err, ErrTranscriptLineTooLarge) {
		t.Fatalf("err=%v, want oversized line", err)
	}
}

func TestCountActionsBeforeEditFailsWhenIntervalFallsOutsideTail(t *testing.T) {
	current := CompactionTranscript{NativeSessionID: "s", SourceSize: 100, TailOffset: 51, MetricComplete: true}
	if got, ok := CountActionsBeforeEdit(current, 50, "prior", "pending"); ok || got != 0 {
		t.Fatalf("got (%d,%t), want unmeasured", got, ok)
	}
	current.TailOffset = 0
	current.ToolCalls = []TranscriptToolCall{
		{ID: "read", Name: "Read", StartOffset: 50},
		{ID: "write", Name: "Write", StartOffset: 70},
	}
	if got, ok := CountActionsBeforeEdit(current, 50, "prior", "pending"); !ok || got != 1 {
		t.Fatalf("got (%d,%t), want one action", got, ok)
	}
}

func TestExplicitEditToolRecognizesNamespacedCodexCalls(t *testing.T) {
	if !IsCompactionEditTool("functions.apply_patch") || !IsCompactionEditTool("functions.Write") || !IsCompactionEditTool("replace_in_file") {
		t.Fatal("namespaced native edit call was not recognized")
	}
}

func TestMatchesCompactionBoundaryRejectsRewrittenTail(t *testing.T) {
	before := CompactionTranscript{SourceSize: 10, TailOffset: 0, tail: []byte("0123456789")}
	before.BoundaryFingerprint = compactionBoundaryFingerprint(before.tail, before.TailOffset, before.SourceSize)
	if !MatchesCompactionBoundary(before, before.SourceSize, before.BoundaryFingerprint) {
		t.Fatal("self boundary did not match")
	}
	after := before
	after.SourceSize = 12
	after.tail = []byte("01234xxxxxZZ")
	if MatchesCompactionBoundary(after, before.SourceSize, before.BoundaryFingerprint) {
		t.Fatal("rewritten boundary was accepted")
	}
}

func TestReadGeminiCompactionLargeTruncatedTranscriptStaysMetricComplete(t *testing.T) {
	pad := strings.Repeat("p", 700)
	records := make([]map[string]any, 0, 7004)
	records = append(records, geminiHeader, gSet(geminiCtx))
	for i := range 7000 {
		records = append(records, gTurn(fmt.Sprintf("pad-%d", i), "gemini", pad))
	}
	records = append(records,
		gSet(geminiCtx, geminiSummary, geminiAck, gTurn("k1", "user", gText("run test")), gCall("k2", "go test ./...")),
		gTurn("tail", "gemini", "done."),
	)
	path, _ := writeGeminiChat(t, records...)
	tr, found, err := ReadGeminiCompaction(path, "g-1")
	if err != nil || !found {
		t.Fatalf("compaction not found: found=%v err=%v", found, err)
	}
	if !tr.Truncated {
		t.Fatalf("expected Truncated=true for oversized transcript (size=%d)", tr.SourceSize)
	}
	if !tr.MetricComplete {
		t.Fatalf("expected MetricComplete=true when all retained tail JSONL lines are valid")
	}

	agRoot := t.TempDir()
	agDir := filepath.Join(agRoot, "brain", "ag-large-1", ".system_generated", "logs")
	if mkdirErr := os.MkdirAll(agDir, 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	agPath := filepath.Join(agDir, "transcript.jsonl")
	af, err := os.Create(agPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = af.Close() }()
	_, _ = af.WriteString(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nfix build\n</USER_REQUEST>"}` + "\n")
	for i := range 7000 {
		_, _ = fmt.Fprintf(af, `{"step_index":%d,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-07-08T14:19:00Z","content":"%s"}`+"\n", i+1, pad)
	}
	_, _ = af.WriteString(`{"step_index":7001,"source":"MODEL","type":"CHECKPOINT","status":"DONE","created_at":"2026-07-08T14:20:00Z","content":"{{ CHECKPOINT 1 }}\n# USER Objective:\nfix build"}` + "\n")
	_, _ = af.WriteString(`{"step_index":7002,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-07-08T14:21:00Z","content":"continuing"}` + "\n")
	if closeErr := af.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	agTr, found, err := ReadAntigravityCompactionIfNew(agPath, "ag-large-1", "/Users/alex/proj", nil)
	if err != nil || !found {
		t.Fatalf("antigravity compaction not found: found=%v err=%v", found, err)
	}
	if !agTr.Truncated {
		t.Fatalf("expected antigravity Truncated=true for oversized transcript (size=%d)", agTr.SourceSize)
	}
	if !agTr.MetricComplete {
		t.Fatalf("expected antigravity MetricComplete=true when all retained tail JSONL lines are valid")
	}
}

func TestFileContainsTokenBoundedReadsHeadAndTailOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	const window = compactionProbeWindow
	pad := strings.Repeat("x", int(window)*3)
	content := "head-token\n" + pad + "\nmiddle-only-token\n" + pad + "\ntail-token\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, n := fileContainsTokenBounded(path, []byte("head-token")); !ok || n != window {
		t.Fatalf("head probe = (%v, %d), want (true, %d)", ok, n, window)
	}
	if ok, n := fileContainsTokenBounded(path, []byte("tail-token")); !ok || n != 2*window {
		t.Fatalf("tail probe = (%v, %d), want (true, %d)", ok, n, 2*window)
	}
	if ok, n := fileContainsTokenBounded(path, []byte("middle-only-token")); ok || n != 2*window {
		t.Fatalf("middle probe = (%v, %d), want (false, %d)", ok, n, 2*window)
	}
}

func TestCompactionCandidatesByIDRespectsProbeBudget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "codex")
	t.Setenv("DEJA_CODEX_ROOT", root)
	dir := filepath.Join(root, "sessions", "2026", "11", "21")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write 8 large rollouts (2 MiB each) with another session id. Calling
	// ReadCompactionSession with an unknown id must inspect only the bounded
	// head+tail window rather than reading all bytes into memory.
	payload := strings.Repeat("{\"type\":\"event_msg\",\"payload\":{\"type\":\"agent_reasoning\",\"text\":\"padding\"}}\n", 24*1024)
	for i := range 8 {
		name := fmt.Sprintf("rollout-2026-11-21T10-00-0%d-00000000-0000-4000-8000-00000000000%d.jsonl", i, i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Also verify compressed archives without the id in the filename are skipped
	// in the fallback loop without error or decompression.
	zstName := "rollout-2026-11-21T10-00-09-00000000-0000-4000-8000-000000000009.jsonl.zst"
	if err := os.WriteFile(filepath.Join(dir, zstName), []byte("not-actually-zstd"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := compactionCandidatesByID("codex", "019d9999-unknown-0000-0000-000000000000")
	if len(got) != 0 {
		t.Fatalf("got %d candidates for unknown id, want 0", len(got))
	}
}

func TestCompactionDBProbeSkipsMissingSession(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	t.Setenv("DEJA_GOOSE_ROOT", root)
	db := filepath.Join(root, "sessions.db")
	sql := `create table sessions(id text primary key, description text, working_dir text, created_at text, updated_at text);
insert into sessions values('goose-present','demo','/work/demo','2026-11-21T10:00:00Z','2026-11-21T10:01:00Z');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 setup: %v %s", err, out)
	}
	if checked, found := compactionDBContainsID("goose", db, "goose-present"); !checked || !found {
		t.Fatalf("existing session probe = (%v, %v), want (true, true)", checked, found)
	}
	if checked, found := compactionDBContainsID("goose", db, "goose-missing"); !checked || found {
		t.Fatalf("missing session probe = (%v, %v), want (true, false)", checked, found)
	}
	if got := compactionCandidatesByID("goose", "goose-missing"); len(got) != 0 {
		t.Fatalf("compactionCandidatesByID returned %d candidates for missing goose id, want 0", len(got))
	}
}

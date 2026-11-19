package sources

import (
	"encoding/json"
	"errors"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func clineWriteBackStore(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", data)
	t.Setenv("DEJA_CLINE_ROOT", "")
	t.Setenv("CLINE_SESSION_DATA_DIR", "")
	if err := os.MkdirAll(filepath.Join(data, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWriteBackClineTakesTheWorkspaceFromTheHookLog(t *testing.T) {
	data := clineWriteBackStore(t)
	const id = "1791454692202_zb19s"
	if err := os.MkdirAll(filepath.Join(data, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	// An absolute path on this OS: cline logs C:\... on Windows.
	root := filepath.Join(t.TempDir(), "api")
	rootJSON := mustJSON(t, root)
	logLine := `{"sessionContext":{"rootSessionId":"` + id + `"},"workspaceRoots":[` + string(rootJSON) + `],"workspaceInfo":{"rootPath":` + string(rootJSON) + `},"hookName":"agent_start"}` + "\n"
	if err := os.WriteFile(filepath.Join(data, "logs", "hooks.jsonl"), []byte(logLine), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "sessions", id, id+".messages.json")
	s := writeBackSample("cline", id, path)
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	sidecars, appends, err := WriteBackExtras(s, f)
	if err != nil || len(sidecars) != 1 || len(appends) != 0 {
		t.Fatalf("extras: %d sidecars, %d appends, %v", len(sidecars), len(appends), err)
	}
	if want := filepath.Join(data, "sessions", id, id+".json"); sidecars[0].Path != want {
		t.Errorf("manifest at %s, want %s", sidecars[0].Path, want)
	}
	var man map[string]any
	if err = json.Unmarshal(sidecars[0].Data, &man); err != nil {
		t.Fatal(err)
	}
	// Cline 3.0.69 refuses a manifest with any of these empty.
	for _, k := range []string{"cwd", "workspace_root", "provider", "model", "session_id", "status", "source", "started_at"} {
		if v, _ := man[k].(string); v == "" {
			t.Errorf("manifest %s is empty", k)
		}
	}
	if man["cwd"] != root {
		t.Errorf("cwd %v, want the hook log's workspace", man["cwd"])
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(sidecars[0].Path, sidecars[0].Data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.Path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClineFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %d sessions, %v", len(ss), err)
	}
	assertTurns(t, ss[0].Messages)
	if ss[0].Project != projectName(root) {
		t.Errorf("project %q, want the workspace's", ss[0].Project)
	}
}

func TestWriteBackClineRefusesWithNoWorkspace(t *testing.T) {
	data := clineWriteBackStore(t)
	const id = "1791454692202_zb19s"
	s := writeBackSample("cline", id, filepath.Join(data, "sessions", id, id+".messages.json"))
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = WriteBackExtras(s, f)
	var r *WriteBackRefusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "directory the session ran in") {
		t.Fatalf("got %v, want a refusal naming the missing directory", err)
	}
}

func TestWriteBackClineRefusesAnExtensionTask(t *testing.T) {
	clineWriteBackStore(t)
	s := writeBackSample("cline", "cline-task-1", filepath.Join(t.TempDir(), "tasks", "1", "api_conversation_history.json"))
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "taskHistory.json") {
		t.Fatalf("got %v, want a refusal naming taskHistory.json", err)
	}
}

func TestWriteBackCodeBuddyReadsBack(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	t.Setenv("DEJA_CODEBUDDY_ROOTS", "")
	cwd := t.TempDir()
	folder := filepath.Join(cfg, "projects", strings.TrimPrefix(claudeEncodePath(cwd), "-"))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	// A sibling transcript names the directory the folder is for.
	sibling := `{"type":"message","role":"user","content":[{"type":"input_text","text":"x"}],"cwd":` + cbJSONString(cwd) + `}` + "\n"
	if err := os.WriteFile(filepath.Join(folder, "other.jsonl"), []byte(sibling), 0o600); err != nil {
		t.Fatal(err)
	}
	id := "01a11b04-6287-726a-a9da-5ed51724ccbf"
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	s := model.Session{Harness: "codebuddy", ID: id, Path: filepath.Join(folder, id+".jsonl"), Messages: []model.Message{
		{Role: "user", Text: "the cache evicts too early", Time: at},
		{Role: "command", Text: "$ go test ./cache", Time: at.Add(time.Second)},
		{Role: "assistant", Text: "raise the TTL", Time: at.Add(2 * time.Second)},
	}}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != s.Path || f.Turns != 2 {
		t.Fatalf("path %q turns %d", f.Path, f.Turns)
	}
	if err = os.WriteFile(f.Path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	back, err := ParseCodeBuddyFile(f.Path)
	if err != nil || len(back) != 1 {
		t.Fatalf("parse: %v %d", err, len(back))
	}
	var got []string
	for _, m := range back[0].Messages {
		if m.Role == "user" || m.Role == "assistant" {
			got = append(got, m.Role+":"+m.Text)
		}
	}
	want := []string{"user:the cache evicts too early", "assistant:raise the TTL"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("read back %q, want %q", got, want)
	}
	if back[0].ID != id {
		t.Fatalf("id %q", back[0].ID)
	}
	if dir := CodeBuddySessionDir(f.Path); dir != cwd {
		t.Fatalf("session dir %q, want %q", dir, cwd)
	}
}

func TestWriteBackCodeBuddyNeedsTheDirectory(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	t.Setenv("DEJA_CODEBUDDY_ROOTS", "")
	id := "01a11b04-0000-726a-a9da-5ed51724ccbf"
	s := model.Session{Harness: "codebuddy", ID: id,
		Path:     filepath.Join(cfg, "projects", "gone-nowhere-xyz", id+".jsonl"),
		Messages: []model.Message{{Role: "user", Text: "hi"}}}
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "directory the session ran in") {
		t.Fatalf("want a refusal naming the directory, got %v", err)
	}
}

func cbJSONString(s string) string {
	b, _ := jsonLines([]any{s})
	return strings.TrimSpace(string(b))
}

func TestWriteBackCodeWhaleRoundTrips(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	t.Setenv("CODEWHALE_HOME", t.TempDir())
	id := "05a3ec91-f208-4981-a1c2-bc29c7878404"
	path := filepath.Join(root, id+".json")
	s := model.Session{Harness: "codewhale", ID: id, Path: path, Messages: writeBackSampleTurns()}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != path || f.Turns != 3 {
		t.Fatalf("got %s with %d turns", f.Path, f.Turns)
	}
	if err = os.WriteFile(path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("parsed %#v", ss)
	}
	got := writeBackTurnsOf(ss)
	want := []model.Message{{Role: "user", Text: "why does the pool exhaust"}, {Role: "assistant", Text: "the cap is 10 & the load is 50 <ok>"}, {Role: "user", Text: "raise it"}}
	if len(got) != len(want) {
		t.Fatalf("turns = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("turn %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestWriteBackRefusalsNameWhatIsMissing(t *testing.T) {
	for harness, word := range map[string]string{
		"prime":       "directory",
		"antigravity": "protobuf",
		"amp":         "ampcode.com",
	} {
		s := model.Session{Harness: harness, ID: "x", Path: filepath.Join(t.TempDir(), "x.jsonl"), Messages: writeBackSampleTurns()}
		_, err := RenderWriteBack(s)
		var r *WriteBackRefusal
		if !errors.As(err, &r) {
			t.Fatalf("%s: err = %v, want a refusal", harness, err)
		}
		if !strings.Contains(r.Reason, word) {
			t.Errorf("%s: reason %q does not say %q", harness, r.Reason, word)
		}
	}
}

// writeBackTurnsOf is the user and assistant turns of ss, in order.
func writeBackTurnsOf(ss []model.Session) []model.Message {
	var out []model.Message
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == "user" || m.Role == "assistant" {
				out = append(out, model.Message{Role: m.Role, Text: m.Text})
			}
		}
	}
	return out
}

func writeBackSampleTurns() []model.Message {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return []model.Message{
		{Role: "user", Text: "why does the pool exhaust", Time: at},
		{Role: "command", Text: "$ go test ./pool", Time: at.Add(time.Second)},
		{Role: "assistant", Text: "the cap is 10 & the load is 50 <ok>", Time: at.Add(2 * time.Second)},
		{Role: "tool-output", Text: "FAIL", Time: at.Add(3 * time.Second)},
		{Role: "user", Text: "raise it", Time: at.Add(4 * time.Second)},
	}
}

func TestWriteBackCommandCodeRoundTrips(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	id := "f3e3619b-9a5a-4ad3-8f2b-6dedf60d1d4b"
	path := filepath.Join(root, "private-tmp-work-app", id+".jsonl")
	s := model.Session{Harness: "commandcode", ID: id, Path: path, Messages: writeBackSampleTurns()}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != path || f.Root != root || f.Turns != 3 {
		t.Fatalf("got %s under %s with %d turns", f.Path, f.Root, f.Turns)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCommandCodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("parsed %#v", ss)
	}
	got := writeBackTurnsOf(ss)
	want := []model.Message{{Role: "user", Text: "why does the pool exhaust"}, {Role: "assistant", Text: "the cap is 10 & the load is 50 <ok>"}, {Role: "user", Text: "raise it"}}
	if len(got) != len(want) {
		t.Fatalf("turns = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("turn %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestWriteBackCommandCodeRefusesAPathItDoesNotKnow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	s := model.Session{Harness: "commandcode", ID: "abc", Path: filepath.Join(root, "p", "abc.checkpoints.jsonl"), Messages: writeBackSampleTurns()}
	if _, err := RenderWriteBack(s); err == nil {
		t.Fatal("a checkpoints stream was taken for a transcript")
	}
}

func TestWriteBackContinueReadsBackWithTheListedWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	const id = "9bf46774-02b2-4c4b-919d-d668cd46795e"
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	list := `[{"sessionId":"` + id + `","title":"pool","dateCreated":"1791455494024","workspaceDirectory":"/work/api"}]`
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	s := writeBackSample("continue", id, filepath.Join(dir, id+".json"))
	assertTurns(t, writeAndRead(t, s, ParseContinueFile))
	b, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		WorkspaceDirectory string `json:"workspaceDirectory"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.WorkspaceDirectory != "/work/api" {
		t.Errorf("workspaceDirectory %q, want the list's", doc.WorkspaceDirectory)
	}
}

func TestWriteBackRooRefuses(t *testing.T) {
	s := model.Session{Harness: "roo", ID: "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e8a", Path: filepath.Join(t.TempDir(), "api_conversation_history.json"),
		Messages: []model.Message{{Role: "user", Text: "hi"}}}
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if CanWriteBack("roo") || !errors.As(err, &r) || !strings.Contains(r.Reason, "task history") {
		t.Fatalf("got %v, want a refusal naming Roo's task history", err)
	}
}

func TestCopilotWriteBackReadsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	t.Setenv("DEJA_COPILOT_ROOT", "")
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	id := "315781b9-c57e-4cb7-9f26-a5db1d7f93d7"
	s := model.Session{
		Harness: "copilot", ID: id,
		Path:    filepath.Join(home, "session-state", id, "events.jsonl"),
		Started: at,
		Messages: []model.Message{
			{Role: "user", Text: "why is the queue stuck", Time: at},
			{Role: RoleToolOutput, Text: "3 jobs waiting", Time: at.Add(time.Second)},
			{Role: "assistant", Text: "the worker is paused", Time: at.Add(2 * time.Second)},
		},
	}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.Data), `"copilotVersion"`) {
		t.Error("session.start without copilotVersion, which Copilot reads as corrupted")
	}
	if err = os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.Path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(f.Path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse back: %v %d", err, len(ss))
	}
	got := make([]string, 0, len(ss[0].Messages))
	for _, m := range ss[0].Messages {
		got = append(got, m.Role+":"+m.Text)
	}
	if want := "user:why is the queue stuck|assistant:the worker is paused"; strings.Join(got, "|") != want {
		t.Errorf("turns read back = %q, want %q", strings.Join(got, "|"), want)
	}
	if ss[0].ID != id {
		t.Errorf("id = %q", ss[0].ID)
	}
}

func TestCopilotWriteBackRefusesAPathOutsideTheStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	t.Setenv("DEJA_COPILOT_ROOT", "")
	id := "315781b9-c57e-4cb7-9f26-a5db1d7f93d7"
	s := model.Session{Harness: "copilot", ID: id, Path: filepath.Join(t.TempDir(), id, "events.jsonl"),
		Messages: []model.Message{{Role: "user", Text: "hi"}}}
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func writeBackTestTurns() []model.Message {
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	return []model.Message{
		{Role: "user", Text: "why does the pool exhaust", Time: at},
		{Role: "command", Text: "$ go test ./pool", Time: at.Add(time.Second)},
		{Role: "assistant", Text: "the cap is 10; raise it to 50", Time: at.Add(2 * time.Second)},
		{Role: "tool-output", Text: "ok", Time: at.Add(3 * time.Second)},
		{Role: "user", Text: "done, <b>thanks</b> & bye", Time: at.Add(4 * time.Second)},
	}
}

func checkWriteBackTurns(t *testing.T, got []model.Message) {
	t.Helper()
	var want []model.Message
	for _, m := range writeBackTestTurns() {
		if m.Role == "user" || m.Role == "assistant" {
			want = append(want, m)
		}
	}
	var conv []model.Message
	for _, m := range got {
		if m.Role == "user" || m.Role == "assistant" {
			conv = append(conv, m)
		}
	}
	if len(conv) != len(want) {
		t.Fatalf("read back %d turns, want %d: %#v", len(conv), len(want), got)
	}
	for i := range want {
		if conv[i].Role != want[i].Role || conv[i].Text != want[i].Text || !conv[i].Time.Equal(want[i].Time) {
			t.Errorf("turn %d = %#v, want %#v", i, conv[i], want[i])
		}
	}
}

func writeBackToDisk(t *testing.T, s model.Session) WriteBackFile {
	t.Helper()
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestWriteBackGeminiReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	work := filepath.Join(t.TempDir(), "app")
	project := filepath.Join(root, "tmp", "app")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".project_root"), []byte(work), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"session-2026-10-01T10-00-0b1c2d3e.jsonl", "session-2026-10-01T10-00-0b1c2d3e.json"} {
		s := model.Session{Harness: "gemini", ID: "0b1c2d3e-1111-4222-8333-444455556666",
			Path: filepath.Join(project, "chats", name), Messages: writeBackTestTurns()}
		f := writeBackToDisk(t, s)
		if f.Turns != 3 || f.Root != filepath.Join(root, "tmp") {
			t.Errorf("%s: turns %d root %s", name, f.Turns, f.Root)
		}
		ss, err := ParseGeminiFile(f.Path)
		if err != nil || len(ss) != 1 || ss[0].ID != s.ID {
			t.Fatalf("%s: read back %#v, %v", name, ss, err)
		}
		checkWriteBackTurns(t, ss[0].Messages)
	}
}

func TestWriteBackGeminiNeedsTheProjectDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	s := model.Session{Harness: "gemini", ID: "0b1c2d3e-1111-4222-8333-444455556666",
		Path: filepath.Join(root, "tmp", "app", "chats", "session-2026-10-01T10-00-0b1c2d3e.jsonl"), Messages: writeBackTestTurns()}
	if _, err := RenderWriteBack(s); err == nil {
		t.Fatal("wrote a session whose project directory nothing names")
	}
	// An older store keys the folder by the hash itself.
	s.Path = filepath.Join(root, "tmp", "c69b19a0c961b32c9ec990e03b714944df3803e77686cc8068f40f446c48f911", "chats", "session-2026-10-01T10-00-0b1c2d3e.json")
	if _, err := RenderWriteBack(s); err != nil {
		t.Fatalf("hash-named project refused: %v", err)
	}
}

// gkSession is a kept session as the index holds it: turns, two answers in a
// row where a tool call sat between them, and a last prompt with no reply.
func gkSession(harness, id, path string) model.Session {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return model.Session{
		Harness: harness, ID: id, Path: path, Started: at, Title: "pool question",
		Messages: []model.Message{
			{Role: "user", Text: "why does the pool exhaust", Time: at},
			{Role: "assistant", Text: "checking the pool", Time: at.Add(time.Second)},
			{Role: "command", Text: "$ go test ./pool", Time: at.Add(2 * time.Second)},
			{Role: "assistant", Text: "raise MaxOpenConns to 50", Time: at.Add(3 * time.Second)},
			{Role: "user", Text: "and the timeout?", Time: at.Add(4 * time.Second)},
		},
	}
}

// gkWrite renders s and writes the transcript and the files beside it.
func gkWrite(t *testing.T, s model.Session) (WriteBackFile, []WriteBackPart) {
	t.Helper()
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	sidecars, appends, err := WriteBackExtras(s, f)
	if err != nil || len(appends) != 0 {
		t.Fatalf("extras: %v, %d appends", err, len(appends))
	}
	for _, w := range append([]WriteBackPart{{Path: f.Path, Data: f.Data}}, sidecars...) {
		if writeBackRootOf(w.Path, []string{f.Root}) == "" {
			t.Fatalf("%s is outside %s", w.Path, f.Root)
		}
		if err := os.MkdirAll(filepath.Dir(w.Path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(w.Path, w.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f, sidecars
}

func gkTurns(ms []model.Message) string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Role+":"+m.Text)
	}
	return strings.Join(out, "|")
}

func TestWriteBackGrokReadsBackWithItsSummary(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	id := "01a11b02-2491-7ae0-ba1b-48dd82cc8841"
	path := filepath.Join(root, "sessions", "%2Fwork%2Fapi", id, "updates.jsonl")
	f, side := gkWrite(t, gkSession("grok", id, path))
	ss, err := ParseGrokFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %#v, %v", ss, err)
	}
	want := "user:why does the pool exhaust|assistant:checking the pool|assistant:raise MaxOpenConns to 50|user:and the timeout?"
	if got := gkTurns(ss[0].Messages); got != want {
		t.Errorf("turns:\n%s\nwant:\n%s", got, want)
	}
	if ss[0].Project != projectName("/work/api") {
		t.Errorf("project %q", ss[0].Project)
	}
	// grok 1.0.41 answers "Session does not exist" unless these are present.
	var doc map[string]any
	if len(side) != 1 || json.Unmarshal(side[0].Data, &doc) != nil {
		t.Fatalf("summary sidecar: %#v", side)
	}
	for _, k := range []string{"info", "session_summary", "created_at", "updated_at", "num_messages", "current_model_id", "chat_format_version"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("summary.json lacks %s", k)
		}
	}
}

func TestWriteBackGrokRefusesTheGrokDevDatabase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	s := gkSession("grok", "abc", filepath.Join(root, "grok.db"))
	var r *WriteBackRefusal
	if _, err := RenderWriteBack(s); !errors.As(err, &r) || !strings.Contains(r.Reason, "grok.db") {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteBackKiroCLIAlternatesAndReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIRO_ROOT", root)
	t.Setenv("DEJA_KIRO_DB", filepath.Join(root, "none.sqlite3"))
	id := "3b0e2c6a-1111-4222-8333-444455556666"
	f, side := gkWrite(t, gkSession("kiro", id, filepath.Join(root, "cli", id+".jsonl")))
	ss, err := ParseKiroCLIFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %#v, %v", ss, err)
	}
	// kiro-cli refuses two answers in a row and a history that ends on a prompt.
	want := "user:why does the pool exhaust|assistant:checking the pool\n\nraise MaxOpenConns to 50"
	if got := gkTurns(ss[0].Messages); got != want {
		t.Errorf("turns:\n%q\nwant:\n%q", got, want)
	}
	if len(side) != 1 || filepath.Base(side[0].Path) != id+".json" {
		t.Fatalf("header sidecar: %#v", side)
	}
}

func TestWriteBackKiroV3ReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIRO_ROOT", filepath.Join(root, "sessions"))
	t.Setenv("DEJA_KIRO_DB", filepath.Join(root, "none.sqlite3"))
	id := "sess_011ff21a-4e1f-4f50-b3ca-4f40f34a81c4"
	if err := os.MkdirAll(filepath.Join(root, "session-index"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"op":"add","sessionPath":"5faa503d2a4212ce/` + id + `","at":1791455425264}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "session-index", "5faa503d2a4212ce.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	f, side := gkWrite(t, gkSession("kiro", id, filepath.Join(root, "sessions", "5faa503d2a4212ce", id, "messages.jsonl")))
	ss, err := ParseKiroIDEFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %#v, %v", ss, err)
	}
	want := "user:why does the pool exhaust|assistant:checking the pool\n\nraise MaxOpenConns to 50"
	if got := gkTurns(ss[0].Messages); got != want {
		t.Errorf("turns:\n%q\nwant:\n%q", got, want)
	}
	if len(side) != 1 || filepath.Base(side[0].Path) != "session.json" {
		t.Fatalf("session.json sidecar: %#v", side)
	}
}

func TestWriteBackKiroRefusesTheIDEAndTheDatabase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIRO_ROOT", filepath.Join(root, "sessions"))
	db := filepath.Join(root, "data.sqlite3")
	t.Setenv("DEJA_KIRO_DB", db)
	for _, s := range []model.Session{
		gkSession("kiro", "conv-1", db),
		gkSession("kiro", "sess_aa", filepath.Join(root, "sessions", "ws", "sess_aa", "messages.jsonl")),
	} {
		var r *WriteBackRefusal
		if _, err := RenderWriteBack(s); !errors.As(err, &r) || r.Reason == "" {
			t.Errorf("%s: err = %v", s.Path, err)
		}
	}
}

func TestWriteBackCursorSaysWhatIsMissing(t *testing.T) {
	var r *WriteBackRefusal
	_, err := RenderWriteBack(gkSession("cursor", "c1", "/x/agent-transcripts/c1.jsonl"))
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "store.db") {
		t.Fatalf("err = %v", err)
	}
}

func kimiWriteBackSession(home string) model.Session {
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	id := "session_1b2e4e28-4ab8-40cd-b705-b22159519311"
	return model.Session{
		Harness: "kimi", ID: id, Title: "why is the cache cold",
		Path:    filepath.Join(home, "sessions", "wd_demo_ae41f942e4c2", id, "agents", "main", "wire.jsonl"),
		Started: at, Updated: at.Add(time.Minute),
		Messages: []model.Message{
			{Role: "user", Text: "why is the cache cold", Time: at},
			{Role: RoleCommand, Text: "$ redis-cli info", Time: at.Add(time.Second)},
			{Role: "assistant", Text: "the TTL is 0", Time: at.Add(2 * time.Second)},
			{Role: "user", Text: "set it to 60", Time: at.Add(3 * time.Second)},
		},
	}
}

func TestKimiWriteBackReadsBackAndListsTheSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	t.Setenv("DEJA_KIMI_ROOT", "")
	work := filepath.Join(t.TempDir(), "demo")
	if err := os.WriteFile(filepath.Join(home, "workspaces.json"),
		[]byte(`{"version":1,"workspaces":{"wd_demo_ae41f942e4c2":{"root":`+jsonQuote(work)+`,"name":"demo"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := kimiWriteBackSession(home)
	// Kimi's own delete leaves a tombstone after the entry.
	index := `{"sessionId":"` + s.ID + `","sessionDir":"x","workDir":"y"}` + "\n" + `{"sessionId":"` + s.ID + `","deleted":true}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	sidecars, appends, err := WriteBackExtras(s, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(sidecars) != 1 || filepath.Base(sidecars[0].Path) != "state.json" || !strings.Contains(string(sidecars[0].Data), jsonQuote(work)) {
		t.Fatalf("state.json sidecar = %+v", sidecars)
	}
	if len(appends) != 1 || !strings.Contains(string(appends[0].Data), `"workDir":`+jsonQuote(work)) {
		t.Fatalf("index entry after the tombstone = %+v", appends)
	}
	for _, p := range append(sidecars, WriteBackPart{Path: f.Path, Data: f.Data}) {
		if err = os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(p.Path, p.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := KimiSessionDir(f.Path); got != work {
		t.Errorf("resume directory = %q, want %q", got, work)
	}
	ss, err := ParseKimiFile(f.Path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse back: %v %d", err, len(ss))
	}
	got := make([]string, 0, len(ss[0].Messages))
	for _, m := range ss[0].Messages {
		got = append(got, m.Role+":"+m.Text)
	}
	want := "user:why is the cache cold|assistant:the TTL is 0|user:set it to 60"
	if strings.Join(got, "|") != want {
		t.Errorf("turns read back = %q, want %q", strings.Join(got, "|"), want)
	}
	if ss[0].ID != s.ID {
		t.Errorf("id = %q", ss[0].ID)
	}

	// A live entry needs nothing appended.
	live := `{"sessionId":"` + s.ID + `","sessionDir":"x","workDir":"y"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, appends, _ := WriteBackExtras(s, f); len(appends) != 0 {
		t.Errorf("appended to an index that already lists the session: %+v", appends)
	}
}

func TestKimiWriteBackRefusesWithoutTheDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	t.Setenv("DEJA_KIMI_ROOT", "")
	_, err := RenderWriteBack(kimiWriteBackSession(home))
	var r *WriteBackRefusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "directory the session ran in") {
		t.Fatalf("err = %v, want a refusal naming the missing directory", err)
	}
}

func jsonQuote(s string) string {
	b, _ := jsonLines([]any{s})
	return strings.TrimSpace(string(b))
}

func TestWriteBackOpenClawReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	const id = "87bed90b-a18b-4d58-90a2-8956a280dbde"
	s := writeBackSample("openclaw", id, filepath.Join(root, "main", "sessions", id+".jsonl"))
	got := writeAndRead(t, s, ParseOpenClawFile)
	assertTurns(t, got)
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if head, _, _ := strings.Cut(string(f.Data), "\n"); strings.Contains(head, `"cwd"`) || !strings.Contains(head, `"type":"session"`) {
		t.Errorf("header %s: want a session header with no cwd", head)
	}
}

func TestWriteBackOpenClawRefusesTheDatabaseAndArchives(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	for path, want := range map[string]string{
		filepath.Join(root, "main", "agent", "openclaw-agent.sqlite"):                    "database",
		filepath.Join(root, "main", "sessions", "s1.jsonl.deleted.2026-10-01T10-00-00Z"): "archived",
	} {
		s := writeBackSample("openclaw", "s1", path)
		_, err := RenderWriteBack(s)
		var r *WriteBackRefusal
		if !errors.As(err, &r) || !strings.Contains(r.Reason, want) {
			t.Errorf("%s: got %v, want a reason naming %q", filepath.Base(path), err, want)
		}
	}
}

func TestWriteBackPiFamilyReadsBack(t *testing.T) {
	at := time.Date(2026, 10, 8, 10, 13, 56, 0, time.UTC)
	for _, tc := range []struct {
		harness, env string
		parse        func(string) ([]model.Session, error)
	}{
		{"pi", "DEJA_PI_ROOT", ParsePiFile},
		{"omp", "DEJA_OMP_ROOT", ParseOmpFile},
		{"gjc", "DEJA_GJC_ROOT", ParseGjcFile},
		{"kimchi", "DEJA_KIMCHI_ROOT", ParseKimchiFile},
		{"senpi", "DEJA_SENPI_ROOT", ParseSenpiFile},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv(tc.env, root)
			project := filepath.Join(root, "--work-app--")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.harness == "gjc" {
				scope := `{"schemaVersion":1,"canonicalPath":"/work/app"}`
				if err := os.WriteFile(filepath.Join(project, ".gjc-managed-session-scope.v2.json"), []byte(scope), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			id := "01a11b01-5c09-747e-ae33-289cbad33c6c"
			s := model.Session{
				ID: id, Harness: tc.harness, Project: "app", Started: at,
				Path: filepath.Join(project, "2026-10-08T10-13-56-362Z_"+id+".jsonl"),
				Messages: []model.Message{
					{Role: "user", Text: "the pool exhausts on deploy", Time: at},
					{Role: "command", Text: "$ go test ./...", Time: at.Add(time.Second)},
					{Role: "tool-output", Text: "FAIL pool", Time: at.Add(2 * time.Second)},
					{Role: "assistant", Text: "raise MaxOpenConns to 50", Time: at.Add(3 * time.Second)},
					{Role: "user", Text: "done?", Time: at.Add(4 * time.Second)},
				},
			}
			f, err := RenderWriteBack(s)
			if err != nil {
				t.Fatal(err)
			}
			if f.Path != s.Path || f.Root != root || f.Turns != 3 {
				t.Fatalf("file = %q in %q with %d turns", f.Path, f.Root, f.Turns)
			}
			if err = os.WriteFile(f.Path, f.Data, 0o600); err != nil {
				t.Fatal(err)
			}
			ss, err := tc.parse(f.Path)
			if err != nil || len(ss) != 1 {
				t.Fatalf("parse: %v, %d sessions", err, len(ss))
			}
			got := ss[0]
			if got.ID != id {
				t.Errorf("id = %q", got.ID)
			}
			var turns []string
			for _, m := range got.Messages {
				turns = append(turns, m.Role+": "+m.Text)
			}
			want := "user: the pool exhausts on deploy|assistant: raise MaxOpenConns to 50|user: done?"
			if strings.Join(turns, "|") != want {
				t.Errorf("turns = %q", strings.Join(turns, "|"))
			}
			if tc.harness == "gjc" && !strings.Contains(string(f.Data), `"cwd":"/work/app"`) {
				t.Errorf("gjc header lost the scope file's directory: %s", f.Data)
			}
		})
	}
}

func TestWriteBackGjcRefusesWithoutItsScopeFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GJC_ROOT", root)
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		ID: "01a11b05-9d97-7010-a005-4a6f511b00fb", Harness: "gjc",
		Path:     filepath.Join(root, "v2-abc", "2026-10-08T10-18-35-287Z_01a11b05-9d97-7010-a005-4a6f511b00fb.jsonl"),
		Messages: []model.Message{{Role: "user", Text: "hi", Time: at}},
	}
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "scope file") {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteBackQwenReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	id := "4c7a32fa-6dad-4b2e-b23f-0adcfdadd364"
	s := model.Session{Harness: "qwen", ID: id,
		Path: filepath.Join(root, "projects", "-work-app", "chats", id+".jsonl"), Messages: writeBackTestTurns()}
	f := writeBackToDisk(t, s)
	if f.Turns != 3 {
		t.Errorf("turns %d", f.Turns)
	}
	ss, err := ParseQwenFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %#v, %v", ss, err)
	}
	checkWriteBackTurns(t, ss[0].Messages)
}

func TestWriteBackQwenRefusesASubagentLog(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	s := model.Session{Harness: "qwen", ID: "agent-1-parent", Kind: "subagent",
		Path: filepath.Join(root, "projects", "-work-app", "subagents", "parent", "agent-1.jsonl"), Messages: writeBackTestTurns()}
	if _, err := RenderWriteBack(s); err == nil {
		t.Fatal("wrote a sub-agent log back")
	}
}

func TestWriteBackRefusesWithItsReason(t *testing.T) {
	for harness, want := range map[string]string{
		"reasonix": "system prompt",
		"zcode":    "CLI database",
		"trae":     "TRAE CLI",
		"muse":     "Muse",
	} {
		if CanWriteBack(harness) {
			t.Errorf("%s: written back, want a refusal", harness)
			continue
		}
		s := model.Session{Harness: harness, ID: "s1", Path: filepath.Join(t.TempDir(), "s1.jsonl"),
			Messages: []model.Message{{Role: "user", Text: "hi"}}}
		_, err := RenderWriteBack(s)
		var r *WriteBackRefusal
		if !errors.As(err, &r) || !strings.Contains(r.Reason, want) {
			t.Errorf("%s: got %v, want a reason naming %q", harness, err, want)
		}
	}
}

func writeBackSample(harness, id, path string) model.Session {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return model.Session{
		Harness: harness, ID: id, Path: path, Started: at,
		Messages: []model.Message{
			{Role: "user", Text: "why does the pool exhaust <under load> & retry", Time: at},
			{Role: "command", Text: "$ go test ./internal/pool", Time: at.Add(time.Second)},
			{Role: RoleToolOutput, Text: "FAIL pool exhausted", Time: at.Add(2 * time.Second)},
			{Role: "assistant", Text: "raise MaxOpenConns to 50", Time: at.Add(3 * time.Second)},
			{Role: "user", Text: "done?"},
		},
	}
}

// writeAndRead renders s, writes it where it says, and reads it back with the
// harness's own parser.
func writeAndRead(t *testing.T, s model.Session, parse func(string) ([]model.Session, error)) []model.Message {
	t.Helper()
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.Path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := parse(f.Path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("read back %d sessions, %v", len(ss), err)
	}
	if ss[0].ID != s.ID {
		t.Errorf("read back id %q, want %q", ss[0].ID, s.ID)
	}
	return ss[0].Messages
}

func assertTurns(t *testing.T, got []model.Message) {
	t.Helper()
	want := []string{"user:why does the pool exhaust <under load> & retry", "assistant:raise MaxOpenConns to 50", "user:done?"}
	have := make([]string, 0, len(got))
	for _, m := range got {
		have = append(have, m.Role+":"+m.Text)
	}
	if strings.Join(have, "|") != strings.Join(want, "|") {
		t.Errorf("turns read back:\n%s\nwant:\n%s", strings.Join(have, "\n"), strings.Join(want, "\n"))
	}
}

func TestWriteBackClaudeReadsBackAsTheSameTurns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	id := "5a1c0de0-1111-4222-8333-444455556666"
	s := writeBackSample("claude", id, filepath.Join(root, "-work-api", id+".jsonl"))
	assertTurns(t, writeAndRead(t, s, ParseClaudeFile))
}

func TestWriteBackCodexReadsBackAsTheSameTurns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("CODEX_HOME", root)
	id := "01a0f6e6-8cd3-72c2-84ff-367e6191e458"
	path := filepath.Join(root, "sessions", "2026", "10", "01", "rollout-2026-10-01T12-00-00-"+id+".jsonl")
	assertTurns(t, writeAndRead(t, codexWriteBackSample(id, path), ParseCodexRollout))

	// A rollout codex compressed goes back as plain JSONL beside it.
	id2 := "01a0f6e6-8cd3-72c2-84ff-367e6191e459"
	zst := filepath.Join(root, "sessions", "2026", "10", "01", "rollout-2026-10-01T12-00-00-"+id2+".jsonl.zst")
	f, err := RenderWriteBack(codexWriteBackSample(id2, zst))
	if err != nil || f.Path != strings.TrimSuffix(zst, ".zst") {
		t.Fatalf("compressed rollout: %q, %v", f.Path, err)
	}
}

// codexWriteBackSample is a codex session whose own edits name the directory
// it ran in, which is all a write-back has once codex's thread row is gone.
func codexWriteBackSample(id, path string) model.Session {
	s := writeBackSample("codex", id, path)
	s.Project = "work/api"
	s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: "/srv/work/api/pool.go"})
	return s
}

func TestWriteBackCodexTakesTheDirectoryTheSessionRecorded(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("CODEX_HOME", root)
	id := "01a0f6e6-8cd3-72c2-84ff-367e6191e460"
	path := filepath.Join(root, "sessions", "2026", "10", "01", "rollout-2026-10-01T12-00-00-"+id+".jsonl")
	meta := func(s model.Session) string {
		t.Helper()
		f, err := RenderWriteBack(s)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), "rollout.jsonl")
		if err := os.WriteFile(p, f.Data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, cwd, _, _ := codexRolloutHead(p)
		return cwd
	}

	// No thread row: the paths the session itself worked on, by majority,
	// each at its nearest parent with the session's project name.
	s := writeBackSample("codex", id, path)
	s.Project = "work/api"
	s.Messages = append(s.Messages,
		model.Message{Role: RoleFiles, Text: "/srv/work/api/internal/pool/pool.go\n/srv/work/api/go.mod"},
		model.Message{Role: RoleEdit, Text: "/srv/work/api/cmd/main.go\n-old"},
		model.Message{Role: RoleWrote, Text: "/tmp/scratch/work/api/x.go\nabc"},
	)
	if got := meta(s); got != "/srv/work/api" {
		t.Errorf("cwd from the session's files = %q, want /srv/work/api", got)
	}

	// Nothing names it anywhere: refused, not written with an empty cwd.
	bare := writeBackSample("codex", id, path)
	bare.Project = "work/api"
	if _, err := RenderWriteBack(bare); err == nil || !strings.Contains(err.Error(), "directory the session ran in") {
		t.Errorf("no cwd anywhere: %v, want a refusal naming the directory", err)
	}

	if !SQLite3Available() {
		t.Skip("sqlite3 not available")
	}
	db := filepath.Join(root, "state_5.sqlite")
	sql := func(q string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, q).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v %s", err, out)
		}
	}
	sql("create table threads(id text, cwd text); insert into threads values('other-1','/home/a/work/api'),('other-2','/home/a/work/web');")
	// Another thread of the same project, when one directory has its name.
	if got := meta(bare); got != "/home/a/work/api" {
		t.Errorf("cwd from codex's other threads = %q, want /home/a/work/api", got)
	}
	// The thread's own row wins over everything else.
	sql("insert into threads values('" + id + "','/home/a/own/api');")
	if got := meta(s); got != "/home/a/own/api" {
		t.Errorf("cwd from the thread row = %q, want /home/a/own/api", got)
	}
	// Two directories share the project's name: no guess.
	sql("delete from threads where id='" + id + "'; insert into threads values('other-3','/opt/work/api');")
	if _, err := RenderWriteBack(bare); err == nil {
		t.Error("two directories with the project's name: picked one")
	}
}

func TestWriteBackStaysInTheStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "projects"))
	id := "5a1c0de0-1111-4222-8333-444455556666"
	for _, p := range []string{
		filepath.Join(root, "elsewhere", id+".jsonl"),
		filepath.Join(root, "projects", "..", "x", id+".jsonl"),
		id + ".jsonl",
	} {
		_, err := RenderWriteBack(writeBackSample("claude", id, p))
		var r *WriteBackRefusal
		if !errors.As(err, &r) {
			t.Errorf("%s: wrote outside the store: %v", p, err)
		}
	}
}

func TestWriteBackRefusesWhatItCannotRebuild(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	id := "5a1c0de0-1111-4222-8333-444455556666"
	s := writeBackSample("claude", id, filepath.Join(root, "-w", id+".jsonl"))
	s.Messages = []model.Message{{Role: "command", Text: "$ ls"}}
	if _, err := RenderWriteBack(s); err == nil || !strings.Contains(err.Error(), "no user or assistant turn") {
		t.Errorf("tool records alone: %v", err)
	}
	for _, h := range []string{"opencode", "goose", "hermes", "crush", "kilocode"} {
		_, err := RenderWriteBack(model.Session{Harness: h, ID: "x"})
		if err == nil || !strings.Contains(err.Error(), "database") && !strings.Contains(err.Error(), ".db") {
			t.Errorf("%s: %v, want the database named", h, err)
		}
	}
}

package sources

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

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
	out := `[{
		"id":"s1","directory":"proj","time_created":"2026-01-02T03:00:00Z","time_updated":"2026-01-02T03:01:00Z",
		"role":"user","text":"fake sqlite needle","pt":"2026-01-02T03:00:30Z","mt":"2026-01-02T03:00:00Z"},
		{"id":"","text":"skipped"},
		{"id":"s1","directory":"proj","role":"assistant","text":"","pt":1767337200000}]`
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$*\" in *count*) printf '1|1\\n' ;; *badjson*) printf '{' ;; *scalar*) printf '{}' ;; *) printf '%s' '"+out+"' ;; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	db := OpencodeDB()
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !SQLite3Available() {
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
	for _, content := range []string{"not json", `{"messages":[]}`, `{"sessionId":"empty","messages":[]}`} {
		p := filepath.Join(chats, strings.ReplaceAll(content[:3], " ", "_")+".json")
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
	if len(ss) != 1 || ss[0].Project != "gem-proj" || ss[0].Messages[0].Text != "a\nb" {
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
	if ClaudeFileWanted(sub) {
		t.Fatal("subagent should be skipped by default")
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if !ClaudeFileWanted(sub) {
		t.Fatal("subagent include override failed")
	}
	if got := itoa(0); got != "0" {
		t.Fatalf("itoa(0)=%q", got)
	}

	t.Setenv("DEJA_CURSOR_ROOT", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	xdgCursor := filepath.Join(tmp, "xdg", "Cursor", "User")
	if err := os.MkdirAll(xdgCursor, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CursorUserRoot(); runtime.GOOS != "darwin" && got != xdgCursor {
		t.Fatalf("CursorUserRoot xdg=%q want %q", got, xdgCursor)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "missing-xdg"))
	if got := CursorUserRoot(); !strings.Contains(got, filepath.Join(".config", "Cursor", "User")) && runtime.GOOS != "darwin" {
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
	cmd := exec.Command("sqlite3", db, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss, err := ParseCursorDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("cursor fallback ss=%#v err=%v", ss, err)
	}
	if ss[0].ID != "fallback" || len(ss[0].Messages[0].Text) != 64*1024 || ss[0].Messages[0].Time != ss[0].Started {
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
	tooLong := filepath.Join(tmp, "too-long.md")
	if err := os.WriteFile(tooLong, []byte("# aider chat started at 2026-01-01 00:00:00\n"+strings.Repeat("x", 9*1024*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAiderFile(tooLong); err == nil {
		t.Fatal("aider scanner error branch not hit")
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

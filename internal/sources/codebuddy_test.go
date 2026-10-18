package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func codeBuddyEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"CODEBUDDY_CONFIG_DIR", "WORKBUDDY_CONFIG_DIR", "DEJA_CODEBUDDY_ROOTS", "DEJA_INCLUDE_SUBAGENTS"} {
		t.Setenv(k, "")
	}
	return home
}

func writeCodeBuddyFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodeBuddyRootsFollowConfigDir(t *testing.T) {
	home := codeBuddyEnv(t)
	if got, want := CodeBuddyRoots(), []string{filepath.Join(home, ".codebuddy", "projects")}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("default roots = %v, want %v", got, want)
	}

	cfg := filepath.Join(home, "work-profile")
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	if got := CodeBuddyRoots(); len(got) != 1 || got[0] != filepath.Join(cfg, "projects") {
		t.Fatalf("CODEBUDDY_CONFIG_DIR roots = %v", got)
	}
	if got := CodeBuddyConfigDir(); got != cfg {
		t.Fatalf("CodeBuddyConfigDir = %q, want %q", got, cfg)
	}

	// WorkBuddy writes the same store under its own directory; it is read once
	// it exists.
	wb := filepath.Join(home, ".workbuddy", "projects")
	if err := os.MkdirAll(wb, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CodeBuddyRoots(); len(got) != 2 || got[1] != wb {
		t.Fatalf("roots with WorkBuddy = %v", got)
	}
	wbCfg := filepath.Join(home, "wb")
	t.Setenv("WORKBUDDY_CONFIG_DIR", wbCfg)
	if err := os.MkdirAll(filepath.Join(wbCfg, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CodeBuddyRoots(); len(got) != 2 || got[1] != filepath.Join(wbCfg, "projects") {
		t.Fatalf("WORKBUDDY_CONFIG_DIR roots = %v", got)
	}

	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	t.Setenv("DEJA_CODEBUDDY_ROOTS", a+string(os.PathListSeparator)+b)
	if got := CodeBuddyRoots(); strings.Join(got, "|") != a+"|"+b {
		t.Fatalf("DEJA_CODEBUDDY_ROOTS roots = %v", got)
	}
}

func TestCodeBuddySessionFiles(t *testing.T) {
	home := codeBuddyEnv(t)
	root := filepath.Join(home, ".codebuddy", "projects")
	main := filepath.Join(root, "repo", "s1.jsonl")
	sub := filepath.Join(root, "repo", "s1", "subagents", "agent-a1.jsonl")
	writeCodeBuddyFile(t, main, "{}\n")
	writeCodeBuddyFile(t, sub, "{}\n")
	writeCodeBuddyFile(t, filepath.Join(root, "repo", "s1.meta.json"), "{}")
	writeCodeBuddyFile(t, filepath.Join(root, "repo", "memory", "MEMORY.md"), "x")

	if got := CodeBuddySessionFiles(); len(got) != 2 {
		t.Fatalf("session files = %v, want the session and its sub-agent", got)
	}
	if !isCodeBuddySession(main) || isCodeBuddySession(sub) {
		t.Fatalf("isCodeBuddySession main=%v sub=%v", isCodeBuddySession(main), isCodeBuddySession(sub))
	}
	writeCodeBuddyFile(t, filepath.Join(root, "repo", "stray.txt"), "x")
	if got := CodeBuddySidecarFiles(); len(got) != 2 {
		t.Fatalf("sidecars = %v", got)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if got := CodeBuddySessionFiles(); len(got) != 1 || got[0] != main {
		t.Fatalf("with sub-agents off = %v, want [%s]", got, main)
	}
}

func TestParseCodeBuddyFixture(t *testing.T) {
	codeBuddyEnv(t)
	path := filepath.Join("..", "..", "fixtures", "registry", "codebuddy", "projects", "workspace-registry-demo", "registry-codebuddy.jsonl")
	sessions, err := ParseCodeBuddyFile(path)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ParseCodeBuddyFile = %d sessions, %v", len(sessions), err)
	}
	s := sessions[0]
	if s.Harness != "codebuddy" || s.ID != "registry-codebuddy" || s.Project != "workspace/registry-demo" {
		t.Fatalf("identity = %q %q %q", s.Harness, s.ID, s.Project)
	}
	if s.Title != "Fix the registry demo build" {
		t.Fatalf("title = %q", s.Title)
	}
	if want := time.UnixMilli(1780000000000); !s.Started.Equal(want) {
		t.Fatalf("started = %v, want %v", s.Started, want)
	}
	var got []string
	for _, m := range s.Messages {
		got = append(got, m.Role+": "+m.Text)
	}
	want := []string{
		"user: why does the registry demo fail to build?",
		"assistant: The build tag is missing from main.go; checking with go build.",
		RoleCommand + ": $ go build ./...",
		RoleToolOutput + ": main.go:3:1: build constraints exclude all Go files",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The client's own plumbing shares role user with the person: skipRun records,
// the slash-command and reminder envelopes, compaction and continuation turns
// and teammate messages are all dropped, the way CodeBuddy's own
// isRealUserMessageItem drops them.
func TestParseCodeBuddyDropsPlumbing(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "repo", "s2.jsonl")
	writeCodeBuddyFile(t, path, strings.Join([]string{
		`{"type":"message","role":"user","timestamp":1780000000000,"cwd":"/src/repo","content":[{"type":"input_text","text":"<local-command-stdout>ok</local-command-stdout>"}]}`,
		`{"type":"message","role":"user","timestamp":1780000000001,"content":[{"type":"input_text","text":"<teammate-message from=\"a\">hi</teammate-message>"}]}`,
		`{"type":"message","role":"user","timestamp":1780000000002,"content":[{"type":"input_text","text":"continue"}],"providerData":{"isMeta":true}}`,
		`{"type":"message","role":"user","timestamp":1780000000003,"content":[{"type":"input_text","text":"summary of earlier"}],"providerData":{"isCompactInternal":true}}`,
		`{"type":"message","role":"user","timestamp":1780000000004,"content":[{"type":"input_text","text":"from a teammate"}],"providerData":{"teammateMessage":{"from":"b"}}}`,
		`{"type":"message","role":"user","timestamp":1780000000005,"content":[{"type":"input_text","text":"/review"}],"providerData":{"skipRun":true}}`,
		`{"type":"reasoning","timestamp":1780000000006,"content":[{"type":"input_text","text":"thinking"}]}`,
		`{"type":"summary","timestamp":1780000000007,"summary":"compacted"}`,
		`{"type":"message","role":"user","timestamp":1780000000008,"content":"rename the flag"}`,
		`{"type":"function_call","timestamp":1780000000009,"callId":"c1","name":"Edit","arguments":"{\"file_path\":\"/src/repo/main.go\",\"old_string\":\"oldFlag\",\"new_string\":\"newFlag\"}"}`,
		`{"type":"ai-title","aiTitle":"Generated","timestamp":1780000000010}`,
		`{"type":"custom-title","customTitle":"Named by hand","timestamp":1780000000011}`,
		`not json`,
	}, "\n")+"\n")
	sessions, err := ParseCodeBuddyFile(path)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ParseCodeBuddyFile = %d sessions, %v", len(sessions), err)
	}
	s := sessions[0]
	var user []string
	roles := map[string]bool{}
	for _, m := range s.Messages {
		roles[m.Role] = true
		if m.Role == "user" {
			user = append(user, m.Text)
		}
	}
	if strings.Join(user, "|") != "rename the flag" {
		t.Fatalf("user turns = %q", user)
	}
	if !roles[RoleFiles] || !roles[RoleEdit] {
		t.Fatalf("Edit call left no file or edit record: %v", roles)
	}
	if s.Title != "Named by hand" {
		t.Fatalf("title = %q, want the custom title over the generated one", s.Title)
	}
	if s.Project != "src/repo" {
		t.Fatalf("project = %q", s.Project)
	}
}

func TestParseCodeBuddySubagent(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "repo", "parent-1", "subagents", "agent-x.jsonl")
	writeCodeBuddyFile(t, path, `{"type":"message","role":"user","timestamp":1780000000000,"content":[{"type":"input_text","text":"look for callers"}]}`+"\n")
	sessions, err := ParseCodeBuddyFile(path)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ParseCodeBuddyFile = %d sessions, %v", len(sessions), err)
	}
	if s := sessions[0]; s.Kind != "subagent" || s.Parent != "parent-1" || s.ID != "agent-x" {
		t.Fatalf("sub-agent = kind %q parent %q id %q", s.Kind, s.Parent, s.ID)
	}
}

// WorkBuddy's desktop app records each prompt behind its context reminders, in
// one item that ends with <user_query>. The turn is the query (#4734); a
// reminder on its own is still plumbing, and a bare prompt is unchanged.
func TestParseCodeBuddyUserQueryWrapper(t *testing.T) {
	home := codeBuddyEnv(t)
	path := filepath.Join(home, ".codebuddy", "projects", "repo", "s3.jsonl")
	wrapped := `<system-reminder data-role=\"user-context\">\n<user_info>\nOS Version: darwin\n</user_info>\n</system-reminder>\n<system-reminder data-role=\"additional-data\">\n<current_time>\nMonday\n</current_time>\n</system-reminder>\n<user_query>What backoff did we pick for the payments retry?</user_query>`
	writeCodeBuddyFile(t, path, strings.Join([]string{
		`{"type":"message","role":"user","timestamp":1780000000000,"cwd":"/src/repo","content":[{"type":"input_text","text":"` + wrapped + `"}]}`,
		`{"type":"message","role":"assistant","timestamp":1780000000001,"content":[{"type":"output_text","text":"Exponential, capped at 30s."}]}`,
		`{"type":"message","role":"user","timestamp":1780000000002,"content":[{"type":"input_text","text":"<system-reminder>\nplan mode is on\n</system-reminder>"}]}`,
		`{"type":"message","role":"user","timestamp":1780000000003,"content":"<system-reminder>x</system-reminder>\n<user_query>first</user_query>\n<user_query>and add jitter</user_query>"}`,
		`{"type":"message","role":"user","timestamp":1780000000004,"content":"why does <user_query> show up in the log"}`,
	}, "\n")+"\n")
	sessions, err := ParseCodeBuddyFile(path)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ParseCodeBuddyFile = %d sessions, %v", len(sessions), err)
	}
	var user []string
	for _, m := range sessions[0].Messages {
		if m.Role == "user" {
			user = append(user, m.Text)
		}
	}
	want := "What backoff did we pick for the payments retry?|and add jitter|why does <user_query> show up in the log"
	if got := strings.Join(user, "|"); got != want {
		t.Fatalf("user turns = %q, want %q", got, want)
	}
}

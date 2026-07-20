package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func hermeticEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(tmp, "warmup-guard"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "aider"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "antigravity"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	t.Setenv("DEJA_COPILOT_ROOT", filepath.Join(tmp, "copilot"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GEMINI_CLI_HOME", "")
	t.Setenv("CURSOR_CONFIG_DIR", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("NO_COLOR", "1")
	return tmp
}

func TestInstallMCPJSONTargetsAndErrors(t *testing.T) {
	hermeticEnv(t)
	if got := homeDir(); got == "" {
		t.Fatal("homeDir empty")
	}
	for _, target := range []string{"cursor", "gemini", "antigravity", "qwen"} {
		t.Run(target, func(t *testing.T) {
			r, err := installTarget(target, "/bin/deja", false)
			if err != nil || r.Action != "created" {
				t.Fatalf("install %s: %#v %v", target, r, err)
			}
			b, _ := os.ReadFile(r.Path)
			if !strings.Contains(string(b), `"deja"`) || !strings.Contains(string(b), "/bin/deja") {
				t.Fatalf("bad config %s: %s", target, b)
			}
			r, err = installTarget(target, "/bin/deja", true)
			if err != nil || r.Action != "updated" {
				t.Fatalf("uninstall %s: %#v %v", target, r, err)
			}
		})
	}
	bad := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(bad, []byte(`{"mcpServers":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installMCPJSON(bad, "/bin/deja", false); err == nil {
		t.Fatal("expected malformed mcp json error")
	}
}

func TestInstallGrokTOML(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "grok-home")
	t.Setenv("GROK_HOME", home)
	cfg := filepath.Join(home, "config.toml")

	r, err := installTarget("grok", "/bin/deja", false)
	if err != nil || r.Action != "created" || r.Path != cfg {
		t.Fatalf("grok install: %#v %v", r, err)
	}
	b, _ := os.ReadFile(cfg)
	// The exe lands in command= on unix and inside args=[...] behind the cmd /c
	// shim on Windows; assert its presence either way.
	if !strings.Contains(string(b), "[mcp_servers.deja]") || !strings.Contains(string(b), "/bin/deja") {
		t.Fatalf("grok config: %s", b)
	}
	// merge into an existing config without touching other sections
	existing := "[cli]\nauto_update = false\n\n[mcp_servers.other]\ncommand = \"x\"\n"
	if err := os.WriteFile(cfg, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err = installTarget("grok", "/bin/deja", false); err != nil || r.Action != "updated" {
		t.Fatalf("grok merge: %#v %v", r, err)
	}
	b, _ = os.ReadFile(cfg)
	for _, want := range []string{"auto_update = false", "[mcp_servers.other]", "[mcp_servers.deja]"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("grok merge lost %q: %s", want, b)
		}
	}
	// idempotent
	if r, err = installTarget("grok", "/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("grok repeat: %#v %v", r, err)
	}
	// uninstall removes only our block
	if r, err = installTarget("grok", "/bin/deja", true); err != nil || r.Action != "updated" {
		t.Fatalf("grok uninstall: %#v %v", r, err)
	}
	b, _ = os.ReadFile(cfg)
	if strings.Contains(string(b), "mcp_servers.deja") || !strings.Contains(string(b), "[mcp_servers.other]") {
		t.Fatalf("grok uninstall config: %s", b)
	}
}

func TestInstallWriteAndJSONEdges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if action, err := writeIfChanged(path, nil, []byte("x")); err != nil || action != "created" {
		t.Fatalf("create = %q %v", action, err)
	}
	if action, err := writeIfChanged(path, []byte("x"), []byte("x")); err != nil || action != "unchanged" {
		t.Fatalf("unchanged = %q %v", action, err)
	}
	if err := backupOnce(filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
	if _, err := updateOpencodeJSON([]byte(`{"mcp":`), "/bin/deja", false); err == nil {
		t.Fatal("expected malformed opencode json error")
	}
	if got := string(updateOpencodeJSONC([]byte("{}\n"), "/bin/deja", true)); got != "{}\n" {
		t.Fatalf("jsonc uninstall no mcp = %q", got)
	}
}

func TestMCPMalformedParamsOversizedAndToolErrors(t *testing.T) {
	hermeticEnv(t)
	for _, req := range []rpcRequest{
		{ID: json.RawMessage(`1`), Method: "tools/call", Params: json.RawMessage(`{"name":`)},
		{ID: json.RawMessage(`2`), Method: "tools/call", Params: json.RawMessage(`{"name":"recall","arguments":{"query":`)},
	} {
		_, code, msg := handleMCP(index.DefaultDir(), req)
		if code != -32602 || msg == "" {
			t.Fatalf("handleMCP(index.DefaultDir(), %#v) code=%d msg=%q", req, code, msg)
		}
	}
	if _, err := callMCPTool(index.DefaultDir(), "recall_context", json.RawMessage(`{"query":`)); err == nil {
		t.Fatal("expected recall_context json error")
	}
	var out bytes.Buffer
	tooLarge := strings.Repeat("x", 10*1024*1024+1) + "\n"
	if err := serveMCP(index.DefaultDir(), strings.NewReader(tooLarge), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "parse error") {
		t.Fatalf("oversized response = %q", out.String())
	}
}

func TestMCPEmptyResultsCounted(t *testing.T) {
	hermeticEnv(t)
	if _, err := callMCPTool(index.DefaultDir(), "recall", json.RawMessage(`{"query":"no-such-result"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := callMCPTool(index.DefaultDir(), "recall_context", json.RawMessage(`{"query":"no-such-result","harness":"claude"}`)); err != nil {
		t.Fatal(err)
	}
	got := usage.Totals(index.DefaultDir())
	if got.Recalls != 2 || got.EmptyResultRate != 1 {
		t.Fatalf("MCP usage = %#v", got)
	}
}

func TestShareDigestBudgetNoiseAndRunErrors(t *testing.T) {
	long := "useful prose before cut " + strings.Repeat("é", 200)
	s := model.Session{ID: "s", Harness: "claude", Project: "p", Messages: []model.Message{
		{Role: "user", Text: ""},
		{Role: "user", Text: "<local-command ignored>"},
		{Role: "user", Text: strings.Repeat("a", 250)},
		{Role: "user", Text: long},
		{Role: "assistant", Text: "file.go:18: grep output\nassistant conclusion is readable and should survive"},
	}}
	d := shareDigest(s, 180)
	if !utf8.ValidString(d) || strings.Contains(d, "local-command") || strings.Contains(d, "file.go:18") || strings.Contains(d, strings.Repeat("é", 80)) {
		t.Fatalf("bad digest len=%d valid=%v:\n%s", len(d), utf8.ValidString(d), d)
	}
	var b bytes.Buffer
	printSanitized(&b, "no newline")
	if !strings.HasSuffix(b.String(), "\n") {
		t.Fatalf("printSanitized no newline = %q", b.String())
	}
	if err := runShare(index.DefaultDir(), []string{"missing"}, io.Discard); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Fatalf("runShare missing err = %v", err)
	}
}

func TestStatsHelperBranches(t *testing.T) {
	var sessions []model.Session
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	for i, p := range []string{"b", "a", "c", "d", "e", "f"} {
		sessions = append(sessions, model.Session{ID: p, Harness: "claude", Project: p, Updated: now.Add(time.Duration(i) * time.Hour), Messages: []model.Message{{Role: "user", Text: p, Time: now}}})
	}
	sessions = append(sessions, model.Session{ID: "empty-project", Harness: "aider", Title: "<local-command noisy>", Messages: []model.Message{{Role: "assistant", Text: "skip"}}})
	r := buildStats(sessions, now)
	if len(r.TopProjects) != 5 || r.TopProjects[0].Project != "-" || r.TopProjects[1].Project != "a" || r.DateRange.Start != "2026-07-16" {
		t.Fatalf("stats = %#v", r)
	}
	var out bytes.Buffer
	printStats(&out, r)
	if !strings.Contains(out.String(), "Longest session") || strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("printStats = %q", out.String())
	}
	for _, h := range []string{"claude", "codex", "opencode", "cursor", "gemini", "aider", "antigravity", "other"} {
		if !strings.Contains(statHarnessTag(h, true), "["+h+"]") {
			t.Fatalf("statHarnessTag(%q)", h)
		}
	}
	if statTitle(model.Session{Title: "good"}) != "good" || statTitle(model.Session{Messages: []model.Message{{Role: "user", Text: "<bash-noise"}}}) != "" {
		t.Fatal("statTitle branches failed")
	}
	if err := runStats(index.DefaultDir(), []string{"--bad"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("runStats bad flag err=%v", err)
	}
}

func TestRunSyncImportAndExportBranches(t *testing.T) {
	hermeticEnv(t)
	in := t.TempDir()
	rec := index.SyncRecord{Harness: "claude", SessionID: "remote", Project: "proj", Role: "user", Text: "hello", Time: time.Now().UTC()}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(in, "batch.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runSync(index.DefaultDir(), []string{"import", in}); err != nil {
		t.Fatalf("sync import: %v", err)
	}
	out := t.TempDir()
	if err := runSync(index.DefaultDir(), []string{"export", "--full", out}); err != nil {
		t.Fatalf("sync export full: %v", err)
	}
	if err := runSync(index.DefaultDir(), []string{"export", out}); err != nil {
		t.Fatalf("sync export: %v", err)
	}
	if err := runSync(index.DefaultDir(), []string{"export", "--full"}); err == nil || !strings.Contains(err.Error(), "target dir") {
		t.Fatalf("sync export missing target err=%v", err)
	}
}

func TestResumeRemainingHarnessBranches(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	cases := []struct {
		s       model.Session
		wantDir string
		wantCmd string
		wantErr string
	}{
		{model.Session{Harness: "opencode", ID: "op", Path: filepath.Join(tmp, "opencode.db")}, "", "opencode -s op", ""},
		{model.Session{Harness: "antigravity", ID: "ag"}, "", "agy --conversation ag", ""},
		{model.Session{Harness: "aider", ID: "aid", Path: filepath.Join(tmp, "aider", ".aider.chat.history.md")}, "", "", "aider has no session resume"},
		{model.Session{Harness: "gemini", ID: "g"}, "", "", "gemini sessions reopen"},
		{model.Session{Harness: "cursor", ID: "c", Path: filepath.Join(tmp, "chat.jsonl")}, "", "", "CLI transcripts"},
		{model.Session{Harness: "cursor", ID: "c", Path: filepath.Join(tmp, "workspace")}, "", "", "Cursor UI"},
		{model.Session{Harness: "claude", ID: "short", Path: ""}, "", "claude --resume short", ""},
	}
	for _, tc := range cases {
		dir, cmd, err := resumeCommand(tc.s)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("%#v err=%v want %q", tc.s, err, tc.wantErr)
			}
			continue
		}
		if err != nil || dir != tc.wantDir || cmd != tc.wantCmd {
			t.Fatalf("%#v got dir=%q cmd=%q err=%v", tc.s, dir, cmd, err)
		}
	}
	if got := short("tiny"); got != "tiny" {
		t.Fatalf("short tiny = %q", got)
	}
}

func TestMainHelpersFallbackAndSourcesBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	badIndex := filepath.Join(tmp, "index-as-file")
	if err := os.WriteFile(badIndex, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", badIndex)
	claudeRoot := filepath.Join(tmp, "claude")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-fallback", "fallback123.jsonl"), "fallback123", []string{
		`{"type":"user","sessionId":"fallback123","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"fallback needle"}}`,
	})
	if s, ok, err := findByPrefix(index.DefaultDir(), "fallback"); err != nil || !ok || s.ID != "fallback123" {
		t.Fatalf("findByPrefix fallback = %#v %v %v", s, ok, err)
	}
	ss, err := recent(index.DefaultDir(), 2)
	if err != nil || len(ss) == 0 || ss[0].ID != "fallback123" {
		t.Fatalf("recent fallback = %#v err=%v", ss, err)
	}
	rroot := filepath.Join(string(filepath.Separator), "root")
	if got := redactionsUnder(map[string]int{rroot: 1, filepath.Join(rroot, "child"): 2, filepath.Join(string(filepath.Separator), "other"): 9}, rroot); got != 3 {
		t.Fatalf("redactionsUnder = %d", got)
	}
	aiderDir := filepath.Join(tmp, "aider-root")
	aiderFile := filepath.Join(aiderDir, ".aider.chat.history.md")
	if err := os.MkdirAll(aiderDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aiderFile, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_AIDER_ROOTS", aiderDir)
	if got := pathSize(aiderDir); got != 5 {
		t.Fatalf("aider pathSize = %d", got)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real"), []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link"))
	if got := pathSize(dir); got != 3 {
		t.Fatalf("symlink pathSize = %d", got)
	}
	if out, err := captureRun(t, "warmup"); err != nil || out != "" {
		t.Fatalf("warmup with bad index out=%q err=%v", out, err)
	}
}

func TestMCPRecallAndProgressBranches(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_DEBUG", "1")
	if mcpProgress() != os.Stderr {
		t.Fatal("debug progress did not use stderr")
	}
	text, err := recallText(index.DefaultDir(), "nomatch", "", 0, 30)
	if err != nil || !strings.Contains(text, "No prior deja sessions") {
		t.Fatalf("recallText no match = %q err=%v", text, err)
	}
	ctx, err := recallContext(index.DefaultDir(), "nomatch")
	if err != nil || !strings.Contains(ctx, "No prior deja sessions") {
		t.Fatalf("recallContext no match = %q err=%v", ctx, err)
	}
}

func TestRunDispatchAdditionalCommands(t *testing.T) {
	withTempStores(t)
	if out, err := captureRun(t, "warmup"); err != nil || out != "" {
		t.Fatalf("warmup out=%q err=%v", out, err)
	}
	if out, err := captureRun(t, "statusline"); err != nil || !strings.Contains(out, "deja ·") {
		t.Fatalf("statusline out=%q err=%v", out, err)
	}
	if _, err := captureRun(t, "frobnicator"); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "ctx", "claude"); err != nil || !strings.Contains(out, "# deja context:") {
		t.Fatalf("ctx prefix out=%q err=%v", out, err)
	}
	if out, err := captureRun(t, "last", "bad"); err != nil || !strings.Contains(out, "claude") {
		t.Fatalf("last bad n out=%q err=%v", out, err)
	}
	if err := run([]string{"show", "no-such-prefix"}); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Fatalf("show no match err=%v", err)
	}
	if err := run([]string{"ctx", "no", "matching", "query"}); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Fatalf("ctx no match err=%v", err)
	}
}

func TestStatuslineOneRecallAndDrainBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	idx := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idx)
	// Exercise the *os.File non-terminal drain path.
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = pipeW.WriteString("session json")
	_ = pipeW.Close()
	var out bytes.Buffer
	if err := runStatusline(index.DefaultDir(), pipeR, &out); err != nil {
		t.Fatal(err)
	}
	_ = pipeR.Close()
	if !strings.Contains(out.String(), "no recalls") {
		t.Fatalf("empty statusline = %q", out.String())
	}
	// One recall switches the noun branch.
	indexDir := idx
	usage.Record(indexDir, usage.KindRecall, 1536)
	out.Reset()
	if err := runStatusline(index.DefaultDir(), strings.NewReader("ignored"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 recall") || !strings.Contains(out.String(), "1.5 KB") {
		t.Fatalf("one recall statusline = %q", out.String())
	}
}

func TestSyncSSHErrorBranches(t *testing.T) {
	setupLocalIndex(t)
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) { return "", os.ErrPermission }
	if err := runSyncSSH(index.DefaultDir(), []string{"host", "--pull"}); err == nil || !strings.Contains(err.Error(), "ssh host") {
		t.Fatalf("pull mktemp err=%v", err)
	}
	if _, err := sshCapture("host", "cmd"); err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("sshCapture err=%v", err)
	}
	sshRunner = func(name string, args ...string) (string, error) { return "/tmp/bad'quote", nil }
	if _, err := sshCapture("host", "cmd"); err == nil || !strings.Contains(err.Error(), "unexpected output") {
		t.Fatalf("sshCapture quote err=%v", err)
	}
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[1] == "mktemp -d" {
			return "/tmp/remote", nil
		}
		return "boom", os.ErrPermission
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"host", "--pull"}); err == nil || !strings.Contains(err.Error(), "remote export") {
		t.Fatalf("pull remote export err=%v", err)
	}
}

func TestAdditionalDispatchAndHelperBranches(t *testing.T) {
	withTempStores(t)
	oldStdin := os.Stdin
	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = wIn.Close()
	os.Stdin = rIn
	if out, err := captureRun(t, "mcp"); err != nil || out != "" {
		t.Fatalf("mcp dispatch out=%q err=%v", out, err)
	}
	os.Stdin = oldStdin
	_ = rIn.Close()
	if out, err := captureRun(t, "resume", "nope"); err == nil || !strings.Contains(err.Error(), "no session matches") || out != "" {
		t.Fatalf("resume dispatch out=%q err=%v", out, err)
	}
	if out, err := captureRun(t, "sync", "ssh", "--evil"); err == nil || !strings.Contains(err.Error(), "unknown flag") || out != "" {
		t.Fatalf("sync ssh dispatch out=%q err=%v", out, err)
	}
	if out, err := captureRun(t, "--rebuild", "definitely-no-match"); err != nil || out != "" {
		t.Fatalf("rebuild no match out=%q err=%v", out, err)
	}
	if _, err := parseSearch([]string{"--all", "needle"}); err != nil {
		t.Fatalf("parse --all: %v", err)
	}
	if out, err := captureRun(t, "--re", "("); err == nil || !strings.Contains(err.Error(), "run:") || out != "" {
		t.Fatalf("bad regex out=%q err=%v", out, err)
	}
	badRoot := t.TempDir()
	badIndex := filepath.Join(badRoot, "index-as-file")
	if err := os.WriteFile(badIndex, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(badIndex, "child"))
	if _, err := captureRun(t, "needle"); err == nil || !strings.Contains(err.Error(), "ensure:") {
		t.Fatalf("ensure error err=%v", err)
	}
	if got := firstUserTitle(model.Session{Messages: []model.Message{{Role: "user", Text: " short title "}}}); got != "short title" {
		t.Fatalf("short title = %q", got)
	}
	if got := firstUserTitle(model.Session{Messages: []model.Message{{Role: "assistant", Text: "skip"}}}); got != "" {
		t.Fatalf("empty title = %q", got)
	}
	oldArgs := os.Args
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"deja", "version"}
	os.Stdout = w
	main()
	_ = w.Close()
	os.Args = oldArgs
	os.Stdout = oldStdout
	b, _ := io.ReadAll(r)
	if !strings.Contains(string(b), "deja dev") {
		t.Fatalf("main version = %q", b)
	}
}

func TestShareStatsResumeAndSyncEdgeBranches(t *testing.T) {
	if got := shareDigest(model.Session{ID: "empty"}, 0); !strings.Contains(got, "# deja share: empty") {
		t.Fatalf("default share digest = %q", got)
	}
	msg := strings.Repeat("word ", 20) + "\n"
	var many []string
	for i := 0; i < 20; i++ {
		many = append(many, msg)
	}
	if got := shareMessageText(strings.Join(many, "")); strings.Count(got, "word") > 16*20 {
		t.Fatalf("shareMessageText did not cap lines: %q", got)
	}
	for _, in := range []string{"", strings.Repeat("9", 90), strings.Repeat("x", 10)} {
		_ = shareMessageText(in)
	}
	_ = looksLikeProse("")
	if got := utf8SafeCut("abc", 10); got != "abc" {
		t.Fatalf("utf8SafeCut no-op = %q", got)
	}
	if got := utf8SafeCut("abc", 0); got != "" {
		t.Fatalf("utf8SafeCut zero = %q", got)
	}

	closed, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	if statColorOK(closed) {
		t.Fatal("closed file reported color")
	}
	var b bytes.Buffer
	printStats(&b, statsReport{Harnesses: []harnessStats{{Harness: strings.Repeat("h", 20), Sessions: 1}}, TopProjects: []projectStats{{Project: "p", Sessions: 0}}, Monthly: []monthStats{{Month: "bad"}}})
	if !strings.Contains(b.String(), "deja stats") {
		t.Fatalf("stats output = %q", b.String())
	}
	if got := short("123456789012345"); got != "123456789012" {
		t.Fatalf("short long = %q", got)
	}
	if got := claudeProjectDirFor(model.Session{Path: filepath.Join(t.TempDir(), "plain.jsonl")}); got != "" {
		t.Fatalf("claudeProjectDirFor = %q", got)
	}

	badRoot := t.TempDir()
	badIndex := filepath.Join(badRoot, "index-as-file")
	if err := os.WriteFile(badIndex, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(badIndex, "child"))
	if err := runStats(index.DefaultDir(), nil); err == nil {
		t.Fatal("expected stats ensure error")
	}
	if err := runSync(index.DefaultDir(), []string{"export", t.TempDir()}); err == nil {
		t.Fatal("expected sync export ensure error")
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	if err := runSync(index.DefaultDir(), []string{"import", "["}); err == nil {
		t.Fatal("expected sync import error")
	}
}

func TestFallbackFindRecentAndMCPEnsureErrors(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claudeRoot := filepath.Join(tmp, "claude")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-fallback2", "fallback2.jsonl"), "fallback2", []string{
		`{"type":"user","sessionId":"fallback2","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"fallback two needle"}}`,
	})
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	blocked := filepath.Join(tmp, "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(blocked, "child"))
	s, ok, err := findByPrefix(index.DefaultDir(), "fallback2")
	if err != nil || !ok || s.ID != "fallback2" {
		t.Fatalf("fallback find = %#v ok=%v err=%v", s, ok, err)
	}
	ss, err := recent(index.DefaultDir(), 1)
	if err != nil || len(ss) != 1 || ss[0].ID != "fallback2" {
		t.Fatalf("fallback recent = %#v err=%v", ss, err)
	}
	if _, err := recallText(index.DefaultDir(), "needle", "", 1, 100); err == nil {
		t.Fatal("expected recallText ensure error")
	}
	if _, err := recallContext(index.DefaultDir(), "needle"); err == nil {
		t.Fatal("expected recallContext ensure error")
	}
}

func TestMoreErrorBranches(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if out, err := captureRun(t, "install", "--auto"); err != nil || !strings.Contains(out, "no known agent") {
		t.Fatalf("install --auto none out=%q err=%v", out, err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexAuto("/bin/deja", false); err == nil {
		t.Fatal("expected codex auto config path error")
	}
	_ = os.Remove(filepath.Join(home, ".codex"))
	plugin := filepath.Join(home, ".config", "opencode", "plugins", "deja.js")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "nested"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpencodePlugin("/bin/deja", true); err == nil {
		t.Fatal("expected opencode plugin remove directory error")
	}
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeIfChanged(filepath.Join(parent, "child"), nil, []byte("x")); err == nil {
		t.Fatal("expected mkdir parent file error")
	}
	dir := t.TempDir()
	if action, err := writeIfChanged(dir, []byte("old"), []byte("new")); err == nil || action != "" {
		t.Fatalf("expected write dir error action=%q err=%v", action, err)
	}
	if _, err := installMCPJSON(filepath.Join(t.TempDir(), "bad.json"), func() string { return string([]byte{0xff}) }(), false); err != nil {
		t.Fatalf("installMCPJSON weird exe: %v", err)
	}
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) { return "", nil }
	_ = runSyncSSH(index.DefaultDir(), []string{"host", "--pull"})
}

func TestSyncSSHPushMoreErrorBranches(t *testing.T) {
	setupLocalIndex(t)
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[1] == "mktemp -d" {
			return "", errors.New("mktemp failed")
		}
		return "", nil
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"host"}); err == nil || !strings.Contains(err.Error(), "mktemp failed") {
		t.Fatalf("push mktemp err=%v", err)
	}
	// Mark exported so a new fixture is available for the remote-import branch.
	setupLocalIndex(t)
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[1] == "mktemp -d" {
			return "/tmp/remote", nil
		}
		if name == "ssh" {
			return "remote boom", errors.New("bad import")
		}
		return "", nil
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"host"}); err == nil || !strings.Contains(err.Error(), "remote import") {
		t.Fatalf("push remote import err=%v", err)
	}
	blocked := filepath.Join(t.TempDir(), "index-file")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(blocked, "child"))
	if err := runSyncSSH(index.DefaultDir(), []string{"host"}); err == nil {
		t.Fatal("expected push ensure error")
	}
}

func TestInstallAdditionalBranches(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	for _, dir := range []string{filepath.Join(h, ".codex"), filepath.Join(h, ".config", "opencode")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := captureRun(t, "install", "--auto"); err != nil || !strings.Contains(out, "codex-auto:") || !strings.Contains(out, "opencode-auto:") {
		t.Fatalf("install --auto codex/opencode out=%q err=%v", out, err)
	}
	if _, err := installTarget("codex-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("opencode-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("statusline", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("claude-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h, ".claude.json"), []byte(`{"mcpServers":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeAuto("/bin/deja", false); err == nil {
		t.Fatal("expected claude-auto malformed json error")
	}
	if err := os.WriteFile(filepath.Join(h, ".codex", "config.toml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(h, ".codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(h, ".codex", "hooks.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexAuto("/bin/deja", false); err == nil {
		t.Fatal("expected codex-auto hook write error")
	}
	if err := os.WriteFile(filepath.Join(h, ".config", "opencode", "opencode.json"), []byte(`{"mcp":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpencodeAuto("/bin/deja", false); err == nil {
		t.Fatal("expected opencode-auto malformed json error")
	}
}

func TestClaudeHookUpdateEdgeBranches(t *testing.T) {
	root := map[string]any{}
	got := updateClaudeSessionStartHook(root, "/bin/deja", true)
	if _, ok := got["hooks"]; ok {
		t.Fatalf("empty uninstall left hooks: %#v", got)
	}
	entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/bin/deja hook-context"}, map[string]any{"type": "command", "command": "other"}}, "matcher": "x"}
	root = map[string]any{"hooks": map[string]any{"SessionStart": []any{"not-map", entry}}}
	got = updateClaudeSessionStartHook(root, "/bin/deja", true)
	s := jsonString(got)
	if strings.Contains(s, "/bin/deja hook-context") || !strings.Contains(s, "other") || !strings.Contains(s, "not-map") {
		t.Fatalf("hook uninstall edge = %#v", got)
	}
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestHookDigestPlainAndLimitBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	idx := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idx)
	claudeRoot := filepath.Join(tmp, "claude")
	projDir := filepath.Join(claudeRoot, "-tmp-many")
	for i := 0; i < 4; i++ {
		id := string(rune('a'+i)) + "many"
		writeClaudeFixture(t, filepath.Join(projDir, id+".jsonl"), id, []string{`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"many project memory ` + id + `"}}`})
	}
	if err := index.EnsureForSearch(idx, search.Options{Query: "many", All: true}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	oldwd, _ := os.Getwd()
	work := filepath.Join("/tmp", "many")
	if runtime.GOOS == "windows" {
		work = filepath.Join(tmp, "many")
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	var out bytes.Buffer
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	if err := runHookContext(index.DefaultDir(), true); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdout = old
	_, _ = io.Copy(&out, r)
	if !strings.Contains(out.String(), "many project memory") || strings.Count(out.String(), "many project memory") > 3 {
		t.Fatalf("plain hook out=%q", out.String())
	}
}

func TestMCPAdditionalBranches(t *testing.T) {
	hermeticEnv(t)
	if _, err := callMCPTool(index.DefaultDir(), "recall", json.RawMessage(`{"query":`)); err == nil {
		t.Fatal("expected recall json error")
	}
	if _, err := callMCPTool(index.DefaultDir(), "recall_context", json.RawMessage(`{"query":"   "}`)); err == nil || !strings.Contains(err.Error(), "query required") {
		t.Fatalf("empty recall_context err=%v", err)
	}
	if got := trimUTF8("abc", 10); got != "abc" {
		t.Fatalf("trim no-op = %q", got)
	}
	var out bytes.Buffer
	t.Setenv("DEJA_DEBUG", "1")
	tooLarge := "\n" + strings.Repeat("x", 10*1024*1024+1) + "\n"
	if err := serveMCP(index.DefaultDir(), strings.NewReader(tooLarge), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "parse error") {
		t.Fatalf("oversized debug out=%q", out.String())
	}
	root, _ := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	text, err := recallText(index.DefaultDir(), "frobnicator", "claude", 1, 80)
	if err != nil || len(text) > 80 || !strings.Contains(text, "deja recall") {
		t.Fatalf("recallText limited len=%d text=%q err=%v", len(text), text, err)
	}
}

func TestSyncSSHAdditionalBranches(t *testing.T) {
	setupLocalIndex(t)
	old := sshRunner
	defer func() { sshRunner = old }()
	var cleaned bool
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[1] == "mktemp -d" {
			return "/tmp/remote-zero", nil
		}
		if name == "ssh" && strings.Contains(args[1], "sync export") {
			return "deja: exported 0 records", nil
		}
		if name == "ssh" && strings.Contains(args[1], "rm -rf") {
			cleaned = true
		}
		return "", nil
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"host", "--pull"}); err != nil {
		t.Fatal(err)
	}
	if !cleaned {
		t.Fatal("expected cleanup on empty pull")
	}
	if err := index.EnsureForSearch(index.DefaultDir(), search.Options{All: true}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := exportBatches(index.DefaultDir(), t.TempDir(), true); err != nil {
		t.Fatal(err)
	}
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[1] == "mktemp -d" {
			return "/tmp/remote", nil
		}
		if name == "scp" {
			return "denied", errors.New("boom")
		}
		return "", nil
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"host", "--full"}); err == nil || !strings.Contains(err.Error(), "scp") {
		t.Fatalf("push scp err=%v", err)
	}
}

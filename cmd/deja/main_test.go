package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func withTempStores(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	claude, _ := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	t.Setenv("NO_COLOR", "1")
	return h
}

func captureRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = run(args)
	_ = w.Close()
	os.Stdout = old
	b, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(b), err
}

func TestRunDispatcherSyntheticFixtures(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{"usage", nil, "Usage:", ""},
		{"version", []string{"version"}, "deja dev", ""},
		{"search", []string{"frobnicator"}, "frobnicator bug", ""},
		{"search json", []string{"--json", "frobnicator"}, `"count"`, ""},
		{"search regex", []string{"--re", "frobnicator|parser"}, "frobnicator", ""},
		{"filters", []string{"--harness", "claude", "--project", "project", "--since", "365000d", "--role", "assistant", "frobnicator"}, "frobnicator bug", ""},
		{"show", []string{"show", "claude"}, "# claude", ""},
		{"ctx", []string{"ctx", "frobnicator"}, "# deja context:", ""},
		{"last", []string{"last", "1"}, "claude", ""},
		{"sources", []string{"sources"}, "opencode", ""},
		{"ctx missing", []string{"ctx"}, "", "ctx needs query"},
		{"show missing", []string{"show"}, "", "show needs id-prefix"},
		{"bad duration", []string{"--since", "nope", "needle"}, "", "invalid duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTempStores(t)
			out, err := captureRun(t, tc.args...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v, want %q out=%q", err, tc.wantErr, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("run error: %v", err)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("out %q does not contain %q", out, tc.want)
			}
		})
	}
}

func TestParseSearchAndSmallHelpers(t *testing.T) {
	for _, tc := range []struct {
		args []string
		err  string
	}{
		{[]string{"--harness"}, "needs value"},
		{[]string{"--project"}, "needs value"},
		{[]string{"--role"}, "needs value"},
		{[]string{}, "query required"},
	} {
		if _, err := parseSearch(tc.args); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Fatalf("parseSearch(%v) err=%v want %q", tc.args, err, tc.err)
		}
	}
	if d, err := parseDur("2d"); err != nil || d != 48*time.Hour {
		t.Fatalf("parseDur days = %v %v", d, err)
	}
	if got := humanBytes(1536); got != "1.5 KB" {
		t.Fatalf("humanBytes = %q", got)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pathSize(dir); got != 3 {
		t.Fatalf("pathSize=%d", got)
	}
	long := strings.Repeat("x", 70)
	got := firstUserTitle(model.Session{ID: "id", Messages: []model.Message{{Role: "assistant", Text: "skip"}, {Role: "user", Text: "  hello   " + long}}})
	if !strings.HasPrefix(got, "hello") || !strings.HasSuffix(got, "…") {
		t.Fatalf("firstUserTitle=%q", got)
	}
}

func TestPrintNoMatchesHelpfulMessage(t *testing.T) {
	var b bytes.Buffer
	printNoMatches(&b, "jwt refresh token", 3)
	out := b.String()
	if !strings.Contains(out, `deja: no matches for "jwt refresh token"`) || !strings.Contains(out, "searched 3 sessions across claude/codex/opencode") || !strings.Contains(out, "try fewer words or --re") {
		t.Fatalf("bad no-match message: %q", out)
	}
}

func TestVersionDefaultIsDev(t *testing.T) {
	if version != "dev" {
		t.Fatalf("version = %q, want dev", version)
	}
}

func TestMCPHandshakeListRecallRoundTrip(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator","harness":"claude","limit":1}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(in))
		_ = pw.Close()
	}()
	if err := serveMCP(pr, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d responses: %q", len(lines), out.String())
	}
	var initResp map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatal(err)
	}
	res := initResp["result"].(map[string]any)
	if res["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("bad init: %#v", initResp)
	}
	if !strings.Contains(lines[1], "recall_context") || !strings.Contains(lines[2], "frobnicator bug") {
		t.Fatalf("bad mcp output:\n%s", out.String())
	}
}

func TestMCPRecallContext(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	in := `{"jsonrpc":"2.0","id":"ctx","method":"tools/call","params":{"name":"recall_context","arguments":{"query":"frobnicator"}}}` + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") || !strings.Contains(out.String(), "# deja context:") {
		t.Fatalf("bad context: %q", out.String())
	}
}

func TestMCPErrorAndNotificationPaths(t *testing.T) {
	withTempStores(t)
	var errBuf bytes.Buffer
	writeRPCError(json.NewEncoder(&errBuf), "x", -1, "boom")
	if !strings.Contains(errBuf.String(), `"code":-1`) || !strings.Contains(errBuf.String(), `"id":"x"`) {
		t.Fatalf("bad rpc error: %s", errBuf.String())
	}
	if got := trimUTF8("éclair", 1); got != "" {
		t.Fatalf("trimUTF8 cut rune = %q", got)
	}
	if got := trimUTF8("éclair", 3); got != "éc" {
		t.Fatalf("trimUTF8 = %q", got)
	}
	in := strings.Join([]string{
		`not-json`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"missing"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":""}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(strings.TrimSpace(got), "\n")+1 != 4 || !strings.Contains(got, "parse error") || !strings.Contains(got, "method not found") || !strings.Contains(got, "query required") || !strings.Contains(got, "unknown tool") {
		t.Fatalf("bad mcp errors: %s", got)
	}
}

func TestInstallClaudeTempHome(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	path := filepath.Join(h, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"other":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := installTarget("claude-code", "/bin/deja", false)
	if err != nil || r.Action != "updated" {
		t.Fatalf("install: %#v %v", r, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"mcpServers"`) || !strings.Contains(string(b), `"command": "/bin/deja"`) {
		t.Fatalf("bad claude config: %s", b)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatal("missing backup", err)
	}
	r, err = installTarget("claude-code", "/bin/deja", false)
	if err != nil || r.Action != "unchanged" {
		t.Fatalf("idempotent: %#v %v", r, err)
	}
	if _, err := installTarget("claude-code", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), `"deja"`) {
		t.Fatalf("uninstall left deja: %s", b)
	}
}

func TestHookContextSyntheticFixtures(t *testing.T) {
	withTempStores(t)
	if out, err := captureRun(t, "hook-context"); err != nil || out != "" {
		t.Fatalf("hook without index out=%q err=%v", out, err)
	}
	if _, err := captureRun(t, "frobnicator"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", filepath.Join("tmp", "project"))
	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("bad json %q: %v", out, err)
	}
	digest := resp.HookSpecificOutput.AdditionalContext
	if resp.HookSpecificOutput.HookEventName != "SessionStart" || !strings.Contains(digest, "Find frobnicator bug") || !strings.Contains(digest, "parser.go") {
		t.Fatalf("bad hook response: %#v", resp)
	}
	if len(digest) > 2000 {
		t.Fatalf("digest too large: %d", len(digest))
	}
}

func TestInstallAutoClaudeHookIdempotentPreservesHooks(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	mcpPath := filepath.Join(h, ".claude.json")
	settingsPath := filepath.Join(h, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldSettings := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/bin/user-hook"}]}],"Stop":[{"hooks":[{"type":"command","command":"/bin/stop"}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(oldSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "--auto"); err != nil || !strings.Contains(out, "claude-auto:") {
		t.Fatalf("install --auto out=%q err=%v", out, err)
	}
	b, _ := os.ReadFile(mcpPath)
	if !strings.Contains(string(b), `"mcpServers"`) || !strings.Contains(string(b), `"mcp"`) {
		t.Fatalf("mcp not installed: %s", b)
	}
	b, _ = os.ReadFile(settingsPath)
	s := string(b)
	if strings.Count(s, "hook-context") != 1 || !strings.Contains(s, "/bin/user-hook") || !strings.Contains(s, `"Stop"`) {
		t.Fatalf("bad auto settings: %s", s)
	}
	if _, err := os.Stat(settingsPath + ".bak"); err != nil {
		t.Fatal("missing settings backup", err)
	}
	if out, err := captureRun(t, "install", "--auto"); err != nil || !strings.Contains(out, "unchanged") {
		t.Fatalf("idempotent out=%q err=%v", out, err)
	}
	b, _ = os.ReadFile(settingsPath)
	if strings.Count(string(b), "hook-context") != 1 {
		t.Fatalf("duplicate hook: %s", b)
	}
	if out, err := captureRun(t, "uninstall", "--auto"); err != nil || !strings.Contains(out, "claude-auto:") {
		t.Fatalf("uninstall --auto out=%q err=%v", out, err)
	}
	b, _ = os.ReadFile(settingsPath)
	if strings.Contains(string(b), "hook-context") || !strings.Contains(string(b), "/bin/user-hook") {
		t.Fatalf("bad uninstall settings: %s", b)
	}
	b, _ = os.ReadFile(mcpPath)
	if strings.Contains(string(b), `"deja"`) {
		t.Fatalf("mcp left deja: %s", b)
	}
}

func TestInstallCodexTempHomePreservesOtherTOML(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	path := filepath.Join(h, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	old := "model = \"x\"\n\n[mcp_servers.other]\ncommand = \"other\"\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("codex", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "[mcp_servers.other]") || !strings.Contains(string(b), "[mcp_servers.deja]") {
		t.Fatalf("bad codex config: %s", b)
	}
	if _, err := installTarget("codex", "/new/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Count(string(b), "[mcp_servers.deja]") != 1 || !strings.Contains(string(b), `/new/deja`) {
		t.Fatalf("bad replace: %s", b)
	}
	if _, err := installTarget("codex", "/new/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "[mcp_servers.deja]") || !strings.Contains(string(b), "[mcp_servers.other]") {
		t.Fatalf("bad uninstall: %s", b)
	}
}

func TestInstallOpencodeJSONAndJSONC(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	jsonPath := filepath.Join(h, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("opencode", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(jsonPath)
	if !strings.Contains(string(b), `"mcp"`) || !strings.Contains(string(b), `"/bin/deja"`) {
		t.Fatalf("bad opencode json: %s", b)
	}

	h2 := t.TempDir()
	t.Setenv("HOME", h2)
	t.Setenv("USERPROFILE", h2)
	jsoncPath := filepath.Join(h2, ".config", "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(jsoncPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsoncPath, []byte("{\n  // keep me\n  \"theme\": \"dark\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("opencode", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(jsoncPath)
	if !strings.Contains(string(b), "// keep me") || !strings.Contains(string(b), `"deja"`) {
		t.Fatalf("bad opencode jsonc: %s", b)
	}
}

func TestRunInstallAllExistingAndJSONCEdges(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	if out, err := captureRun(t, "install", "--all"); err != nil || !strings.Contains(out, "no known agent") {
		t.Fatalf("empty --all out=%q err=%v", out, err)
	}
	if err := os.WriteFile(filepath.Join(h, ".claude.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := existingTargets(); strings.Join(got, ",") != "claude-code,codex,opencode" {
		t.Fatalf("existingTargets=%v", got)
	}
	if out, err := captureRun(t, "install", "--all"); err != nil || !strings.Contains(out, "claude-code:") || !strings.Contains(out, "codex:") || !strings.Contains(out, "opencode:") {
		t.Fatalf("install --all out=%q err=%v", out, err)
	}
	if out, err := captureRun(t, "uninstall", "--all"); err != nil || !strings.Contains(out, "opencode:") {
		t.Fatalf("uninstall --all out=%q err=%v", out, err)
	}
	for _, tc := range []struct{ name, old, want string }{
		{"empty", "", `"mcp"`},
		{"existing mcp no comma", "{\n  \"mcp\": {\n    \"other\": {\"type\":\"local\"}\n  }\n}\n", `"other": {"type":"local"},`},
		{"top trailing", "{\n  \"theme\": \"dark\",\n}\n", `"mcp"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(updateOpencodeJSONC([]byte(tc.old), "/bin/deja", false))
			if !strings.Contains(got, tc.want) || !strings.Contains(got, `"deja"`) {
				t.Fatalf("jsonc got:\n%s\nwant contains %q", got, tc.want)
			}
			un := string(updateOpencodeJSONC([]byte(got), "/bin/deja", true))
			if strings.Contains(un, `"deja"`) {
				t.Fatalf("uninstall left deja: %s", un)
			}
		})
	}
}

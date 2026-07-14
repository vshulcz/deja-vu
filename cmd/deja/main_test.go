package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintNoMatchesHelpfulMessage(t *testing.T) {
	var b bytes.Buffer
	printNoMatches(&b, "jwt refresh token", 3)
	out := b.String()
	if !strings.Contains(out, `deja: no matches for "jwt refresh token"`) || !strings.Contains(out, "searched 3 sessions across claude/codex/opencode") || !strings.Contains(out, "try fewer words or --re") {
		t.Fatalf("bad no-match message: %q", out)
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

func TestInstallClaudeTempHome(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
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

func TestInstallCodexTempHomePreservesOtherTOML(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
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

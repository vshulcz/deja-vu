package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestParseBlame(t *testing.T) {
	path, options, jsonOutput, err := parseBlame([]string{"--json", "--all", "--harness", "claude", "--project", "api", "--since", "30d", "main.go"})
	if err != nil || path != "main.go" || !jsonOutput || !options.All || options.Harness != "claude" || options.Project != "api" || options.Since <= 0 {
		t.Fatalf("parse path=%q options=%#v json=%v err=%v", path, options, jsonOutput, err)
	}
	for _, args := range [][]string{{}, {"--harness"}, {"--bad", "main.go"}, {"a.go", "b.go"}} {
		if _, _, _, err := parseBlame(args); err == nil {
			t.Fatalf("parse accepted %#v", args)
		}
	}
	if _, _, _, err := parseBlame([]string{"--since", "bad", "main.go"}); err == nil {
		t.Fatal("bad since accepted")
	}
	if _, _, _, err := parseBlame([]string{"--json", "main.go"}); err != nil {
		t.Fatalf("json parse err=%v", err)
	}
}

func TestBlameCLIAndMCP(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "synthetic", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	out, err := captureRun(t, "blame", "--json", "parser.go")
	if err != nil || !strings.Contains(out, `"session"`) || !strings.Contains(out, "parser.go") {
		t.Fatalf("blame out=%q err=%v", out, err)
	}
	text, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"parser.go","harness":"claude","limit":1}`))
	if err != nil || !strings.Contains(text, `"session"`) {
		t.Fatalf("mcp blame=%q err=%v", text, err)
	}
	if text, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"parser.go","all":true}`)); err != nil || !strings.Contains(text, `"session"`) {
		t.Fatalf("mcp all blame=%q err=%v", text, err)
	}
	if err := runBlame(index.DefaultDir(), []string{"parser.go"}); err != nil {
		t.Fatal(err)
	}
	if err := runBlame(index.DefaultDir(), []string{"missing.go"}); err != nil {
		t.Fatal(err)
	}
	if err := runBlame(index.DefaultDir(), nil); err == nil {
		t.Fatal("missing blame path accepted")
	}
	if _, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":`)); err == nil {
		t.Fatal("malformed mcp blame accepted")
	}
	if _, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"x.go","since":"bad"}`)); err == nil {
		t.Fatal("bad mcp blame since accepted")
	}
	if _, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"   "}`)); err == nil {
		t.Fatal("empty mcp blame path accepted")
	}
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(blocked, "child"))
	if err := runBlame(index.DefaultDir(), []string{"parser.go"}); err == nil {
		t.Fatal("blame accepted blocked index")
	}
	if _, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"parser.go"}`)); err == nil {
		t.Fatal("mcp blame accepted blocked index")
	}
}

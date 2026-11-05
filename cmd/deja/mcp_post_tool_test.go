package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPPostToolHandoffSuppressesTUIDisplayForQwenAndGemini(t *testing.T) {
	seedFileHistory(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	home := os.Getenv("HOME")
	t.Cleanup(func() { setMCPClient("") })

	for _, tc := range []struct {
		name     string
		client   string
		install  func() error
		event    string
		toolName string
	}{
		{
			name:     "qwen-code",
			client:   "qwen-code-mcp-client",
			install:  func() error { _, err := installQwenAuto("/bin/deja", false); return err },
			event:    "PostToolUse",
			toolName: "mcp__deja__deja",
		},
		{
			name:     "gemini-cli",
			client:   "gemini-cli-mcp-client",
			install:  func() error { _, err := installGeminiExtension("/bin/deja", false); return err },
			event:    "AfterTool",
			toolName: "mcp_deja_deja",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.install(); err != nil {
				t.Fatal(err)
			}
			_, code, msg := handleMCP(dir, rpcRequest{
				Method: "initialize",
				Params: json.RawMessage(`{"clientInfo":{"name":"` + tc.client + `"}}`),
			})
			if code != 0 {
				t.Fatalf("initialize error %d: %s", code, msg)
			}

			res, code, msg := handleMCP(dir, rpcRequest{
				Method: "tools/call",
				Params: json.RawMessage(`{"name":"deja","arguments":{"mode":"recall","query":"ARENAGUARD"}}`),
			})
			if code != 0 {
				t.Fatalf("tools/call error %d: %s", code, msg)
			}
			m, ok := res.(map[string]any)
			if !ok {
				t.Fatalf("unexpected tools/call result %T", res)
			}
			content, _ := m["content"].([]map[string]string)
			if len(content) != 0 {
				t.Fatalf("tools/call returned non-empty display content for %s: %+v", tc.name, content)
			}

			payload := `{"session_id":"s-live","cwd":"/work/alpha","hook_event_name":"` + tc.event + `","tool_name":"` + tc.toolName + `","tool_input":{"mode":"recall","query":"ARENAGUARD"}}`
			var out bytes.Buffer
			if err := runHookTool(dir, strings.NewReader(payload), &out); err != nil {
				t.Fatal(err)
			}
			var resp sessionStartHookResponse
			if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
				t.Fatalf("hook output not JSON: %v (%q)", err, out.String())
			}
			if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "<deja-recall>") ||
				!strings.Contains(resp.HookSpecificOutput.AdditionalContext, "ARENAGUARD") {
				t.Errorf("PostToolUse did not deliver staged <deja-recall> block: %q", resp.HookSpecificOutput.AdditionalContext)
			}
			if resp.SystemMessage != "" {
				t.Errorf("PostToolUse emitted unexpected systemMessage %q", resp.SystemMessage)
			}

			// A second hook run after the staged output was consumed stays quiet.
			out.Reset()
			if err := runHookTool(dir, strings.NewReader(payload), &out); err != nil {
				t.Fatal(err)
			}
			if out.Len() != 0 {
				t.Errorf("staged output was delivered twice: %q", out.String())
			}
		})
	}

	// Without the post-tool hook wired (e.g. MCP-only install), tools/call
	// returns the recall block inline so the model still receives it.
	_ = os.RemoveAll(filepath.Join(home, ".qwen"))
	setMCPClient("qwen-code-mcp-client")
	res, code, msg := handleMCP(dir, rpcRequest{
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"deja","arguments":{"mode":"recall","query":"ARENAGUARD"}}`),
	})
	if code != 0 {
		t.Fatalf("tools/call error %d: %s", code, msg)
	}
	m, _ := res.(map[string]any)
	content, _ := m["content"].([]map[string]string)
	if len(content) != 1 || !strings.Contains(content[0]["text"], "<deja-recall>") {
		t.Errorf("unwired Qwen client did not receive inline <deja-recall>: %+v", content)
	}
}

func TestQwenReinstallUpgradesPostToolUseMatcherForMCP(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	settings := filepath.Join(home, ".qwen", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"hooks":{"PostToolUse":[{"matcher":"read_file|edit|write_file","hooks":[{"type":"command","command":"/bin/deja hook-tool","timeout":60000}]}]}}`
	if err := os.WriteFile(settings, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"read_file|edit|write_file|mcp__deja__.*"`) {
		t.Errorf("reinstall did not upgrade PostToolUse matcher:\n%s", b)
	}
}

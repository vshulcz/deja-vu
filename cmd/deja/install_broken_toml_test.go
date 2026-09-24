package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config somebody has already broken by hand. Every JSON target refuses one
// and says where; the TOML path spliced its block in regardless, so deja's
// entry landed in a file the harness cannot load — and the harness's own error
// then named deja's lines (#3576).
func TestInstallRefusesATOMLFileThatIsAlreadyBroken(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	broken := "model = \"gpt-5\"\n[mcp_servers.theirs\ncommand = \"x\"\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := installTOML(path, "[mcp_servers.deja]\ncommand = \"deja\"\n", false)
	if err == nil {
		t.Fatal("install wrote into a file that does not parse")
	}
	if !strings.Contains(err.Error(), "table header") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != broken {
		t.Fatalf("the file was touched:\n%s", after)
	}
}

// What the check must not do is refuse an ordinary config: headers with
// comments, nested tables, arrays of tables and quoted keys are all fine.
func TestTheTOMLCheckLeavesOrdinaryConfigsAlone(t *testing.T) {
	for _, text := range []string{
		"model = \"gpt-5\"\n",
		"[mcp_servers.deja]\ncommand = \"deja\"\n",
		"[mcp_servers.deja]  # the one deja wrote\ncommand = \"deja\"\n",
		"[[servers]]\nname = \"a\"\n\n[[servers]]\nname = \"b\"\n",
		"[tools.\"weird.name\"]\nenabled = true\n",
		// codex writes hook trust headers whose quoted key carries a `#`
		// (the plugin json path and the hook index). A comment-stripper that
		// cuts at every `#` refuses them, so a real codex config could not
		// be installed into at all.
		"[hooks.state.\"browser@openai-bundled:plugin.json#hooks[0]:subagent_stop:0:0\"]\nenabled = true\n",
		"[hooks.state.\"unified-computer-use@openai-bundled:plugin.json#hooks[0]:post_tool_use:0:0\"]\nenabled = true\n",
		"# [commented.out]\nvalue = 1\n",
		"",
	} {
		if err := tomlHeadersClose(text); err != nil {
			t.Errorf("a good config was refused: %q -> %v", text, err)
		}
	}
}

// And it has to catch the shape it exists for, wherever it sits in the file.
func TestTheTOMLCheckCatchesAHeaderThatNeverCloses(t *testing.T) {
	for _, text := range []string{
		"[mcp_servers.theirs\ncommand = \"x\"\n",
		"model = \"gpt-5\"\n\n[tools\n",
		"[a]\nx = 1\n[b\ny = 2\n",
		// the `#` inside the quoted key must not hide a missing closing bracket
		"[hooks.state.\"browser@openai-bundled:plugin.json#hooks[0]:subagent_stop:0:0\nenabled = true\n",
	} {
		if err := tomlHeadersClose(text); err == nil {
			t.Errorf("a broken header was accepted: %q", text)
		}
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `deja install trae-ide` writes the entry TRAE IDE 3.5.104 loaded live: under
// mcpServers in <user data>/User/mcp.json, command and args and no type key,
// which the IDE's schema rejects. The skill goes to ~/.trae/skills. A second
// run changes nothing, and uninstall leaves both files as they were.
func TestInstallTraeIDEMCPAndSkill(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	dir := index.DefaultDir()
	mcp := traeIDEMCPPath()
	if err := os.MkdirAll(filepath.Dir(mcp), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := `{"mcpServers":{"other":{"command":"x","args":["y"],"disabled":true}}}` + "\n"
	if err := os.WriteFile(mcp, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	install := func(uninstall bool) {
		t.Helper()
		captureStdout(t, func() {
			if err := runInstall(dir, []string{"trae-ide"}, uninstall); err != nil {
				t.Fatal(err)
			}
		})
	}

	// The exact entry, from installTarget with a known binary.
	if _, err := installTarget("trae-ide", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	var root struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(readString(t, mcp)), &root); err != nil {
		t.Fatal(err)
	}
	command, args := mcpCommandArgs("/bin/deja")
	wantArgs := make([]any, len(args))
	for i, a := range args {
		wantArgs[i] = a
	}
	if want := map[string]any{"command": command, "args": wantArgs}; !reflect.DeepEqual(root.MCPServers["deja"], want) {
		t.Errorf("deja entry = %v, want %v (no type key: TRAE IDE rejects it)", root.MCPServers["deja"], want)
	}
	if !reflect.DeepEqual(root.MCPServers["other"], map[string]any{"command": "x", "args": []any{"y"}, "disabled": true}) {
		t.Errorf("the reader's server changed: %v", root.MCPServers["other"])
	}

	install(false)
	first := readString(t, mcp)
	skill := filepath.Join(home, ".trae", "skills", "deja-history", "SKILL.md")
	if !strings.Contains(readString(t, skill), "name: deja-history") {
		t.Errorf("no skill at %s", skill)
	}

	install(false)
	if got := readString(t, mcp); got != first {
		t.Errorf("a second install changed mcp.json:\n%s\nwas\n%s", got, first)
	}

	install(true)
	if got := readString(t, mcp); got != mine {
		t.Errorf("mcp.json after uninstall:\n%q\nwant\n%q", got, mine)
	}
	if _, err := os.Stat(skill); !os.IsNotExist(err) {
		t.Errorf("skill left after uninstall: %v", err)
	}
}

// `deja install trae-ide-auto` puts Claude-shaped hooks in ~/.trae/hooks.json,
// keeps the reader's own, says the IDE runs none of them until hooks are on,
// and leaves TRAE CLI's hooks file alone. Uninstall returns the file byte for
// byte.
func TestInstallTraeIDEAuto(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	hooksPath := filepath.Join(home, ".trae", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	own := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}` + "\n"
	if err := os.WriteFile(hooksPath, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "install", "trae-ide-auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Settings > Hooks") {
		t.Errorf("install did not say where hooks are turned on:\n%s", out)
	}
	first := readString(t, hooksPath)
	var root map[string]any
	if err := json.Unmarshal([]byte(first), &root); err != nil {
		t.Fatal(err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	for _, h := range traeIDEHookWiring {
		if !strings.Contains(traeJSON(t, hooks[h.Event]), h.Sub) {
			t.Errorf("%s has no %s: %v", h.Event, h.Sub, hooks[h.Event])
		}
	}
	if hooks["SessionEnd"] != nil {
		t.Errorf("TRAE IDE has no SessionEnd event: %v", hooks["SessionEnd"])
	}
	if !strings.Contains(traeJSON(t, hooks["Stop"]), "say done") {
		t.Errorf("the reader's Stop hook is gone: %v", hooks["Stop"])
	}
	if _, err := os.Stat(filepath.Join(home, ".trae", "cli", "hooks.json")); err == nil {
		t.Error("trae-ide-auto wrote TRAE CLI's hooks.json")
	}
	if !strings.Contains(readString(t, traeIDEMCPPath()), `"deja"`) {
		t.Error("trae-ide-auto wrote no MCP entry")
	}

	if _, err := captureRun(t, "install", "trae-ide-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, hooksPath); got != first {
		t.Errorf("a second install changed hooks.json:\n%s\nwas\n%s", got, first)
	}

	if _, err := captureRun(t, "uninstall", "trae-ide-auto"); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, hooksPath); got != own {
		t.Errorf("hooks.json after uninstall:\n%q\nwant\n%q", got, own)
	}
}

// The CN build is "Trae CN" with ~/.trae-cn. Where only it has user data,
// every file goes there and none to the international build's.
func TestTraeIDECNEdition(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	cn := filepath.Join(traeIDEAppDir("Trae CN"), "User")
	if err := os.MkdirAll(cn, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := traeIDEMCPPath(); got != filepath.Join(cn, "mcp.json") {
		t.Errorf("mcp path %q", got)
	}
	if got := traeIDEHooksPath(); got != filepath.Join(home, ".trae-cn", "hooks.json") {
		t.Errorf("hooks path %q", got)
	}
	if got := guidancePath("trae-ide"); got != filepath.Join(home, ".trae-cn", "skills", "deja-history", "SKILL.md") {
		t.Errorf("skill path %q", got)
	}
	if _, err := captureRun(t, "install", "trae-ide-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{traeIDEAppDir("Trae"), filepath.Join(home, ".trae")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a CN-only install wrote %s", p)
		}
	}
}

// With both builds on the machine, each gets its own server, skill and hooks,
// and doctor has a row for each. Uninstall gives both sets of files back as
// they were.
func TestInstallTraeIDEBothEditions(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	type files struct{ mcp, hooks, skill string }
	eds := make([]files, 0, len(traeIDEEditions))
	for _, e := range traeIDEEditions {
		f := files{
			filepath.Join(traeIDEAppDir(e.app), "User", "mcp.json"),
			filepath.Join(home, e.dataFolder, "hooks.json"),
			filepath.Join(home, e.dataFolder, "skills", "deja-history", "SKILL.md"),
		}
		eds = append(eds, f)
		for _, p := range []string{f.mcp, f.hooks} {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	ownMCP := `{"mcpServers":{"other":{"command":"x"}}}` + "\n"
	ownHooks := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}` + "\n"
	for _, f := range eds {
		if err := os.WriteFile(f.mcp, []byte(ownMCP), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.hooks, []byte(ownHooks), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := captureRun(t, "install", "trae-ide-auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range eds {
		if !strings.Contains(readString(t, f.mcp), `"deja"`) {
			t.Errorf("%s has no deja server", f.mcp)
		}
		if !strings.Contains(readString(t, f.hooks), "hook-prompt") {
			t.Errorf("%s has no deja hooks", f.hooks)
		}
		if !strings.Contains(readString(t, f.skill), "name: deja-history") {
			t.Errorf("no skill at %s", f.skill)
		}
		if !reportNames(out, f.mcp) || !reportNames(out, f.hooks) {
			t.Errorf("install did not name %s and %s:\n%s", f.mcp, f.hooks, out)
		}
	}
	var mcpRows, hookRows int
	for _, c := range doctorMCPConfigs() {
		if c.name == "trae-ide" && c.wired(c.path) {
			mcpRows++
		}
	}
	for _, a := range autoWirings() {
		if a.name == "trae-ide" {
			if st, _ := autoWiringState(a); st == "wired" {
				hookRows++
			}
		}
	}
	if mcpRows != 2 || hookRows != 2 {
		t.Errorf("doctor wired rows: %d MCP, %d hooks; want 2 and 2", mcpRows, hookRows)
	}

	if _, err := captureRun(t, "uninstall", "trae-ide-auto"); err != nil {
		t.Fatal(err)
	}
	for _, f := range eds {
		if got := readString(t, f.mcp); got != ownMCP {
			t.Errorf("%s after uninstall:\n%q\nwant\n%q", f.mcp, got, ownMCP)
		}
		if got := readString(t, f.hooks); got != ownHooks {
			t.Errorf("%s after uninstall:\n%q\nwant\n%q", f.hooks, got, ownHooks)
		}
		if _, err := os.Stat(f.skill); !os.IsNotExist(err) {
			t.Errorf("skill left at %s", f.skill)
		}
	}
}

// Doctor names both halves and says the hooks may not run: the IDE's switch is
// off by default and not in a file deja can read.
func TestDoctorTraeIDERows(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "trae-ide-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	var auto bytes.Buffer
	doctorAutoRecall(&auto)
	var row string
	for l := range strings.SplitSeq(auto.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "trae-ide ") {
			row = l
		}
	}
	if !strings.Contains(row, "wired") {
		t.Errorf("hooks row %q:\n%s", row, auto.String())
	}
	if !strings.Contains(auto.String(), traeIDEHooksOffNote) {
		t.Errorf("doctor did not say the hooks may be off:\n%s", auto.String())
	}
	for _, c := range doctorMCPConfigs() {
		if c.name == "trae-ide" && !c.wired(c.path) {
			t.Errorf("MCP row reads %s as not wired", c.path)
		}
	}
}

// The PreToolUse payload a live TRAE IDE run sent before a Write, trimmed of
// the file's content. Claude's tool name and file_path, so the file line
// reads it unchanged; llm_tool_name stands in when tool_name is absent.
const traeIDEPreToolUse = `{"session_id":"6ac38f703c8318180a8cd414","cwd":"/p","hook_event_name":"PreToolUse","workspace_roots":["/p"],"agent_id":"solo_agent","agent_type":"solo_agent","tool_use_id":"call_1","tool_name":"Write","llm_tool_name":"Write","tool_input":{"file_path":"/p/client.go","content":"package p"}}`

func TestHookToolReadsTraeIDEPayload(t *testing.T) {
	for _, payload := range []string{
		traeIDEPreToolUse,
		strings.Replace(traeIDEPreToolUse, `"tool_name":"Write",`, "", 1),
	} {
		var in toolHookInput
		if err := json.Unmarshal([]byte(payload), &in); err != nil {
			t.Fatal(err)
		}
		in.adopt()
		if in.ToolName != "Write" || in.ToolInput.FilePath != "/p/client.go" || hookProjectPath(in.CWD, in.WorkspaceRoots) != "/p" {
			t.Errorf("tool %q path %q cwd %q from %s", in.ToolName, in.ToolInput.FilePath, in.CWD, payload)
		}
	}
	var after toolAfterInput
	if err := json.Unmarshal([]byte(`{"hook_event_name":"PostToolUse","llm_tool_name":"Bash","tool_input":{"command":"go test"}}`), &after); err != nil {
		t.Fatal(err)
	}
	if after.LLMToolName != "Bash" {
		t.Errorf("hook-tool-after does not read llm_tool_name: %+v", after)
	}
	// The matchers are tested against Claude's names, as the live run sent them.
	for _, w := range traeIDEHookWiring {
		if w.Event == "PreToolUse" && !regexp.MustCompile("^("+w.Matcher+")$").MatchString("Write") {
			t.Errorf("PreToolUse matcher %q misses Write", w.Matcher)
		}
	}
}

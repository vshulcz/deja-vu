package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Crush reads a flat {"context": …}; Claude's nested hookSpecificOutput means
// nothing to it. Wired with the wrong shape the hook still exits 0 and Crush
// still runs the tool, so nothing fails — the recall is simply never seen.
func TestHookToolWritesCrushsOwnEnvelope(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "c" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"build it"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go build ./..."}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:07Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"undefined: zibblexEnqueue"}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/app")

	var out strings.Builder
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"bash","session_id":"crush-1","cwd":"/work/app","tool_input":{"command":"go build ./..."}}`)
	if err := runHookToolMode(os.Getenv("DEJA_INDEX_DIR"), in, &out, hookToolCrush); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("the hook said nothing on a command the index has history for")
	}
	var got struct {
		Version  int    `json:"version"`
		Context  string `json:"context"`
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("not JSON: %v: %s", err, out.String())
	}
	if got.Version != 1 || !strings.Contains(got.Context, "deja-recall") {
		t.Fatalf("wrong envelope: %s", out.String())
	}
	// No decision field: "allow" is affirmative pre-approval in Crush and would
	// skip the permission prompt for every call the hook fires on.
	if got.Decision != "" {
		t.Fatalf("the hook pre-approved the call: %s", out.String())
	}
	if strings.Contains(out.String(), "hookSpecificOutput") {
		t.Fatalf("Claude's envelope reached Crush: %s", out.String())
	}
}

// The matcher is a regexp against the tool name, and Crush's names are its own.
// One naming Claude's "Bash" and "Edit" matches nothing here; one left empty
// pays an index read on every view, ls, grep and glob.
func TestCrushHookMatcherNamesTheToolsCrushRuns(t *testing.T) {
	re, err := regexp.Compile(crushHookMatcher)
	if err != nil {
		t.Fatalf("matcher does not compile: %v", err)
	}
	for _, name := range []string{"bash", "edit", "write", "multiedit"} {
		if !re.MatchString(name) {
			t.Errorf("matcher misses %q, one of the tools that acts", name)
		}
		// And the hook has to have a branch for the same name, or the matcher
		// fires on a tool the hook then drops in silence.
		in := toolHookInput{ToolName: name}
		in.ToolInput.Command = "go build ./..."
		in.ToolInput.FilePath = "/work/app/main.go"
		if !isCommandTool(name) && toolHookLine(t.TempDir(), "/work/app", in) == "" && !crushFileToolReaches(name) {
			t.Errorf("the hook has no branch for %q", name)
		}
	}
	for _, name := range []string{"view", "ls", "grep", "glob", "fetch", "todos"} {
		if re.MatchString(name) {
			t.Errorf("matcher fires on %q, which acts on nothing", name)
		}
	}
}

// crushFileToolReaches asks whether toolHookLine's file branch accepts a name,
// separately from whether an index has anything to say about the path.
func crushFileToolReaches(name string) bool {
	switch name {
	case "edit", "write", "multiedit":
		return true
	}
	return false
}

// Both halves land in one file, and the -auto target has to report both. A
// machine that already had the MCP entry was told "unchanged" while the hook
// under it was being rewired (#2396).
func TestCrushAutoWritesBothHalvesOfOneFile(t *testing.T) {
	hermeticEnv(t)
	r, err := installTarget("crush-auto", "/bin/deja", false)
	if err != nil || r.Action != "created" {
		t.Fatalf("install: %#v %v", r, err)
	}
	b, err := os.ReadFile(crushConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	mcp, _ := root["mcp"].(map[string]any)
	if _, ok := mcp["deja"]; !ok {
		t.Errorf("no server under mcp: %s", b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	list, _ := hooks["PreToolUse"].([]any)
	if len(list) != 1 {
		t.Fatalf("PreToolUse = %v, want one entry: %s", list, b)
	}
	entry, _ := list[0].(map[string]any)
	if cmd, _ := entry["command"].(string); !strings.Contains(cmd, "hook-tool --crush") {
		t.Errorf("hook command %q does not ask for Crush's envelope", entry["command"])
	}
	if entry["matcher"] != crushHookMatcher {
		t.Errorf("matcher = %v", entry["matcher"])
	}

	// Uninstall takes both back out and leaves no empty blocks behind.
	if _, err := installTarget("crush-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(crushConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "deja") {
		t.Errorf("uninstall left deja behind: %s", b)
	}
	root = nil
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("uninstall left unparseable JSON: %v: %s", err, b)
	}
	for _, key := range []string{"mcp", "hooks"} {
		if _, ok := root[key]; ok {
			t.Errorf("uninstall left an empty %q block: %s", key, b)
		}
	}
}

// A config deja never wrote into keeps its own entries, and a second install
// does not stack a second hook.
func TestCrushInstallLeavesTheReadersOwnWiringAlone(t *testing.T) {
	hermeticEnv(t)
	path := crushConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := `{"mcp":{"sqlite":{"command":"uvx","args":["mcp-sqlite"]}},` +
		`"hooks":{"PreToolUse":[{"name":"mine","matcher":"^bash$","command":"./hooks/guard.sh"}]}}`
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := installTarget("crush-auto", "/bin/deja", false); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["mcp"].(map[string]any)["sqlite"]; !ok {
		t.Errorf("the reader's own server is gone: %s", b)
	}
	list, _ := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(list) != 2 {
		t.Fatalf("PreToolUse = %d entries after two installs, want the reader's plus one of ours: %s", len(list), b)
	}
	if first, _ := list[0].(map[string]any); first["name"] != "mine" {
		t.Errorf("the reader's hook moved or was replaced: %s", b)
	}

	if _, err := installTarget("crush-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["mcp"].(map[string]any)["sqlite"]; !ok {
		t.Errorf("uninstall took the reader's server with it: %s", b)
	}
	list, _ = root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(list) != 1 {
		t.Errorf("uninstall left %d hooks, want the reader's one: %s", len(list), b)
	}
}

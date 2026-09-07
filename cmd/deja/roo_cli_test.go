package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// rooCLITask writes a task in the shape the Roo CLI leaves behind: its own
// storage root, a UUID for a name, and the workspace in history_item.json.
func rooCLITask(t *testing.T, root, id, workspace string) string {
	t.Helper()
	dir := filepath.Join(root, "tasks", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(`[{"role":"user","content":"hi"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	item := `{"id":"` + id + `","ts":1788785794339,"task":"fix the queue","workspace":"` + workspace + `"}`
	if err := os.WriteFile(filepath.Join(dir, "history_item.json"), []byte(item), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Roo grew a CLI (@roo-code/cli 0.1.17): it runs the extension against a VS
// Code shim, keeps its tasks in a store of its own, and takes --session-id.
// deja used to answer "reopen it from the extension's history UI" for every
// roo session, including the ones a terminal had just written.
func TestResumeRooSplitsTheCLIFromTheEditor(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "vscode-mock", "global-storage")
	t.Setenv("DEJA_ROO_CLI_ROOT", cli)
	work := filepath.Join(tmp, "app")
	id := "01a07bf9-8882-7703-a3fa-245deb8ea752"
	path := rooCLITask(t, cli, id, work)

	dir, cmd, err := resumeCommand(model.Session{Harness: "roo", ID: "roo-task-" + id, Project: "app", Path: path})
	if err != nil {
		t.Fatalf("roo CLI resume: %v", err)
	}
	if cmd != "roo --session-id "+id {
		t.Fatalf("cmd = %q", cmd)
	}
	// The CLI lists only tasks whose workspace it is standing in, so the
	// command has to carry the directory the task was in.
	if runtime.GOOS != "windows" && dir != work {
		t.Fatalf("dir = %q, want the workspace %q", dir, work)
	}

	// An editor task lives under the host's globalStorage, and the CLI never
	// lists it: printing a command for it sends someone to "session not found".
	editor := filepath.Join(tmp, "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline")
	ext := rooCLITask(t, editor, "01a07c12-d264-733d-9ac4-b376e75a9cf5", work)
	if _, _, err := resumeCommand(model.Session{Harness: "roo", ID: "roo-task-x", Path: ext}); err == nil {
		t.Fatal("an editor task produced a terminal command")
	}

	// And a task named the old way is not one --session-id accepts: it
	// validates the argument as a UUID before it looks anything up.
	old := rooCLITask(t, cli, "1767225700000", work)
	if _, _, err := resumeCommand(model.Session{Harness: "roo", ID: "roo-task-1767225700000", Path: old}); err == nil {
		t.Fatal("a timestamp-named task produced a --session-id command")
	}
}

// Roo asks before it runs an MCP tool unless the server entry names that tool
// in alwaysAllow. In the editor that is a click before every recall; in the
// CLI's non-interactive mode nobody can click, and the run waits there.
func TestInstallRooAllowsDejasOwnTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "storage")
	t.Setenv("DEJA_ROO_ROOTS", root)
	if err := os.MkdirAll(filepath.Join(root, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "settings", "mcp_settings.json")

	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	allow := rooAllowList(t, path)
	if len(allow) != 1 || allow[0] != "deja" {
		b, _ := os.ReadFile(path)
		t.Fatalf("alwaysAllow = %v, want deja's own tool:\n%s", allow, b)
	}

	// Twice is not twice: a second install leaves the file exactly as it was.
	before, _ := os.ReadFile(path)
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("a second install changed the file:\n%s\n%s", before, after)
	}
}

// A reader who took deja off the list said something, and an install that puts
// it back overrules them.
func TestInstallRooLeavesAnApprovalListAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "storage")
	t.Setenv("DEJA_ROO_ROOTS", root)
	if err := os.MkdirAll(filepath.Join(root, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "settings", "mcp_settings.json")
	before := `{"mcpServers":{"deja":{"command":"/old/deja","args":["mcp"],"alwaysAllow":[]}}}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if allow := rooAllowList(t, path); len(allow) != 0 {
		t.Fatalf("alwaysAllow = %v, want the empty list the reader left", allow)
	}
	// The wiring itself is still adopted — only the approval is theirs.
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "/bin/deja") {
		t.Fatalf("the entry was not adopted:\n%s", b)
	}
}

func rooAllowList(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Servers map[string]struct {
			AlwaysAllow []string `json:"alwaysAllow"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("settings are not JSON Roo can read: %v\n%s", err, b)
	}
	return cfg.Servers["deja"].AlwaysAllow
}

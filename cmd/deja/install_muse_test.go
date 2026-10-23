package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func readMuseSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

// `deja install muse` writes the server into ~/.config/muse/settings.json in
// the shape Muse's own migrate skill writes: typed, and optional so a server
// that fails cannot hold up startup (#4709).
func TestInstallMuseMCP(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	r, err := installTarget("muse", "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "muse", "settings.json")
	if r.Path != want {
		t.Fatalf("path = %q, want %q", r.Path, want)
	}
	servers, _ := readMuseSettings(t, want)["mcpServers"].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	// Windows gets the cmd /c wrapper every stdio entry there carries.
	command, args := mcpCommandArgs("/bin/deja")
	if entry["type"] != "stdio" || entry["command"] != command || entry["mode"] != "optional" {
		t.Fatalf("mcpServers.deja = %v", entry)
	}
	gotArgs, jerr := json.Marshal(entry["args"])
	if jerr != nil {
		t.Fatal(jerr)
	}
	wantArgs, jerr := json.Marshal(args)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if !bytes.Equal(gotArgs, wantArgs) {
		t.Fatalf("args = %s, want %v", gotArgs, args)
	}

	// XDG_CONFIG_HOME moves it, as it moves Muse's own settings.
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if r, err = installTarget("muse", "/bin/deja", false); err != nil || r.Path != filepath.Join(xdg, "muse", "settings.json") {
		t.Fatalf("XDG install = %q, %v", r.Path, err)
	}
}

// Muse loads the legacy mcp_servers block on its own and drops every server
// when both blocks are present, so deja joins the one already there and will
// not add the second.
func TestInstallMuseMCPKeepsToTheFilesBlock(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := filepath.Join(home, ".config", "muse", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"mcp_servers":{"docs":{"type":"stdio","command":"docs-mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("muse", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := readMuseSettings(t, path)
	if _, both := root["mcpServers"]; both {
		t.Fatalf("mcpServers added beside mcp_servers, which switches every server off: %v", root)
	}
	legacy, _ := root["mcp_servers"].(map[string]any)
	if legacy["deja"] == nil || legacy["docs"] == nil {
		t.Fatalf("mcp_servers = %v, want docs and deja", legacy)
	}

	if err := os.WriteFile(path, []byte(`{"mcp_servers":{},"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("muse", "/bin/deja", false); err == nil || !strings.Contains(err.Error(), "both mcp_servers and mcpServers") {
		t.Fatalf("install over both blocks: err = %v, want a refusal naming them", err)
	}
}

// `deja install muse-auto` adds the Claude-shaped hook block to the same file
// and the server beside it, keeps the user's own settings, and an uninstall
// gives back the file byte for byte.
func TestInstallMuseAuto(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	settings := filepath.Join(home, ".config", "muse", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	own := "{\n  \"schema_version\": 1,\n  \"reasoning_effort\": \"xhigh\",\n  \"hooks\": {\n    \"Stop\": [\n      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"say done\"\n          }\n        ]\n      }\n    ]\n  }\n}\n"
	if err := os.WriteFile(settings, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("muse-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := readMuseSettings(t, settings)
	hooks, _ := root["hooks"].(map[string]any)
	for _, w := range []struct{ event, sub, matcher string }{
		{"SessionStart", "hook-context", ""},
		{"UserPromptSubmit", "hook-prompt", ""},
		{"PreToolUse", "hook-tool", "bash|edit_file|write_file"},
		{"PostToolUse", "hook-tool-after", "bash"},
		{"PostToolUseFailure", "hook-tool-after", "bash"},
		{"PreCompact", "hook-precompact", ""},
		{"SessionEnd", "hook-session-end", ""},
	} {
		entries, _ := hooks[w.event].([]any)
		if len(entries) != 1 {
			t.Fatalf("%s: %d entries, want 1", w.event, len(entries))
		}
		entry := typedOrFatal[map[string]any](t, entries[0])
		if m, _ := entry["matcher"].(string); m != w.matcher {
			t.Fatalf("%s matcher = %q, want %q", w.event, m, w.matcher)
		}
		h := typedOrFatal[map[string]any](t, typedOrFatal[[]any](t, entry["hooks"])[0])
		if h["type"] != "command" || !strings.HasSuffix(typedOrFatal[string](t, h["command"]), " "+w.sub) {
			t.Fatalf("%s hook = %v", w.event, h)
		}
		if h["timeout"] != float64(60) {
			t.Fatalf("%s timeout = %v, want 60 seconds", w.event, h["timeout"])
		}
	}
	if root["reasoning_effort"] != "xhigh" || hooks["Stop"] == nil {
		t.Fatalf("the user's settings were not kept: %v", root)
	}
	if servers, _ := root["mcpServers"].(map[string]any); servers["deja"] == nil {
		t.Fatalf("muse-auto wrote no MCP entry: %v", root)
	}

	if _, err := installTarget("muse-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(settings); string(b) != own {
		t.Fatalf("after uninstall:\n%s\nwant the file as it was:\n%s", b, own)
	}
}

func TestMuseIsAnInstallTarget(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	names := " " + strings.Join(installTargetNames(), " ") + " "
	for _, n := range []string{"muse", "muse-auto"} {
		if !strings.Contains(names, " "+n+" ") {
			t.Fatalf("installTargetNames lacks %s", n)
		}
	}
	if autoTargetFor("muse") != "muse-auto" {
		t.Fatalf("--auto maps muse to %q", autoTargetFor("muse"))
	}
	// --auto finds Muse by its session store, not by the config directory
	// deja creates when it installs.
	has := func() bool {
		return slices.Contains(existingTargets(), "muse")
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "muse"), 0o755); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Fatal("an empty ~/.config/muse counted as a Muse machine")
	}
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "muse", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !has() {
		t.Fatal("~/.local/share/muse/sessions did not count as a Muse machine")
	}
}

// Muse's edit_file and write_file name the file under tool_input.path, and
// the pre-edit line looks a file up by it.
func TestHookToolReadsMusePath(t *testing.T) {
	var in toolHookInput
	if err := json.Unmarshal([]byte(`{"hook_event_name":"PreToolUse","tool_name":"edit_file","tool_input":{"path":"internal/pool/acquire.go","find":"a","replace":"b"},"session_id":"s","cwd":"/w/poollab","transcript_path":null}`), &in); err != nil {
		t.Fatal(err)
	}
	in.adopt()
	if in.ToolInput.FilePath != "internal/pool/acquire.go" {
		t.Fatalf("file = %q, want edit_file's path", in.ToolInput.FilePath)
	}
}

func writeMuseResumeLog(t *testing.T, path, id, workspace string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	rec, jerr := json.Marshal(map[string]any{
		"stream":       map[string]any{"kind": "session", "id": id},
		"recorded_at":  1789790400000000,
		"payload_type": "runtime.session.metadata",
		"payload":      map[string]any{"kind": "metadata", "record": map[string]any{"workspace_root": workspace}},
	})
	if jerr != nil {
		t.Fatal(jerr)
	}
	if err := os.WriteFile(path, append(rec, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// `muse resume <id>` reopens a session from anywhere but takes the directory
// it runs in as the workspace, so the command runs in the one the session
// recorded. A child log is not something muse reopens (#4710).
func TestResumeMuseRunsInTheWorkspace(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "w", "poollab")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "01a0ac0d-5355-7c41-bc77-3b55f0e77ea1"
	path := filepath.Join(t.TempDir(), "2026", "10", "05", id, "session.jsonl")
	writeMuseResumeLog(t, path, id, ws)
	dir, cmd, err := resumeCommand(model.Session{Harness: "muse", ID: id, Path: path, Project: "w/poollab"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "muse resume "+id || filepath.Clean(dir) != filepath.Clean(ws) {
		t.Fatalf("dir, cmd = %q, %q", dir, cmd)
	}

	// The workspace is gone: the conversation still reopens, without the cd.
	if rerr := os.RemoveAll(ws); rerr != nil {
		t.Fatal(rerr)
	}
	if dir, cmd, err = resumeCommand(model.Session{Harness: "muse", ID: id, Path: path}); err != nil || dir != "" || cmd != "muse resume "+id {
		t.Fatalf("gone workspace: %q, %q, %v", dir, cmd, err)
	}

	child := "4c579dbf-4e3a-796d-8bcb-de78af441dd5"
	_, _, err = resumeCommand(model.Session{Harness: "muse", ID: child, Kind: "subagent", Parent: id, Path: path})
	if err == nil || !strings.Contains(err.Error(), "deja resume "+id) {
		t.Fatalf("child: err = %v, want a pointer to the parent", err)
	}
}

// A file muse-auto refuses over its MCP blocks gets no hooks either, and an
// uninstall takes deja out of whichever block it ended up in, including one
// the user put beside it after the install.
func TestInstallMuseAutoRefusesWhole(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := filepath.Join(home, ".config", "muse", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	both := `{"mcp_servers":{},"mcpServers":{}}`
	if err := os.WriteFile(path, []byte(both), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("muse-auto", "/bin/deja", false); err == nil {
		t.Fatal("muse-auto installed over both MCP blocks")
	}
	if b, _ := os.ReadFile(path); string(b) != both {
		t.Fatalf("refused install still wrote:\n%s", b)
	}

	if err := os.WriteFile(path, []byte(`{"schema_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("muse", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := readMuseSettings(t, path)
	root["mcp_servers"] = map[string]any{"docs": map[string]any{"command": "docs-mcp"}}
	b, jerr := json.Marshal(root)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("muse", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if servers, _ := readMuseSettings(t, path)["mcpServers"].(map[string]any); servers["deja"] != nil {
		t.Fatalf("uninstall left deja in mcpServers: %v", servers)
	}
}

// Muse writes no settings file of its own and refuses to start on one without
// schema_version, so the file a first install creates carries it, and the
// uninstall takes the file away again. An empty file the reader made comes
// back empty.
func TestInstallMuseSeedsSchemaVersion(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	path := filepath.Join(home, ".config", "muse", "settings.json")
	t.Cleanup(func() { removingWiring = false; createdByThisRun = nil; snapshotsByThisRun = nil })
	uninstall := func(target string) {
		t.Helper()
		removingWiring = true
		defer func() { removingWiring = false }()
		if _, err := installTarget(target, "/bin/deja", true); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"muse", "muse-auto"} {
		r, err := installTarget(target, "/bin/deja", false)
		if err != nil {
			t.Fatal(err)
		}
		if r.Action != "created" {
			t.Errorf("%s: action = %q, want created", target, r.Action)
		}
		if v := readMuseSettings(t, path)["schema_version"]; v != float64(1) {
			t.Fatalf("%s: schema_version = %v, want 1 — Muse will not start without it", target, v)
		}
		uninstall(target)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			b, _ := os.ReadFile(path)
			t.Fatalf("%s: uninstall left the file it created: %s", target, b)
		}
	}

	// A later run: what this one created is no longer the file in question.
	createdByThisRun, snapshotsByThisRun = nil, nil
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("muse-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if v := readMuseSettings(t, path)["schema_version"]; v != float64(1) {
		t.Fatalf("empty file: schema_version = %v, want 1", v)
	}
	uninstall("muse-auto")
	if b, err := os.ReadFile(path); err != nil || len(b) != 0 {
		t.Fatalf("empty file came back as %q (%v), want empty", b, err)
	}
}

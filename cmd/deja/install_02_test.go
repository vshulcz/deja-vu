package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
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

// Four bugs in one week were the same shape: an -auto target writing two files
// and reporting one (#3185, #3220, #3208, #3171). This is the general form —
// install each target on an empty home, list what appeared, and require the
// report to name it, by its own path or by a directory the report named whole
// (#3254).
func TestEveryAutoTargetNamesWhatItWrote(t *testing.T) {
	for _, target := range installTargetNames() {
		if !strings.HasSuffix(target, "-auto") {
			continue
		}
		t.Run(target, func(t *testing.T) {
			tmp := hermeticEnv(t)
			home := filepath.Join(tmp, "home")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			before := absFilesUnder(t, home)
			out, err := captureRun(t, "install", target, "--no-index")
			if err != nil {
				t.Skipf("target refused here: %v", err)
			}
			for p := range absFilesUnder(t, home) {
				if before[p] || dejasOwnBookkeeping(p) {
					continue
				}
				if !reportNames(out, p) {
					t.Errorf("wrote %s and did not say so:\n%s", shortHome(p), out)
				}
			}
		})
	}
}

// dejasOwnBookkeeping is what an install writes for itself rather than for the
// harness: the wiring record, the index, the notes file, the warmup sentinel,
// and the snapshots, which have a line of their own.
func dejasOwnBookkeeping(path string) bool {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, ".bak"), strings.HasPrefix(base, ".deja"):
		return true
	case base == "wiring.json", base == "notes.jsonl":
		return true
	case base == "install.lock":
		// The lock one install holds while it edits, so a second process does
		// not build its write on the state before the first one's (#3691).
		// deja's own file, beside wiring.json, and carrying nothing.
		return true
	case base == "deja-hook", base == "deja-hook.cmd":
		// The launcher the hook entries name (#3422). It lives beside
		// wiring.json under deja's own directory and is deja's plumbing rather
		// than a file in the harness's configuration, which is what the report
		// is a list of.
		return true
	}
	return strings.Contains(path, string(filepath.Separator)+".cache"+string(filepath.Separator)) ||
		strings.Contains(path, "index.db")
}

// reportNames reports whether the install said it wrote this file: the path
// itself, or a directory holding it — named as a directory, not as the head of
// some other path. Both spellings, since the report shortens the home
// directory to ~.
func reportNames(out, path string) bool {
	for p := path; len(p) > 1; p = filepath.Dir(p) {
		if namedWhole(out, p) || namedWhole(out, shortHome(p)) {
			return true
		}
		// The walk stops at the root. On Windows filepath.Dir("C:\\") is
		// "C:\\", so a file the report never named sent this loop round the
		// drive forever — the test hung instead of failing, which is the one
		// outcome a reader of it cannot act on (#3254).
		if next := filepath.Dir(p); next == p {
			break
		}
	}
	return false
}

// namedWhole reports whether the text names this path and stops there. Without
// the boundary, "~/.agents/skills" counts as named because it is the head of
// the deja-history skill's path, and the file beside it goes unmentioned —
// which is the shape #3254 is about.
func namedWhole(out, path string) bool {
	for i := 0; ; {
		j := strings.Index(out[i:], path)
		if j < 0 {
			return false
		}
		end := i + j + len(path)
		if end >= len(out) || (out[end] != '/' && out[end] != '\\') {
			return true
		}
		i = end
	}
}

func absFilesUnder(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		out[p] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Each of the three writes the server where that tool's own source says it
// reads one, and says the one thing the file cannot say for itself (#3651).
func TestInstallKiroWritesTheGlobalSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	res, err := installKiro("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".kiro", "settings", "mcp.json")
	if res.Path != want {
		t.Errorf("path = %q, want %q", res.Path, want)
	}
	assertMCPServerEntry(t, want)
	if mb, rerr := os.ReadFile(want); rerr != nil || !strings.Contains(string(mb), `"autoApprove"`) {
		t.Errorf("kiro mcp.json missing autoApprove: %s (%v)", mb, rerr)
	}
	if werr := os.WriteFile(want, []byte(`{"mcpServers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}}`+"\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if res2, ierr := installKiro("/usr/local/bin/deja", false); ierr != nil || res2.Action != "updated" {
		t.Errorf("upgrade kiro mcp.json = %+v, %v; want Action=updated", res2, ierr)
	}
	// A custom agent does not inherit global servers, and a reader who has one
	// would otherwise find recall missing with nothing saying why.
	if !strings.Contains(res.Note, "agents") {
		t.Errorf("note = %q, want it to mention custom agents", res.Note)
	}
	// includeMcpJson is the switch that gives an agent the global file, and
	// the agent kiro-auto writes sets it, so "copy the entry" was the wrong fix.
	if !strings.Contains(res.Note, `"includeMcpJson": true`) || strings.Contains(res.Note, "copy the deja entry") {
		t.Errorf("note = %q, want the includeMcpJson switch", res.Note)
	}

	// The global steering file is the user-level guidance channel, and it is
	// always-on: every line is in front of every turn, so it stays short.
	steering := filepath.Join(home, ".kiro", "steering", "deja.md")
	sb, err := os.ReadFile(steering)
	if err != nil {
		t.Fatalf("steering: %v", err)
	}
	if !strings.Contains(string(sb), "inclusion: always") {
		t.Errorf("the steering file declares no inclusion mode:\n%s", sb)
	}
	// Four of these lines say which recalled lines to follow and that the rest
	// is asked about first (#5676).
	if n := strings.Count(string(sb), "\n"); n > 18 {
		t.Errorf("the always-on file is %d lines — it costs that on every turn", n)
	}

	out, err := installKiro("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Action == "unchanged" {
		t.Errorf("uninstall changed nothing: %+v", out)
	}
	if b, err := os.ReadFile(want); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("the server is still wired after uninstall: %s", b)
	}
}

func TestInstallKimchiWritesTheAgentDirServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Kimchi sets KIMCHI_CODING_AGENT_DIR itself and never reads
	// XDG_CONFIG_HOME, so neither may move what deja writes.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("KIMCHI_CODING_AGENT_DIR", filepath.Join(home, "moved-agent"))

	if _, err := installKimchi("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "kimchi", "harness")
	assertMCPServerEntry(t, filepath.Join(dir, "mcp.json"))
	if _, err := os.Stat(filepath.Join(dir, "skills", "deja-history", "SKILL.md")); err != nil {
		t.Errorf("the native skill is not there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extensions", "deja.ts")); err == nil {
		t.Error("plain kimchi wrote the extension; that is kimchi-auto's")
	}
}

func TestInstallKimchiAutoAddsTheExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := installKimchiAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(home, ".config", "kimchi", "harness", "extensions", "deja.ts")
	b, err := os.ReadFile(ext)
	if err != nil {
		t.Fatalf("extension: %v", err)
	}
	for _, event := range []string{"before_agent_start", "tool_result", "session_compact"} {
		if !strings.Contains(string(b), event) {
			t.Errorf("the extension answers no %s event", event)
		}
	}
	if again, err := installKimchiAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	} else if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}
	if _, err := installKimchiAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ext); err == nil {
		t.Errorf("the extension survived uninstall: %s", ext)
	}
}

func TestInstallGjcWritesTheServerAndTheNativeSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GJC_CODING_AGENT_DIR", "")

	res, err := installGjc("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatalf("install changed nothing: %+v", res)
	}
	assertMCPServerEntry(t, filepath.Join(home, ".gjc", "agent", "mcp.json"))

	// gjc loads its own skills directory; Claude's and Codex's are import
	// candidates there, so a skill written to those would never be read.
	skill := filepath.Join(home, ".gjc", "agent", "skills", "deja-history", "SKILL.md")
	b, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("skill: %v", err)
	}
	if !strings.Contains(string(b), "deja") {
		t.Errorf("the skill does not mention deja: %q", string(b)[:60])
	}

	// And the slash command, in the directory gjc's own plugin marketplace
	// verifies after an install.
	command := filepath.Join(home, ".gjc", "agent", "commands", "deja.md")
	if cb, err := os.ReadFile(command); err != nil {
		t.Fatalf("command: %v", err)
	} else if !strings.Contains(string(cb), "deja") {
		t.Errorf("the command file does not mention deja: %q", string(cb)[:60])
	}

	if again, err := installGjc("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	} else if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}

	if _, err := installGjc("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skill); err == nil {
		t.Errorf("the skill survived uninstall: %s", skill)
	}
}

// gjc's extension loader is pi's, so the -auto target writes pi's extension
// into gjc's own agent directory — and it has to keep the plain target's three
// files, because the extension alone leaves the model told something it cannot
// follow up on. The skill is the part to watch: senpi moves the same file to
// the shared directory, which gjc reports as source-ignored (#3651).
func TestInstallGjcAutoAddsTheExtensionAndKeepsTheNativeSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GJC_CODING_AGENT_DIR", "")

	if _, err := installGjcAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(home, ".gjc", "agent", "extensions", "deja.ts")
	b, err := os.ReadFile(ext)
	if err != nil {
		t.Fatalf("extension: %v", err)
	}
	// The events gjc's own loader hands an extension module, not a guess at
	// them: an extension answering something else is loaded and silent.
	for _, event := range []string{"before_agent_start", "tool_result", "session_compact"} {
		if !strings.Contains(string(b), event) {
			t.Errorf("the extension answers no %s event", event)
		}
	}
	skill := filepath.Join(home, ".gjc", "agent", "skills", "deja-history", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("the native skill is not there: %v", err)
	}
	assertMCPServerEntry(t, filepath.Join(home, ".gjc", "agent", "mcp.json"))

	if again, err := installGjcAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	} else if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}

	if _, err := installGjcAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ext); err == nil {
		t.Errorf("the extension survived uninstall: %s", ext)
	}
}

// assertMCPServerEntry is the shape every client here reads: a `mcpServers`
// block with deja in it, ending in `mcp`. windows gets the `cmd /c` shim, so
// the binary may be an argument rather than the command.
func assertMCPServerEntry(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var cfg struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("%s is not the shape the client reads: %v", path, err)
	}
	srv, ok := cfg.Servers["deja"]
	if !ok {
		t.Fatalf("%s: no deja server in %v", path, cfg.Servers)
	}
	if len(srv.Args) == 0 || srv.Args[len(srv.Args)-1] != "mcp" {
		t.Errorf("%s: entry = %q %v, want it to end in `mcp`", path, srv.Command, srv.Args)
	}
	if !strings.Contains(srv.Command+" "+strings.Join(srv.Args, " "), "deja") {
		t.Errorf("%s: entry = %q %v, want the binary in it", path, srv.Command, srv.Args)
	}
}

func TestInstallOmpAutoWritesAnExtensionModule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	r, err := installOmpAuto("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".omp", "agent", "extensions", "deja", "index.js")
	if r.Path != want {
		t.Fatalf("wrote %q, want %q — omp discovers modules one directory deep under extensions/", r.Path, want)
	}
	body, err := os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	js := string(body)

	// The context event is where a per-prompt block goes in: it hands over the
	// message list on the way to the provider, and `input` never fires in print
	// mode. The once-per-session digest goes in through before_agent_start,
	// which stores what it returns as a message of its own.
	if !strings.Contains(js, `pi.on("context"`) {
		t.Errorf("the extension listens on no event that can inject:\n%s", js)
	}
	if !strings.Contains(js, "return { messages: out }") {
		t.Errorf("the handler returns nothing, so the injection is dropped:\n%s", js)
	}
	if !strings.Contains(js, `"hook-prompt", "--plain"`) {
		t.Errorf("the extension does not ask deja for a block:\n%s", js)
	}
	// Extensions run in-process with no isolation: a throw here takes the whole
	// session with it, so the call has to be guarded.
	if !strings.Contains(js, "catch") {
		t.Errorf("the deja call is unguarded:\n%s", js)
	}
	// The same prompt reaches the handler once per provider request, so the
	// answer is cached against the prompt itself rather than merely stored.
	if !strings.Contains(js, "prompt !== asked") {
		t.Errorf("no guard on the cached prompt, so deja runs again for every provider request:\n%s", js)
	}
}

// omp's bash tool leaves isError false on a command that exits non-zero and
// reports the exit code in details.exitCode instead. Measured on omp 18.1.12: a
// failing build arrived as tool_result isError=false, details.exitCode=1, so a
// repair gated on isError alone never speaks.
func TestOmpRepairsCommandsThatExitedNonZero(t *testing.T) {
	js := ompExtensionJS("/bin/deja")
	for _, want := range []string{
		`pi.on("tool_result"`,
		`"hook-tool-after", "--plain"`,
		`typeof event.details.exitCode === "number"`,
		"event.toolCallId",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("no fix pair at the point of action, missing %q:\n%s", want, js)
		}
	}
}

// Same seam as pi: nothing runs before an edit whose return the model reads, so
// the file's history goes out on the read that precedes it.
func TestOmpCarriesFileHistoryOnRead(t *testing.T) {
	js := ompExtensionJS("/bin/deja")
	for _, want := range []string{
		`event.toolName === "read"`,
		`"hook-tool", "--plain"`,
		"event.input.path",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("no file history at the read, missing %q:\n%s", want, js)
		}
	}
}

// Compaction drops the blocks this session was shown; the list that keeps them
// from repeating outlives it. omp emits session_compact — the name in its own
// extension types — and a handler on anything else never runs.
func TestOmpForgetsAfterCompaction(t *testing.T) {
	js := ompExtensionJS("/bin/deja")
	if !strings.Contains(js, `pi.on("session_compact"`) {
		t.Fatalf("nothing forgets after compaction:\n%s", js)
	}
	if !strings.Contains(js, "hook-precompact") {
		t.Fatalf("session_compact is wired to something other than the forget hook:\n%s", js)
	}
}

// Without the session digest, omp starts every session empty and only speaks
// when a prompt happens to match. The digest is what makes the first answer
// carry the project's recent history.
func TestOmpInjectsTheSessionDigestOnce(t *testing.T) {
	js := ompExtensionJS("/bin/deja")
	if !strings.Contains(js, `pi.on("before_agent_start"`) || !strings.Contains(js, "hook-context") {
		t.Fatalf("no session digest:\n%s", js)
	}
	if !strings.Contains(js, "if (injected.has(key)) return;") {
		t.Fatalf("the digest is not held to one per session:\n%s", js)
	}
	if !strings.Contains(js, "customType: \"deja-recall\"") {
		t.Fatalf("the digest is not returned as a message, so omp drops it:\n%s", js)
	}
}

func TestOmpExtensionQuotesTheBinaryPath(t *testing.T) {
	js := ompExtensionJS(`C:\Program Files\deja\deja.exe`)
	if !strings.Contains(js, `"C:\\Program Files\\deja\\deja.exe"`) {
		t.Errorf("a Windows path was not escaped for JavaScript:\n%s", js)
	}
}

func TestUninstallOmpAutoRemovesTheModule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := installOmpAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	r, err := installOmpAuto("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "removed" {
		t.Errorf("action = %q, want removed", r.Action)
	}
	if _, serr := os.Stat(filepath.Dir(r.Path)); !os.IsNotExist(serr) {
		t.Errorf("the extension directory survived uninstall: %v", serr)
	}

	// Uninstalling again on a machine that has none must not fail or create one.
	again, err := installOmpAuto("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" {
		t.Errorf("second uninstall action = %q, want unchanged", again.Action)
	}
}

// openclawIncludeStand is a state dir whose openclaw.json keeps sections in
// included files, each written as given, and a deja binary that exists.
func openclawIncludeStand(t *testing.T, cfg string, included map[string]string) (exe string) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
	dir := filepath.Dir(openclawConfigPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"openclaw.json": cfg}
	maps.Copy(files, included)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe = filepath.Join(tmp, "deja")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// The refusal told the reader to add entries running a launcher, a plugin
// directory and a hook pack that nothing had written, and a rerun refused
// again, so the hand-added entries pointed at nothing (#5331). Every path it
// names is on disk once it is said, and neither openclaw.json nor the file it
// includes is touched.
func TestOpenClawIncludeRefusalWritesWhatItNames(t *testing.T) {
	entry := regexp.MustCompile(`"deja": (\{"args":\[[^\]]*\],"command":"(?:[^"\\]|\\.)*"\})`)
	paths := regexp.MustCompile(`"paths": (\[[^\]]*\])`)
	for name, tc := range map[string]struct {
		target, cfg, inc string
	}{
		"mcp":       {"openclaw", `{"mcp": {"$include": "./inc.json5"}}`, `{}`},
		"mcp, auto": {"openclaw-auto", `{"mcp": {"$include": "./inc.json5"}}`, `{}`},
		"plugins":   {"openclaw-auto", `{"plugins": {"$include": "./inc.json5"}}`, `{"allow": ["x"]}`},
		"hooks":     {"openclaw-auto", `{"hooks": {"$include": "./inc.json5"}}`, `{"internal": {}}`},
	} {
		t.Run(name, func(t *testing.T) {
			exe := openclawIncludeStand(t, tc.cfg, map[string]string{"inc.json5": tc.inc})
			_, err := installTarget(tc.target, exe, false)
			if err == nil {
				t.Fatal("install wrote beside the $include")
			}
			msg := err.Error()
			var named []string
			m := entry.FindStringSubmatch(msg)
			if m == nil {
				t.Fatalf("the refusal names no MCP entry:\n%s", msg)
			}
			var e struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			}
			if err := json.Unmarshal([]byte(m[1]), &e); err != nil {
				t.Fatal(err)
			}
			cmd := e.Command
			if cmd == "cmd" && len(e.Args) > 1 { // Windows: cmd /c <deja> mcp
				cmd = e.Args[1]
			}
			named = append(named, cmd)
			if runtime.GOOS != "windows" && cmd != dejaLauncherPath() {
				t.Errorf("the MCP entry runs %s, not the launcher", cmd)
			}
			auto := tc.target == "openclaw-auto"
			if m := paths.FindStringSubmatch(msg); auto {
				if m == nil {
					t.Fatalf("the refusal names no plugin load path:\n%s", msg)
				}
				var ps []string
				if err := json.Unmarshal([]byte(m[1]), &ps); err != nil {
					t.Fatal(err)
				}
				named = append(named, ps...)
				named = append(named, filepath.Join(openclawPluginDir(), "index.mjs"), filepath.Join(openclawHookDir(), "handler.js"))
				for _, want := range []string{openclawAccessKey, `"` + openclawHookName + `"`, `"command":`} {
					if !strings.Contains(msg, want) {
						t.Errorf("the refusal leaves out %s, which the install did not write either:\n%s", want, msg)
					}
				}
			}
			for _, p := range named {
				if _, err := os.Stat(p); err != nil {
					t.Errorf("the refusal names %s, which is not there: %v", p, err)
				}
			}
			if !auto {
				if _, err := os.Stat(openclawPluginDir()); err == nil {
					t.Error("the MCP-only target wrote the plugin")
				}
			}
			dir := filepath.Dir(openclawConfigPath())
			if got := readFile(t, openclawConfigPath()); got != tc.cfg {
				t.Errorf("a refused install changed openclaw.json:\n%s", got)
			}
			if got := readFile(t, filepath.Join(dir, "inc.json5")); got != tc.inc {
				t.Errorf("a refused install changed the included file:\n%s", got)
			}
		})
	}
}

// Doctor reads the included files: the files alone are not a wiring, and the
// entries the refusal asked for, once added by hand, are (#5331).
func TestOpenClawDoctorReadsIncludedWiring(t *testing.T) {
	exe := openclawIncludeStand(t, `{"mcp": {"$include": "./mcp.json5"}, "plugins": {"$include": "./plugins.json5"}}`,
		map[string]string{"mcp.json5": `{}`, "plugins.json5": `{}`})
	if _, err := installTarget("openclaw-auto", exe, false); err == nil {
		t.Fatal("install wrote beside the $include")
	}
	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "openclaw" {
			row = a
		}
	}
	if st, _ := autoWiringState(row); st != "stale" {
		t.Errorf("deja's files with no entry naming them read as %q, want stale", st)
	}
	if doctorOpenClawWired(openclawConfigPath()) {
		t.Error("the MCP row reads wired with no entry anywhere")
	}

	dir := filepath.Dir(openclawConfigPath())
	hook := hookCommandExe(exe)
	write := func(name string, v any) {
		b, jerr := json.Marshal(v)
		if jerr != nil {
			t.Fatal(jerr)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("mcp.json5", map[string]any{"servers": map[string]any{"deja": map[string]any{"command": hook, "args": []string{"mcp"}}}})
	write("plugins.json5", map[string]any{
		"entries": map[string]any{openclawPluginID: map[string]any{"enabled": true}},
		"load":    map[string]any{"paths": []string{openclawPluginDir()}},
	})
	write("openclaw.json", map[string]any{
		"mcp":     map[string]any{"$include": "./mcp.json5"},
		"plugins": map[string]any{"$include": "./plugins.json5"},
		"hooks":   map[string]any{"internal": map[string]any{"enabled": true, "entries": map[string]any{openclawHookName: map[string]any{"enabled": true}}}},
	})
	if st, _ := autoWiringState(row); st != "wired" {
		t.Errorf("wired by hand as the refusal said, the row reads %q", st)
	}
	if !doctorOpenClawWired(openclawConfigPath()) {
		t.Error("the MCP entry in the included file reads as not wired")
	}

	// Uninstall takes deja's files and says what it left in the included ones.
	before := readFile(t, filepath.Join(dir, "plugins.json5"))
	res, err := installTarget("openclaw-auto", exe, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{openclawPluginDir(), openclawHookDir()} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("uninstall left %s", p)
		}
	}
	for _, want := range []string{"mcp.servers.deja in", "mcp.json5", "plugins.entries.deja in", "plugins.json5"} {
		if !strings.Contains(res.Note, want) {
			t.Errorf("the uninstall note does not say %q:\n%s", want, res.Note)
		}
	}
	if got := readFile(t, filepath.Join(dir, "plugins.json5")); got != before {
		t.Errorf("uninstall edited the included file:\n%s", got)
	}
}

// A switch kept in an included file is OpenClaw's as much as one in
// openclaw.json: doctor reported a wired, running deja while the included
// file turned the MCP entry, the server list or the hook pack off (#5339).
func TestOpenClawDoctorReadsSwitchesInIncludedFiles(t *testing.T) {
	cfg := `{"mcp": {"$include": "./mcp.json5"}, "plugins": {"$include": "./plugins.json5"}, "hooks": {"$include": "./hooks.json5"}, "tools": {"$include": "./tools.json5"}}`
	for name, tc := range map[string]struct {
		file, body string
		off        func() string // the line doctor prints, "" when on
	}{
		"tools.deny":             {"tools.json5", `{"deny": ["bundle-mcp"]}`, func() string { return clientMCPDenied("openclaw") }},
		"hooks.internal.enabled": {"hooks.json5", `{"internal": {"enabled": false, "entries": {"deja-recall": {"enabled": true}}}}`, func() string { return clientHooksOff("openclaw") }},
		"plugin grant":           {"plugins.json5", `{"entries": {"deja": {"enabled": true}}}`, func() string { return clientHooksOff("openclaw") }},
		"mcp entry enabled": {"mcp.json5", `{"servers": {"deja": {"command": "deja", "args": ["mcp"], "enabled": false}}}`, func() string {
			if dejaEntrySwitchedOff("openclaw", openclawConfigPath()) {
				return "mcp.json5"
			}
			return ""
		}},
	} {
		t.Run(name, func(t *testing.T) {
			ok := map[string]string{
				"mcp.json5":     `{"servers": {"deja": {"command": "deja", "args": ["mcp"]}}}`,
				"plugins.json5": `{"entries": {"deja": {"enabled": true, "hooks": {"` + openclawAccessKey + `": true}}}}`,
				"hooks.json5":   `{"internal": {"enabled": true, "entries": {"deja-recall": {"enabled": true}}}}`,
				"tools.json5":   `{}`,
			}
			openclawIncludeStand(t, cfg, ok)
			t.Setenv("PATH", t.TempDir()) // the grant check asks the installed openclaw's version
			if !doctorOpenClawWired(openclawConfigPath()) {
				t.Fatal("the MCP entry in the included file reads as not wired")
			}
			if note := tc.off(); note != "" {
				t.Fatalf("with every included file on, doctor reads a switch off: %s", note)
			}
			dir := filepath.Dir(openclawConfigPath())
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			note := tc.off()
			if note == "" {
				t.Fatalf("%s in %s reads as on", name, tc.file)
			}
			if !strings.Contains(note, tc.file) {
				t.Errorf("the line does not name %s, where the switch is: %s", tc.file, note)
			}
		})
	}

	// Two files in one $include: the line names the one that sets the switch.
	openclawIncludeStand(t, `{"hooks": {"$include": ["./a.json5", "./b.json5"]}}`,
		map[string]string{"a.json5": `{}`, "b.json5": `{"internal": {"enabled": false}}`})
	if note := clientHooksOff("openclaw"); !strings.Contains(note, "b.json5") {
		t.Errorf("the switch is in b.json5, the line says: %s", note)
	}
}

// A section OpenClaw keeps in its own file is refused, not written beside:
// a key next to the $include turned OpenClaw's own edits of that section into
// overrides in openclaw.json, and they stayed after uninstall (#5258).
func TestOpenClawInstallRefusesASectionKeptInAnInclude(t *testing.T) {
	for name, tc := range map[string]struct {
		target, cfg string
		names       []string // what the refusal has to name
	}{
		"mcp":            {"openclaw", `{"mcp": {"$include": "./mcp.json5"}}`, []string{"mcp comes from", "mcp.json5", "under servers", `"deja": {"args":[`}},
		"mcp, auto":      {"openclaw-auto", `{"mcp": {"$include": "./mcp.json5"}}`, []string{"mcp comes from", "mcp.json5"}},
		"plugins":        {"openclaw-auto", `{"plugins": {"$include": "./plugins.json5"}}`, []string{"plugins comes from", "plugins.json5", "under entries", "under load", `"paths"`}},
		"hooks":          {"openclaw-auto", `{"hooks": {"$include": ["./a.json5", "./b.json5"]}}`, []string{"hooks comes from", "a.json5", "b.json5", "under internal"}},
		"servers nested": {"openclaw", `{"mcp": {"servers": {"$include": "./oc/servers.json5"}}}`, []string{"mcp.servers comes from", "servers.json5", "add there"}},
	} {
		t.Run(name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
			path := openclawConfigPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := installTarget(tc.target, "/bin/deja", false)
			if err == nil {
				t.Fatalf("install wrote beside the $include:\n%s", readFile(t, path))
			}
			for _, want := range append(tc.names, "in place of the $include") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q:\n%v", want, err)
				}
			}
			if got := readFile(t, path); got != tc.cfg {
				t.Errorf("a refused install changed openclaw.json:\n%s", got)
			}
		})
	}
}

// Controls: a root $include, which OpenClaw never writes through, and a
// section beside one that deja does not write, install as before; and an
// entry an older install left beside a $include still comes out.
func TestOpenClawInstallBesideOtherIncludes(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
	path := openclawConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []string{`{"$include": "./base.json5"}`, `{"agents": {"$include": "./agents.json5"}}`} {
		if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installTarget("openclaw-auto", "/bin/deja", false); err != nil {
			t.Fatalf("%s: %v", cfg, err)
		}
		if !strings.Contains(readFile(t, path), `"deja"`) {
			t.Fatalf("%s: install wrote nothing:\n%s", cfg, readFile(t, path))
		}
		if _, err := installTarget("openclaw-auto", "/bin/deja", true); err != nil {
			t.Fatal(err)
		}
	}
	old := `{"mcp": {"$include": "./mcp.json5", "servers": {"deja": {"command": "/bin/deja", "args": ["mcp"]}}}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("openclaw", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); strings.Contains(got, `"deja"`) || !strings.Contains(got, "mcp.json5") {
		t.Errorf("uninstall did not take out the entry an older install left:\n%s", got)
	}
}

// OpenClaw reads openclaw.json as JSON5. A file with unquoted keys was refused
// with a JSON error pointing at a key or at the comment above it, and one with
// trailing commas installed and then could not be uninstalled (#4557).
func TestOpenClawJSON5Config(t *testing.T) {
	seed := func(t *testing.T, text string) string {
		t.Helper()
		hermeticEnv(t)
		p := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("unquoted keys are refused naming JSON5", func(t *testing.T) {
		for _, text := range []string{
			"{\n  agents: {defaults: {model: 'anthropic/x'}},\n}\n",
			"// mine\n{\n  agents: {defaults: {model: 'anthropic/x'}},\n}\n",
		} {
			for _, target := range []string{"openclaw", "openclaw-auto"} {
				p := seed(t, text)
				_, err := captureRun(t, "install", target, "--no-index", "--no-guidance")
				// Past the path: the test's own temp dir has the word in it.
				if err == nil || !strings.Contains(err.Error(), "as JSON5") {
					t.Errorf("%s: refusal does not name JSON5: %v", target, err)
				}
				if got, _ := os.ReadFile(p); string(got) != text {
					t.Errorf("%s: config changed:\n%s", target, got)
				}
			}
		}
	})
	t.Run("trailing commas uninstall the way they install", func(t *testing.T) {
		text := "{\n  \"agents\": {\"defaults\": {\"model\": \"anthropic/x\"}},\n}\n"
		p := seed(t, text)
		if _, err := captureRun(t, "install", "openclaw-auto", "--no-index", "--no-guidance"); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "uninstall", "openclaw-auto"); err != nil {
			t.Fatalf("uninstall refused what install accepted: %v", err)
		}
		if got, _ := os.ReadFile(p); string(got) != text {
			t.Errorf("uninstall did not give the file back:\n%q\nwant\n%q", got, text)
		}
	})
}

// OpenClaw 2026.7.1-2 warns twice on every start about a plugin it found only
// by scanning extensions/: the open allow list, and no install or load-path
// provenance. A plugin named in plugins.load.paths is origin "config", which
// both checks accept, and that leaves the user's allow list alone (#4579).
func TestOpenClawPluginInstallNamesItsLoadPath(t *testing.T) {
	for _, tc := range []struct{ name, before string }{
		{"plain", `{"plugins":{"load":{"paths":["/opt/theirs"]}}}` + "\n"},
		{"jsonc", "{\n  // mine\n  \"plugins\": {\n    \"load\": {\n      \"paths\": [\"/opt/theirs\"]\n    }\n  }\n}\n"},
		{"none", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			state := sources.OpenClawStateDir()
			if err := os.MkdirAll(state, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(state, "openclaw.json")
			if tc.before != "" {
				if err := os.WriteFile(cfg, []byte(tc.before), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			dir := filepath.Join(state, "extensions", openclawPluginID)
			paths := openclawLoadPathsIn(t, cfg)
			if !slices.Contains(paths, dir) {
				t.Fatalf("plugins.load.paths does not name %s, so OpenClaw warns about it on every start: %v", dir, paths)
			}
			if tc.before != "" && !slices.Contains(paths, "/opt/theirs") {
				t.Errorf("their load path is gone: %v", paths)
			}
			// Twice is once.
			if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			if got := openclawLoadPathsIn(t, cfg); len(got) != len(paths) {
				t.Errorf("a second install added the path again: %v", got)
			}
			if _, err := installOpenClawPlugin("/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			after, _ := os.ReadFile(cfg)
			if strings.Contains(string(after), "extensions") {
				t.Errorf("uninstall left the load path behind:\n%s", after)
			}
			if tc.before != "" && string(after) != tc.before {
				t.Errorf("uninstall did not give the file back:\n%s\nwant:\n%s", after, tc.before)
			}
		})
	}
}

func openclawLoadPathsIn(t *testing.T, cfg string) []string {
	t.Helper()
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Plugins struct {
			Load struct {
				Paths []string `json:"paths"`
			} `json:"load"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
		t.Fatalf("config does not parse: %v\n%s", err, b)
	}
	return root.Plugins.Load.Paths
}

// Taking out a whole block that is the last key in a JSONC file has to take
// the comma in front of it too, or the next run refuses the file.
func TestOpenClawPluginUninstallDropsLastBlockCleanly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	state := sources.OpenClawStateDir()
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(state, "openclaw.json")
	before := "{\n  // mine\n  \"gateway\": {\"mode\": \"local\"},\n  \"plugins\": {\"load\": {\"paths\": []}}\n}\n"
	if err := os.WriteFile(cfg, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installOpenClawPlugin("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	after, _ := os.ReadFile(cfg)
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(after))), &root); err != nil {
		t.Fatalf("uninstall left a config that does not parse: %v\n%s", err, after)
	}
	if _, ok := root["gateway"]; !ok {
		t.Errorf("their gateway block is gone:\n%s", after)
	}
}

// The user's load paths are theirs, comments in the list included: install
// adds deja's path beside them and uninstall takes only that back out.
func TestOpenClawPluginLoadPathKeepsACommentInTheList(t *testing.T) {
	for _, before := range []string{
		"{\n  // mine\n  \"plugins\": {\n    \"load\": {\n      \"paths\": [\n        // the work plugin\n        \"/opt/theirs\"\n      ]\n    }\n  }\n}\n",
		"{\n  // mine\n  \"plugins\": {\"load\": {\"paths\": [\"/opt/theirs\" /* keep */]}}\n}\n",
	} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		state := sources.OpenClawStateDir()
		if err := os.MkdirAll(state, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := filepath.Join(state, "openclaw.json")
		if err := os.WriteFile(cfg, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
			t.Fatalf("install: %v", err)
		}
		mid, _ := os.ReadFile(cfg)
		if !strings.Contains(string(mid), "the work plugin") && !strings.Contains(string(mid), "/* keep */") {
			t.Errorf("install dropped the comment in their load paths:\n%s", mid)
		}
		if paths := openclawLoadPathsIn(t, cfg); !slices.Contains(paths, filepath.Join(state, "extensions", openclawPluginID)) || !slices.Contains(paths, "/opt/theirs") {
			t.Errorf("load paths after install: %v", paths)
		}
		if _, err := installOpenClawPlugin("/bin/deja", true); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
		if after, _ := os.ReadFile(cfg); string(after) != before {
			t.Errorf("uninstall did not give the file back:\n%s\nwant:\n%s", after, before)
		}
	}
}

func TestInstallOpenClawPluginWritesWhatTheLoaderNeeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(sources.OpenClawStateDir(), "extensions", "deja")
	// Both files or neither: openclaw fails the load with a validation error
	// when one is missing rather than ignoring the plugin.
	for _, name := range []string{"package.json", "openclaw.plugin.json", "index.mjs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("plugin missing %s: %v", name, err)
		}
	}
	var pkg struct {
		OpenClaw struct {
			Extensions []string `json:"extensions"`
		} `json:"openclaw"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if uerr := json.Unmarshal(b, &pkg); uerr != nil {
		t.Fatalf("package.json is not valid JSON: %v", uerr)
	}
	if len(pkg.OpenClaw.Extensions) == 0 {
		t.Error("package.json declares no openclaw.extensions, which is the field " +
			"the installer rejects a plugin for")
	}

	// Discovery is not enough — the entry is what turns the plugin on.
	cfg := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(body), `"deja"`) {
		t.Errorf("plugin not enabled in the config:\n%s", body)
	}
	// The user's trust list is theirs to write.
	if strings.Contains(string(body), `"allow"`) {
		t.Errorf("install wrote a plugins.allow entry:\n%s", body)
	}

	if _, err := installOpenClawPlugin("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("plugin survived uninstall: %v", err)
	}
	after, _ := os.ReadFile(cfg)
	if strings.Contains(string(after), "deja") {
		t.Errorf("uninstall left the plugin enabled:\n%s", after)
	}
}

// openclaw keeps other people's plugins in the same config file, so the entry
// deja adds and removes must not take theirs with it.
func TestInstallOpenClawPluginLeavesOtherPluginsAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	state := sources.OpenClawStateDir()
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(state, "openclaw.json")
	theirs := `{"plugins":{"allow":["theirs"],"entries":{"theirs":{"enabled":true}}}}` + "\n"
	if err := os.WriteFile(cfg, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installOpenClawPlugin("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	var root map[string]any
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("config is no longer valid JSON: %v", err)
	}
	plugins, _ := root["plugins"].(map[string]any)
	entries, _ := plugins["entries"].(map[string]any)
	if _, ok := entries["theirs"]; !ok {
		t.Errorf("their plugin entry is gone:\n%s", b)
	}
	if _, ok := plugins["allow"]; !ok {
		t.Errorf("their allow list is gone:\n%s", b)
	}
}

// The plugin's contract is narrow enough to be worth exercising: openclaw
// hands before_prompt_build the prompt and uses only prependContext and the
// system-context fields from what it returns. A handler that returns the
// recall in the wrong shape looks installed and injects nothing.
func TestOpenClawPluginRecallsForThePrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	// Answers only the question the corpus knows about, so silence is checked
	// on the same stub rather than a second one.
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$in\" >> " + calls + "\n" +
		"case \"$in\" in *rotate*) printf 'RECALLED every 47 days' ;; esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "index.mjs")
	if werr := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import plugin from "` + plugin + `";
let handler;
plugin.register({ on: (name, fn) => { if (name === "before_prompt_build") handler = fn } });
if (!handler) { console.log("NOHOOK"); process.exit(0) }
const asked = await handler({ prompt: "how often does the token rotate?", messages: [] });
const quiet = await handler({ prompt: "something the history never saw", messages: [] });
console.log(JSON.stringify(asked));
console.log(JSON.stringify(quiet ?? null));
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		t.Fatalf("driver said %q", out)
	}
	if lines[0] == "NOHOOK" {
		t.Fatal("the plugin registers no before_prompt_build handler, so it never " +
			"sees a prompt")
	}
	var got map[string]string
	if uerr := json.Unmarshal([]byte(lines[0]), &got); uerr != nil {
		t.Fatalf("handler returned %s", lines[0])
	}
	if !strings.Contains(got["prependContext"], "RECALLED") {
		t.Errorf("recall did not come back as prependContext, which is the only "+
			"field openclaw reads here: %s", lines[0])
	}
	// A hook that talks on every prompt is wallpaper; deja stays silent when
	// the history has nothing.
	if lines[1] != "null" {
		t.Errorf("the plugin injected something for a prompt with no match: %s", lines[1])
	}
	body, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the plugin never ran deja: %v", err)
	}
	if !strings.Contains(string(body), "how often does the token rotate?") {
		t.Errorf("the prompt never reached the hook:\n%s", body)
	}
}

// Compaction throws away the blocks a session was shown, and the list that
// stops them repeating outlives it. before_compaction fires in the plugin —
// measured on OpenClaw 2026.7.1-2, with the session id on the hook context.
func TestOpenClawPluginForgetsOnCompaction(t *testing.T) {
	js := openclawPluginJS("/bin/deja")
	if !strings.Contains(js, `"before_compaction"`) {
		t.Fatalf("nothing forgets after a compaction:\n%s", js)
	}
	// The session it sends is checked by driving the plugin
	// (TestOpenClawPluginStampsTheTranscriptID); here, that the capture is told
	// the file and whose store it is.
	if !strings.Contains(js, `ask(["hook-precompact"], {
          session_id: at.id,
          transcript_path: event?.sessionFile || "",`) ||
		!strings.Contains(js, `harness: "openclaw"`) {
		t.Fatalf("the forget is not the one deja means — no hook-precompact with this session:\n%s", js)
	}
}

// OpenClaw's own docs call before_agent_start a compatibility-only combined
// phase and ask new plugins to use the explicit ones. agent_turn_prepare is
// also where queued next-turn injections are drained, so the digest sits where
// that seam would arrive if it ever starts delivering.
func TestOpenClawPluginUsesThePhaseHooks(t *testing.T) {
	js := openclawPluginJS("/bin/deja")
	if strings.Contains(js, `"before_agent_start"`) {
		t.Fatalf("the digest is still on the compatibility hook:\n%s", js)
	}
	for _, want := range []string{`"agent_turn_prepare"`, `"before_prompt_build"`} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %s:\n%s", want, js)
		}
	}
}

// deja's receipt reaches the person in a 1:1 chat with the owner: the prompt
// hook asks once for the JSON reply, the model gets what --plain prints, and
// the final reply on its way to Telegram carries one short receipt line as a
// context block. Everything else stays --plain and leaves the reply alone: a
// group, a group whose channel says isGroup false, the TUI, receipt:false, a
// stranger's DM after an owner turn that never replied (every DM shares
// agent:main:main), a block-stream chunk, and a DM on a channel that streams
// its replies, where deja is not asked for a receipt that could not show.
func TestOpenClawPluginShowsTheReceiptInAnOwnerDM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	reply := `{"systemMessage":"deja-vu — from Codex CLI, 2 weeks ago: \"zorbit drain\" (via zorbit, drain)","hookSpecificOutput":{"additionalContext":"RECALLED drain with --grace 40"}}`
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$*\" >> \"$DEJA_CALLS\"\n" +
		"case \"$in\" in *zorbit*) ;; *) exit 0 ;; esac\n" +
		"case \"$*\" in\n" +
		"  'hook-prompt --plain') printf 'RECALLED drain with --grace 40' ;;\n" +
		"  hook-prompt) printf '%s\\n' '" + reply + "' ;;\n" +
		"esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "index.mjs")
	if werr := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
const calls = process.env.DEJA_CALLS;
const owner = { channels: { telegram: { allowFrom: ["4242"], streaming: { mode: "off" } }, googlechat: { allowFrom: ["4242"], streaming: "off" } } };
// Each case is a run of turns on one fresh plugin; the last turn's reply is judged.
const cases = {
  dm: [{}],
  group: [{ key: "agent:main:telegram:group:-100", conv: "-100", isGroup: true }],
  unnamedGroup: [{ key: "agent:main:googlechat:group:spaces-a", channel: "googlechat", conv: "spaces-a", isGroup: false }],
  tui: [{ channel: "webchat" }],
  off: [{ config: { receipt: false } }],
  stranger: [{ kinds: [] }, { sender: "7", prompt: "zorbit, hi" }],
  block: [{ kinds: ["block"] }],
  streaming: [{ host: { channels: { telegram: { allowFrom: ["4242"] } } }, kinds: ["block"] }],
  legacyBlock: [{ host: { channels: { telegram: { allowFrom: ["4242"], streaming: "off", blockStreaming: true } } } }],
  // session.scope "global": no group word in the key, so the delivery's chat type decides.
  global: [{ key: "global", chatType: null }],
  slack: [{ host: { channels: { slack: { allowFrom: ["U9"], streaming: "off" } } }, key: "agent:main:slack:direct:u9",
    channel: "slack", sender: "U9", conv: "D0123", replyConv: "slack:channel:D0123" }],
  twice: [{ prompts: 2 }],
};
const out = {};
let run = 0;
for (const [name, turns] of Object.entries(cases)) {
  writeFileSync(calls, "");
  const { default: plugin } = await import("` + plugin + `?" + name);
  const hooks = {};
  plugin.register({ on: (n, fn) => { hooks[n] = fn }, pluginConfig: turns[0].config || {}, config: turns[0].host || owner });
  let last;
  for (const t of turns) {
    const key = t.key || "agent:main:main", channel = t.channel || "telegram", sender = t.sender || "4242";
    const conv = t.conv || sender, runId = "run-" + ++run;
    const chatType = t.chatType === undefined ? (t.isGroup ? "group" : "direct") : t.chatType;
    await hooks.before_dispatch?.({ sessionKey: key, channel, senderId: sender, isGroup: t.isGroup ?? false },
      { sessionKey: key, channelId: channel, conversationId: conv, senderId: sender });
    let built;
    for (let i = 0; i < (t.prompts || 1); i++) {
      built = (await hooks.before_prompt_build({ prompt: t.prompt || "what did we decide about the zorbit drain", messages: [] },
        { sessionKey: key, sessionId: "s", channel, senderId: sender, trigger: "user", runId })) ?? built;
    }
    const sent = [];
    for (const kind of t.kinds ?? ["final"]) {
      sent.push(hooks.reply_payload_sending
        ? (await hooks.reply_payload_sending({ kind, channel, sessionKey: key, runId, usageState: chatType ? { chatType } : undefined,
          payload: { text: "Use --grace 40." } },
          { channelId: channel, conversationId: t.replyConv || channel + ":" + conv, senderId: sender, runId })) ?? null
        : null);
    }
    last = { context: built?.prependContext ?? null, sent: sent[sent.length - 1] ?? null };
  }
  out[name] = { ...last, calls: readFileSync(calls, "utf8") };
}
console.log(JSON.stringify(out));
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	cmd := exec.CommandContext(t.Context(), node, run)
	cmd.Env = append(os.Environ(), "DEJA_CALLS="+filepath.Join(home, "calls"))
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, raw)
	}
	var got map[string]struct {
		Context string `json:"context"`
		Calls   string `json:"calls"`
		Sent    *struct {
			Payload struct {
				Text         string `json:"text"`
				Presentation struct {
					Blocks []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"blocks"`
				} `json:"presentation"`
			} `json:"payload"`
		} `json:"sent"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("driver said %s", raw)
	}
	for name, c := range got {
		if c.Context != "RECALLED drain with --grace 40" {
			t.Errorf("%s: the model was not given what --plain prints: %q", name, c.Context)
		}
	}
	dm := got["dm"]
	if dm.Sent == nil {
		t.Fatalf("the reply in the owner's DM went out without the receipt:\n%s", raw)
	}
	blocks := dm.Sent.Payload.Presentation.Blocks
	want := `_↺ from Codex CLI, 2 weeks ago: "zorbit drain" · deja_`
	if dm.Sent.Payload.Text != "Use --grace 40." || len(blocks) != 1 || blocks[0].Type != "context" || blocks[0].Text != want {
		t.Errorf("want one context block %q under the reply:\n%s", want, raw)
	}
	if dm.Calls != "hook-prompt\n" {
		t.Errorf("want one JSON hook-prompt spawn in the DM, got %q", dm.Calls)
	}
	for _, name := range []string{"group", "unnamedGroup", "tui", "off", "streaming", "legacyBlock"} {
		if got[name].Sent != nil {
			t.Errorf("%s: the reply was changed:\n%s", name, raw)
		}
		if got[name].Calls != "hook-prompt --plain\n" {
			t.Errorf("%s: want --plain, got %q", name, got[name].Calls)
		}
	}
	for _, name := range []string{"stranger", "block", "global"} {
		if got[name].Sent != nil {
			t.Errorf("%s: a reply that is not the owner's final direct reply took the receipt:\n%s", name, raw)
		}
	}
	// A Slack DM's reply names its conversation "slack:channel:D…", the
	// dispatch "D…"; and a second prompt hook in the run keeps the receipt.
	for _, name := range []string{"slack", "twice"} {
		c := got[name]
		if c.Sent == nil || len(c.Sent.Payload.Presentation.Blocks) != 1 || c.Sent.Payload.Presentation.Blocks[0].Text != want {
			t.Errorf("%s: the owner's DM reply went out without the receipt:\n%s", name, raw)
		}
	}
}

// The installer's plugin carries the receipt rules as a copy of
// extensions/openclaw/lib.mjs's, so a fix to one that misses the other would
// gate the receipt differently depending on how deja was installed.
func TestOpenClawPluginSharesTheReceiptRules(t *testing.T) {
	lib, err := os.ReadFile(filepath.Join("..", "..", "extensions", "openclaw", "lib.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	// A windows checkout turns lib.mjs's line endings into CRLF.
	lib = bytes.ReplaceAll(lib, []byte("\r\n"), []byte("\n"))
	js := openclawPluginJS("/bin/deja")
	for _, name := range []string{"splitHookReply", "deliversNotes", "ownerSender", "directSessionKey", "conversationOf",
		"directTurn", "wholeReplies", "receiptAllowed", "replyTakes", "escapeMarkdown", "receiptLine", "withReceipt"} {
		src := string(lib)
		i := strings.Index(src, "export function "+name+"(")
		if i < 0 {
			t.Fatalf("lib.mjs has no %s", name)
		}
		end := strings.Index(src[i:], "\n}\n")
		fn := src[i+len("export ") : i+end+2]
		if !strings.Contains(js, fn) {
			t.Errorf("the installer's plugin does not carry lib.mjs's %s as it is", name)
		}
	}
}

// The digest goes in through agent:bootstrap, which OpenClaw fires on every
// agent run. The plugin answers it as a plugin hook, which runs under --local
// and needs no allowConversationAccess; in the gateway the hook pack answers
// the same event first. A gateway session got the digest twice, because the
// pack named no session and the once-per-session check had nothing to match.
// Both now send the transcript id with deja_once, so deja hands out one.
// An OpenClaw without plugin hooks falls back to agent_turn_prepare.
func TestOpenClawPluginOpensWithTheProjectDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$in\" >> " + calls + "\n" +
		"case \"$1\" in hook-context) printf 'DIGEST for this project' ;; esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "index.mjs")
	if werr := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	pack := filepath.Join(home, "handler.mjs")
	if werr := os.WriteFile(pack, []byte(openclawHandlerJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	// The context OpenClaw 2026.9.8 builds for agent:bootstrap.
	driver := `
import plugin from "` + plugin + `";
import pack from "` + pack + `";
const ctx = () => ({ workspaceDir: "/w", bootstrapFiles: [], sessionKey: "agent:main:s1", sessionId: "6bb01805-a4da-4f4c-a26b-6159d976f6d5", agentId: "main" });
const on = {}, hooks = {};
plugin.register({ on: (name, fn) => { on[name] = fn }, registerHook: (name, fn) => { hooks[name] = fn } });
const a = { type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: ctx() };
await hooks["agent:bootstrap"](a);
const b = { type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: ctx() };
await pack(b);
const old = {};
plugin.register({ on: (name, fn) => { old[name] = fn } });
console.log(JSON.stringify({
  turnPrepare: "agent_turn_prepare" in on,
  plugin: a.context.bootstrapFiles,
  pack: b.context.bootstrapFiles,
  fallback: await old["agent_turn_prepare"]?.({ prompt: "hello" }, { sessionKey: "agent:main:s1", sessionId: "6bb01805-a4da-4f4c-a26b-6159d976f6d5" }),
}));
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	var got struct {
		TurnPrepare bool              `json:"turnPrepare"`
		Plugin      []map[string]any  `json:"plugin"`
		Pack        []map[string]any  `json:"pack"`
		Fallback    map[string]string `json:"fallback"`
	}
	if uerr := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &got); uerr != nil {
		t.Fatalf("driver printed %s", out)
	}
	if got.TurnPrepare {
		t.Error("the plugin also answers agent_turn_prepare where plugin hooks run, a second digest path")
	}
	for name, files := range map[string][]map[string]any{"plugin": got.Plugin, "pack": got.Pack} {
		content := ""
		if len(files) == 1 {
			content = typedOrFatal[string](t, files[0]["content"])
		}
		if len(files) != 1 || !strings.Contains(content, "DIGEST") || files[0]["path"] != "deja://recall" {
			t.Errorf("the %s's bootstrap hook did not add the digest to the Project Context: %v", name, files)
		}
	}
	if !strings.Contains(got.Fallback["prependContext"], "DIGEST") {
		t.Errorf("without plugin hooks the digest should come back as prependContext: %v", got.Fallback)
	}
	asked, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("nothing called deja: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(asked)), "\n")
	if len(lines) != 3 {
		t.Fatalf("want three hook-context calls, got %d:\n%s", len(lines), asked)
	}
	for i, l := range lines {
		var payload struct {
			SessionID string `json:"session_id"`
			Once      bool   `json:"deja_once"`
			CWD       string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(l), &payload); err != nil {
			t.Fatalf("payload %d is not JSON: %v (%s)", i, err, l)
		}
		// One id across all of them, the transcript's: the guard that keeps
		// the digest to one turn is keyed on it.
		if payload.SessionID != "6bb01805-a4da-4f4c-a26b-6159d976f6d5" {
			t.Errorf("payload %d names session %q", i, payload.SessionID)
		}
		if !payload.Once {
			t.Errorf("payload %d does not ask for one digest per session", i)
		}
		if i < 2 && payload.CWD != "/w" {
			t.Errorf("payload %d names project %q, not the agent's workspace", i, payload.CWD)
		}
	}
}

// OpenClaw's TUI footer takes nothing from plugins, so deja's status line goes
// in a Control UI sidebar tab: a gateway-auth route the tab descriptor points
// at, which the Control UI renders without the Custom plugin UI lab (stand on
// 2026.9.8). The route answers with `deja statusline`, escaped.
func TestOpenClawPluginServesTheStatusLineToItsTab(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	script := "#!/bin/sh\ncat >/dev/null\n[ \"$1\" = statusline ] && printf 'deja · 3 recalls · <b>today</b>'\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "index.mjs")
	if werr := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import plugin from "` + plugin + `";
let route, tab;
plugin.register({
  on: () => {},
  registerHttpRoute: (r) => { route = r },
  session: { controls: { registerControlUiDescriptor: (d) => { tab = d } } },
});
const res = { headers: {}, setHeader(k, v) { this.headers[k] = v }, end(b) { this.body = b } };
const handled = await route.handler({}, res);
console.log(JSON.stringify({ route: { path: route.path, auth: route.auth }, tab, handled, status: res.statusCode, body: res.body }));
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	var got struct {
		Route struct {
			Path string `json:"path"`
			Auth string `json:"auth"`
		} `json:"route"`
		Tab struct {
			Surface string `json:"surface"`
			Path    string `json:"path"`
			Label   string `json:"label"`
		} `json:"tab"`
		Handled bool   `json:"handled"`
		Status  int    `json:"status"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("driver said %s", out)
	}
	if got.Route.Auth != "gateway" {
		t.Errorf("the status route is open to anyone who reaches the gateway: auth %q", got.Route.Auth)
	}
	if got.Tab.Surface != "tab" || got.Tab.Path != got.Route.Path {
		t.Errorf("the tab does not point at the route: %+v vs %s", got.Tab, got.Route.Path)
	}
	if !got.Handled || got.Status != 200 {
		t.Errorf("handled %v, status %d", got.Handled, got.Status)
	}
	if !strings.Contains(got.Body, "deja · 3 recalls · &lt;b&gt;today&lt;/b&gt;") {
		t.Errorf("the page lacks the escaped status line:\n%s", got.Body)
	}
}

// The switch that turns openclaw's internal hooks on is the reader's setting,
// and deja only borrows it. Uninstall restored it only when no other entry was
// left, so a reader with a hook of their own and the switch off got it back on
// — deja gone, their hook running where it had not been. The JSONC writer
// restored it either way, so the two spellings of one file disagreed (#3204).
func TestOpenClawUninstallGivesTheSwitchBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seed   string
		want   any // what "enabled" must be afterwards; nil = the key is gone
		theirs bool
	}{
		{
			name:   "off, with an entry of their own",
			seed:   `{"hooks":{"internal":{"enabled":false,"entries":{"theirs":{"command":"x"}}}}}`,
			want:   false,
			theirs: true,
		},
		{
			name: "off, with nothing else",
			seed: `{"hooks":{"internal":{"enabled":false}}}`,
			want: false,
		},
		{
			name:   "on already, with an entry of their own",
			seed:   `{"hooks":{"internal":{"enabled":true,"entries":{"theirs":{"command":"x"}}}}}`,
			want:   true,
			theirs: true,
		},
		{
			name: "no switch at all — deja's to remove",
			seed: `{"hooks":{"internal":{"entries":{}}}}`,
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.seed+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := setOpenClawHookEnabled(true); err != nil {
				t.Fatal(err)
			}
			if err := setOpenClawHookEnabled(false); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal(b, &root); err != nil {
				t.Fatalf("%v: %s", err, b)
			}
			hooks, _ := root["hooks"].(map[string]any)
			internal, _ := hooks["internal"].(map[string]any)
			got, present := internal["enabled"]
			if tc.want == nil {
				if present {
					t.Errorf("deja left a switch it had added: %s", b)
				}
			} else if got != tc.want {
				t.Errorf("enabled = %v, want %v — the reader's setting changed:\n%s", got, tc.want, b)
			}
			// And their own entry is still there.
			if tc.theirs {
				entries, _ := internal["entries"].(map[string]any)
				if _, ok := entries["theirs"]; !ok {
					t.Errorf("uninstall took the reader's hook with it:\n%s", b)
				}
			}
			// Never ours.
			if entries, _ := internal["entries"].(map[string]any); entries != nil {
				if _, ok := entries[openclawHookName]; ok {
					t.Errorf("deja's own entry survived uninstall:\n%s", b)
				}
			}
		})
	}
}

func openclawConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".openclaw", "openclaw.json"))
	if err != nil {
		t.Fatalf("read openclaw.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("openclaw.json is not valid JSON: %v", err)
	}
	return root
}

func openclawHookEntry(root map[string]any) (enabled bool, masterSwitch any, present bool) {
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := hooks["internal"].(map[string]any)
	entries, _ := internal["entries"].(map[string]any)
	e, ok := entries[openclawHookName].(map[string]any)
	if !ok {
		return false, internal["enabled"], false
	}
	on, _ := e["enabled"].(bool)
	return on, internal["enabled"], true
}

func TestInstallOpenClawHooksWritesPackAndEnablesIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installOpenClawHooks("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(home, ".openclaw", "hooks", openclawHookName)
	doc, err := os.ReadFile(filepath.Join(dir, "HOOK.md"))
	if err != nil {
		t.Fatalf("HOOK.md missing: %v", err)
	}
	// The frontmatter is what binds the pack to the event; without it the
	// handler is never called.
	if !strings.Contains(string(doc), `"events": ["agent:bootstrap"]`) {
		t.Fatalf("HOOK.md does not subscribe to agent:bootstrap:\n%s", doc)
	}
	handler, err := os.ReadFile(filepath.Join(dir, "handler.js"))
	if err != nil {
		t.Fatalf("handler.js missing: %v", err)
	}
	// The launcher, not the build it was installed from (#3682).
	for _, want := range []string{"bootstrapFiles", "hook-context", strconv.Quote(hookExeInConfigs("/bin/deja"))} {
		if !strings.Contains(string(handler), want) {
			t.Fatalf("handler.js missing %q:\n%s", want, handler)
		}
	}
	// A pack that is not enabled in config is listed as ready and never runs.
	on, master, present := openclawHookEntry(openclawConfig(t, home))
	if !present || !on {
		t.Fatalf("hook entry not enabled: present=%v on=%v", present, on)
	}
	if master != true {
		t.Fatalf("hooks.internal.enabled = %v, want true", master)
	}
}

func TestInstallOpenClawHooksUninstallLeavesOtherHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := filepath.Join(home, ".openclaw")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcp":{"servers":{}},"hooks":{"internal":{"enabled":true,"entries":{"boot-md":{"enabled":true}}}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "openclaw.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawHooks("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installOpenClawHooks("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "hooks", openclawHookName)); !os.IsNotExist(err) {
		t.Fatalf("pack survived uninstall: %v", err)
	}
	root := openclawConfig(t, home)
	if _, _, present := openclawHookEntry(root); present {
		t.Fatal("our entry survived uninstall")
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := hooks["internal"].(map[string]any)
	entries, _ := internal["entries"].(map[string]any)
	if _, ok := entries["boot-md"]; !ok {
		t.Fatalf("uninstall removed the user's own hook entry: %v", entries)
	}
	if internal["enabled"] != true {
		t.Fatalf("uninstall flipped the master switch off with hooks still configured: %v", internal)
	}
	if _, ok := root["mcp"]; !ok {
		t.Fatal("uninstall dropped unrelated config")
	}
}

func TestInstallOpenClawHooksUninstallClearsConfigItCreated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installOpenClawHooks("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installOpenClawHooks("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, ok := openclawConfig(t, home)["hooks"]; ok {
		t.Fatal("uninstall left the hooks block it created behind")
	}
}

func opencodeV1(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	opencodeVersionMajor = func() int { return 1 }
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// opencode reads tui.json as JSONC: 1.18.35 logs "applying tui config" for a
// file with a comment and a trailing comma. deja decoded it as strict JSON, so
// `install opencode-auto` failed the whole target after writing half of it —
// no skill, no /deja command — and uninstall failed on the same file.
func TestOpencodeAutoTakesACommentedTuiJSON(t *testing.T) {
	dir := opencodeV1(t)
	list := filepath.Join(dir, "tui.json")
	seed := "{\n  // my tui settings\n  \"$schema\": \"https://opencode.ai/tui.json\",\n  \"theme\": \"tokyonight\",\n}\n"
	if err := os.WriteFile(list, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "opencode-auto", "--no-index")
	if err != nil {
		t.Fatalf("install refused a tui.json opencode loads: %v\n%s", err, out)
	}
	if !strings.Contains(out, "./plugins/deja-status.tsx") {
		t.Errorf("install did not say what to add to the commented tui.json:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "commands", "deja.md")); err != nil {
		t.Errorf("install stopped before the /deja command: %v", err)
	}
	if b, _ := os.ReadFile(list); string(b) != seed {
		t.Errorf("install rewrote the commented tui.json:\n%s", b)
	}
	if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
		t.Fatalf("uninstall refused it: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(list); string(b) != seed {
		t.Errorf("uninstall changed the commented tui.json:\n%s", b)
	}
}

// A reader who followed install's note and listed deja's status-line plugin
// in a commented tui.json was left by uninstall with a list entry pointing at
// the file it had just deleted (#5313). Only deja's element goes, as text:
// the comment stays, and so do the reader's own entries.
func TestUninstallTakesDejasEntryOutOfACommentedTuiJSON(t *testing.T) {
	for _, c := range []struct{ seed, want string }{
		{
			"{\n  // my tui settings\n  \"$schema\": \"https://opencode.ai/tui.json\",\n  \"theme\": \"tokyonight\",\n  \"plugin\": [\"./plugins/deja-status.tsx\"]\n}\n",
			"{\n  // my tui settings\n  \"$schema\": \"https://opencode.ai/tui.json\",\n  \"theme\": \"tokyonight\"\n}\n",
		},
		{
			"{\n  // my tui settings\n  \"plugin\": [\n    // mine first\n    \"./plugins/mine.tsx\",\n    \"./plugins/deja-status.tsx\",\n  ],\n  \"theme\": \"tokyonight\"\n}\n",
			"{\n  // my tui settings\n  \"plugin\": [\n    // mine first\n    \"./plugins/mine.tsx\",\n  ],\n  \"theme\": \"tokyonight\"\n}\n",
		},
	} {
		dir := opencodeV1(t)
		list := filepath.Join(dir, "tui.json")
		if err := os.WriteFile(list, []byte(c.seed), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := captureRun(t, "uninstall", "opencode-auto")
		if err != nil {
			t.Fatalf("uninstall refused it: %v\n%s", err, out)
		}
		if strings.Contains(out, "from its plugin list") {
			t.Errorf("uninstall left the entry to the reader:\n%s", out)
		}
		if b, _ := os.ReadFile(list); string(b) != c.want {
			t.Errorf("uninstall left\n%s\nwant\n%s", b, c.want)
		}
	}
}

// A tui.json the reader had is theirs to keep: uninstall deleted every one
// that held nothing but what install found or added — an empty file, `{}`,
// an empty plugin list, opencode's own $schema line.
func TestUninstallKeepsTheReadersOwnTuiJSON(t *testing.T) {
	for _, seed := range []string{
		"",
		"{}\n",
		"{\n  \"plugin\": []\n}\n",
		"{\n  \"$schema\": \"https://opencode.ai/tui.json\"\n}\n",
	} {
		dir := opencodeV1(t)
		list := filepath.Join(dir, "tui.json")
		if err := os.WriteFile(list, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "install", "opencode-auto", "--no-index"); err != nil {
			t.Fatalf("%q: install: %v\n%s", seed, err, out)
		}
		if got := tuiPluginList(t, list); strings.Join(got, ",") != "./plugins/deja-status.tsx" {
			t.Errorf("%q: install listed %v", seed, got)
		}
		if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
			t.Fatalf("%q: uninstall: %v\n%s", seed, err, out)
		}
		b, err := os.ReadFile(list)
		if err != nil {
			t.Errorf("%q: uninstall deleted the reader's tui.json", seed)
			continue
		}
		if string(b) != seed {
			t.Errorf("%q: uninstall left %q", seed, b)
		}
	}
	// The control: a tui.json install created goes with the entry.
	dir := opencodeV1(t)
	if out, err := captureRun(t, "install", "opencode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "tui.json")); err == nil {
		t.Error("uninstall left the tui.json install created")
	}
}

// opencode.jsonc holding plain JSON on one line — `{}`, the $schema line, a
// minified config — was refused by install while the same bytes in
// opencode.json were edited, and an empty `"mcp": {}` came back from the round
// trip split over two lines.
func TestOpencodeJSONCWithPlainJSONIsEdited(t *testing.T) {
	for _, seed := range []string{
		"{}\n",
		"{\"$schema\": \"https://opencode.ai/config.json\"}\n",
		`{"$schema":"https://opencode.ai/config.json","mcp":{"mine":{"type":"local","command":["my-server"]}},"model":"openai/gpt-5"}`,
		"{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {}\n}\n",
	} {
		dir := opencodeV1(t)
		cfg := filepath.Join(dir, "opencode.jsonc")
		if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "install", "opencode-auto", "--no-index"); err != nil {
			t.Errorf("%q: install refused: %v\n%s", seed, err, out)
			continue
		}
		b, _ := os.ReadFile(cfg)
		var root map[string]any
		if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
			t.Fatalf("%q: install wrote a config that does not parse: %v\n%s", seed, err, b)
		}
		if mcp, _ := root["mcp"].(map[string]any); mcp["deja"] == nil {
			t.Errorf("%q: no deja under mcp:\n%s", seed, b)
		}
		if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
			t.Fatalf("%q: uninstall: %v\n%s", seed, err, out)
		}
		if b, _ := os.ReadFile(cfg); string(b) != seed {
			t.Errorf("%q: uninstall left %q", seed, b)
		}
	}
}

// Uninstall on a config deja never wrote leaves its bytes alone. The text
// writer opened a one-line block on the way through and wrote that back, with
// a .bak beside it, though there was no deja entry to take out.
func TestOpencodeUninstallLeavesAConfigWithoutDejaAlone(t *testing.T) {
	for _, seed := range []string{
		"{\n  // mine\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {}\n}\n",
		"{\n  // mine\n  \"mcp\": {\"mine\": {\"type\": \"local\", \"command\": [\"my-server\"]}}\n}\n",
		`{"$schema":"https://opencode.ai/config.json","mcp":{"mine":{"type":"local","command":["x"]}}}`,
	} {
		dir := opencodeV1(t)
		cfg := filepath.Join(dir, "opencode.jsonc")
		if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
			t.Fatalf("%q: uninstall: %v\n%s", seed, err, out)
		}
		if b, _ := os.ReadFile(cfg); string(b) != seed {
			t.Errorf("%q: uninstall rewrote it to %q", seed, b)
		}
		if _, err := os.Stat(cfg + ".bak"); err == nil {
			t.Errorf("%q: uninstall left a snapshot of a file it did not change", seed)
		}
	}
}

// What opencode 1.18.35 makes of an opencode.jsonc holding `{}` on its first
// start: the $schema line with the closing brace after its comma. Install
// refused it as not laid out to add to. And a commented `"mcp": {}` came back
// from the round trip split over two lines.
func TestOpencodeJSONCAsOpencodeWritesIt(t *testing.T) {
	for _, seed := range []string{
		"{\n  \"$schema\": \"https://opencode.ai/config.json\",}",
		"{\n  \"$schema\": \"https://opencode.ai/config.json\",}\n",
		"{\n  // mine\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {}\n}\n",
	} {
		dir := opencodeV1(t)
		cfg := filepath.Join(dir, "opencode.jsonc")
		if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "install", "opencode-auto", "--no-index"); err != nil {
			t.Errorf("%q: install refused: %v\n%s", seed, err, out)
			continue
		}
		b, _ := os.ReadFile(cfg)
		var root map[string]any
		if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
			t.Fatalf("%q: install wrote a config that does not parse: %v\n%s", seed, err, b)
		}
		if mcp, _ := root["mcp"].(map[string]any); mcp["deja"] == nil {
			t.Errorf("%q: no deja under mcp:\n%s", seed, b)
		}
		if out, err := captureRun(t, "uninstall", "opencode-auto"); err != nil {
			t.Fatalf("%q: uninstall: %v\n%s", seed, err, out)
		}
		if b, _ := os.ReadFile(cfg); string(b) != seed {
			t.Errorf("%q: uninstall left %q", seed, b)
		}
	}
}

// OpenClaw's openclaw.json goes through the same text writer once it holds a
// comment. Uninstall split the reader's one-line plugins.entries and
// hooks.internal.entries across lines (#5257).
func TestOpenClawAutoRoundTripKeepsTheReadersOneLineBlocks(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "// user settings\n{\n  // top comment\n" +
		"  \"agents\": {\"defaults\": {\"model\": \"anthropic/x\"}},\n" +
		"  \"plugins\": {\"allow\": [\"my-plugin\"], \"entries\": {\"my-plugin\": {\"enabled\": true}}, \"load\": {\"paths\": [\"/opt/my-plugin\"]}},\n" +
		"  \"hooks\": {\"internal\": {\"enabled\": true, \"entries\": {\"session-memory\": {\"enabled\": true}}}},\n" +
		"  \"mcp\": {\"servers\": {\"zz-mine\": {\"command\": \"my-server\"}}}\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "openclaw-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := captureRun(t, "uninstall", "openclaw-auto"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if got := readFile(t, path); got != seed {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, seed)
	}
}

// A 2.x config keeps MCP servers under `mcp.servers`. The auto target must wire
// that object and still write the plugin used for recall (#3938).
func TestOpencodeAutoUpdatesNestedConfigAndWritesThePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_OPENCODE_MAJOR", "2")
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := "{\n  // mine\n  \"mcp\": {\n    \"servers\": {\n      \"deja\": {\n        \"type\": \"local\",\n" +
		"        \"command\": [\"/usr/local/bin/deja\", \"mcp\"],\n        \"environment\": { \"DEJA_HOME\": \"/mnt/nas/deja\" }\n" +
		"      }\n    }\n  }\n}\n"
	cfg := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(cfg, []byte(nested), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installOpencodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("nested config install failed: %v", err)
	}
	// The config stays in the shape the OpenCode 2.x reader expects.
	b, readErr := os.ReadFile(cfg)
	if readErr != nil {
		t.Fatal(readErr)
	}
	got := jsoncValue(t, b)
	mcp, ok := got["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp block is missing: %s", b)
	}
	servers, ok := mcp["servers"].(map[string]any)
	if !ok {
		t.Fatalf("servers block is missing: %s", b)
	}
	if _, ok := servers["deja"]; !ok {
		t.Fatalf("deja is not under mcp.servers: %s", b)
	}
	if _, ok := mcp["deja"]; ok {
		t.Fatalf("deja was written beside mcp.servers: %s", b)
	}
	if strings.Contains(string(b), "DEJA_HOME") == false {
		t.Error("the existing environment was not preserved")
	}
	// The plugin is deja's, and it is the half that still works.
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "deja.js"))
	if err != nil {
		t.Fatalf("the plugin was not written: %v", err)
	}
	if !strings.Contains(string(b), "hook-context") {
		t.Error("the plugin was written without the digest hook")
	}
}

func tuiPluginList(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var root struct {
		Plugin []string `json:"plugin"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return root.Plugin
}

// opencode 1.x and Kilo load a TUI plugin only when tui.json lists it; the
// plugin runs deja statusline and shows its line in the sidebar, the prompt
// row and the home screen. Uninstall takes the file and the entry back and
// leaves the reader's own plugins.
func TestV1TUIPluginIsListedAndTakenBack(t *testing.T) {
	hermeticEnv(t)
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	opencodeVersionMajor = func() int { return 1 }
	for _, c := range []struct{ target, dir string }{
		{"opencode-auto", filepath.Join(opencodeConfigHome(), "opencode")},
		{"kilocode-auto", kilocodeCLIConfigDir()},
	} {
		if err := os.MkdirAll(c.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		list := filepath.Join(c.dir, "tui.json")
		if err := os.WriteFile(list, []byte(`{"plugin":["./plugins/mine.tsx"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", false); err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		got := tuiPluginList(t, list)
		if strings.Join(got, ",") != "./plugins/mine.tsx,./plugins/deja-status.tsx" {
			t.Errorf("%s: tui.json plugin = %v", c.target, got)
		}
		b, err := os.ReadFile(filepath.Join(c.dir, "plugins", "deja-status.tsx"))
		if err != nil {
			t.Fatalf("%s: no plugin file: %v", c.target, err)
		}
		src := string(b)
		for _, want := range []string{"@jsxImportSource @opentui/solid", `["statusline"]`, "order: 50", "home_bottom", "order: 150", "sidebar_content", "session_prompt_right", "export default { id: \"deja.status\", tui }"} {
			if !strings.Contains(src, want) {
				t.Errorf("%s: plugin lacks %q", c.target, want)
			}
		}
		if strings.Index(src, "home_bottom") > strings.Index(src, "sidebar_content") {
			t.Errorf("%s: home_bottom must register at order 50 before internal:home-tips (order 100)", c.target)
		}
		if !strings.HasPrefix(src, "/** @jsxImportSource") {
			t.Errorf("%s: the JSX pragma is not the first line", c.target)
		}
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", true); err != nil {
			t.Fatal(err)
		}
		if got := tuiPluginList(t, list); strings.Join(got, ",") != "./plugins/mine.tsx" {
			t.Errorf("%s: uninstall left tui.json plugin = %v", c.target, got)
		}
		if _, err := os.Stat(filepath.Join(c.dir, "plugins", "deja-status.tsx")); err == nil {
			t.Errorf("%s: uninstall left the plugin file", c.target)
		}
	}
}

// opencode 2.x discovers <config>/plugins/<name>/tui.tsx itself and has
// footer slots; nothing is listed anywhere.
func TestV2TUIPluginNeedsNoList(t *testing.T) {
	hermeticEnv(t)
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	opencodeVersionMajor = func() int { return 2 }
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	if _, err := installTarget("opencode-auto", "/opt/deja/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "deja-status", "tui.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"setup(ctx)", `append: "prompt.footer.status"`, `append: "home.footer.status"`, `append: "sidebar.content"`, `["statusline"]`, "input.sessionID", "ctx.theme.text.muted", "ctx.theme.text.base"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("plugin lacks %q", want)
		}
	}
	// 2.0.x has no theme.textMuted: the line came out in the default colour.
	if strings.Contains(string(b), "textMuted") {
		t.Error("plugin reads theme.textMuted, which 2.x does not have")
	}
	if _, err := os.Stat(filepath.Join(dir, "tui.json")); err == nil {
		t.Error("a 2.x install wrote tui.json")
	}
	if _, err := installTarget("opencode-auto", "/opt/deja/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "deja-status")); err == nil {
		t.Error("uninstall left the plugin directory")
	}
}

func TestOpencodeTUIPluginFitsFooterAndRefreshesStaleGeneratedFile(t *testing.T) {
	hermeticEnv(t)
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	dir := filepath.Join(opencodeConfigHome(), "opencode")

	opencodeVersionMajor = func() int { return 1 }
	if _, err := installTarget("opencode-auto", "/opt/deja/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	v1Bytes, v1Err := os.ReadFile(filepath.Join(dir, "plugins", "deja-status.tsx"))
	if v1Err != nil {
		t.Fatal(v1Err)
	}
	v1Src := string(v1Bytes)
	for _, want := range []string{"fitSegments", "sidebarOpen", `process.stdout?.on?.("resize"`} {
		if !strings.Contains(v1Src, want) {
			t.Errorf("V1 plugin lacks %q", want)
		}
	}

	opencodeVersionMajor = func() int { return 2 }
	if _, err := installTarget("opencode-auto", "/opt/deja/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	v2Path := filepath.Join(dir, "plugins", "deja-status", "tui.tsx")
	v2Bytes, v2Err := os.ReadFile(v2Path)
	if v2Err != nil {
		t.Fatal(v2Err)
	}
	v2Src := string(v2Bytes)
	for _, want := range []string{"fitSegments", "onSizeChange", "sidebarOpen", `process.stdout?.on?.("resize"`} {
		if !strings.Contains(v2Src, want) {
			t.Errorf("V2 plugin lacks %q", want)
		}
	}
	if strings.Contains(v2Src, `from "@opentui/solid"`) {
		t.Error(`V2 plugin must not import runtime symbols from "@opentui/solid"`)
	}
	if strings.Contains(v2Src, `<text fg={ctx.theme.text.muted}>{""}</text>`) {
		t.Error("V2 plugin still renders an empty text node in prompt.footer.status")
	}

	st := wiringState{Targets: []string{"opencode-auto"}}
	if st.opencodeNeedsRefresh() {
		t.Error("fresh V2 install unexpectedly reports opencodeNeedsRefresh() == true")
	}
	if err := os.WriteFile(v2Path, []byte("// generated by deja install\nexport default { id: \"deja.status\", setup(ctx) {} }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !st.opencodeNeedsRefresh() {
		t.Error("stale V2 tui.tsx without onSizeChange reported opencodeNeedsRefresh() == false")
	}
}

func TestUpgradingOpencodeFromV1ToV2ReplacesTUIAndServerPlugins(t *testing.T) {
	hermeticEnv(t)
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	dir := filepath.Join(opencodeConfigHome(), "opencode")

	opencodeVersionMajor = func() int { return 1 }
	if _, err := installTarget("opencode-auto", "/opt/deja/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "deja-status.tsx")); err != nil {
		t.Fatalf("1.x install did not write deja-status.tsx: %v", err)
	}

	opencodeVersionMajor = func() int { return 2 }
	row := autoWirings()[0]
	if got, _ := autoWiringState(row); got != "stale" {
		t.Fatalf("1.x wiring after upgrading to 2.x reads %q, want stale", got)
	}
	if _, err := installTarget("opencode-auto", "/opt/deja/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "deja-status.tsx")); err == nil {
		t.Error("2.x install left 1.x plugins/deja-status.tsx behind")
	}
	if got := tuiPluginList(t, filepath.Join(dir, "tui.json")); len(got) != 0 {
		t.Errorf("2.x install left ./plugins/deja-status.tsx in tui.json: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "deja-status", "tui.tsx")); err != nil {
		t.Errorf("2.x install did not write plugins/deja-status/tui.tsx: %v", err)
	}
	if got, _ := autoWiringState(row); got != "wired" {
		t.Errorf("2.x wiring after install reads %q, want wired", got)
	}
}

// OpenCode 2.x keeps its MCP servers under `mcp.servers`. The installer must
// update that object in place so the harness keeps reading the entry and fields
// such as `environment` survive the merge.
func TestOpencodeJSONCUpdatesV2NestedServers(t *testing.T) {
	seed := "{\n" +
		"  // my MCP servers\n" +
		"  \"mcp\": {\n" +
		"    \"servers\": {\n" +
		"      \"deja\": {\n" +
		"        \"type\": \"local\",\n" +
		"        \"command\": [\"/usr/local/bin/deja\", \"mcp\"],\n" +
		"        \"environment\": { \"DEJA_HOME\": \"/mnt/nas/deja\" }\n" +
		"      },\n" +
		"      \"theirs\": { \"command\": [\"other\"] }\n" +
		"    }\n" +
		"  }\n" +
		"}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatalf("nested config was refused: %v", err)
	}
	got := jsoncValue(t, out)
	mcp, ok := got["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp block is missing: %s", out)
	}
	servers, ok := mcp["servers"].(map[string]any)
	if !ok {
		t.Fatalf("servers block is missing: %s", out)
	}
	entry, ok := servers["deja"].(map[string]any)
	if !ok {
		t.Fatalf("deja is not under mcp.servers: %s", out)
	}
	if got, want := entry["command"], []any{"/bin/deja", "mcp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command = %#v, want %#v", got, want)
	}
	environment, ok := entry["environment"].(map[string]any)
	if !ok || environment["DEJA_HOME"] != "/mnt/nas/deja" {
		t.Errorf("existing environment was not preserved: %#v", entry["environment"])
	}
	if _, ok := mcp["deja"]; ok {
		t.Errorf("deja was moved to the top-level mcp block:\n%s", out)
	}
	if _, ok := servers["theirs"]; !ok {
		t.Errorf("the unrelated server was lost:\n%s", out)
	}
	if !strings.Contains(string(out), "// my MCP servers") {
		t.Errorf("the surrounding comment was lost:\n%s", out)
	}
}

// A fresh OpenCode 2.x config has no deja entry yet. It should be added to the
// existing nested object rather than creating an invisible sibling at mcp.
func TestOpencodeJSONCAddsToV2NestedServers(t *testing.T) {
	seed := "{\n" +
		"  \"mcp\": {\n" +
		"    \"servers\": {\n" +
		"      // keep this server\n" +
		"      \"theirs\": {\"command\": [\"other\"]}\n" +
		"    }\n" +
		"  }\n" +
		"}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatalf("nested config was refused: %v", err)
	}
	got := jsoncValue(t, out)
	mcp := typedOrFatal[map[string]any](t, got["mcp"])
	servers := typedOrFatal[map[string]any](t, mcp["servers"])
	if _, ok := servers["deja"]; !ok {
		t.Fatalf("deja was not added under mcp.servers:\n%s", out)
	}
	if _, ok := mcp["deja"]; ok {
		t.Errorf("deja was added beside mcp.servers:\n%s", out)
	}
	if _, ok := servers["theirs"]; !ok || !strings.Contains(string(out), "// keep this server") {
		t.Errorf("the existing server or comment was lost:\n%s", out)
	}
}

// The same shape is valid when the nested object was written on one line. The
// generic JSONC writer replaces only the entry value and leaves the rest of the
// line intact.
func TestOpencodeJSONCUpdatesInlineV2NestedServers(t *testing.T) {
	seed := "{\n" +
		"  \"mcp\": {\n" +
		"    \"servers\": { \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]}, \"theirs\": {\"command\":[\"other\"]} }\n" +
		"  }\n" +
		"}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatalf("inline nested config was refused: %v", err)
	}
	got := jsoncValue(t, out)
	mcp := typedOrFatal[map[string]any](t, got["mcp"])
	servers := typedOrFatal[map[string]any](t, mcp["servers"])
	entry := typedOrFatal[map[string]any](t, servers["deja"])
	if got, want := entry["command"], []any{"/bin/deja", "mcp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command = %#v, want %#v", got, want)
	}
	if _, ok := mcp["deja"]; ok {
		t.Errorf("deja was moved to the top-level mcp block:\n%s", out)
	}
	if _, ok := servers["theirs"]; !ok {
		t.Errorf("the unrelated server was lost:\n%s", out)
	}
}

func TestOpencodeJSONUpdatesV2NestedServers(t *testing.T) {
	seed := []byte(`{"mcp":{"servers":{"deja":{"type":"local","command":["/old/deja","mcp"],"enabled":false},"theirs":{"command":["other"]}}}}`)
	out, _, err := updateOpencodeJSON(seed, "opencode.json", "/bin/deja", false)
	if err != nil {
		t.Fatalf("nested JSON config was refused: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("output no longer parses: %v\n%s", err, out)
	}
	mcp := typedOrFatal[map[string]any](t, root["mcp"])
	servers := typedOrFatal[map[string]any](t, mcp["servers"])
	entry := typedOrFatal[map[string]any](t, servers["deja"])
	if got, want := entry["command"], []any{"/bin/deja", "mcp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command = %#v, want %#v", got, want)
	}
	if got := entry["enabled"]; got != false {
		t.Errorf("enabled = %#v, want false", got)
	}
	if _, ok := mcp["deja"]; ok {
		t.Errorf("deja was moved to the top-level mcp block:\n%s", out)
	}
}

func TestOpencodeJSONCUninstallsFromV2NestedServers(t *testing.T) {
	seed := "{\n" +
		"  \"mcp\": {\n" +
		"    \"servers\": {\n" +
		"      // keep this server\n" +
		"      \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]},\n" +
		"      \"theirs\": {\"command\": [\"other\"]}\n" +
		"    }\n" +
		"  }\n" +
		"}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", true)
	if err != nil {
		t.Fatalf("nested config uninstall failed: %v", err)
	}
	got := jsoncValue(t, out)
	mcp := typedOrFatal[map[string]any](t, got["mcp"])
	servers := typedOrFatal[map[string]any](t, mcp["servers"])
	if _, ok := servers["deja"]; ok {
		t.Fatalf("deja was not removed from mcp.servers:\n%s", out)
	}
	if _, ok := servers["theirs"]; !ok || !strings.Contains(string(out), "// keep this server") {
		t.Fatalf("the unrelated server or comment was lost:\n%s", out)
	}
}

// `deja doctor` reads the config back, and it read only the 1.x shape: a 2.x
// config the installer had just wired was reported `not-wired`, so the fix
// above sent the user round the install again with nothing left to do.
func TestDoctorReadsTheNestedShapeTheInstallerWrites(t *testing.T) {
	seed := []byte(`{"mcp":{"servers":{"deja":{"type":"local","command":["/bin/deja","mcp"]},"theirs":{"command":["other"]}}}}`)
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	if !doctorJSONWiredIn(doctorOpencodeServers)(path) {
		t.Error("a wired 2.x config reads as not-wired")
	}
	if got := doctorJSONDejaKeysIn(doctorOpencodeServers)(path); !reflect.DeepEqual(got, []string{"deja"}) {
		t.Errorf("the entry under mcp.servers counts as %v", got)
	}
}

// A config with both shapes is a 1.x file that happens to have a server called
// `servers`, and the writer stays on the direct entry there. Doctor reads the
// same one, and the duplicate line still names a hand-named server beside it.
func TestDoctorAndInstallAgreeOnAMixedConfig(t *testing.T) {
	seed := []byte(`{"mcp":{"deja":{"type":"local","command":["/old/deja","mcp"]},"deja-vu":{"command":["/old/deja","mcp"]},"servers":{"command":["other"]}}}`)
	out, _, err := updateOpencodeJSON(seed, "opencode.json", "/bin/deja", false)
	if err != nil {
		t.Fatalf("mixed config was refused: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("output no longer parses: %v\n%s", err, out)
	}
	mcp := typedOrFatal[map[string]any](t, root["mcp"])
	entry, ok := mcp["deja"].(map[string]any)
	if !ok {
		t.Fatalf("the direct entry was abandoned:\n%s", out)
	}
	if got, want := entry["command"], []any{"/bin/deja", "mcp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command = %#v, want %#v", got, want)
	}
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	if !doctorJSONWiredIn(doctorOpencodeServers)(path) {
		t.Error("the config the installer just wrote reads as not-wired")
	}
	if got, want := doctorJSONDejaKeysIn(doctorOpencodeServers)(path), []string{"deja", "deja-vu"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deja keys = %v, want %v", got, want)
	}
}

// Installing twice is the ordinary case — every release runs `deja install`
// again — and the second pass must leave the file it already wrote alone.
func TestASecondInstallLeavesTheNestedConfigAlone(t *testing.T) {
	seed := "{\n  \"mcp\": {\n    \"servers\": {\n      \"theirs\": {\"command\": [\"other\"]}\n    }\n  }\n}\n"
	once, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	twice, _, err := updateOpencodeJSONC(once, "/bin/deja", false)
	if err != nil {
		t.Fatalf("second install failed: %v", err)
	}
	if !bytes.Equal(twice, once) {
		t.Errorf("the second install rewrote the file:\n%s\n---\n%s", once, twice)
	}
	got := jsoncValue(t, twice)
	mcp := typedOrFatal[map[string]any](t, got["mcp"])
	servers := typedOrFatal[map[string]any](t, mcp["servers"])
	if _, ok := mcp["deja"]; ok {
		t.Errorf("the second install added a sibling at mcp.deja:\n%s", twice)
	}
	if len(servers) != 2 {
		t.Errorf("mcp.servers holds %d entries: %v", len(servers), servers)
	}
}

// With deja already wired under another name, install added a second server
// beside it in nine targets and said only "updated", so the client started
// deja twice (#4556, after #2269 and #2712 for the mcpServers writers). The
// map writers adopt the entry now; the rest say so.
func TestInstallSeesDejaUnderAnotherName(t *testing.T) {
	const old = "/usr/local/bin/deja"
	for _, c := range []struct {
		target, seed string
		path         func() string
		// the writer, for the ones that adopt: driven directly so the binary
		// is named deja, which the test binary is not
		write func(path, exe string, uninstall bool) (installResult, error)
	}{
		{"vscode", `{"servers": {"deja-vu": {"type": "stdio", "command": "` + old + `", "args": ["mcp"]}}}` + "\n",
			func() string { return filepath.Join(vsCodeDefaultUserDir(), "mcp.json") }, installVSCodeMCPAt},
		{"prime", `{"mcpServers": {"deja-vu": {"command": "` + old + `", "args": ["mcp"]}}}` + "\n", primeSettingsPath, installPrimeMCPAt},
		{"amp", `{"amp.mcpServers": {"deja-vu": {"command": "` + old + `", "args": ["mcp"]}}}` + "\n", sources.AmpSettingsFile, installAmpMCPAt},
		{"zcode", `{"mcp": {"servers": {"deja-vu": {"type": "stdio", "command": "` + old + `", "args": ["mcp"]}}}}` + "\n", zcodeConfigPath, zcodeServerAt},
		{"zed", `{"context_servers": {"deja-vu": {"command": "` + old + `", "args": ["mcp"]}}}` + "\n", sources.ZedSettingsPath, nil},
		{"grok", `{"mcp": {"servers": [{"id": "deja-vu", "label": "deja-vu", "transport": "stdio", "command": "` + old + `", "args": ["mcp"], "enabled": true}]}}` + "\n",
			func() string { return filepath.Join(sources.GrokHome(), "user-settings.json") }, nil},
		{"hermes", "mcp_servers:\n  deja-vu:\n    command: " + old + "\n    args: [mcp]\n",
			func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }, nil},
		{"goose", "extensions:\n  deja-vu:\n    enabled: true\n    type: stdio\n    name: deja-vu\n    cmd: " + old + "\n    args: [mcp]\n",
			func() string { return filepath.Join(gooseConfigDir(), "config.yaml") }, nil},
		{"continue", "mcpServers:\n  - name: deja-vu\n    command: " + old + "\n    args: [mcp]\n", continueConfigPath, nil},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			if c.write != nil {
				t.Cleanup(func() { removingWiring = false })
				exe := filepath.Join(t.TempDir(), "deja")
				res, err := c.write(p, exe, false)
				if err != nil {
					t.Fatal(err)
				}
				if got, _ := os.ReadFile(p); strings.Contains(string(got), `"deja":`) {
					t.Errorf("a second entry beside deja-vu (%s):\n%s", res.Note, got)
				}
				// And uninstall takes back what it adopted.
				removingWiring = true
				if _, err := c.write(p, exe, true); err != nil {
					t.Fatal(err)
				}
				if got, _ := os.ReadFile(p); strings.Contains(string(got), "deja-vu") {
					t.Errorf("uninstall left the entry it adopted:\n%s", got)
				}
				return
			}
			out, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"deja-vu" also runs deja`) {
				t.Errorf("install did not say deja-vu also runs deja:\n%s", out)
			}
		})
	}
}

// A filesystem server told to serve ~/code/deja has the binary's name as its
// last argument and is not deja. Install took it over as deja's entry and
// wrote deja's command over it.
func TestInstallLeavesAServerThatOnlyServesADejaPath(t *testing.T) {
	const files = `{"command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/home/me/code/deja"]}`
	for _, c := range []struct {
		name, seed string
		path       func() string
		write      func(path, exe string, uninstall bool) (installResult, error)
	}{
		{"vscode", `{"servers": {"files": ` + files + `}}` + "\n",
			func() string { return filepath.Join(vsCodeDefaultUserDir(), "mcp.json") }, installVSCodeMCPAt},
		{"prime", `{"mcpServers": {"files": ` + files + `}}` + "\n", primeSettingsPath, installPrimeMCPAt},
		{"amp", `{"amp.mcpServers": {"files": ` + files + `}}` + "\n", sources.AmpSettingsFile, installAmpMCPAt},
		{"zcode", `{"mcp": {"servers": {"files": ` + files + `}}}` + "\n", zcodeConfigPath, zcodeServerAt},
		{"claude-code", `{"mcpServers": {"files": ` + files + `}}` + "\n", sources.ClaudeJSONPath,
			func(_, exe string, uninstall bool) (installResult, error) { return installClaude(exe, uninstall) }},
		{"claude-code-jsonc", "{\n  // mine\n  \"mcpServers\": {\"files\": " + files + "}\n}\n", sources.ClaudeJSONPath,
			func(_, exe string, uninstall bool) (installResult, error) { return installClaude(exe, uninstall) }},
		{"codex", "[mcp_servers.files]\ncommand = \"npx\"\nargs = [\"-y\", \"@modelcontextprotocol/server-filesystem\", \"/home/me/code/deja\"]\n",
			func() string { return filepath.Join(sources.CodexHome(), "config.toml") },
			func(_, exe string, uninstall bool) (installResult, error) { return installCodex(exe, uninstall) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := c.write(p, filepath.Join(t.TempDir(), "deja"), false); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); !strings.Contains(string(got), "server-filesystem") {
				t.Errorf("the filesystem server was taken over as deja's entry:\n%s", got)
			}
		})
	}
}

// A snapshot that holds nothing but deja's own block is deja's, and the
// uninstall said it was a config the reader already had. The rule was there —
// writeIfChanged drops a backup that mentionsDeja — but the two "removed" fast
// paths return before the defer that applies it, and taking deja's whole block
// out of a file deja wrote is exactly that path (#3340).
func TestUninstallDropsASnapshotThatIsOnlyDejas(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cordis.patch.yml")
	own := "# deja mcp:start — generated by deja install, safe to delete\n- insert:\n    - id: mcp-deja\n# deja mcp:end\n"
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".bak", []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	removingWiring = true
	createdByThisRun = append(createdByThisRun, path)
	rememberSnapshot(path + ".bak")
	defer func() { removingWiring = false; createdByThisRun = nil; snapshotsByThisRun = nil }()

	got, err := writeIfChanged(path, []byte(own), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "removed" {
		t.Fatalf("state = %q, want removed", got)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("the snapshot of deja's own block is still there, and uninstall calls it a config the reader already had")
	}
}

// And the other side, unchanged since #2604: a snapshot of the reader's own
// config stays, whatever deja does to the live file.
func TestUninstallKeepsASnapshotThatIsTheReadersOwn(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cordis.patch.yml")
	if err := os.WriteFile(path, []byte("# deja mcp:start\n- insert: []\n# deja mcp:end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	theirs := "plugins:\n  - name: something-of-mine\n"
	if err := os.WriteFile(path+".bak", []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	removingWiring = true
	defer func() { removingWiring = false }()

	if _, err := writeIfChanged(path, nil, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("the reader's own snapshot was deleted: %v", err)
	}
	if !strings.Contains(string(b), "something-of-mine") {
		t.Errorf("snapshot = %q, want the reader's own config", b)
	}
}

// And the guard on that: a config the reader wrote themselves is not deja's to
// delete, whatever its snapshot happens to say. mentionsDeja matches a comment
// with the word in it, so on a file deja never created the snapshot stays
// (review of #3340).
func TestUninstallKeepsASnapshotBesideAFileDejaDidNotCreate(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cordis.patch.yml")
	if err := os.WriteFile(path, []byte("# deja mcp:start\n- insert: []\n# deja mcp:end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	theirs := "# my note: I looked at \"deja\" last year\nplugins:\n  - name: something-of-mine\n"
	if err := os.WriteFile(path+".bak", []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}

	removingWiring = true
	defer func() { removingWiring = false }()

	if _, err := writeIfChanged(path, []byte("x"), nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("a snapshot beside a file deja never created was deleted: %v", err)
	}
	if !strings.Contains(string(b), "something-of-mine") {
		t.Errorf("snapshot = %q, want the reader's own config", b)
	}
}

// Provenance, not content: a snapshot the reader put there themselves stays,
// even beside a file deja created and even when its text trips the marker
// list — a comment with the word in it is still their comment (second review
// of #3340).
func TestUninstallKeepsASnapshotDejaDidNotTake(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cordis.patch.yml")
	own := "# deja mcp:start — generated by deja install\n- insert:\n    - id: mcp-deja\n# deja mcp:end\n"
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	theirs := "# I disabled deja: here for now, keeping my own notes\nplugins:\n  - name: something-of-mine\n"
	if err := os.WriteFile(path+".bak", []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}

	removingWiring = true
	createdByThisRun = append(createdByThisRun, path)
	defer func() { removingWiring = false; createdByThisRun = nil; snapshotsByThisRun = nil }()

	if _, err := writeIfChanged(path, []byte(own), nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("a snapshot deja never took was deleted: %v", err)
	}
	if !strings.Contains(string(b), "something-of-mine") {
		t.Errorf("snapshot = %q, want the reader's own file", b)
	}
}

// The write side follows a symlink before taking the snapshot, so the record
// holds the resolved name while the uninstall still has the one it was given —
// on macOS every t.TempDir() path is /var, resolving to /private/var, and a
// config in a dotfiles repository is the real-world case. Both spellings are
// asked, or deja would keep its own snapshot forever (second review of #3340).
func TestUninstallDropsItsOwnSnapshotThroughASymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(link, "cordis.patch.yml")
	own := "# deja mcp:start\n- insert:\n    - id: mcp-deja\n      command: \"deja\"\n# deja mcp:end\n"
	// The file already holds deja's block from an earlier run, so the snapshot
	// taken now is deja's own — the case the drop rule is about.
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	// Install: the snapshot is taken through the link and recorded resolved.
	if _, err := writeIfChanged(path, []byte(own), []byte(own+"# again\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("install took no snapshot: %v", err)
	}

	removingWiring = true
	createdByThisRun = append(createdByThisRun, path)
	defer func() { removingWiring = false; createdByThisRun = nil; snapshotsByThisRun = nil }()

	if _, err := writeIfChanged(path, []byte(own), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("deja kept its own snapshot because the recorded path was the resolved one")
	}
}

// Either spelling finds it: the record now carries the name the caller gave
// as well as the resolved one, because nothing can resolve a real path back to
// the link that pointed at it (second review of #3340).
func TestUninstallDropsASnapshotRecordedUnderTheOtherSpelling(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	own := "# deja mcp:start\n- insert:\n    - id: mcp-deja\n      command: \"deja\"\n# deja mcp:end\n"
	viaLink := filepath.Join(link, "cordis.patch.yml")
	if err := os.WriteFile(viaLink, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(viaLink+".bak", []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	// The record holds the link spelling, which is what an older deja wrote
	// and what any write that did not resolve the path leaves behind.
	snapshotsByThisRun = nil
	rememberSnapshot(viaLink + ".bak")

	// The uninstall arrives with the real path, not the link.
	viaReal := filepath.Join(target, "cordis.patch.yml")
	removingWiring = true
	createdByThisRun = append(createdByThisRun, viaReal)
	defer func() { removingWiring = false; createdByThisRun = nil; snapshotsByThisRun = nil }()
	if _, err := writeIfChanged(viaReal, []byte(own), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(viaReal + ".bak"); err == nil {
		t.Error("deja's own snapshot outlived the uninstall because it was recorded under the other spelling")
	}
}

// A config deja cannot parse makes the target refuse, and the refusal used to
// be a bare parser error: "unexpected end of JSON input — fix what it reports
// and run it again", with the path deja had just read left out. It is the end
// of the trail doctor sends people down after a failed rewire (#2214).
func TestInstallNamesTheConfigItCannotParse(t *testing.T) {
	hermeticEnv(t)
	claudeJSON := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("claude", "/some/deja", false); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: this file parses today, so the refusal below is about the
	// damage and not about the fixture.
	if _, ierr := installTarget("claude", "/some/deja", false); ierr != nil {
		t.Fatalf("a config deja just wrote does not parse: %v", ierr)
	}

	if werr := os.WriteFile(claudeJSON, good[:len(good)/2], 0o644); werr != nil {
		t.Fatal(werr)
	}
	_, err = installTarget("claude", "/some/deja", false)
	if err == nil {
		t.Fatal("a truncated config was accepted")
	}
	if !strings.Contains(err.Error(), claudeJSON) {
		t.Errorf("the refusal names no file, so nobody knows what to fix: %v", err)
	}
	// The parser's own words stay: they say what is wrong with it.
	if !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Errorf("the refusal dropped what the parser said: %v", err)
	}

	// The same for the other writers that read a config of their own.
	settings := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeJSON, good, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"hooks":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("claude-auto", "/some/deja", false); err == nil {
		t.Error("a truncated settings.json was accepted")
	} else if !strings.Contains(err.Error(), settings) {
		t.Errorf("the refusal names no file: %v", err)
	}
}

// The fixture below makes a directory refuse writes, which is a permission bit
// on unix and a suggestion on windows — where the install would simply succeed
// and the test would fail for the wrong reason. Same guard the other
// read-only fixtures in this package use.
func skipWhereModeBitsDoNotRefuse(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the directory mode is not enforced here")
	}
}

// "install finished what it could" returned before the banner and before the
// index build, so a run that wired nine harnesses and failed one said nothing
// about the nine and left them without a store to read (#2721).
func TestAnInstallThatRefusesOneTargetStillReportsTheRest(t *testing.T) {
	skipWhereModeBitsDoNotRefuse(t)
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	// A codex home, so there is a target that can be written beside the one
	// that cannot.
	if err := os.MkdirAll(filepath.Join(tmp, "home", ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	// One config deja cannot write, beside one it can.
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claude, []byte(`{"mcpServers":{}}`), 0o444); err != nil {
		t.Fatal(err)
	}
	unwritable := filepath.Join(home, ".claude")
	if err := os.MkdirAll(unwritable, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unwritable, 0o755) })

	// The banner, which is the screen this is about: the line-by-line output
	// printed its per-target lines before the old return, so only the table
	// could lose them.
	restore := logoWanted
	t.Cleanup(func() { logoWanted = restore })
	logoWanted = func(*os.File) bool { return true }

	out, err := captureRun(t, "install", "--all", "--no-index")
	if err == nil {
		t.Fatal("a target that could not be written was reported as installed")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("the refusal did not name itself: %v", err)
	}
	if !strings.Contains(out, "codex") {
		t.Errorf("the table never named the target that was written:\n%s\nerror: %v", out, err)
	}
	if strings.Contains(out, "claude-code") {
		t.Errorf("the table named a target that refused:\n%s", out)
	}
}

// And the store: a run that wired something ends with a build, because
// installing is the one moment a person has already accepted a wait. The
// refusal was taking that away too.
func TestAnInstallThatRefusesOneTargetStillBuildsTheIndex(t *testing.T) {
	skipWhereModeBitsDoNotRefuse(t)
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"the zonkoshard retry budget"},` +
		`"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{}}`), 0o444); err != nil {
		t.Fatal(err)
	}
	unwritable := filepath.Join(home, ".claude")
	if err := os.MkdirAll(unwritable, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unwritable, 0o755) })

	if _, err := captureRun(t, "install", "--all"); err == nil {
		t.Fatal("a target that could not be written was reported as installed")
	}
	if _, err := os.Stat(filepath.Join(tmp, "index.db", "manifest.gob")); err != nil {
		t.Errorf("an install that wired a harness left it with no index: %v", err)
	}
}

// fakePiOnPath puts a pi command on PATH beside the npm package it starts,
// the layout npm leaves on Windows and the one piPackageVersion falls back to.
func fakePiOnPath(t *testing.T, pkg, version string) {
	t.Helper()
	bin := t.TempDir()
	name := "pi"
	if runtime.GOOS == "windows" {
		name = "pi.cmd"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(bin, "node_modules", filepath.FromSlash(pkg))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + pkg + `","version":"` + version + `"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func piMCPRow(t *testing.T) doctorMCPStatus {
	t.Helper()
	for _, r := range collectDoctorMCP() {
		if r.Name == "pi" {
			return r
		}
	}
	t.Fatal("no pi row")
	return doctorMCPStatus{}
}

// pi 0.99 reads ~/.pi/agent/mcp.json itself: `pi mcp list` on 1.3.0 shows
// deja connected with no adapter installed. Install still told the person to
// install pi-mcp-adapter and doctor called the row `no adapter`.
func TestPiBuiltinMCPReadsTheFile(t *testing.T) {
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	fakePiOnPath(t, "@earendil-works/pi-coding-agent", "1.3.0")
	r, err := installPiAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if strings.Contains(r.Note, "pi-mcp-adapter") {
		t.Errorf("pi 1.3.0 reads mcp.json itself and install asks for the adapter: %q", r.Note)
	}
	if row := piMCPRow(t); row.State != "wired" {
		t.Errorf("pi 1.3.0 reads mcp.json itself and doctor says %q: %+v", row.State, row)
	}
}

// With the built-in extension switched off and no adapter, nothing reads the
// file, and the note names the setting that did it.
func TestPiBuiltinMCPSwitchedOff(t *testing.T) {
	for _, tc := range []struct {
		name, user, project string
		off                 bool
	}{
		{"user minus", `["-builtin:mcp"]`, "", true},
		{"user glob", `["!builtin:*"]`, "", true},
		{"glob brought back", `["!builtin:*", "+builtin:mcp"]`, "", false},
		{"minus beats plus", `["+builtin:mcp", "-builtin:mcp"]`, "", true},
		{"project minus", "", `["-builtin:mcp"]`, true},
		{"project brings it back", `["-builtin:mcp"]`, `["+builtin:mcp"]`, false},
		{"other builtin", `["-builtin:llama"]`, "", false},
		{"dot slash minus", `["-./builtin:mcp"]`, "", true},
		{"dot slash plus", `["!builtin:*", "+./builtin:mcp"]`, "", false},
		{"globstar", `["!**/*"]`, "", true},
		{"braces", `["!{builtin:mcp,builtin:llama}"]`, "", true},
		{"braces other", `["!{builtin:llama,builtin:codemode}"]`, "", false},
		{"untrusted project ignored", "", `["-builtin:mcp"]`, false},
		{"brace without comma is literal", `["!builtin:{mcp}"]`, "", false},
		{"class body is literal", `["![\\w]uiltin:mcp"]`, "", false},
		{"class", `["![ab]uiltin:mcp"]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			proj := t.TempDir()
			t.Chdir(proj)
			fakePiOnPath(t, "@earendil-works/pi-coding-agent", "1.3.0")
			write := func(p, list string) {
				if list == "" {
					return
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(`{"extensions": `+list+`}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(sources.PiConfigDir(), "settings.json"), tc.user)
			write(filepath.Join(proj, ".pi", "settings.json"), tc.project)
			if tc.project != "" && !strings.HasPrefix(tc.name, "untrusted") {
				resolved, _ := filepath.EvalSymlinks(proj)
				trust, _ := json.Marshal(map[string]bool{resolved: true})
				if err := os.MkdirAll(sources.PiConfigDir(), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sources.PiConfigDir(), "trust.json"), trust, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r, err := installPiAuto("/bin/deja", false)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			row := piMCPRow(t)
			if tc.off {
				if !strings.Contains(r.Note, "built-in MCP is switched off") || row.State != "no-adapter" {
					t.Errorf("builtin:mcp is off and nothing reads mcp.json:\nnote %q\nrow %+v", r.Note, row)
				}
				return
			}
			if strings.Contains(r.Note, "switched off") || strings.Contains(r.Note, "pi-mcp-adapter") || row.State != "wired" {
				t.Errorf("pi's built-in MCP reads mcp.json here:\nnote %q\nrow %+v", r.Note, row)
			}
		})
	}
}

// pi moves its whole agent directory with PI_CODING_AGENT_DIR. deja kept
// writing ~/.pi/agent, so pi loaded neither the extension nor the MCP entry.
func TestPiAgentDirFollowsTheEnvironment(t *testing.T) {
	hermeticEnv(t)
	agent := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	if _, err := installPiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, p := range []string{filepath.Join(agent, "mcp.json"), filepath.Join(agent, "extensions", "deja.ts")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("PI_CODING_AGENT_DIR=%s and %s was not written: %v", agent, p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sources.Home(), ".pi")); err == nil {
		t.Errorf("install wrote ~/.pi while pi lives in %s", agent)
	}
	if got := sources.PiRoot(); got != filepath.Join(agent, "sessions") {
		t.Errorf("sessions read from %s, pi writes them under %s", got, agent)
	}
}

// A shim that hides the package (pnpm, Volta, a Nix wrapper) still says its
// version when asked, so an old pi behind one keeps the adapter note.
func TestPiVersionFromTheCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	hermeticEnv(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\necho 0.73.1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if v := piPackageVersion(); v != "0.73.1" {
		t.Fatalf("version %q, the command says 0.73.1", v)
	}
	if note := piMCPUnreadNote(); !strings.Contains(note, "pi-mcp-adapter") {
		t.Errorf("pi 0.73.1 without the adapter and no note: %q", note)
	}
}

// PI_CODING_AGENT_SESSION_DIR moves pi's sessions alone.
func TestPiSessionDirFollowsTheEnvironment(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	if got := sources.PiRoot(); got != dir {
		t.Errorf("sessions read from %s, pi writes them to %s", got, dir)
	}
}

// The version shim's own output may carry a notice, and a wrapper that does
// not exec its child must not hold doctor past the timeout.
func TestPiVersionCommandShapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	for _, tc := range []struct{ name, script, want string }{
		{"notice after", "echo 0.73.1; echo 'Update available: 1.3.0'", "0.73.1"},
		{"noise before", "echo 'pi v0.73.1'", "0.73.1"},
		{"silent", ":", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\n"+tc.script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if v := piPackageVersion(); v != tc.want {
				t.Errorf("version %q, want %q", v, tc.want)
			}
		})
	}
	t.Run("wrapper that never exits", func(t *testing.T) {
		hermeticEnv(t)
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\nsleep 30; echo 0.73.1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		start := time.Now()
		piPackageVersion()
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("pi --version held the caller %s", d)
		}
	})
}

func TestInstallPiExtensionWritesDiscoverableFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installPiExtension("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	// pi auto-discovers ~/.pi/agent/extensions/*.ts; anywhere else is inert.
	path := filepath.Join(home, ".pi", "agent", "extensions", "deja.ts")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("extension not at the discovered path: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"before_agent_start", // the only event that can inject a message
		"hook-context",
		"hook-prompt",
		"ctx.ui.notify",
		"ctx.ui.setStatus", // pi keeps a footer line; the build belongs there
		"session_start",    // status has to appear before the first prompt
		strconv.Quote(hookExeInConfigs("/bin/deja")), // the launcher (#3682)
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("extension missing %q:\n%s", want, src)
		}
	}
}

func TestInstallPiExtensionRemovesOnUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".pi", "agent", "extensions", "deja.ts")
	if _, err := installPiExtension("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installPiExtension("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("extension survived uninstall: %v", err)
	}
	// Uninstalling twice is not an error.
	if r, err := installPiExtension("/bin/deja", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("second uninstall = %+v, %v", r, err)
	}
}

func TestPiExtensionQuotesExecutablePath(t *testing.T) {
	// A path with a quote or backslash must not break out of the TS string.
	src := piExtensionTS(`/tmp/we"ird\path/deja`)
	if !strings.Contains(src, `const DEJA = "/tmp/we\"ird\\path/deja";`) {
		t.Fatalf("executable path not escaped:\n%s", src)
	}
}

// The command handler takes its argument as `args`, so the search array cannot
// also be `const args`: pi parses the extension as TypeScript and refuses the
// whole file with "Identifier 'args' has already been declared", which takes
// deja's command — and the agent's startup — down with it (#3089).
func TestPiCommandDoesNotRedeclareItsArgument(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	if !strings.Contains(src, "handler: async (args: string") {
		t.Fatalf("the handler no longer takes args, so this guard is stale:\n%s", src)
	}
	if strings.Contains(src, "const args ") || strings.Contains(src, "const args=") {
		t.Fatalf("the search array shadows the handler's `args` parameter, which pi rejects:\n%s", src)
	}
}

// The fix pair only lands if it goes out on tool_result: measured on pi 0.73.1,
// tool_execution_start and tool_execution_end are observe-only, and what their
// handlers return reaches neither the transcript nor the model.
func TestPiRepairsFailedCommandsOnToolResult(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	for _, want := range []string{
		`pi.on("tool_result"`,
		`"hook-tool-after", "--plain"`,
		"event.isError",         // a command that succeeded needs no repair
		"tool_response: output", // deja matches on what the command printed
		"event.toolCallId",      // one lookup per call, not per re-render
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("no fix pair at the point of action, missing %q:\n%s", want, src)
		}
	}
	// Observe-only events cannot carry it, so nothing may be wired there.
	for _, wrong := range []string{
		`pi.on("tool_execution_start"`,
		`pi.on("tool_execution_end"`,
	} {
		if strings.Contains(src, wrong) {
			t.Fatalf("the repair is wired to %s, whose return value pi drops:\n%s", wrong, src)
		}
	}
}

// The file's history has to go out on the read, because pi has no seam that
// runs before an edit: tool_call can only block a tool or rewrite its
// arguments, and by tool_result the edit is already on disk. The read is the
// step an agent takes first.
func TestPiCarriesFileHistoryOnRead(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	for _, want := range []string{
		`event.toolName === "read"`,
		`"hook-tool", "--plain"`,
		"event.input.path", // pi names it path, not file_path
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("no file history at the read, missing %q:\n%s", want, src)
		}
	}
}

// pi names the event session_compact. "compaction" — the name the docs use for
// the feature — registers a handler that never fires, so the session keeps its
// list of shown blocks and stays quiet about a fix it could repeat.
func TestPiForgetsOnTheCompactionEventPiActuallyEmits(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	if !strings.Contains(src, `pi.on("session_compact"`) {
		t.Fatalf("nothing forgets after compaction:\n%s", src)
	}
	if !strings.Contains(src, "hook-precompact") {
		t.Fatalf("session_compact is wired to something other than the forget hook:\n%s", src)
	}
	if strings.Contains(src, `pi.on("compaction"`) {
		t.Fatalf("wired to an event pi does not emit:\n%s", src)
	}
}

// A search the user typed may rebuild the index; a hook may not. One shared
// timeout made /deja answer "nothing matches" for a query with real hits.
func TestPiCommandOutlivesTheHookTimeout(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	if !strings.Contains(src, "120000") {
		t.Fatalf("the slash command runs on the hook budget:\n%s", src)
	}
	if !strings.Contains(src, "timeout = 10000") {
		t.Fatalf("the hook budget is no longer the default:\n%s", src)
	}
}

// Senpi and the pi-coding-agent Kimchi ships have a powershell tool beside
// bash, with the same {command}; a failed PowerShell run got no fix pair, and
// hook-tool-after did not take the lowercase name for a command (#4523).
func TestPiRepairsAFailedPowerShellRun(t *testing.T) {
	src := piExtensionTS("/bin/deja")
	if !strings.Contains(src, `event.toolName !== "powershell"`) {
		t.Fatalf("the failure hook still stops at bash:\n%s", src)
	}
	if !strings.Contains(src, "tool_name: event.toolName") {
		t.Fatalf("the failure hook does not say which shell ran:\n%s", src)
	}
	if !isCommandTool("powershell") {
		t.Error("hook-tool-after does not read powershell as a command tool")
	}
}

// A target that wires more than one file wrote the first and refused the
// second, so a run reported as refused had already changed a config and left a
// snapshot beside it. What a reader is told is "refused"; what is on disk is
// half an install (#2744).
func TestATargetThatWillRefuseWritesNothing(t *testing.T) {
	for _, c := range []struct {
		target string
		// the file the target cannot read, and the one it would have written
		// first
		unreadable func() string
		written    func() string
	}{
		{"cursor-auto",
			func() string { return filepath.Join(sources.CursorCLIHome(), "hooks.json") },
			func() string { return filepath.Join(sources.CursorCLIHome(), "mcp.json") }},
		{"claude-auto",
			func() string { return filepath.Join(sources.ClaudeConfigDir(), "settings.json") },
			sources.ClaudeJSONPath},
		{"codex-auto",
			func() string { return filepath.Join(sources.CodexHome(), "hooks.json") },
			func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"grok-auto",
			func() string { return filepath.Join(sources.GrokHome(), "user-settings.json") },
			func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
		// grok wires a third file, and it is the last one: a refusal there
		// used to land after both of the others were written.
		{"grok-auto",
			func() string { return filepath.Join(sources.GrokHome(), "hooks", "deja.json") },
			func() string { return filepath.Join(sources.GrokHome(), "user-settings.json") }},
	} {
		t.Run(c.target+" "+filepath.Base(c.unreadable()), func(t *testing.T) {
			hermeticEnv(t)
			bad, first := c.unreadable(), c.written()
			for _, dir := range []string{filepath.Dir(bad), filepath.Dir(first)} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			// A file the hook writers cannot edit: comments are the ordinary
			// way to get there, and #2739 deliberately does not help here.
			if err := os.WriteFile(bad, []byte("{\n  // mine\n  \"hooks\": {}\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := "{\n  \"mcpServers\": {}\n}\n"
			if strings.HasSuffix(first, ".toml") {
				before = "[tools]\nweb = true\n"
			}
			if err := os.WriteFile(first, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := captureRun(t, "install", c.target, "--no-index"); err == nil {
				t.Fatal("a target that cannot finish reported itself installed")
			}
			b, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != before {
				t.Errorf("the first config was written before the refusal:\n%s", b)
			}
			if _, err := os.Stat(first + ".bak"); err == nil {
				t.Errorf("a snapshot was left beside a file that was never changed")
			}
		})
	}
}

// And a target whose files are all readable still installs.
func TestThePreflightDoesNotRefuseAReadableConfig(t *testing.T) {
	hermeticEnv(t)
	hooks := filepath.Join(sources.CursorCLIHome(), "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooks), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks, []byte(`{"version":1,"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor-auto", "--no-index"); err != nil {
		t.Fatalf("a readable config was refused: %v", err)
	}
	b, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "hook-context") {
		t.Errorf("the hook was not written:\n%s", b)
	}
}

// The check itself: a file that is not there, an empty one and a readable one
// are all fine, and only a broken one is named.
func TestReadableStrictJSONNamesOnlyWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	empty := filepath.Join(dir, "empty.json")
	good := filepath.Join(dir, "good.json")
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(empty, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("{\n // c\n}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := readableStrictJSON(missing, empty, good); err != nil {
		t.Errorf("a readable set was refused: %v", err)
	}
	err := readableStrictJSON(good, bad, missing)
	if err == nil {
		t.Fatal("a file that cannot be parsed was accepted")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
}

// A config that parses is not yet a config the writers can use: they decode
// into a map, so `null` left them assigning into a nil map — a panic — and a
// list refused one write too late.
func TestThePreflightRefusesJSONThatIsNotAnObject(t *testing.T) {
	for _, body := range []string{"null", "[1, 2]", `"a string"`} {
		t.Run(body, func(t *testing.T) {
			hermeticEnv(t)
			hooks := filepath.Join(sources.CursorCLIHome(), "hooks.json")
			mcp := filepath.Join(sources.CursorCLIHome(), "mcp.json")
			if err := os.MkdirAll(filepath.Dir(hooks), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(hooks, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			before := "{\n  \"mcpServers\": {}\n}\n"
			if err := os.WriteFile(mcp, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "cursor-auto", "--no-index"); err == nil {
				t.Fatal("a config the writers cannot use was accepted")
			}
			b, err := os.ReadFile(mcp)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != before {
				t.Errorf("the other config was written first:\n%s", b)
			}
		})
	}
}

// A file that is there but cannot be read is not a file that is absent.
func TestThePreflightPassesOnAReadErrorRatherThanSkippingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The mode bits are the whole fixture, and Windows does not read them
		// — a 0o000 file opens there, so the test would be asserting that a
		// readable file is unreadable (#2808).
		t.Skip("a file mode is not what decides this on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.json")
	if err := os.WriteFile(path, []byte(`{"a":1}`), 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads it anyway")
	}
	if err := readableStrictJSON(path); err == nil {
		t.Error("a file that could not be read was taken for a readable one")
	}
}

// grok's hook file is read on the way in and removed on the way out, so a
// file the install refuses is one the uninstall still has to take — otherwise
// grok keeps calling a binary that is no longer wired anywhere else.
func TestUninstallStillTakesAHookFileTheInstallWouldRefuse(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "grok-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	hooks := grokHooksPath()
	if err := os.WriteFile(hooks, []byte("{\n  // mine\n  \"hooks\": []\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "grok-auto"); err != nil {
		t.Fatalf("uninstall refused a hook file it only had to remove: %v", err)
	}
	if _, err := os.Stat(hooks); !os.IsNotExist(err) {
		t.Errorf("the hook file survived the uninstall")
	}
}

// Nobody installs into an empty home. They install into a config they have
// already set up, and the one thing deja must never do is take any of it away.
// Every case here seeds a file the way a person would have it, installs,
// uninstalls, and checks their content is still there both times.
func TestInstallPreservesWhatWasAlreadyThere(t *testing.T) {
	cases := []struct {
		target string
		rel    string // config path, relative to home
		before string
		keep   []string
	}{
		{"qwen-auto", ".qwen/settings.json",
			`{"theme":"dark","hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"/usr/bin/theirs"}]}]}}`,
			[]string{`"theme"`, "/usr/bin/theirs"}},
		{"kimi-auto", ".kimi-code/config.toml",
			"default_model = \"theirs\"\n\n[[hooks]]\nevent = \"SessionStart\"\ncommand = \"/usr/bin/theirs\"\n",
			[]string{`default_model = "theirs"`, "/usr/bin/theirs"}},
		{"goose-auto", ".config/goose/config.yaml",
			"GOOSE_PROVIDER: openai\nextensions:\n  theirs:\n    enabled: true\n    cmd: \"/usr/bin/theirs\"\n",
			[]string{"GOOSE_PROVIDER: openai", "theirs:", "/usr/bin/theirs"}},
		{"codex-auto", ".codex/hooks.json",
			`{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"/usr/bin/theirs"}]}]}}`,
			[]string{"/usr/bin/theirs"}},
		{"grok-auto", ".grok/user-settings.json",
			`{"apiKey":"theirs","defaultModel":"grok-4"}`,
			[]string{`"apiKey"`, "grok-4"}},
		{"opencode-auto", ".config/opencode/opencode.json",
			`{"model":"theirs/model","mcp":{"theirs":{"type":"local","command":["/usr/bin/theirs"]}}}`,
			[]string{"theirs/model", "/usr/bin/theirs"}},
	}

	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			setTestHome(t, home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			path := homeConfigPath(home, tc.rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installTarget(tc.target, "/usr/local/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			after := readFile(t, path)
			// The test means nothing if the install did not touch this file.
			if !dejaWiringPresent(after, "/usr/local/bin/deja") {
				t.Fatalf("install wrote no deja wiring into %s; this case proves nothing:\n%s", tc.rel, after)
			}
			for _, want := range tc.keep {
				if !strings.Contains(after, want) {
					t.Errorf("install dropped %q from their config:\n%s", want, after)
				}
			}

			removingWiring = true
			_, uerr := installTarget(tc.target, "/usr/local/bin/deja", true)
			removingWiring = false
			if uerr != nil {
				t.Fatalf("uninstall: %v", uerr)
			}
			restored := readFile(t, path)
			for _, want := range tc.keep {
				if !strings.Contains(restored, want) {
					t.Errorf("uninstall dropped %q from their config:\n%s", want, restored)
				}
			}
			if strings.Contains(restored, "/usr/local/bin/deja") {
				t.Errorf("uninstall left deja behind in their config:\n%s", restored)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// A settings.json with a comment was re-marshalled whole on install, and the
// comment was gone for good; the JSONC path keeps every other byte.
func TestInstallPrimeKeepsTheComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	seed := "{\n  // keep me\n  \"mcpServers\": {\n    \"fs\": {\"type\": \"stdio\", \"command\": \"fs-mcp\", \"args\": []}\n  }\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installPrimeMCPAt(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "// keep me") || !strings.Contains(string(b), `"deja"`) || !strings.Contains(string(b), `"fs"`) {
		t.Fatalf("install lost the comment or a server:\n%s", b)
	}
	if _, err := installPrimeMCPAt(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != seed {
		t.Fatalf("uninstall did not give the file back:\n%s", got)
	}
}

func primeSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("settings are not JSON prime-agent can read: %v\n%s", err, b)
	}
	return root
}

func TestInstallPrimeWiresTheMCPServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	r, err := installPrimeMCP("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".prime", "agent", "settings.json")
	if r.Path != want {
		t.Fatalf("wrote %q, want %q — prime-agent reads its own settings file", r.Path, want)
	}
	servers, _ := primeSettings(t, r.Path)["mcpServers"].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if entry == nil {
		t.Fatalf("no deja server under mcpServers: %v", servers)
	}
	// prime-agent's settings carry stdio and http servers under one key and its
	// docs write the type out, so an entry without it is a guess.
	if entry["type"] != "stdio" {
		t.Fatalf("server entry has no stdio type: %v", entry)
	}
	if entry["command"] == nil || entry["args"] == nil {
		t.Fatalf("server entry is missing command/args: %v", entry)
	}
}

// Someone else's servers and the rest of the settings file have to survive
// both directions.
func TestInstallPrimeKeepsOtherSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".prime", "agent", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{"mcpServers":{"linear":{"type":"http","url":"https://mcp.example.com/mcp"}},"theme":"dark"}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installPrimeMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := primeSettings(t, path)
	servers, _ := root["mcpServers"].(map[string]any)
	if servers["linear"] == nil || servers["deja"] == nil {
		t.Fatalf("install did not sit beside the existing server: %v", root)
	}
	if root["theme"] != "dark" {
		t.Fatalf("an unrelated setting was dropped: %v", root)
	}
	if _, err := installPrimeMCP("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	root = primeSettings(t, path)
	servers, _ = root["mcpServers"].(map[string]any)
	if servers["deja"] != nil {
		t.Fatalf("uninstall left the deja entry: %v", root)
	}
	if servers["linear"] == nil {
		t.Fatalf("uninstall took someone else's server with it: %v", root)
	}
}

func TestInstallPrimeAutoWritesADiscoverableExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := installPrimeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	// prime-agent auto-discovers ~/.prime/agent/extensions/*.ts; anywhere else
	// is inert.
	path := filepath.Join(home, ".prime", "agent", "extensions", "deja.ts")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("extension not at the discovered path: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		`pi.on("before_agent_start"`, // the one event whose return reaches the model
		"hook-context",
		"hook-prompt",
		`pi.on("session_compact"`,
		"hook-precompact",
		"registerCommand",
		"ctx.ui.setStatus",
		strconv.Quote(hookExeInConfigs("/usr/local/bin/deja")), // the launcher (#3682)
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("extension missing %q:\n%s", want, src)
		}
	}
	// The MCP half goes in at the same time.
	if primeSettings(t, filepath.Join(home, ".prime", "agent", "settings.json"))["mcpServers"] == nil {
		t.Fatal("prime-auto wrote no MCP server")
	}
}

// tool_result fires on prime-agent 0.9.8 for its one tool, ipython; the probe
// that found it silent on 0.9.1 called a bash tool the model is never given.
// tool_call still carries nothing the model reads.
func TestPrimeExtensionWiresTheToolResult(t *testing.T) {
	src := primeExtensionTS("/bin/deja")
	if strings.Contains(src, `pi.on("tool_call"`) {
		t.Fatalf("wired to tool_call, whose return reaches nothing:\n%s", src)
	}
	for _, want := range []string{`pi.on("tool_result"`, `"ipython"`, "hook-tool-after", `"hook-tool", "--plain"`} {
		if !strings.Contains(src, want) {
			t.Fatalf("extension missing %s:\n%s", want, src)
		}
	}
}

func TestUninstallPrimeAutoRemovesTheExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := installPrimeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installPrimeAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".prime", "agent", "extensions", "deja.ts")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("extension survived uninstall: %v", err)
	}
	if r, err := installPrimeExtension("/usr/local/bin/deja", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("second uninstall = %+v, %v", r, err)
	}
}

func TestPrimeExtensionQuotesTheBinaryPath(t *testing.T) {
	src := primeExtensionTS(`C:\Program Files\deja\deja.exe`)
	if !strings.Contains(src, `const DEJA = "C:\\Program Files\\deja\\deja.exe"`) {
		t.Fatalf("a Windows path was not escaped for TypeScript:\n%s", src)
	}
}

// The same shadowing trap pi hit (#3089): the command handler takes `args`, so
// the search array cannot also be `const args` — prime-agent parses the file as
// TypeScript and would refuse the whole extension.
func TestPrimeCommandDoesNotRedeclareItsArgument(t *testing.T) {
	src := primeExtensionTS("/bin/deja")
	if !strings.Contains(src, "handler: async (args: string") {
		t.Fatalf("the handler no longer takes args, so this guard is stale:\n%s", src)
	}
	if strings.Contains(src, "const args ") || strings.Contains(src, "const args=") {
		t.Fatalf("the search array shadows the handler's args parameter:\n%s", src)
	}
	if !strings.Contains(src, `["search", "--", query]`) {
		t.Fatalf("a query that names a deja flag dies in flag parsing:\n%s", src)
	}
}

// The proof after `deja install --auto` listed three recent sessions — titles
// the reader typed last week and already knows. What they do not know is the
// error their agents have hit in a dozen separate sessions, which is the one
// thing the index holds that they never kept score of. That is the line the
// install shows now, and it is the same rule `friction` prints by (#2966).
func TestInstallProofNamesTheRecurringError(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// One error across four sessions, one session that is fine.
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("s%d", i)
		writeClaudeFixture(t, filepath.Join(root, id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-07-2` + strconv.Itoa(i) + `T10:00:00Z","message":{"role":"user","content":"deploy it"}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-07-2` + strconv.Itoa(i) + `T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"zsh: command not found: timeout"}]}}`,
		})
	}
	writeClaudeFixture(t, filepath.Join(root, "ok.jsonl"), "ok", []string{
		`{"type":"user","sessionId":"ok","cwd":"/w/p","timestamp":"2026-07-28T10:00:00Z","message":{"role":"user","content":"all green today"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, func() { printInstallProof(index.DefaultDir()) })
	if !strings.Contains(out, "command not found: timeout") || !strings.Contains(out, "4 sessions") {
		t.Errorf("the proof does not name the error this machine keeps hitting:\n%s", out)
	}
	// Above the listing, not below it: the listing is what the reader already
	// knows, and the line is the reason to keep reading.
	if at, list := strings.Index(out, "command not found"), strings.Index(out, "[claude"); at < 0 || list < 0 || at > list {
		t.Errorf("the recurring error sits below the session listing:\n%s", out)
	}
}

// And nothing when nothing recurs — a line that says "this machine keeps
// hitting …" over a single sighting would be the tool inventing a pattern.
func TestInstallProofStaysQuietWithoutARecurringError(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := captureStderr(t, func() { printInstallProof(index.DefaultDir()) })
	if strings.Contains(out, "keeps hitting") || strings.Contains(out, "has hit") {
		t.Errorf("the proof claims a recurring error on a store that has none:\n%s", out)
	}
	if !strings.Contains(out, "deja already knows this machine:") {
		t.Errorf("the listing itself went missing:\n%s", out)
	}
}

func TestInstallProofShowsCurrentProjectRecallPreview(t *testing.T) {
	hermeticEnv(t)
	projDir := filepath.Join(t.TempDir(), "work", "orbit")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projDir)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-orbit")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	slashDir := filepath.ToSlash(projDir)
	writeClaudeFixture(t, filepath.Join(root, "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","cwd":"` + slashDir + `","timestamp":"2026-07-28T10:00:00Z","message":{"role":"user","content":"wire the queue consumer"}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"` + slashDir + `","timestamp":"2026-07-28T10:01:00Z","message":{"role":"assistant","content":"We decided to avoid global mutexes in the queue consumer and pass a channel per worker instead."}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := captureStderr(t, func() { printInstallProof(index.DefaultDir()) })
	if !strings.Contains(out, "settled —") || !strings.Contains(out, "avoid global mutexes") {
		t.Fatalf("expected live project recall preview in install proof, got:\n%s", out)
	}
}

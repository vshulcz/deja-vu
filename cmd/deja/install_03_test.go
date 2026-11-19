package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// CodeWhale reads `servers` in mcp.json and starts a server only when
// tools.always_load names its tool; with both, a 0.10.0 stand had deja's tool
// in the request. Uninstall takes both out and leaves the reader's config.
func TestInstallCodeWhaleWritesTheServerAndLoadsItsTool(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("CODEWHALE_HOME", "")
	home := os.Getenv("HOME")
	cfg := filepath.Join(home, ".codewhale", "config.toml")
	theirs := "provider = \"openai\"\n\n[tools]\nalways_load = [\"git_show\"]\n"
	writeFileMkdir(t, cfg, theirs)

	if _, err := captureRun(t, "install", "codewhale-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// The test binary is deja.test, which is deja's only by the wiring record,
	// and the record is cached per process: read before this install wrote it,
	// the cache failed the check whenever an earlier test had filled it.
	forgetWrittenExes()
	var mcp struct {
		Servers map[string]map[string]any `json:"servers"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(home, ".codewhale", "mcp.json"))), &mcp); err != nil {
		t.Fatal(err)
	}
	if !entryRunsDeja(mcp.Servers["deja"]) {
		t.Errorf("mcp.json has no deja server: %+v", mcp.Servers)
	}
	got := readFile(t, cfg)
	if !strings.Contains(got, `always_load = ["git_show", "mcp_deja_deja"]`) {
		t.Errorf("always_load not extended:\n%s", got)
	}
	for _, ev := range codewhaleHookEvents {
		if !strings.Contains(got, "event = \""+ev+"\"") || !strings.Contains(got, "hook-codewhale "+ev) {
			t.Errorf("no %s hook:\n%s", ev, got)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "deja-history", "SKILL.md")); err != nil {
		t.Errorf("no shared skill: %v", err)
	}

	if _, err := captureRun(t, "install", "codewhale-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, cfg); again != got {
		t.Errorf("second install changed config.toml:\n%s", again)
	}

	if _, err := captureRun(t, "uninstall", "codewhale-auto"); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, cfg); after != theirs {
		t.Errorf("uninstall left config.toml as\n%q\nwant\n%q", after, theirs)
	}
}

func TestTomlListedUnder(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "[tools]\nalways_load = [\"x\"]\n"},
		{"a = 1\n", "a = 1\n\n[tools]\nalways_load = [\"x\"]\n"},
		{"[tools]\nb = 2\n", "[tools]\nalways_load = [\"x\"]\nb = 2\n"},
		{"[tools]\nalways_load = [\"y\"] # mine\n[other]\n", "[tools]\nalways_load = [\"y\", \"x\"]\n[other]\n"},
		{"[tools]\nalways_load = [\"x\"]\n", "[tools]\nalways_load = [\"x\"]\n"},
	}
	for _, c := range cases {
		got, err := tomlListedUnder(c.in, "tools", "always_load", "x", false)
		if err != nil || got != c.want {
			t.Errorf("add to %q = %q, %v; want %q", c.in, got, err, c.want)
		}
		back, err := tomlListedUnder(got, "tools", "always_load", "x", true)
		if err != nil {
			t.Fatal(err)
		}
		if c.in != "[tools]\nalways_load = [\"x\"]\n" && strings.Contains(back, `"x"`) {
			t.Errorf("remove from %q left %q", got, back)
		}
	}
	if _, err := tomlListedUnder("[tools]\nalways_load = [\n  \"y\",\n]\n", "tools", "always_load", "x", false); err == nil {
		t.Error("a multi-line array was edited")
	}
	if _, err := tomlListedUnder("tools.always_load = []\n", "tools", "always_load", "x", false); err == nil {
		t.Error("a dotted key was not refused")
	}
}

// message_submit answers with the person's text first and deja's additions
// framed after it; nothing at all when deja has nothing to add, so CodeWhale
// keeps the message as typed.
func TestHookCodeWhaleMessageSubmitKeepsTheTextFirst(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	var out bytes.Buffer
	in := `{"event":"message_submit","text":"fix the flaky test","session_id":"sess_1","workspace":"/w"}`
	if err := runHookCodeWhale(dir, []string{"message_submit"}, strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimSpace(out.String()); s != "" {
		var resp struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(s), &resp); err != nil {
			t.Fatalf("not JSON: %q", s)
		}
		if !strings.HasPrefix(resp.Text, "fix the flaky test\n\n<deja-recall>") {
			t.Errorf("text = %q", resp.Text)
		}
	}
	if err := runHookCodeWhale(dir, []string{"nope"}, strings.NewReader(""), &out); err == nil {
		t.Error("an unknown event was accepted")
	}
}

// tool_call_before reads the tool from the environment and never sends a
// decision: allow and none are the same to CodeWhale.
func TestHookCodeWhaleToolCallBeforeSendsNoDecision(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEEPSEEK_TOOL_NAME", "edit")
	t.Setenv("DEEPSEEK_TOOL_ARGS", `{"path":"main.go"}`)
	t.Setenv("DEEPSEEK_SESSION_ID", "sess_1")
	t.Setenv("DEEPSEEK_WORKSPACE", t.TempDir())
	var out bytes.Buffer
	if err := runHookCodeWhale(t.TempDir(), []string{"tool_call_before"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "decision") {
		t.Errorf("hook sent a decision: %s", out.String())
	}
}

// A message near CodeWhale's limit leaves deja little room, and the cut came
// after framing: the reply ended mid-digest with `<deja-recall>` open (#5051).
// What is added is cut inside its frame, which still closes.
func TestHookCodeWhaleMessageSubmitCutsInsideTheFrame(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	key := deferredKey("cw-1", "/w")
	if err := os.MkdirAll(filepath.Join(deferredRoot(dir), key), 0o700); err != nil {
		t.Fatal(err)
	}
	waiting := frameRecall(strings.Repeat("the frobnicator uses zorblax. ", 200))
	if err := os.WriteFile(filepath.Join(deferredRoot(dir), key, "a"), []byte(waiting), 0o600); err != nil {
		t.Fatal(err)
	}
	typed := "please look at this log " + strings.Repeat("x", 31000)
	in, jerr := json.Marshal(map[string]string{"text": typed, "session_id": "cw-1", "workspace": "/w"})
	if jerr != nil {
		t.Fatal(jerr)
	}
	var out bytes.Buffer
	if err := runHookCodeWhale(dir, []string{"message_submit"}, bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("not JSON: %q", out.String())
	}
	if len(resp.Text) > codewhaleTextMax {
		t.Fatalf("reply is %d bytes against %d", len(resp.Text), codewhaleTextMax)
	}
	added := strings.TrimPrefix(resp.Text, typed+"\n\n")
	if !strings.Contains(added, "zorblax") {
		t.Fatalf("nothing was added, so this proves nothing: %q", added)
	}
	if strings.Count(added, "<deja-recall>") != 1 || !strings.HasSuffix(added, recallFrameFooter) {
		t.Errorf("the cut left the frame open: …%q", added[max(0, len(added)-80):])
	}
}

func TestFitFramedDropsABlockWithNoRoomForItsFrame(t *testing.T) {
	a, b := frameRecall("first"), frameRecall(strings.Repeat("second ", 50))
	over := len(recallFrameHeader) + len(recallFrameFooter)
	if got := fitFramed([]string{a, b}, "\n\n", len(a)+2+over); got != a {
		t.Errorf("got %q, want only the first block", got)
	}
	if got := fitFramed([]string{a, b}, "\n\n", len(a)+2+over+10); !strings.HasSuffix(got, "second sec"+recallFrameFooter) {
		t.Errorf("the second block was not cut inside its frame: %q", got)
	}
	if got := fitFramed([]string{"plain text here"}, "", 5); got != "plain" {
		t.Errorf("an unframed part was not cut plainly: %q", got)
	}
}

// Codex 0.163 warns that `mcp_servers.deja.type` is a setting it does not
// know, and picks stdio from `command` anyway (#5590).

const oldCodexBlockWithType = "model = \"gpt-5\"\n\n[mcp_servers.deja]\ntype = \"stdio\"\ncommand = \"/old/bin/deja\"\nargs = [\"mcp\"]\nstartup_timeout_sec = 30\n"

func codexConfigForTest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(sources.CodexHome(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func codexDejaBlock(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, blk := range tomlMCPBlocks(string(b)) {
		if blk.key == "deja" {
			lines := strings.Split(string(b), "\n")
			return strings.Join(lines[blk.start:blk.end], "\n")
		}
	}
	t.Fatalf("no [mcp_servers.deja] in:\n%s", b)
	return ""
}

func blockHasKey(block, key string) bool {
	for line := range strings.SplitSeq(block, "\n") {
		if k, _, ok := tomlLineKeyValue(line); ok && k == key {
			return true
		}
	}
	return false
}

func fakeDejaExe(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestInstallCodexWritesNoType(t *testing.T) {
	for name, install := range map[string]func(string, bool) (installResult, error){
		"codex":      installCodex,
		"codex-auto": installCodexAuto,
	} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			path := codexConfigForTest(t, "")
			if _, err := install(fakeDejaExe(t), false); err != nil {
				t.Fatal(err)
			}
			block := codexDejaBlock(t, path)
			if blockHasKey(block, "type") {
				t.Errorf("a fresh install wrote `type` for codex:\n%s", block)
			}
			if !blockHasKey(block, "command") || !blockHasKey(block, "args") {
				t.Errorf("the block lost command or args:\n%s", block)
			}
		})
	}
}

func TestReinstallCodexDropsOldType(t *testing.T) {
	hermeticEnv(t)
	path := codexConfigForTest(t, oldCodexBlockWithType)
	exe := fakeDejaExe(t)
	if _, err := installCodex(exe, false); err != nil {
		t.Fatal(err)
	}
	block := codexDejaBlock(t, path)
	if blockHasKey(block, "type") {
		t.Errorf("a reinstall kept the old `type` line:\n%s", block)
	}
	if !strings.Contains(block, "startup_timeout_sec = 30") {
		t.Errorf("a reinstall dropped the reader's own key:\n%s", block)
	}
	if want, _ := mcpLaunchCommandArgs(exe, false); dejaCommandIn(path) != want {
		t.Errorf("the block runs %q, want %q", dejaCommandIn(path), want)
	}
	// And a second run has nothing left to change.
	res, err := installCodex(exe, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "unchanged" {
		t.Errorf("a repeat install reported %q, want unchanged", res.Action)
	}
}

func TestUninstallCodexRemovesOldBlockWithType(t *testing.T) {
	hermeticEnv(t)
	path := codexConfigForTest(t, oldCodexBlockWithType)
	if _, err := installCodex(fakeDejaExe(t), true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "mcp_servers.deja") || strings.Contains(string(b), "type =") {
		t.Errorf("uninstall left the old block behind:\n%s", b)
	}
	if !strings.Contains(string(b), `model = "gpt-5"`) {
		t.Errorf("uninstall took the reader's own settings:\n%s", b)
	}
}

// The block without `type` is what doctor reads from now on, so it has to be
// wired and name its command, the same as the old one did.
func TestDoctorReadsCodexBlockWithoutType(t *testing.T) {
	hermeticEnv(t)
	path := codexConfigForTest(t, "")
	exe := fakeDejaExe(t)
	if _, err := installCodex(exe, false); err != nil {
		t.Fatal(err)
	}
	if blockHasKey(codexDejaBlock(t, path), "type") {
		t.Fatalf("the install still writes `type`, so this measures nothing")
	}
	if !doctorTOMLWired(path) {
		t.Error("doctor does not count the block as wired")
	}
	if want, _ := mcpLaunchCommandArgs(exe, false); dejaCommandIn(path) != want {
		t.Errorf("dejaCommandIn = %q, want %q", dejaCommandIn(path), want)
	}
	if got := dejaCommandMissing(path); got != "" {
		t.Errorf("dejaCommandMissing = %q for a binary that exists", got)
	}
	var buf bytes.Buffer
	doctorMCP(&buf)
	var row string
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "codex" {
			row = line
		}
	}
	if f := strings.Fields(row); len(f) < 2 || f[1] != "wired" {
		t.Errorf("codex row = %q, want wired\n%s", row, buf.String())
	}
}

// All four of Command Code's surfaces are shapes deja already writes, and the
// two that would fail quietly are the ones this pins: the hook timeout is in
// seconds there, not milliseconds, and its tool matcher uses its own display
// names, so a Claude-shaped `Bash` would never fire (#3651).
func TestInstallCommandCodeWritesAllFourSurfaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := installCommandCodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, ".commandcode")
	assertMCPServerEntry(t, filepath.Join(root, "mcp.json"))

	if b, err := os.ReadFile(filepath.Join(root, "skills", "deja-history", "SKILL.md")); err != nil {
		t.Fatalf("skill: %v", err)
	} else if !strings.Contains(string(b), "deja") {
		t.Errorf("the skill does not mention deja")
	}
	if b, err := os.ReadFile(filepath.Join(root, "commands", "deja.md")); err != nil {
		t.Fatalf("command: %v", err)
	} else if !strings.Contains(string(b), "deja") {
		t.Errorf("the command does not mention deja")
	}

	b, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("settings are not the shape Command Code reads: %v", err)
	}
	for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse"} {
		entries := cfg.Hooks[event]
		if len(entries) == 0 || len(entries[0].Hooks) == 0 {
			t.Fatalf("%s has no hook", event)
		}
		h := entries[0].Hooks[0]
		if !strings.Contains(h.Command, "deja") {
			t.Errorf("%s runs %q", event, h.Command)
		}
		// Seconds, not milliseconds: 60000 here would be ten minutes, well
		// past the documented 600-second maximum.
		if h.Timeout > 600 {
			t.Errorf("%s timeout = %d, and Command Code reads that as seconds", event, h.Timeout)
		}
	}
	// Its tool names are SHELL/EDIT/WRITE/READ, so a Bash matcher fires never.
	if m := cfg.Hooks["PreToolUse"][0].Matcher; strings.Contains(m, "Bash") || !strings.Contains(m, "SHELL") {
		t.Errorf("PreToolUse matcher = %q, want Command Code's own tool names", m)
	}

	if _, err := installCommandCodeAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "settings.json")); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("a hook survived uninstall: %s", b)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "deja-history", "SKILL.md")); err == nil {
		t.Error("the skill survived uninstall")
	}
}

func TestInstallClaudeCommandsWritesDiscoverableCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installClaudeCommands("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Claude Code only picks up ~/.claude/commands/*.md.
	path := filepath.Join(home, ".claude", "commands", "deja.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("command not at the discovered path: %v", err)
	}
	src := string(b)
	// Frontmatter is what names the command in the "/" menu.
	if !strings.HasPrefix(src, "---\nname: deja\n") {
		t.Fatalf("missing frontmatter:\n%s", src)
	}
	if !strings.Contains(src, "$ARGUMENTS") {
		t.Fatalf("command ignores the user's arguments:\n%s", src)
	}
	if !strings.Contains(src, "/bin/deja") {
		t.Fatalf("CLI fallback does not name the binary:\n%s", src)
	}
	if _, err := installClaudeCommands("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("command survived uninstall: %v", err)
	}
	if r, err := installClaudeCommands("/bin/deja", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("second uninstall = %+v, %v", r, err)
	}
}

func TestClaudeAutoInstallsCommandAlongsideHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installClaudeAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "commands", "deja.md"),
		filepath.Join(home, ".claude", "settings.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("claude-auto did not write %s: %v", p, err)
		}
	}
}

// A refused target writes nothing, also when its config is not there yet and
// the directory it goes in cannot be made: cursor-auto wired mcp.json and
// hooks.json before finding out it could not create ~/.config/cursor (#5291).
// A regular file where the directory should be fails on every platform and
// for root, where a 0555 parent would not.
func TestCursorAutoRefusesAStatusLineDirectoryItCannotMakeBeforeWriting(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	cfg := cursorCLIConfigPath()
	blocker := filepath.Dir(cfg)
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// On unix the read of cli-config.json already fails with ENOTDIR and
	// refuses the target; Windows reports the path as not found, and the
	// pre-flight's mkdir check is what refuses it there. Either way nothing
	// may have been written.
	_, err := captureRun(t, "install", "cursor-auto", "--no-index")
	if err == nil || !strings.Contains(err.Error(), "cursor-auto") ||
		(!strings.Contains(err.Error(), "mkdir "+blocker) && !strings.Contains(err.Error(), "not a directory")) {
		t.Fatalf("install err = %v, want cursor-auto refused over %s", err, blocker)
	}
	for _, name := range []string{"mcp.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(sources.CursorCLIHome(), name)); err == nil {
			t.Errorf("%s was written by a refused target", name)
		}
	}

	// With the directory makeable, the same install goes through.
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "cursor-auto", "--no-index"); err != nil {
		t.Fatalf("install after removing the blocker: %v\n%s", err, out)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Errorf("status line file not written: %v", err)
	}
}

// The issue's own reproduction: ~/.config is there and read-only, so
// ~/.config/cursor cannot be made. Needs the mode bit to mean something.
func TestCursorAutoRefusesAReadOnlyConfigParentBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	hermeticEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	parent := filepath.Dir(filepath.Dir(cursorCLIConfigPath()))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	_, err := captureRun(t, "install", "cursor-auto", "--no-index")
	if err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("install err = %v, want a permission refusal", err)
	}
	for _, name := range []string{"mcp.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(sources.CursorCLIHome(), name)); err == nil {
			t.Errorf("%s was written by a refused target", name)
		}
	}
}

func TestConfigWritableAsksWhetherAMissingConfigCanBeMade(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "have.json")
	if err := os.WriteFile(existing, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"existing file", existing, ""},
		{"missing file in an existing directory", filepath.Join(root, "new.json"), ""},
		{"missing directories under a writable one", filepath.Join(root, "a", "b", "new.json"), ""},
		{"a file where the directory should be", filepath.Join(blocker, "new.json"), "mkdir " + blocker},
		{"a file further up", filepath.Join(blocker, "sub", "new.json"), "mkdir " + blocker},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := configWritable(tc.path)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("configWritable(%s) = %v, want nil", tc.path, err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("configWritable(%s) = %v, want %q", tc.path, err, tc.wantErr)
			}
		})
	}
	// The probe leaves nothing behind: no scratch file, no directory made.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n := e.Name(); n != "have.json" && n != "blocker" {
			t.Errorf("configWritable left %s behind", n)
		}
	}
}

func TestConfigWritableNamesTheDirectoryItCouldNotMake(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	err := configWritable(filepath.Join(locked, "cursor", "cli-config.json"))
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), "mkdir "+filepath.Join(locked, "cursor")) {
		t.Errorf("missing directory: err = %v, want mkdir %s: permission denied", err, filepath.Join(locked, "cursor"))
	}
	path := filepath.Join(locked, "cli-config.json")
	err = configWritable(path)
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), path) {
		t.Errorf("missing file: err = %v, want a permission error naming %s", err, path)
	}
}

// When CLAUDE_CONFIG_DIR points Claude Code at a non-default profile, deja must
// wire the MCP server, SessionStart hook and statusline into that profile
// rather than the default ~/.claude.json and ~/.claude/settings.json.
func TestInstallClaudeHonorsConfigDir(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "custom-claude")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	if _, err := installClaude("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHook("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installStatusline("/bin/deja", false); err != nil {
		t.Fatal(err)
	}

	mcp := filepath.Join(cfg, ".claude.json")
	settings := filepath.Join(cfg, "settings.json")
	for _, p := range []string{mcp, settings} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to be written: %v", p, err)
		}
	}
	if b, err := os.ReadFile(mcp); err != nil || !strings.Contains(string(b), `"deja"`) {
		t.Fatalf("mcp server not in %s: %s (%v)", mcp, b, err)
	}
	if b, err := os.ReadFile(settings); err != nil || !strings.Contains(string(b), hookExeInConfigs("/bin/deja")+" hook-context") || !strings.Contains(string(b), "deja statusline") {
		t.Fatalf("hook/statusline not in %s: %s (%v)", settings, b, err)
	}

	// The default ~/.claude location must be left untouched.
	for _, p := range []string{filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude", "settings.json")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("default location %s should not have been written", p)
		}
	}
}

func TestClaudeHookUninstallNeverLeavesNullHooks(t *testing.T) {
	root := map[string]any{}
	root = updateClaudeHook(root, "PreCompact", "/bin/deja hook-precompact", "manual|auto", false)
	root = updateClaudeHook(root, "PreCompact", "/bin/deja hook-precompact", "manual|auto", true)
	root = updateClaudeHook(root, "PreCompact", "/bin/deja hook-precompact", "manual|auto", false)
	entries, _ := root["hooks"].(map[string]any)["PreCompact"].([]any)
	if len(entries) != 1 {
		t.Fatalf("install/uninstall/install left %d entries, want 1: %#v", len(entries), entries)
	}
	for _, e := range entries {
		entry := typedOrFatal[map[string]any](t, e)
		hs, ok := entry["hooks"].([]any)
		if !ok || len(hs) == 0 {
			t.Fatalf("entry with null/empty hooks survived: %#v", entry)
		}
	}
	// Pre-existing damage from older versions heals on the next install.
	damaged := map[string]any{"hooks": map[string]any{"PreCompact": []any{
		map[string]any{"matcher": "manual|auto", "hooks": nil},
		map[string]any{"matcher": "manual|auto", "hooks": []any{map[string]any{"type": "command", "command": "/bin/deja hook-precompact"}}},
	}}}
	healed := updateClaudeHook(damaged, "PreCompact", "/bin/deja hook-precompact", "manual|auto", false)
	entries2, _ := healed["hooks"].(map[string]any)["PreCompact"].([]any)
	if len(entries2) != 1 {
		t.Fatalf("null-hooks entry not healed: %#v", entries2)
	}
}

// Installing from a new path used to leave the old entry behind, so both
// fired: the digest was injected twice and hook-prompt ran twice per prompt.
func TestClaudeHookInstallTakesOverAnOlderPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installClaudeHook("/opt/old/deja", false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := installClaudeHook("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("second install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command       string `json:"command"`
				StatusMessage string `json:"statusMessage"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	for event, groups := range root.Hooks {
		var ours []string
		for _, g := range groups {
			for _, h := range g.Hooks {
				if !strings.Contains(h.Command, "deja") {
					continue
				}
				ours = append(ours, h.Command)
				// An entry we own carries the status message whether it was
				// created now or adopted from an older install.
				if hookStatusMessage(event) != "" && h.StatusMessage == "" {
					t.Errorf("%s: adopted entry has no statusMessage", event)
				}
			}
		}
		if len(ours) != 1 {
			t.Errorf("%s: %d deja hooks, want 1: %v", event, len(ours), ours)
		}
		if len(ours) == 1 && !strings.HasPrefix(ours[0], hookExeInConfigs("/usr/local/bin/deja")) {
			t.Errorf("%s: kept the stale path: %s", event, ours[0])
		}
	}
}

// continueInstalledConfig runs the installer against an empty home and returns
// the config it wrote — the artifact, not a description of it.
func continueInstalledConfig(t *testing.T, exe string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CONTINUE_ROOT", filepath.Join(home, ".continue"))
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	if _, err := installContinue(exe, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".continue", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// continueCommandLine is the `command:` line install writes on this platform.
// Windows spawns a stdio MCP client through cmd, so the executable is an
// argument there rather than the command — three of these tests asserted the
// POSIX shape and were red on the Windows leg and nowhere else.
func continueCommandLine(exe string) string {
	cmd, _ := mcpCommandArgs(exe)
	return `command: "` + cmd + `"`
}

// continueNamesBinary counts the entries that carry this executable, wherever
// the platform puts it: in `command:` or in the args list under it.
func continueNamesBinary(config, exe string) int {
	return strings.Count(config, `"`+exe+`"`)
}

// The Windows leg is the only place the wrapped shape appears, so pin the
// counter against both spellings here rather than only where it runs.
func TestContinueNamesBinaryCountsEitherShape(t *testing.T) {
	posix := "mcpServers:\n  - name: deja\n    command: \"/bin/deja\"\n    args:\n      - \"mcp\"\n"
	windows := "mcpServers:\n  - name: deja\n    command: \"cmd\"\n    args:\n      - \"/c\"\n      - \"/bin/deja\"\n      - \"mcp\"\n"
	for name, config := range map[string]string{"posix": posix, "windows": windows} {
		if got := continueNamesBinary(config, "/bin/deja"); got != 1 {
			t.Errorf("%s: the binary is named %d times, want once:\n%s", name, got, config)
		}
		if got := continueNamesBinary(config+config, "/bin/deja"); got != 2 {
			t.Errorf("%s: a doubled entry counted %d, so doubling would go unnoticed", name, got)
		}
	}
}

// Continue keeps the MCP server and the slash command in the same assistant
// config, both as sequences of mappings rather than the keyed objects every
// other harness uses. Measured on @continuedev/cli 1.5.47: with this entry the
// `deja` tool is in the tool list of every request (#3062).
func TestInstallContinueWritesTheServerAndTheCommand(t *testing.T) {
	got := continueInstalledConfig(t, "/usr/local/bin/deja")
	for _, want := range []string{
		"mcpServers:",
		"  - name: deja",
		continueCommandLine("/usr/local/bin/deja"),
		"      - \"mcp\"",
		"prompts:",
		"description: Search this machine's past coding sessions",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the config does not carry %q:\n%s", want, got)
		}
	}
	// Two items, one under each key — not one key with both.
	if n := strings.Count(got, "- name: deja"); n != 2 {
		t.Fatalf("deja is named %d times, want once under each key:\n%s", n, got)
	}
}

// The config is the reader's own file: whatever else is in it survives, and a
// second install neither doubles the entry nor moves anything.
func TestInstallContinueKeepsTheRestOfTheConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".continue")
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	before := `name: mine
version: 0.0.1
models:
  - name: sonnet
    provider: anthropic
    model: claude-sonnet-4
mcpServers:
  - name: sqlite
    command: uvx
    args:
      - mcp-server-sqlite
rules:
  - be brief
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installContinue("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got := readFileString(t, path)
	for _, want := range []string{"name: mine", "- name: sonnet", "- name: sqlite", "- mcp-server-sqlite", "rules:", "  - be brief"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the reader's own config lost %q:\n%s", want, got)
		}
	}
	// deja's server joins the list rather than replacing it.
	if strings.Index(got, "- name: sqlite") > strings.Index(got, "- name: deja") {
		t.Fatalf("deja was inserted ahead of the reader's own server:\n%s", got)
	}

	if _, err := installContinue("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	twice := readFileString(t, path)
	if continueNamesBinary(twice, "/bin/deja") != 1 {
		t.Fatalf("a second install doubled the entry:\n%s", twice)
	}

	// And the way out leaves the file as it was found.
	if _, err := installContinue("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	after := readFileString(t, path)
	if strings.Contains(after, "deja") {
		t.Fatalf("uninstall left deja behind:\n%s", after)
	}
	if !strings.Contains(after, "- name: sqlite") || !strings.Contains(after, "  - be brief") {
		t.Fatalf("uninstall took something that was not deja's:\n%s", after)
	}
}

// A binary path moves; the entry has to move with it rather than doubling.
func TestInstallContinueAdoptsAMovedBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CONTINUE_ROOT", filepath.Join(home, ".continue"))
	if _, err := installContinue("/old/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installContinue("/new/deja", false); err != nil {
		t.Fatal(err)
	}
	got := readFileString(t, filepath.Join(home, ".continue", "config.yaml"))
	if strings.Contains(got, "/old/deja") {
		t.Fatalf("the old path is still there:\n%s", got)
	}
	if continueNamesBinary(got, "/new/deja") != 1 {
		t.Fatalf("the moved binary is not named once:\n%s", got)
	}
}

// An inline list is not a block, and appending under it would leave the key
// twice — of which a parser takes one, silently dropping the reader's servers.
// The goose writer refuses the same shape for the same reason.
func TestInstallContinueRefusesAnInlineList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".continue")
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("name: mine\nmcpServers: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installContinue("/bin/deja", false); err == nil {
		t.Fatal("an inline list was edited anyway")
	}
	if got := readFileString(t, path); !strings.Contains(got, "mcpServers: []") {
		t.Fatalf("the refused file was changed:\n%s", got)
	}
}

// Continue reads skills from its own global folder and names each one in the
// Skills tool's description — that is the surface that makes recall arrive
// without being asked for, so the file has to land there and nowhere else.
func TestContinueSkillLandsWhereContinueReadsIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".continue")
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	want := filepath.Join(root, "skills", "deja-history", "SKILL.md")
	if got := guidancePath("continue"); got != want {
		t.Fatalf("guidance path = %q, want %q", got, want)
	}
	if _, err := installGuidance("continue", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("the skill was not written where Continue reads: %v", err)
	}
	for _, line := range []string{"name: deja-history", "description:"} {
		if !strings.Contains(string(b), line) {
			t.Fatalf("the skill has no %q, so Continue lists nothing:\n%s", line, b)
		}
	}
}

// deja's hook file under ~/.copilot/hooks, which Copilot CLI and VS Code
// Copilot Chat both read.
type copilotHookFile struct {
	Hooks map[string][]struct {
		Type       string `json:"type"`
		Bash       string `json:"bash"`
		TimeoutSec int    `json:"timeoutSec"`
		Matcher    string `json:"matcher"`
	} `json:"hooks"`
}

func copilotTestHome(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	t.Setenv("COPILOT_HOME", "")
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func readCopilotHookFile(t *testing.T) (copilotHookFile, string) {
	t.Helper()
	got := readFile(t, copilotHooksPath())
	var cfg copilotHookFile
	if err := json.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatalf("the hook file is not JSON: %v\n%s", err, got)
	}
	return cfg, got
}

// Every surface deja has, on the events a stand against Copilot CLI 1.0.92
// showed reach the model, and PreCompact spelled the way VS Code reads it.
func TestInstallCopilotAutoWiresEverySurfaceInItsOwnFile(t *testing.T) {
	home := copilotTestHome(t)
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".copilot", "hooks", "deja.json"); copilotHooksPath() != want {
		t.Fatalf("hook file at %s, want %s", copilotHooksPath(), want)
	}
	cfg, first := readCopilotHookFile(t)
	for event, want := range map[string]struct{ cmd, matcher string }{
		"sessionStart":        {" hook-context --copilot", ""},
		"userPromptSubmitted": {" hook-prompt --copilot", ""},
		"preToolUse":          {" hook-tool --copilot", "bash|powershell|edit|create"},
		"postToolUse":         {" hook-tool-after --copilot", "bash|powershell"},
		"postToolUseFailure":  {" hook-tool-after --copilot", "bash|powershell"},
		"PreCompact":          {" hook-precompact", ""},
		"preMcpToolCall":      {" hook-mcp-call", ""},
		"sessionEnd":          {" hook-session-end", ""},
	} {
		entries := cfg.Hooks[event]
		if len(entries) != 1 {
			t.Errorf("%s: %d entries, want 1:\n%s", event, len(entries), first)
			continue
		}
		e := entries[0]
		if e.Type != "command" || !strings.HasSuffix(e.Bash, want.cmd) || e.Matcher != want.matcher {
			t.Errorf("%s = %+v, want %q matcher %q", event, e, want.cmd, want.matcher)
		}
		// Copilot waits on a hook before the request it belongs to, so a slow
		// deja must not hold the prompt for the default minute.
		if e.TimeoutSec <= 0 || e.TimeoutSec > 10 {
			t.Errorf("%s timeoutSec = %d, want a few seconds", event, e.TimeoutSec)
		}
	}
	if len(cfg.Hooks) != 8 {
		t.Errorf("hook file carries %d events, want 8:\n%s", len(cfg.Hooks), first)
	}
	if _, err := os.Stat(filepath.Join(home, ".copilot", "config.json")); err == nil {
		t.Errorf("install wrote Copilot's own config.json")
	}
	// settings.json gets the status line and nothing else.
	var settings map[string]any
	if b, err := os.ReadFile(filepath.Join(home, ".copilot", "settings.json")); err != nil || json.Unmarshal(b, &settings) != nil {
		t.Fatalf("no status line in settings.json: %v", err)
	}
	line, _ := settings["statusLine"].(map[string]any)
	if cmd, _ := line["command"].(string); len(settings) != 1 || !strings.HasSuffix(cmd, " statusline") {
		t.Errorf("settings.json = %v, want deja's statusLine alone", settings)
	}

	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, copilotHooksPath()); again != first {
		t.Errorf("a second install changed the file:\nfirst:\n%s\nsecond:\n%s", first, again)
	}
	if _, err := captureRun(t, "uninstall", "copilot-auto"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(copilotHooksPath()); err == nil {
		t.Errorf("uninstall left the hook file deja created:\n%s", b)
	}
}

// An install from before the hook file left deja's entries in settings.json,
// or in a config.json Copilot had not moved yet. The new install takes them
// out, so an upgrade does not run deja twice, and leaves the reader's own.
func TestInstallCopilotAutoClearsTheEntriesOlderInstallsWrote(t *testing.T) {
	home := copilotTestHome(t)
	settings := filepath.Join(home, ".copilot", "settings.json")
	config := filepath.Join(home, ".copilot", "config.json")
	deja := hookExeInConfigs("/usr/local/bin/deja")
	legacy := func(extra string) string {
		return `{"model":"gpt-5.4","hooks":{"sessionStart":[{"type":"command","bash":"/usr/bin/theirs start","timeoutSec":5},{"type":"command","bash":"` + deja + ` hook-context --copilot","timeoutSec":10}],"postToolUse":[{"type":"command","bash":"` + deja + ` hook-tool-after --copilot","timeoutSec":10}]` + extra + `}}` + "\n"
	}
	if err := os.WriteFile(settings, []byte(legacy("")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(copilotManagedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, settings)
	if strings.Contains(got, "hook-context") || strings.Contains(got, "hook-tool-after") {
		t.Errorf("deja's old entries are still in settings.json:\n%s", got)
	}
	if !strings.Contains(got, "/usr/bin/theirs start") || !strings.Contains(got, `"model"`) {
		t.Errorf("install took what was not deja's:\n%s", got)
	}
	if got := readFile(t, config); got != copilotManagedConfig {
		t.Errorf("install rewrote Copilot's own config.json:\n%s", got)
	}
	if cfg, _ := readCopilotHookFile(t); len(cfg.Hooks["sessionStart"]) != 1 {
		t.Errorf("the hook file does not carry the digest")
	}
}

// A file deja cannot read is a refusal, and a refusal writes nothing: the
// MCP entry and the hook file used to be written first, leaving half a target.
func TestCopilotAutoRefusesBeforeWritingAnything(t *testing.T) {
	home := copilotTestHome(t)
	config := filepath.Join(home, ".copilot", "config.json")
	before := "{\n  \"hooks\": {\"sessionStart\": [{\"type\": \"command\", \"bash\": \"" + hookExeInConfigs("/usr/local/bin/deja") + " hook-context --copilot\"}]}\n"
	if err := os.WriteFile(config, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err == nil {
		t.Fatal("install went ahead past a config.json it cannot read")
	}
	for _, name := range []string{"mcp-config.json", "settings.json", filepath.Join("hooks", "deja.json"), filepath.Join("skills", "deja-history", "SKILL.md")} {
		if _, err := os.Stat(filepath.Join(home, ".copilot", name)); err == nil {
			t.Errorf("a refused install still wrote %s", name)
		}
	}
	if got := readFile(t, config); got != before {
		t.Errorf("a refused install changed config.json:\n%s", got)
	}
}

// The file is shared: vscode-auto writes the same one, and taking either
// target out leaves it while the other is installed.
func TestCopilotAndVSCodeAutoShareTheHookFile(t *testing.T) {
	copilotTestHome(t)
	if _, err := captureRun(t, "install", "vscode-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	_, first := readCopilotHookFile(t)
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, copilotHooksPath()); again != first {
		t.Errorf("copilot-auto rewrote the file vscode-auto wrote:\nfirst:\n%s\nsecond:\n%s", first, again)
	}
	if _, err := captureRun(t, "uninstall", "copilot-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(copilotHooksPath()); err != nil {
		t.Fatalf("uninstalling copilot-auto took the file vscode-auto still needs: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "vscode-auto"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(copilotHooksPath()); err == nil {
		t.Errorf("the last target out left the hook file:\n%s", b)
	}
}

// Copilot reads `additionalContext` at the top level and nothing nested: the
// Claude envelope runs, succeeds, and reaches no one. The payload names the
// session as `sessionId`, and that session stays out of its own digest.
func TestHookContextCopilotAnswersInCopilotsShape(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	now := time.Now()
	seedClaudeAt(t, claude, "app", "older-session", "the ledger export drops the last row", "the writer missed a final flush", now.Add(-2*time.Hour))
	seedClaudeAt(t, claude, "app", "live-session", "the glimmerquest cache misses on every cold start", "warming it at boot from the last snapshot fixed it", now.Add(-time.Minute))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	was := copilotHookOutput
	copilotHookOutput = true
	defer func() { copilotHookOutput = was }()

	withHookStdin(t, `{"sessionId":"live-session","timestamp":1790866280348,"cwd":"/tmp/app","source":"new","initialPrompt":"fix the export"}`)
	out := captureStdout(t, func() {
		if err := runHookContext(dir, false); err != nil {
			t.Error(err)
		}
	})
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace([]byte(out)), &resp); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out)
	}
	if len(resp) != 1 {
		t.Errorf("want only additionalContext, got keys %v", resp)
	}
	ctx, _ := resp["additionalContext"].(string)
	if !strings.Contains(ctx, "ledger export") {
		t.Fatalf("the digest for the payload's project is missing:\n%s", out)
	}
	if strings.Contains(ctx, "glimmerquest") {
		t.Errorf("the session asking was served its own prompt:\n%s", out)
	}
}

func TestDoctorReportsCopilotAutoRecall(t *testing.T) {
	home := copilotTestHome(t)
	row := func() string {
		var out bytes.Buffer
		doctorAutoRecall(&out)
		// The row and the lines printed under it, which are indented past the
		// name column.
		var lines []string
		on := false
		for line := range strings.SplitSeq(out.String(), "\n") {
			switch {
			case strings.HasPrefix(line, "  copilot "):
				on = true
			case !strings.HasPrefix(line, strings.Repeat(" ", 15)):
				on = false
			}
			if on {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	if got := row(); !strings.Contains(got, "missing") {
		t.Fatalf("no row, or not missing before install:\n%s", got)
	}
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if got := row(); !strings.Contains(got, "wired") {
		t.Errorf("not wired after install:\n%s", got)
	}

	// The switch that turns every hook off leaves the entry looking installed.
	settings := filepath.Join(home, ".copilot", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"disableAllHooks":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := row(); !strings.Contains(got, "switched off") || !strings.Contains(got, "disableAllHooks") {
		t.Errorf("disableAllHooks is on and the row does not say so:\n%s", got)
	}
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}

	// An entry naming a binary that is gone is a hook that exits 127. Absolute
	// the way this platform spells it: "/gone/deja" is relative on Windows.
	gone := filepath.Join(t.TempDir(), "gone", "deja")
	dead := `{"hooks":{"userPromptSubmitted":[{"type":"command","bash":"` + jsonEscaped(t, gone) + ` hook-prompt --copilot","timeoutSec":10}]}}`
	if err := os.WriteFile(copilotHooksPath(), []byte(dead), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := row(); !strings.Contains(got, gone) || !strings.Contains(got, "copilot-auto") {
		t.Errorf("a dead binary in the hook is not reported:\n%s", got)
	}
}

// What Copilot CLI 1.0.79 leaves in config.json once it has moved the user's
// settings into settings.json: two comment lines of its own, then JSON.
const copilotManagedConfig = "// User settings belong in settings.json.\n// This file is managed automatically.\n{\n  \"firstLaunchAt\": \"2026-03-11T00:00:00.000Z\"\n}\n"

// Uninstalling the base target takes the hook with it, the way claude-code
// takes claude-auto's: a hook calling a server that is gone is half a target.
func TestUninstallCopilotTakesTheAutoHookToo(t *testing.T) {
	copilotTestHome(t)
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "copilot"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(copilotHooksPath()); err == nil && strings.Contains(string(b), "hook-context") {
		t.Errorf("`deja uninstall copilot` left the hook:\n%s", b)
	}
}

// A hook-prompt line without --copilot answers in Claude's envelope, which
// Copilot CLI runs and drops: installed-looking, delivering nothing.
func TestDoctorCallsAPlainHookPromptEntryStale(t *testing.T) {
	copilotTestHome(t)
	if err := os.MkdirAll(filepath.Dir(copilotHooksPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copilotHooksPath(), []byte(`{"hooks":{"userPromptSubmitted":[{"type":"command","bash":"deja hook-prompt","timeoutSec":10}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, a := range autoWirings() {
		if a.name != "copilot" {
			continue
		}
		if state, _ := autoWiringState(a); state != "stale" {
			t.Errorf("state = %q for a hook Copilot drops the answer of, want stale", state)
		}
		return
	}
	t.Fatal("no copilot row")
}

// COPILOT_HOME moves every file Copilot CLI keeps, and a relative one is used
// as written, as Copilot's own resolveCopilotHome uses it (#4567).
func TestCopilotHomeMovesEveryCopilotPath(t *testing.T) {
	copilotTestHome(t)
	t.Setenv("DEJA_COPILOT_ROOT", "")
	abs := filepath.Join(t.TempDir(), "ch")
	t.Setenv("COPILOT_HOME", abs)
	for name, got := range map[string]string{
		"session root": sources.CopilotRoot(),
		"skill":        guidancePath("copilot"),
		"hooks":        copilotHooksPath(),
		"mcp":          copilotMCPConfigPath(),
	} {
		if !strings.HasPrefix(got, abs+string(filepath.Separator)) {
			t.Errorf("%s = %s, want under COPILOT_HOME %s", name, got, abs)
		}
	}
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("COPILOT_HOME", "relch")
	want := filepath.Join("relch", "session-state")
	if got := sources.CopilotRoot(); got != want {
		t.Errorf("relative COPILOT_HOME: session root = %s, want %s", got, want)
	}
	// The deja override still wins for the session root.
	t.Setenv("DEJA_COPILOT_ROOT", filepath.Join(cwd, "mine"))
	if got := sources.CopilotRoot(); got != filepath.Join(cwd, "mine") {
		t.Errorf("DEJA_COPILOT_ROOT lost to COPILOT_HOME: %s", got)
	}
}

// PowerShell runs a quoted path only behind `&`, and inside single quotes the
// only character that means anything is the quote itself, doubled.
func TestCopilotPowerShellLineQuotesThePath(t *testing.T) {
	got := copilotPowerShellCommand(`C:\Users\O'Brien\$x (1)\deja.exe`, "hook-context", "--copilot")
	want := `& 'C:/Users/O''Brien/$x (1)/deja.exe' hook-context --copilot`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Copilot writes settings.json itself, so the file being there says nothing
// about deja: a machine that never ran copilot-auto reads missing.
func TestDoctorCallsCopilotsOwnSettingsMissing(t *testing.T) {
	home := copilotTestHome(t)
	if err := os.WriteFile(filepath.Join(home, ".copilot", "settings.json"), []byte(`{"model":"gpt-5.4"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, a := range autoWirings() {
		if a.name != "copilot" {
			continue
		}
		if state, _ := autoWiringState(a); state != "missing" {
			t.Errorf("state = %q for Copilot's own settings with no deja hook, want missing", state)
		}
		return
	}
	t.Fatal("no copilot row")
}

// Copilot Chat fires no hook, so the prompt file is the only thing that tells
// its model to reach for the tool. It goes in the same `User` directory the
// reader already walks, once per host that exists (#3651).
func TestInstallCopilotChatPromptWritesPerHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", "")

	// Two hosts present, one absent: the absent one must not be created.
	hosts := make([]string, 0, 2)
	for _, name := range []string{"Code", "VSCodium"} {
		dir := vsCodeUserDirForTest(t, home, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		hosts = append(hosts, dir)
	}
	absent := vsCodeUserDirForTest(t, home, "Code - Insiders")

	res, err := installCopilotChatPrompt("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatalf("install changed nothing: %+v", res)
	}
	for _, dir := range hosts {
		path := filepath.Join(dir, "prompts", "deja.prompt.md")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body := string(b)
		// The frontmatter is what makes it a prompt file rather than a note,
		// and the description is what the chat box lists beside `/deja`.
		if !strings.HasPrefix(body, "---\n") || !strings.Contains(body, "description:") {
			t.Errorf("%s has no prompt frontmatter:\n%s", path, body[:120])
		}
		if !strings.Contains(body, "$ARGUMENTS") {
			t.Errorf("%s takes no arguments, so /deja <query> would drop the query", path)
		}
		if !strings.Contains(body, "/usr/local/bin/deja --framed search") {
			t.Errorf("%s names no fallback command", path)
		}
	}
	if _, err := os.Stat(absent); err == nil {
		t.Errorf("the installer created a directory for an editor that is not installed: %s", absent)
	}

	if again, err := installCopilotChatPrompt("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	} else if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}

	if _, err := installCopilotChatPrompt("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	for _, dir := range hosts {
		if _, err := os.Stat(filepath.Join(dir, "prompts", "deja.prompt.md")); err == nil {
			t.Errorf("the prompt file survived uninstall in %s", dir)
		}
	}
}

// vsCodeUserDirForTest is where this platform keeps a host's User directory —
// the same three branches CopilotChatRoots walks.
func vsCodeUserDirForTest(t *testing.T, home, host string) string {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", host, "User")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", host, "User")
	default:
		return filepath.Join(home, ".config", host, "User")
	}
}

// Copilot CLI reads settings.json as JSONC: a `//` line and trailing commas
// load (1.0.98, `copilot config model`). copilot-auto refused such a file
// after it had written the MCP entry and the hook file, so the status line
// and the guidance skill were never written and the run exited 1.
func TestCopilotAutoTakesACommentedSettingsJSON(t *testing.T) {
	home := copilotTestHome(t)
	settings := filepath.Join(home, ".copilot", "settings.json")
	before := "{\n  // my copilot settings\n  \"model\": \"gpt-5\",\n  \"theme\": \"dark\",\n}\n"
	if err := os.WriteFile(settings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatalf("install refused a settings.json Copilot reads: %v\n%s", err, out)
	}
	got := readFile(t, settings)
	if !strings.Contains(got, "// my copilot settings") || !strings.Contains(got, `"theme": "dark",`) {
		t.Errorf("install lost the reader's comment or layout:\n%s", got)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(got)), &root); err != nil {
		t.Fatalf("settings.json no longer reads: %v\n%s", err, got)
	}
	line, _ := root["statusLine"].(map[string]any)
	if cmd, _ := line["command"].(string); !strings.HasSuffix(cmd, " statusline") {
		t.Errorf("no status line was written:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".copilot", "skills", "deja-history", "SKILL.md")); err != nil {
		t.Errorf("the guidance skill was not written: %v", err)
	}
	if out, err := captureRun(t, "uninstall", "copilot-auto"); err != nil {
		t.Fatalf("uninstall refused: %v\n%s", err, out)
	}
	if got := readFile(t, settings); got != before {
		t.Errorf("uninstall did not give settings.json back:\n%s\nwant:\n%s", got, before)
	}
}

// The status line was written while the file was plain JSON, and the reader
// commented it afterwards. Uninstall refused the file and removed deja-hook
// anyway, leaving Copilot a status line that runs a command which is gone.
func TestCopilotUninstallTakesTheStatusLineOutOfACommentedSettingsJSON(t *testing.T) {
	home := copilotTestHome(t)
	settings := filepath.Join(home, ".copilot", "settings.json")
	if err := os.WriteFile(settings, []byte("{\n  \"model\": \"gpt-5\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	commented := strings.Replace(readFile(t, settings), "{\n", "{\n  // pinned model\n", 1)
	if !strings.Contains(commented, "statusLine") {
		t.Fatalf("install wrote no status line to start from:\n%s", commented)
	}
	if err := os.WriteFile(settings, []byte(commented), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "uninstall", "copilot-auto"); err != nil {
		t.Fatalf("uninstall refused: %v\n%s", err, out)
	}
	if got, want := readFile(t, settings), "{\n  // pinned model\n  \"model\": \"gpt-5\"\n}\n"; got != want {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, want)
	}
}

// An older install's hook entries in a commented settings.json are deja's to
// clear, and the install that clears them used to refuse the whole target.
func TestCopilotAutoClearsAnOlderHookFromACommentedSettingsJSON(t *testing.T) {
	home := copilotTestHome(t)
	settings := filepath.Join(home, ".copilot", "settings.json")
	deja := hookExeInConfigs("/usr/local/bin/deja")
	before := "{\n  // my copilot settings\n  \"model\": \"gpt-5\",\n  \"hooks\": {\n" +
		"    \"sessionStart\": [{\"type\": \"command\", \"bash\": \"/usr/bin/theirs start\", \"timeoutSec\": 5}, {\"type\": \"command\", \"bash\": \"" + deja + " hook-context --copilot\", \"timeoutSec\": 10}],\n" +
		"    // deja's, from before the hook file\n" +
		"    \"postToolUse\": [{\"type\": \"command\", \"bash\": \"" + deja + " hook-tool-after --copilot\", \"timeoutSec\": 10}]\n" +
		"  }\n}\n"
	if err := os.WriteFile(settings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "copilot-auto", "--no-index"); err != nil {
		t.Fatalf("install refused: %v\n%s", err, out)
	}
	got := readFile(t, settings)
	if strings.Contains(got, "hook-context") || strings.Contains(got, "hook-tool-after") {
		t.Errorf("deja's old entries are still in settings.json:\n%s", got)
	}
	if !strings.Contains(got, "/usr/bin/theirs start") || !strings.Contains(got, "// my copilot settings") {
		t.Errorf("install took what was not deja's:\n%s", got)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(got)), &root); err != nil {
		t.Fatalf("settings.json no longer reads: %v\n%s", err, got)
	}
	if cfg, _ := readCopilotHookFile(t); len(cfg.Hooks["sessionStart"]) != 1 {
		t.Errorf("the hook file does not carry the digest")
	}
}

// The same writer serves Qwen's ui.statusLine, one key deeper: a ui block the
// status line was the only thing in goes with it, as it does in plain JSON.
func TestAJSONCStatusLineOneKeyDeepRoundTrips(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	before := "{\n  // mine\n  \"theme\": \"dark\",\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"type": "command", "command": "/opt/deja/deja-hook statusline", "refreshInterval": 1}
	if _, err := installJSONStatusline(path, []string{"ui", "statusLine"}, entry, false); err != nil {
		t.Fatalf("install refused: %v", err)
	}
	got := readFile(t, path)
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(got)), &root); err != nil {
		t.Fatalf("the file no longer reads: %v\n%s", err, got)
	}
	if ui, _ := root["ui"].(map[string]any); ui == nil || ui["statusLine"] == nil {
		t.Fatalf("no ui.statusLine:\n%s", got)
	}
	if _, err := installJSONStatusline(path, []string{"ui", "statusLine"}, entry, true); err != nil {
		t.Fatalf("uninstall refused: %v", err)
	}
	if got := readFile(t, path); got != before {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, before)
	}
}

// Shapes a commented settings file comes in, each installed and taken out
// again: the round trip gives the bytes back, and the status line written in
// between is the one key of that name.
func TestAJSONCStatusLineRoundTripsEveryShape(t *testing.T) {
	hermeticEnv(t)
	entry := map[string]any{"type": "command", "command": "/opt/deja/deja-hook statusline", "refreshInterval": 1}
	for _, tc := range []struct {
		name string
		keys []string
		text string
		want string // after uninstall, when it is not text
	}{
		{"comment only above an empty object", []string{"statusLine"}, "// head\n{}\n", ""},
		// The null is replaced, and uninstall takes deja's line out whole.
		{"null under a comment", []string{"statusLine"}, "{\n  // c\n  \"statusLine\": null\n}\n", "{\n  // c\n}\n"},
		// An empty ui goes with the line, as it does in plain JSON, and
		// leaves no comma behind.
		{"nested, ui the last key", []string{"ui", "statusLine"}, "{ // c\n  \"model\": \"x\",\n  \"ui\": {}\n}\n", "{ // c\n  \"model\": \"x\"\n}\n"},
		{"comment between the last two keys", []string{"statusLine"}, "{\n  \"model\": \"x\",\n  // note\n  \"theme\": \"dark\"\n}\n", ""},
		{"block comment before the null", []string{"statusLine"}, "{\n  \"statusLine\": /* x */ null,\n  \"a\": 1\n}\n", "{\n  \"a\": 1\n}\n"},
		// A comment after the key's comma stays on the line it was on,
		// rather than joining the object's opening brace (#5310).
		{"line comment after the null", []string{"statusLine"}, "{\n  \"statusLine\": null, // off until I pick one\n  \"theme\": \"dark\"\n}\n", "{\n  // off until I pick one\n  \"theme\": \"dark\"\n}\n"},
		// And a one-line block holding only a comment comes back on one line.
		{"one-line ui holding a comment", []string{"ui", "statusLine"}, "{\n  \"ui\": { /* later */ }\n}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(tc.text), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installJSONStatusline(path, tc.keys, entry, false); err != nil {
				t.Fatalf("install refused: %v", err)
			}
			got := readFile(t, path)
			if n := strings.Count(got, `"statusLine"`); n != 1 {
				t.Errorf("%d statusLine keys after install:\n%s", n, got)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
				t.Errorf("not JSON once comments are out: %v\n%s", err, got)
			}
			if _, err := installJSONStatusline(path, tc.keys, entry, true); err != nil {
				t.Fatalf("uninstall refused: %v", err)
			}
			want := tc.want
			if want == "" {
				want = tc.text
			}
			if got := readFile(t, path); got != want {
				t.Errorf("uninstall left:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// A comment in front of an event deja takes out stays, and so do the
// reader's characters in the list it shares with deja.
func TestClearingCopilotHooksKeepsTheReadersComment(t *testing.T) {
	deja := "/opt/deja/deja-hook hook-context --copilot"
	text := "{\n  \"hooks\": {\n    \"a\": [{\"bash\": \"a && b\"}, {\"bash\": \"" + deja + "\"}],\n    /* y */ \"sessionStart\": [{\"bash\": \"" + deja + "\"}],\n    \"preToolUse\": /* z */ [{\"bash\": \"" + deja + "\"}]\n  }\n}\n"
	var was map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(text)), &was); err != nil {
		t.Fatal(err)
	}
	root := map[string]any{"hooks": map[string]any{"a": []any{map[string]any{"bash": "a && b"}}}}
	got, err := dropCopilotHooksJSONC(text, typedOrFatal[map[string]any](t, was["hooks"]), root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/* y */") || strings.Contains(got, "preToolUse") || !strings.Contains(got, "a && b") || strings.Contains(got, deja) {
		t.Errorf("got:\n%s", got)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(got)), &back); err != nil {
		t.Errorf("not JSON: %v\n%s", err, got)
	}
}

// The reader's comments inside a list they share with deja stay where they
// were written, and the list is not written again on one line (#5312).
func TestClearingCopilotHooksKeepsCommentsInsideTheSharedList(t *testing.T) {
	deja := "/usr/local/bin/deja hook-context --copilot"
	for _, tc := range []struct{ name, list, want string }{
		{
			name: "deja last",
			list: "      // mine: warm the cache first\n      {\"type\": \"command\", \"bash\": \"make warm\"},\n      {\"type\": \"command\", \"bash\": \"" + deja + "\"}\n",
			want: "      // mine: warm the cache first\n      {\"type\": \"command\", \"bash\": \"make warm\"}\n",
		},
		{
			name: "deja first",
			list: "      {\"type\": \"command\", \"bash\": \"" + deja + "\"},\n      // mine: warm the cache first\n      {\"type\": \"command\", \"bash\": \"make warm\"}\n",
			want: "      // mine: warm the cache first\n      {\"type\": \"command\", \"bash\": \"make warm\"}\n",
		},
		{
			name: "deja between two of the reader's",
			list: "      {\"bash\": \"a\"}, // first\n      {\"bash\": \"" + deja + "\"},\n      /* last */ {\"bash\": \"b\", /* why */ \"timeoutSec\": 5}\n",
			want: "      {\"bash\": \"a\"}, // first\n      /* last */ {\"bash\": \"b\", /* why */ \"timeoutSec\": 5}\n",
		},
		{
			name: "deja's element carries a comment of its own",
			list: "      {\"bash\": \"a\"},\n      {\"bash\": \"" + deja + "\", /* ours */ \"type\": \"command\"}\n",
			want: "      {\"bash\": \"a\"}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := "{\n  // copilot settings\n  \"hooks\": {\n    \"sessionStart\": [\n" + tc.list + "    ]\n  }\n}\n"
			var was map[string]any
			if err := json.Unmarshal([]byte(jsoncToJSON(text)), &was); err != nil {
				t.Fatal(err)
			}
			hooks := typedOrFatal[map[string]any](t, was["hooks"])
			start := typedOrFatal[[]any](t, hooks["sessionStart"])
			var kept []any
			for _, e := range start {
				m := typedOrFatal[map[string]any](t, e)
				if b, _ := m["bash"].(string); b != deja {
					kept = append(kept, e)
				}
			}
			root := map[string]any{"hooks": map[string]any{"sessionStart": kept}}
			got, err := dropCopilotHooksJSONC(text, typedOrFatal[map[string]any](t, was["hooks"]), root)
			if err != nil {
				t.Fatal(err)
			}
			want := "{\n  // copilot settings\n  \"hooks\": {\n    \"sessionStart\": [\n" + tc.want + "    ]\n  }\n}\n"
			if got != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// A line comment that ends the line in front of the key taken out keeps its
// newline: without it the closing brace landed inside the comment.
func TestTakingAKeyOutAfterALineCommentKeepsTheBrace(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"{\n  \"tools\": {\"a\": 1, // keep\n    \"deja\": true}\n}", "{\n  \"tools\": {\"a\": 1 // keep\n}\n}"},
		{"{\n  \"tools\": {\"a\": 1, // src/**/\n    \"deja\": true}\n}", "{\n  \"tools\": {\"a\": 1 // src/**/\n}\n}"},
	} {
		got, err := jsoncRemoveKey(tc.text, "tools", "deja", 1)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
	text := "{\"x\": 1, // keep\n  \"mcpServers\": {\"deja\": {\"command\": \"d\"}}}"
	got, err := jsoncSetEntry(text, "mcpServers", "deja", "", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
		t.Errorf("not JSON: %v\n%s", err, got)
	}
}

// A comment between an entry and its trailing comma stays when the entry is
// taken out, and the comma after the comment goes so the object still parses.
func TestTakingAKeyOutWithCommentBeforeCommaLeavesValidJSON(t *testing.T) {
	cases := []struct {
		name    string
		comment string
		text    string
	}{
		{
			name:    "block comment between entry and comma",
			comment: "/* deja's */",
			text:    "{\n  \"mcpServers\": {\n    \"deja\": {\"args\": [\"mcp\"], \"command\": \"/usr/local/bin/deja\"} /* deja's */,\n    \"other\": {\"command\": \"x\"}\n  }\n}",
		},
		{
			name:    "line comment between entry and comma",
			comment: "// deja's",
			text:    "{\n  \"mcpServers\": {\n    \"deja\": {\"args\": [\"mcp\"], \"command\": \"/usr/local/bin/deja\"} // deja's\n,\n    \"other\": {\"command\": \"x\"}\n  }\n}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsoncSetEntry(tc.text, "mcpServers", "deja", "", true, 0)
			if err != nil {
				t.Fatalf("jsoncSetEntry failed: %v", err)
			}
			if !strings.Contains(got, tc.comment) {
				t.Errorf("comment %q was lost:\n%s", tc.comment, got)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
				t.Fatalf("not valid JSONC after removing entry: %v\n%s", err, got)
			}
			servers, ok := root["mcpServers"].(map[string]any)
			if !ok {
				t.Fatalf("mcpServers is missing or not an object: %v", root)
			}
			if _, hasDeja := servers["deja"]; hasDeja {
				t.Errorf("deja was not removed")
			}
			if _, hasOther := servers["other"]; !hasOther {
				t.Errorf("other was lost")
			}
		})
	}
}

// A null ui in a commented file is refused rather than written beside.
func TestAJSONCStatusLineUnderANullParentIsRefused(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	before := "{\n  // c\n  \"ui\": null\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"type": "command", "command": "/opt/deja/deja-hook statusline"}
	if _, err := installJSONStatusline(path, []string{"ui", "statusLine"}, entry, false); err == nil {
		t.Error("a status line went in beside a null ui")
	}
	if got := readFile(t, path); got != before {
		t.Errorf("the file was touched:\n%s", got)
	}
}

func countEvalSymlinks(t *testing.T) *int {
	t.Helper()
	n := 0
	orig := evalSymlinks
	evalSymlinks = func(p string) (string, error) {
		n++
		return orig(p)
	}
	t.Cleanup(func() { evalSymlinks = orig })
	return &n
}

// Each write compared its path against every config the run had created by
// resolving all of them, so an install of n configs walked n² paths. A file
// with another name cannot resolve to this one.
func TestWriteResolvesOnlyCreatedFilesOfTheSameName(t *testing.T) {
	dir := t.TempDir()
	createdByThisRun = make([]string, 0, 50)
	t.Cleanup(func() { createdByThisRun = nil; snapshotsByThisRun = nil })
	for i := range 50 {
		p := filepath.Join(dir, fmt.Sprintf("config-%d.json", i))
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		createdByThisRun = append(createdByThisRun, p)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	walks := countEvalSymlinks(t)
	if _, err := backupOnceUnlessCreated(path); err != nil {
		t.Fatal(err)
	}
	if *walks != 0 {
		t.Errorf("resolved %d created files to compare against one write, want 0", *walks)
	}
}

// A file created under a linked directory is still deja's own when the write
// arrives with the resolved name.
func TestWriteKnowsACreatedFileThroughALinkedDirectory(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "dotfiles", "cfg")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "cfg")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.WriteFile(filepath.Join(target, "mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	createdByThisRun = []string{filepath.Join(link, "other.json"), filepath.Join(link, "mcp.json")}
	t.Cleanup(func() { createdByThisRun = nil; snapshotsByThisRun = nil })
	resolved, err := filepath.EvalSymlinks(filepath.Join(link, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	took, err := backupOnceUnlessCreated(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if took {
		t.Error("took a snapshot of a file this run created, reached through a linked directory")
	}
}

// snapshotTaken resolved the directory of every recorded snapshot on each
// call; resolution keeps the file name, so only same-named ones can match.
func TestSnapshotTakenResolvesOnlySnapshotsOfTheSameName(t *testing.T) {
	dir := t.TempDir()
	snapshotsByThisRun = make([]string, 0, 50)
	t.Cleanup(func() { snapshotsByThisRun = nil })
	for i := range 50 {
		snapshotsByThisRun = append(snapshotsByThisRun, filepath.Join(dir, fmt.Sprintf("config-%d.json.bak", i)))
	}
	walks := countEvalSymlinks(t)
	if snapshotTaken(filepath.Join(dir, "settings.json")) {
		t.Fatal("settings.json has no snapshot")
	}
	if *walks > 1 {
		t.Errorf("resolved %d directories to look up one snapshot, want 1", *walks)
	}
}

// A Windows user's configs are CRLF, and install rewrote the JSON ones LF-only
// while leaving the TOML and YAML ones half and half — deja's appended block in
// LF inside a CRLF file (#1668).
func TestInstallKeepsCRLFConfigs(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	files := map[string]string{
		".gemini/settings.json":  "{\n  \"theme\": \"dark\"\n}\n",
		".cursor/mcp.json":       "{\n  \"mcpServers\": {}\n}\n",
		".codex/config.toml":     "model = \"o3\"\n",
		".kimi-code/config.toml": "model = \"kimi\"\n",
	}
	for rel, text := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		crlf := strings.ReplaceAll(text, "\n", "\r\n")
		if err := os.WriteFile(p, []byte(crlf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--auto"); err != nil {
		t.Fatal(err)
	}
	for rel := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lf := bytes.Count(b, []byte("\n"))
		crlf := bytes.Count(b, []byte("\r\n"))
		if lf == 0 {
			t.Errorf("%s: no lines at all after install", rel)
			continue
		}
		if crlf != lf {
			t.Errorf("%s: %d of %d line endings are CRLF, the file was written CRLF throughout", rel, crlf, lf)
		}
	}
}

// A file that was LF must not be converted the other way, and a file deja
// creates itself stays LF.
func TestInstallLeavesLFConfigsAlone(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	p := filepath.Join(home, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{\n  \"theme\": \"dark\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "--auto"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("\r")) {
		t.Errorf("an LF config came back with CR in it:\n%q", string(b))
	}
}

// Idempotence has to survive the conversion: the CRLF file is compared after
// the endings are matched, not before, or every repeat install rewrites it.
func TestInstallCRLFStaysUnchangedOnRepeat(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	seed := map[string]string{
		".gemini/settings.json":  "{\n  \"theme\": \"dark\"\n}\n",
		".cursor/mcp.json":       "{\n  \"mcpServers\": {}\n}\n",
		".codex/config.toml":     "model = \"o3\"\n",
		".kimi-code/config.toml": "model = \"kimi\"\n",
		".grok/config.toml":      "[mcp_servers.mine]\ncommand = \"my-server\"\n",
		".hermes/config.yaml":    "model: hermes-3\n",
		".dsh/cordis.patch.yml":  "# dsh patch\n- insert:\n    - id: mine\n      name: my-plugin\n",
	}
	for rel, text := range seed {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		crlf := strings.ReplaceAll(text, "\n", "\r\n")
		if err := os.WriteFile(p, []byte(crlf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--auto"); err != nil {
		t.Fatal(err)
	}
	first := map[string][]byte{}
	for rel := range seed {
		b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		first[rel] = b
	}
	if _, err := captureRun(t, "install", "--auto"); err != nil {
		t.Fatal(err)
	}
	for rel := range seed {
		b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first[rel], b) {
			t.Errorf("%s: the second install rewrote a CRLF config:\nfirst:  %q\nsecond: %q", rel, string(first[rel]), string(b))
		}
	}
}

// Crush reads a flat {"context": …}; Claude's nested hookSpecificOutput means
// nothing to it. Wired with the wrong shape the hook still exits 0 and Crush
// still runs the tool, so nothing fails — the recall is simply never seen.
func TestHookToolWritesCrushsOwnEnvelope(t *testing.T) {
	t.Setenv("DEJA_COMMAND_HINTS", "on")
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
	re, err := regexp.Compile(crushHookMatcher) //nolint:gocritic // a matcher that does not compile is the failure this test reports
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
	if uerr := json.Unmarshal(b, &root); uerr != nil {
		t.Fatalf("%v: %s", uerr, b)
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
	if _, ierr := installTarget("crush-auto", "/bin/deja", true); ierr != nil {
		t.Fatal(ierr)
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

// cursor-agent 2026.09.02 runs an mcp.json entry with "disabled": true — its
// schema drops the key and `cursor-agent mcp list` says ready — so install and
// doctor calling it off told the person recall was off while it ran (#5162).
func TestCursorEntryDisabledKeyIsNotASwitch(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"],"disabled":true}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "cursor", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "switched off") {
		t.Errorf("install called the cursor entry off:\n%s", out)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"disabled": true`) && !strings.Contains(string(b), `"disabled":true`) {
		t.Errorf("the reader's key was dropped:\n%s", b)
	}
	js, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		MCP []doctorMCPStatus `json:"mcp"`
	}
	if uerr := json.Unmarshal([]byte(js), &report); uerr != nil {
		t.Fatalf("doctor --json: %v: %s", uerr, js)
	}
	seen := false
	for _, r := range report.MCP {
		if r.Name != "cursor" {
			continue
		}
		seen = true
		if r.State != "wired" || r.SwitchedOff {
			t.Errorf("cursor row = %+v, want wired and not switched off", r)
		}
	}
	if !seen {
		t.Fatalf("no cursor row:\n%s", js)
	}
	text, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "the entry is switched off") {
		t.Errorf("doctor called the cursor entry off:\n%s", text)
	}
}

// cursor-agent reads hooks.json with its comments dropped, so a commented
// file is a working one. Uninstall refused it as broken with nothing of deja's
// in it, and install called it broken too (#5163).
func TestCursorAutoOverACommentedHooksFile(t *testing.T) {
	hermeticEnv(t)
	dir := sources.CursorCLIHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(dir, "hooks.json")
	seed := "// mine\n{\n  /* v1 */\n  \"version\": 1,\n  \"hooks\": {\n    \"stop\": [{\"command\": \"say done\"}]\n  }\n}\n"
	if err := os.WriteFile(hooks, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "uninstall", "cursor-auto"); err != nil {
		t.Fatalf("uninstall refused a commented hooks.json with nothing of deja's in it: %v\n%s", err, out)
	}
	_, err := captureRun(t, "install", "cursor-auto", "--no-index")
	if err == nil || !strings.Contains(err.Error(), "carries comments") {
		t.Fatalf("install err = %v, want the comments refusal", err)
	}
	if b, _ := os.ReadFile(hooks); string(b) != seed {
		t.Errorf("hooks.json changed:\n%s", b)
	}
	// Refused before the MCP half wrote anything (#2748).
	if _, err := os.Stat(filepath.Join(dir, "mcp.json")); err == nil {
		t.Error("mcp.json was written by a refused target")
	}

	// Nor is the reader's own `version` deja's to take on the way out.
	for _, only := range []string{
		"// mine\n{\"version\": 1}\n",
		"// mine\n{\"version\": 1, \"hooks\": {}}\n",
		"// mine\n{\"version\": 1, \"hooks\": {\"sessionStart\": []}}\n",
		"{\"version\": 1, \"hooks\": {\"sessionStart\": {\"command\": \"mine\"}}}\n",
	} {
		if err := os.WriteFile(hooks, []byte(only), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := captureRun(t, "uninstall", "cursor-auto"); err != nil {
			t.Errorf("uninstall refused %q: %v\n%s", only, err, out)
		}
		if b, _ := os.ReadFile(hooks); string(b) != only {
			t.Errorf("uninstall changed %q to %q", only, b)
		}
	}
}

// The status line file was outside cursor-auto's pre-flight, so a read-only
// one was refused after mcp.json and hooks.json were written (#5164).
func TestCursorAutoRefusesAReadOnlyStatusLineFileBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	hermeticEnv(t)
	cfg := cursorCLIConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("{\n  \"version\": 1\n}\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "install", "cursor-auto", "--no-index")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("install err = %v, want the read-only refusal", err)
	}
	for _, name := range []string{"mcp.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(sources.CursorCLIHome(), name)); err == nil {
			t.Errorf("%s was written by a refused target", name)
		}
	}

	// A status line already deja's needs no write, and is not refused.
	if err := os.Chmod(cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o444); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "cursor-auto", "--no-index"); err != nil {
		t.Errorf("a repeat install refused an unchanged read-only status line file: %v\n%s", err, out)
	}
	// And uninstall still takes the hooks out, refusing only the status line.
	if _, err := captureRun(t, "uninstall", "cursor-auto"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("uninstall err = %v, want the status line refused", err)
	}
	if b, _ := os.ReadFile(filepath.Join(sources.CursorCLIHome(), "hooks.json")); strings.Contains(string(b), "hook-context") {
		t.Errorf("uninstall left deja's hooks in hooks.json:\n%s", b)
	}
}

// cursor-auto checks its status line before mcp.json is written. On a
// commented cli-config.json that check wrote the status line, so a target
// refused for an unreadable mcp.json still left deja's status line wired.
func TestCursorAutoRefusalLeavesACommentedCLIConfigAlone(t *testing.T) {
	hermeticEnv(t)
	cfg := cursorCLIConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "{\n  // mine\n  \"version\": 1\n}\n"
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	mcp := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(mcp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcp, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor-auto", "--no-index"); err == nil {
		t.Fatal("install over an unreadable mcp.json succeeded")
	}
	if b, _ := os.ReadFile(cfg); string(b) != orig {
		t.Errorf("a refused target changed cli-config.json:\n%s", b)
	}
}

func cursorHooksFile(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v", err)
	}
	return root
}

func cursorCommands(t *testing.T, root map[string]any, event string) []string {
	t.Helper()
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	var out []string
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if c, ok := m["command"].(string); ok {
			out = append(out, c)
		}
	}
	return out
}

func TestInstallCursorHooksWritesSessionStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installCursorHooks("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	root := cursorHooksFile(t, home)
	// Cursor's own event names, not Claude's — the file is read by cursor.
	if got := cursorCommands(t, root, "sessionStart"); len(got) != 1 || got[0] != hookExeInConfigs("/bin/deja")+" hook-context" {
		t.Fatalf("sessionStart = %v", got)
	}
	if got := cursorCommands(t, root, "beforeSubmitPrompt"); len(got) != 1 {
		t.Fatalf("beforeSubmitPrompt = %v", got)
	}
	if root["version"] != float64(1) {
		t.Fatalf("version = %v, want 1", root["version"])
	}
}

func TestInstallCursorHooksIsIdempotentAndReversible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A hook the user already had; uninstall must not touch it.
	seed := `{"version":1,"hooks":{"sessionStart":[{"command":"/usr/bin/mine"}]}}`
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := installCursorHooks("/bin/deja", false); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	if got := cursorCommands(t, cursorHooksFile(t, home), "sessionStart"); len(got) != 2 {
		t.Fatalf("repeated installs duplicated entries: %v", got)
	}
	if _, err := installCursorHooks("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	root := cursorHooksFile(t, home)
	got := cursorCommands(t, root, "sessionStart")
	if len(got) != 1 || got[0] != "/usr/bin/mine" {
		t.Fatalf("uninstall did not leave the user's hook alone: %v", got)
	}
	if c := cursorCommands(t, root, "beforeSubmitPrompt"); len(c) != 0 {
		t.Fatalf("beforeSubmitPrompt survived uninstall: %v", c)
	}
}

// The binary moves and the repair reinstalls cursor. An entry deja wrote from
// the old path is deja's, not a stranger's: keeping it leaves cursor running a
// binary that is gone on every session start and every prompt, and one more
// entry accumulates with every move (#2691).
func TestInstallCursorHooksRewritesItsOwnEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	// One hook of the reader's own, and one they built around deja: neither is
	// deja's line to rewrite.
	seed := `{"version":1,"hooks":{"sessionStart":[` +
		`{"command":"/usr/bin/mine"},` +
		`{"command":"/old/bin/deja hook-context"},` +
		`{"command":"cd /tmp && /old/bin/deja hook-context | tee /tmp/log"}` +
		`]}}`
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installCursorHooks("/new/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got := cursorCommands(t, cursorHooksFile(t, home), "sessionStart")
	want := []string{
		"/usr/bin/mine",
		hookExeInConfigs("/new/bin/deja") + " hook-context",
		"cd /tmp && /old/bin/deja hook-context | tee /tmp/log",
	}
	if len(got) != len(want) {
		t.Fatalf("sessionStart = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sessionStart[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// And uninstall takes back the line deja wrote from wherever it was
	// written, leaving the reader's two alone.
	if _, err := installCursorHooks("/newer/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got = cursorCommands(t, cursorHooksFile(t, home), "sessionStart")
	want = []string{"/usr/bin/mine", "cd /tmp && /old/bin/deja hook-context | tee /tmp/log"}
	if len(got) != len(want) {
		t.Fatalf("after uninstall sessionStart = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("after uninstall sessionStart[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Two shapes the rewrite has to answer for beyond the plain one: a file whose
// only entry is a wrapper the reader built, and a file that already collected
// several of deja's own lines from several paths (#2691).
func TestInstallCursorHooksLeavesOneEntryBehind(t *testing.T) {
	seedCursor := func(t *testing.T, entries string) string {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
		if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
			t.Fatal(err)
		}
		seed := `{"version":1,"hooks":{"sessionStart":[` + entries + `]}}`
		if err := os.WriteFile(filepath.Join(home, ".cursor", "hooks.json"), []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		return home
	}

	// The wrapper already runs the hook. Adding deja's line beside it runs it
	// twice per session.
	wrapper := `{"command":"cd /tmp && /old/bin/deja hook-context | tee /tmp/log"}`
	home := seedCursor(t, wrapper)
	if _, err := installCursorHooks("/new/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got := cursorCommands(t, cursorHooksFile(t, home), "sessionStart")
	if len(got) != 1 || got[0] != "cd /tmp && /old/bin/deja hook-context | tee /tmp/log" {
		t.Errorf("a wrapper got deja's own line beside it: %v", got)
	}

	// And the file #2691 was written for: the stale entries go, one line for
	// this binary stays.
	home = seedCursor(t, `{"command":"/a/deja hook-context"},{"command":"/b/deja hook-context"}`)
	if _, err := installCursorHooks("/new/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got = cursorCommands(t, cursorHooksFile(t, home), "sessionStart")
	if len(got) != 1 || got[0] != hookExeInConfigs("/new/bin/deja")+" hook-context" {
		t.Errorf("the entries deja left behind were not collected: %v", got)
	}
}

// Cursor reads ~/.claude/settings.json as well as its own hooks.json and maps
// the Claude event names onto its own — PreToolUse→preToolUse,
// PostToolUse→postToolUse, PreCompact→preCompact — so it has been running these
// three deja hooks all along for anyone who also has Claude Code installed, and
// none of them for anyone who does not. They are also three of the five events
// cursor lists as taking a hook's additionalContext.
func TestInstallCursorWiresTheMomentOfTheAction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installCursorHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".cursor", "hooks.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if uerr := json.Unmarshal(b, &root); uerr != nil {
		t.Fatal(uerr)
	}
	// The tool events carry a matcher, which cursor tests against tool_name,
	// so deja does not start on every read and grep.
	for event, want := range map[string]struct{ sub, matcher string }{
		"sessionStart":       {"hook-context", ""},
		"beforeSubmitPrompt": {"hook-prompt", ""},
		"preToolUse":         {"hook-tool", "^(Shell|Write|Task)$"},
		"postToolUse":        {"hook-tool-after", "^Shell$"},
		"postToolUseFailure": {"hook-tool-after", "^Shell$"},
		"preCompact":         {"hook-precompact", ""},
	} {
		entries := root.Hooks[event]
		if len(entries) != 1 {
			t.Fatalf("%s entries = %d, want 1: %s", event, len(entries), b)
		}
		if got := entries[0].Command; !strings.HasSuffix(got, want.sub) {
			t.Errorf("%s runs %q, want %s", event, got, want.sub)
		}
		if got := entries[0].Matcher; got != want.matcher {
			t.Errorf("%s matcher = %q, want %q", event, got, want.matcher)
		}
	}
	// The command string is what cursor dedupes on against ~/.claude, so it has
	// to be exactly what deja writes there — a wrapper or a flag here would
	// give a user with both two hooks per action.
	if got := root.Hooks["postToolUse"][0].Command; got != hookExeInConfigs("/usr/local/bin/deja")+" hook-tool-after" {
		t.Errorf("command = %q, want the same string the claude wiring writes", got)
	}
	// And uninstall takes all five back out.
	if _, ierr := installCursorHooks("/usr/local/bin/deja", true); ierr != nil {
		t.Fatal(ierr)
	}
	b2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b2), "deja hook-") {
		t.Errorf("uninstall left a hook behind: %s", b2)
	}
}

func dshLayerHome(t *testing.T, seed string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	path := filepath.Join(home, ".dsh", "cordis.patch.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if seed != "" {
		if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func readLayer(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// dsh applies a layer's patches in order, and a row addressing an id that is
// not inserted yet is dropped ("entry not found"). Re-install appended its
// block after the user's own `disabled: true` row for mcp-deja, so the switch
// stopped applying and deja ran again (#5180).
func TestDeepSeekReinstallKeepsTheBlockAboveTheUsersSwitch(t *testing.T) {
	path := dshLayerHome(t, "- id: timer\n  disabled: true\n")
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	off := "- id: mcp-deja\n  disabled: true\n- id: deja-auto\n  disabled: true\n"
	if err := os.WriteFile(path, []byte(readLayer(t, path)+off), 0o644); err != nil {
		t.Fatal(err)
	}
	if !dshRowDisabled(readLayer(t, path), "mcp-deja") || !dshRowDisabled(readLayer(t, path), "deja-auto") {
		t.Errorf("doctor does not see the user's switch rows after the block:\n%s", readLayer(t, path))
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got := readLayer(t, path)
	block, user := strings.Index(got, dshBlockStart), strings.Index(got, off)
	if block < 0 || user < 0 || block > user {
		t.Fatalf("the block no longer comes before the rows that patch it:\n%s", got)
	}
	if !dshRowDisabled(got, "mcp-deja") {
		t.Errorf("mcp-deja reads on after re-install:\n%s", got)
	}

	// A switch row above the block patches nothing, so it is not off.
	above := "- id: mcp-deja\n  disabled: true\n" + strings.Replace(got, off, "", 1)
	if dshRowDisabled(above, "mcp-deja") {
		t.Errorf("a row above the insert was read as a switch dsh applies:\n%s", above)
	}
	// The later row decides.
	if dshRowDisabled(got+"- id: mcp-deja\n  disabled: false\n", "mcp-deja") {
		t.Errorf("a later `disabled: false` did not turn it back on")
	}
}

// dsh reads the layer as a single document. Appending after a closing `...`
// made a second one, and a column-0 row after a list indented by two spaces
// ended the list; either way dsh refused to start (#5181).
func TestDeepSeekInstallKeepsTheLayerOneParseableList(t *testing.T) {
	for _, tc := range []struct{ name, seed, want string }{
		{"document end", "- id: timer\n  disabled: true\n...\n", "...\n"},
		{"document end and comment", "- id: timer\n  disabled: true\n...\n# trailing note\n", "...\n# trailing note\n"},
		{"indented list", "  - id: timer\n    disabled: true\n", ""},
		{"indented after comment", "# mine\n---\n    - id: timer\n      disabled: true\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := dshLayerHome(t, tc.seed)
			if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			got := readLayer(t, path)
			if tc.want != "" && !strings.HasSuffix(got, dshBlockEnd+"\n"+tc.want) {
				t.Errorf("the block is not inside the document:\n%s", got)
			}
			want := ""
			for line := range strings.SplitSeq(tc.seed, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "- ") {
					want = line[:yamlIndentWidth(line)]
					break
				}
			}
			for line := range strings.SplitSeq(got, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "- insert:") && !strings.HasPrefix(line, want+"- insert:") {
					t.Errorf("deja's row is at %q, the list is at %q:\n%s", line, want, got)
				}
			}
			again, err := installDeepSeekAuto("/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if readLayer(t, path) != got {
				t.Errorf("a second install changed the layer (%s):\n%s", again.Action, readLayer(t, path))
			}
			if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if back := readLayer(t, path); back != tc.seed {
				t.Errorf("uninstall left\n%q\nwant\n%q", back, tc.seed)
			}
		})
	}
}

// A read-only layer is refused at its own write, which came after the three
// plugin directories: install said it left the file alone, doctor called
// auto-recall wired, and uninstall said "not installed" and kept them (#5182).
func TestDeepSeekReadOnlyLayerIsRefusedBeforeAnyPlugin(t *testing.T) {
	path := dshLayerHome(t, "- id: timer\n  disabled: true\n")
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if configWritable(path) == nil {
		t.Skip("the read-only mode does not hold here")
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err == nil {
		t.Fatal("install over a read-only layer succeeded")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "plugins")); !os.IsNotExist(err) {
		t.Errorf("plugins were written for a layer deja could not change (stat: %v)", err)
	}
}

// A layer that already holds deja's block is not written, so locking it does
// not stop a re-install from refreshing the plugins.
func TestDeepSeekReinstallOverALockedWiredLayer(t *testing.T) {
	path := dshLayerHome(t, "- id: timer\n  disabled: true\n")
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Errorf("re-install over a locked layer that needs no change: %v", err)
	}
	// Without its final newline the layer still needs no change.
	_ = os.Chmod(path, 0o644)
	if err := os.WriteFile(path, []byte(strings.TrimRight(readLayer(t, path), "\n")), 0o444); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0o444)
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Errorf("re-install over a locked layer with no final newline: %v", err)
	}
}

// What an older install left: its block after `...`. Re-install moves the
// block inside the document, so dsh starts again. A switch it left above the
// block patches nothing; doctor and install say deja is on, as dsh runs it.
func TestDeepSeekReinstallHealsWhatTheAppendBroke(t *testing.T) {
	block := dshPatchBlock("/usr/local/bin/deja", true)
	path := dshLayerHome(t, "- id: timer\n...\n"+block+"\n")
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got := readLayer(t, path); !strings.HasSuffix(got, dshBlockEnd+"\n...\n") {
		t.Errorf("the block stayed after the document end:\n%s", got)
	}

	path = dshLayerHome(t, "- id: mcp-deja\n  disabled: True\n"+block+"\n")
	r, err := installDeepSeekAuto("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := readLayer(t, path); dshRowDisabled(got, "mcp-deja") || strings.Contains(r.Note, switchedOffNote) {
		t.Errorf("a dead row above the block read as off (note %q):\n%s", r.Note, got)
	}
	if !dshRowDisabled(readLayer(t, path)+"- id: mcp-deja\n  disabled: True\n", "mcp-deja") {
		t.Errorf("`disabled: True` below the block is not read as off")
	}

	// A switch in the block that a later row of the user's turns back on: no
	// "switched off" note.
	off := strings.Replace(block, "    - id: mcp-deja\n", "    - id: mcp-deja\n      disabled: true\n", 1)
	dshLayerHome(t, off+"\n- id: mcp-deja\n  disabled: false\n")
	if r, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil || strings.Contains(r.Note, switchedOffNote) {
		t.Errorf("note %q (err %v) for a switch the user turned back on", r.Note, err)
	}
}

// The generated `[]` goes back inside the document on uninstall: after `...`
// the layer would be null, which dsh refuses (#3271).
func TestDeepSeekUninstallPutsTheArrayInsideTheDocument(t *testing.T) {
	for _, seed := range []string{"# generated\n[]\n...\n", "---\n[]\n", "# generated\r\n[]\r\n...\r\n", "---\r\n[]\r\n"} {
		path := dshLayerHome(t, seed)
		if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
			t.Fatal(err)
		}
		if got := readLayer(t, path); got != seed {
			t.Errorf("uninstall left %q, want %q", got, seed)
		}
	}
}

// dsh 0.2.0 made a session's event array private: the log is read one event at
// a time with eventAt(seq), and session.events is undefined. auto.js read only
// session.events, found no prompt, and never asked deja on any turn — the
// per-prompt recall was off for every 0.2.0 profile with nothing reported.
func TestDeepSeekAutoFindsThePromptInTheDsh020Log(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s\\n' \"$1\" \"$in\" >> " + calls + "\n" +
		"case \"$1\" in hook-prompt) printf 'RECALLED' ;; esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "auto.js")
	if werr := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import plugin from "` + plugin + `";
const contexts = {};
plugin({ systemPrompt: { context: (c) => { contexts[c.name] = c.text } }, on: () => {} });
// The 0.2.0 shape: no events array, eventAt over a private log.
const log = [
  { type: "agent/inbox/spliced", data: { inserted: [{ role: "user", source: { kind: "user" }, content: [{ type: "text", text: "how do we drain the zorbit queue" }] }] } },
  { type: "turn/start", data: { turn: 1 } },
  { type: "agent/inbox/spliced", data: { removedCount: 1, inserted: [] } },
];
const session = { id: "session-s1", header: { cwd: "/w/proj" }, get seq() { return log.length }, eventAt: (i) => log[i] };
console.log(contexts["deja:recall"]({ agent: { sessionId: "session-s1", session } }) || "SILENT");
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "RECALLED" {
		t.Errorf("the recall never reached the model on a 0.2.0 session: %s", out)
	}
	asked, _ := os.ReadFile(calls)
	if !strings.Contains(string(asked), `"prompt":"how do we drain the zorbit queue"`) {
		t.Errorf("deja was not asked about the prompt:\n%s", asked)
	}
}

// driveDSHAuto installs deepseek-auto, points its auto.js at a stub deja, and
// runs one turn through it the way dsh would: two contexts assembled for a
// session, and, when web is true, the connection and web-server services the
// web profile injects. It returns what the driver printed and the stub's calls.
func driveDSHAuto(t *testing.T, web, withLib, card bool) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := dshStatusHome(t)
	if _, ierr := installDeepSeekAuto("/usr/local/bin/deja", false); ierr != nil {
		t.Fatal(ierr)
	}
	if !withLib {
		if rerr := os.Remove(dshLibPath()); rerr != nil {
			t.Fatal(rerr)
		}
	}
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	// JSON unless asked for --plain, as deja answers: the model's text in
	// additionalContext, the person's lines in systemMessage.
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' \"$*\" >> " + calls + "\n" +
		"case \"$*\" in\n" +
		"  *--plain*) printf 'MODEL TEXT %s' \"$1\" ;;\n" +
		"  hook-prompt) printf '{\"hookSpecificOutput\":{\"hookEventName\":\"UserPromptSubmit\",\"additionalContext\":\"MODEL TEXT hook-prompt\"},\"systemMessage\":\"deja-vu — you have been here: \\\\\"zorbit drain\\\\\" (4d ago · via: drain)\"}' ;;\n" +
		"  hook-context) printf '{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"MODEL TEXT hook-context\"},\"systemMessage\":\"deja: first build done\"}' ;;\n" +
		"esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(dshAutoPath(), []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	loadCard := ""
	if card {
		loadCard = `(await import(pathToFileURL(process.argv[3]).href)).default({ effect: (f) => f() });`
	}
	services := "{}"
	if web {
		services = `{ connection: { fetch: { register: (r) => { routes.push(r) } } }, webServer: {} }`
	}
	driver := `
import { pathToFileURL } from "node:url";
const plugin = (await import(pathToFileURL(process.argv[2]).href)).default;
` + loadCard + `
const routes = [];
const contexts = {};
const services = ` + services + `;
plugin({
  systemPrompt: { context: (c) => { contexts[c.name] = c.text } },
  on: () => {},
  inject: (names, cb) => { if (names.every((n) => n in services)) cb(services) },
});
await new Promise((r) => setTimeout(r, 50));
const log = [{ type: "user/message", data: { role: "user", content: [{ type: "text", text: "how do we drain zorbit" }] } }];
const agent = { sessionId: "session-s1", session: { id: "session-s1", header: { cwd: "/w" }, events: log } };
console.log("project=" + contexts["deja:project"]({ agent }));
console.log("recall=" + contexts["deja:recall"]({ agent }));
console.log("routes=" + routes.length);
for (const r of routes) {
  const take = async () => (await (await r.fetch(new Request("http://x/api/deja-notes?session=session-s1"))).json()).notes;
  console.log("path=" + r.path);
  console.log("notes=" + JSON.stringify(await take()));
  console.log("again=" + JSON.stringify(await take()));
}
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run, dshAutoPath(), dshNotesPath()).CombinedOutput()
	if err != nil {
		t.Fatalf("driving auto.js: %v\n%s", err, out)
	}
	asked, _ := os.ReadFile(calls)
	return string(out), string(asked)
}

// In dsh web the person sees deja's lines: auto.js asks each hook once for its
// JSON reply, hands the model exactly the text --plain prints, and keeps the
// receipt and notes for the card behind /api/deja-notes, once (#4983).
func TestDeepSeekAutoShowsThePersonDejasLinesInDshWeb(t *testing.T) {
	out, asked := driveDSHAuto(t, true, true, true)
	for _, want := range []string{
		"project=MODEL TEXT hook-context\n",
		"recall=MODEL TEXT hook-prompt\n",
		"routes=1\n",
		"path=/api/deja-notes\n",
		`notes=["deja: first build done","deja-vu — you have been here: \"zorbit drain\" (4d ago · via: drain)"]` + "\n",
		"again=[]\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(asked, "--plain") || strings.Count(asked, "\n") != 2 {
		t.Errorf("want one JSON call per hook, got:\n%s", asked)
	}
}

// Without a page — tui, headless, sdk — nothing reads the board, so the hooks
// stay --plain and deja does not mark notes shown that nobody saw. Without
// lib.js the plugin still recalls and registers no route.
func TestDeepSeekAutoKeepsPlainWhereNoPageReads(t *testing.T) {
	for _, tc := range []struct {
		name               string
		web, withLib, card bool
		routes             string
	}{
		{"headless", false, true, true, "routes=0\n"},
		{"no lib.js", true, false, true, "routes=0\n"},
		// The person switched the deja-notes row off: no card, so the route
		// may stand but nothing is asked for it.
		{"card row off", true, true, false, "routes=1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, asked := driveDSHAuto(t, tc.web, tc.withLib, tc.card)
			if !strings.Contains(out, "project=MODEL TEXT hook-context\n") || !strings.Contains(out, "recall=MODEL TEXT hook-prompt\n") || !strings.Contains(out, tc.routes) {
				t.Errorf("driver:\n%s", out)
			}
			if strings.Count(asked, "--plain") != 2 {
				t.Errorf("want both hooks asked --plain, got:\n%s", asked)
			}
		})
	}
}

// The card is the npm plugin's client.js in a package of its own: dsh serves
// a browser half only from a package declaring dsh.client, and refuses two
// rows resolving to one package. The MCP-only target and uninstall take it
// back out, with lib.js.
func TestDeepSeekAutoWritesTheNotesCard(t *testing.T) {
	home := dshStatusHome(t)
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".dsh", "plugins", "deja-notes")
	var pkg struct {
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Exports map[string]string `json:"exports"`
		Dsh     struct {
			Client struct {
				Platform string `json:"platform"`
			} `json:"client"`
		} `json:"dsh"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "deja-notes" || pkg.Version == "" || pkg.Exports["./client"] != "./client.js" || pkg.Dsh.Client.Platform != "web" {
		t.Errorf("notes package.json = %+v", pkg)
	}
	client, _ := os.ReadFile(filepath.Join(dir, "client.js"))
	if !strings.Contains(string(client), `id: "deja-notes",`) || strings.Contains(string(client), `id: "dsh-deja"`) || !strings.Contains(string(client), `"conversation.input.dock"`) {
		t.Errorf("client.js does not register as deja-notes:\n%.400s", client)
	}
	if _, err := os.Stat(filepath.Join(home, ".dsh", "plugins", "deja", "lib.js")); err != nil {
		t.Errorf("lib.js is not beside auto.js: %v", err)
	}
	layer := filepath.Join(home, ".dsh", "cordis.patch.yml")
	if l, _ := os.ReadFile(layer); !strings.Contains(string(l), "- id: deja-notes\n") || !strings.Contains(string(l), yamlQuote(filepath.Join(dir, "index.js"))) {
		t.Errorf("the layer does not name the notes package:\n%s", l)
	}
	if res, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil || res.Action != "unchanged" {
		t.Errorf("a second install: %+v, %v", res, err)
	}

	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the MCP-only target left the notes package: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".dsh", "plugins", "deja", "lib.js")); !os.IsNotExist(err) {
		t.Errorf("the MCP-only target left lib.js: %v", err)
	}

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uninstall left the notes package: %v", err)
	}
}

// `deja install dsh-auto` is what the dsh guide prints. Recorded under the
// alias it was dropped as unknown, and `deja uninstall dsh-auto` was refused.
func TestDshAliasIsRecordedAndUninstalls(t *testing.T) {
	got := withAutoTargets([]string{"dsh-auto"})
	if strings.Join(got, ",") != "deepseek,deepseek-auto" {
		t.Errorf("withAutoTargets(dsh-auto) = %v", got)
	}
	hermeticEnv(t)
	recordWiring([]string{"dsh-auto"}, false)
	if st := readWiringState(); strings.Join(st.Targets, ",") != "deepseek-auto" {
		t.Errorf("recorded targets = %v", st.Targets)
	}
}

// A plugins/deja-notes or plugins/deja-status of somebody else's is left as it
// is by install, by the MCP-only target and by uninstall, and the layer names
// no deja row for it.
func TestDeepSeekLeavesSomeoneElsesPluginPackages(t *testing.T) {
	home := dshStatusHome(t)
	theirs := map[string]string{}
	for _, name := range []string{"deja-notes", "deja-status"} {
		dir := filepath.Join(home, ".dsh", "plugins", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "export default function apply() { /* mine: " + name + " */ }\n"
		if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		theirs[dir] = body
	}
	check := func(step string) {
		t.Helper()
		for dir, body := range theirs {
			if b, err := os.ReadFile(filepath.Join(dir, "index.js")); err != nil || string(b) != body {
				t.Errorf("%s: %s/index.js = %q, %v", step, dir, b, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "client.js")); !os.IsNotExist(err) {
				t.Errorf("%s: deja wrote into %s", step, dir)
			}
		}
	}
	res, err := installDeepSeekAuto("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	check("install")
	if !strings.Contains(res.Note, "alone") || !strings.Contains(res.Note, "deja-notes") {
		t.Errorf("install did not say it left them: %+v", res)
	}
	layer, _ := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if strings.Contains(string(layer), "deja-notes") || strings.Contains(string(layer), "deja-status") {
		t.Errorf("the layer names a package deja did not write:\n%s", layer)
	}
	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	check("MCP-only install")
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	check("uninstall")
}

// A machine wired with `deja install dsh-auto` before the alias was recorded
// has deja's auto.js on disk and no deepseek-auto in the record. install --all
// has to keep it, or it takes auto.js and the card away.
func TestInstallAllKeepsDshAutoWiredBeforeItWasRecorded(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DSH_HOME", filepath.Join(t.TempDir(), ".dsh"))
	if got := withWiredAutoTargets([]string{"deepseek"}); strings.Join(got, ",") != "deepseek" {
		t.Fatalf("premise: nothing on disk, got %v", got)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got := withWiredAutoTargets([]string{"deepseek"}); strings.Join(got, ",") != "deepseek,deepseek-auto" {
		t.Errorf("withWiredAutoTargets = %v", got)
	}
}

// dsh's per-prompt recall answers the question just asked; the project digest —
// what this project settled, regardless of the question — had no channel, so a
// dsh session opened knowing nothing about the project until it happened to ask
// something the store answered. agent/session-start is emit-only, so the digest
// rides the same systemPrompt.context seam the recall does, gated to one turn
// per session.
func TestDeepSeekAutoOpensWithTheProjectDigest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile(filepath.Join(home, ".dsh", "plugins", "deja", "auto.js"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(js)
	if !strings.Contains(s, `name: "deja:project"`) {
		t.Errorf("no project-digest context is registered:\n%s", s)
	}
	if !strings.Contains(s, `askHook("hook-context"`) {
		t.Errorf("the digest never asks deja for the project's history:\n%s", s)
	}
	if !strings.Contains(s, "deja_once: true") {
		t.Errorf("the digest is not gated to one turn, so it repeats every assembly:\n%s", s)
	}
}

// The plugin's contract exercised the way the host uses it: two assemblies of
// one session get the digest exactly once, a second session gets its own, and
// the digest asks deja with deja_once and the session id.
func TestDeepSeekAutoDigestContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s\\n' \"$1\" \"$in\" >> " + calls + "\n" +
		"case \"$1\" in hook-context) printf 'DIGEST for this project' ;; esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "auto.js")
	if werr := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import plugin from "` + plugin + `";
const contexts = [];
plugin({ systemPrompt: { context: (c) => contexts.push(c) } });
const digest = contexts.find((c) => c.name === "deja:project");
if (!digest) { console.log("NODIGEST"); process.exit(0) }
const a = { sessionId: "sess-A", session: { events: [] } };
const b = { sessionId: "sess-B", session: { events: [] } };
console.log(JSON.stringify(digest.text({ agent: a })));
// DSH calls systemPrompt.refreshContext at request admission on the first turn
// (#5904); the cached digest survives that re-resolution and clears on turn 2.
console.log(JSON.stringify(digest.text({ agent: a })));
a.session.events.push({ type: "turn/start", data: { turn: 2 } });
console.log(JSON.stringify(digest.text({ agent: a })));
console.log(JSON.stringify(digest.text({ agent: b })));
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
	if lines[0] == "NODIGEST" {
		t.Fatal("the plugin registers no deja:project context, so a session opens with no memory of the project")
	}
	if len(lines) < 4 {
		t.Fatalf("driver said %q", out)
	}
	if !strings.Contains(lines[0], "DIGEST") {
		t.Errorf("the session's first turn carried no digest: %q", lines[0])
	}
	if !strings.Contains(lines[1], "DIGEST") {
		t.Errorf("refreshContext on the first turn lost the digest (#5904): %q", lines[1])
	}
	if strings.Contains(lines[2], "DIGEST") {
		t.Errorf("the digest came back on the same session's second turn: %q", lines[2])
	}
	if !strings.Contains(lines[3], "DIGEST") {
		t.Errorf("a second session was refused the digest the first one had: %q", lines[3])
	}
	// One session, one call: the in-process guard must keep the long-lived web
	// and tui profiles from spawning deja on every assembly.
	asked, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the plugin never called deja: %v", err)
	}
	if n := strings.Count(string(asked), "hook-context"); n != 2 {
		t.Errorf("expected one hook-context per session (2 total), got %d:\n%s", n, asked)
	}
	if !strings.Contains(string(asked), `"deja_once":true`) {
		t.Errorf("the digest payload does not ask for one per session:\n%s", asked)
	}
	if !strings.Contains(string(asked), `"session_id":"sess-A"`) {
		t.Errorf("the digest payload names no session, and the guard is keyed on it:\n%s", asked)
	}
}

func dshStatusHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	return home
}

// deepseek-auto adds the status plugin in a package of its own, with the
// dsh.client declaration and "./client" export the web client's module scan
// looks for, and a row naming it; the MCP-only target and uninstall take both
// back out. Every package.json carries a version: dsh 0.2 refuses one without,
// and the refusal failed every model request.
func TestDeepSeekAutoAddsTheWebStatusPlugin(t *testing.T) {
	home := dshStatusHome(t)
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".dsh", "plugins", "deja-status")
	var pkg struct {
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Exports map[string]string `json:"exports"`
		Dsh     struct {
			Client struct {
				Platform string `json:"platform"`
			} `json:"client"`
		} `json:"dsh"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if uerr := json.Unmarshal(b, &pkg); uerr != nil {
		t.Fatal(uerr)
	}
	if pkg.Name != "deja-status" || pkg.Version == "" || pkg.Exports["./client"] != "./client.js" || pkg.Dsh.Client.Platform != "web" {
		t.Errorf("status package.json = %+v", pkg)
	}
	client, err := os.ReadFile(filepath.Join(dir, "client.js"))
	if err != nil || !strings.Contains(string(client), `id: "deja-status"`) || !strings.Contains(string(client), `"conversation.composer.dock"`) {
		t.Errorf("client.js does not register the slot item (%v):\n%s", err, client)
	}
	var cmdPkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	b, _ = os.ReadFile(filepath.Join(home, ".dsh", "plugins", "deja", "package.json"))
	if err := json.Unmarshal(b, &cmdPkg); err != nil || cmdPkg.Version == "" {
		t.Errorf("the command plugin's package.json has no version (%v):\n%s", err, b)
	}
	layer := filepath.Join(home, ".dsh", "cordis.patch.yml")
	if l, _ := os.ReadFile(layer); !strings.Contains(string(l), "- id: deja-status\n") || !strings.Contains(string(l), yamlQuote(filepath.Join(dir, "index.js"))) {
		t.Errorf("the layer does not name the status plugin:\n%s", l)
	}
	if res, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil || res.Action != "unchanged" {
		t.Errorf("a second install: %+v, %v", res, err)
	}

	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the MCP-only target left the status plugin: %v", err)
	}
	if l, _ := os.ReadFile(layer); strings.Contains(string(l), "deja-status") {
		t.Errorf("the MCP-only layer still names the status plugin:\n%s", l)
	}

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uninstall left the status plugin: %v", err)
	}
}

// The node half answers /deja/statusline with deja's line on dsh's web server,
// only once that service exists, and only for a page on this machine.
func TestDeepSeekStatusPluginServesTheLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	home := dshStatusHome(t)
	fake := filepath.Join(home, "deja")
	if werr := os.WriteFile(fake, []byte("#!/bin/sh\n[ \"$1\" = statusline ] && echo 'deja · 3 recalls · 2 KB ctx today'\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	if _, ierr := installDeepSeekAuto(fake, false); ierr != nil {
		t.Fatal(ierr)
	}
	const drive = `
const { default: apply } = await import(process.argv[1]);
let route, waited;
apply({
  inject(deps, cb) { waited = deps; cb({ webServer: { register(r) { route = r; return () => {}; } }, effect() {} }); },
});
const ask = (host) => new Promise((resolve) => {
  const res = { code: 0, body: "", writeHead(c) { this.code = c; return this; }, end(b) { this.body = b || ""; resolve(this); } };
  route.handler({ headers: { host } }, res);
});
const ok = await ask("127.0.0.1:3080");
const far = await ask("attacker.example");
console.log(JSON.stringify({ waited, path: route.path, ok: [ok.code, ok.body], far: far.code }));
`
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", drive, "file://"+dshStatusPath())
	cmd.Dir = home
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node could not run the plugin: %v\n%s", err, out)
	}
	want := `{"waited":["webServer"],"path":"/deja/statusline","ok":[200,"deja · 3 recalls · 2 KB ctx today"],"far":403}`
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// dsh renders a prompt context through a strict {{name}} interpolation and
// throws on a name it has not registered, ending the turn before the model is
// called. A recall quoting {{cwd}} from a past session did exactly that on
// every turn (#5421). The installer's auto.js registers the same two contexts
// as the npm plugin, so it has to hand dsh inert text too.
func TestDeepSeekAutoRecallWithTemplateBracesRenders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	script := "#!/bin/sh\ncat >/dev/null\n" +
		"printf '%s\\n' 'last time: unknown prompt variable \"{{cwd}}\" in context \"deja:project\"; {{cwd b {{a b}}'\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "auto.js")
	if werr := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	// render mirrors dsh-system-prompt 0.2.1-rc.2's strict interpolation, with
	// the interpolate flag ignored the way every dsh before 0.2.1-alpha.2 does.
	driver := `
import plugin from ` + strconv.Quote(plugin) + `;
const contexts = [];
plugin({ systemPrompt: { context: (c) => contexts.push(c) } });
function render(c, text) {
  for (let open = text.indexOf("{{"); open >= 0; open = text.indexOf("{{", open + 2)) {
    const group = /^\{\{([^{}]*)\}\}/.exec(text.slice(open));
    if (!group) {
      if (text.indexOf("}}", open + 2) >= 0) throw new Error("malformed prompt variable reference in context " + c.name);
      continue;
    }
    if (!["provider", "model"].includes(group[1])) throw new Error("unknown prompt variable {{" + group[1] + "}} in context " + c.name);
  }
  return text;
}
const agent = { sessionId: "sess-A", session: { events: [
  { type: "user/message", data: { role: "user", content: [{ type: "text", text: "why did the turn fail" }] } },
] } };
for (const c of contexts) {
  try {
    console.log(c.name + " interpolate=" + c.interpolate + " " + JSON.stringify(render(c, c.text({ agent }))));
  } catch (e) {
    console.log(c.name + " THROWS " + e.message);
  }
}
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	got := string(out)
	for _, name := range []string{"deja:project", "deja:recall"} {
		line := ""
		for l := range strings.SplitSeq(got, "\n") {
			if strings.HasPrefix(l, name+" ") {
				line = l
			}
		}
		if line == "" {
			t.Fatalf("%s was not registered:\n%s", name, got)
		}
		if strings.Contains(line, "THROWS") {
			t.Errorf("recalled {{cwd}} fails the turn: %s", line)
		}
		if !strings.Contains(line, "interpolate=false") {
			t.Errorf("%s lets dsh read recalled text as a template: %s", name, line)
		}
		if !strings.Contains(line, "{ {cwd} }") {
			t.Errorf("%s did not carry the recalled text: %s", name, line)
		}
	}
}

// A generated layer holds the empty array. A list entry cannot follow it, so
// the literal has to go — otherwise dsh refuses the whole profile and the only
// symptom is a harness that will not boot.
func TestDeepSeekPatchReplacesTheEmptyArray(t *testing.T) {
	got, err := dshPatchWith("# a comment\n[]\n", dshPatchBlock("/usr/local/bin/deja", false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "[]") {
		t.Errorf("the empty-array literal survived:\n%s", got)
	}
	if !strings.Contains(got, "# a comment") {
		t.Errorf("the user's own line was dropped:\n%s", got)
	}
	// An insert list, not a bare row: a patch entry addresses a row that
	// already exists, and dsh answers `entry "mcp-deja" not found` for one that
	// does not.
	if !strings.Contains(got, "- insert:") {
		t.Errorf("the block is not an insert list:\n%s", got)
	}
	if !strings.Contains(got, "@deepseek-ai/dsh-mcp-client") {
		t.Errorf("the block names no plugin:\n%s", got)
	}
}

func TestDeepSeekPatchIsRewrittenNotRepeated(t *testing.T) {
	first, err := dshPatchWith("[]\n", dshPatchBlock("/old/deja", false))
	if err != nil {
		t.Fatal(err)
	}
	second, err := dshPatchWith(first, dshPatchBlock("/new/deja", false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(second, dshBlockStart) != 1 {
		t.Errorf("the block was added twice:\n%s", second)
	}
	if strings.Contains(second, "/old/deja") {
		t.Errorf("the previous path survived a re-install:\n%s", second)
	}
}

func TestInstallDeepSeekWritesTheHomeLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	r, err := installDeepSeekMCP("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".dsh", "cordis.patch.yml"); r.Path != want {
		t.Fatalf("wrote %q, want the home layer %q", r.Path, want)
	}
	body, err := os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "serverName: deja") {
		t.Errorf("layer does not configure the server:\n%s", body)
	}

	// Uninstall leaves the file loadable rather than empty: a patch file with
	// nothing in it fails the profile load.
	if _, ierr := installDeepSeekMCP("/usr/local/bin/deja", true); ierr != nil {
		t.Fatal(ierr)
	}
	body, err = os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "mcp-deja") {
		t.Errorf("uninstall left the entry behind:\n%s", body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("uninstall left %q, want the empty array", strings.TrimSpace(string(body)))
	}
}

// dsh registers slash commands in code, so /deja is a plugin file the profile
// row names by absolute path. Both the shape of that file and the order of the
// two writes were learnt by running dsh: a row naming a missing file, a wrong
// `inject` declaration, or `handle` where it wants `handler` each fail the
// whole profile load rather than skipping the plugin.
func TestInstallDeepSeekWritesTheCommandPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, ".dsh", "plugins", "deja", "command.js")
	body, err := os.ReadFile(plugin)
	if err != nil {
		t.Fatalf("no command plugin: %v", err)
	}
	js := string(body)
	if !strings.Contains(js, "apply.inject") {
		t.Errorf("the plugin declares no dependency, so ctx.commands is unreachable:\n%s", js)
	}
	if !strings.Contains(js, "async handler(") {
		t.Errorf("the command has no handler field dsh accepts:\n%s", js)
	}
	if !strings.Contains(js, `name: "deja"`) {
		t.Errorf("the command is not named deja:\n%s", js)
	}

	layer, err := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// Quoted the way the writer quotes it: a Windows path goes into the layer
	// as "C:\\Users\\…", so comparing against the raw path fails there and
	// passes everywhere else — which is how it reached CI.
	if !strings.Contains(string(layer), yamlQuote(plugin)) {
		t.Errorf("the layer does not name the plugin file:\n%s", layer)
	}

	if _, err := installDeepSeekMCP("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(plugin)); !os.IsNotExist(err) {
		t.Errorf("uninstall left the plugin behind: %v", err)
	}
}

// Recall has to arrive through `ctx.systemPrompt.context`, the seam the host
// evaluates on every assembly. A middleware on `agent/pre-step` also loads,
// also completes the turn, and never reaches the request: a later listener in
// that waterfall rebuilds its answer from the payload and drops the added
// message. That was verified by reading what dsh 0.1.1-rc.2 actually sent.
func TestInstallDeepSeekWritesTheAutoRecallPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, ".dsh", "plugins", "deja", "auto.js")
	body, err := os.ReadFile(plugin)
	if err != nil {
		t.Fatalf("no auto-recall plugin: %v", err)
	}
	js := string(body)
	if !strings.Contains(js, "ctx.systemPrompt.context(") {
		t.Errorf("the plugin uses no seam whose text reaches the request:\n%s", js)
	}
	// The one pre-step listener waits for a compaction capture and hands the
	// decision back as it came.
	if parts := strings.Split(js, `ctx.on("agent/pre-step"`); len(parts) > 2 {
		t.Errorf("more than one pre-step listener:\n%s", js)
	} else if len(parts) == 2 {
		handler, _, _ := strings.Cut(parts[1], "});")
		if strings.Contains(handler, "messages") || !strings.Contains(handler, "return decision;") {
			t.Errorf("the plugin injects where the addition is silently dropped:\n%s", handler)
		}
	}
	if !strings.Contains(js, `apply.inject = ["systemPrompt"]`) {
		t.Errorf("the service is not declared, so the plugin never applies:\n%s", js)
	}
	if !strings.Contains(js, `"agent/inbox/spliced"`) {
		t.Errorf("at assembly time the prompt is only in the inbox splice, and this plugin never reads it:\n%s", js)
	}
	if !strings.Contains(js, `askHook("hook-prompt"`) {
		t.Errorf("the plugin never asks deja for a block:\n%s", js)
	}
	if !strings.Contains(js, "prompt !== asked") {
		t.Errorf("no guard on the cached prompt, so deja runs again every assembly:\n%s", js)
	}
	if strings.Contains(js, `kind: "plugin"`) {
		t.Errorf("the notice wraps its source in the retired v3 plugin kind, which dsh v4 refuses at append time:\n%s", js)
	}
	if !strings.Contains(js, `kind: "plugin:deja"`) {
		t.Errorf("the notice does not carry the producer-owned v4 kind, what dsh's own migration assigns an unmapped plugin:\n%s", js)
	}

	layer, err := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(layer), yamlQuote(plugin)) {
		t.Errorf("the layer does not name the auto-recall plugin:\n%s", layer)
	}
}

// dsh refuses to boot a profile whose plugin list names a file that is not
// there, so the plain target must leave neither the row nor a stale plugin
// behind when someone drops back to it from --auto.
func TestInstallDeepSeekWithoutAutoLeavesNoDanglingPluginRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, ".dsh", "plugins", "deja", "auto.js")
	if _, err := os.Stat(plugin); !os.IsNotExist(err) {
		t.Errorf("auto-recall plugin survived an install without --auto: %v", err)
	}
	layer, err := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(layer), yamlQuote(plugin)) {
		t.Errorf("the layer still names a plugin nobody wrote:\n%s", layer)
	}
	if !strings.Contains(string(layer), yamlQuote(filepath.Join(home, ".dsh", "plugins", "deja", "command.js"))) {
		t.Errorf("dropping --auto took the command plugin with it:\n%s", layer)
	}
}

// Uninstalling on a machine that never had deja must not create the file.
func TestUninstallDeepSeekWithoutALayerWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	r, err := installDeepSeekMCP("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", r.Action)
	}
	if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
		t.Errorf("uninstall created %s", r.Path)
	}
}

// dsh runs a `tools/post-execute` waterfall on every tool result, and an
// accept decision's additionalContexts reach the model on the next step. The
// plugin puts hook-tool's line there after a read, edit or write, and
// hook-tool-after's after a bash that exited non-zero, the way pi's extension
// does on tool_result (#4293).
func TestDeepSeekAutoNotesTheFileAndTheFailedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s %s\\n' \"$1\" \"$2\" \"$in\" >> " + calls + "\n" +
		"case \"$1\" in hook-tool) printf 'NOTE about the file' ;; hook-tool-after) printf 'FIX seen before' ;; esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "auto.js")
	if werr := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import plugin from "` + plugin + `";
const listeners = {};
plugin({ systemPrompt: { context: () => {} }, on: (name, fn) => { listeners[name] = fn } });
const post = listeners["tools/post-execute"];
if (!post) { console.log("NOLISTENER"); process.exit(0) }
const agent = { sessionId: "sess-A", session: { header: { cwd: "/w/proj" }, events: [] } };
const accept = async () => ({ kind: "accept" });
const text = (t) => ({ content: [{ type: "text", text: t }] });
const show = (d) => console.log(JSON.stringify((d.additionalContexts || []).map((m) => [m.role, m.source && m.source.kind, m.content.map((c) => c.text).join("")])));
show(await post({ name: "read", arguments: { file_path: "/w/proj/retry.go" }, agent }, text("1\tpackage retry"), accept));
show(await post({ name: "bash", arguments: { command: "go test ./..." }, agent }, text("FAIL retry\n[exit code: 1]"), accept));
show(await post({ name: "bash", arguments: { command: "ls" }, agent }, text("retry.go"), accept));
show(await post({ name: "edit", arguments: { file_path: "/w/proj/retry.go" }, agent }, text("ok"), async () => ({ kind: "block", feedback: [] })));
show(await post({ name: "bash", arguments: { command: "cat build.log" }, agent }, text("old run:\n[exit code: 1]\nall green now"), accept));
show(await post({ name: "bash", arguments: { command: "make serve" }, agent }, text("listening\n[killed by signal: SIGKILL]"), accept));
show(await post({ name: "pwsh", arguments: { command: "dotnet build" }, agent }, text("error CS1002\n[exit code: 1]"), accept));
show(await post({ name: "str_replace_editor", arguments: { command: "str_replace", path: "/w/proj/retry.go" }, agent }, text("edited"), accept));
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
	if lines[0] == "NOLISTENER" {
		t.Fatal("auto.js registers no tools/post-execute listener, so dsh hears nothing at the point of action")
	}
	if len(lines) != 8 {
		t.Fatalf("driver said %q", out)
	}
	if lines[0] != `[["user","plugin:deja","NOTE about the file"]]` {
		t.Errorf("a read did not carry the file's note: %s", lines[0])
	}
	if lines[1] != `[["user","plugin:deja","FIX seen before"]]` {
		t.Errorf("a failed bash did not carry the earlier fix: %s", lines[1])
	}
	if lines[2] != `[]` {
		t.Errorf("a bash that exited 0 was answered: %s", lines[2])
	}
	if lines[3] != `[]` {
		t.Errorf("a blocked call got a note: %s", lines[3])
	}
	// The marker is dsh's only when it ends the result, as dsh's own
	// parser reads it (dsh-shell parseExitStatus): a log that quotes one
	// mid-text passed.
	if lines[4] != `[]` {
		t.Errorf("a command that exited 0 with a marker in its output was answered: %s", lines[4])
	}
	if lines[5] != `[["user","plugin:deja","FIX seen before"]]` {
		t.Errorf("a command killed by a signal did not carry the earlier fix: %s", lines[5])
	}
	// dsh's other shell and its other editor (#4293).
	if lines[6] != `[["user","plugin:deja","FIX seen before"]]` {
		t.Errorf("a failed pwsh command did not carry the earlier fix: %s", lines[6])
	}
	if lines[7] != `[["user","plugin:deja","NOTE about the file"]]` {
		t.Errorf("a str_replace_editor edit did not carry the file's note: %s", lines[7])
	}
	asked, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	got := string(asked)
	for _, want := range []string{
		`hook-tool --plain {"tool_name":"read","tool_input":{"file_path":"/w/proj/retry.go"},"session_id":"sess-A","cwd":"/w/proj"}`,
		`hook-tool-after --plain {"tool_name":"bash","tool_input":{"command":"go test ./..."},"tool_response":"FAIL retry","session_id":"sess-A","cwd":"/w/proj"}`,
		`hook-tool-after --plain {"tool_name":"bash","tool_input":{"command":"make serve"},"tool_response":"listening","session_id":"sess-A","cwd":"/w/proj"}`,
		`hook-tool-after --plain {"tool_name":"powershell","tool_input":{"command":"dotnet build"},"tool_response":"error CS1002","session_id":"sess-A","cwd":"/w/proj"}`,
		`hook-tool --plain {"tool_name":"edit","tool_input":{"file_path":"/w/proj/retry.go"},"session_id":"sess-A","cwd":"/w/proj"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("deja was not asked %s\ncalls:\n%s", want, got)
		}
	}
	if strings.Count(got, "hook-tool") != 5 {
		t.Errorf("deja was asked for a call that needs nothing:\n%s", got)
	}
}

// The note is asked for while dsh goes on: a synchronous spawn held the whole
// event loop, the web profile's other sessions included, for as long as deja
// took, up to its ten-second timeout.
func TestDeepSeekAutoToolNoteDoesNotBlockTheHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	if werr := os.WriteFile(stub, []byte("#!/bin/sh\ncat >/dev/null\nsleep 0.5\nprintf 'NOTE'\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "auto.js")
	if werr := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import plugin from "` + plugin + `";
const listeners = {};
plugin({ systemPrompt: { context: () => {} }, on: (name, fn) => { listeners[name] = fn } });
let ticks = 0;
const timer = setInterval(() => ticks++, 10);
const d = await listeners["tools/post-execute"]({ name: "read", arguments: { file_path: "/w/a.go" }, agent: {} }, { content: [] }, async () => ({ kind: "accept" }));
clearInterval(timer);
console.log(ticks + " " + (d.additionalContexts || []).length);
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	var ticks, notes int
	if _, err := fmt.Sscan(string(out), &ticks, &notes); err != nil {
		t.Fatalf("driver said %q", out)
	}
	if notes != 1 {
		t.Errorf("the note did not arrive: %q", out)
	}
	if ticks < 10 {
		t.Errorf("the host's timers ran %d times in half a second: the call blocks the event loop", ticks)
	}
}

// dsh's generated layer is a comment and the empty array. Uninstall dropped
// the array and kept the comment, and a YAML file that is only a comment
// parses as null, which dsh refuses: "must be a top-level YAML array of loader
// patch entries" (#3271).
func TestUninstallDeepSeekKeepsTheLayerLoadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	path := filepath.Join(home, ".dsh", "cordis.patch.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "# dsh profile root — an empty entry list. Edit cordis.patch.yml, not this file.\n[]\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if lines[0] != strings.Split(seed, "\n")[0] {
		t.Errorf("the comment did not survive:\n%s", got)
	}
	if lines[len(lines)-1] != "[]" {
		t.Errorf("the layer has no array after uninstall, dsh cannot load it:\n%s", got)
	}
}

// The generated plugins are ES modules, and Node takes a file's module type
// from the nearest package.json above it. A home directory whose own
// package.json declares CommonJS made dsh refuse both plugins ("Failed to load
// the ES module"), so the plugin directory declares its own type.
func TestInstallDeepSeekDeclaresThePluginsAsModules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".dsh", "plugins", "deja")
	body, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatalf("no package.json beside the plugins: %v", err)
	}
	var pkg struct {
		Type string `json:"type"`
	}
	if uerr := json.Unmarshal(body, &pkg); uerr != nil {
		t.Fatalf("package.json: %v\n%s", uerr, body)
	}
	if pkg.Type != "module" {
		t.Errorf("package.json type = %q, want module", pkg.Type)
	}
	if !mentionsDeja(body) {
		t.Errorf("package.json is not recognisable as deja's, so an uninstall would keep its snapshot:\n%s", body)
	}

	// Installing again changes nothing, and uninstall takes the marker with
	// the directory.
	res, err := installDeepSeekAuto("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "unchanged" {
		t.Errorf("a second install reported %q, want unchanged", res.Action)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uninstall left the plugin directory: %v", err)
	}
}

// One dsh web process serves sessions from every workspace and never changes
// directory, so process.cwd() is wherever dsh was launched. The plugin has to
// ask deja about the workspace the session header names, and the same question
// asked in a second workspace is a new question rather than a cache hit.
//
// This loads the generated file in Node the way dsh does, below a package.json
// that declares CommonJS, with a stand-in deja that records what it was asked.
func TestDeepSeekAutoPluginAsksAboutTheSessionsWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	if werr := os.WriteFile(filepath.Join(home, "package.json"), []byte(`{"type": "commonjs"}`+"\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	calls := filepath.Join(home, "calls.jsonl")
	fake := filepath.Join(home, "deja")
	if werr := os.WriteFile(fake, []byte("#!/bin/sh\ncat >> '"+calls+"'\necho >> '"+calls+"'\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	if _, ierr := installDeepSeekAuto(fake, false); ierr != nil {
		t.Fatal(ierr)
	}

	const drive = `
const { default: apply } = await import(process.argv[1]);
const contexts = {};
apply({ systemPrompt: { context: (c) => { contexts[c.name] = c; } } });
const agent = (id, cwd) => ({
  sessionId: id,
  session: {
    id,
    header: { id, cwd },
    events: [{ type: "user/message", data: { source: { kind: "user" }, content: [{ type: "text", text: "where does the shard map live" }] } }],
  },
});
for (const [id, cwd] of [["s1", "/work/alpha"], ["s2", "/work/beta"]]) {
  contexts["deja:project"].text({ agent: agent(id, cwd) });
  contexts["deja:recall"].text({ agent: agent(id, cwd) });
}
`
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", drive, "file://"+dshAutoPath())
	cmd.Dir = home
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("node could not run the plugin: %v\n%s", cerr, out)
	}

	body, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("deja was never asked: %v", err)
	}
	var got []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
		var payload struct {
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("payload %q: %v", line, err)
		}
		got = append(got, payload.CWD)
	}
	// The digest and the recall for each session, in that order.
	want := []string{"/work/alpha", "/work/alpha", "/work/beta", "/work/beta"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("deja was asked about %v, want %v", got, want)
	}
}

// A newcomer whose only harness store is behind a permission wall was told by
// the very first command — the install — that the machine held no history at
// all. Only doctor, three commands later, named the wall.
func TestInstallNamesADeniedStoreInsteadOfClaimingNoHistory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	hermeticEnv(t)
	if err := os.MkdirAll(sourcesClaudeConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(root, "project")
	writeClaudeFixture(t, filepath.Join(proj, "session.jsonl"), "session", []string{
		`{"type":"user","sessionId":"session","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"history"}}`,
	})
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	out, err := captureRunStderr(t, "install", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no agent history detected") {
		t.Errorf("install called an unreadable store an empty machine:\n%s", out)
	}
	if !strings.Contains(out, "permission denied") || !strings.Contains(out, "deja doctor") {
		t.Errorf("install did not name the permission wall or where to see it:\n%s", out)
	}
}

// The hint under the install logo said "index 0 agent stores" in the same
// state: a zero plus an instruction that would change nothing.
func TestInstallHintDoesNotOfferToIndexZeroStores(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	hermeticEnv(t)
	if err := os.MkdirAll(sourcesClaudeConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "project", "session.jsonl"), "session", []string{
		`{"type":"user","sessionId":"session","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"history"}}`,
	})
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	got := installIndexHint(t.TempDir())
	if strings.Contains(got, "index 0 agent stores") {
		t.Errorf("hint offers to index nothing: %q", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Errorf("hint does not name the permission wall: %q", got)
	}
}

// docInstallTarget finds every `deja install <target>` the site and the READMEs
// print. A guide page is where someone gets the command from, so a name there
// that the binary refuses is a broken instruction: memory-for-dsh.html said
// `deja install dsh-auto` and the only names that resolved were `deepseek` and
// `deepseek-auto`.
//
// Checked against the name list rather than by installing: `installTarget`
// writes files, and on Windows several targets write under APPDATA, which
// t.Setenv("HOME") does not move — a run of all of them left state that two
// other tests then read as their own.
var docInstallTarget = regexp.MustCompile(`deja install ([a-z][a-z0-9-]*)`)

func TestEveryInstallTargetTheDocsPrintResolves(t *testing.T) {
	root := filepath.Join("..", "..")
	named := map[string][]string{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if fi.IsDir() {
			switch fi.Name() {
			case ".git", ".github", "node_modules", "vendor", "cmd", "internal", "fixtures", "tools", "scripts":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".md", ".html", ".txt":
		default:
			return nil
		}
		// The changelog records what the command was called at the time.
		if strings.Contains(filepath.Base(p), "CHANGELOG") {
			return nil
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // the walk is over the repo's own docs
		if rerr != nil {
			return nil //nolint:nilerr // unreadable files are skipped
		}
		rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(p, root), string(filepath.Separator)))
		for _, m := range docInstallTarget.FindAllStringSubmatch(string(b), -1) {
			named[m[1]] = append(named[m[1]], rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(named) < 10 {
		t.Fatalf("found only %d install targets in the docs — the scan is broken", len(named))
	}

	known := map[string]bool{}
	for _, n := range installTargetNames() {
		known[n] = true
	}
	for alias := range installTargetAliases {
		known[alias] = true
	}
	// A target deja offers only where it can schedule something is still a
	// real target, and the page naming it is right on the platforms that
	// have a service manager. Without this the Windows leg refused
	// `sync-timer`, which sync-across-machines.html correctly prints.
	if !syncTimerSchedulable(runtime.GOOS) {
		known["sync-timer"] = true
	}

	names := make([]string, 0, len(named))
	for n := range named {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		if !known[name] {
			t.Errorf("%s prints `deja install %s`, and there is no such target",
				strings.Join(dedupe(named[name]), ", "), name)
		}
	}
}

// Every alias has to name a real target, or it resolves to a switch label that
// is not there and the refusal is worse than the one it was added to fix.
func TestEveryInstallAliasNamesARealTarget(t *testing.T) {
	isTarget := map[string]bool{}
	for _, n := range installTargetNames() {
		isTarget[n] = true
	}
	for alias, canonical := range installTargetAliases {
		if !isTarget[canonical] {
			t.Errorf("alias %q points at %q, which is not a target", alias, canonical)
		}
		if isTarget[alias] {
			t.Errorf("alias %q is also a listed target — one of the two is wrong", alias)
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Hermes lists a memory provider at `hermes memory setup` when a directory
// under ~/.hermes/plugins/ has an __init__.py that mentions MemoryProvider;
// that directory must not be the hook plugin's, which the general loader
// would then skip as "exclusive" and lose the hook and /deja with it.
func TestInstallHermesWritesAMemoryProviderBesideTheHookPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	if _, err := installHermesAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(home, ".hermes", "plugins", "deja-memory")
	manifest, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	// `hermes memory setup` runs the dependency check before activating.
	for _, want := range []string{"name: deja-memory", "check: \"deja --version\"", "brew install deja-vu"} {
		if !strings.Contains(string(manifest), want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
	code, err := os.ReadFile(filepath.Join(dir, "__init__.py"))
	if err != nil {
		t.Fatalf("provider code missing: %v", err)
	}
	src := string(code)
	for _, want := range []string{
		"class DejaMemoryProvider(MemoryProvider)", // what the discovery scan looks for
		"ctx.register_memory_provider(",
		`"deja_recall"`, `"deja_fix"`, `"deja_blame"`,
		"hook-context", "hook-prompt", // the same recall the hooks inject elsewhere
		`"/bin/deja"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("provider missing %q:\n%s", want, src)
		}
	}
	// The hook plugin must not mention MemoryProvider anywhere, or Hermes
	// reclassifies it and stops loading its hook.
	hook, err := os.ReadFile(filepath.Join(home, ".hermes", "plugins", "deja", "__init__.py"))
	if err != nil {
		t.Fatalf("hook plugin missing: %v", err)
	}
	// The Python construct, not the bare word: the launcher path the plugin
	// now names carries the test's own directory, and this test's name has
	// "MemoryProvider" in it — so the loose check failed on its own name
	// (#3682).
	for _, forbidden := range []string{"(MemoryProvider)", "import MemoryProvider", "register_memory_provider("} {
		if strings.Contains(string(hook), forbidden) {
			t.Fatalf("hook plugin carries %q, which turns it into an exclusive plugin the general loader skips", forbidden)
		}
	}
	// With the provider active, the hook stays silent: the provider injects
	// the same recall before each turn.
	for _, want := range []string{`"memory", "provider") != "deja-memory"`, `"deja-memory", "__init__.py"`} {
		if !strings.Contains(string(hook), want) {
			t.Fatalf("hook plugin does not step aside for a provider that exists (%q):\n%s", want, hook)
		}
	}
	if _, err := installHermesAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the provider directory: %v", err)
	}
}

// A MEMORY.md entry is often a bullet ("- user prefers X"). Passed bare, deja
// read it as a flag and the note was lost without a word.
func TestHermesMemoryWritePassesDashTextAfterSeparator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub deja is a shebang script")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv.json")
	stub := filepath.Join(dir, "deja")
	stubSrc := "#!" + py + "\nimport json, sys\njson.dump(sys.argv[1:], open(" + strconv.Quote(argvFile) + ", 'w'))\n"
	if werr := os.WriteFile(stub, []byte(stubSrc), 0o755); werr != nil {
		t.Fatal(werr)
	}
	// Just enough of Hermes for the provider to import.
	if merr := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); merr != nil {
		t.Fatal(merr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "agent", "__init__.py"), nil, 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "agent", "memory_provider.py"), []byte("class MemoryProvider:\n    pass\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "provider.py"), []byte(hermesMemoryPy(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	run := "import provider\nprovider.DejaMemoryProvider().on_memory_write('add', 'user', '- prefers tabs')\n"
	cmd := exec.CommandContext(t.Context(), py, "-c", run)
	cmd.Dir = dir
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("provider: %v\n%s", cerr, out)
	}
	got, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("stub deja was not called: %v", err)
	}
	want := `["remember", "--tag", "hermes-user", "--", "- prefers tabs"]`
	if string(got) != want {
		t.Fatalf("argv = %s, want %s", got, want)
	}
}

// The generated Python has to at least parse; a template slip here ships to
// every Hermes user as a provider that fails to import.
func TestHermesGeneratedPythonCompiles(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	for name, src := range map[string]string{
		"provider.py": hermesMemoryPy(`C:\Program Files\deja "x".exe`),
		"hook.py":     hermesPluginPy(`C:\Program Files\deja "x".exe`),
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.CommandContext(t.Context(), py, "-m", "py_compile", path).CombinedOutput(); err != nil {
			t.Fatalf("%s does not compile: %v\n%s", name, err, out)
		}
	}
}

// The model-facing tools hand back what the command printed, and fix and blame
// print for a person: no frame, planted tags intact (#5051). Each asks for the
// framed form. deja_recall asks for the recall page, which frames itself and
// stays within the MCP tool's budget, where search grew with every hit (#5568).
func TestHermesToolsAskForFramedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub deja is a shebang script")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv.json")
	stub := filepath.Join(dir, "deja")
	stubSrc := "#!" + py + "\nimport json, sys\nopen(" + strconv.Quote(argvFile) + ", 'a').write(json.dumps(sys.argv[1:]) + '\\n')\n"
	if werr := os.WriteFile(stub, []byte(stubSrc), 0o755); werr != nil {
		t.Fatal(werr)
	}
	if merr := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); merr != nil {
		t.Fatal(merr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "agent", "__init__.py"), nil, 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "agent", "memory_provider.py"), []byte("class MemoryProvider:\n    pass\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "provider.py"), []byte(hermesMemoryPy(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	run := "import provider\np = provider.DejaMemoryProvider()\n" +
		"p.handle_tool_call('deja_recall', {'query': 'q'})\n" +
		"p.handle_tool_call('deja_recall', {'query': '-q', 'offset': 3})\n" +
		"p.handle_tool_call('deja_fix', {'error': 'e'})\n" +
		"p.handle_tool_call('deja_blame', {'path': 'a.go'})\n"
	cmd := exec.CommandContext(t.Context(), py, "-c", run)
	cmd.Dir = dir
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("provider: %v\n%s", cerr, out)
	}
	got, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("stub deja was not called: %v", err)
	}
	want := `["recall", "q"]` + "\n" + `["recall", "--offset", "3", "--", "-q"]` + "\n" + `["--framed", "fix", "e"]` + "\n" + `["--framed", "blame", "a.go"]` + "\n"
	if string(got) != want {
		t.Fatalf("argv =\n%s\nwant\n%s", got, want)
	}
}

func TestHermesToolsRunInSessionWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub deja is a shebang script")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	cwdFile := filepath.Join(dir, "cwd.txt")
	stub := filepath.Join(dir, "deja")
	stubSrc := "#!" + py + "\nimport os\nopen(" + strconv.Quote(cwdFile) + ", 'a').write(os.path.realpath(os.getcwd()) + '\\n')\n"
	if werr := os.WriteFile(stub, []byte(stubSrc), 0o755); werr != nil {
		t.Fatal(werr)
	}
	ws := filepath.Join(dir, "workspace")
	if merr := os.MkdirAll(ws, 0o755); merr != nil {
		t.Fatal(merr)
	}
	realWS, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if merr := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); merr != nil {
		t.Fatal(merr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "agent", "__init__.py"), nil, 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "agent", "memory_provider.py"), []byte("class MemoryProvider:\n    pass\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "provider.py"), []byte(hermesMemoryPy(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	run := "import provider\np = provider.DejaMemoryProvider()\n" +
		"p.initialize('s1', workspace_dir=" + strconv.Quote(ws) + ")\n" +
		"p.handle_tool_call('deja_recall', {'query': 'q'})\n" +
		"p.handle_tool_call('deja_fix', {'error': 'e'})\n" +
		"p.handle_tool_call('deja_blame', {'path': 'a.go'})\n"
	cmd := exec.CommandContext(t.Context(), py, "-c", run)
	cmd.Dir = dir
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("provider: %v\n%s", cerr, out)
	}
	got, err := os.ReadFile(cwdFile)
	if err != nil {
		t.Fatalf("stub deja was not called: %v", err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(got)), "\n") {
		if line != realWS {
			t.Errorf("tool ran in %q, want session workspace %q", line, realWS)
		}
	}
}

// runMainEnv makes the test binary run deja's main(), so a host plugin under
// test can spawn the real hooks without a separate build.
const runMainEnv = "DEJA_TEST_RUN_MAIN"

// hermesNotesStand writes just enough of Hermes for the provider to import,
// and a deja that answers --plain with the model's text and the JSON form with
// the same text plus a line for the person. withStatus=false is Hermes 0.17,
// which has no RecallStatus.
func hermesNotesStand(t *testing.T, withStatus bool) (dir, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub deja is a shebang script")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir = t.TempDir()
	calls = filepath.Join(dir, "calls")
	stub := filepath.Join(dir, "deja")
	src := "#!" + py + `
import json, os, sys
args = sys.argv[1:]
open(` + strconv.Quote(calls) + `, "a").write(" ".join(args) + "\n")
quiet = os.path.exists(` + strconv.Quote(filepath.Join(dir, "quiet")) + `)
context = "" if quiet else "<deja-recall>\n- **zorbit** dec-0001 · retry in-process, no queue\n</deja-recall>"
line = {"hook-prompt": "deja-vu — from Codex CLI, 3d ago: \"ledger webhook retry\" (via ledger, webhook)",
        "hook-context": "deja recalled 1 prior session from this project · 1.2 KB of context\ndeja indexed 15 sessions from 2 agents on this machine"}.get(args[0], "")
if quiet and args[0] == "hook-context":
    line = "deja is indexing your history — recall comes online when it finishes\n" + line
if "--plain" in args:
    if context:
        print(context)
else:
    out = {"hookSpecificOutput": {"hookEventName": "UserPromptSubmit"}}
    if context:
        out["hookSpecificOutput"]["additionalContext"] = context
    if line:
        out["systemMessage"] = line
    print(json.dumps(out))
`
	if err := os.WriteFile(stub, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	mp := "class MemoryProvider:\n    pass\n"
	if withStatus {
		mp += "\nfrom dataclasses import dataclass\n\n\n@dataclass\nclass RecallStatus:\n    provider_label: str\n    count: int\n    glyph: str = \"G\"\n"
	}
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "__init__.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "memory_provider.py"), []byte(mp), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provider.py"), []byte(hermesMemoryPy(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, calls
}

func runHermesProvider(t *testing.T, dir, script string) string {
	t.Helper()
	// What the CLI hands a provider: the agent's own _emit_status, whose
	// agent hermes -z and chat -Q mark suppress_status_output.
	prelude := "import provider\n" +
		"class Agent:\n" +
		"    suppress_status_output = False\n" +
		"    def emit(self, message):\n" +
		"        pass\n" +
		"agent = Agent()\n"
	cmd := exec.CommandContext(t.Context(), "python3", "-c", prelude+script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("provider: %v\n%s", err, out)
	}
	return string(out)
}

// Hermes shows the person one line per recall, "🧠 deja — recalled N
// memories", and deja asked for --plain, so its receipt and notes never
// reached anyone in Hermes. One run per hook now gives the model the text
// --plain printed and puts deja's lines on that indicator.
func TestHermesProviderPutsDejasLinesOnTheRecallIndicator(t *testing.T) {
	dir, calls := hermesNotesStand(t, true)
	out := runHermesProvider(t, dir, `
p = provider.DejaMemoryProvider()
p.initialize("s1", platform="cli", status_callback=agent.emit)
text = p.prefetch("the ledger webhook times out again")
assert text == "<deja-recall>\n- **zorbit** dec-0001 · retry in-process, no queue\n</deja-recall>\n\n<deja-recall>\n- **zorbit** dec-0001 · retry in-process, no queue\n</deja-recall>", repr(text)
print(repr(p.recall_status().provider_label))
text = p.prefetch("and the backoff cap?")
assert text == "<deja-recall>\n- **zorbit** dec-0001 · retry in-process, no queue\n</deja-recall>", repr(text)
print(repr(p.recall_status().provider_label))
`)
	want := []string{
		`'deja · recalled 1 prior session from this project · 1.2 KB of context\n   indexed 15 sessions from 2 agents on this machine\n   from Codex CLI, 3d ago: "ledger webhook retry" (via ledger, webhook)\n  '`,
		`'deja · from Codex CLI, 3d ago: "ledger webhook retry" (via ledger, webhook)'`,
	}
	if got := strings.Split(strings.TrimSpace(out), "\n"); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("indicator labels:\n%s\nwant:\n%s", out, strings.Join(want, "\n"))
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "--plain") || strings.Count(string(b), "\n") != 3 {
		t.Fatalf("want one JSON run per hook, got:\n%s", b)
	}
}

// Where nothing shows the indicator, deja is asked for --plain: it marks a
// note shown when it prints it, and a note spent there is never seen.
func TestHermesProviderKeepsPlainWhereNobodySeesTheIndicator(t *testing.T) {
	for _, tc := range []struct {
		name       string
		withStatus bool
		platform   string
		setup      string
	}{
		{"hermes 0.17 has no RecallStatus", false, "cli", ""},
		{"the API server drops status lines", true, "api_server", ""},
		{"the TUI only flashes them in its status bar", true, "tui", ""},
		{"a gateway chat may be a group other people read", true, "telegram", ""},
		// hermes -z and hermes chat -Q run as "cli" and set the flag after
		// the provider was initialized; the line is never printed.
		{"a one-shot run suppresses status output", true, "cli", "agent.suppress_status_output = True"},
		{"no agent to ask and stdout is not a terminal", true, "cli", "p._status_callback = None"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, calls := hermesNotesStand(t, tc.withStatus)
			out := runHermesProvider(t, dir, `
p = provider.DejaMemoryProvider()
p.initialize("s1", platform="`+tc.platform+`", status_callback=agent.emit)
`+tc.setup+`
text = p.prefetch("the ledger webhook times out again")
assert "<deja-recall>" in text, repr(text)
print(p.recall_status() is None or p.recall_status().provider_label)
`)
			if strings.TrimSpace(out) != "True" && strings.TrimSpace(out) != "deja" {
				t.Errorf("label = %q, want none or bare deja", out)
			}
			b, _ := os.ReadFile(calls)
			if strings.Count(string(b), "--plain") != 2 {
				t.Fatalf("want both hooks asked for --plain, got:\n%s", b)
			}
		})
	}
}

// Hermes prints the indicator only after a prefetch that returned text. A
// line deja sent with nothing for the model waits for the next indicator
// instead of going nowhere.
func TestHermesProviderHoldsALineThatCameWithoutRecall(t *testing.T) {
	dir, _ := hermesNotesStand(t, true)
	quiet := filepath.Join(dir, "quiet")
	out := runHermesProvider(t, dir, `
import os
open(`+strconv.Quote(quiet)+`, "w").close()
p = provider.DejaMemoryProvider()
p.initialize("s1", platform="cli", status_callback=agent.emit)
assert p.prefetch("hello there friend") == ""
assert p.recall_status() is None
os.remove(`+strconv.Quote(quiet)+`)
p.prefetch("the ledger webhook times out again")
print(repr(p.recall_status().provider_label))
`)
	if !strings.Contains(out, "indexed 15 sessions") || strings.Count(out, `"ledger webhook retry"`) != 2 {
		t.Fatalf("held lines did not reach the next indicator:\n%s", out)
	}
	// "indexing your history" was true when it was said; a turn later the
	// build may be done, so it is not held.
	if strings.Contains(out, "indexing your history") {
		t.Fatalf("a held build-progress line was shown a turn late:\n%s", out)
	}
}

// /new and /reset start another conversation: lines held for the one that
// ended must not open it.
func TestHermesProviderDropsHeldLinesOnANewConversation(t *testing.T) {
	dir, _ := hermesNotesStand(t, true)
	quiet := filepath.Join(dir, "quiet")
	out := runHermesProvider(t, dir, `
import os
open(`+strconv.Quote(quiet)+`, "w").close()
p = provider.DejaMemoryProvider()
p.initialize("s1", platform="cli", status_callback=agent.emit)
assert p.prefetch("hello there friend") == ""
p.on_session_switch("s2", parent_session_id="s1", reset=True, reason="new")
os.remove(`+strconv.Quote(quiet)+`)
p._first_turn = False
p.prefetch("the ledger webhook times out again")
print(repr(p.recall_status().provider_label))
`)
	if strings.Contains(out, "indexed 15 sessions") || strings.Count(out, `"ledger webhook retry"`) != 1 {
		t.Fatalf("the new conversation opened on the last one's lines:\n%s", out)
	}
}

// The provider's model text with the real deja behind it, JSON form against
// --plain, on the payloads the provider sends: hook-context with session_id
// and cwd, hook-prompt with prompt and session_id and no cwd. Each form runs
// on a fresh store, since the hooks keep once-only state.
func TestHermesProviderModelTextIsWhatPlainPrints(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the provider runs under python3 with a POSIX stub tree")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	// This test binary is deja: TestMain runs main() under runMainEnv.
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, platform string) string {
		t.Helper()
		withStatsStores(t)
		t.Setenv(runMainEnv, "1")
		root := os.Getenv("DEJA_CLAUDE_ROOT")
		when := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "beta", "drain.jsonl"), "drainpass", []string{
			`{"type":"user","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"user","content":"how should the zorbit queue drain on deploy"}}`,
			`{"type":"assistant","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"assistant","content":[{"type":"text","text":"we settled on draining the zorbit queue with --grace 40 before each deploy, because 10 dropped jobs"}]}}`,
		})
		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		for _, d := range []string{"agent", filepath.Join("tmp", "beta")} {
			if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		mp := "from dataclasses import dataclass\n\n\nclass MemoryProvider:\n    pass\n\n\n@dataclass\nclass RecallStatus:\n    provider_label: str\n    count: int\n"
		files := map[string]string{
			filepath.Join("agent", "__init__.py"):        "",
			filepath.Join("agent", "memory_provider.py"): mp,
			"provider.py": hermesMemoryPy(bin),
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		script := "import sys\nsys.path.insert(0, " + strconv.Quote(dir) + ")\nimport provider\n" +
			"class Agent:\n    suppress_status_output = False\n    def emit(self, m):\n        pass\n" +
			"p = provider.DejaMemoryProvider()\n" +
			"p.initialize('parity-1', platform=" + strconv.Quote(platform) + ", status_callback=Agent().emit)\n" +
			"sys.stdout.write(p.prefetch('how do we drain the zorbit queue'))\n"
		cmd := exec.CommandContext(t.Context(), "python3", "-c", script)
		cmd.Dir = filepath.Join(dir, "tmp", "beta")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("provider: %v\n%s", err, out)
		}
		return string(out)
	}
	var plain, js string
	t.Run("plain", func(t *testing.T) { plain = run(t, "tui") })
	t.Run("json", func(t *testing.T) { js = run(t, "cli") })
	if !strings.Contains(plain, "zorbit") {
		t.Fatalf("the stand never spoke, so there is nothing to compare:\n%s", plain)
	}
	if js != plain {
		t.Errorf("the model would read something else:\n--plain:\n%q\n--json:\n%q", plain, js)
	}
}

// The provider deja writes has to load on the Hermes it finds, and the hook
// has to know whether it did. On 0.17 the generated file imported a name that
// build does not define, so the provider never loaded — while the hook stood
// down because the config named it (#3390).
func TestHermesProviderLoadsWithoutRecallStatus(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", home)
	if _, err := installHermesMemoryProvider("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "plugins", "deja-memory", "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "import MemoryProvider, RecallStatus") {
		t.Error("the provider imports RecallStatus unconditionally; a build without it cannot load the file at all")
	}
	if !strings.Contains(src, "RecallStatus = None") {
		t.Error("nothing stands in for RecallStatus on a build that lacks it")
	}
}

// And the hook asks whether the provider loaded rather than whether the config
// names it, or it stands down for a provider that answers nothing.
func TestHermesHookAsksTheLoaderNotTheConfig(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", home)
	if _, err := installHermesPlugin("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "plugins", "deja", "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "load_memory_provider") {
		t.Error("_provider_active reads the config alone; a provider that failed to load still silences the hook")
	}
}

// Hermes 0.17 has MemoryProvider and no RecallStatus, so the provider deja
// writes failed to import there: Hermes reported it unavailable while the hook
// had already stood down for it, and the turn got memory from neither (#3202).
func TestTheProviderImportsOnAHermesWithoutRecallStatus(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if merr := os.MkdirAll(agentDir, 0o755); merr != nil {
		t.Fatal(merr)
	}
	// The 0.17 interface: the base class, nothing else.
	stub := "from abc import ABC\n\n\nclass MemoryProvider(ABC):\n    pass\n"
	if werr := os.WriteFile(filepath.Join(agentDir, "memory_provider.py"), []byte(stub), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(agentDir, "__init__.py"), nil, 0o644); werr != nil {
		t.Fatal(werr)
	}
	provider := filepath.Join(root, "deja_memory.py")
	if werr := os.WriteFile(provider, []byte(hermesMemoryPy(`"/bin/deja"`)), 0o644); werr != nil {
		t.Fatal(werr)
	}

	cmd := exec.CommandContext(t.Context(), py, "-c", "import deja_memory; p = deja_memory.DejaMemoryProvider(); print(p.recall_status())")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the provider does not import on a Hermes without RecallStatus: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "None" {
		t.Errorf("recall_status() = %q, want None with nothing recalled yet", got)
	}
}

// A provider directory that exists but does not load is not a provider. The
// hook has to keep answering, or the turn has no memory at all.
func TestTheHookDoesNotStandDownForAProviderThatCannotLoad(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	root := t.TempDir()
	plugins := filepath.Join(root, "plugins")
	hookDir := filepath.Join(plugins, "deja")
	broken := filepath.Join(plugins, "deja-memory")
	for _, d := range []string{hookDir, broken} {
		if merr := os.MkdirAll(d, 0o755); merr != nil {
			t.Fatal(merr)
		}
	}
	if werr := os.WriteFile(filepath.Join(hookDir, "__init__.py"), []byte(hermesPluginPy(`"/bin/deja"`)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	// What an older Hermes gives the provider: a module that raises on import.
	if werr := os.WriteFile(filepath.Join(broken, "__init__.py"),
		[]byte("from agent.memory_provider import MemoryProvider, RecallStatus\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	// The config says deja-memory is the provider.
	cfg := filepath.Join(root, "hermes_cli")
	if merr := os.MkdirAll(cfg, 0o755); merr != nil {
		t.Fatal(merr)
	}
	if werr := os.WriteFile(filepath.Join(cfg, "__init__.py"), nil, 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(cfg, "config.py"),
		[]byte("def load_config():\n    return {}\n\n\ndef cfg_get(cfg, *keys):\n    return \"deja-memory\"\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}

	script := "import importlib.util, sys\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugins/deja/__init__.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"print(m._provider_active())\n"
	cmd := exec.CommandContext(t.Context(), py, "-c", script)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook plugin does not run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "False" {
		t.Errorf("_provider_active() = %q, want False for a provider that cannot load", got)
	}
}

// Hermes had no after-failure line: pre_llm_call runs before the turn, so the
// pair for a command that just failed waited for the next user message. The
// plugin now answers transform_tool_result, whose string replaces the result
// the model reads.
func TestHermesPluginAppendsTheFixLineToAFailedCommand(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	root := t.TempDir()
	log := filepath.Join(root, "calls")
	fake := filepath.Join(root, "deja")
	script := "#!/bin/sh\nprintf '%s|' \"$*\" >> " + log + "\ncat >> " + log + "\necho >> " + log + "\n" +
		"[ \"$1\" = hook-tool-after ] && echo '<deja-recall>ran make clean after this before</deja-recall>'\nexit 0\n"
	if werr := os.WriteFile(fake, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	dir := filepath.Join(root, "plugins", "deja")
	if merr := os.MkdirAll(dir, 0o755); merr != nil {
		t.Fatal(merr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "__init__.py"), []byte(hermesPluginPy(fake)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	probe := "import importlib.util, json\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugins/deja/__init__.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"hooks = []\n" +
		"class Ctx:\n" +
		"    def register_hook(self, name, fn): hooks.append(name)\n" +
		"    def register_command(self, *a, **k): pass\n" +
		"m.register(Ctx())\n" +
		"failed = json.dumps({'output': 'panic: sql: database is closed', 'exit_code': 2, 'error': None})\n" +
		"passed = json.dumps({'output': 'ok', 'exit_code': 0, 'error': None})\n" +
		"print(json.dumps({\n" +
		"  'hooks': hooks,\n" +
		"  'failed': m.repair(tool_name='terminal', result=failed, session_id='s1'),\n" +
		"  'passed': m.repair(tool_name='terminal', result=passed, session_id='s1'),\n" +
		"  'other': m.repair(tool_name='read_file', result=failed, session_id='s1'),\n" +
		"}))\n"
	cmd := exec.CommandContext(t.Context(), py, "-c", probe)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("plugin: %v\n%s", err, out)
	}
	var got struct {
		Hooks  []string `json:"hooks"`
		Failed *string  `json:"failed"`
		Passed *string  `json:"passed"`
		Other  *string  `json:"other"`
	}
	if uerr := json.Unmarshal(out, &got); uerr != nil {
		t.Fatalf("probe output %q: %v", out, uerr)
	}
	if !strings.Contains(strings.Join(got.Hooks, ","), "transform_tool_result") {
		t.Fatalf("register() did not answer transform_tool_result: %v", got.Hooks)
	}
	if got.Failed == nil || !strings.HasPrefix(*got.Failed, `{"output": "panic`) ||
		!strings.HasSuffix(*got.Failed, "\n\n<deja-recall>ran make clean after this before</deja-recall>") {
		t.Fatalf("the failed result did not come back with the line after it: %v", got.Failed)
	}
	if got.Passed != nil || got.Other != nil {
		t.Fatalf("a passing command or another tool was changed: passed=%v other=%v", got.Passed, got.Other)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(calls), "hook-tool-after --plain|"); n != 1 {
		t.Fatalf("deja was asked %d times, want once, for the failed command only:\n%s", n, calls)
	}
	if !strings.Contains(string(calls), `"session_id": "s1"`) || !strings.Contains(string(calls), `"tool_name": "terminal"`) {
		t.Fatalf("hook-tool-after was not told the tool and the session:\n%s", calls)
	}
	if !strings.Contains(hermesPluginManifest, "transform_tool_result") {
		t.Fatal("the manifest does not list transform_tool_result")
	}
}

// What the plugin hands deja is Hermes's own terminal result: a JSON document
// inside a string, with the output under "output". The fix pair has to come
// out of that shape.
func TestFixPairReadsHermesTerminalResult(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	result, jerr := json.Marshal(map[string]any{
		"output":    "goroutine 1 [running]:\npanic: sql: database is closed",
		"exit_code": 2,
		"error":     nil,
	})
	if jerr != nil {
		t.Fatal(jerr)
	}
	payload, jerr := json.Marshal(map[string]any{
		"tool_name":     "terminal",
		"tool_response": string(result),
		"session_id":    "hermes-1",
		"cwd":           "/work/app",
	})
	if jerr != nil {
		t.Fatal(jerr)
	}
	var out bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "make clean && make CGO_ENABLED=0") {
		t.Fatalf("no fix line for a Hermes terminal failure:\n%s", out.String())
	}
}

func TestInstallHermesPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	if _, err := installHermesAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(home, ".hermes", "plugins", "deja")
	manifest, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	// Hermes reads the manifest to decide what the plugin provides.
	if !strings.Contains(string(manifest), "pre_llm_call") {
		t.Fatalf("manifest does not declare the injecting hook:\n%s", manifest)
	}
	code, err := os.ReadFile(filepath.Join(dir, "__init__.py"))
	if err != nil {
		t.Fatalf("plugin code missing: %v", err)
	}
	src := string(code)
	for _, want := range []string{
		`ctx.register_hook("pre_llm_call"`,           // the only hook that can inject
		"ctx.register_command",                       // /deja, so the tool is findable
		"hook-context",                               // first turn: the session digest
		"hook-prompt",                                // later turns: relevance
		strconv.Quote(hookExeInConfigs("/bin/deja")), // the launcher (#3682)
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("plugin missing %q:\n%s", want, src)
		}
	}
	cfg, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	if err != nil {
		t.Fatalf("config missing: %v", err)
	}
	// Discovered is not loaded: Hermes lists the plugin as "not enabled" until
	// its name is in plugins.enabled.
	if !strings.Contains(string(cfg), "plugins:\n  enabled:\n    - deja") {
		t.Fatalf("plugin never enabled:\n%s", cfg)
	}
	if !strings.Contains(string(cfg), "mcp_servers:\n  deja:") {
		t.Fatalf("MCP server not registered:\n%s", cfg)
	}
}

// An exe path with a YAML metacharacter (a ": " reads as a nested key, a " #"
// starts a comment) must be quoted, or Hermes cannot parse the config it was
// just handed.
func TestInstallHermesQuotesTheCommandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	// The entry names the launcher under XDG_CONFIG_HOME, so that is where
	// the YAML metacharacters go (#5269).
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg: v#2"))
	exe := "/opt/deja: v#2/deja"
	if _, err := installHermesMCP(exe, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	if err != nil {
		t.Fatalf("config missing: %v", err)
	}
	s := string(cfg)
	if !strings.Contains(s, "command: "+yamlQuote(hookExeInConfigs(exe))) || !strings.Contains(hookExeInConfigs(exe), ": v#2") {
		t.Fatalf("command path is not YAML-quoted, so Hermes cannot parse it:\n%s", s)
	}
	if strings.Contains(s, "command: "+hookExeInConfigs(exe)) {
		t.Fatalf("raw unquoted path written, breaks the YAML:\n%s", s)
	}
}

func TestInstallHermesUninstallLeavesNeighbours(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	dir := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "# Hermes config\nplugins:\n  enabled:\n    - other\n  disabled: []\nmcp_servers:\n  github:\n    command: gh\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installHermesAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installHermesAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	cfg := string(b)
	if strings.Contains(cfg, "deja") {
		t.Fatalf("uninstall left deja behind:\n%s", cfg)
	}
	for _, keep := range []string{"# Hermes config", "- other", "github:", "command: gh"} {
		if !strings.Contains(cfg, keep) {
			t.Fatalf("uninstall dropped %q:\n%s", keep, cfg)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "deja")); !os.IsNotExist(err) {
		t.Fatalf("plugin directory survived: %v", err)
	}
}

// A hook must not stall a turn, but a command the user typed can wait: the
// first search of a session may rebuild the index, which takes far longer than
// the hook budget. With one shared timeout /deja answered "nothing matches"
// for a query that had thirty-six hits.
func TestHermesCommandGetsALongerTimeoutThanTheHook(t *testing.T) {
	src := hermesPluginPy("/bin/deja")
	if !strings.Contains(src, "timeout=120") {
		t.Fatalf("the slash command still runs on the hook budget:\n%s", src)
	}
	if !strings.Contains(src, "def _deja(args, payload=\"\", timeout=10)") {
		t.Fatalf("the hook budget is no longer the default:\n%s", src)
	}
}

// With HOME unset every filepath.Join(homeDir(), …) became a relative path, so
// install wrote .claude/, .claude.json and .config/deja/wiring.json into the
// working directory — a repository, usually — and reported success (#1690).
func TestInstallRefusesWhenHomeIsUnknown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("USERPROFILE and the profile APIs make an unset HOME a different question here")
	}
	hermeticEnv(t)
	t.Setenv("HOME", "")

	wd := t.TempDir()
	t.Chdir(wd)

	_, err := captureRun(t, "install", "claude-code")
	if err == nil {
		t.Error("install accepted a run with no home directory")
	} else if !strings.Contains(err.Error(), "home") {
		t.Errorf("the refusal does not say what is missing: %s", err)
	}

	for _, name := range []string{".claude", ".claude.json", ".agents", ".config"} {
		if _, statErr := os.Stat(filepath.Join(wd, name)); statErr == nil {
			t.Errorf("install created %s in the working directory", name)
		}
	}
}

// The neighbouring case must keep working: a home that exists is not affected.
func TestInstallStillWorksWithAHome(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err != nil {
		t.Errorf("install did not write into the real home: %v", err)
	}
}

// A redirect written without a space ends the command word as surely as a pipe
// does. It was not in the set, so a line that already runs deja's hook was read
// as somebody else's and deja installed a second one beside it — the session
// start then injects memory twice (#2493).
func TestARedirectEndsTheSubcommand(t *testing.T) {
	const cmd = "/new/path/deja hook-context"
	for _, was := range []string{
		"/usr/local/bin/deja hook-context>>/tmp/deja.log",
		"/usr/local/bin/deja hook-context>/tmp/deja.log",
		"/usr/local/bin/deja hook-context</dev/null",
		"/usr/local/bin/deja hook-context|tee /tmp/deja.log",
	} {
		got := sessionStartCommands(t, updateClaudeHook(hookRootWith(was), "SessionStart", cmd, "", false))
		if len(got) != 1 || got[0] != was {
			t.Errorf("install turned\n  %q\ninto\n  %q\nwant the reader's line alone", was, got)
		}
	}
}

// A subcommand that is the head of a longer word is still not ours.
func TestALongerSubcommandIsNotOurs(t *testing.T) {
	const cmd = "/new/path/deja hook-context"
	was := "/usr/local/bin/deja hook-context-extra"
	got := sessionStartCommands(t, updateClaudeHook(hookRootWith(was), "SessionStart", cmd, "", false))
	if len(got) != 2 {
		t.Errorf("a different subcommand was taken for ours: %q", got)
	}
}

func sessionStartCommands(t *testing.T, root map[string]any) []string {
	t.Helper()
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks["SessionStart"].([]any)
	var out []string
	for _, e := range entries {
		entry, _ := e.(map[string]any)
		hs, _ := entry["hooks"].([]any)
		for _, h := range hs {
			cmd, _ := h.(map[string]any)["command"].(string)
			out = append(out, cmd)
		}
	}
	return out
}

func hookRootWith(cmd string) map[string]any {
	return map[string]any{"hooks": map[string]any{"SessionStart": []any{
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}},
	}}}
}

// The reader's own line that runs deja's hook is deja's hook: adding a second
// entry runs it twice at every session start, and rewriting the line throws
// away whatever else it did. Somebody else's tool that merely lives under a
// path containing the word is not deja's (#2477).
func TestAHookWrapperIsNeitherDuplicatedNorRewritten(t *testing.T) {
	const cmd = "/new/path/deja hook-context"
	for _, tc := range []struct {
		name    string
		was     string
		want    []string
		wantSub string
	}{
		{
			name: "a wrapper around deja's own hook",
			was:  `sh -c 'caffeinate -i /usr/local/bin/deja hook-context'`,
			want: []string{`sh -c 'caffeinate -i /usr/local/bin/deja hook-context'`},
		},
		{
			name: "a wrapper that runs something first",
			was:  "/usr/local/bin/mylog && /usr/local/bin/deja hook-context",
			want: []string{"/usr/local/bin/mylog && /usr/local/bin/deja hook-context"},
		},
		{
			name: "somebody else's tool under a path with the word in it",
			was:  "/home/deja/bin/mytool hook-context",
			want: []string{"/home/deja/bin/mytool hook-context", cmd},
		},
		{
			name: "deja's own hook at an older path",
			was:  "/old/path/deja hook-context",
			want: []string{cmd},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionStartCommands(t, updateClaudeHook(hookRootWith(tc.was), "SessionStart", cmd, "", false))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("install left\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// Uninstall takes deja's own line out and leaves the reader's alone: a wrapper
// is their command, and somebody else's tool was never deja's to remove.
func TestUninstallLeavesLinesThatAreNotDejasOwn(t *testing.T) {
	const cmd = "/new/path/deja hook-context"
	for _, tc := range []struct {
		was  string
		want []string
	}{
		{was: "/old/path/deja hook-context", want: nil},
		{was: "/home/deja/bin/mytool hook-context", want: []string{"/home/deja/bin/mytool hook-context"}},
		{was: "/usr/local/bin/mylog && /usr/local/bin/deja hook-context", want: []string{"/usr/local/bin/mylog && /usr/local/bin/deja hook-context"}},
	} {
		got := sessionStartCommands(t, updateClaudeHook(hookRootWith(tc.was), "SessionStart", cmd, "", true))
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("uninstall of %q left %q, want %q", tc.was, got, tc.want)
		}
	}
}

func TestSettingsHookLeavesTheRestOfTheFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := `{"mcpServers":{"other":{"command":"x"}},"selectedAuthType":"oauth","hooks":{"AfterTool":[{"hooks":[{"type":"command","command":"someone-else"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		b, _ := os.ReadFile(path)
		var root map[string]any
		_ = json.Unmarshal(b, &root)
		return root
	}
	if _, err := installSettingsHook(path, "BeforeAgent", "", 10000, "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := read()
	if _, ok := root["mcpServers"]; !ok {
		t.Error("mcpServers was dropped")
	}
	if root["selectedAuthType"] != "oauth" {
		t.Error("auth setting was dropped")
	}
	hooks, _ := root["hooks"].(map[string]any)
	if _, ok := hooks["AfterTool"]; !ok {
		t.Error("another tool's hook was dropped")
	}
	if _, err := installSettingsHook(path, "BeforeAgent", "", 10000, "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	hooks, _ = read()["hooks"].(map[string]any)
	if _, ok := hooks["AfterTool"]; !ok {
		t.Error("uninstall removed a hook that was not ours")
	}
	if _, ok := hooks["BeforeAgent"]; ok {
		t.Error("uninstall left our hook")
	}
}
func TestKimiUninstallKeepsSomeoneElsesHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", "")

	dir := filepath.Join(home, ".kimi-code")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	original := "[providers.openrouter]\napi_key = \"secret\"\n\n[[hooks]]\nevent = \"Stop\"\ncommand = \"mine\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installKimiAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installKimiAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	for _, want := range []string{"[providers.openrouter]", `api_key = "secret"`, `event = "Stop"`, `command = "mine"`} {
		if !strings.Contains(got, want) {
			t.Errorf("uninstall destroyed %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hook-context") {
		t.Error("uninstall left our entry behind")
	}
}

// An install from a newer deja has to update the entry it already owns —
// otherwise anyone who installed once never sees a field added later, and a
// moved binary leaves a second entry firing beside the new one.
func TestCodexHookInstallAdoptsAnOlderEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// What an older version wrote: our command, no status message.
	seed := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"/opt/old/deja hook-context","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
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
	groups := root.Hooks["SessionStart"]
	var ours []string
	for _, g := range groups {
		for _, h := range g.Hooks {
			if !strings.Contains(h.Command, "deja") {
				continue
			}
			ours = append(ours, h.Command)
			if h.StatusMessage == "" {
				t.Error("adopted entry never gained the status message")
			}
		}
	}
	if len(ours) != 1 {
		t.Fatalf("%d deja hooks, want 1: %v", len(ours), ours)
	}
	if !strings.HasPrefix(ours[0], hookExeInConfigs("/usr/local/bin/deja")) {
		t.Fatalf("kept the stale path: %s", ours[0])
	}
}

// Point-of-action recall is wired as a PreToolUse hook for both codex and
// Claude, idempotently, and removed on uninstall.
func TestPreToolUseWiresHookTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	countHookTool := func(path, event string) int {
		b, err := os.ReadFile(path)
		if err != nil {
			return 0
		}
		var root struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(b, &root); err != nil {
			t.Fatalf("bad json in %s: %v", path, err)
		}
		n := 0
		for _, g := range root.Hooks[event] {
			for _, h := range g.Hooks {
				if strings.Contains(h.Command, "deja") && strings.HasSuffix(h.Command, "hook-tool") {
					n++
				}
			}
		}
		return n
	}
	codexPath := filepath.Join(home, ".codex", "hooks.json")
	claudePath := filepath.Join(home, ".claude", "settings.json")

	// Install twice: the PreToolUse hook-tool entry lands once, not twice.
	for range 2 {
		if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
			t.Fatalf("codex install: %v", err)
		}
		if _, err := installClaudeHook("/usr/local/bin/deja", false); err != nil {
			t.Fatalf("claude install: %v", err)
		}
	}
	if n := countHookTool(codexPath, "PreToolUse"); n != 1 {
		t.Errorf("codex PreToolUse hook-tool count = %d, want 1", n)
	}
	if n := countHookTool(claudePath, "PreToolUse"); n != 1 {
		t.Errorf("claude PreToolUse hook-tool count = %d, want 1", n)
	}

	// Uninstall removes it.
	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHook("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if n := countHookTool(codexPath, "PreToolUse"); n != 0 {
		t.Errorf("codex PreToolUse hook-tool survived uninstall: %d", n)
	}
	if n := countHookTool(claudePath, "PreToolUse"); n != 0 {
		t.Errorf("claude PreToolUse hook-tool survived uninstall: %d", n)
	}
}

// Kimi took three wrong readings: it injects the hook's plain stdout rather
// than a JSON field, only UserPromptSubmit does it, and its prompt arrives as
// content parts. Each one on its own looks like "recall found nothing".
func TestKimiHooksUserPromptSubmitWithPlainOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installKimiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	if strings.Contains(cfg, `event = "SessionStart"`) {
		t.Fatal("SessionStart runs but its output goes nowhere: wrong event")
	}
	if !strings.Contains(cfg, `event = "UserPromptSubmit"`) {
		t.Fatalf("no UserPromptSubmit hook:\n%s", cfg)
	}
	// Kimi reads stdout verbatim; the JSON envelope would land in the prompt.
	if !strings.Contains(cfg, "hook-prompt --plain") {
		t.Fatalf("hook does not ask for the bare digest:\n%s", cfg)
	}
	if _, err := installKimiAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, _ = os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if strings.Contains(string(b), "hook-prompt") {
		t.Fatalf("uninstall left the hook behind:\n%s", b)
	}
}

// Installing twice must leave what installing once left. It did not: the entry
// deja creates had no statusMessage while the entry it adopts on a second run
// did, so a first install wrote one file and a reinstall wrote another — and
// whoever installed once never saw the line their harness shows while the hook
// runs. Found by sweeping install-then-install across the harnesses.
func TestInstallIsIdempotent(t *testing.T) {
	for _, target := range installTargetNames() {
		if !strings.HasSuffix(target, "-auto") {
			continue
		}
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			// VS Code resolves through APPDATA on Windows, and vscode-auto
			// writes there.
			t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			const exe = "/usr/local/bin/deja"
			if _, err := installTarget(target, exe, false); err != nil {
				t.Fatalf("first install: %v", err)
			}
			first := snapshotHome(t, home)
			if _, err := installTarget(target, exe, false); err != nil {
				t.Fatalf("second install: %v", err)
			}
			second := snapshotHome(t, home)

			for path, a := range first {
				b, ok := second[path]
				if !ok {
					t.Errorf("%s disappeared on reinstall", path)
					continue
				}
				if a != b {
					t.Errorf("%s differs between the first install and the second:\n--- first\n%s\n--- second\n%s",
						path, a, b)
				}
			}
			for path := range second {
				if _, ok := first[path]; !ok {
					t.Errorf("%s appeared only on the second install", path)
				}
			}
		})
	}
}

func snapshotHome(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".bak") {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		// deja's own state records when it last wired, which is allowed to
		// differ between two runs.
		if strings.Contains(path, filepath.Join(".config", "deja")) {
			return nil
		}
		b, rerr := os.ReadFile(path) //nolint:gosec // the walk is over the test's own temp home
		if rerr != nil {
			return nil //nolint:nilerr // unreadable files are skipped
		}
		rel, rerr := filepath.Rel(home, path)
		if rerr != nil {
			rel = path
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// jsoncValue reads a .jsonc file the way a config parser does: comments out,
// trailing commas out, then JSON. A test that only looked at the text would
// miss the case where deja's entry lands outside the "mcp" block — that file
// parses, it just does not wire deja to anything.
func jsoncValue(t *testing.T, b []byte) map[string]any {
	t.Helper()
	s := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(b), "")
	s = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(s, "$1")
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("the config no longer parses (%v):\n%s", err, b)
	}
	return m
}

// The writer bounds the "mcp" block by counting braces as text, so a brace
// inside a quoted command path counts as structure: an unmatched "{" puts
// deja's entry outside the block, an unmatched "}" puts it in without a comma,
// and one in deja's own entry makes the drop loop swallow the server under it
// (#2475). Paths like ${VENDOR} or /opt/{a,b}/bin are ordinary.
func TestABraceInAQuotedPathIsNotStructure(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		keep string
	}{
		{
			name: "an unmatched open brace",
			old: `{
  "mcp": {
    "tools": {"type":"local","command":["/opt/shells/{/bin/tools"]},
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  },
  "theme": "dark"
}`,
			keep: "tools",
		},
		{
			name: "an unmatched closing brace",
			old: `{
  "mcp": {
    "tools": {"type":"local","command":["/opt/shells/}/bin/tools"]},
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  },
  "theme": "dark"
}`,
			keep: "tools",
		},
		{
			name: "a brace in deja's own entry",
			old: `{
  "mcp": {
    "deja": {"type":"local","command":["/opt/{/deja","mcp"]},
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  }
}`,
			keep: "mine",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := updateOpencodeJSONC([]byte(tc.old), "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			got := jsoncValue(t, out)
			mcp, _ := got["mcp"].(map[string]any)
			if mcp == nil {
				t.Fatalf("the mcp block is gone:\n%s", out)
			}
			if _, ok := mcp["deja"]; !ok {
				t.Errorf("deja is not in the mcp block:\n%s", out)
			}
			if _, ok := got["deja"]; ok {
				t.Errorf("deja was written as a top-level key, where opencode will not look for it:\n%s", out)
			}
			if _, ok := mcp[tc.keep]; !ok {
				t.Errorf("the %q server the reader already had is gone:\n%s", tc.keep, out)
			}
			if strings.Contains(string(out), `"/usr/local/bin/deja"`) == false {
				t.Errorf("deja's own command is not in the file:\n%s", out)
			}
		})
	}
}

// A .jsonc config that only mentions deja in a comment has no deja entry to
// replace. The drop loop matched the word anywhere on the line, so it deleted
// the comment — and with a block comment it deleted the opening line and left
// the closing */ behind, which costs the reader every server in the file
// (#2473).
func TestACommentThatMentionsDejaSurvivesTheInstall(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		keep string
	}{
		{
			name: "a line comment",
			old: `{
  "mcp": {
    // "deja": {"type":"local","command":["/old/deja","mcp"]},
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  }
}`,
			keep: `// "deja"`,
		},
		{
			name: "a block comment",
			old: `{
  "mcp": {
    /* the "deja" server is installed by ` + "`deja install`" + `
       do not edit this block by hand */
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  }
}`,
			keep: "/* the \"deja\" server",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, note, err := updateOpencodeJSONC([]byte(tc.old), "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			if !strings.Contains(got, tc.keep) {
				t.Errorf("the comment is gone:\n%s", got)
			}
			if !strings.Contains(got, `"mine"`) {
				t.Errorf("the server the comment sat above is gone:\n%s", got)
			}
			if !strings.Contains(got, `"deja": {"type":"local","command":["/usr/local/bin/deja","mcp"]}`) {
				t.Errorf("deja was not added:\n%s", got)
			}
			if note != "" {
				t.Errorf("nothing was replaced, but install says %q", note)
			}
		})
	}
}

// A CRLF config with a comment in it goes through the JSONC text writers, which
// looked for '\n' only: the second opencode install put a comma after the CR
// (`},\r,`) and opencode refused the file, and the others left a `   \r` line
// that uninstall kept (#4553).
func TestCRLFCommentedConfigsRoundTrip(t *testing.T) {
	const exe = "/usr/local/bin/deja"
	for _, c := range []struct {
		name  string
		seed  string
		path  func(dir string) string
		write func(path string, uninstall bool) (installResult, error)
	}{
		{"opencode", "{\n  // my servers\n  \"mcp\": {\n    \"mine\": {\"type\": \"local\", \"command\": [\"my-server\"]}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "opencode.json") },
			func(p string, u bool) (installResult, error) {
				return installOpencodeShaped(filepath.Dir(p), "opencode", exe, u)
			}},
		{"gemini", "{\n  // mine\n  \"mcpServers\": {\n    \"mine\": {\"command\": \"my-server\"}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "settings.json") },
			func(p string, u bool) (installResult, error) { return installMCPJSON(p, exe, u) }},
		{"vscode", "{\n  // mine\n  \"servers\": {\n    \"mine\": {\"type\": \"stdio\", \"command\": \"my-server\"}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "mcp.json") },
			func(p string, u bool) (installResult, error) { return installVSCodeMCPAt(p, exe, u) }},
		{"prime", "{\n  // mine\n  \"mcpServers\": {\n    \"mine\": {\"command\": \"my-server\"}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "settings.json") },
			func(p string, u bool) (installResult, error) { return installPrimeMCPAt(p, exe, u) }},
		{"amp", "{\n  // mine\n  \"amp.mcpServers\": {\n    \"mine\": {\"command\": \"my-server\"}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "settings.json") },
			func(p string, u bool) (installResult, error) { return installAmpMCPAt(p, exe, u) }},
		{"openclaw", "{\n  // mine\n  \"mcp\": {\n    \"servers\": {\n      \"mine\": {\"command\": \"my-server\"}\n    }\n  }\n}\n",
			func(string) string { return filepath.Join(sources.OpenClawStateDir(), "openclaw.json") },
			func(_ string, u bool) (installResult, error) { return installOpenClawMCP(exe, u) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			t.Cleanup(func() { removingWiring = false })
			path := c.path(t.TempDir())
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			seed := strings.ReplaceAll(c.seed, "\n", "\r\n")
			if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
				t.Fatal(err)
			}
			for i := range 2 {
				if _, err := c.write(path, false); err != nil {
					t.Fatalf("install %d: %v", i+1, err)
				}
			}
			b, _ := os.ReadFile(path)
			var probe any
			if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &probe); err != nil {
				t.Fatalf("after two installs the config is not JSONC (%v):\n%q", err, b)
			}
			for i, line := range strings.SplitAfter(string(b), "\n") {
				if strings.TrimSpace(line) == "" && line != "" {
					t.Fatalf("line %d is blank in what install wrote:\n%q", i+1, b)
				}
			}
			removingWiring = true
			if _, err := c.write(path, true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if got, _ := os.ReadFile(path); string(got) != seed {
				t.Fatalf("uninstall did not give the file back:\n%q\nwant\n%q", got, seed)
			}
		})
	}
}

// The .jsonc writer drops the deja entry by looking for the string on a line.
// An entry written across lines carries it on the first one only, so the rest
// stayed behind and the config stopped parsing (#2394).
func TestInstallDropsAWholeMultilineEntry(t *testing.T) {
	before := `{
  // my settings
  "mcp": {
    "mine": {"type":"local","command":["/usr/bin/mine"]},
    "deja": {
      "type": "local",
      "command": ["/home/me/bin/deja-wrapper", "mcp"]
    }
  }
}
`
	next, note, err := updateOpencodeJSONC([]byte(before), "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	got := string(next)
	if !strings.Contains(got, "// my settings") {
		t.Errorf("the comment did not survive:\n%s", got)
	}
	if !strings.Contains(note, "/home/me/bin/deja-wrapper") {
		t.Errorf("the note did not name what it replaced: %q", note)
	}
	if strings.Contains(got, "deja-wrapper") {
		t.Errorf("the old entry is still in the file:\n%s", got)
	}

	var root struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	stripped := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(got, "")
	if err := json.Unmarshal([]byte(stripped), &root); err != nil {
		t.Fatalf("the config no longer parses: %v\n%s", err, got)
	}
	if len(root.MCP) != 2 || root.MCP["mine"] == nil || root.MCP["deja"] == nil {
		t.Errorf("the block should hold their server and deja's, got %d entries:\n%s", len(root.MCP), got)
	}
}

// The .json writer names the entry it replaced (#2390). Opencode's second
// writer edits lines so comments survive, and said nothing — so what install
// told you depended on which of the two names your config had (#2392).
func TestInstallSaysWhatItReplacedInAJSONCConfig(t *testing.T) {
	cases := []struct {
		name   string
		before string
		want   string // "" means: say nothing besides the action
	}{
		{"an entry someone pointed at their own wrapper", `{
  // my settings
  "mcp": {
    "mine": {"type":"local","command":["/usr/bin/mine"]},
    "deja": {"type":"local","command":["/home/me/bin/deja-wrapper","mcp","--quiet"]}
  }
}
`, "/home/me/bin/deja-wrapper"},
		{"no deja entry at all", `{
  "mcp": {
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  }
}
`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
			dir := filepath.Join(home, ".config", "opencode")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "opencode.jsonc")
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}

			r, err := installTarget("opencode", "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if tc.want == "" {
				if r.Note != "" {
					t.Errorf("install spoke about an entry nobody had: %q", r.Note)
				}
				return
			}
			if !strings.Contains(r.Note, tc.want) {
				t.Errorf("install replaced their wiring without naming it: note %q", r.Note)
			}
			// The comment is why this writer exists at all.
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "// my settings") {
				t.Errorf("the comment did not survive the write:\n%s", body)
			}
		})
	}
}

// Junie reads its user hooks from $JUNIE_HOME/config.json and its servers from
// $JUNIE_HOME/mcp/mcp.json. Two UserPromptSubmit lines differ only in their
// subcommand and flags, and a second install has to leave each one alone
// rather than read one as the other.
func TestInstallJunieAutoWritesHooksAndServer(t *testing.T) {
	hermeticEnv(t)
	jh := filepath.Join(t.TempDir(), "junie home")
	t.Setenv("JUNIE_HOME", jh)
	cfg := filepath.Join(jh, "config.json")
	writeFileMkdir(t, cfg, `{"theme":"dark"}`)

	if _, err := captureRun(t, "install", "junie-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(jh, "mcp", "mcp.json"))), &mcp); err != nil {
		t.Fatal(err)
	}
	if !entryRunsDeja(mcp.Servers["deja"]) {
		t.Errorf("mcp.json has no deja server: %+v", mcp.Servers)
	}
	got := readFile(t, cfg)
	var root struct {
		Theme string                      `json:"theme"`
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(got), &root); err != nil {
		t.Fatal(err)
	}
	if root.Theme != "dark" {
		t.Errorf("install dropped the reader's own key:\n%s", got)
	}
	for _, h := range junieHookWiring {
		if !strings.Contains(got, strings.Join(h.Args, " ")) {
			t.Errorf("no %s hook running %v:\n%s", h.Event, h.Args, got)
		}
	}
	if len(root.Hooks["UserPromptSubmit"]) != 2 || len(root.Hooks["PreToolUse"]) != 1 {
		t.Errorf("hook entries = %v", root.Hooks)
	}
	if m, _ := root.Hooks["PreToolUse"][0]["matcher"].(string); m != "Bash|Read|Edit" {
		t.Errorf("PreToolUse matcher = %q", m)
	}
	for _, ev := range []string{"SessionStart", "Stop"} {
		if _, ok := root.Hooks[ev]; ok {
			t.Errorf("%s is wired; Junie drops its output or blocks on it", ev)
		}
	}
	if _, err := os.Stat(filepath.Join(jh, "commands", "deja.md")); err != nil {
		t.Errorf("no /deja command: %v", err)
	}

	if _, err := captureRun(t, "install", "junie-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, cfg); again != got {
		t.Errorf("second install changed config.json:\n%s", again)
	}
	if _, err := captureRun(t, "uninstall", "junie-auto"); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, cfg); strings.Contains(after, "hook-") {
		t.Errorf("uninstall left deja's hooks:\n%s", after)
	}
}

// The payload's cwd is Junie's home; the command has to see the project.
func TestJunieHookHandsTheProjectAsCwd(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_JUNIE_ROOT", t.TempDir())
	payload := `{"hook_event_name":"UserPromptSubmit","session_id":"session-261001-090000-ab12","cwd":"/home/u/.junie","project_path":"/work/notes","prompt":"hi"}`
	var seen string
	cmd := func(_ string, rest []string) error { //nolint:unparam // it stands in for a command, which returns an error
		raw := readHookStdin()
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		seen, _ = m["cwd"].(string)
		for _, a := range rest {
			if a == "--junie" {
				t.Errorf("the command was handed --junie: %v", rest)
			}
		}
		return nil
	}
	err := withStdin([]byte(payload), func() error {
		return runHookDeferred(t.TempDir(), "hook-context", []string{"--plain", "--junie"}, cmd)
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != "/work/notes" {
		t.Fatalf("cwd the command saw = %q, want the project", seen)
	}
}

func TestWithPlainExtra(t *testing.T) {
	if got := string(withPlainExtra([]byte("own\n"), "")); got != "own\n" {
		t.Fatalf("no extra: %q", got)
	}
	if got := string(withPlainExtra(nil, "pair")); got != "pair\n" {
		t.Fatalf("extra only: %q", got)
	}
	if got := string(withPlainExtra([]byte("own\n"), "pair")); got != "pair\n\nown\n" {
		t.Fatalf("both: %q", got)
	}
}

// switchJSON sets one key deep in a JSON config, the way a client's own
// `disable` command or a hand edit leaves it.
func switchJSON(t *testing.T, path string, value any, keys ...string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	m := root
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			t.Fatalf("%s has no %q on the way to %v:\n%s", path, k, keys, b)
		}
		m = next
	}
	m[keys[len(keys)-1]] = value
	out, jerr := json.MarshalIndent(root, "", "  ")
	if jerr != nil {
		t.Fatal(jerr)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSONPath(t *testing.T, path string, keys ...string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, k := range keys {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

func switchText(t *testing.T, path, from, to string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(string(b), from, to, 1)
	if next == string(b) {
		t.Fatalf("%s has no %q to switch:\n%s", path, from, b)
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An install over deja's own entry turned it back on wherever the writer built
// a fresh entry rather than merging into the old one, and said nothing about
// it: the next client start ran deja again for somebody who had switched it
// off (#4467). The plugin registries did the same through the -auto targets,
// openclaw while reporting "unchanged" (#4472). Install keeps the switch where
// the reader put it and says so, the way the shared JSON writer always has (#2479).
func TestInstallKeepsTheReadersOffSwitch(t *testing.T) {
	for _, tc := range []struct {
		target   string
		off      func(t *testing.T)
		stillOff func(t *testing.T) bool
	}{
		{
			// The control: the shared writer has kept it since #2479. Kiro,
			// not cursor: cursor-agent has no switch on the entry (#5162).
			target: "kiro",
			off: func(t *testing.T) {
				t.Helper()
				switchJSON(t, kiroMCPSettingsPath(), true, "mcpServers", "deja", "disabled")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return readJSONPath(t, kiroMCPSettingsPath(), "mcpServers", "deja", "disabled") == true
			},
		},
		{
			target: "prime",
			off: func(t *testing.T) {
				t.Helper()
				switchJSON(t, primeSettingsPath(), false, "mcpServers", "deja", "enabled")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return readJSONPath(t, primeSettingsPath(), "mcpServers", "deja", "enabled") == false
			},
		},
		{
			target: "zed",
			off: func(t *testing.T) {
				t.Helper()
				switchJSON(t, sources.ZedSettingsPath(), false, zedServerKey, zedServerID, "enabled")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return readJSONPath(t, sources.ZedSettingsPath(), zedServerKey, zedServerID, "enabled") == false
			},
		},
		{
			target: "zcode",
			off: func(t *testing.T) {
				t.Helper()
				switchJSON(t, zcodeConfigPath(), false, "mcp", "servers", "deja", "enabled")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return readJSONPath(t, zcodeConfigPath(), "mcp", "servers", "deja", "enabled") == false
			},
		},
		{
			target: "goose",
			off: func(t *testing.T) {
				t.Helper()
				switchText(t, filepath.Join(gooseConfigDir(), "config.yaml"), "enabled: true", "enabled: false")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return strings.Contains(fileText(t, filepath.Join(gooseConfigDir(), "config.yaml")), "enabled: false")
			},
		},
		{
			target: "hermes",
			off: func(t *testing.T) {
				t.Helper()
				switchText(t, filepath.Join(sources.HermesHome(), "config.yaml"), "enabled: true", "enabled: false")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return strings.Contains(fileText(t, filepath.Join(sources.HermesHome(), "config.yaml")), "enabled: false")
			},
		},
		{
			target: "codex",
			off: func(t *testing.T) {
				t.Helper()
				switchText(t, filepath.Join(sources.CodexHome(), "config.toml"), "[mcp_servers.deja]\n", "[mcp_servers.deja]\nenabled = false\n")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return strings.Contains(fileText(t, filepath.Join(sources.CodexHome(), "config.toml")), "enabled = false")
			},
		},
		{
			target: "grok",
			off: func(t *testing.T) {
				t.Helper()
				switchText(t, filepath.Join(sources.GrokHome(), "config.toml"), "[mcp_servers.deja]\n", "[mcp_servers.deja]\nenabled = false\n")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return strings.Contains(fileText(t, filepath.Join(sources.GrokHome(), "config.toml")), "enabled = false")
			},
		},
		{
			target: "deepseek-auto",
			off: func(t *testing.T) {
				t.Helper()
				path := filepath.Join(sources.DSHHome(), "cordis.patch.yml")
				switchText(t, path, "    - id: mcp-deja\n", "    - id: mcp-deja\n      disabled: true\n")
				switchText(t, path, "    - id: deja-auto\n", "    - id: deja-auto\n      disabled: true\n")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return strings.Count(fileText(t, filepath.Join(sources.DSHHome(), "cordis.patch.yml")), "disabled: true") == 2
			},
		},
		{
			// `openclaw plugins disable deja` and `openclaw hooks disable
			// deja-recall`.
			target: "openclaw-auto",
			off: func(t *testing.T) {
				t.Helper()
				path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
				switchJSON(t, path, false, "plugins", "entries", openclawPluginID, "enabled")
				switchJSON(t, path, false, "hooks", "internal", "entries", openclawHookName, "enabled")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
				return readJSONPath(t, path, "plugins", "entries", openclawPluginID, "enabled") == false &&
					readJSONPath(t, path, "hooks", "internal", "entries", openclawHookName, "enabled") == false
			},
		},
		{
			// `hermes plugins disable deja` moves the name from one list to
			// the other.
			target: "hermes-auto",
			off: func(t *testing.T) {
				t.Helper()
				switchText(t, filepath.Join(sources.HermesHome(), "config.yaml"),
					"plugins:\n  enabled:\n    - deja\n", "plugins:\n  enabled: []\n  disabled:\n  - deja\n")
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return strings.Contains(fileText(t, filepath.Join(sources.HermesHome(), "config.yaml")), "  enabled: []\n  disabled:\n  - deja\n")
			},
		},
		{
			// `reasonix plugin disable deja`.
			target: "reasonix-auto",
			off: func(t *testing.T) {
				t.Helper()
				switchText(t, reasonixStatePath(), `"enabled": true`, `"enabled": false`)
			},
			stillOff: func(t *testing.T) bool {
				t.Helper()
				return strings.Contains(fileText(t, reasonixStatePath()), `"enabled": false`)
			},
		},
	} {
		t.Run(tc.target, func(t *testing.T) {
			hermeticEnv(t)
			noReasonixCLI(t)
			if _, err := captureRun(t, "install", tc.target, "--no-index"); err != nil {
				t.Fatalf("install: %v", err)
			}
			// The record of the binaries deja wrote is cached per process,
			// and another test's home may have filled it.
			forgetWrittenExes()
			t.Cleanup(forgetWrittenExes)
			tc.off(t)
			if !tc.stillOff(t) {
				t.Fatal("the fixture did not switch the entry off")
			}
			out, err := captureRun(t, "install", tc.target, "--no-index")
			if err != nil {
				t.Fatalf("second install: %v", err)
			}
			if !tc.stillOff(t) {
				t.Errorf("install turned deja back on over the reader's off switch:\n%s", out)
			}
			if !strings.Contains(out, "switched off") {
				t.Errorf("install left deja off and did not say so:\n%s", out)
			}
		})
	}
}

// gemini's hooksConfig.enabled defaults to true, so a false there is the
// reader turning every hook off. Install turned it on with nothing but "also
// updated", and uninstall left it on, so their own hooks ran from then on
// (#4471, the shape #4431 fixed for zcode).
func TestGeminiAutoGivesBackTheHooksSwitch(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	theirs := "{\n  \"hooksConfig\": {\n    \"enabled\": false\n  }\n}\n"
	writeTestFile(t, path, theirs)
	out, err := captureRun(t, "install", "gemini-auto", "--no-index")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(out, "hooksConfig.enabled") {
		t.Errorf("install turned on hooks the reader had off and did not say so:\n%s", out)
	}
	if out, err = captureRun(t, "uninstall", "gemini-auto"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if got := fileText(t, path); got != theirs {
		t.Errorf("settings.json did not come back as it was.\nbefore:\n%s\nafter:\n%s\nuninstall said:\n%s", theirs, got, out)
	}
	if strings.Contains(out, "left hooksConfig.enabled on") {
		t.Errorf("uninstall says it left the switch on after turning it back off:\n%s", out)
	}
}

// A reader who had openclaw's internal hooks on when deja came, and switched
// them off later, got "uninstall turns it back off" from the next install, and
// the uninstall then turned them on: the record kept the first install's
// "on". A false at install is always the reader's, since deja only ever
// writes true (#4472).
func TestOpenClawUninstallKeepsAnOffTheReaderSetAfterInstall(t *testing.T) {
	for _, spelling := range []string{"json", "jsonc"} {
		t.Run(spelling, func(t *testing.T) {
			hermeticEnv(t)
			path := openclawConfigPath()
			head := ""
			if spelling == "jsonc" {
				head = "// mine\n"
			}
			writeTestFile(t, path, head+"{\n  \"hooks\": {\n    \"internal\": {\n      \"enabled\": true\n    }\n  }\n}\n")
			if _, err := captureRun(t, "install", "openclaw-auto", "--no-index"); err != nil {
				t.Fatalf("install: %v", err)
			}
			// The switch alone, as a hand edit leaves it, comments and all.
			off := strings.Replace(fileText(t, path), "\n      \"enabled\": true", "\n      \"enabled\": false", 1)
			if err := os.WriteFile(path, []byte(off), 0o644); err != nil {
				t.Fatal(err)
			}
			if readJSONPath(t, path, "hooks", "internal", "enabled") != false {
				t.Fatalf("the fixture did not switch internal hooks off:\n%s", fileText(t, path))
			}
			out, err := captureRun(t, "install", "openclaw-auto", "--no-index")
			if err != nil {
				t.Fatalf("second install: %v", err)
			}
			if !strings.Contains(out, "uninstall turns it back off") {
				t.Errorf("install turned internal hooks on and did not say so:\n%s", out)
			}
			if _, err := captureRun(t, "uninstall", "openclaw-auto"); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if got := readJSONPath(t, path, "hooks", "internal", "enabled"); got != false {
				t.Errorf("uninstall left hooks.internal.enabled %v after install said it would turn it back off:\n%s", got, fileText(t, path))
			}
		})
	}
}

// Kilo CLI is opencode vendored and loads `<config>/kilo/plugins/*.js` with
// opencode 1.x's loader. Measured on 7.8.3: the 1.x plugin's system transform
// put the digest in front of the model, and the 2.x default export was refused
// ("must default export an object with server()"), whichever opencode is on
// PATH (#4398).
func TestInstallKilocodeAutoWritesThePluginKiloLoads(t *testing.T) {
	hermeticEnv(t)
	// The opencode on PATH says nothing about Kilo's loader.
	t.Setenv("DEJA_OPENCODE_MAJOR", "2")
	if _, err := captureRun(t, "install", "kilocode-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(kilocodeCLIConfigDir(), "plugins", "deja.js")
	js := readFile(t, path)
	if strings.Contains(js, "export default") || !strings.Contains(js, `"experimental.chat.system.transform"`) {
		t.Fatalf("plugin is not the opencode 1.x shape Kilo loads:\n%.400s", js)
	}
	if !strings.Contains(js, "hook-context") || !strings.Contains(js, "hook-prompt") {
		t.Errorf("plugin does not run the digest and per-prompt hooks:\n%.400s", js)
	}
	if !strings.Contains(js, "regenerate with: deja install kilocode-auto") {
		t.Errorf("plugin header names another target:\n%.200s", js)
	}
	// The MCP half is the kilocode target's, CLI config included.
	if cfg := readFile(t, kilocodeCLIConfigPath()); !strings.Contains(cfg, `"deja"`) {
		t.Errorf("kilo config has no deja server:\n%s", cfg)
	}
	if _, err := os.Stat(filepath.Join(opencodeConfigHome(), "opencode", "plugins", "deja.js")); err == nil {
		t.Error("kilocode-auto wrote opencode's plugin too")
	}

	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "kilocode" {
			row = a
		}
	}
	if row.name == "" {
		t.Fatal("doctor has no auto-recall row for kilocode")
	}
	if state, _ := autoWiringState(row); state != "wired" {
		t.Errorf("doctor reads the fresh install as %q, want wired", state)
	}

	if _, err := captureRun(t, "uninstall", "kilocode-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("uninstall left the plugin behind: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("uninstall left the plugins directory it made: %v", err)
	}
}

// A deja.js in Kilo's plugins directory that deja did not generate is the
// reader's: uninstall leaves it.
func TestUninstallKilocodeAutoLeavesAPluginItDidNotWrite(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(kilocodeCLIConfigDir(), "plugins", "deja.js")
	theirs := "export const Mine = async () => ({})\n"
	writeFileMkdir(t, path, theirs)
	if _, err := captureRun(t, "uninstall", "kilocode-auto"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != theirs {
		t.Errorf("uninstall removed or changed a plugin deja did not write: %q", got)
	}
}

// Kilo Code takes the MCP server in `<globalStorage>/kilocode.kilo-code/settings/mcp_settings.json`
// — the file Kilo's own KilocodePaths names — and the shared manual in
// ~/.kilocode/skills, which is where its loader looks first. It has no hooks,
// so those two are the whole wiring (#3643).
func TestInstallKilocodeWiresTheServerAndTheSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "Code", "User", "globalStorage", "kilocode.kilo-code")
	if err := os.MkdirAll(filepath.Join(root, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KILO_ROOTS", root)

	res, err := installKilocode("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatalf("install changed nothing: %+v", res)
	}

	settings := filepath.Join(root, "settings", "mcp_settings.json")
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("mcp settings: %v", err)
	}
	var cfg struct {
		Servers map[string]struct {
			Command     string   `json:"command"`
			Args        []string `json:"args"`
			AlwaysAllow []string `json:"alwaysAllow"`
		} `json:"mcpServers"`
	}
	if uerr := json.Unmarshal(b, &cfg); uerr != nil {
		t.Fatalf("mcp settings are not the shape Kilo reads: %v", uerr)
	}
	srv, ok := cfg.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server in %v", cfg.Servers)
	}
	if len(srv.AlwaysAllow) != 1 || srv.AlwaysAllow[0] != "deja" {
		t.Errorf("alwaysAllow = %v, want [deja]", srv.AlwaysAllow)
	}
	if werr := os.WriteFile(settings, []byte(`{"mcpServers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}}`+"\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if res2, ierr := installKilocode("/usr/local/bin/deja", false); ierr != nil || res2.Action == "unchanged" {
		t.Errorf("upgrade kilocode mcp_settings.json = %+v, %v; want updated", res2, ierr)
	}
	// windows gets the `cmd /c <exe> mcp` shim, so the binary is an argument
	// there rather than the command: what has to hold on every platform is that
	// the entry ends in `mcp` and names this executable.
	if len(srv.Args) == 0 || srv.Args[len(srv.Args)-1] != "mcp" {
		t.Errorf("server entry = %q %v, want it to end in `mcp`", srv.Command, srv.Args)
	}
	if !strings.Contains(srv.Command+" "+strings.Join(srv.Args, " "), "/usr/local/bin/deja") {
		t.Errorf("server entry = %q %v, want the binary in it", srv.Command, srv.Args)
	}

	skill := filepath.Join(home, ".kilocode", "skills", "deja-history", "SKILL.md")
	sb, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("skill: %v", err)
	}
	if !strings.Contains(string(sb), "deja") {
		t.Errorf("the skill does not mention deja: %q", string(sb)[:60])
	}

	// And the global slash command, in the directory Kilo's workflows doc
	// names: a file called deja.md there is invoked as /deja.
	command := filepath.Join(home, ".config", "kilo", "commands", "deja.md")
	if cb, rerr := os.ReadFile(command); rerr != nil {
		t.Fatalf("command: %v", rerr)
	} else if !strings.Contains(string(cb), "deja") {
		t.Errorf("the command file does not mention deja: %q", string(cb)[:60])
	}

	// Idempotent: a second run reports nothing changed rather than rewriting.
	again, err := installKilocode("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}

	// And the uninstall takes both back out.
	out, err := installKilocode("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Action == "unchanged" {
		t.Errorf("uninstall changed nothing: %+v", out)
	}
	if b, err := os.ReadFile(settings); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("the server is still wired after uninstall: %s", b)
	}
	if _, err := os.Stat(skill); err == nil {
		t.Errorf("the skill survived uninstall: %s", skill)
	}
}

// A machine with VS Code but without the extension is not a Kilo machine: the
// installer must not create settings for an editor that never had it.
func TestInstallKilocodeSkipsAHostWithoutTheExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_KILO_ROOTS", filepath.Join(home, "Code", "User", "globalStorage", "kilocode.kilo-code"))

	res, err := installKilocode("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	// The skill is still written — the CLI reads the same directory — but the
	// note has to say the server was not wired anywhere.
	if !strings.Contains(res.Note, "no VS Code host") {
		t.Errorf("note = %q, want it to say no host has Kilo Code", res.Note)
	}
	if _, err := os.Stat(kilocodeSkillPath()); err != nil {
		t.Errorf("the skill was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Code")); err == nil {
		t.Error("the installer created an editor directory that was not there")
	}
}

func kimiDoctorRow(t *testing.T) doctorMCPStatus {
	t.Helper()
	js, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		MCP []doctorMCPStatus `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(js), &report); err != nil {
		t.Fatalf("doctor --json: %v: %s", err, js)
	}
	for _, r := range report.MCP {
		if r.Name == "kimi" {
			return r
		}
	}
	t.Fatalf("no kimi row:\n%s", js)
	return doctorMCPStatus{}
}

// Kimi Code 2.2 reads `enabled` on an mcp.json entry and drops every other
// key, so an entry with "disabled": true is started and `/mcp` lists it
// connected. Install and doctor called it off.
func TestKimiEntryDisabledKeyIsNotItsSwitch(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sources.KimiConfigDir(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		key string
		off bool
	}{
		{`"disabled": true`, false},
		{`"enabled": false`, true}, // Kimi's own switch: the control
	} {
		body := `{"mcpServers": {"deja": {"command": "` + filepath.ToSlash(bin) + `", "args": ["mcp"], ` + c.key + `}}}` + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := captureRun(t, "install", "kimi", "--no-index")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out, "switched off"); got != c.off {
			t.Errorf("%s: install says switched off = %v, want %v:\n%s", c.key, got, c.off, out)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), c.key) {
			t.Errorf("%s: the reader's key was dropped:\n%s", c.key, b)
		}
		if r := kimiDoctorRow(t); r.State != "wired" || r.SwitchedOff != c.off {
			t.Errorf("%s: doctor row = %+v, want wired and switched_off=%v", c.key, r, c.off)
		}
		text, err := captureRun(t, "doctor")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(text, "the entry is switched off — kimi"); got != c.off {
			t.Errorf("%s: doctor text says switched off = %v, want %v", c.key, got, c.off)
		}
	}
}

// Kimi Code's own login and logout write config.toml back from its parsed
// form, which drops every comment, deja's markers among them. Uninstall then
// left the seven hooks running a deja-hook it had deleted, and the next
// install stacked seven more beside them.
func TestKimiAutoFindsItsHooksAfterKimiRewroteTheConfig(t *testing.T) {
	hermeticEnv(t)
	cfg := filepath.Join(sources.KimiConfigDir(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "default_model = \"luna\"\n\n[[hooks]]\nevent = \"Stop\"\ncommand = \"say done\"\n"
	if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	n := strings.Count(string(b), "[[hooks]]")
	// What Kimi 2.2 left after /logout from Kimi Code: the same tables, no
	// comments, a blank line between them.
	var kept []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	rewritten := strings.Join(kept, "\n")
	if strings.Contains(rewritten, kimiHookMarker) {
		t.Fatal("the rewrite kept the marker")
	}
	if err := os.WriteFile(cfg, []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cfg)
	if got := strings.Count(string(b), "[[hooks]]"); got != n {
		t.Errorf("reinstall: %d hooks, want %d:\n%s", got, n, b)
	}
	if got := strings.Count(string(b), "hook-prompt"); got != 1 {
		t.Errorf("reinstall: hook-prompt %d times, want 1:\n%s", got, b)
	}
	if err := os.WriteFile(cfg, []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "kimi-auto"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cfg)
	if strings.Contains(string(b), "deja") {
		t.Errorf("uninstall left deja's hooks behind:\n%s", b)
	}
	if !strings.Contains(string(b), `command = "say done"`) {
		t.Errorf("uninstall took the reader's own hook:\n%s", b)
	}

	// The control: a rule the reader wrote around deja's hook is not one deja
	// wrote, and neither install nor uninstall takes it.
	theirs := "[[hooks]]\nevent = \"UserPromptSubmit\"\ncommand = \"deja hook-prompt --plain\"\n"
	if err := os.WriteFile(cfg, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "kimi-auto"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != theirs {
		t.Errorf("the reader's own rule changed:\n%q", b)
	}
}

// A read-only config.toml or tui.toml was refused after mcp.json (and
// config.toml) had been written, so the refused target was half wired.
func TestKimiAutoRefusesAReadOnlyFileBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	for _, c := range []struct {
		file, seed string
		refused    bool
	}{
		{"config.toml", "default_model = \"luna\"\n", true},
		{"tui.toml", "theme = \"dark\"\n", true},
		// Nothing to change in it, so nothing to refuse: the control.
		{"tui.toml", "[status_line]\ncommand = \"my-status\"\n", false},
	} {
		t.Run(c.file, func(t *testing.T) {
			hermeticEnv(t)
			dir := sources.KimiConfigDir()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, c.file), []byte(c.seed), 0o444); err != nil {
				t.Fatal(err)
			}
			_, err := captureRun(t, "install", "kimi-auto", "--no-index")
			if !c.refused {
				if err != nil {
					t.Fatalf("install refused a read-only %s it had nothing to write to: %v", c.file, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Fatalf("install err = %v, want the read-only refusal", err)
			}
			for _, name := range []string{"mcp.json", "config.toml", "tui.toml"} {
				if name == c.file {
					continue
				}
				if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
					t.Errorf("%s was written by a refused target", name)
				}
			}
		})
	}
}

// The hooks were written naming the binary while the launcher's directory
// was read-only. Once it opens, the pre-flight passed tui.toml on the binary
// while the writers moved config.toml to the launcher, then refused tui.toml.
func TestKimiAutoPreflightChecksThePathTheWriterNames(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions as a normal user")
	}
	hermeticEnv(t)
	bin := filepath.Dir(dejaLauncherPath())
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bin, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bin, 0o755) })
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := sources.KimiConfigDir()
	if err := os.Chmod(filepath.Join(dir, "tui.toml"), 0o444); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.toml")
	before, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = captureRun(t, "install", "kimi-auto", "--no-index")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("install err = %v, want the read-only refusal", err)
	}
	if after, _ := os.ReadFile(cfg); !bytes.Equal(after, before) {
		t.Errorf("config.toml was rewritten by a refused target:\n%s\nwant:\n%s", after, before)
	}
}

// [status_line] can hold Kimi's own `items` beside the command. Rewriting
// deja's line rebuilt the table without them, and uninstall took the whole
// table.
func TestKimiStatusLineKeepsTheItemsBesideIt(t *testing.T) {
	hermeticEnv(t)
	tui := filepath.Join(sources.KimiConfigDir(), "tui.toml")
	if err := os.MkdirAll(filepath.Dir(tui), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tui, []byte("theme = \"dark\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(tui)
	withItems := strings.Replace(string(b), "[status_line]\n", "[status_line]\nitems = [\"model\", \"git\"]\n", 1)
	if withItems == string(b) {
		t.Fatalf("no [status_line] written:\n%s", b)
	}
	if err := os.WriteFile(tui, []byte(withItems), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(tui); string(b2) != withItems {
		t.Errorf("reinstall changed the table:\n%s\nwant:\n%s", b2, withItems)
	}
	if _, err := captureRun(t, "uninstall", "kimi-auto"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(tui)
	if want := "theme = \"dark\"\n\n[status_line]\nitems = [\"model\", \"git\"]\n"; string(b) != want {
		t.Errorf("uninstall left:\n%q\nwant:\n%q", b, want)
	}
}

// Review cases: a comment inside deja's status line table is not the reader's
// key, and a note above the reader's next hook does not hide the unmarked
// deja hook before it.
func TestKimiConfigCommentsAroundDejasTables(t *testing.T) {
	hermeticEnv(t)
	dir := sources.KimiConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := hookExeInConfigs("/bin/deja")
	tui := filepath.Join(dir, "tui.toml")
	seed := "theme = \"dark\"\n\n[status_line]\n# deja's line\ncommand = \"" + exe + " statusline\"\n"
	if err := os.WriteFile(tui, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installKimiStatusline("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tui); string(b) != "theme = \"dark\"\n" {
		t.Errorf("status line uninstall left %q", b)
	}

	cfg := filepath.Join(dir, "config.toml")
	hooks := "[[hooks]]\nevent = \"SessionEnd\"\ncommand = \"" + exe + " hook-session-end\"\ntimeout = 30\n\n" +
		"[[hooks]]\nevent = \"PreCompact\"\ncommand = \"" + exe + " hook-precompact\"\ntimeout = 30\n\n" +
		"# mine\n[[hooks]]\nevent = \"Stop\"\ncommand = \"say\"\n"
	if err := os.WriteFile(cfg, []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installKimiAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "# mine\n[[hooks]]\nevent = \"Stop\"\ncommand = \"say\"\n" {
		t.Errorf("hooks uninstall left %q", b)
	}
}

// kiro-cli edits an agent file in place: `kiro-cli mcp add --agent deja`
// writes the server into ~/.kiro/agents/deja.json and normalises the rest of
// the file (shape captured from kiro-cli 2.32.0). A re-install used to write
// deja's agent whole and drop the server; an uninstall deleted it with the
// file.
func TestKiroAutoKeepsWhatKiroAddedToTheDejaAgent(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := kiroAgentPath()
	hook := kiroTestHookExe(t, path)
	entry := func(sub string) string {
		return `[{"command": "` + hook + ` ` + sub + `", "timeout_ms": 10000, "max_output_size": 10240, "cache_ttl_seconds": 0}]`
	}
	edited := `{
  "name": "deja",
  "description": "` + kiroAgentDescription + `",
  "prompt": null,
  "mcpServers": {
    "gh-tools": {"command": "/usr/bin/gh-mcp", "args": ["stdio"], "env": {}}
  },
  "tools": ["*"],
  "toolAliases": {},
  "allowedTools": ["fs_read"],
  "resources": [],
  "hooks": {
    "stop": [{"command": "say done", "timeout_ms": 1000}, {"command": "/usr/bin/time ` + hook + ` hook-session-end", "timeout_ms": 1000}, ` + strings.TrimPrefix(entry("hook-session-end"), "[") + `,
    "agentSpawn": ` + entry("hook-context --plain") + `,
    "postToolUse": ` + entry("hook-tool-after --defer") + `,
    "preToolUse": ` + entry("hook-tool --defer") + `,
    "userPromptSubmit": ` + entry("hook-prompt --plain") + `
  },
  "toolsSettings": {},
  "includeMcpJson": true,
  "model": "claude-sonnet-4"
}
`
	writeFileMkdir(t, path, edited)

	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	agent := kiroTestAgent(t, path)
	servers, _ := agent["mcpServers"].(map[string]any)
	if _, ok := servers["gh-tools"]; !ok {
		t.Errorf("re-install dropped the server kiro-cli added to the agent:\n%s", readFile(t, path))
	}
	if allowed, _ := agent["allowedTools"].([]any); agent["model"] != "claude-sonnet-4" || len(allowed) != 1 {
		t.Errorf("re-install dropped the agent's model or allowedTools:\n%s", readFile(t, path))
	}
	hooks, _ := agent["hooks"].(map[string]any)
	for trigger, sub := range map[string]string{"agentSpawn": "hook-context", "userPromptSubmit": "hook-prompt",
		"preToolUse": "hook-tool", "postToolUse": "hook-tool-after", "stop": "hook-session-end"} {
		if n := kiroTestDejaHooks(hooks[trigger], sub); n != 1 {
			t.Errorf("%s runs deja %s %d times after re-install, want 1", trigger, sub, n)
		}
	}
	if !strings.Contains(readFile(t, path), `"say done"`) || !strings.Contains(readFile(t, path), "/usr/bin/time ") {
		t.Errorf("re-install dropped the reader's own stop hooks:\n%s", readFile(t, path))
	}

	out, err := captureRun(t, "uninstall", "kiro-auto")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("uninstall deleted an agent that holds the reader's server: %v\n%s", err, out)
	}
	if !strings.Contains(string(got), `"gh-tools"`) || !strings.Contains(string(got), `"say done"`) || !strings.Contains(string(got), "/usr/bin/time ") {
		t.Errorf("uninstall took the reader's additions out of the agent:\n%s", got)
	}
	if strings.Contains(string(got), "hook-context") || strings.Count(string(got), "hook-session-end") != 1 {
		t.Errorf("uninstall left deja's hooks in the agent:\n%s", got)
	}
	if !strings.Contains(out, reportPath(path)) {
		t.Errorf("uninstall did not say it kept the agent:\n%s", out)
	}
}

// `kiro-cli agent upgrade` (2.32) rewrites the agent into the format both
// engines load: hooks become a list of {name, trigger, action}, and it adds a
// default permissions rule and its own kiroUpgrade record. A re-install keeps
// that shape; an uninstall of an upgraded file nobody else touched removes it.
func TestKiroAutoFollowsAnUpgradedDejaAgent(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := kiroAgentPath()
	hook := kiroTestHookExe(t, path)
	h := func(trigger, sub string) string {
		return `{"name": "` + trigger + `-0", "trigger": "` + trigger + `", "action": {"type": "command", "command": "` + hook + ` ` + sub + `"}, "timeout": 10}`
	}
	upgraded := `{
  "description": "` + kiroAgentDescription + `",
  "hooks": [
    ` + h("agentSpawn", "hook-context --plain") + `,
    ` + h("postToolUse", "hook-tool-after --defer") + `,
    ` + h("preToolUse", "hook-tool --defer") + `,
    ` + h("stop", "hook-session-end") + `,
    ` + h("userPromptSubmit", "hook-prompt --plain") + `
  ],
  "includeMcpJson": true,
  "name": "deja",
  "tools": ["*"],
  "allowedTools": [],
  "permissions": {"rules": [{"capability": "fs_read", "match": ["./**"], "effect": "allow"}]},
  "kiroUpgrade": {"revision": 2, "fingerprint": "sha256:aa", "source": "sha256:bb"}
}
`
	writeFileMkdir(t, path, upgraded)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	agent := kiroTestAgent(t, path)
	list, ok := agent["hooks"].([]any)
	if !ok {
		t.Fatalf("re-install turned the upgraded hook list back into the old map:\n%s", readFile(t, path))
	}
	if n := kiroTestDejaHooks(list, "hook-context"); n != 1 {
		t.Errorf("agentSpawn runs deja %d times, want 1:\n%s", n, readFile(t, path))
	}
	if _, ok := agent["permissions"]; !ok {
		t.Errorf("re-install dropped the upgraded agent's permissions:\n%s", readFile(t, path))
	}

	if _, err := captureRun(t, "uninstall", "kiro-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("uninstall left an upgraded agent with nothing of the reader's in it:\n%s", readFile(t, path))
	}
}

// The default V2 engine runs kiro_default when chat.defaultAgent names an
// agent that is not there; kiro-cli 2.32 prints `agent "deja" not found, using
// "kiro_default"`. doctor called that wired.
func TestDoctorKiroDefaultAgentWithoutTheAgentFile(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	writeFileMkdir(t, kiroSettingsPath(), "{\n  \"chat.defaultAgent\": \"deja\"\n}\n")
	if err := os.Remove(kiroAgentPath()); err != nil {
		t.Fatal(err)
	}
	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "kiro" {
			row = a
		}
	}
	if state, _ := autoWiringState(row); state == "wired" {
		t.Errorf("doctor says wired while kiro-cli's default agent file is gone")
	}
	// A deja agent of the reader's own, without deja's hooks, runs none either.
	writeFileMkdir(t, kiroAgentPath(), `{"name":"deja","description":"mine","tools":["read"]}`+"\n")
	if state, _ := autoWiringState(row); state == "wired" {
		t.Errorf("doctor says wired while the default agent runs no deja hook")
	}
	// install leaves that agent alone, so the fix doctor names is the rename.
	if !strings.Contains(kiroV2Note(), "rename it") {
		t.Errorf("doctor's fix for the reader's own deja agent: %s", kiroV2Note())
	}
}

func kiroTestAgent(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &m); err != nil {
		t.Fatalf("agent file is not JSON: %v\n%s", err, readFile(t, path))
	}
	return m
}

// kiroTestHookExe is the hook binary the install wrote into the agent.
func kiroTestHookExe(t *testing.T, path string) string {
	t.Helper()
	hooks := typedOrFatal[map[string]any](t, kiroTestAgent(t, path)["hooks"])
	spawn := typedOrFatal[map[string]any](t, typedOrFatal[[]any](t, hooks["agentSpawn"])[0])
	cmd := typedOrFatal[string](t, spawn["command"])
	exe, _, _ := strings.Cut(cmd, " hook-context")
	return filepath.ToSlash(exe)
}

// kiroTestDejaHooks counts entries, of either agent format, that run deja sub.
func kiroTestDejaHooks(v any, sub string) int {
	n := 0
	list, _ := v.([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		cmd, _ := m["command"].(string)
		if a, ok := m["action"].(map[string]any); ok {
			cmd, _ = a["command"].(string)
		}
		if f := strings.Fields(cmd); len(f) > 1 && f[1] == sub {
			n++
		}
	}
	return n
}

// kiro-cli runs the hooks of the agent a chat starts in, and adds what an
// agentSpawn or userPromptSubmit hook prints to the model's context. Measured
// on 2.22.0: both outputs were in the request; preToolUse and postToolUse
// output was not (#4304).
func TestInstallKiroAutoWritesAnAgentWithRecallHooks(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(kiroTestDir(t), "agents", "deja.json")
	var agent struct {
		Name           string   `json:"name"`
		Tools          []string `json:"tools"`
		IncludeMcpJSON bool     `json:"includeMcpJson"`
		Hooks          map[string][]struct {
			Command   string `json:"command"`
			TimeoutMs int    `json:"timeout_ms"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readFile(t, path)), &agent); err != nil {
		t.Fatalf("agent file is not JSON: %v", err)
	}
	if agent.Name != "deja" || !agent.IncludeMcpJSON || len(agent.Tools) != 1 || agent.Tools[0] != "*" {
		t.Errorf("agent is not a full-tool agent that reads the global MCP file: %+v", agent)
	}
	spawn, prompt := agent.Hooks["agentSpawn"], agent.Hooks["userPromptSubmit"]
	if len(spawn) != 1 || !strings.HasSuffix(spawn[0].Command, " hook-context --plain") {
		t.Errorf("agentSpawn does not run hook-context --plain: %+v", spawn)
	}
	if len(prompt) != 1 || !strings.HasSuffix(prompt[0].Command, " hook-prompt --plain") {
		t.Errorf("userPromptSubmit does not run hook-prompt --plain: %+v", prompt)
	}
	if spawn[0].TimeoutMs <= 0 || spawn[0].TimeoutMs > 30000 {
		t.Errorf("timeout_ms = %d", spawn[0].TimeoutMs)
	}
	// The MCP half is the kiro target's.
	if !strings.Contains(readFile(t, kiroMCPSettingsPath()), `"deja"`) {
		t.Error("kiro-auto did not write the MCP entry")
	}

	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "kiro" {
			row = a
		}
	}
	if row.name == "" {
		t.Fatal("doctor has no auto-recall row for kiro")
	}
	// kiro-cli starts kiro_default unless told otherwise, so an agent nobody
	// starts is installed, not wired.
	if state, _ := autoWiringState(row); state != "installed" {
		t.Errorf("doctor reads the fresh install as %q, want installed", state)
	}

	if _, err := captureRun(t, "uninstall", "kiro-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("uninstall left the agent behind: %v", err)
	}
}

// The IDE and kiro-cli's V3 engine run ~/.kiro/hooks/*.json in every chat, in
// the v1 schema acp-server.js validates; on a 2.28 stand SessionStart and
// UserPromptSubmit stdout reached the model. The skill directory is loaded by
// both engines.
func TestInstallKiroAutoWritesTheGlobalHooksAndSkill(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(kiroTestDir(t), "hooks", "deja.json")
	var file struct {
		Version string `json:"version"`
		Hooks   []struct {
			Name    string `json:"name"`
			Trigger string `json:"trigger"`
			Action  struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"action"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readFile(t, path)), &file); err != nil {
		t.Fatalf("hook file is not JSON: %v", err)
	}
	if file.Version != "v1" {
		t.Errorf("version = %q, want v1", file.Version)
	}
	want := map[string]string{
		"SessionStart":     " hook-context --plain",
		"UserPromptSubmit": " hook-prompt --plain",
		"SessionEnd":       " hook-session-end",
	}
	for _, h := range file.Hooks {
		suffix, ok := want[h.Trigger]
		if !ok || h.Action.Type != "command" || !strings.HasSuffix(h.Action.Command, suffix) {
			t.Errorf("unexpected hook %+v", h)
		}
		delete(want, h.Trigger)
	}
	if len(want) != 0 {
		t.Errorf("triggers missing: %v", want)
	}
	skill := filepath.Join(kiroTestDir(t), "skills", "deja-history", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("no skill: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "kiro-auto"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, skill} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("uninstall left %s: %v", p, err)
		}
	}
}

// An agent the reader named deja is theirs: install leaves it alone and still
// writes the MCP server and steering, which `deja install --auto` used to give
// that machine before kiro-auto existed.
func TestInstallKiroAutoLeavesTheReadersOwnDejaAgent(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(kiroTestDir(t), "agents", "deja.json")
	theirs := `{"name":"deja","description":"mine","tools":["read"]}` + "\n"
	writeFileMkdir(t, path, theirs)
	out, err := captureRun(t, "install", "kiro-auto", "--no-index")
	if err != nil {
		t.Fatalf("install refused the whole target over their agent: %v", err)
	}
	if got := readFile(t, path); got != theirs {
		t.Errorf("their agent changed:\n%s", got)
	}
	if !strings.Contains(readFile(t, kiroMCPSettingsPath()), `"deja"`) {
		t.Error("no MCP entry beside their agent")
	}
	if _, err := os.Stat(kiroSteeringPath()); err != nil {
		t.Errorf("no steering file beside their agent: %v", err)
	}
	if !strings.Contains(out, "did not write") {
		t.Errorf("install did not say why the agent was skipped:\n%s", out)
	}
	if _, err := captureRun(t, "uninstall", "kiro-auto"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != theirs {
		t.Errorf("uninstall took their agent:\n%s", got)
	}
}

func kiroTestDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("HOME"), ".kiro")
}

// The deja agent runs only when a chat starts in it. doctor says wired once it
// is kiro-cli's default, and uninstall takes the default back when it named
// deja's agent, so kiro-cli is not left pointing at a file that is gone.
func TestKiroAutoDefaultAgent(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "kiro" {
			row = a
		}
	}
	var report bytes.Buffer
	doctorAutoRecall(&report)
	if !strings.Contains(report.String(), "kiro-cli agent set-default deja") {
		t.Errorf("doctor does not say how to make the agent run:\n%s", report.String())
	}
	settings := filepath.Join(kiroTestDir(t), "settings", "cli.json")
	writeFileMkdir(t, settings, "{\n  \"chat.defaultAgent\": \"deja\",\n  \"chat.enableThinking\": true\n}\n")
	if state, _ := autoWiringState(row); state != "wired" {
		t.Errorf("with deja as the default agent doctor says %q, want wired", state)
	}
	if _, err := captureRun(t, "uninstall", "kiro-auto"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, settings)
	if strings.Contains(got, "chat.defaultAgent") || !strings.Contains(got, "chat.enableThinking") {
		t.Errorf("uninstall did not take back only the default agent:\n%s", got)
	}

	// A default of the reader's own stays.
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	mine := "{\n  \"chat.defaultAgent\": \"mine\"\n}\n"
	writeFileMkdir(t, settings, mine)
	if _, err := captureRun(t, "uninstall", "kiro-auto"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, settings); got != mine {
		t.Errorf("uninstall changed a default that was not deja's:\n%s", got)
	}
}

func TestKiroAutoUninstallPrunesSettingsDirectory(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(kiroTestDir(t), "settings")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("settings not created by install: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "kiro-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Errorf("uninstall left the empty settings directory behind: %v", err)
	}
}

func TestKiroAutoUninstallKeepsSettingsHoldingUserFiles(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(kiroTestDir(t), "settings")
	userFile := filepath.Join(settings, "custom.json")
	if err := os.WriteFile(userFile, []byte(`{"custom": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "kiro-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Errorf("uninstall removed settings directory holding user files: %v", err)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Errorf("user file in settings was removed: %v", err)
	}
}

func TestKiroUninstallPrunesSettingsDirectory(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro", "--no-index"); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(kiroTestDir(t), "settings")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("settings not created by install: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "kiro"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Errorf("uninstall left the empty settings directory behind: %v", err)
	}
}

// `kiro-auto` is one edit from `kimi-auto`, and following that hint wires a
// different agent. A name that is a known target plus `-auto` says so (#4301).
func TestUnknownAutoTargetPointsAtThePlainOne(t *testing.T) {
	for _, h := range []string{"kiro", "roo"} {
		err := unknownTargetError(h + "-auto")
		if err == nil {
			t.Fatalf("%s-auto: no error", h)
		}
		msg := err.Error()
		if !strings.Contains(msg, h+" has no -auto target") || !strings.Contains(msg, "deja install "+h) {
			t.Errorf("%s-auto: %q, want it to point at `deja install %s`", h, msg, h)
		}
		if strings.Contains(msg, "did you mean") {
			t.Errorf("%s-auto: %q names another agent", h, msg)
		}
	}
	// A typo still gets the nearest name.
	if msg := unknownTargetError("claud").Error(); !strings.Contains(msg, "did you mean") {
		t.Errorf("claud: %q, want the nearest target", msg)
	}
}

// A deja entry the user switched off stays off, and the run says so even when
// nothing else changed: the note was dropped with the unchanged MCP write,
// and the line named the steering file (#4302).
func TestInstallKiroSaysTheEntryIsSwitchedOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installKiro("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".kiro", "settings", "mcp.json")
	switchOffDeja(t, cfg)

	res, err := installKiro("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != cfg {
		t.Errorf("path = %q, want the mcp.json the note is about", res.Path)
	}
	if !strings.Contains(res.Note, "switched off") {
		t.Errorf("note = %q, want it to say the entry is still off", res.Note)
	}
}

// Doctor read a switched-off entry as wired, though the client will never start
// it (#4303). Cursor has no such key on the entry and runs it (#5162), so its
// row stays unmarked.
func TestDoctorMarksASwitchedOffMCPEntry(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	kiro := kiroMCPSettingsPath()
	cursor := filepath.Join(home, ".cursor", "mcp.json")
	for _, p := range []string{kiro, cursor} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"],"disabled":true}}}`
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		MCP []doctorMCPStatus `json:"mcp"`
	}
	if uerr := json.Unmarshal([]byte(out), &report); uerr != nil {
		t.Fatalf("doctor --json: %v: %s", uerr, out)
	}
	seen := 0
	for _, r := range report.MCP {
		if r.Name != "kiro" && r.Name != "cursor" {
			continue
		}
		seen++
		if want := r.Name == "kiro"; r.SwitchedOff != want {
			t.Errorf("%s row = %+v, want switched_off %v", r.Name, r, want)
		}
	}
	if seen != 2 {
		t.Fatalf("rows seen = %d:\n%s", seen, out)
	}
	text, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(text, "switched off") != 1 {
		t.Errorf("the text report should say kiro's entry is off and only that:\n%s", text)
	}

	// An entry that is on is not marked.
	body := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"]}}}`
	if err := os.WriteFile(kiro, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if dejaEntrySwitchedOff("kiro", kiro) {
		t.Error("an entry that is on reads as switched off")
	}
}

func switchOffDeja(t *testing.T, cfg string) {
	t.Helper()
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if uerr := json.Unmarshal(b, &root); uerr != nil {
		t.Fatal(uerr)
	}
	servers := typedOrFatal[map[string]any](t, root["mcpServers"])
	typedOrFatal[map[string]any](t, servers["deja"])["disabled"] = true
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// OpenClaw's MCP entry moved to the launcher (#5323), and every upgrade then
// said "replaced the deja entry that was already here, which ran <deja>" of
// the very binary the launcher wraps — the noise #2479 took out. Said only
// when the old entry ran some other deja.
func TestOpenClawUpgradeToLauncherSaysNothingOfTheSameDeja(t *testing.T) {
	if dejaLauncherPath() == "" {
		t.Skip("no launcher on this platform; the entry keeps the binary's path")
	}
	for name, tc := range map[string]struct {
		was   func(exe string) string
		jsonc bool
		quiet bool
	}{
		"this binary":         {was: func(exe string) string { return exe }, quiet: true},
		"this binary, JSONC":  {was: func(exe string) string { return exe }, jsonc: true, quiet: true},
		"an older launcher":   {was: func(string) string { return "/old/.config/deja/bin/deja-hook" }, quiet: true},
		"another deja":        {was: func(string) string { return "/opt/other/deja" }},
		"another deja, JSONC": {was: func(string) string { return "/opt/other/deja" }, jsonc: true},
	} {
		t.Run(name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
			exe := filepath.Join(tmp, "deja")
			if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			was, _ := json.Marshal(tc.was(exe))
			cfg := `{"mcp": {"servers": {"deja": {"command": ` + string(was) + `, "args": ["mcp"]}}}}`
			if tc.jsonc {
				cfg = "// mine\n" + cfg
			}
			path := openclawConfigPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := installOpenClawMCP(exe, false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(readFile(t, path), dejaLauncherPath()) {
				t.Fatalf("the entry did not move to the launcher:\n%s", readFile(t, path))
			}
			said := strings.Contains(res.Note, "replaced the deja entry")
			if tc.quiet && said {
				t.Errorf("an upgrade from the same deja says: %s", res.Note)
			}
			if !tc.quiet && (!said || !strings.Contains(res.Note, "/opt/other/deja")) {
				t.Errorf("taking over another deja's entry went unsaid: %q", res.Note)
			}
		})
	}
}

func TestRepeatQuestionExampleNamesTheMostRepeatedQuestion(t *testing.T) {
	ask := func(text string) model.Session {
		return model.Session{Messages: []model.Message{{Role: "user", Text: text}}}
	}
	ss := []model.Session{
		ask("why does the retry loop drop the last attempt?"),
		ask("Why does the retry loop drop the last attempt"),
		ask("why does the retry loop drop the last attempt?"),
		ask("how do I run the integration tests here?"),
		ask("how do I run the integration tests here"),
		ask("what is the deploy command for staging?"),
	}
	example, n := stats.RepeatQuestionExample(ss)
	if n != 2 {
		t.Fatalf("repeated = %d, want 2", n)
	}
	if !strings.Contains(example, "retry loop") {
		t.Fatalf("example = %q, want the question asked three times", example)
	}
	if _, n := stats.RepeatQuestionExample(ss[5:]); n != 0 {
		t.Fatalf("a question asked once counted as repeated")
	}
}

func TestInstallLeadOpensWithTheRepeatCount(t *testing.T) {
	got := repeatQuestionsText(26, "prepared statement errors behind pgbouncer")
	for _, want := range []string{"26 questions asked more than once", `"prepared statement errors behind pgbouncer"`, "earlier answer first"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lead %q lacks %q", got, want)
		}
	}
	if got := repeatQuestionsText(1, "x"); !strings.HasPrefix(got, "1 question asked") {
		t.Fatalf("singular: %q", got)
	}
	if got := repeatQuestionsText(0, ""); got != "" {
		t.Fatalf("no repeats still spoke: %q", got)
	}
	if got := joinNotesWith("\n  ", "", "b", "", "c"); got != "b\n  c" {
		t.Fatalf("join = %q", got)
	}
}

// Every config an installer writes has to be recognisable as deja's own, or the
// uninstall cannot take back the snapshot it made of it (#2575) and the reader
// keeps a file naming a binary they removed. The marker list is written by hand
// against twenty installers, so it is checked by installing all of them rather
// than by reading it: zed and dsh were missing, and after them codex, grok and
// aider (#2578). A new harness now fails here until its spelling is known.
func TestEveryInstalledConfigIsRecognisableAsDejas(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	// This test installs; a wrong home would rewire the machine it runs on.
	if home == "" || !strings.Contains(home, "TestEveryInstalledConfig") {
		t.Fatalf("refusing to install into %q", home)
	}
	seen := map[string]bool{}
	walk := func(fn func(path string, body []byte)) {
		_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
			if err != nil || !fi.Mode().IsRegular() {
				return nil //nolint:nilerr // unreadable entries are skipped
			}
			// deja's own state and guidance are not a harness config.
			if strings.Contains(p, filepath.Join(".config", "deja")) {
				return nil
			}
			b, rerr := os.ReadFile(p) //nolint:gosec // the walk is over the test's own temp home
			if rerr != nil {
				return nil //nolint:nilerr // unreadable files are skipped
			}
			fn(p, b)
			return nil
		})
	}
	walk(func(p string, _ []byte) { seen[p] = true })

	for _, target := range []string{
		"claude-code", "codex", "opencode", "cursor", "gemini", "antigravity", "qwen", "kimi",
		"hermes", "pi", "omp", "deepseek", "openclaw", "cline", "goose", "grok", "copilot",
		"roo", "aider", "zed",
	} {
		if _, err := captureRun(t, "install", target, "--no-index", "--no-guidance"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}

	written := 0
	walk(func(p string, b []byte) {
		if seen[p] {
			return
		}
		written++
		if !mentionsDeja(b) || !snapshotHoldsDejaWiring(b) {
			line := strings.Join(strings.Fields(string(b)), " ")
			if len(line) > 120 {
				line = line[:120] + "…"
			}
			t.Errorf("%s is deja's own wiring and mentionsDeja does not know it — an uninstall will leave its backup behind:\n  %s",
				strings.TrimPrefix(p, home), line)
		}
	})
	// The premise: the installers really did write something here.
	if written < 10 {
		t.Fatalf("only %d configs were written, so this measures nothing", written)
	}
}

// Both mcode and mcode-auto should report guidance as missing before installation
// and written after installation.
func TestGuidanceStatusSeesTheMcodeSkill(t *testing.T) {
	hermeticEnv(t)
	harnesses := []string{"mcode", "mcode-auto"}

	for _, harness := range harnesses {
		if status := guidanceStatus(harness); status != "missing" {
			t.Errorf("%s before install: guidance %q, want missing", harness, status)
		}
	}

	exe := filepath.Join(t.TempDir(), "deja")
	if _, err := installMcodeAuto(exe, false); err != nil {
		t.Fatalf("install: %v", err)
	}

	for _, harness := range harnesses {
		if status := guidanceStatus(harness); status != "written" {
			t.Errorf("%s after install: guidance %q, want written", harness, status)
		}
	}
}

// The package MCode loads: a manifest, the hook document, and the icon the
// manifest names. The hook document is the part that matters — the events it
// answers, and the hook each one runs.
func TestMcodeAutoWritesThePackageAndTheMCPEntry(t *testing.T) {
	hermeticEnv(t)
	if _, err := installTarget("mcode-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")

	// The MCP entry, in the shape MCode's own page writes.
	b, err := os.ReadFile(filepath.Join(home, ".minimax", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if jerr := json.Unmarshal(b, &cfg); jerr != nil {
		t.Fatalf("mcp.json is not JSON: %v", jerr)
	}
	s, ok := cfg.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server: %s", b)
	}
	if s.Type != "stdio" {
		t.Errorf("type = %q, want stdio", s.Type)
	}
	if len(s.Args) != 1 || s.Args[0] != "mcp" {
		t.Errorf("args = %v, want [mcp]", s.Args)
	}
	if s.Command == "" {
		t.Error("the server names no command")
	}

	// The manifest, and the icon it points at.
	manifest := filepath.Join(home, ".minimax", "plugins", "deja", ".minimax-plugin", "plugin.json")
	mb, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		SchemaVersion int      `json:"schemaVersion"`
		Name          string   `json:"name"`
		Icon          string   `json:"icon"`
		Hooks         []string `json:"hooks"`
		Skills        []string `json:"skills"`
	}
	if jerr := json.Unmarshal(mb, &m); jerr != nil {
		t.Fatal(jerr)
	}
	if m.SchemaVersion != 1 || m.Name != "deja" {
		t.Errorf("manifest = %s", mb)
	}
	if len(m.Hooks) != 1 || m.Hooks[0] != "hooks/hooks.json" {
		t.Errorf("hooks = %v, want [hooks/hooks.json]", m.Hooks)
	}
	// The skill rides inside the package: MiniMax Code reads no user-level
	// skill directory, so the manifest is what names it.
	if len(m.Skills) != 1 || m.Skills[0] != "skills/deja-history/SKILL.md" {
		t.Errorf("skills = %v, want [skills/deja-history/SKILL.md]", m.Skills)
	}
	skill := filepath.Join(home, ".minimax", "plugins", "deja", "skills", "deja-history", "SKILL.md")
	sb, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("the skill the manifest names is not there: %v", err)
	}
	if !strings.Contains(string(sb), "name: deja-history") {
		t.Errorf("the skill has no frontmatter: %s", sb)
	}
	icon := filepath.Join(home, ".minimax", "plugins", "deja", filepath.FromSlash(m.Icon))
	if fi, err := os.Stat(icon); err != nil || fi.Size() == 0 {
		t.Errorf("the icon the manifest names is not there: %v", err)
	}
}

// Every event the runtime fires has an answer, and the command names the hook
// that answers it: a reader of the file can see which hook runs where.
func TestMcodePluginHooksNameTheHookTheyRun(t *testing.T) {
	hermeticEnv(t)
	if _, err := installTarget("mcode-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("HOME"), ".minimax", "plugins", "deja", "hooks", "hooks.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatalf("hooks.json is not JSON: %v", err)
	}
	want := map[string]string{
		"SessionStart":     "hook-context",
		"UserPromptSubmit": "hook-prompt",
		"PreToolUse":       "hook-tool",
		"PostToolUse":      "hook-tool-after",
		"SessionEnd":       "hook-session-end",
		"PreCompact":       "hook-precompact",
	}
	for event, sub := range want {
		groups := file.Hooks[event]
		if len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Errorf("no %s hook in the package", event)
			continue
		}
		h := groups[0].Hooks[0]
		if h.Type != "command" {
			t.Errorf("%s handler type = %q, want command", event, h.Type)
		}
		if !strings.Contains(h.Command, sub) {
			t.Errorf("%s runs %q, want the %s hook", event, h.Command, sub)
		}
		// The runtime allows a handler ten seconds and no more.
		if h.Timeout < 1 || h.Timeout > 10 {
			t.Errorf("%s timeout = %d, want 1..10", event, h.Timeout)
		}
	}
	// The hooks that carry text to the model ask for MiniMax Code's shape.
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		if !strings.Contains(file.Hooks[event][0].Hooks[0].Command, "--mcode") {
			t.Errorf("%s does not ask for MiniMax Code's shape", event)
		}
	}
}

// The answer MCode reads: one JSON object, the text under
// hookSpecificOutput.additionalContext, under the event it answers. A reply
// naming another event is dropped by the runtime, so the name is carried.
func TestMcodeHookShapeIsWhatMiniMaxCodeReads(t *testing.T) {
	var out bytes.Buffer
	emitMcodeContext(&out, "UserPromptSubmit", "déjà vu: the retry count was three")
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("the answer is not JSON: %v: %q", err, out.String())
	}
	if resp.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("event = %q, want the one it answers", resp.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "the retry count was three") {
		t.Errorf("context = %q", resp.HookSpecificOutput.AdditionalContext)
	}
}

// A hook with nothing to say writes nothing: that is how it stays quiet.
func TestMcodeHookShapeSaysNothingWhenThereIsNothing(t *testing.T) {
	var out bytes.Buffer
	emitMcodeContext(&out, "UserPromptSubmit", "   ")
	if out.Len() != 0 {
		t.Errorf("a silent hook wrote %q", out.String())
	}
}

// What the install wrote has to be in the report. The package is three files
// and the MCP entry a fourth, and a report that names one of them leaves the
// rest invisible to whoever reads it (#3254).
func TestMcodeAutoNamesEveryFileItWrote(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "install", "mcode-auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	for _, p := range []string{
		filepath.Join(home, ".minimax", "mcp.json"),
		filepath.Join(home, ".minimax", "plugins", "deja"),
		filepath.Join(home, ".minimax", "plugins", "deja", ".minimax-plugin", "plugin.json"),
		filepath.Join(home, ".minimax", "plugins", "deja", "hooks", "hooks.json"),
		filepath.Join(home, ".minimax", "plugins", "deja", "icon.png"),
		filepath.Join(home, ".minimax", "plugins", "deja", "skills", "deja-history", "SKILL.md"),
	} {
		if !strings.Contains(out, shortHome(p)) {
			t.Errorf("the report does not name %s:\n%s", shortHome(p), out)
		}
	}
}

// `mcode --session <id>` opens a session by the manifest's id (0.6.12
// --help); the registry used to call resume impossible (#5593).
func TestMcodeResumeOpensTheSessionByID(t *testing.T) {
	hermeticEnv(t)
	base := t.TempDir()
	t.Setenv("DEJA_MCODE_ROOT", filepath.Join(base, "v2", "sessions"))
	s := model.Session{Harness: "mcode", ID: "mvs_0f1e2d3c", Project: "p"}
	dir, cmd, err := resumeCommand(s)
	if err != nil || dir != "" || cmd != "mcode --session mvs_0f1e2d3c" {
		t.Fatalf("resume = %q %q %v", dir, cmd, err)
	}

	// The runtime's database records the workspace: the command runs there
	// while it exists, and without the cd once it is gone.
	if _, lerr := exec.LookPath("sqlite3"); lerr != nil {
		t.Skip("sqlite3 not installed")
	}
	ws := filepath.Join(base, "work", "billing-api")
	if merr := os.MkdirAll(ws, 0o755); merr != nil {
		t.Fatal(merr)
	}
	db := sources.McodeStateDB()
	if merr := os.MkdirAll(filepath.Dir(db), 0o755); merr != nil {
		t.Fatal(merr)
	}
	sql := "create table local_runtime_sessions (session_id text primary key, workspace_dir text, project_workspace_dir text, is_default_workspace integer not null default 0);" +
		"insert into local_runtime_sessions values ('mvs_0f1e2d3c', '" + strings.ReplaceAll(ws, "'", "''") + "', null, 0);"
	if out, serr := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); serr != nil {
		t.Fatalf("sqlite3: %v %s", serr, out)
	}
	dir, cmd, err = resumeCommand(s)
	if err != nil || filepath.Clean(dir) != filepath.Clean(ws) || cmd != "mcode --session mvs_0f1e2d3c" {
		t.Fatalf("with the workspace: resume = %q %q %v", dir, cmd, err)
	}
	if rerr := os.RemoveAll(ws); rerr != nil {
		t.Fatal(rerr)
	}
	dir, cmd, err = resumeCommand(s)
	if err != nil || dir != "" || cmd != "mcode --session mvs_0f1e2d3c" {
		t.Fatalf("workspace gone: resume = %q %q %v", dir, cmd, err)
	}
}

func TestMcodeAutoUninstallPrunesCreatedDirs(t *testing.T) {
	t.Run("removes empty created dirs across processes", func(t *testing.T) {
		root := hermeticEnv(t)
		home := filepath.Join(root, "home")
		if _, err := captureRun(t, "install", "mcode-auto", "--no-index"); err != nil {
			t.Fatal(err)
		}
		resetClineTestRunRecord()
		if _, err := captureRun(t, "uninstall", "mcode-auto"); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{
			filepath.Join(home, ".minimax", "plugins"),
			filepath.Join(home, ".minimax"),
		} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("%s survived uninstall (err = %v)", p, err)
			}
		}
	})

	t.Run("keeps plugins dir when user file is present", func(t *testing.T) {
		root := hermeticEnv(t)
		home := filepath.Join(root, "home")
		if _, err := captureRun(t, "install", "mcode-auto", "--no-index"); err != nil {
			t.Fatal(err)
		}
		userFile := filepath.Join(home, ".minimax", "plugins", "other.txt")
		if err := os.WriteFile(userFile, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		resetClineTestRunRecord()
		if _, err := captureRun(t, "uninstall", "mcode-auto"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(userFile); err != nil {
			t.Errorf("user file in ~/.minimax/plugins removed: %v", err)
		}
	})
}

func TestMimoInstallAndUninstall(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "mimocode")
	t.Setenv("DEJA_MIMO_CONFIG_DIR", dir)

	const exe = "/opt/deja/bin/deja"
	if got, want := mimoConfigPath(), filepath.Join(dir, "mimocode.jsonc"); got != want {
		t.Fatalf("mimoConfigPath() = %q, want %q", got, want)
	}
	if !strings.HasSuffix(mimoSkillPath(), filepath.Join("skills", "deja-history", "SKILL.md")) {
		t.Fatalf("unexpected mimoSkillPath: %q", mimoSkillPath())
	}
	if !strings.Contains(mimoPluginJS(exe), "mimo-auto") {
		t.Fatalf("mimoPluginJS does not identify mimo-auto")
	}

	// An existing mimocode.json takes precedence over mimocode.jsonc.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonCfg := filepath.Join(dir, "mimocode.json")
	if err := os.WriteFile(jsonCfg, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mimoConfigPath(); got != jsonCfg {
		t.Fatalf("mimoConfigPath() with existing .json = %q, want %q", got, jsonCfg)
	}

	if _, err := installMimoAuto(exe, false); err != nil {
		t.Fatalf("installMimoAuto: %v", err)
	}
	if _, err := installMimoAuto(exe, false); err != nil {
		t.Fatalf("idempotent installMimoAuto: %v", err)
	}
	if list := tuiPluginList(t, filepath.Join(dir, "tui.json")); strings.Join(list, ",") != "./plugins/deja-status.tsx" {
		t.Fatalf("tui.json plugins = %v", list)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "deja-status.tsx")); err != nil {
		t.Fatalf("missing MiMo TUI plugin: %v", err)
	}
	if _, err := os.Stat(mimoSkillPath()); err != nil {
		t.Fatalf("missing MiMo skill file: %v", err)
	}

	if _, err := installMimoAuto(exe, true); err != nil {
		t.Fatalf("uninstall installMimoAuto: %v", err)
	}
	for _, p := range []string{
		filepath.Join(dir, "plugins", "deja-status.tsx"),
		mimoSkillPath(),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("uninstall left behind %s", p)
		}
	}

	// Malformed config surfaces an error rather than overwriting it.
	if err := os.WriteFile(jsonCfg, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installMimo(exe, false); err == nil {
		t.Fatal("expected error on malformed mimocode.json")
	}
}

func TestOmnigentInstallAndStatusLine(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(t.TempDir(), "omnigent")
	t.Setenv("DEJA_OMNIGENT_ROOT", root)

	const exe = "/opt/deja/bin/deja"
	if got := omnigentMCPPath(); got != filepath.Join(root, "mcp.json") {
		t.Fatalf("omnigentMCPPath() = %q", got)
	}
	if got := omnigentSettingsPath(); got != filepath.Join(root, "settings.json") {
		t.Fatalf("omnigentSettingsPath() = %q", got)
	}
	if !strings.HasSuffix(omnigentSkillPath(), filepath.Join("skills", "deja-history", "SKILL.md")) {
		t.Fatalf("unexpected omnigentSkillPath: %q", omnigentSkillPath())
	}

	if _, err := installOmnigentAuto(exe, false); err != nil {
		t.Fatalf("installOmnigentAuto: %v", err)
	}
	assertMCPServerEntry(t, omnigentMCPPath())
	if _, err := installOmnigentAuto(exe, false); err != nil {
		t.Fatalf("idempotent installOmnigentAuto: %v", err)
	}
	if settingsBytes, readErr := os.ReadFile(omnigentSettingsPath()); readErr != nil {
		t.Fatal(readErr)
	} else {
		var rootJSON map[string]any
		if umErr := json.Unmarshal(settingsBytes, &rootJSON); umErr != nil {
			t.Fatal(umErr)
		}
		if _, ok := rootJSON["statusLine"]; !ok {
			t.Fatal("settings.json missing statusLine after installOmnigentAuto")
		}
	}

	if _, err := installOmnigentAuto(exe, true); err != nil {
		t.Fatalf("uninstall installOmnigentAuto: %v", err)
	}
	if b, err := os.ReadFile(omnigentMCPPath()); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("omnigent mcp.json still contains deja after uninstall: %s", b)
	}
	if _, err := os.Stat(omnigentSkillPath()); err == nil {
		t.Errorf("uninstall left behind %s", omnigentSkillPath())
	}

	// Exercise installSettingsStatusLine branches directly.
	slPath := filepath.Join(t.TempDir(), "sl.json")
	if res, err := installSettingsStatusLine(slPath, exe, true); err != nil || res.Action != "unchanged" {
		t.Fatalf("uninstall missing statusline = (%+v, %v)", res, err)
	}
	if err := os.WriteFile(slPath, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installSettingsStatusLine(slPath, exe, false); err == nil {
		t.Fatal("expected parse error on invalid JSON")
	}
	// Non-deja statusLine produces combinedStatusline guidance.
	if err := os.WriteFile(slPath, []byte(`{"statusLine":{"type":"command","command":"custom-status"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installSettingsStatusLine(slPath, exe, false); err == nil || !strings.Contains(err.Error(), "statusline is already configured") {
		t.Fatalf("expected conflict error, got %v", err)
	}
	// Wrapped statusLine is left unchanged.
	wrapped, wrapErr := json.Marshal(map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": combinedStatusline("custom-status", exe),
		},
	})
	if wrapErr != nil {
		t.Fatal(wrapErr)
	}
	if err := os.WriteFile(slPath, wrapped, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err := installSettingsStatusLine(slPath, exe, false); err != nil || res.Action != "unchanged" {
		t.Fatalf("wrapped statusLine install = (%+v, %v)", res, err)
	}
	if res, err := installSettingsStatusLine(slPath, exe, true); err != nil || res.Action != "unchanged" {
		t.Fatalf("wrapped statusLine uninstall = (%+v, %v)", res, err)
	}
	// Previous deja binary path is replaced with an explanatory note.
	stale, staleErr := json.Marshal(map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "/usr/local/old/deja statusline",
		},
	})
	if staleErr != nil {
		t.Fatal(staleErr)
	}
	if err := os.WriteFile(slPath, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err := installSettingsStatusLine(slPath, exe, false); err != nil || !strings.Contains(res.Note, "replaced the deja statusline") {
		t.Fatalf("stale deja replacement = (%+v, %v)", res, err)
	}
	if res, err := installSettingsStatusLine(slPath, exe, true); err != nil || res.Action == "unchanged" {
		t.Fatalf("statusLine uninstall = (%+v, %v)", res, err)
	}

	// Malformed mcp.json returns an error cleanly.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(omnigentMCPPath(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installOmnigentAuto(exe, false); err == nil {
		t.Fatal("expected error on malformed omnigent mcp.json")
	}
}

func TestOrcaInstallAndUninstall(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(t.TempDir(), "orca")
	t.Setenv("DEJA_ORCA_ROOT", root)

	const exe = "/opt/deja/bin/deja"
	if got := orcaMCPPath(); got != filepath.Join(root, "mcp.json") {
		t.Fatalf("orcaMCPPath() = %q", got)
	}
	if got := orcaSettingsPath(); got != filepath.Join(root, "settings.json") {
		t.Fatalf("orcaSettingsPath() = %q", got)
	}
	if !strings.HasSuffix(orcaSkillPath(), filepath.Join("skills", "deja-history", "SKILL.md")) {
		t.Fatalf("unexpected orcaSkillPath: %q", orcaSkillPath())
	}

	if _, err := installOrcaAuto(exe, false); err != nil {
		t.Fatalf("installOrcaAuto: %v", err)
	}
	assertMCPServerEntry(t, orcaMCPPath())
	if _, err := installOrcaAuto(exe, false); err != nil {
		t.Fatalf("idempotent installOrcaAuto: %v", err)
	}
	for _, p := range []string{orcaMCPPath(), orcaSettingsPath(), orcaSkillPath()} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing Orca file %s: %v", p, err)
		}
	}

	if _, err := installOrcaAuto(exe, true); err != nil {
		t.Fatalf("uninstall installOrcaAuto: %v", err)
	}
	if b, err := os.ReadFile(orcaMCPPath()); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("orca mcp.json still contains deja after uninstall: %s", b)
	}
	if _, err := os.Stat(orcaSkillPath()); err == nil {
		t.Errorf("uninstall left behind %s", orcaSkillPath())
	}

	// Malformed mcp.json propagates error.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orcaMCPPath(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installOrcaAuto(exe, false); err == nil {
		t.Fatal("expected error on malformed orca mcp.json")
	}
}

func TestCoverageFloorHelpersAndDoctorProbes(t *testing.T) {
	hermeticEnv(t)

	// bench_ingest reporting + CLI dispatch.
	var buf bytes.Buffer
	printIngestReport(&buf, ingestReport{
		Sessions: 4,
		Seed:     1,
		Classes: []ingestClass{
			{Name: "", Rewritten: false},
			{Name: "append", WallMS: 1.5, RecordsMB: 0.2, AddedKB: 4.0, ClearedKB: 0.5, Rewritten: true},
		},
	})
	if !strings.Contains(buf.String(), "(unnamed)") || !strings.Contains(buf.String(), "YES") {
		t.Fatalf("unexpected printIngestReport output:\n%s", buf.String())
	}
	if err := runBenchIngest([]string{"--bogus"}); err == nil {
		t.Fatal("expected error for bogus ingest bench flag")
	}
	var textErr, jsonErr error
	textOut := captureStdoutCall(t, func() { textErr = runBenchIngest([]string{"--seed", "1"}) })
	jsonOut := captureStdoutCall(t, func() { jsonErr = runBenchIngest([]string{"--json", "--seed", "1"}) })
	if textErr != nil || !strings.Contains(textOut, "deja bench ingest") {
		t.Fatalf("runBenchIngest text = (%v, %q)", textErr, textOut)
	}
	if jsonErr != nil || !strings.Contains(jsonOut, `"classes"`) {
		t.Fatalf("runBenchIngest json = (%v, %q)", jsonErr, jsonOut)
	}

	// doctor_report probe wrappers on missing files return cleanly or error without panicking.
	missing := filepath.Join(t.TempDir(), "missing.db")
	_, _ = doctorProbeKiro(missing)
	_, _ = doctorProbeKiro(sources.KiroDB())
	_, _ = doctorProbeDevin(missing)
	_, _ = doctorProbeMimo(missing)
	_, _ = doctorProbeZCode(missing)
	_, _ = doctorProbeZCode(sources.ZCodeDB())
	_, _ = doctorProbeKilo(missing)
	_, _ = doctorProbeKilo(sources.KiloDB())

	// cli_help_empty grammar & diagnostic helpers.
	if pluralNote(1) != "the promoted note" || pluralNote(2) != "the promoted notes" {
		t.Fatal("unexpected pluralNote output")
	}
	if !strings.Contains(forgetTitleFix(fs.ErrPermission), "permissions") ||
		!strings.Contains(forgetTitleFix(syscall.ENOSPC), "space") ||
		forgetTitleFix(errors.New("other")) != "clear the problem above" {
		t.Fatal("unexpected forgetTitleFix output")
	}
	if got := strings.Join(sortedHarnesses(map[string]index.HarnessIngest{"b": {}, "a": {}}), ","); got != "a,b" {
		t.Fatalf("sortedHarnesses = %q", got)
	}
	if pluralSessionWord(1) != "a session" || pluralSessionWord(3) != "sessions" {
		t.Fatal("unexpected pluralSessionWord output")
	}
	if pluralWhich(1) != "it" || pluralWhich(2) != "them" {
		t.Fatal("unexpected pluralWhich output")
	}

	// install_json_mcp list & entry replacement helpers.
	if naturalList(nil) != "" || naturalList([]string{"a"}) != "a" ||
		naturalList([]string{"a", "b"}) != "a and b" ||
		naturalList([]string{"a", "b", "c"}) != "a, b, and c" {
		t.Fatal("unexpected naturalList output")
	}
	entry := map[string]any{"command": "/new/deja", "args": []any{"mcp"}}
	if replacedEntryNote(nil, entry) != "" || replacedEntryNote(entry, entry) != "" {
		t.Fatal("unexpected replacedEntryNote for nil/matching entry")
	}
	if replacedEntryNote(map[string]any{"x": 1}, entry) != "replaced the deja entry that was already here" {
		t.Fatal("unexpected replacedEntryNote for commandless entry")
	}
	if got := replacedEntryNote(map[string]any{"command": "/old/deja"}, entry); !strings.Contains(got, "/old/deja") {
		t.Fatalf("unexpected replacedEntryNote for old command: %q", got)
	}

	// hook_junie catch-up and failure dedup branches.
	dir := t.TempDir()
	junieCatchUpCompaction(dir, "missing-session", dir, sources.JunieTurnState{})
	junieCatchUpCompaction(dir, "missing-session", dir, sources.JunieTurnState{
		CompactionID: "c1",
		CompactionAt: time.Now(),
	})
	if got := junieFailurePair(dir, "s1", dir, sources.JunieTurnState{}); got != "" {
		t.Fatalf("empty failure state returned %q", got)
	}
	st := sources.JunieTurnState{FailureID: "f1", Failure: "exit 1", FailureCommand: "make test"}
	_ = junieFailurePair(dir, "s1", dir, st)
	if dup := junieFailurePair(dir, "s1", dir, st); dup != "" {
		t.Fatalf("expected deduped second failure call to return empty, got %q", dup)
	}

	// bench_read CLI + printReadReport + readReport skipped/non-skipped branches.
	if err := runBenchRead([]string{"--bogus"}); err == nil {
		t.Fatal("expected error for bogus read bench flag")
	}
	var readTextErr, readJSONErr error
	readTextOut := captureStdoutCall(t, func() { readTextErr = runBenchRead([]string{"--seed", "1"}) })
	readJSONOut := captureStdoutCall(t, func() { readJSONErr = runBenchRead([]string{"--json", "--seed", "1"}) })
	if readTextErr != nil || !strings.Contains(readTextOut, "deja bench read") {
		t.Fatalf("runBenchRead text = (%v, %q)", readTextErr, readTextOut)
	}
	if readJSONErr != nil || !strings.Contains(readJSONOut, `"corpus_hash"`) {
		t.Fatalf("runBenchRead json = (%v, %q)", readJSONErr, readJSONOut)
	}
	buf.Reset()
	printReadReport(&buf, readReport{Skipped: "no sqlite3"})
	if !strings.Contains(buf.String(), "skipped: no sqlite3") {
		t.Fatalf("unexpected skipped read report: %q", buf.String())
	}
	buf.Reset()
	printReadReport(&buf, readReport{
		CorpusHash: "abc", Seed: 1, Ratio: 12.5,
		Classes: []readClass{{Name: "ordinary", WallMS: 1.0, StoreMB: 0.1, Sessions: 2, Messages: 4}},
	})
	if !strings.Contains(buf.String(), "12.5x") {
		t.Fatalf("unexpected read report: %q", buf.String())
	}

	// parseDoctor* wrappers (dispatcher branches for missing/synthetic paths).
	_, _ = parseDoctorClaude(filepath.Join(dir, "history.jsonl"))
	_, _ = parseDoctorClaude(filepath.Join(dir, "session.jsonl"))
	_, _ = parseDoctorTrae(filepath.Join(dir, "history.jsonl"))
	_, _ = parseDoctorTrae(filepath.Join(dir, "rollout.jsonl"))
	_, _ = parseDoctorOpenClaw(filepath.Join(dir, "openclaw-agent.sqlite"))
	_, _ = parseDoctorOpenClaw(filepath.Join(dir, "session.jsonl"))
	_, _ = parseDoctorGoose(filepath.Join(dir, "sessions.db"))
	_, _ = parseDoctorGoose(filepath.Join(dir, "session.jsonl"))
	_, _ = parseDoctorHermes(filepath.Join(dir, "state.db"))

	// printAuditReport all formatting branches (>6 stores, retention modes, claude-mem, wired/unwired).
	buf.Reset()
	printAuditReport(&buf, auditReport{})
	if !strings.Contains(buf.String(), "none detected") || !strings.Contains(buf.String(), "not wired") {
		t.Fatalf("unexpected empty audit report:\n%s", buf.String())
	}
	buf.Reset()
	printAuditReport(&buf, auditReport{
		Stores: []auditStoreRow{
			{Name: "claude", Files: 1}, {Name: "codex", Files: 1}, {Name: "cursor", Files: 1},
			{Name: "gemini", Files: 1}, {Name: "opencode", Files: 1}, {Name: "goose", Files: 1},
			{Name: "kilo", Files: 2},
		},
		TotalFiles: 8, TotalBytes: 2048,
		ClaudeRetention: auditClaudeRetention{
			CleanupPeriodDays: 14, Explicit: true, TotalFiles: 5,
			PastPeriod: 1, ExpiringIn7Days: 1, PreservedDeleted: 2,
		},
		ClaudeMemDBPresent: true, ClaudeMemDBBytes: 4096,
		IndexedSessions: 5, RecoverableFiles: 3,
		WiredTargets: []string{"claude", "codex"},
	})
	if !strings.Contains(buf.String(), "+1 more") || !strings.Contains(buf.String(), "claude-mem.db") {
		t.Fatalf("unexpected populated audit report:\n%s", buf.String())
	}
	buf.Reset()
	printAuditReport(&buf, auditReport{
		TotalFiles: 3,
		ClaudeRetention: auditClaudeRetention{
			CleanupPeriodDays: 0, Explicit: true, TotalFiles: 3,
		},
		IndexedSessions: 2,
		NextCommand:     "deja install --auto",
	})
	if !strings.Contains(buf.String(), "disabled") || !strings.Contains(buf.String(), "deja install --auto") {
		t.Fatalf("unexpected disabled-retention audit report:\n%s", buf.String())
	}

	// changes_block, delete, and doctor_zed_reach pure helpers.
	if clipToFit("abc", 10) != "abc" || !strings.HasSuffix(clipToFit(strings.Repeat("x", 30), 10), "…\n") {
		t.Fatal("unexpected clipToFit result")
	}
	if clipChange("\nabc\n", 10) != "abc" || !strings.HasSuffix(clipChange(strings.Repeat("y", 20), 5), "…") {
		t.Fatal("unexpected clipChange result")
	}
	parent := model.Session{ID: "parent-1"}
	if !childOf(model.Session{Parent: "parent-1"}, parent) ||
		!childOf(model.Session{Path: filepath.Join(dir, "session-parent-1", "a.jsonl")}, parent) ||
		childOf(model.Session{Path: filepath.Join(dir, "unrelated", "a.jsonl")}, parent) {
		t.Fatal("unexpected childOf result")
	}
	if !dirOfSession("session_id1", "id1") || dirOfSession("work-id1", "id1") {
		t.Fatal("unexpected dirOfSession result")
	}
	if dirs := zedExtensionDirs(); len(dirs) == 0 {
		t.Fatal("zedExtensionDirs returned empty slice")
	}
	zedSettings := filepath.Join(dir, "zed-settings.json")
	if err := os.WriteFile(zedSettings, []byte(`{"context_servers":{"deja-context-server":{"command":"/bin/deja","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !zedEntryHasCommand(zedSettings) || zedEntryHasCommand(filepath.Join(dir, "nope.json")) {
		t.Fatal("unexpected zedEntryHasCommand result")
	}
}

// OpenClaw loads a non-bundled plugin only when a non-empty plugins.allow
// names it, and none at all under plugins.enabled: false. Install and doctor
// called deja's plugin wired while `openclaw plugins list` had it disabled.
func TestOpenClawPluginOffByAllowListOrMasterSwitch(t *testing.T) {
	for _, tc := range []struct {
		name, plugins, want string
	}{
		{"allow list without deja", `{"allow": ["my-plugin"]}`, "plugins.allow"},
		{"allow list with padded names", `{"allow": [" my-plugin ", ""]}`, "plugins.allow"},
		{"plugins switched off", `{"enabled": false}`, "plugins.enabled"},
		{"allow list naming deja", `{"allow": ["my-plugin", " deja "]}`, ""},
		{"empty allow list", `{"allow": []}`, ""},
		{"allow list of empty names", `{"allow": ["", "  "]}`, ""},
		{"memory slot names deja", `{"allow": ["x"], "slots": {"memory": "deja"}}`, ""},
		{"context engine slot names deja", `{"allow": ["x"], "slots": {"contextEngine": "deja"}}`, ""},
		{"plugins on", `{"enabled": true}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{\n  \"plugins\": "+tc.plugins+"\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := captureRun(t, "install", "openclaw-auto", "--no-index")
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			var row *doctorAutoStatus
			for _, st := range collectDoctorAutoRecall() {
				if st.Name == "openclaw" {
					st := st
					row = &st
				}
			}
			if row == nil {
				t.Fatal("no openclaw row in doctor's auto recall")
			}
			doctor, _ := captureRun(t, "doctor")
			if tc.want == "" {
				if row.SwitchedOff || strings.Contains(out, "switched off") || strings.Contains(out, "blocked") {
					t.Fatalf("plugin is on, but reported off:\n%s\nswitched_off=%v", out, row.SwitchedOff)
				}
				return
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("install did not say %s keeps the plugin off:\n%s", tc.want, out)
			}
			if !row.SwitchedOff {
				t.Errorf("doctor auto recall: openclaw row not switched off: %+v", *row)
			}
			if !strings.Contains(doctor, tc.want) {
				t.Errorf("doctor text did not name %s:\n%s", tc.want, doctor)
			}
		})
	}
}

// Qwen frames a command's output as a labelled report before handing it to the
// model. The "Output:" label in front of the first line is what stopped a build
// failure reading as an error, so the fix pair said nothing about a failure the
// store had already settled.
func TestQwenShellReportIsUnwrapped(t *testing.T) {
	raw := json.RawMessage(`{"llmContent":"Command: go build ./...\nDirectory: (root)\nOutput: ./main.go:12:2: undefined: zorbquuxHelper\nError: (none)\nExit Code: 0\nSignal: 0\nProcess Group PGID: 2726"}`)
	got := strings.TrimSpace(toolResponseText(raw))
	if got != "./main.go:12:2: undefined: zorbquuxHelper" {
		t.Errorf("qwen's shell report was not unwrapped: %q", got)
	}
}

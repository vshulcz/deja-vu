package main

import (
	"bytes"
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
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

// The install used to end with a placeholder — `deja "something you fixed weeks
// ago"` — which leaves the newcomer to invent a question at the moment they know
// least about what is in there. The prompt it prints instead is built from their
// own history and run before it is shown, because a prompt that misses on the
// first try is worse than none (#575).
func TestTheInstallPromptComesFromHistoryAndHasBeenRun(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-work-api", "a.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"pgbouncer prepared statements keep failing under the pool"}}`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"we decided to run the pooler in transaction mode and turn the driver cache off"}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(root, "-work-api", "b.jsonl"), "s2", []string{
		`{"type":"user","sessionId":"s2","timestamp":"2026-07-14T09:00:00Z","message":{"role":"user","content":"pgbouncer prepared statements again after the driver upgrade"}}`,
		`{"type":"assistant","sessionId":"s2","timestamp":"2026-07-14T09:20:00Z","message":{"role":"assistant","content":[{"type":"text","text":"the fix was to keep transaction mode and leave the driver cache off"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	p, ok := buildTryPrompt(dir)
	if !ok {
		t.Fatal("no prompt was built from a history that holds the same topic twice, with a decision")
	}
	if !strings.Contains(p.Question, "pgbouncer") {
		t.Errorf("question = %q, want the topic the sessions share", p.Question)
	}
	if !strings.HasPrefix(p.Question, "what did we decide about ") || !strings.HasSuffix(p.Question, "?") {
		t.Errorf("question = %q, want a question", p.Question)
	}
	if p.Sessions < 2 {
		t.Errorf("sessions = %d, want the several the topic appears in", p.Sessions)
	}

	var out bytes.Buffer
	printTryPrompt(&out, p)
	got := out.String()
	if !strings.Contains(got, "paste this into your agent") || !strings.Contains(got, p.Question) {
		t.Errorf("the line is not pasteable:\n%s", got)
	}
	// The provenance is the part that says this is their own work rather than
	// an example out of a README.
	if !strings.Contains(got, "picked from your history") || !strings.Contains(got, "Jun 2026 – Jul 2026") {
		t.Errorf("the provenance line is wrong:\n%s", got)
	}
}

// A store with nothing to build a prompt from must not produce one: the
// placeholder is the honest fallback. Each of these is a gate that was missing
// at some point while this was written.
func TestNoPromptWithoutSeveralSessionsAndADecision(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string][]string
	}{
		{
			// A topic nothing decided answers the question with the question.
			name: "one session, no decision",
			files: map[string][]string{"a.jsonl": {
				`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"pgbouncer prepared statements keep failing under the pool"}}`,
				`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"looking at the pool configuration now"}]}}`,
			}},
		},
		{
			// One session that settled something is a fact about one session.
			// The prompt claims the topic runs through their history, so two is
			// the floor — otherwise the agent answers from the single session
			// the reader happens to be looking at.
			name: "a decision, but only one session on the topic",
			files: map[string][]string{"a.jsonl": {
				`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"quaxbolt overflow in the ledger importer"}}`,
				`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"we decided to cast the quaxbolt counter to int64 before the ledger sum"}]}}`,
			}},
		},
		{
			// Two sessions on one topic and nothing settled in either: the
			// question says "what did we decide", so this would answer with the
			// question again.
			name: "several sessions, no decision in any of them",
			files: map[string][]string{
				"a.jsonl": {
					`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"quaxbolt overflow in the ledger importer"}}`,
					`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"reading the importer now to see where the counter comes from"}]}}`,
				},
				"b.jsonl": {
					`{"type":"user","sessionId":"s2","timestamp":"2026-07-02T12:00:00Z","message":{"role":"user","content":"quaxbolt overflow again in the ledger importer"}}`,
					`{"type":"assistant","sessionId":"s2","timestamp":"2026-07-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"still reading; the counter is summed in two places"}]}}`,
				},
			},
		},
		{
			// Two sessions, a decision in both, and the first line of each is a
			// pasted command: a topic lifted out of one reads as noise, which is
			// what the first candidate on a real store was.
			name: "the topic would come from a pasted command",
			files: map[string][]string{
				"a.jsonl": {
					`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"ssh -o ConnectTimeout=25 -p 2222 deploy@example.com restart the importer"}}`,
					`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"we decided to restart the importer from the deploy host"}]}}`,
				},
				"b.jsonl": {
					`{"type":"user","sessionId":"s2","timestamp":"2026-07-02T12:00:00Z","message":{"role":"user","content":"ssh -o ConnectTimeout=25 -p 2222 deploy@example.com restart the importer again"}}`,
					`{"type":"assistant","sessionId":"s2","timestamp":"2026-07-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"the fix was to restart the importer from the deploy host again"}]}}`,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			root := os.Getenv("DEJA_CLAUDE_ROOT")
			for name, lines := range tc.files {
				writeClaudeFixture(t, filepath.Join(root, "-work-api", name), name, lines)
			}
			dir := index.DefaultDir()
			if err := index.Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			if p, ok := buildTryPrompt(dir); ok {
				t.Errorf("a prompt was built anyway: %q over %d sessions", p.Question, p.Sessions)
			}
		})
	}
}

// What counts as a topic, and what does not. The rejections are all shapes a
// real store handed over: a pasted ssh command as the first line of a session,
// a path, a bare number.
func TestATopicIsWhatSomebodyWouldSayOutLoud(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
		want  string
	}{
		{"content words in the order written", "tighten the pgbouncer prepared statement pool", "tighten pgbouncer prepared statement pool"},
		{"stop words and short words go", "why is the ci job so slow now", "job slow now"},
		{"a path is not a topic", "fix internal/index/sync.go watermark handling", "fix watermark handling"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(promptTopicWords(tc.title), " "); got != tc.want {
				t.Errorf("topic = %q, want %q", got, tc.want)
			}
		})
	}
	if got := promptTopicWords("deploy"); got != nil {
		t.Errorf("one word is not a topic: %q", got)
	}
	for _, pasted := range []string{
		"ssh -o ConnectTimeout=25 -p 2222 root@example.com and restart the box",
		"https://example.com/thread/12 says the opposite",
		"cat log | grep timeout",
	} {
		if !pastedHeadline(pasted) {
			t.Errorf("a pasted line was taken for a topic: %q", pasted)
		}
	}
	if pastedHeadline("tighten the pgbouncer prepared statement pool") {
		t.Error("a sentence someone typed was taken for a paste")
	}
}

func TestThePromptSpanReadsAsOneMonthWhenItIsOne(t *testing.T) {
	jun := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	if got := promptSpan(jun, jun.Add(48*time.Hour)); got != "Jun 2026" {
		t.Errorf("span = %q, want one month", got)
	}
	if got := promptSpan(time.Time{}, jun); got != "" {
		t.Errorf("span = %q, want nothing when a date is missing", got)
	}
}

// Claude Code wraps pasted text in <pasted_content id="…">, and on a machine
// whose sessions mostly open with a paste the install asked "what did we
// decide about pasted_content set?" and listed a session as
// `<pasted_content id="dd73"> Set up and test…`. The wrapper is the host's
// markup: the topic, the preview and the index hold only the pasted text.
func TestPasteWrapperIsNotATopicATitleOrATerm(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-work-api", "a.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T12:00:00Z","message":{"role":"user","content":"<pasted_content id=\"dd73\">\npgbouncer prepared statements keep failing under the pool\n</pasted_content id=\"dd73\">"}}`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"we decided to run the pooler in transaction mode and turn the driver cache off"}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(root, "-work-api", "b.jsonl"), "s2", []string{
		`{"type":"user","sessionId":"s2","timestamp":"2026-07-14T09:00:00Z","message":{"role":"user","content":"<pasted_content id=\"a1f0\">\npgbouncer prepared statements again after the driver upgrade\n</pasted_content id=\"a1f0\">"}}`,
		`{"type":"assistant","sessionId":"s2","timestamp":"2026-07-14T09:20:00Z","message":{"role":"assistant","content":[{"type":"text","text":"the fix was to keep transaction mode and leave the driver cache off"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	p, ok := buildTryPrompt(dir)
	if !ok {
		t.Fatal("no prompt was built")
	}
	if strings.Contains(p.Question, "pasted_content") || !strings.Contains(p.Question, "pgbouncer") {
		t.Errorf("question = %q, want the pasted text's topic", p.Question)
	}

	recent, err := index.Recent(dir, 10)
	if err != nil || len(recent) != 2 {
		t.Fatalf("recent: %v, %d", err, len(recent))
	}
	for _, s := range recent {
		if title := sessionHeadline(s); strings.Contains(title, "pasted_content") || !strings.HasPrefix(title, "pgbouncer prepared statements") {
			t.Errorf("preview = %q, want the pasted text", title)
		}
	}

	result, err := index.SearchWithRecoveryDetailed(dir, query.Options{Query: "pasted_content", All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if hits := matchedSessions(result, result.Sessions); len(hits) != 0 {
		t.Errorf("the wrapper was indexed as a term: %d sessions match it", len(hits))
	}
}

// The writers read the old config with `old, _ := os.ReadFile`, so a file that
// exists but cannot be read looked exactly like one that is not there and deja
// wrote a fresh config over it. backupOnce caught that by accident — until a
// `.bak` was already beside the file, which is the ordinary state after any
// earlier install, and then the user's config went with nothing left of it
// (#2751).
func TestAConfigThatCannotBeReadIsNotOverwritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not take a file's read away here")
	}
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "{\"mcpServers\":{\"mine\":{\"command\":\"x\"}}}\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	// A snapshot from an earlier install: backupOnce takes one per file per
	// run, so with this here it never reads the config at all.
	if err := os.WriteFile(path+".bak", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	_, err := captureRun(t, "install", "cursor", "--no-index")
	if err == nil {
		t.Error("a config deja could not read was reported as installed")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal does not name the config: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(b) != mine {
		t.Errorf("the config deja could not read was written over:\n%s", b)
	}
}

// And the same on the way out: what cannot be read cannot be edited down to
// "the file without deja in it" either.
func TestUninstallDoesNotWriteOverAConfigItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not take a file's read away here")
	}
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".bak", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if _, err := captureRun(t, "uninstall", "cursor"); err == nil {
		t.Error("a config deja could not read was reported as unwired")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal does not name the config: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("the config was written over on the way out:\n%s", after)
	}
}

// A target that wires more than one file has to be refused on the file that
// cannot be read, not on whichever one it happens to report: `uninstall
// claude-auto` read no entry to remove from an unreadable ~/.claude.json and
// said the target was unwired while the file still named deja.
func TestAMultiFileTargetIsRefusedOnTheFileItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not take a file's read away here")
	}
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := sources.ClaudeJSONPath()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if _, err := captureRun(t, "uninstall", "claude-auto"); err == nil {
		t.Error("a target was reported unwired while a config it could not read still names deja")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("the config was written over:\n%s", after)
	}
}

// The sync timer's unit is read only to decide whether the job is loaded; the
// removal itself is by path. Refusing to read it left a launchd job running
// against a binary the user was removing.
func TestUninstallTakesOutASyncUnitItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not take a file's read away here")
	}
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "sync-timer", "--no-index"); err != nil {
		t.Fatal(err)
	}
	unit := syncAutoPlistPath()
	if runtime.GOOS != "darwin" {
		unit = filepath.Join(syncAutoUnitDir(), "deja-sync.timer")
	}
	if _, err := os.Stat(unit); err != nil {
		t.Skipf("no unit written here: %v", err)
	}
	if err := os.Chmod(unit, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unit, 0o644) })

	if _, err := captureRun(t, "uninstall", "sync-timer"); err != nil {
		t.Fatalf("uninstall refused a unit it only had to remove: %v", err)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Errorf("the unit survived the uninstall")
	}
}

// Copilot Chat has no hook, so the only thing that can sit in front of the model
// before it reads the question is a custom-instructions file. VS Code applies
// one to every chat when its frontmatter says `applyTo: "**"` — measured on
// 1.134.0, with this file alone in the User folder's prompts directory the agent
// called deja on a question that had nothing to do with past work.
func TestVSCodeGuidanceIsAnAlwaysAppliedInstructionsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", dir)

	path := guidancePath("vscode")
	if want := filepath.Join(dir, "prompts", "deja.instructions.md"); path != want {
		t.Fatalf("guidance goes to %q, want %q — VS Code reads the prompts folder", path, want)
	}
	text := guidanceText("vscode")
	if !strings.HasPrefix(text, "---\napplyTo: \"**\"\n---") {
		t.Errorf("no applyTo frontmatter, so the file applies to nothing:\n%s", text)
	}
	if strings.Contains(text, "name: deja-history") {
		t.Errorf("wrote skill frontmatter into an instructions file:\n%s", text)
	}
	// The block is only worth having if it names the thing to call.
	if !strings.Contains(text, "deja") {
		t.Errorf("the instructions never mention deja:\n%s", text)
	}
}

// install writes it, and uninstall takes it away: a stale always-applied file
// would keep telling the agent to call a server that is no longer wired.
func TestVSCodeGuidanceIsWrittenAndRemoved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, "User")
	t.Setenv("DEJA_VSCODE_USER_DIRS", dir)

	if _, err := guidanceResult("vscode", false); err != nil {
		t.Fatalf("guidance install: %v", err)
	}
	path := filepath.Join(dir, "prompts", "deja.instructions.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("guidance not written where VS Code reads it: %v", err)
	}
	if !strings.Contains(string(b), "applyTo") {
		t.Errorf("the written file has no applyTo:\n%s", b)
	}

	if _, err := guidanceResult("vscode", true); err != nil {
		t.Fatalf("guidance uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the always-applied file survived uninstall: %v", err)
	}
}

// Three hosts written, one named: the result of the last host was the whole
// answer, so Code and Insiders were updated in silence (#3233).
func TestInstallVSCodeNamesEveryHostItWrites(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", a+string(os.PathListSeparator)+b)
	res, err := installVSCodeMCP("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	all := res.Path + " " + res.Note
	for _, dir := range []string{a, b} {
		if !strings.Contains(all, filepath.Join(dir, "mcp.json")) && !strings.Contains(all, shortHome(filepath.Join(dir, "mcp.json"))) {
			t.Fatalf("host %s not named: path=%q note=%q", dir, res.Path, res.Note)
		}
	}
}

func TestInstallRooNamesEveryHostItWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{
		rooStorage(t, home, "Code"),
		rooStorage(t, home, "Cursor"),
	}, string(os.PathListSeparator)))
	res, err := installRoo("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	all := res.Path + " " + res.Note
	for _, host := range []string{"Code", "Cursor"} {
		if !strings.Contains(all, host) {
			t.Fatalf("host %s not named: path=%q note=%q", host, res.Path, res.Note)
		}
	}
}

// vscode-auto puts deja's status item into VS Code as a local extension that
// runs `deja statusline`, named in extensions.json beside the reader's own
// extensions, and uninstall takes both out again.
func TestVSCodeAutoAddsTheStatusExtension(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("VSCODE_EXTENSIONS", "")
	extDir := filepath.Join(sources.Home(), ".vscode", "extensions")
	theirs := `[{"identifier":{"id":"someone.theirs"},"version":"2.0.0","relativeLocation":"someone.theirs-2.0.0"}]`
	writeRulesTestFile(t, filepath.Join(extDir, "extensions.json"), theirs)

	if _, err := installTarget("vscode-auto", "/opt/deja/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(extDir, "vshulcz.deja-status-1.0.0")
	js := readRulesTestFile(t, filepath.Join(dir, "extension.js"))
	if !strings.Contains(js, `["statusline"]`) || !strings.Contains(js, "createStatusBarItem") {
		t.Fatalf("extension.js does not run deja statusline into a status item:\n%s", js)
	}
	var pkg struct {
		Name       string   `json:"name"`
		Publisher  string   `json:"publisher"`
		Main       string   `json:"main"`
		Activation []string `json:"activationEvents"`
	}
	if err := json.Unmarshal([]byte(readRulesTestFile(t, filepath.Join(dir, "package.json"))), &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Publisher+"."+pkg.Name != vsCodeStatusID || pkg.Main != "./extension.js" || len(pkg.Activation) == 0 {
		t.Fatalf("package.json = %+v", pkg)
	}
	list := readRulesTestFile(t, filepath.Join(extDir, "extensions.json"))
	if !strings.Contains(list, `{"identifier":{"id":"someone.theirs"},"version":"2.0.0","relativeLocation":"someone.theirs-2.0.0"}`) ||
		!strings.Contains(list, `"id":"vshulcz.deja-status"`) || !strings.Contains(list, `"relativeLocation":"vshulcz.deja-status-1.0.0"`) {
		t.Fatalf("extensions.json = %s", list)
	}

	if r, err := installVSCodeStatusItem("/opt/deja/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install: %+v, %v", r, err)
	}

	if _, err := installTarget("vscode-auto", "/opt/deja/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the extension: %v", err)
	}
	if got := readRulesTestFile(t, filepath.Join(extDir, "extensions.json")); got != theirs {
		t.Fatalf("extensions.json after uninstall = %s, want the reader's list back", got)
	}
}

func TestVSCodeURIPath(t *testing.T) {
	if got := vsCodeURIPath("windows", `C:\Users\a b\.vscode\extensions\x`); got != "/c:/Users/a b/.vscode/extensions/x" {
		t.Errorf("windows: %q", got)
	}
	if got := vsCodeURIPath("linux", "/home/a/.vscode/extensions/x"); got != "/home/a/.vscode/extensions/x" {
		t.Errorf("linux: %q", got)
	}
}

func TestVSCodeForkStatusItemInstallsAndUninstallsCleanly(t *testing.T) {
	hermeticEnv(t)
	for _, tc := range []struct {
		target    string
		dotFolder string
	}{
		{"cursor-auto", ".cursor"},
		{"windsurf", ".windsurf"},
		{"kiro-auto", ".kiro"},
		{"antigravity-auto", ".antigravity"},
		{"trae-ide-auto", ".trae"},
	} {
		extDir := filepath.Join(sources.Home(), tc.dotFolder, "extensions")
		if err := os.MkdirAll(extDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := installTarget(tc.target, "/opt/deja/bin/deja", false); err != nil {
			t.Fatalf("%s install: %v", tc.target, err)
		}
		dir := filepath.Join(extDir, "vshulcz.deja-status-1.0.0")
		if _, err := os.Stat(filepath.Join(dir, "extension.js")); err != nil {
			t.Fatalf("%s did not write extension.js: %v", tc.target, err)
		}
		list := readRulesTestFile(t, filepath.Join(extDir, "extensions.json"))
		if !strings.Contains(list, `"id":"vshulcz.deja-status"`) {
			t.Fatalf("%s extensions.json = %s", tc.target, list)
		}
		if _, err := installTarget(tc.target, "/opt/deja/bin/deja", true); err != nil {
			t.Fatalf("%s uninstall: %v", tc.target, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s uninstall left the extension: %v", tc.target, err)
		}
	}
}

// VS Code Copilot Chat reads MCP servers from mcp.json under `servers` (not the
// common `mcpServers`), and each entry carries a `type`. A file in the other
// shape loads nothing and the chat gets no tools.
func TestInstallVSCodeMCPWritesTheServersShape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", dir)
	if _, err := installVSCodeMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "mcp.json"))
	if err != nil {
		t.Fatalf("mcp.json not written where VS Code reads it: %v", err)
	}
	var root struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("mcp.json is not valid JSON: %v\n%s", err, b)
	}
	d, ok := root.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server under the servers key:\n%s", b)
	}
	if d.Type != "stdio" {
		t.Errorf("VS Code needs an explicit type, got %q", d.Type)
	}
	if d.Command == "" || len(d.Args) == 0 || d.Args[len(d.Args)-1] != "mcp" {
		t.Errorf("the server does not launch `deja mcp`: %+v", d)
	}
	if strings.Contains(string(b), "mcpServers") {
		t.Errorf("wrote the mcpServers key VS Code does not read:\n%s", b)
	}
}

// A machine can run more than one VS Code, and other people keep their own
// servers. install writes each host that exists and leaves every other server
// alone; uninstall takes only deja's.
func TestInstallVSCodeMCPPreservesOtherServersAndHosts(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", a+string(os.PathListSeparator)+b)
	// One host already has a server of the user's own.
	theirs := `{"servers":{"other":{"type":"stdio","command":"x","args":[]}}}`
	if err := os.WriteFile(filepath.Join(a, "mcp.json"), []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installVSCodeMCP("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, dir := range []string{a, b} {
		body, err := os.ReadFile(filepath.Join(dir, "mcp.json"))
		if err != nil {
			t.Fatalf("%s: mcp.json not written: %v", dir, err)
		}
		if !strings.Contains(string(body), `"deja"`) {
			t.Errorf("%s: deja not wired:\n%s", dir, body)
		}
	}
	if body, _ := os.ReadFile(filepath.Join(a, "mcp.json")); !strings.Contains(string(body), `"other"`) {
		t.Errorf("install dropped the user's own server:\n%s", body)
	}

	if _, err := installVSCodeMCP("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(a, "mcp.json"))
	if strings.Contains(string(body), `"deja"`) {
		t.Errorf("uninstall left deja behind:\n%s", body)
	}
	if !strings.Contains(string(body), `"other"`) {
		t.Errorf("uninstall took the user's own server with it:\n%s", body)
	}
}

// VS Code writes mcp.json with comments; a strict parse would refuse a file the
// editor itself produced.
func TestInstallVSCodeMCPKeepsComments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", dir)
	jsonc := "{\n  // my servers\n  \"servers\": {}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installVSCodeMCP("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "mcp.json"))
	if !strings.Contains(string(body), "// my servers") {
		t.Errorf("the comment was lost:\n%s", body)
	}
	if !strings.Contains(string(body), `"deja"`) {
		t.Errorf("deja not wired into the commented file:\n%s", body)
	}
}

// No VS Code User folder is not an error: say so rather than write a file into a
// directory the editor will never read.
func TestInstallVSCodeMCPWithoutAUserFolder(t *testing.T) {
	t.Setenv("DEJA_VSCODE_USER_DIRS", filepath.Join(t.TempDir(), "does-not-exist"))
	// The override names a dir that does not exist; installVSCodeMCP still writes
	// there because the override is explicit. The empty case is the resolver
	// returning nothing, which the override cannot express, so this checks the
	// explicit path writes rather than the empty-note path — the note path is
	// covered by the resolver returning [] on a machine with no VS Code.
	if _, err := installVSCodeMCP("/bin/deja", false); err != nil {
		t.Fatalf("install into an explicit dir: %v", err)
	}
}

// goose-auto writes its recall block during install, and that read asked for
// a detached build: install then waited on that build's lock, built nothing
// itself and printed "index: built (0 sessions, 0 messages)" over a full store.
// Install builds the index right after its targets, so nothing a target does
// on the way starts another (#4268).
func TestInstallStartsNoBuildOfItsOwnIndexOnTheSide(t *testing.T) {
	tmp := hermeticEnv(t)
	// hermeticEnv turns warmups off; this test is about one, stubbed below.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claude, "alpha", "one.jsonl"), "c1", []string{
		`{"type":"user","sessionId":"c1","timestamp":"` + at +
			`","message":{"role":"user","content":"why does the retry loop spin"}}`,
	})
	spawned := 0
	saved := spawnWarmup
	spawnWarmup = func(_, _ string) error { spawned++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })

	_, said := captureBoth(t, "install", "goose-auto")
	if spawned != 0 {
		t.Errorf("install started %d background builds beside its own", spawned)
	}
	if !strings.Contains(said, "index: built (1 session") {
		t.Errorf("the build line does not count the store:\n%s", said)
	}
}

// A build another process finished while install waited on its lock left
// this process's own count at zero; the line reads the store (#4268).
func TestInstallBuiltLineCountsAStoreAnotherDejaBuilt(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	writeClaudeFixture(t, filepath.Join(claude, "alpha", "one.jsonl"), "c1", []string{
		`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"why does the retry loop spin"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	saved := index.LastBuild
	index.LastBuild = index.BuildSummary{}
	t.Cleanup(func() { index.LastBuild = saved })
	if got := installBuiltLine(index.DefaultDir()); !strings.Contains(got, "(1 session") {
		t.Errorf("line = %q, want the store's one session", got)
	}
}

func TestMCPCommandArgsPerOS(t *testing.T) {
	command, args := mcpCommandArgs("/usr/local/bin/deja")
	if runtime.GOOS == "windows" {
		if command != "cmd" || len(args) != 3 || args[0] != "/c" || args[2] != "mcp" {
			t.Fatalf("windows form = %q %v", command, args)
		}
	} else {
		if command != "/usr/local/bin/deja" || len(args) != 1 || args[0] != "mcp" {
			t.Fatalf("unix form = %q %v", command, args)
		}
	}
	entry := mcpServerEntry("/usr/local/bin/deja")
	if entry["type"] != "stdio" || entry["command"] != command {
		t.Fatalf("entry = %#v", entry)
	}
}

func windsurfTestRoot(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "windsurf")
	t.Setenv("DEJA_WINDSURF_ROOT", root)
	return root
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// install windsurf writes the guidance block into Cascade's global rules, the
// skill into its skill root, and /deja as a global workflow; uninstall takes
// all three back.
func TestInstallWindsurfWritesRuleSkillAndWorkflow(t *testing.T) {
	root := windsurfTestRoot(t)
	rules := filepath.Join(root, "memories", "global_rules.md")
	skill := filepath.Join(root, "skills", "deja-history", "SKILL.md")
	workflow := filepath.Join(root, "global_workflows", "deja.md")

	if _, err := installWindsurf("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got := readFileT(t, rules); got != guidanceText("append") {
		t.Fatalf("global_rules.md:\n%s", got)
	}
	if got := readFileT(t, skill); got != skillFile(skillBody) {
		t.Fatalf("skill:\n%s", got)
	}
	if r, err := installCommandFile("windsurf", "/bin/deja", false); err != nil || r.Path != workflow {
		t.Fatalf("workflow = %+v, %v", r, err)
	}
	if got := readFileT(t, workflow); got != windsurfWorkflow("/bin/deja") {
		t.Fatalf("workflow:\n%s", got)
	}
	if ownGuidanceFile("windsurf") != skill || guidanceStatus("windsurf") != "written" {
		t.Fatalf("guidance status = %s", guidanceStatus("windsurf"))
	}

	// Again: nothing changes.
	r, err := installWindsurfRules(false)
	if err != nil || r.Action != "unchanged" {
		t.Fatalf("second rules install = %+v, %v", r, err)
	}
	if r, err := installSkillFile(windsurfSkillPath(), false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second skill install = %+v, %v", r, err)
	}
	if r, err := installCommandFile("windsurf", "/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second workflow install = %+v, %v", r, err)
	}

	if _, err := installWindsurf("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile("windsurf", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{rules, skill, workflow} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there after uninstall", p)
		}
	}
}

// The user's own rules come back byte for byte, CRLF and all, and only deja's
// block goes on uninstall.
func TestInstallWindsurfKeepsTheUsersRules(t *testing.T) {
	root := windsurfTestRoot(t)
	rules := filepath.Join(root, "memories", "global_rules.md")
	if err := os.MkdirAll(filepath.Dir(rules), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "# My rules\r\n\r\n- tabs, not spaces — always\r\n- café stays NFC\r\n"
	if err := os.WriteFile(rules, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installWindsurfRules(false); err != nil {
		t.Fatal(err)
	}
	got := readFileT(t, rules)
	if !strings.HasPrefix(got, strings.TrimRight(mine, "\r\n")) || !strings.Contains(got, guidanceStart+"\r\n") {
		t.Fatalf("install changed the user's rules or the line endings:\n%q", got)
	}
	if r, err := installWindsurfRules(false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %+v, %v", r, err)
	}
	if _, err := installWindsurfRules(true); err != nil {
		t.Fatal(err)
	}
	if got := readFileT(t, rules); got != mine {
		t.Fatalf("uninstall did not give the file back as it was:\n%q\nwant\n%q", got, mine)
	}
}

// Cascade drops what is past 6,000 characters, and the user's rules count
// toward it. A file that would pass the cap with the block is left alone and
// install says why; doctor says the same.
func TestInstallWindsurfRefusesRulesPastTheCap(t *testing.T) {
	root := windsurfTestRoot(t)
	rules := filepath.Join(root, "memories", "global_rules.md")
	if err := os.MkdirAll(filepath.Dir(rules), 0o755); err != nil {
		t.Fatal(err)
	}
	// Multi-byte runes: the cap is characters, not bytes, so this one fits
	// with room even though it is over 6,000 bytes.
	fits := strings.Repeat("é", windsurfRulesCap-len([]rune(guidanceText("append")))-10)
	if err := os.WriteFile(rules, []byte(fits), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := installWindsurfRules(false); err != nil || r.Action == "unchanged" || r.Note != "" {
		t.Fatalf("a file under the cap was refused: %+v, %v", r, err)
	}

	long := strings.Repeat("x", windsurfRulesCap-100) + "\n"
	if err := os.WriteFile(rules, []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := installWindsurfRules(false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "unchanged" || !strings.Contains(r.Note, "over Cascade's 6000") {
		t.Fatalf("result = %+v, want unchanged with a note about the cap", r)
	}
	if got := readFileT(t, rules); got != long {
		t.Fatal("install wrote past the cap")
	}
	if s := windsurfRulesState(); !strings.HasPrefix(s, "not written") {
		t.Fatalf("doctor state = %q", s)
	}
	// The rest of the install still happens.
	res, err := installWindsurf("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Note, "over Cascade's 6000") {
		t.Fatalf("install result does not carry the note: %+v", res)
	}
	if _, err := os.Stat(windsurfSkillPath()); err != nil {
		t.Fatal("the skill was not written beside the refused rules")
	}
}

// A start marker with no end cannot be bounded, so install refuses and names
// the file.
func TestInstallWindsurfRulesRefusesAnUnboundedBlock(t *testing.T) {
	root := windsurfTestRoot(t)
	rules := filepath.Join(root, "memories", "global_rules.md")
	if err := os.MkdirAll(filepath.Dir(rules), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules, []byte("mine\n"+guidanceStart+"\nhalf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installWindsurfRules(false); err == nil || !strings.Contains(err.Error(), rules) {
		t.Fatalf("err = %v", err)
	}
	if s := windsurfRulesState(); s != "unbounded" {
		t.Fatalf("doctor state = %q", s)
	}
}

// Cascade substitutes nothing into a workflow, so the file names the query in
// words, runs a search rather than handing the query to deja as its first
// word, and ends deja's flags before it.
func TestWindsurfWorkflowRunsASearch(t *testing.T) {
	body := commandFileText("windsurf", "/bin/deja")
	if !strings.Contains(body, `/bin/deja --framed search -- "<the text after /deja>"`) {
		t.Fatalf("workflow does not run a search:\n%s", body)
	}
	if strings.Contains(body, "$ARGUMENTS") || strings.Contains(body, `/bin/deja "`) {
		t.Fatalf("workflow carries a placeholder Cascade never fills, or the bare-query form:\n%s", body)
	}
	if !strings.HasPrefix(body, "---\ndescription: ") || !isOurCommandFile([]byte(body)) {
		t.Fatalf("workflow frontmatter:\n%s", body)
	}
}

// doctor names the rules block under the windsurf row.
func TestDoctorReportsWindsurfRules(t *testing.T) {
	windsurfTestRoot(t)
	if _, err := installWindsurf("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runDoctor(&out, nil, stubLookup("2.0.0", true), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "global_rules.md: written") {
		t.Fatalf("doctor does not report the rules block:\n%s", out.String())
	}
	if s := windsurfRulesState(); s != "written" {
		t.Fatalf("state = %q", s)
	}
	if err := os.Remove(windsurfRulesPath()); err != nil {
		t.Fatal(err)
	}
	if s := windsurfRulesState(); s != "missing" {
		t.Fatalf("state = %q", s)
	}
}

// Cascade reads ~/.codeium/windsurf/mcp_config.json; the servers already in
// it stay, and uninstall takes only deja's (#308).
func TestInstallWindsurfKeepsTheOtherServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_WINDSURF_ROOT", "")
	path := filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers": {"github": {"command": "gh-mcp", "args": []}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := installWindsurf("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != path {
		t.Fatalf("path = %q, want %q", res.Path, path)
	}
	assertMCPServerEntry(t, path)
	servers := func() map[string]any {
		var doc struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		b, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(b, &doc) != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return doc.MCPServers
	}
	if _, ok := servers()["github"]; !ok {
		t.Fatal("install dropped the server that was already there")
	}
	if _, err := installWindsurf("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	got := servers()
	if _, ok := got["github"]; !ok || len(got) != 1 {
		t.Fatalf("after uninstall the servers are %v, want github alone", got)
	}
}

// A trajectory that fails authentication is a warning with a count, not a
// shorter history nobody mentions.
func TestDoctorCountsWindsurfTrajectoriesThatFailAuthentication(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "windsurf")
	t.Setenv("DEJA_WINDSURF_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "cascade"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "..", "fixtures", "registry", "windsurf", "cascade")
	for i, name := range []string{"c4f3a2b1-7e6d-4c5b-9a8f-1e2d3c4b5a69", "8a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"} {
		b, err := os.ReadFile(filepath.Join(src, name+".pb"))
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			b[len(b)-1] ^= 0x01
		}
		if err := os.WriteFile(filepath.Join(root, "cascade", name+".pb"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := runDoctor(&out, nil, stubLookup("2.0.0", true), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "windsurf 1 trajectory failed authentication; those sessions are missing from recall") {
		t.Fatalf("doctor does not name the refused trajectory:\n%s", out.String())
	}
}

// WorkBuddy runs CodeBuddy's agent with CODEBUDDY_CONFIG_DIR set to its own
// home, so wiring ~/.codebuddy never reaches it. `deja install workbuddy-auto`
// writes the hooks and the server into the WorkBuddy home on disk, and the
// server into the app's own mcp.json: the agent reads only the first of
// .mcp.json and mcp.json, so a new .mcp.json would hide every server added in
// the app.
func TestInstallWorkBuddyAuto(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	wb := filepath.Join(home, ".workbuddy-ai")
	if err := os.MkdirAll(filepath.Join(wb, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(wb, "settings.json")
	own := []byte(`{"proxy":{"mode":"system"}}`)
	if err := os.WriteFile(settings, own, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installTarget("workbuddy-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := readCodeBuddyJSON(t, settings)
	hooks, _ := root["hooks"].(map[string]any)
	want := map[string]int{}
	for _, w := range codeBuddyHookWiring {
		want[w.Event]++
	}
	for event, n := range want {
		if entries, _ := hooks[event].([]any); len(entries) != n {
			t.Fatalf("%s: %d entries, want %d", event, len(entries), n)
		}
	}
	if root["proxy"] == nil {
		t.Fatalf("the app's settings were not kept: %v", root)
	}
	servers, _ := readCodeBuddyJSON(t, filepath.Join(wb, "mcp.json"))["mcpServers"].(map[string]any)
	if servers["deja"] == nil {
		t.Fatalf("mcp.json servers = %v", servers)
	}
	for _, p := range []string{filepath.Join(wb, ".mcp.json"), filepath.Join(home, ".codebuddy")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("workbuddy-auto wrote %s", p)
		}
	}

	if _, err := installTarget("workbuddy-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(settings); strings.Contains(string(b), "hook-context") {
		t.Fatalf("after uninstall settings = %s", b)
	}
}

// An existing .mcp.json is the file the agent reads, so the entry goes there.
func TestInstallWorkBuddyKeepsTheFileTheAgentReads(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	wb := filepath.Join(home, ".workbuddy")
	if err := os.MkdirAll(wb, 0o755); err != nil {
		t.Fatal(err)
	}
	dot := filepath.Join(wb, ".mcp.json")
	if err := os.WriteFile(dot, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := installTarget("workbuddy", "/bin/deja", false)
	if err != nil || r.Path != dot {
		t.Fatalf("install = %q, %v; want %s", r.Path, err, dot)
	}
}

// --auto finds WorkBuddy by a session store of either edition.
func TestWorkBuddyDetectedByItsStore(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	has := func() bool {
		return slices.Contains(existingTargets(), "workbuddy")
	}
	if has() {
		t.Fatal("a machine without WorkBuddy counted as one")
	}
	if err := os.MkdirAll(filepath.Join(home, ".workbuddy-ai", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !has() || autoTargetFor("workbuddy") != "workbuddy-auto" {
		t.Fatal("~/.workbuddy-ai/projects did not count as a WorkBuddy machine")
	}
}

// Every target, installed into an empty home, must leave behind files their
// harness can actually read. Driving the real interfaces turned up the failure
// this guards: what deja writes is valid on its own terms and rejected where it
// lands. A format error is the cheapest half of that class and needs no harness
// to catch — an unparsable config is a harness that will not start.
func TestEveryTargetWritesReadableFiles(t *testing.T) {
	for _, target := range installTargetNames() {
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			// Windows resolvers read APPDATA rather than the home directory.
			// Left at the package-wide value, a target that writes there lands
			// outside the directory walked below and the check passes on a
			// target that wrote nothing here.
			t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
			t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			if _, err := installTarget(target, "/usr/local/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			if _, err := guidanceResult(target, false); err != nil {
				t.Fatalf("guidance: %v", err)
			}

			wrote := 0
			err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil //nolint:nilerr // unreadable entries are skipped
				}
				raw, rerr := os.ReadFile(path) //nolint:gosec // the walk is over the test's own temp home
				if rerr != nil {
					t.Errorf("%s: unreadable: %v", rel(home, path), rerr)
					return nil
				}
				wrote++
				if len(raw) == 0 {
					t.Errorf("%s: empty file", rel(home, path))
					return nil
				}
				switch {
				case strings.HasSuffix(path, ".json"):
					var v any
					if jerr := json.Unmarshal(raw, &v); jerr != nil {
						t.Errorf("%s: invalid JSON: %v", rel(home, path), jerr)
					}
				case strings.HasSuffix(path, "SKILL.md"):
					// The frontmatter is the whole contract: a skill without a
					// name and a description is a file no harness will load.
					if !strings.HasPrefix(string(raw), "---") {
						t.Errorf("%s: skill has no frontmatter", rel(home, path))
						break
					}
					head := strings.SplitN(string(raw), "---", 3)
					if len(head) < 3 {
						t.Errorf("%s: unterminated frontmatter", rel(home, path))
						break
					}
					for _, key := range []string{"name:", "description:"} {
						if !strings.Contains(head[1], key) {
							t.Errorf("%s: frontmatter has no %s", rel(home, path), key)
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if wrote == 0 && target != "statusline" {
				t.Errorf("%s wrote nothing at all", target)
			}
		})
	}
}

func rel(home, path string) string {
	if r, err := filepath.Rel(home, path); err == nil {
		return r
	}
	return path
}

// A config deja cannot parse is refused and left alone. A config it can parse
// whose MCP block is a list was rewritten instead, and the servers in it went
// out with the write (#2399).
func TestInstallRefusesABlockOfTheWrongShape(t *testing.T) {
	cases := []struct {
		name   string
		target string
		rel    string
		before string
		keep   []string
	}{
		{"claude, mcpServers as a list", "claude", ".claude.json",
			`{"theme":"dark","mcpServers":[{"name":"mine","command":"/usr/bin/mine"}]}`,
			[]string{"/usr/bin/mine", `"theme"`}},
		{"opencode, mcp as a list", "opencode", ".config/opencode/opencode.json",
			`{"model":"theirs/model","mcp":["mine"]}`,
			[]string{`"mine"`, "theirs/model"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := installTarget(tc.target, "/usr/local/bin/deja", false)
			if err == nil {
				t.Fatalf("install rewrote a block it did not understand")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the refusal does not name the file: %v", err)
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, k := range tc.keep {
				if !strings.Contains(string(body), k) {
					t.Errorf("%s went out with the write:\n%s", k, body)
				}
			}

			// On the way out there is nothing of deja's in a block it never
			// wrote, and refusing would leave the rest of the target wired.
			r, err := installTarget(tc.target, "/usr/local/bin/deja", true)
			if err != nil {
				t.Errorf("uninstall refused a block it had nothing to remove from: %v", err)
			} else if r.Action != "unchanged" {
				t.Errorf("uninstall = %s %s, want unchanged", r.Action, r.Path)
			}
		})
	}
}

// A YAML config whose whole document is `{}` got deja's block appended after
// it, which no parser reads: hermes fell back to its defaults and goose dropped
// the config. `mcp_servers: {}` got a second `mcp_servers:` key (#4555). Both
// are refused now, with the file left as it was.
func TestInstallRefusesYAMLItCouldOnlyBreak(t *testing.T) {
	for _, c := range []struct {
		target, seed string
		path         func() string
	}{
		{"hermes", "{}\n", func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }},
		{"hermes", "mcp_servers: {}\n", func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }},
		{"goose", "{}\n", func() string { return filepath.Join(gooseConfigDir(), "config.yaml") }},
		{"continue", "{}\n", continueConfigPath},
		{"aider", "{}\n", func() string { return filepath.Join(homeDir(), ".aider.conf.yml") }},
		{"deepseek", "{}\n", func() string { return filepath.Join(sources.DSHHome(), "cordis.patch.yml") }},
		// The same document opened on the marker's line.
		{"hermes", "--- {}\n", func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }},
		{"goose", "--- {}  # empty\n", func() string { return filepath.Join(gooseConfigDir(), "config.yaml") }},
	} {
		t.Run(c.target+" "+strings.TrimSpace(c.seed), func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance")
			if err == nil {
				t.Errorf("install did not refuse")
			}
			if got, _ := os.ReadFile(p); string(got) != c.seed {
				t.Errorf("config changed:\n%s", got)
			}
		})
	}
}

// The control: a block mapping takes deja's block.
func TestInstallAppendsToABlockYAMLDocument(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.HermesHome(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("model: hermes-3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "hermes", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); !strings.Contains(string(got), "mcp_servers:\n  deja:") {
		t.Errorf("deja's block missing:\n%s", got)
	}
}

// These exercise the fallback when a desktop version cannot be read (another
// platform, or a non-standard install): a config.json the runtime maintains
// carries a hooks block, while a first-launch migration source has none. Only
// the live file gets a second copy of the hooks. The 3.17+ version-driven path
// is covered in zcode_version_test.go.
func TestInstallZCodeHooksAlsoReachALiveConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	legacy := filepath.Join(dir, "config.json")

	// The reader's own hooks have to survive the extra write.
	live := `{
  "mcp": {"servers": {"docs": {"type": "stdio", "command": "docs-server"}}},
  "hooks": {
    "enabled": true,
    "events": {
      "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "/tmp/mine.sh", "timeout": 10}]}]
    }
  }
}
`
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}

	// setting.json is still written the way #4429 requires.
	if b, err := os.ReadFile(setting); err != nil {
		t.Fatalf("setting.json was not written: %v", err)
	} else if !strings.Contains(string(b), "hook-context") {
		t.Errorf("setting.json has no deja hook: %s", b)
	}

	b, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("config.json is gone: %v", err)
	}
	var cfg struct {
		MCP struct {
			Servers map[string]json.RawMessage `json:"servers"`
		} `json:"mcp"`
		Hooks struct {
			Enabled bool                       `json:"enabled"`
			Events  map[string]json.RawMessage `json:"events"`
		} `json:"hooks"`
	}
	if uerr := json.Unmarshal(b, &cfg); uerr != nil {
		t.Fatalf("config.json is not JSON after install: %v", uerr)
	}
	if !cfg.Hooks.Enabled {
		t.Errorf("hooks.enabled is off in config.json: %s", b)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		if !strings.Contains(string(cfg.Hooks.Events[event]), "--strict") {
			t.Errorf("config.json hooks.events.%s = %s, want deja's hook", event, cfg.Hooks.Events[event])
		}
	}
	if !strings.Contains(string(cfg.Hooks.Events["PreToolUse"]), "/tmp/mine.sh") {
		t.Errorf("the reader's own PreToolUse hook was lost: %s", cfg.Hooks.Events["PreToolUse"])
	}
	// The server must not be copied into config.json: the first-launch
	// migration would carry a removed one back (#4429).
	if _, ok := cfg.MCP.Servers["deja"]; ok {
		t.Errorf("config.json gained a deja server: %s", b)
	}
	if _, ok := cfg.MCP.Servers["docs"]; !ok {
		t.Errorf("config.json lost the reader's server: %s", b)
	}
}

// config.json with no hooks.events is the one-off migration source of a first
// launch. Writing hooks into it would leave a file the runtime reads once, and
// #4429's test that install leaves it byte for byte must keep holding.
func TestInstallZCodeHooksLeaveTheMigrationSourceAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"provider":{"glm":{"apiKey":"sk-test"}},"model":"glm-5","mcp":{"servers":{"docs":{"command":"docs-server"}}}}`
	if err := os.WriteFile(legacy, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(legacy); string(got) != old {
		t.Errorf("install changed the migration source:\n%s", got)
	}
}

// The extra copy is deja's too, so uninstall takes it back out and leaves the
// reader's own hooks and the file itself as they were.
func TestUninstallZCodeHooksClearsTheLiveConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"hooks":{"enabled":true,"events":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/tmp/mine.sh","timeout":10}]}]}}}`
	if err := os.WriteFile(legacy, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "deja hook-") {
		t.Errorf("uninstall left deja's hooks in config.json: %s", b)
	}
	if string(b) != live {
		t.Errorf("config.json did not come back as it was.\nbefore:\n%s\nafter:\n%s", live, b)
	}
}

// A runtime that already wrote setting.json reads that file, so there is no
// second copy to make.
func TestInstallZCodeHooksLeaveAnExistingSettingAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"hooks":{"enabled":true,"events":{"PreToolUse":[]}}}`
	if err := os.WriteFile(setting, []byte(`{"hooks":{"enabled":false,"events":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(legacy); string(got) != live {
		t.Errorf("config.json was changed even though setting.json exists:\n%s", got)
	}
}

func TestInstallZCodeAutoReachesLiveConfigAcrossReinstalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"hooks":{"enabled":true,"events":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/tmp/mine.sh","timeout":10}]}]}}}`
	if err := os.WriteFile(legacy, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeAuto("/opt/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "hook-context --strict") {
		t.Errorf("installZCodeAuto did not wire hooks into live config.json: %s", b)
	}
	if _, uerr := installZCodeAuto("/opt/bin/deja", true); uerr != nil {
		t.Fatal(uerr)
	}
	after, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != live {
		t.Errorf("config.json did not come back as it was after installZCodeAuto uninstall.\nbefore:\n%s\nafter:\n%s", live, after)
	}
}

// zcodeDefaultSetting is the setting.json the ZCode 3.14.4 runtime writes on
// its first launch (setting.example.json in zcode-app-cli 0.16.9): the server
// map under mcp.servers, and the hooks off, with every event listed under
// hooks.events.
const zcodeDefaultSetting = `{
  "modelStream": {
    "idleTimeoutMs": 60000
  },
  "permission": {
    "mode": "build",
    "allowedTools": [],
    "disallowedTools": [],
    "autoApproveHighRisk": false,
    "allowMediumRiskInAuto": false
  },
  "storage": {
    "dir": "~/.zcode",
    "sessionDbPath": "~/.zcode/cli/db/db.sqlite"
  },
  "network": {
    "timeout": 180000
  },
  "features": {
    "compact": true,
    "rewind": true,
    "subagent": true,
    "skill": true,
    "mcp": true
  },
  "subagents": {
    "autoBackgroundMs": 1000
  },
  "memory": {
    "autoConsolidate": true,
    "summaryMaxBytes": 8192
  },
  "mcp": {
    "servers": {}
  },
  "plugins": {
    "enabled": true,
    "dirs": [],
    "enabledPlugins": {},
    "options": {},
    "suppressedBuiltins": []
  },
  "skills": {
    "enabled": true,
    "includeInstructions": true,
    "metadataBudget": 20000,
    "roots": []
  },
  "skill": {},
  "command": {},
  "logging": {
    "level": "info",
    "format": "text"
  },
  "ui": {
    "theme": "auto",
    "tuiMode": "regular",
    "copyOnSelect": true,
    "notifications": {
      "method": "auto",
      "condition": "unfocused"
    }
  },
  "toolConcurrency": {
    "maxConcurrency": 10
  },
  "modelAnomalyGuard": {
    "repeatedToolCallWarningThreshold": 3,
    "maxBudgetWarningsPerTurn": 3
  },
  "hooks": {
    "enabled": false,
    "timeoutMs": 60000,
    "maxOutputBytes": 32768,
    "events": {
      "SessionStart": [],
      "UserPromptSubmit": [],
      "PreToolUse": [],
      "PermissionRequest": [],
      "PostToolUse": [],
      "PostToolUseFailure": [],
      "Stop": []
    }
  }
}
`

// The runtime reads its servers and hooks from ~/.zcode/cli/setting.json, the
// hooks under hooks.events. config.json is read once, as the source of a
// first-launch migration, so on a machine where ZCode had run deja's install
// reached nothing and doctor still said wired (#4429).
func TestInstallZCodeWritesTheRuntimeSettingFile(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setting, []byte(zcodeDefaultSetting), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "zcode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Errorf("install wrote %s, which the runtime does not load", legacy)
	}
	var cfg struct {
		MCP struct {
			Servers map[string]json.RawMessage `json:"servers"`
		} `json:"mcp"`
		Hooks struct {
			Enabled bool                       `json:"enabled"`
			Events  map[string]json.RawMessage `json:"events"`
			Rest    map[string]json.RawMessage `json:"-"`
		} `json:"hooks"`
	}
	b, err := os.ReadFile(setting)
	if err != nil {
		t.Fatal(err)
	}
	if uerr := json.Unmarshal(b, &cfg); uerr != nil {
		t.Fatalf("setting.json is not JSON after install: %v", uerr)
	}
	if _, ok := cfg.MCP.Servers["deja"]; !ok {
		t.Errorf("no deja server under mcp.servers in setting.json: %s", b)
	}
	if !cfg.Hooks.Enabled {
		t.Errorf("hooks.enabled is off, so the hooks never run: %s", b)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		if !strings.Contains(string(cfg.Hooks.Events[event]), "--strict") {
			t.Errorf("hooks.events.%s = %s, want deja's hook", event, cfg.Hooks.Events[event])
		}
	}
	var top map[string]map[string]json.RawMessage
	if uerr := json.Unmarshal(b, &top); uerr == nil {
		if _, ok := top["hooks"]["SessionStart"]; ok {
			t.Errorf("a hook directly under hooks, where the runtime does not look: %s", b)
		}
	}
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "setting.json") {
		t.Errorf("doctor does not name setting.json:\n%s", out)
	}
}

// An install from before this fix left deja's entries in config.json. The
// uninstall takes them out of there too, so a later first-launch migration
// cannot bring back a server deja was asked to remove.
func TestUninstallZCodeClearsTheOldConfigFile(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"hooks":{"SessionStart":[{"hooks":[{"command":"/home/u/.config/deja/bin/deja-hook hook-context --strict","timeout":30,"type":"command"}]}],"PostToolUse":[{"hooks":[{"command":"/tmp/notify.sh","timeout":5,"type":"command"}]}],"enabled":true},"mcp":{"servers":{"deja":{"args":["mcp"],"command":"/usr/local/bin/deja","type":"stdio"},"docs":{"command":"docs-server"}}}}`
	if err := os.WriteFile(legacy, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "zcode-auto"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("the reader's config.json is gone: %v", err)
	}
	if strings.Contains(string(b), "deja") {
		t.Errorf("deja's entries survived in config.json: %s", b)
	}
	if !strings.Contains(string(b), "notify.sh") || !strings.Contains(string(b), "docs-server") {
		t.Errorf("uninstall took the reader's own entries: %s", b)
	}
}

// ZCode runs config-file hooks only with hooks.enabled on, and its own
// setting.json starts with it off. Install turned it on and uninstall left it
// on, so hooks the reader had switched off ran after an install and an
// uninstall, and the re-marshalled file came back with its keys sorted (#4431).
func TestUninstallZCodePutsTheHooksSwitchBack(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := strings.Replace(zcodeDefaultSetting, `"PostToolUse": [],`,
		`"PostToolUse": [{"hooks": [{"type": "command", "command": "/tmp/notify.sh", "timeout": 5}]}],`, 1)
	if theirs == zcodeDefaultSetting {
		t.Fatal("the fixture has no PostToolUse list to put the reader's hook in")
	}
	if err := os.WriteFile(setting, []byte(theirs), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "zcode-auto", "--no-index")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(out, "hooks.enabled") {
		t.Errorf("install turned on hooks the reader had off and did not say so:\n%s", out)
	}
	if _, cerr := captureRun(t, "uninstall", "zcode-auto"); cerr != nil {
		t.Fatalf("uninstall: %v", cerr)
	}
	got, err := os.ReadFile(setting)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != theirs {
		t.Errorf("setting.json did not come back as it was.\nbefore:\n%s\nafter:\n%s", theirs, got)
	}

	// A switch that was already on stays on: something else may run on it.
	on := strings.Replace(theirs, `"enabled": false,
    "timeoutMs"`, `"enabled": true,
    "timeoutMs"`, 1)
	if on == theirs {
		t.Fatal("the fixture has no hooks.enabled to turn on")
	}
	if err := os.WriteFile(setting, []byte(on), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "zcode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "zcode-auto"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if got, _ := os.ReadFile(setting); string(got) != on {
		t.Errorf("uninstall changed a file whose switch was already on:\n%s", got)
	}
}

// Before ZCode's first launch, setting.json is not there and the runtime will
// build it from config.json, minus the provider fields, but only while
// setting.json is missing. An install that created setting.json first took
// that migration away: the reader's servers, permissions and hooks in
// config.json never reached the runtime. Install starts the file the way the
// runtime would, and only when the runtime has not already done so (#4429).
func TestInstallZCodeBeforeFirstLaunchKeepsTheMigration(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	legacy := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{
    "provider": {"glm": {"apiKey": "sk-test"}},
    "model": "glm/glm-5",
    "permission": {"mode": "plan"},
    "mcp": {"servers": {"docs": {"type": "stdio", "command": "docs-server"}}}
}
`
	if err := os.WriteFile(legacy, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "zcode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(setting)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"docs"`, `"plan"`, `"deja"`, "hook-context"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("setting.json lacks %s, which the first-launch migration would have carried:\n%s", want, b)
		}
	}
	for _, not := range []string{"sk-test", "glm-5"} {
		if strings.Contains(string(b), not) {
			t.Errorf("setting.json carries %s, a provider field the runtime leaves in config.json:\n%s", not, b)
		}
	}
	if got, _ := os.ReadFile(legacy); string(got) != old {
		t.Errorf("install changed config.json:\n%s", got)
	}

	// Once the runtime has migrated, its marker says so, and config.json is
	// never read again: a setting.json made then starts empty.
	if err := os.Remove(setting); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "migrations", "settings-v1.json"), []byte(`{"schemaVersion":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "zcode-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(setting); strings.Contains(string(b), "docs-server") {
		t.Errorf("setting.json took config.json after the runtime's own migration had run:\n%s", b)
	}
}

// ZCode's first-launch migration copies config.json across as it is, so a
// machine wired by an older deja has its hooks directly under hooks, where the
// runtime does not look, and a reader can switch hooks.enabled off. Either way
// no hook runs, and the row said wired because the file names hook-context
// (#4429).
func TestDoctorZCodeHooksThatDoNotRunAreStale(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	setting := filepath.Join(dir, "setting.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := `[{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-context --strict","timeout":30}]}]`
	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "zcode" {
			row = a
		}
	}
	for name, body := range map[string]string{
		"flat":     `{"hooks":{"enabled":true,"SessionStart":` + hook + `}}`,
		"disabled": `{"hooks":{"enabled":false,"events":{"SessionStart":` + hook + `}}}`,
	} {
		if err := os.WriteFile(setting, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if state, _ := autoWiringState(row); state != "stale" {
			t.Errorf("%s: state = %q, want stale", name, state)
		}
	}
	// The control: the shape the runtime runs.
	if err := os.WriteFile(setting, []byte(`{"hooks":{"enabled":true,"events":{"SessionStart":`+hook+`}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if state, _ := autoWiringState(row); state != "wired" {
		t.Errorf("runtime shape: state = %q, want wired", state)
	}
}

// ZCode's writers created the containers they needed and never recorded them,
// so an uninstall left `{"hooks":{"enabled":true},"mcp":{"servers":{}}}` in a
// file deja had created itself — the shape #2604 exists to prevent (#3690).
func TestUninstallZCodeLeavesNoContainerItAdded(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".zcode", "cli", "setting.json")
	for _, target := range []string{"zcode", "zcode-auto"} {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	for _, target := range []string{"zcode", "zcode-auto"} {
		if _, err := captureRun(t, "uninstall", target); err != nil {
			t.Fatalf("uninstall %s: %v", target, err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		// Removed outright is the better answer and the one the config
		// writers give for a file that was entirely deja's.
		return
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, key := range []string{"mcp", "hooks"} {
		if _, ok := root[key]; ok {
			t.Errorf("uninstall left the %q block deja added: %s", key, b)
		}
	}
}

// A container the reader already had is theirs, empty or not — including the
// `enabled` switch, which is gemini's rule: something else may be running on it.
func TestUninstallZCodeKeepsWhatTheReaderHad(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".zcode", "cli", "setting.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := map[string]any{
		"hooks": map[string]any{"enabled": true},
		"mcp":   map[string]any{"servers": map[string]any{"theirs": map[string]any{"command": "their-server"}}},
	}
	b, err := json.Marshal(theirs)
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(path, b, 0o644); werr != nil {
		t.Fatal(werr)
	}
	for _, target := range []string{"zcode", "zcode-auto"} {
		if _, cerr := captureRun(t, "install", target, "--no-index"); cerr != nil {
			t.Fatalf("install %s: %v", target, cerr)
		}
		if _, cerr := captureRun(t, "uninstall", target); cerr != nil {
			t.Fatalf("uninstall %s: %v", target, cerr)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("uninstall removed a config the reader had: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(got, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	if on, _ := hooks["enabled"].(bool); !on {
		t.Errorf("uninstall turned off a switch that was not deja's: %s", got)
	}
	mcp, _ := root["mcp"].(map[string]any)
	servers, _ := mcp["servers"].(map[string]any)
	if _, ok := servers["theirs"]; !ok {
		t.Errorf("uninstall took someone else's server with it: %s", got)
	}
}

// ZCode keeps the server and the hooks in one file, and the three things that
// make the difference between working and silently doing nothing are: the
// server sits one level deeper than everywhere else (`mcp.servers`), the hooks
// do not run at all without `hooks.enabled`, and the output has to carry no
// key ZCode does not know (#3651).
func TestInstallZCodeWritesTheServerAndTheHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".zcode", "cli", "setting.json")

	// Something of the user's is already in the file: it has to survive both
	// halves of the install.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"glm-5.2","mcp":{"servers":{"theirs":{"command":"x"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installZCode("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}

	cfg := readZCodeConfig(t, path)
	if cfg.Model != "glm-5.2" {
		t.Errorf("the install rewrote the user's own settings: model = %q", cfg.Model)
	}
	if _, ok := cfg.MCP.Servers["theirs"]; !ok {
		t.Error("another server was dropped from the map")
	}
	srv, ok := cfg.MCP.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server under mcp.servers: %v", cfg.MCP.Servers)
	}
	if len(srv.Args) == 0 || srv.Args[len(srv.Args)-1] != "mcp" {
		t.Errorf("server entry = %q %v, want it to end in `mcp`", srv.Command, srv.Args)
	}
	if !cfg.Hooks.Enabled {
		t.Error("hooks.enabled is false, so the hooks that were just written never fire")
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		entries := cfg.Hooks.Events[event]
		if len(entries) == 0 {
			t.Fatalf("%s has no entry", event)
		}
		cmd := entries[0].Hooks[0].Command
		if !strings.Contains(cmd, "deja") {
			t.Errorf("%s runs %q", event, cmd)
		}
		// Without --strict the receipt line rides along, ZCode rejects the
		// whole response over it, and the context never arrives.
		if !strings.Contains(cmd, "--strict") {
			t.Errorf("%s = %q, want --strict so the output passes ZCode's schema", event, cmd)
		}
		if entries[0].Hooks[0].Type != "command" {
			t.Errorf("%s type = %q, want command", event, entries[0].Hooks[0].Type)
		}
	}

	// Twice is once: a second install must not stack a second copy.
	if _, err := installZCodeHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	again := readZCodeConfig(t, path)
	if n := len(again.Hooks.Events["SessionStart"]); n != 1 {
		t.Errorf("SessionStart has %d entries after two installs, want 1", n)
	}

	// And out again, leaving what was not ours.
	if _, err := installZCodeHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := installZCode("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	out := readZCodeConfig(t, path)
	if len(out.Hooks.Events["SessionStart"]) != 0 {
		t.Errorf("a hook of ours survived uninstall: %+v", out.Hooks.Events["SessionStart"])
	}
	if _, ok := out.MCP.Servers["deja"]; ok {
		t.Error("the server survived uninstall")
	}
	if _, ok := out.MCP.Servers["theirs"]; !ok {
		t.Error("uninstall took somebody else's server with it")
	}
}

// The strict shape is the point of the flag: one unknown key and ZCode discards
// the whole response, so the receipt has to go and the context has to stay.
func TestStrictHookOutputDropsTheReceiptOnly(t *testing.T) {
	var resp sessionStartHookResponse
	resp.SystemMessage = "deja: 12 sessions indexed"
	resp.HookSpecificOutput.HookEventName = "SessionStart"
	resp.HookSpecificOutput.AdditionalContext = "what you fixed last week"

	loose := marshalHookResponse(t, resp, false)
	if _, ok := loose["systemMessage"]; !ok {
		t.Error("the receipt is gone where the host can take it")
	}
	strict := marshalHookResponse(t, resp, true)
	if _, ok := strict["systemMessage"]; ok {
		t.Error("the receipt survived the strict shape, so ZCode would discard the context with it")
	}
	out, _ := strict["hookSpecificOutput"].(map[string]any)
	if out["additionalContext"] != "what you fixed last week" {
		t.Errorf("the context did not survive: %v", strict)
	}
	if len(strict) != 1 {
		t.Errorf("the strict shape carries %d keys, want only hookSpecificOutput: %v", len(strict), strict)
	}
}

func marshalHookResponse(t *testing.T, resp sessionStartHookResponse, strict bool) map[string]any {
	t.Helper()
	was := strictHookOutput
	strictHookOutput = strict
	defer func() { strictHookOutput = was }()
	if strict {
		resp.SystemMessage = ""
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

type zcodeConfig struct {
	Model string `json:"model"`
	MCP   struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"servers"`
	} `json:"mcp"`
	Hooks struct {
		Enabled bool `json:"enabled"`
		Events  map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"-"`
		Raw map[string]json.RawMessage `json:"-"`
	} `json:"hooks"`
}

// readZCodeConfig reads the file back with the events wherever they are. The
// runtime reads `hooks.events` only, and deja writes there (#4429); an install
// from before that put them at the top of `hooks`.
func readZCodeConfig(t *testing.T, path string) zcodeConfig {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg zcodeConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("the config is not readable JSON: %v", err)
	}
	var whole struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(b, &whole); err != nil {
		t.Fatal(err)
	}
	cfg.Hooks.Events = map[string][]struct {
		Hooks []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	}{}
	source := whole.Hooks
	if nested, ok := whole.Hooks["events"]; ok {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(nested, &inner); err == nil {
			source = inner
		}
	}
	for key, raw := range source {
		if key == "enabled" || key == "events" {
			continue
		}
		var entries []struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			continue
		}
		cfg.Hooks.Events[key] = entries
	}
	return cfg
}

// Zed on Windows keeps settings.json with CRLF endings; the insert wrote LF
// lines into it and the uninstall left a stray CR behind (#3231).
func TestInstallZedKeepsTheFilesLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	seed := "// hdr\r\n{\r\n  \"theme\": \"x\",\r\n}\r\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for i, line := range strings.SplitAfter(string(b), "\n") {
		if line != "" && strings.HasSuffix(line, "\n") && !strings.HasSuffix(line, "\r\n") {
			t.Fatalf("line %d ends with a bare LF in a CRLF file:\n%q", i+1, b)
		}
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != seed {
		t.Fatalf("uninstall did not give the file back:\n%q\nwant\n%q", got, seed)
	}
}

// Zed ships settings.json with a comment header and tolerates trailing commas,
// and it is the file a person edits by hand. Decoding it and writing the result
// back — what the other JSON installers do to a config file of their own —
// fails on the first `//` and would delete every comment if it did not.
const zedRealSettings = `// Zed settings
//
// For information on how to configure Zed, see the Zed
// documentation: https://zed.dev/docs/configuring-zed
{
  "git_panel": {
    "tree_view": true
  },
  "ui_font_size": 16,
  "theme": {
    "mode": "system",
    "light": "One Light",
    "dark": "One Dark",
  },
}
`

func zedSettingsFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// zedDejaEntry decodes the installed entry after stripping the comments Zed
// allows, so the test reads the file the way Zed does.
func zedDejaEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(zedStripForTest(b), &root); err != nil {
		t.Fatalf("the file Zed would read does not parse: %v\n%s", err, b)
	}
	// No block at all is a legitimate answer: uninstalling the only server
	// takes the block with it.
	servers, ok := root["context_servers"].(map[string]any)
	if !ok {
		return nil
	}
	entry, ok := servers[zedServerID].(map[string]any)
	if !ok {
		return nil
	}
	return entry
}

// zedStripForTest is the comment and trailing-comma tolerance Zed has, so the
// assertions above can use encoding/json.
func zedStripForTest(b []byte) []byte {
	var out []byte
	inString, escaped := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && (b[i] != '*' || b[i+1] != '/') {
				i++
			}
			i++
			out = append(out, ' ')
		default:
			out = append(out, c)
		}
	}
	// Trailing commas.
	var clean []byte
	for i := range out {
		if out[i] == ',' {
			j := i + 1
			for j < len(out) && (out[j] == ' ' || out[j] == '\n' || out[j] == '\t' || out[j] == '\r') {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				continue
			}
		}
		clean = append(clean, out[i])
	}
	return clean
}

func TestInstallZedKeepsTheCommentsSomeoneWrote(t *testing.T) {
	path := zedSettingsFile(t, zedRealSettings)

	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"// Zed settings",
		"// documentation: https://zed.dev/docs/configuring-zed",
		`"dark": "One Dark",`,
	} {
		if !strings.Contains(string(got), line) {
			t.Errorf("the file lost %q:\n%s", line, got)
		}
	}
	// And the settings themselves are still there, unreordered.
	if !strings.Contains(string(got), `"ui_font_size": 16`) {
		t.Errorf("a setting was dropped:\n%s", got)
	}
	entry := zedDejaEntry(t, path)
	if entry == nil {
		t.Fatalf("deja was not installed:\n%s", got)
	}
	if entry["command"] == "" {
		t.Errorf("the entry names no command: %v", entry)
	}
}

func TestInstallZedIsIdempotentAndReversible(t *testing.T) {
	path := zedSettingsFile(t, zedRealSettings)

	first, err := installZedMCP(path, "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	afterFirst, _ := os.ReadFile(path)
	second, err := installZedMCP(path, "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	afterSecond, _ := os.ReadFile(path)

	if first.Action == "unchanged" {
		t.Errorf("the first install changed nothing")
	}
	if second.Action != "unchanged" {
		t.Errorf("a second install rewrote the file: %s", second.Action)
	}
	if !bytes.Equal(afterFirst, afterSecond) {
		t.Errorf("installing twice is not the same as once:\n%s\n---\n%s", afterFirst, afterSecond)
	}

	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	back, _ := os.ReadFile(path)
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived the uninstall:\n%s", back)
	}
	for _, line := range []string{"// Zed settings", `"ui_font_size": 16`} {
		if !strings.Contains(string(back), line) {
			t.Errorf("uninstall took %q with it:\n%s", line, back)
		}
	}
	if string(back) != zedRealSettings {
		t.Errorf("install then uninstall did not put the file back:\n--- want ---\n%s--- got ---\n%s", zedRealSettings, back)
	}
}

// A settings file that already has other servers keeps them, and deja joins
// them rather than replacing the block.
func TestInstallZedJoinsExistingServers(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    "some-other": {
      "command": "other",
      "args": ["serve"]
    }
  },
  "ui_font_size": 16
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"some-other"`) {
		t.Errorf("another server was dropped:\n%s", got)
	}
	if zedDejaEntry(t, path) == nil {
		t.Errorf("deja was not added beside it:\n%s", got)
	}
	// Uninstalling takes ours and leaves theirs.
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if !strings.Contains(string(back), `"some-other"`) {
		t.Errorf("uninstall took another server with it:\n%s", back)
	}
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived:\n%s", back)
	}
}

// No settings file at all is the first-run case: write one Zed can read.
func TestInstallZedWritesAFileWhenThereIsNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")

	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if zedDejaEntry(t, path) == nil {
		b, _ := os.ReadFile(path)
		t.Fatalf("nothing installed:\n%s", b)
	}
}

// Uninstalling from a machine that never had deja must not create a file, nor
// add an empty block to one (#676).
func TestUninstallZedLeavesAnUntouchedMachineAlone(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "settings.json")
	if _, err := installZedMCP(missing, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("uninstall created a settings file: %v", err)
	}

	path := zedSettingsFile(t, zedRealSettings)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != zedRealSettings {
		t.Errorf("uninstall rewrote a file that never mentioned deja:\n%s", got)
	}
}

// A "deja" key belonging to some other setting is not ours to touch.
func TestInstallZedDoesNotMistakeANestedKeyForOurs(t *testing.T) {
	path := zedSettingsFile(t, `{
  "lsp": {
    "deja": {
      "binary": {
        "path": "/somewhere/else"
      }
    }
  }
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "/somewhere/else") {
		t.Errorf("someone else's deja settings were overwritten:\n%s", got)
	}
	entry := zedDejaEntry(t, path)
	if entry == nil {
		t.Fatalf("the server was not installed:\n%s", got)
	}
	if entry["command"] == "/somewhere/else" {
		t.Errorf("the wrong block was edited: %v", entry)
	}
}

// And a "deja" key inside another server's own configuration is not ours
// either: the search has to stay at the depth it is looking in.
func TestInstallZedDoesNotMistakeAKeyInsideAnotherServer(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    "some-other": {
      "command": "other",
      "env": {
        "deja": {
          "path": "/nope"
        }
      }
    }
  }
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "/nope") {
		t.Errorf("another server's own config was overwritten:\n%s", got)
	}
	entry := zedDejaEntry(t, path)
	if entry == nil {
		t.Fatalf("the server was not installed beside it:\n%s", got)
	}
	if entry["path"] != nil {
		t.Errorf("the nested block was edited instead of ours: %v", entry)
	}
}

// JSONC has two comment shapes and a settings file may use either anywhere.
// A block comment sitting between a key and its value, or holding a brace,
// used to be read as content: the object end came out wrong and the edit
// landed in the middle of someone's settings.
func TestInstallZedSurvivesBlockComments(t *testing.T) {
	path := zedSettingsFile(t, `/* Zed settings
   { not an object } */
{
  "context_servers": /* which servers the agent may call */ {
    "some-other": {
      /* the wrapper opens a brace here { and never closes it */
      "command": "other"
    }
  },
  "ui_font_size": 16
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, _ := os.ReadFile(path)
	for _, keep := range []string{
		"/* Zed settings",
		"/* which servers the agent may call */",
		"/* the wrapper opens a brace here { and never closes it */",
		`"ui_font_size": 16`,
		`"some-other"`,
	} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("the file lost %q:\n%s", keep, got)
		}
	}
	if zedDejaEntry(t, path) == nil {
		t.Fatalf("deja was not installed:\n%s", got)
	}
	// One block, not two. A scanner that loses its place inside a comment
	// reports no block at all and inserts a second one; the entry is then
	// readable and the settings are still wrong.
	if n := strings.Count(string(got), `"context_servers"`); n != 1 {
		t.Errorf("the file has %d context_servers blocks:\n%s", n, got)
	}

	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived the uninstall:\n%s", back)
	}
	if !strings.Contains(string(back), `"some-other"`) {
		t.Errorf("uninstall took the other server with it:\n%s", back)
	}
}

// Someone annotates the entry deja installed, and the note holds a brace that
// never closes. Replacing or removing that entry needs its end, and a scanner
// that counts braces inside comments finds the wrong one — which is how an
// uninstall takes the settings after it along too.
func TestInstallZedFindsTheEntryEndPastAComment(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    "deja": {
      /* installed by deja — the wrapper opens a brace { on purpose */
      "command": "/old/path/deja",
      "args": ["mcp"]
    },
    "some-other": {
      "command": "other"
    }
  },
  "ui_font_size": 16
}
`)
	// Replacing: the old path goes, everything after the entry stays.
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, _ := os.ReadFile(path)
	entry := zedDejaEntry(t, path)
	if entry == nil {
		t.Fatalf("the entry was lost:\n%s", got)
	}
	// Windows wraps the executable in cmd /c, so what the entry holds is
	// whatever mcpCommandArgs makes of it, not the path as written.
	wantCommand, _ := mcpCommandArgs("/usr/local/bin/deja")
	if entry["command"] != wantCommand {
		t.Errorf("the entry was not updated: %v", entry)
	}
	if strings.Contains(string(got), "/old/path/deja") {
		t.Errorf("the old command survived beside the new one:\n%s", got)
	}
	for _, keep := range []string{`"some-other"`, `"ui_font_size": 16`} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("replacing the entry took %q with it:\n%s", keep, got)
		}
	}

	// Removing: the same end, and the same everything-after.
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived:\n%s", back)
	}
	for _, keep := range []string{`"some-other"`, `"command": "other"`, `"ui_font_size": 16`} {
		if !strings.Contains(string(back), keep) {
			t.Errorf("uninstall took %q with it:\n%s", keep, back)
		}
	}
}

// install then uninstall has to leave the file it was handed, byte for byte.
// Leaving an empty "context_servers": {} behind is a change the reader did not
// ask for and did not make.
func TestInstallZedLeavesNoEmptyBlockBehind(t *testing.T) {
	before := `// Zed settings
{
  "theme": "One Dark",
  "buffer_font_size": 15
}
`
	path := zedSettingsFile(t, before)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if zedDejaEntry(t, path) == nil {
		t.Fatal("deja was not installed")
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if string(back) != before {
		t.Errorf("uninstall did not put the file back:\n--- want ---\n%s--- got ---\n%s", before, back)
	}
}

// But a block that still holds something the reader put there stays, even when
// deja was the only server in it.
func TestInstallZedKeepsABlockThatStillHoldsSomething(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    // the agent talks to these
    "deja": {
      "command": "/old/deja",
      "args": ["mcp"]
    }
  },
  "theme": "One Dark"
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	for _, keep := range []string{`"context_servers"`, "// the agent talks to these", `"theme": "One Dark"`} {
		if !strings.Contains(string(back), keep) {
			t.Errorf("uninstall took %q with it:\n%s", keep, back)
		}
	}
	if zedDejaEntry(t, path) != nil {
		t.Errorf("deja survived:\n%s", back)
	}
}

// Wiring deja into Zed and then asking doctor whether it is wired have to give
// the same answer. Doctor's generic probe falls back to looking for "deja"
// anywhere in a file it cannot parse, and a settings file is full of comments,
// so Zed gets the installer's own scanner.
func TestDoctorReportsZedWiring(t *testing.T) {
	path := zedSettingsFile(t, `// Zed settings
{
  // deja is not installed here — this line only says the word
  "context_servers": {
    "some-other": {
      "command": "other"
    }
  },
  "ui_font_size": 16
}
`)
	if doctorZedWired(path) {
		t.Error("doctor calls Zed wired with no deja entry")
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if !doctorZedWired(path) {
		b, _ := os.ReadFile(path)
		t.Errorf("doctor does not see the entry install just wrote:\n%s", b)
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if doctorZedWired(path) {
		b, _ := os.ReadFile(path)
		t.Errorf("doctor still calls it wired after uninstall:\n%s", b)
	}
	if doctorZedWired(filepath.Join(t.TempDir(), "nothing.json")) {
		t.Error("doctor calls a machine with no settings file wired")
	}
}

// The zed line has to be in doctor's list at all: an install target whose
// result doctor never reports is wiring nobody can check.
func TestDoctorListsZed(t *testing.T) {
	var found bool
	for _, c := range doctorMCPConfigs() {
		if c.name == "zed" {
			found = true
		}
	}
	if !found {
		t.Error("doctor lists no zed row, so `deja install zed` cannot be verified")
	}
}

// The extension's own entry is the user's, not ours: `deja install` writes
// under the same id, finds that entry and leaves it exactly as it is rather
// than turning it into a command of ours.
//
// While the extension is there. An entry that names no command and defers to
// an extension which is not installed is an enabled server with nothing behind
// it, and leaving that alone made `deja install zed` — the remedy doctor names
// — report "unchanged" and fix nothing (#3660).
func TestZedInstallLeavesTheExtensionEntryAlone(t *testing.T) {
	body := `{
  "context_servers": {
    "deja-context-server": {
      "enabled": true,
      "settings": {}
    }
  }
}
`
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, dir := range zedExtensionDirs() {
		if err := os.MkdirAll(filepath.Join(dir, zedServerID), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	path := zedSettingsFile(t, body)
	if _, err := installZedMCP(path, "deja", false); err != nil {
		t.Fatal(err)
	}
	if got := zedRead(t, path); got != body {
		t.Fatalf("settings were rewritten while the extension is installed:\n%s", got)
	}

	// Now the extension goes, which is the state found on a real machine: a
	// symlink into a scratch directory that no longer existed.
	for _, dir := range zedExtensionDirs() {
		if err := os.RemoveAll(filepath.Join(dir, zedServerID)); err != nil {
			t.Fatal(err)
		}
	}
	orphan := zedSettingsFile(t, body)
	if _, err := installZedMCP(orphan, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	entry := zedDejaEntry(t, orphan)
	if entry["command"] == nil {
		t.Errorf("the orphaned entry was left with nothing to run: %v", entry)
	}
}

// The server id deja writes and the one the extension registers have to be the
// same string — that is the whole reason the two cannot become two servers.
// They live in different files, edited months apart. The extension's own id is
// a different thing: Zed's registry wants an MCP extension named *-mcp-server.
func TestZedServerIDMatchesTheExtensionManifest(t *testing.T) {
	manifest := string(repoFile(t, "extensions/zed/extension.toml"))
	if want := `id = "deja-mcp-server"`; !strings.Contains(manifest, want) {
		t.Fatalf("extension.toml does not declare %s:\n%s", want, manifest)
	}
	if !strings.Contains(manifest, "[context_servers."+zedServerID+"]") {
		t.Fatalf("extension.toml does not register the context server under %q:\n%s", zedServerID, manifest)
	}
}

// The id deja wrote before this is renamed by the next install, so a machine
// that already had both stops running two servers without being told.
func TestZedInstallRenamesTheLegacyEntry(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    "deja": {"command": "/old/deja", "args": ["mcp"]},
    "other": {"command": "other"}
  }
}
`)
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	after := zedRead(t, path)
	if strings.Contains(after, `"deja":`) {
		t.Fatalf("the old id survived:\n%s", after)
	}
	if strings.Count(after, `"deja-context-server"`) != 1 || !strings.Contains(after, "/usr/local/bin/deja") {
		t.Fatalf("want exactly one current entry naming this binary:\n%s", after)
	}
	if !strings.Contains(after, `"other"`) {
		t.Fatalf("another server was lost:\n%s", after)
	}
}

// Uninstall takes both ids with it, including the one from before the rename.
func TestZedUninstallRemovesBothIDs(t *testing.T) {
	path := zedSettingsFile(t, `{
  "context_servers": {
    "deja": {"command": "/old/deja", "args": ["mcp"]},
    "deja-context-server": {"command": "/usr/local/bin/deja", "args": ["mcp"]}
  },
  "ui_font_size": 16
}
`)
	if _, err := installZedMCP(path, "deja", true); err != nil {
		t.Fatal(err)
	}
	after := zedRead(t, path)
	if strings.Contains(after, "deja") {
		t.Fatalf("an entry survived uninstall:\n%s", after)
	}
	if !strings.Contains(after, "ui_font_size") {
		t.Fatalf("the rest of the settings did not survive:\n%s", after)
	}
}

// An extension entry is not deja's to delete: uninstalling the CLI wiring on a
// machine where the extension provides the server has to leave it running.
func TestZedUninstallKeepsTheExtensionEntry(t *testing.T) {
	body := `{
  "context_servers": {
    "deja-context-server": {"enabled": true, "settings": {}}
  }
}
`
	path := zedSettingsFile(t, body)
	if _, err := installZedMCP(path, "deja", true); err != nil {
		t.Fatal(err)
	}
	if got := zedRead(t, path); got != body {
		t.Fatalf("the extension entry was touched:\n%s", got)
	}
}

func zedRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// The Agent Plugins manifest schema is closed: an unknown top-level field is a
// schema violation, and the name has its own grammar. Both are the kind of thing
// a directory rejects on submission rather than a reviewer catching by eye, so
// they are pinned here.
//
// https://open-plugins.com/plugin-authors/manifest
func TestAgentPluginManifestIsPortable(t *testing.T) {
	for _, path := range []string{"plugin.json", "claude-plugin/plugin.json"} {
		t.Run(path, func(t *testing.T) { checkAgentPluginManifest(t, path) })
	}
}

// The Claude Code plugin carries its manifest twice: .claude-plugin/plugin.json
// for Claude Code and plugin.json at the plugin root for directories that read
// the Agent Plugins layout (awesome-copilot's intake rejected the plugin for
// its absence, and then for the version being behind the tag). One version.
func TestClaudePluginManifestsAgree(t *testing.T) {
	var a, b map[string]any
	if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(repoFile(t, "claude-plugin/plugin.json"), &b); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "version", "description", "license"} {
		if a[k] != b[k] {
			t.Errorf("%s: .claude-plugin has %v, plugin.json has %v", k, a[k], b[k])
		}
	}
	for _, m := range []map[string]any{a, b} {
		kws, _ := m["keywords"].([]any)
		for _, kw := range kws {
			if s, _ := kw.(string); !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(s) {
				t.Errorf("keyword %q: directories accept lowercase letters, digits and hyphens only", s)
			}
		}
	}
}

func checkAgentPluginManifest(t *testing.T, path string) {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(repoFile(t, path), &manifest); err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	allowed := map[string]bool{
		"$schema": true, "name": true, "version": true, "description": true,
		"author": true, "homepage": true, "repository": true, "license": true,
		"keywords": true, "extensions": true,
	}
	for key := range manifest {
		if !allowed[key] {
			t.Errorf("%q is not a portable top-level field; client-specific data belongs under extensions", key)
		}
	}
	for _, required := range []string{"$schema", "name"} {
		if manifest[required] == nil {
			t.Errorf("%q is required", required)
		}
	}

	// 1–64 characters, lowercase ASCII letters, digits, hyphens and periods,
	// starting and ending alphanumeric, with no doubled separator.
	name, _ := manifest["name"].(string)
	ok := regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,62}[a-z0-9])?$`).MatchString(name)
	if !ok || regexp.MustCompile(`--|\.\.`).MatchString(name) {
		t.Errorf("plugin name %q does not satisfy the Agent Plugins grammar", name)
	}
}

// Skills are discovered at a fixed depth: an immediate child of skills/ whose
// SKILL.md is a regular file. A skill nested one level deeper is not found, and
// nothing else in this repository would notice.
func TestAgentPluginSkillsAreDiscoverable(t *testing.T) {
	root := filepath.Join("..", "..")
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatalf("skills/: %v", err)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(root, "skills", e.Name(), "SKILL.md"))
		if err != nil || !info.Mode().IsRegular() {
			t.Errorf("skills/%s has no SKILL.md at the top level, so no client will discover it", e.Name())
			continue
		}
		found++
	}
	if found == 0 {
		t.Fatal("no skill is discoverable, which makes the plugin manifest a promise of nothing")
	}
}

// mcp.json is a closed document too: only $schema and mcpServers, and a stdio
// command is one executable token rather than a shell line.
func TestAgentPluginMCPDocument(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(repoFile(t, "mcp.json"), &doc); err != nil {
		t.Fatalf("mcp.json: %v", err)
	}
	for key := range doc {
		if key != "$schema" && key != "mcpServers" {
			t.Errorf("%q is not allowed at the top level of mcp.json", key)
		}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	server, ok := servers["deja"].(map[string]any)
	if !ok {
		t.Fatal("mcp.json declares no deja server")
	}
	if server["type"] != "stdio" {
		t.Errorf("transport is %v, want stdio", server["type"])
	}
	cmd, _ := server["command"].(string)
	if cmd != "deja" {
		t.Errorf("command is %q; it has to be one executable token resolved on PATH, not a shell command", cmd)
	}
}

// The conventions block and the environment block are text an agent reads. Both
// cut long entries at a byte count, which lands mid-character on anything but
// ASCII — a decision recorded in Russian reached the model as invalid UTF-8
// (#1319).
func TestAgentFacingTextIsCutOnRuneBoundaries(t *testing.T) {
	long := map[string]string{
		"cyrillic": strings.Repeat("решение про очередь повторов ", 20),
		"cjk":      strings.Repeat("重试队列的决定 ", 40),
		"ascii":    strings.Repeat("the retry queue decision ", 20),
	}
	for name, text := range long {
		got := conventionLine(sources.PromotedNote{Text: text})
		if !utf8.ValidString(got) {
			t.Errorf("conventions line for %s is not valid UTF-8: %q", name, got)
		}
		if got == "" {
			t.Errorf("conventions line for %s came back empty", name)
		}
	}
}

// A note that fits keeps its whole text, and the decision is what the line
// carries — the title is the session's own opening line, which is the problem
// rather than what was decided about it (#2456).
func TestAShortConventionKeepsItsText(t *testing.T) {
	if got := conventionLine(sources.PromotedNote{Text: "queue retries with full jitter"}); got != "queue retries with full jitter" {
		t.Errorf("a short note was changed: %q", got)
	}
	if got := conventionLine(sources.PromotedNote{Title: "retry policy", Text: "queue retries with full jitter"}); got != "queue retries with full jitter" {
		t.Errorf("the decision should be the line, not the title: %q", got)
	}
	if got := conventionLine(sources.PromotedNote{Title: "retry policy"}); got != "retry policy" {
		t.Errorf("a note with only a title should still say it: %q", got)
	}
	// The first sentence, when there is one.
	if got := conventionLine(sources.PromotedNote{Text: "Retry three times. Then give up."}); got != "Retry three times." {
		t.Errorf("the first sentence was not taken: %q", got)
	}
}

// The mark left where text was cut has to be the character, not what a
// mis-decoded copy of it turns into: this constant held the Shift_JIS reading
// of "…" and the plan hook appended that to every truncated finding.
func TestTheTruncationMarkIsAnEllipsis(t *testing.T) {
	got := truncatePlanBytes(strings.Repeat("a", 100), 20)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncation mark is %q", got[len(got)-6:])
	}
	if strings.Contains(got, "窶") {
		t.Errorf("the mangled ellipsis is still there: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncated text is not valid UTF-8: %q", got)
	}
}

// The environment block names the walls this machine keeps hitting, and it cut
// each one at 96 bytes — mid-character on a wall recorded in Russian or
// Chinese, and the block goes straight into the model's context. Through the
// block itself, since that is where the cut lives.
func TestTheEnvironmentBlockCutsOnRuneBoundaries(t *testing.T) {
	tmp := hermeticEnv(t)
	// Three project directories: a wall of one repository is no longer a fact
	// about the machine.
	roots := make([]string, 0, environmentMinProjects)
	for i := range environmentMinProjects {
		root := filepath.Join(tmp, "claude", fmt.Sprintf("proj-w%d", i))
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, root)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	// English shape so it is recognised as a wall, Cyrillic tail so the 96-byte
	// cut lands inside a character. 105 bytes: past the 96-byte cut, inside the
	// 120-byte bound on what counts as a wall, and with the cut falling on an
	// odd offset into two-byte letters — at the obvious phrasing it fell on a
	// boundary and the test could not fail.
	wall := "ModuleNotFoundError: No module named очередьповторовпредпродакшенмодуля"
	for i := range index.FrictionMinSessions {
		sid := fmt.Sprintf("w%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/w","timestamp":"2026-07-2` +
			strconv.Itoa(i%10) + `T10:00:00Z","message":{"role":"user","content":"run the checks"}}` + "\n" +
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w/w","timestamp":"2026-07-2` +
			strconv.Itoa(i%10) + `T10:05:00Z","message":{"role":"user","content":[{"type":"tool_result",` +
			`"content":"` + wall + `"}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(roots[i%len(roots)], sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got := environmentBlock(dir, policy.ActivationAuto)
	if got == "" {
		t.Fatal("no environment block to judge")
	}
	if !utf8.ValidString(got) {
		t.Errorf("a wall in Russian was cut mid-character:\n%q", got)
	}
}

// The friction screen prints walls to a terminal, and cut each at 76 bytes.
func TestFrictionLinesCutOnRuneBoundaries(t *testing.T) {
	for _, pad := range []string{"", "a", "ab"} {
		got := trimFriction(pad + strings.Repeat("ошибка подключения ", 20))
		if !utf8.ValidString(got) {
			t.Errorf("pad %q: a wall printed as broken bytes: %q", pad, got)
		}
		if len(got) > 79 {
			t.Errorf("pad %q: the line ran past its bound at %d bytes", pad, len(got))
		}
	}
	short := "npm ERR! code ELIFECYCLE"
	if got := trimFriction(short); got != short {
		t.Errorf("a short wall was changed: %q", got)
	}
}

// A session with no user turn borrows the assistant's opening line (#692), and
// the listing printed it in the place of the reader's own question — an
// assertion nobody made reading like something they said (#1100).
func TestTheListingSaysWhenATitleIsTheAgentsOwnWords(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	asked := `{"type":"user","message":{"role":"user","content":"how do we rotate the vault keys"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"withq","cwd":"/proj"}` + "\n"
	said := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"the vault rotation now runs weekly"}]},"timestamp":"2026-08-01T10:00:00Z","sessionId":"noq","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(asked), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "b.jsonl"), []byte(said), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		switch {
		case strings.Contains(line, "noq"):
			if !strings.Contains(line, "agent: the vault rotation") {
				t.Errorf("the fallback title is not marked as the agent's: %q", line)
			}
		case strings.Contains(line, "withq"):
			if strings.Contains(line, "agent:") {
				t.Errorf("a real question was marked as the agent's: %q", line)
			}
		}
	}
}

// The agents page is where a reader learns what the MCP tool can do: it names
// each mode in a table and counts them in the sentence above it. Both are
// hand-written beside an enum that has changed twice — `recall_context` became
// `context`, and the six became one tool with a mode — so the page is checked
// against the enum rather than trusted.
func TestTheAgentsPageNamesEveryMCPMode(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "agents.html"))
	if err != nil {
		t.Fatal(err)
	}
	modes := mcpToolModes(t)
	if len(modes) == 0 {
		t.Fatal("the tool schema declares no modes, so this checks nothing")
	}
	for _, m := range modes {
		if !strings.Contains(string(page), "<code>"+m+"</code>") {
			t.Errorf("docs/guide/agents.html never names the %q mode", m)
		}
	}
	// And the count in the sentence above the table.
	said := regexp.MustCompile(`([a-z]+) capabilities behind one entry`).FindSubmatch(page)
	if said == nil {
		t.Fatal("the page no longer says how many capabilities there are")
	}
	spelled := map[string]int{"four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9}
	got, ok := spelled[string(said[1])]
	if !ok {
		got, _ = strconv.Atoi(string(said[1]))
	}
	if got != len(modes) {
		t.Errorf("the page says %q capabilities; the tool declares %d", said[1], len(modes))
	}
}

// mcpToolModes reads the enum out of the tool schema the server advertises, so
// a mode added there has to reach the page.
func mcpToolModes(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("mcp.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`"enum": \[\]string\{([^}]*)\}`).FindSubmatch(b)
	if m == nil {
		t.Fatal("the tool schema no longer declares a mode enum")
	}
	var out []string
	for part := range strings.SplitSeq(string(m[1]), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var s string
		if json.Unmarshal([]byte(part), &s) == nil && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// The agents page named the harnesses wired per prompt and before an action,
// and the lists went stale as targets gained hooks: five were wired before an
// action, the page said, while thirty-one were. It now counts them, and the
// counts are held to the registry the capability-drift test holds to the code.
func TestAgentsPageCountsTheWiredSurfaces(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			Surfaces map[string]struct {
				Status string `json:"status"`
			} `json:"surfaces"`
		} `json:"harnesses"`
	}
	if err = json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	count := func(surface, status string) int {
		n := 0
		for _, h := range reg.Harnesses {
			if h.Surfaces[surface].Status == status {
				n++
			}
		}
		return n
	}
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "agents.html"))
	if err != nil {
		t.Fatal(err)
	}
	claim := regexp.MustCompile(`in (\d+) of the (\d+) harnesses[^.;]*?in part in (\d+) more`)
	for _, c := range []struct{ item, surface string }{
		{"<b>On each prompt</b>", "prompt"},
		{"<b>Before an edit or a command</b>", "pre_tool"},
	} {
		at := bytes.Index(page, []byte(c.item))
		if at < 0 {
			t.Fatalf("agents.html lost the %q item", c.item)
		}
		li := string(page[at:])
		end := strings.Index(li, "</li>")
		if end < 0 {
			t.Fatalf("agents.html: the %q item never closes", c.item)
		}
		li = li[:end]
		m := claim.FindStringSubmatch(li)
		if m == nil {
			t.Fatalf("%s: no \"in N of the M harnesses … in part in K more\" sentence:\n%s", c.surface, li)
		}
		got := []string{m[1], m[2], m[3]}
		want := []string{strconv.Itoa(count(c.surface, "yes")), strconv.Itoa(len(reg.Harnesses)), strconv.Itoa(count(c.surface, "partial"))}
		if strings.Join(got, "/") != strings.Join(want, "/") {
			t.Errorf("%s: page says %s (wired/harnesses/partial), registry says %s", c.surface, strings.Join(got, "/"), strings.Join(want, "/"))
		}
	}
}

// Two aiders started together leave no header AiderSessionAt can call this
// run's: asker() caches only a non-empty answer, so once the header window
// (launch to launch+1m) has closed with nothing resolved, it still walks the
// whole history again on every later call instead of settling on the
// fallback sid (#5295).
//
// A wall-clock comparison stood here before and was flaky on a shared
// runner (a slow run's "first" call could cost as little as a fast one's
// "second"). This pins the same bug on behaviour instead: the history is
// rewritten after the window has closed to a shape a fresh walk would
// resolve, so a re-walk is caught by the id it would return, not by timing.
func TestAiderAskerStopsWalkingOnceTheWindowCloses(t *testing.T) {
	tmp := t.TempDir()
	history := filepath.Join(tmp, ".aider.chat.history.md")

	launch, err := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-01 09:00:00", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	before := "\n# aider chat started at 2026-09-01 09:00:00\n\n#### filler  \n\nok\n\n"
	// Two aiders started in the same launch second, both past offset: nothing
	// says which header is this run's.
	ambiguous := "\n# aider chat started at 2026-10-01 09:00:00\n\n#### one  \n\nok\n\n" +
		"\n# aider chat started at 2026-10-01 09:00:00\n\n#### two  \n\nok\n\n"
	if err = os.WriteFile(history, []byte(before+ambiguous), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := int64(len(before))

	l := &aiderLive{history: history, offset: offset, launch: launch, sid: "aider-test-fallback"}

	if first := l.asker(); first != l.sid {
		t.Fatalf("ambiguous launch: asker() returned %q, want the fallback %q", first, l.sid)
	}

	// The window is long closed, so the fallback is final. Rewrite the
	// history to one header in the same window: a walk that still runs now
	// would resolve to that session's id instead of keeping the fallback.
	resolved := "\n# aider chat started at 2026-10-01 09:00:00\n\n#### one  \n\nok\n\n"
	if err = os.WriteFile(history, []byte(before+resolved), 0o644); err != nil {
		t.Fatal(err)
	}
	if second := l.asker(); second != l.sid {
		t.Fatalf("second call returned %q, want the same fallback %q: asker() is still walking the whole history after the window closed", second, l.sid)
	}
}

// The aider `deja aider` started is a session in the index as soon as anything
// refreshes it, under the id the reader gives it. The live recall asked under
// an id of its own, so the session being written came back to itself as "this
// was asked here before", and the serving spent the project's cooldown the next
// aider needed it for. Measured on aider 0.86.2.
func TestALiveAiderIsNotRecalledToItself(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("DEJA_AIDER_ROOTS", "")

	history := filepath.Join(home, ".aider.chat.history.md")
	appendTo := func(s string) {
		t.Helper()
		f, err := os.OpenFile(history, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-72 * time.Hour).Format("2006-01-02 15:04:05")
	appendTo("\n# aider chat started at " + old + "\n\n" +
		"#### the gateway_timeout in the reconnect_loop drops heartbeats  \n\n" +
		"The reconnect_loop gives up after gateway_timeout; raise the deadline.\n\n")

	dir := filepath.Join(tmp, "index.db")
	l := newAiderLive(dir, home, history, "")
	// What aider writes on start, and the first message of this run.
	appendTo("\n# aider chat started at " + time.Now().Format("2006-01-02 15:04:05") + "\n\n" +
		"#### the osprey_worker retries seven times before parking_jobs  \n\n" +
		"Noted: osprey_worker retries seven times, then parks the job.\n\n")
	// Any other harness's hook, or a search, refreshes the index meanwhile.
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := sources.ParseAiderFile(history)
	if err != nil || len(ss) != 2 {
		t.Fatalf("history parsed to %d sessions (%v)", len(ss), err)
	}
	own := ss[1].ID

	appendTo("#### how many times does the osprey_worker retry before parking_jobs  \n\n")
	if got := l.body(); strings.Contains(got, "osprey_worker") || strings.Contains(got, shortID(own)) {
		t.Errorf("the session being written was recalled to itself (%s):\n%s", own, got)
	}

	// Control: the same reader, asked about the session before it, gets it.
	appendTo("#### the gateway_timeout in the reconnect_loop is back  \n\n")
	if got := l.body(); !strings.Contains(got, "gateway_timeout") {
		t.Errorf("an earlier session was not recalled:\n%s", got)
	}
}

// aider has no hook on a user message. The stand-in does what aider 0.86.2
// does on each message: append it to the chat history, then read every
// read-only file. The recall for that message has to be in what it reads, and
// the project's own read: list has to survive.
func TestDejaAiderRecallsForTheMessageBeingSent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in aider is a shell script, and there is no FIFO")
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(tmp, "proj", "beta")
	bin := filepath.Join(tmp, "bin")
	for _, d := range []string{home, filepath.Join(claude, "beta"), filepath.Join(claude, "alpha"), proj, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("AIDER_READ", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(claude, "beta", "hookfix.jsonl"), fmt.Sprintf(`{"type":"user","sessionId":"hookfix","timestamp":%q,`+
		`"message":{"role":"user","content":"gateway_timeout on the reconnect_loop keeps dropping heartbeats"}}`+"\n", at))
	write(filepath.Join(claude, "alpha", "f.jsonl"), fmt.Sprintf(`{"type":"user","sessionId":"filler","timestamp":%q,`+
		`"message":{"role":"user","content":"unrelated work about deployments and dashboards"}}`+"\n", at))
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := installAider("", false); err != nil {
		t.Fatal(err)
	}
	// The project's own list, which hid the home one.
	write(filepath.Join(proj, ".aider.conf.yml"), "read:\n  - CONVENTIONS.md\n")
	write(filepath.Join(proj, "CONVENTIONS.md"), "conventions\n")
	t.Chdir(proj)

	args, before, seen := filepath.Join(tmp, "args"), filepath.Join(tmp, "before"), filepath.Join(tmp, "seen")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + args + "'\n" +
		"for a; do last=$a; done\n" +
		"cat \"$last\" > '" + before + "'\n" +
		"printf '\\n#### seeing gateway_timeout in the reconnect_loop again  \\n' >> .aider.chat.history.md\n" +
		"cat \"$last\" > '" + seen + "'\n"
	write(filepath.Join(bin, "aider"), script)
	if err := os.Chmod(filepath.Join(bin, "aider"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := cmdAider(dir, []string{"--message", "hi"}, ""); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(args)
	if !strings.Contains(string(a), "--read\nCONVENTIONS.md\n--read\n") {
		t.Errorf("the project's read: list was not handed on:\n%s", a)
	}
	if strings.Contains(string(a), "aider-context.md") {
		t.Errorf("the shared context file is read beside the live one:\n%s", a)
	}
	if b, _ := os.ReadFile(before); strings.Contains(string(b), promptHookLead) {
		t.Fatalf("a recall before any message was sent:\n%s", b)
	}
	got, _ := os.ReadFile(seen)
	if !strings.Contains(string(got), promptHookLead) || !strings.Contains(string(got), "gateway_timeout") {
		t.Errorf("the message being sent got no recall:\n%s", got)
	}
}

// Aider reads this file in every session because the config points at it, and
// only `deja aider` ever rewrites it. Install runs before there is an index, so
// the placeholder is what a user who starts plain `aider` reads forever —
// driven through the real interface it said "No matching history yet.", which
// reads as deja having nothing rather than as nothing having refreshed it.
func TestAiderPlaceholderSaysHowToFillIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "no-history"))

	if err := refreshAiderContext(filepath.Join(home, "index.db")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(aiderContextPath())
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, "deja aider") {
		t.Fatalf("the placeholder does not say what would fill it:\n%s", body)
	}
	if strings.TrimSpace(body) == "" {
		t.Fatal("the file must exist and be non-empty; aider refuses to start without it")
	}
}

// aider's config points every launch at one context file. `deja aider` filled
// it with the digest of the directory it ran in and left it there, so plain
// aider in another project read that project's sessions under a lead calling
// them "this project's" — live, the model answered "we previously fixed
// retry.py" from it (#4328).
func TestDejaAiderLeavesNoDigestForPlainAider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in aider is a shell script")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := filepath.Join(home, "index")
	projA := filepath.Join(home, "proj-a")
	for _, d := range []string{dir, projA} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(projA)
	cwd, _ := os.Getwd()
	writeHookCache(dir, cwd, "  - Session: **proj-a** `aider-1`\n", 1, 10, nil, 0, []string{"aider-1"}, []string{"proj-a"})

	// The stand-in aider keeps a copy of what it was handed.
	bin := filepath.Join(home, "bin")
	seen := filepath.Join(home, "seen.md")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncp '" + aiderContextPath() + "' '" + seen + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "aider"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := cmdAider(dir, []string{"--exit"}, ""); err != nil {
		t.Fatal(err)
	}
	during, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(during), "aider-1") {
		t.Fatalf("the aider deja started was not handed the digest:\n%s", during)
	}
	// The lead names the directory the digest was built for, rather than
	// calling it whatever project the reader is in.
	if !strings.Contains(string(during), cwd) || strings.Contains(string(during), "this project's recent history") {
		t.Errorf("the lead does not name the project it was built for:\n%s", during)
	}
	after, err := os.ReadFile(aiderContextPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "aider-1") {
		t.Errorf("plain aider started next, anywhere, reads proj-a's sessions:\n%s", after)
	}
	if strings.TrimSpace(string(after)) == "" {
		t.Error("the file must stay non-empty; aider refuses to start without it")
	}
}

func aiderReadHome(t *testing.T) (home, proj string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AIDER_READ", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("AIDER_GIT", "")
	proj = filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "read:\n  - " + aiderContextPath() + "\n"
	if err := os.WriteFile(aiderConfPath(), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, proj
}

// A project .aider.conf.yml with its own read: list hid the home one, and with
// it deja's context file: aider takes one list, the first it finds (#4801).
func TestAiderReadArgsKeepsTheProjectList(t *testing.T) {
	_, proj := aiderReadHome(t)
	ctx := aiderContextPath()
	if got := aiderReadArgs(nil, proj, ctx); got != nil {
		t.Fatalf("the home list already reads the file, want no args, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(proj, ".aider.conf.yml"), []byte("model: x\nread: [CONVENTIONS.md, 'docs/a b.md']  # mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"--read", "CONVENTIONS.md", "--read", "docs/a b.md", "--read", ctx}
	if got := aiderReadArgs(nil, proj, ctx); !slices.Equal(got, want) {
		t.Errorf("project list: got %q, want %q", got, want)
	}
	// Block form, and a second key: PyYAML keeps the last.
	if err := os.WriteFile(filepath.Join(proj, ".aider.conf.yml"), []byte("read: old.md\nread:\n  # comment\n  - CONVENTIONS.md\n  - \"x.md\"\nmodel: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want = []string{"--read", "CONVENTIONS.md", "--read", "x.md", "--read", ctx}
	if got := aiderReadArgs(nil, proj, ctx); !slices.Equal(got, want) {
		t.Errorf("block list: got %q, want %q", got, want)
	}
	// A list given on the command line is added to, not repeated.
	if got := aiderReadArgs([]string{"--read=n.md"}, proj, ctx); !slices.Equal(got, []string{"--read", ctx}) {
		t.Errorf("command line: got %q", got)
	}
	// AIDER_READ outranks every config file.
	t.Setenv("AIDER_READ", `["e.md"]`)
	if got := aiderReadArgs(nil, proj, ctx); !slices.Equal(got, []string{"--read", "e.md", "--read", ctx}) {
		t.Errorf("AIDER_READ: got %q", got)
	}
}

// The live file stands in for the shared one: the home list is handed over
// without the shared file, so aider does not read the digest twice.
func TestAiderReadArgsSwapsTheSharedFile(t *testing.T) {
	home, proj := aiderReadHome(t)
	conf := "read:\n  - CONVENTIONS.md\n  - " + aiderContextPath() + "\n"
	if err := os.WriteFile(aiderConfPath(), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(home, "live.md")
	want := []string{"--read", "CONVENTIONS.md", "--read", live}
	if got := aiderReadArgs(nil, proj, live); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAiderChatHistory(t *testing.T) {
	_, proj := aiderReadHome(t)
	if got := aiderChatHistory(nil, proj); got != filepath.Join(proj, ".aider.chat.history.md") {
		t.Errorf("default: %s", got)
	}
	if err := os.WriteFile(filepath.Join(proj, ".aider.conf.yml"), []byte("chat-history-file: logs/h.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := aiderChatHistory(nil, proj); got != filepath.Join(proj, "logs", "h.md") {
		t.Errorf("config: %s", got)
	}
	if got := aiderChatHistory([]string{"--chat-history-file", "c.md"}, proj); got != filepath.Join(proj, "c.md") {
		t.Errorf("command line: %s", got)
	}
}

func TestAiderLastAsk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.md")
	old := "# aider chat started at 2026-10-01\n\n#### asked last week  \n\nanswer\n"
	if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	off := int64(len(old))
	if got := aiderLastAsk(p, off); got != "" {
		t.Errorf("a message from before the run: %q", got)
	}
	add := func(s string) {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(s)
		_ = f.Close()
	}
	add("\n#### why does the cache  \n#### drop warm entries?  \n")
	if got := aiderLastAsk(p, off); got != "why does the cache\ndrop warm entries?" {
		t.Errorf("two-line message: %q", got)
	}
	add("\nreply\n\n#### /ask where is evict.go  \n")
	if got := aiderLastAsk(p, off); got != "where is evict.go" {
		t.Errorf("/ask: %q", got)
	}
	add("\n#### /add evict.go  \n")
	if got := aiderLastAsk(p, off); got != "" {
		t.Errorf("an aider command is not a question: %q", got)
	}
	add("\n#### /etc/hosts is wrong on the build box  \n")
	if got := aiderLastAsk(p, off); !strings.HasPrefix(got, "/etc/hosts") {
		t.Errorf("a path is the reader's words: %q", got)
	}
}

// The live file is a pipe deja answers on every read, with the recall for the
// message aider has just written to its history.
func TestServeAiderLiveAnswersEachRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFO")
	}
	n := 0
	path, stop, err := serveAiderLive(func() string { n++; return "answer " + string(rune('0'+n)) }) //nolint:gosec // n counts a handful of calls
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"answer 1", "answer 2"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want {
			t.Errorf("read %d: %q, want %q", i, b, want)
		}
	}
	stop()
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("the pipe's directory outlived the run: %v", err)
	}
}

// aiderStandIn sets up a home with a digest for its project and an `aider` on
// PATH running script, and returns the index dir.
func aiderStandIn(t *testing.T, home, script string) string {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := filepath.Join(home, "index")
	proj := filepath.Join(home, "proj")
	bin := filepath.Join(home, "bin")
	for _, d := range []string{dir, proj, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(proj)
	cwd, _ := os.Getwd()
	writeHookCache(dir, cwd, "  - Session: **proj** `aider-1`\n", 1, 10, nil, 0, []string{"aider-1"}, []string{"proj"})
	if err := os.WriteFile(filepath.Join(bin, "aider"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func waitForFile(t *testing.T, p string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	t.Fatalf("%s never appeared", p)
}

// Two `deja aider` in one project write the same digest. The first to exit
// found the file still holding it and put the placeholder back, and the aider
// still running lost its recall mid-session (#4328).
func TestDejaAiderKeepsTheDigestWhileAnotherRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in aider is a shell script")
	}
	home := t.TempDir()
	started, release := filepath.Join(home, "started"), filepath.Join(home, "release")
	// With an argument the stand-in waits to be released; without, it exits.
	dir := aiderStandIn(t, home, "[ \"$1\" = wait ] || exit 0\ntouch '"+started+"'\nwhile [ ! -e '"+release+"' ]; do sleep 0.05; done\n")
	done := make(chan error, 1)
	go func() { done <- cmdAider(dir, []string{"wait"}, "") }()
	waitForFile(t, started)
	if err := cmdAider(dir, []string{"--exit"}, ""); err != nil {
		t.Fatal(err)
	}
	during, _ := os.ReadFile(aiderContextPath())
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(during), "aider-1") {
		t.Errorf("the aider still running lost its digest when the other exited:\n%s", during)
	}
	if after, _ := os.ReadFile(aiderContextPath()); strings.TrimSpace(string(after)) != strings.TrimSpace(aiderPlaceholder) {
		t.Errorf("the last one out left:\n%q", after)
	}
}

// show learned to say when a prefix reached more than one session (#719, #859).
// promote, handoff, resume and share resolve the same way and picked in
// silence — promote records a state against whichever one it chose (#872).
func TestEveryPrefixCommandSaysWhenItPicked(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"deja-2026-08-01-hyper-service", "deja-2026-08-01-super-service"} {
		line := `{"type":"user","message":{"role":"user","content":"pool exhausted in ` + id + `"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	elided := "deja-2026…er-service"

	for _, cmd := range []struct{ name, action string }{
		{"show", "showing"},
		{"promote", "promoting"},
		{"resume", "resuming"},
		{"share", "sharing"},
	} {
		out, err := captureRunStderr(t, cmd.name, elided)
		if err != nil {
			t.Fatalf("%s: %v", cmd.name, err)
		}
		if !strings.Contains(out, "2 sessions match") || !strings.Contains(out, cmd.action+" the most recent") {
			t.Errorf("%s picked in silence:\n%s", cmd.name, out)
		}
		if !strings.Contains(out, "the line elides") {
			t.Errorf("%s offers a prefix the reader cannot see:\n%s", cmd.name, out)
		}
	}

	// One match, no notice: the line is about a choice having been made.
	out, err := captureRunStderr(t, "show", "deja-2026-08-01-hyper-service")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sessions match") {
		t.Errorf("an unambiguous id was told it was ambiguous:\n%s", out)
	}
}

// Every answer deja snapshots lands in one injection log, and that log holds a
// record of `usage.RecordRoom` before it starts rotating on every write — at
// which point half of two concurrent injections is rewritten away (#1971). The
// budgets that decide a record's size live here and in internal/search, which
// that package cannot import, so this is where the two meet.
//
// A budget raised past this line is not wrong on its own; what is wrong is
// raising it and leaving the log's threshold where it was.
func TestEveryAnswerFitsTheRoomTheLogHas(t *testing.T) {
	for _, c := range []struct {
		name   string
		budget int
	}{
		{"recall", recallMCPBudget},
		{"recall_context", contextMCPBudget},
		{"blame", blameMCPBudget},
		{"resource", search.ContextBudget},
		{"handoff", handoffBudget},
		{"deja vu", promptHookBudget},
	} {
		if usage.RecordSize(c.budget) > usage.RecordRoom {
			t.Errorf("%s answers in %d bytes, which is %d on disk against %d of room: raise the log's threshold, not the budget",
				c.name, c.budget, usage.RecordSize(c.budget), usage.RecordRoom)
		}
	}
}

func TestAuditReportsClaudeRetentionAndUnwiredNextStep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("DEJA_XCODE_CLAUDE_ROOT", filepath.Join(home, ".none-xcode-claude"))
	t.Setenv("DEJA_CC_MIRROR_ROOT", filepath.Join(home, ".none-cc-mirror"))
	t.Setenv("DEJA_STORES", "claude")

	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-app")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	prevNow := auditNow
	auditNow = func() time.Time { return now }
	defer func() { auditNow = prevNow }()

	writeTranscript := func(name string, ageDays int) {
		p := filepath.Join(projDir, name)
		line := `{"type":"user","message":{"role":"user","content":"fix jwt rotation"},"timestamp":"2026-10-01T10:00:00Z"}` + "\n"
		if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := now.Add(-time.Duration(ageDays) * 24 * time.Hour)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	writeTranscript("fresh.jsonl", 5)
	writeTranscript("soon.jsonl", 25)
	writeTranscript("expired.jsonl", 34)

	// Aged auto-memory notes belong to Claude's store but are not deleted by cleanupPeriodDays.
	memDir := filepath.Join(projDir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	memPath := filepath.Join(memDir, "project_old.md")
	if err := os.WriteFile(memPath, []byte("# Old note\nMust not count toward transcript expiry.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTS := now.Add(-45 * 24 * time.Hour)
	if err := os.Chtimes(memPath, oldTS, oldTS); err != nil {
		t.Fatal(err)
	}

	claudeMemDir := filepath.Join(home, ".claude-mem")
	if err := os.MkdirAll(claudeMemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeMemDir, "claude-mem.db"), []byte("sqlite-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	idxDir := filepath.Join(home, ".cache", "deja")
	var out bytes.Buffer
	if err := runAudit(idxDir, nil, &out); err != nil {
		t.Fatalf("runAudit: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"2 of 3 transcripts at deletion risk (1 past 30d, 1 within 7d; default 30d)",
		"claude-mem.db detected",
		"wiring:   not wired",
		"next:     npx @vshulcz/deja-vu install --auto",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("audit output missing %q\ngot:\n%s", want, text)
		}
	}

	// Verify --json output and custom cleanupPeriodDays=0 (disabled).
	settings := []byte(`{"cleanupPeriodDays": 0}`)
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), settings, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runAudit(idxDir, []string{"--json"}, &out); err != nil {
		t.Fatalf("runAudit --json: %v", err)
	}
	var rep auditReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal audit json: %v\n%s", err, out.String())
	}
	if rep.ClaudeRetention.CleanupPeriodDays != 0 || !rep.ClaudeRetention.Explicit {
		t.Errorf("cleanup_period_days = %+v, want explicit 0", rep.ClaudeRetention)
	}
	if rep.ClaudeRetention.ExpiringIn7Days != 0 || rep.ClaudeRetention.PastPeriod != 0 {
		t.Errorf("expected 0 expiring when cleanupPeriodDays=0, got %+v", rep.ClaudeRetention)
	}
	if !rep.ClaudeMemDBPresent {
		t.Errorf("expected claude_mem_db_present = true")
	}
	if rep.NextCommand != "npx @vshulcz/deja-vu install --auto" {
		t.Errorf("next_command = %q", rep.NextCommand)
	}
}

func TestAuditRejectsUnknownFlagsAndArgs(t *testing.T) {
	var out bytes.Buffer
	if err := runAudit(t.TempDir(), []string{"--bogus"}, &out); err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if err := runAudit(t.TempDir(), []string{"extra"}, &out); err == nil {
		t.Fatal("expected error for positional argument")
	}
}

func isolateAuditEnv(t *testing.T, stores string) (root, home, xdgCache, idxDir string) {
	t.Helper()
	root = t.TempDir()
	home = filepath.Join(root, "home")
	xdgCache = filepath.Join(root, "xdg-cache")
	idxDir = filepath.Join(root, "missing-index")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg-config"))
	t.Setenv("XDG_CACHE_HOME", xdgCache)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(home, ".codex"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("DEJA_XCODE_CLAUDE_ROOT", filepath.Join(home, ".none-xcode-claude"))
	t.Setenv("DEJA_CC_MIRROR_ROOT", filepath.Join(home, ".none-cc-mirror"))
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(root, "notes.jsonl"))
	t.Setenv("DEJA_STORES", stores)
	t.Setenv("NO_COLOR", "1")
	return root, home, xdgCache, idxDir
}

func TestAuditNamedSecretAllowMatchesIndex(t *testing.T) {
	if len(auditNamedSecretAllow) == 0 {
		t.Fatal("auditNamedSecretAllow must not be empty")
	}
	for kind, ok := range auditNamedSecretAllow {
		if !ok || !index.SecretKindNamed(kind) {
			t.Errorf("auditNamedSecretAllow[%q] = %v, SecretKindNamed = %v", kind, ok, index.SecretKindNamed(kind))
		}
	}
	for _, nonNamed := range []string{"entropy", "credential", "quoted-secret"} {
		if index.SecretKindNamed(nonNamed) {
			t.Errorf("SecretKindNamed(%q) unexpectedly true", nonNamed)
		}
	}
	for _, skip := range []string{"/tmp/claude-mem.db", "/tmp/a.db", "/tmp/a.sqlite", "/tmp/a.db-wal", "/tmp/a.bin", "/tmp/img.png"} {
		if !auditShouldSkipPath(skip) {
			t.Errorf("auditShouldSkipPath(%q) = false, want true", skip)
		}
	}
	for _, scan := range []string{"/tmp/session.jsonl", "/tmp/memory.md"} {
		if auditShouldSkipPath(scan) {
			t.Errorf("auditShouldSkipPath(%q) = true, want false", scan)
		}
	}
}

func TestAuditSecretsStreamsRawTranscriptsWithoutPrintingValues(t *testing.T) {
	_, home, _, idxDir := isolateAuditEnv(t, "claude,codex")
	t.Setenv("DEJA_NO_REDACT", "1")

	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	prevNow := auditNow
	auditNow = func() time.Time { return now }
	defer func() { auditNow = prevNow }()

	antKey := "sk-ant-" + "api03-1234567890abcdefghij1234567890abcdefghij"
	ghTok := "ghp_" + "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890"
	awsSecret := "wJalrXUtnFEMI" + "/K7MDENG/bPxRfiCY" + "AUDITKEY123"
	awsAccess := "AKIA" + "IOSFODNN7EXAMPLE99"
	bearerVal := "tok_live_" + "9876543210abcdef9876543210"
	pemBody := "MIIEowIBAAKCAQEAnAuditPemExampleBody0123456789"
	skippedBinaryTok := "npm_" + "BinarySkipped0000000000000000000000000"
	skippedDBTok := "npm_" + "ClaudeMemDbSkipped0000000000000000000"

	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-app")
	memDir := filepath.Join(projDir, "memory")
	codexDir := filepath.Join(home, ".codex", "sessions", "2026", "11", "10")
	claudeMemDir := filepath.Join(home, ".claude-mem")
	for _, d := range []string{projDir, memDir, codexDir, claudeMemDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	oldLeakPath := filepath.Join(projDir, "old_leak.jsonl")
	oldLeakBody := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"export key=` + antKey + `"},"timestamp":"2026-10-17T10:00:00Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"` + ghTok + `"},"timestamp":"2026-10-17T10:01:00Z"}`,
		`{`,
		`  "aws_secret_access_key":`,
		`  "` + awsSecret + `"`,
		`}`,
	}, "\n") + "\n"
	if err := os.WriteFile(oldLeakPath, []byte(oldLeakBody), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTS := now.Add(-26 * 24 * time.Hour)
	if err := os.Chtimes(oldLeakPath, oldTS, oldTS); err != nil {
		t.Fatal(err)
	}

	pemPath := filepath.Join(memDir, "key_dump.md")
	pemText := strings.Join([]string{
		"# Recovery key note",
		"-----BEGIN " + "RSA PRIVATE KEY-----",
		pemBody,
		"-----END " + "RSA PRIVATE KEY-----",
	}, "\n") + "\n"
	if err := os.WriteFile(pemPath, []byte(pemText), 0o644); err != nil {
		t.Fatal(err)
	}

	binPath := filepath.Join(projDir, "binary_blob.jsonl")
	binPayload := append([]byte{0x00, 0x01, 0x02, 0x03}, []byte("\ntoken="+skippedBinaryTok+"\n")...)
	if err := os.WriteFile(binPath, binPayload, 0o644); err != nil {
		t.Fatal(err)
	}
	freshTS := now.Add(-2 * 24 * time.Hour)
	if err := os.Chtimes(binPath, freshTS, freshTS); err != nil {
		t.Fatal(err)
	}

	codexPath := filepath.Join(codexDir, "rollout-2026-11-10T10-00-00-c1.jsonl")
	codexBody := strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-11-10T10:00:00Z","payload":{"session_id":"c1","cwd":"/Users/alex/app"}}`,
		`{"type":"message","timestamp":"2026-11-10T10:01:00Z","payload":{"role":"user","content":"Authorization: Bearer ` + bearerVal + ` and ` + awsAccess + `"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(codexPath, []byte(codexBody), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(claudeMemDir, "claude-mem.db"), []byte("sqlite "+skippedDBTok), 0o644); err != nil {
		t.Fatal(err)
	}

	var textOut bytes.Buffer
	if err := runAudit(idxDir, []string{"--secrets"}, &textOut); err != nil {
		t.Fatalf("runAudit --secrets: %v", err)
	}
	txt := textOut.String()
	for _, want := range []string{"secrets:", "anthropic-key", "github-token", "private-key", "bearer-token", "aws-access-key", "aws-secret"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text output missing %q:\n%s", want, txt)
		}
	}

	var jsonOut bytes.Buffer
	if err := runAudit(idxDir, []string{"--secrets", "--json"}, &jsonOut); err != nil {
		t.Fatalf("runAudit --secrets --json: %v", err)
	}
	for _, rawSecret := range []string{antKey, ghTok, awsSecret, awsAccess, bearerVal, pemBody, skippedBinaryTok, skippedDBTok} {
		if strings.Contains(txt, rawSecret) {
			t.Fatalf("text output leaked raw credential %q", rawSecret)
		}
		if strings.Contains(jsonOut.String(), rawSecret) {
			t.Fatalf("json output leaked raw credential %q", rawSecret)
		}
	}

	var rep auditReport
	if err := json.Unmarshal(jsonOut.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal audit json: %v\n%s", err, jsonOut.String())
	}
	if !rep.SecretsScanned {
		t.Fatal("expected secrets_scanned = true")
	}
	if !rep.ClaudeMemDBPresent {
		t.Fatal("expected claude_mem_db_present = true")
	}
	if rep.SecretFiles != 3 {
		t.Errorf("secret_files = %d, want 3", rep.SecretFiles)
	}
	if rep.SecretMatches != 6 {
		t.Errorf("secret_matches = %d, want 6 (classes=%v)", rep.SecretMatches, rep.CredentialClasses)
	}
	if rep.CredentialClasses["npm-token"] != 0 {
		t.Errorf("unexpected npm-token match from skipped binary/db files: %v", rep.CredentialClasses)
	}

	rows := map[string]auditStoreRow{}
	for _, r := range rep.Stores {
		rows[r.Name] = r
	}
	cl := rows["claude"]
	if cl.ExpiringFiles != 1 {
		t.Errorf("claude.expiring_files = %d, want 1", cl.ExpiringFiles)
	}
	if cl.SecretFiles != 2 || cl.SecretMatches != 4 {
		t.Errorf("claude secret counts = (%d files, %d matches), want (2, 4): %+v", cl.SecretFiles, cl.SecretMatches, cl)
	}
	cx := rows["codex"]
	if cx.SecretFiles != 1 || cx.SecretMatches != 2 {
		t.Errorf("codex secret counts = (%d files, %d matches), want (1, 2): %+v", cx.SecretFiles, cx.SecretMatches, cx)
	}
}

func TestAuditFailOnSecretsExitCode(t *testing.T) {
	_, home, _, idxDir := isolateAuditEnv(t, "claude")
	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-app")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cleanFile := filepath.Join(projDir, "clean.jsonl")
	if err := os.WriteFile(cleanFile, []byte(`{"type":"user","message":{"role":"user","content":"discuss retry backoff"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var cleanJSON bytes.Buffer
	if err := runAudit(idxDir, []string{"--fail-on-secrets", "--json"}, &cleanJSON); err != nil {
		t.Fatalf("clean runAudit --fail-on-secrets returned err: %v", err)
	}
	var cleanRep auditReport
	if err := json.Unmarshal(cleanJSON.Bytes(), &cleanRep); err != nil {
		t.Fatalf("unmarshal clean report: %v", err)
	}
	if !cleanRep.SecretsScanned || cleanRep.SecretMatches != 0 {
		t.Fatalf("unexpected clean secret report: %+v", cleanRep)
	}

	ghTok := "ghp_" + "FailOnSecrets1234567890abcdefghij"
	leakFile := filepath.Join(projDir, "leak.jsonl")
	if err := os.WriteFile(leakFile, []byte(`{"type":"user","message":{"role":"user","content":"token=`+ghTok+`"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var dirtyJSON bytes.Buffer
	err := runAudit(idxDir, []string{"--fail-on-secrets", "--json"}, &dirtyJSON)
	if !errors.Is(err, errAlreadySaid) {
		t.Fatalf("expected errors.Is(err, errAlreadySaid), got %v", err)
	}
	if strings.Contains(dirtyJSON.String(), ghTok) {
		t.Fatal("json output leaked token value")
	}
	var dirtyRep auditReport
	if uerr := json.Unmarshal(dirtyJSON.Bytes(), &dirtyRep); uerr != nil {
		t.Fatalf("dirty --fail-on-secrets --json did not emit valid JSON: %v\n%s", uerr, dirtyJSON.String())
	}
	if !dirtyRep.SecretsScanned || dirtyRep.SecretMatches != 1 || dirtyRep.CredentialClasses["github-token"] != 1 {
		t.Fatalf("unexpected dirty report: %+v", dirtyRep)
	}

	var dirtyText bytes.Buffer
	err = runAudit(idxDir, []string{"--fail-on-secrets"}, &dirtyText)
	if !errors.Is(err, errAlreadySaid) {
		t.Fatalf("expected text mode errors.Is(err, errAlreadySaid), got %v", err)
	}
	if !strings.Contains(dirtyText.String(), "secrets:") || !strings.Contains(dirtyText.String(), "github-token") {
		t.Fatalf("unexpected dirty text output:\n%s", dirtyText.String())
	}
	if strings.Contains(dirtyText.String(), ghTok) {
		t.Fatal("text output leaked token value")
	}
}

func TestAuditIndexlessRunCreatesZeroFiles(t *testing.T) {
	root, home, xdgCache, idxDir := isolateAuditEnv(t, "claude")
	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-many")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for i := range 55 {
		p := filepath.Join(projDir, "sess-"+string(rune('a'+i/26))+string(rune('a'+i%26))+".jsonl")
		if err := os.WriteFile(p, []byte(`{"type":"user","message":{"role":"user","content":"hello"}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := runAudit(idxDir, []string{"--secrets"}, &out); err != nil {
		t.Fatalf("runAudit: %v", err)
	}
	if !strings.Contains(out.String(), "55 transcript files") {
		t.Fatalf("expected >=50 transcript files reported, got:\n%s", out.String())
	}

	if notice := startReleaseNotice([]string{"audit"}, true, idxDir, time.Now(), func(string) error {
		t.Fatal("audit must not spawn release notice look")
		return nil
	}); notice != nil {
		t.Fatalf("expected nil releaseNotice for audit, got %+v", notice)
	}

	for _, absent := range []string{
		idxDir,
		idxDir + ".release",
		xdgCache,
		filepath.Join(root, "xdg-config"),
		filepath.Join(root, "notes.jsonl"),
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".config"),
	} {
		if _, err := os.Stat(absent); !os.IsNotExist(err) {
			t.Errorf("expected %s to remain absent after indexless audit (stat err=%v)", absent, err)
		}
	}
}

func TestAuditStreamChunksAndSplitIndex(t *testing.T) {
	withDelim := make([]byte, 20000)
	for i := range withDelim {
		withDelim[i] = 'a'
	}
	delimPos := len(withDelim) - 128
	withDelim[delimPos] = ','
	if got, want := auditChunkSplitIndex(withDelim), delimPos+1; got != want {
		t.Fatalf("auditChunkSplitIndex(withDelim) = %d, want %d", got, want)
	}

	noDelim := make([]byte, 20000)
	for i := range noDelim {
		noDelim[i] = 'z'
	}
	if got := auditChunkSplitIndex(noDelim); got != len(noDelim) {
		t.Fatalf("auditChunkSplitIndex(noDelim) = %d, want %d", got, len(noDelim))
	}

	// Single oversized line containing a delimiter inside the trailing 16 KiB window.
	delimLine := strings.Repeat("a", auditMaxLineChunkBytes-64) + " " + strings.Repeat("b", 256)
	var chunks []string
	if err := auditStreamChunks(strings.NewReader(delimLine+"\ntail\n"), func(chunk string) {
		chunks = append(chunks, chunk)
	}); err != nil {
		t.Fatalf("auditStreamChunks(delimLine): %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("delimLine chunks = %d, want 3", len(chunks))
	}
	if chunks[0]+chunks[1] != delimLine || chunks[2] != "tail" {
		t.Fatalf("unexpected delimLine chunks: lens=%d,%d last=%q", len(chunks[0]), len(chunks[1]), chunks[2])
	}

	// Single oversized line with no delimiters hits the fallback split at len(buf).
	solidLine := strings.Repeat("q", auditMaxLineChunkBytes+64)
	chunks = chunks[:0]
	if err := auditStreamChunks(strings.NewReader(solidLine), func(chunk string) {
		chunks = append(chunks, chunk)
	}); err != nil {
		t.Fatalf("auditStreamChunks(solidLine): %v", err)
	}
	if len(chunks) != 2 || chunks[0]+chunks[1] != solidLine {
		t.Fatalf("solidLine chunks = %d (sum matches=%v), want 2", len(chunks), len(chunks) == 2 && chunks[0]+chunks[1] == solidLine)
	}
}

func TestAuditPrintReportBranchesAndJSONFailOnSecrets(t *testing.T) {
	var emptyOut bytes.Buffer
	printAuditReport(&emptyOut, auditReport{
		SecretsScanned: true,
	})
	for _, want := range []string{
		"stores:   none detected on this machine",
		"0 unredacted credentials found",
		"wiring:   not wired",
	} {
		if !strings.Contains(emptyOut.String(), want) {
			t.Errorf("empty printAuditReport missing %q\ngot:\n%s", want, emptyOut.String())
		}
	}

	var manyOut bytes.Buffer
	printAuditReport(&manyOut, auditReport{
		Stores: []auditStoreRow{
			{Name: "claude", Files: 2, Bytes: 2048, SecretFiles: 1, SecretMatches: 1},
			{Name: "codex", Files: 1, Bytes: 512},
			{Name: "cursor", Files: 1, Bytes: 512},
			{Name: "gemini", Files: 1, Bytes: 512},
			{Name: "opencode", Files: 1, Bytes: 512},
			{Name: "goose", Files: 1, Bytes: 512},
			{Name: "aider", Files: 1, Bytes: 512},
			{Name: "pi", Files: 1, Bytes: 512},
		},
		TotalFiles:        9,
		TotalBytes:        5632,
		SecretsScanned:    true,
		SecretFiles:       1,
		SecretMatches:     1,
		CredentialClasses: map[string]int{"github-token": 1},
		ClaudeRetention: auditClaudeRetention{
			CleanupPeriodDays: 0,
			Explicit:          true,
			TotalFiles:        2,
			PreservedDeleted:  3,
		},
		IndexedSessions: 4,
		WiredTargets:    []string{"claude"},
	})
	for _, want := range []string{
		"+2 more",
		"cleanupPeriodDays=0 (disabled)",
		"kept:     3 deleted Claude sessions still searchable in deja",
		"1 unredacted match across 1 file (claude (1 file))",
		"index:    4 sessions indexed",
		"1 target wired (claude)",
	} {
		if !strings.Contains(manyOut.String(), want) {
			t.Errorf("many-store printAuditReport missing %q\ngot:\n%s", want, manyOut.String())
		}
	}

	var recoverableOut bytes.Buffer
	printAuditReport(&recoverableOut, auditReport{
		Stores:         []auditStoreRow{{Name: "claude", Files: 1, Bytes: 128, SecretFiles: 1, SecretMatches: 2}},
		TotalFiles:     1,
		TotalBytes:     128,
		SecretsScanned: true,
		SecretFiles:    1,
		SecretMatches:  2,
		CredentialClasses: map[string]int{
			"github-token": 2,
		},
		ClaudeRetention: auditClaudeRetention{
			CleanupPeriodDays: 30,
			TotalFiles:        1,
		},
		IndexedSessions:  3,
		RecoverableFiles: 1,
		WiredTargets:     []string{"claude", "codex"},
	})
	for _, want := range []string{
		"2 unredacted matches across 1 file (claude (1 file))",
		"1 file path recoverable via",
		"2 targets wired (claude, codex)",
	} {
		if !strings.Contains(recoverableOut.String(), want) {
			t.Errorf("recoverable printAuditReport missing %q\ngot:\n%s", want, recoverableOut.String())
		}
	}

	_, home, _, idxDir := isolateAuditEnv(t, "claude")
	t.Setenv("DEJA_NO_REDACT", "1")
	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-json-fail")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tok := "ghp_" + "AuditJsonFail0000000000000000000000000"
	line := `{"type":"user","message":{"role":"user","content":"token ` + tok + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projDir, "leak.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	var jsonOut bytes.Buffer
	err := runAudit(idxDir, []string{"--json", "--fail-on-secrets"}, &jsonOut)
	if !errors.Is(err, errAlreadySaid) {
		t.Fatalf("runAudit(--json --fail-on-secrets) err = %v, want errAlreadySaid", err)
	}
	if strings.Contains(jsonOut.String(), tok) {
		t.Fatalf("JSON output leaked raw secret value")
	}
	var rep auditReport
	if uerr := json.Unmarshal(jsonOut.Bytes(), &rep); uerr != nil {
		t.Fatalf("unmarshal jsonOut: %v", uerr)
	}
	if rep.SecretMatches == 0 {
		t.Fatalf("expected SecretMatches > 0, got %+v", rep)
	}
}

func TestAuditStoreSpecsCoversAllHarnesses(t *testing.T) {
	specs := auditStoreSpecs()
	byName := make(map[string]auditStoreSpec, len(specs))
	for _, sp := range specs {
		if sp.files == nil {
			t.Errorf("auditStoreSpecs()[%q].files is nil", sp.name)
		}
		byName[sp.name] = sp
	}
	for _, h := range sources.HarnessNames() {
		if h == "deja" {
			continue
		}
		if _, ok := byName[h]; !ok {
			t.Errorf("auditStoreSpecs() missing harness %q", h)
		}
	}
}

const autoOffWord = "zanzibarquux"

// autoOffStore is forkStore with a transcript grown after the build, the
// warmup spawner recorded rather than run, and the test guard on warmups
// lifted so requestWarmup is the code under test.
func autoOffStore(t *testing.T) (dir string, spawned *int) {
	t.Helper()
	dir, _ = forkStore(t, false)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	n := 0
	saved := spawnWarmup
	spawnWarmup = func(_, _ string) error { n++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })
	saidManualIndex = sync.Once{}
	t.Cleanup(func() { saidManualIndex = sync.Once{} })
	growPriorSession(t, autoOffWord)
	return dir, &n
}

// growPriorSession appends a turn holding word to a session already indexed.
func growPriorSession(t *testing.T, word string) {
	t.Helper()
	transcript := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", "prior-c.jsonl")
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(`{"type":"user","sessionId":"prior-c","cwd":"/w/p","timestamp":"2026-08-04T09:05:00Z","message":{"role":"user","content":"` + word + ` showed up after the build"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
}

type manifestStamp struct {
	body  []byte
	mtime int64
}

func stampManifest(t *testing.T, dir string) manifestStamp {
	t.Helper()
	p := filepath.Join(dir, "manifest.gob")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return manifestStamp{b, fi.ModTime().UnixNano()}
}

// Every place deja updates the index on its own leaves it alone with
// DEJA_NO_AUTO_INDEX=1: CLI search and show, the TUI refresh, MCP tools, the
// session-start hook, its detached digest refresh and the warmup it asks for
// (#5385).
func TestNoAutoIndexEveryTriggerLeavesTheIndexAlone(t *testing.T) {
	dir, spawned := autoOffStore(t)
	t.Setenv("DEJA_NO_AUTO_INDEX", "1")
	before := stampManifest(t, dir)

	triggers := []struct {
		name string
		run  func()
	}{
		{"search", func() { _ = ensureForCLISearch(dir, search.Options{Query: autoOffWord}, false, io.Discard) }},
		{"show/last", func() { _ = ensureForCLIRead(dir) }},
		{"warmup", func() { _ = cmdWarmup(dir, nil) }},
		{"hook-refresh", func() { runHookRefresh(dir) }},
		{"request warmup", func() { requestWarmup(dir) }},
		{"session start", func() {
			withHookStdin(t, `{"session_id":"s-new","source":"startup","cwd":"/w/p"}`)
			_ = captureStdout(t, func() { _ = runHookContext(dir, true) })
		}},
		{"mcp recall", func() { _, _ = callMCPTool(dir, "recall", json.RawMessage(`{"query":"`+autoOffWord+`"}`)) }},
		{"mcp recall_context", func() { _, _ = callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"`+autoOffWord+`"}`)) }},
		{"mcp blame", func() { _, _ = callMCPTool(dir, "blame", json.RawMessage(`{"path":"fetch.go"}`)) }},
		{"mcp orient", func() { _, _ = callMCPTool(dir, "orient", json.RawMessage(`{}`)) }},
		{"mcp remember", func() { _, _ = callMCPTool(dir, "remember", json.RawMessage(`{"text":"a note"}`)) }},
	}
	// Each trigger gets a change of its own to pick up, so one that refreshed
	// cannot hide the next.
	for i, tr := range triggers {
		if i > 0 {
			growPriorSession(t, fmt.Sprintf("%s%d", autoOffWord, i))
		}
		prior := stampManifest(t, dir)
		_ = captureStderr(t, tr.run)
		if after := stampManifest(t, dir); !bytes.Equal(after.body, prior.body) || after.mtime != prior.mtime {
			t.Errorf("%s rewrote the index with automatic indexing off", tr.name)
		}
	}
	if *spawned != 0 {
		t.Errorf("%d detached warmup(s) started with automatic indexing off", *spawned)
	}
	if _, err := os.Stat(filepath.Join(dir, "warmup.sentinel")); err == nil {
		t.Error("a warmup was asked for with automatic indexing off")
	}
	if r, err := index.SearchDetailed(dir, search.Options{Query: autoOffWord, All: true}); err != nil || len(r.Sessions) != 0 {
		t.Errorf("the change reached the index: %d sessions (%v)", len(r.Sessions), err)
	}

	// `deja index` is the one writer left, and its warmup-child form is not.
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(t.TempDir(), "warmup.sentinel"))
	_ = captureStderr(t, func() { _ = cmdIndex(dir, nil) })
	if after := stampManifest(t, dir); !bytes.Equal(after.body, before.body) {
		t.Error("a detached warmup's `deja index` rewrote the index with automatic indexing off")
	}
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	_ = captureStderr(t, func() {
		if err := cmdIndex(dir, nil); err != nil {
			t.Errorf("deja index: %v", err)
		}
	})
	if r, err := index.SearchDetailed(dir, search.Options{Query: autoOffWord, All: true}); err != nil || len(r.Sessions) != 1 {
		t.Errorf("deja index did not pick the change up: %d sessions (%v)", len(r.Sessions), err)
	}
}

// The control: without the switch the same search picks the change up, so the
// test above is not passing on a store that never refreshes.
func TestDefaultModeSearchStillRefreshes(t *testing.T) {
	dir, _ := autoOffStore(t)
	before := stampManifest(t, dir)
	_ = captureStderr(t, func() {
		if err := ensureForCLISearch(dir, search.Options{Query: autoOffWord}, false, io.Discard); err != nil {
			t.Error(err)
		}
	})
	if after := stampManifest(t, dir); bytes.Equal(after.body, before.body) {
		t.Error("a search in the default mode did not refresh the index")
	}
	if r, err := index.SearchDetailed(dir, search.Options{Query: autoOffWord, All: true}); err != nil || len(r.Sessions) != 1 {
		t.Errorf("the change is not searchable after the refresh: %d sessions (%v)", len(r.Sessions), err)
	}
}

// Answers say how old the index is: the CLI search, MCP recall and
// recall_context, and the session-start digest. The default mode says nothing.
func TestNoAutoIndexAnswersCarryTheIndexDate(t *testing.T) {
	dir, _ := autoOffStore(t)
	const dated = "automatic indexing is off — answering from the index of "
	q := json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`)

	plain, err := callMCPTool(dir, "recall", q)
	if err != nil || strings.Contains(plain, dated) {
		t.Fatalf("default-mode recall carried the date line (%v):\n%s", err, plain)
	}

	t.Setenv("DEJA_NO_AUTO_INDEX", "1")
	var progress bytes.Buffer
	if err := ensureForCLISearch(dir, search.Options{Query: "retry"}, false, &progress); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(progress.String(), dated) || strings.Count(progress.String(), "\n") != 1 {
		t.Errorf("search said %q, want one line with the index date", progress.String())
	}
	for _, tool := range []string{"recall", "recall_context"} {
		out, err := callMCPTool(dir, tool, q)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if !strings.HasPrefix(out, "deja: "+dated) || !strings.Contains(out, "<deja-recall>") {
			t.Errorf("%s did not answer with the index date:\n%s", tool, out)
		}
	}
	withHookStdin(t, `{"session_id":"s-dated","source":"startup","cwd":"/w/p"}`)
	out := captureStdout(t, func() { _ = runHookContext(dir, true) })
	if !strings.Contains(out, "deja-recall") || !strings.Contains(out, dated) {
		t.Errorf("the session-start digest did not carry the index date:\n%s", out)
	}
}

// With no index at all nothing builds; each surface says so and names the
// command.
func TestNoAutoIndexWithNoIndexSaysHowToBuildIt(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	n := 0
	saved := spawnWarmup
	spawnWarmup = func(_, _ string) error { n++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", "a.jsonl"),
		`{"type":"user","sessionId":"a","cwd":"/w/p","timestamp":"2026-08-04T09:00:00Z","message":{"role":"user","content":"retry loop"}}`+"\n")
	// The policy file, so the other way to set the switch is covered too.
	pol := filepath.Join(os.Getenv("HOME"), ".config", "deja", "policy.json")
	write(pol, `{"auto_index": false}`)
	dir := index.DefaultDir()

	err := ensureForCLISearch(dir, search.Options{Query: "retry"}, false, io.Discard)
	if err == nil || !strings.Contains(ensureError(dir, err).Error(), "build it with: deja index") || strings.HasPrefix(ensureError(dir, err).Error(), "ensure:") {
		t.Errorf("search over no index = %v", err)
	}
	if out, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"retry"}`)); err != nil || !strings.Contains(out, "deja index") || strings.Contains(out, "ask again") {
		t.Errorf("recall over no index = %q, %v", out, err)
	}
	withHookStdin(t, `{"session_id":"s1","source":"startup","cwd":"/w/p"}`)
	out := captureStdout(t, func() { _ = runHookContext(dir, false) })
	if !strings.Contains(out, "build it with: deja index") {
		t.Errorf("session start over no index said:\n%s", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the index directory was created: %v", err)
	}
	if n != 0 {
		t.Errorf("%d warmup(s) started", n)
	}
}

// The switch reaches hooks a host starts with a stripped environment: the
// launcher carries it from install time, as it does DEJA_INDEX_DIR (#5323).
func TestLauncherCarriesNoAutoIndex(t *testing.T) {
	got := launcherEnv([]string{"DEJA_NO_AUTO_INDEX=1"})
	if len(got) != 1 || got[0] != [2]string{"DEJA_NO_AUTO_INDEX", "1"} {
		t.Errorf("launcher env = %v", got)
	}
}

// After an upgrade that only bumps the content version the index still
// answers; with automatic indexing off MCP recall answers from it, as search
// does, rather than telling the agent to wait for a rebuild.
func TestNoAutoIndexRecallAnswersAcrossAVersionBump(t *testing.T) {
	dir, spawned := autoOffStore(t)
	t.Setenv("DEJA_NO_AUTO_INDEX", "1")
	if err := index.SetManifestVersionForTest(dir, index.CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	out, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"retry loop fetch.go HTTP 500"}`))
	if err != nil || !strings.Contains(out, "<deja-recall>") || !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("recall over an older content version (%v):\n%s", err, out)
	}
	if *spawned != 0 {
		t.Error("a warmup was started")
	}
}

// `deja install --auto` is the command the docs put in front of everyone, and
// the mapping from "this harness is on the machine" to "install its deepest
// integration" used to be a switch someone had to remember to extend. Two
// harnesses landed after it was written and fell through to the default, so
// --auto wired their MCP server and left auto-recall off — on a machine that
// had it, with nothing in the output saying so.
func TestEveryAutoTargetIsReachableFromInstallAuto(t *testing.T) {
	for _, name := range installTargetNames() {
		if !strings.HasSuffix(name, "-auto") {
			continue
		}
		base := strings.TrimSuffix(name, "-auto")
		// Detection reports Claude under its own name.
		detected := base
		if base == "claude" {
			detected = "claude-code"
		}
		if got := autoTargetFor(detected); got != name {
			t.Errorf("a machine with %s installs %q, not %q — auto-recall is silently skipped", detected, got, name)
		}
	}

	// A harness with no -auto target keeps the plain one rather than being
	// dropped: for the IDE extensions the MCP server is the whole integration.
	for _, plain := range []string{"windsurf", "roo"} {
		if got := autoTargetFor(plain); got != plain && !strings.HasPrefix(got, plain+"-auto") {
			t.Errorf("autoTargetFor(%q) = %q", plain, got)
		}
	}
}

// The published numbers used to be re-runnable and not checkable: the harnesses
// were committed and the runs were not, so a reader either took the docs on
// faith or re-ran the benchmark themselves. The independent review at
// neoneye/agent-memory-atlas said exactly that.
//
// docs/benchmarks/ now holds what each run wrote, and this pins the guide page
// to those files: every headline figure on the page has to be the figure in the
// artifact, rounded the way the page rounds it. A re-run that moves a number
// moves the page or fails here.
type benchArtifact struct {
	Benchmark string `json:"benchmark"`
	Questions int    `json:"questions"`
	Dataset   struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	} `json:"dataset"`
	Rows  []benchRow `json:"rows"`
	Total benchRow   `json:"total"`
	Extra struct {
		EvidenceRecall map[string]float64 `json:"evidence_recall"`
	} `json:"extra"`
}

type benchRow struct {
	Name  string   `json:"name"`
	N     int      `json:"n"`
	Hit1  float64  `json:"hit@1"`
	Hit5  float64  `json:"hit@5"`
	Hit10 *float64 `json:"hit@10"`
	Hit20 *float64 `json:"hit@20"`
	MRR   float64  `json:"mrr"`
}

func TestBenchmarkPageMatchesTheCommittedRuns(t *testing.T) {
	root := filepath.Join("..", "..")
	page, err := os.ReadFile(filepath.Join(root, "docs", "guide", "benchmarks.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)

	read := func(name string) benchArtifact {
		b, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", name))
		if err != nil {
			t.Fatalf("%s: %v — the page cites a run whose record is missing", name, err)
		}
		var a benchArtifact
		if err := json.Unmarshal(b, &a); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if a.Questions == 0 || a.Total.N == 0 || a.Dataset.SHA256 == "" {
			t.Fatalf("%s: a record without questions, a total or a dataset digest", name)
		}
		if len(a.Rows) < 2 {
			t.Fatalf("%s: %d slices, so the record is a total with nothing behind it", name, len(a.Rows))
		}
		return a
	}

	lme := read("longmemeval-s-cleaned.json")
	full := read("longmemeval-s-full.json")
	locomo := read("locomo.json")

	// One decimal is how the page writes a percentage, and three how it writes
	// an MRR; anything the page states has to survive that rounding.
	pct1 := func(v float64) string { return fmt.Sprintf("%.1f%%", v) }
	mrr3 := func(v float64) string { return fmt.Sprintf("%.3f", v) }

	for _, want := range []struct{ what, text string }{
		{"LongMemEval questions", fmt.Sprintf("%d questions on the cleaned set", lme.Questions)},
		{"LongMemEval hit@1", "hit@1 " + pct1(lme.Total.Hit1)},
		{"LongMemEval hit@5", "hit@5 " + pct1(lme.Total.Hit5)},
		{"LongMemEval MRR", "MRR " + mrr3(lme.Total.MRR)},
		{"the full-set questions", fmt.Sprintf("(%d questions, abstention included)", full.Total.N)},
		{"the full-set hit@1", "hit@1 " + pct1(full.Total.Hit1)},
		{"the full-set MRR", "MRR " + mrr3(full.Total.MRR)},
		{"LoCoMo questions", withThousands(locomo.Total.N) + " questions"},
		{"LoCoMo hit@1", "hit@1 " + pct1(locomo.Total.Hit1)},
		{"LoCoMo hit@5", "hit@5 " + pct1(locomo.Total.Hit5)},
		{"LoCoMo MRR", "MRR " + mrr3(locomo.Total.MRR)},
	} {
		if !strings.Contains(html, want.text) {
			t.Errorf("benchmarks.html does not state %s as the run recorded it: %q",
				want.what, want.text)
		}
	}

	if lme.Total.Hit10 == nil || lme.Total.Hit20 == nil {
		t.Fatal("the LongMemEval record has no hit@10 or hit@20")
	}
	for _, want := range []string{
		"hit@10 " + pct1(*lme.Total.Hit10),
		"hit@20 " + pct1(*lme.Total.Hit20),
	} {
		if !strings.Contains(html, want) {
			t.Errorf("benchmarks.html does not state %q", want)
		}
	}

	// The weakest slice is named on the page with its own number, and it is the
	// one a reader checks first.
	for _, r := range lme.Rows {
		if r.Name == "single-session-preference" {
			if !strings.Contains(html, pct1(r.Hit1)+" hit@1") {
				t.Errorf("the preference slice is %s in the run and the page does not say so", pct1(r.Hit1))
			}
		}
	}

	for _, want := range []struct{ at, text string }{
		{"@1", "evidence-recall@1 " + pct1(lme.Extra.EvidenceRecall["@1"])},
		{"@5", "@5 " + pct1(lme.Extra.EvidenceRecall["@5"])},
	} {
		if !strings.Contains(html, want.text) {
			t.Errorf("benchmarks.html does not state evidence recall %s as recorded: %q", want.at, want.text)
		}
	}
}

// The headline figure is quoted in six other places, and only the guide page
// was pinned to the run. Nothing said which run: the committed record is the
// 470-question cleaned set, `-skip-abs`, and a re-run without that flag returns
// 84.8% — which reads as drift in every one of these files and cost an hour of
// chasing a number that had not moved. The guide page states both denominators;
// the short quotes state one, so they have to state the one the record holds.
func TestQuotedHeadlineNumbersComeFromTheRuns(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "longmemeval-s-cleaned.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lme benchArtifact
	if err = json.Unmarshal(b, &lme); err != nil {
		t.Fatal(err)
	}
	locomoRaw, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "locomo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var locomo benchArtifact
	if err = json.Unmarshal(locomoRaw, &locomo); err != nil {
		t.Fatal(err)
	}

	fullRaw, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "longmemeval-s-full.json"))
	if err != nil {
		t.Fatal(err)
	}
	var full benchArtifact
	if err = json.Unmarshal(fullRaw, &full); err != nil {
		t.Fatal(err)
	}

	pct1 := func(v float64) string { return fmt.Sprintf("%.1f", v) }
	wantHit1 := pct1(lme.Total.Hit1)
	wantFull := pct1(full.Total.Hit1)
	wantLoCoMo := pct1(locomo.Total.Hit1)

	// In these files hit@1 is only ever ours — the comparison table quotes the
	// neighbours' own metrics under their own names (R@5, BEAM) and never a
	// hit@1. Which of our runs a quote belongs to is read off the words beside
	// it: the full set's denominator, or the name of the other dataset.
	// "hit@1" must not match inside "hit@10", which is a different depth and a
	// different number.
	quoted := regexp.MustCompile(`([0-9]+\.[0-9])%\s*hit@1([^0-9]|$)`)
	for _, name := range []string{
		"README.md",
		"docs/readme/README.zh.md",
		"npm/README.md",
		"docs/llms.txt",
		"docs/guide/compare.html",
	} {
		raw, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if readErr != nil {
			t.Fatalf("%s: %v", name, readErr)
		}
		text := string(raw)
		// By offset, not by the matched text: two quotes in one file can read
		// the same and belong to different runs, and searching for the string
		// finds the first one every time.
		found := quoted.FindAllStringSubmatchIndex(text, -1)
		if len(found) == 0 {
			t.Errorf("%s no longer quotes hit@1; if that is on purpose, take it out of this list", name)
		}
		for _, m := range found {
			whole, got := text[m[0]:m[1]], text[m[2]:m[3]]
			// A quote that names the full set is about the other run. The
			// comparison table does that on purpose, because the neighbour's
			// cell states its own 500-question denominator and a row where
			// only one side says what it counted is not a comparison.
			want := wantHit1
			switch {
			case namesBefore(text, m[0], "LoCoMo"):
				want = wantLoCoMo
			case namesTheFullSet(text, m[0], m[1], full.Total.N):
				want = wantFull
			}
			if got != want {
				t.Errorf("%s says %q; the run it names scored %s%%", name, whole, want)
			}
		}
		// Both READMEs put the LoCoMo figure next to it, in a sentence that
		// names no metric.
		if strings.Contains(text, "LoCoMo") && strings.Contains(name, "README") {
			if !strings.Contains(text, wantLoCoMo+"%") {
				t.Errorf("%s does not carry the LoCoMo run's %s%%", name, wantLoCoMo)
			}
		}
	}

	// The landing page animates the figure, so it lives in an attribute no
	// text search reaches. The label under the counter is what identifies it.
	index, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	// It quotes R@5 on the full set, because that is the metric and the
	// denominator MemPalace and agentmemory publish beside it.
	got, ok := countedBefore(string(index), "R@5 · LongMemEval-S, all 500 questions")
	if !ok {
		t.Fatal("docs/index.html has no counter labelled R@5 · LongMemEval-S, all 500 questions")
	}
	if want := pct1(full.Total.Hit5); got != want {
		t.Errorf("the landing counter is %s; the committed full run is %s", got, want)
	}
	if full.Total.N != 500 {
		t.Errorf("the landing label says 500 questions; the full run counted %d", full.Total.N)
	}
}

// namesTheFullSet reports whether a quoted figure says, within the sentence
// around it, that it counted every question rather than the cleaned set.
// namesBefore reports whether a word introduces a quoted figure — within the
// few characters in front of it, never after. The comparison row states our
// LoCoMo hit@1 beside our LongMemEval one and the two come from different runs;
// looking after the figure as well would read the READMEs' "88.1% hit@1 ·
// LoCoMo 70.6%" as one claim about LoCoMo.
func namesBefore(text string, start int, word string) bool {
	lo := max(start-25, 0)
	return strings.Contains(text[lo:start], word)
}

func namesTheFullSet(text string, start, end, n int) bool {
	// A narrow window on purpose: one cell states both runs, so a wide one
	// reads the full set's denominator as the cleaned figure's too.
	lo := max(start-40, 0)
	hi := min(end+40, len(text))
	if strings.Contains(text[end:hi], "470") {
		return false
	}
	return strings.Contains(text[lo:hi], strconv.Itoa(n))
}

// countedBefore returns the data-count value of the counter a label belongs to.
func countedBefore(html, label string) (string, bool) {
	before, _, ok := strings.Cut(html, label)
	if !ok {
		return "", false
	}
	const attr = `data-count="`
	open := strings.LastIndex(before, attr)
	if open < 0 {
		return "", false
	}
	rest := html[open+len(attr):]
	before0, _, ok0 := strings.Cut(rest, "\"")
	if !ok0 {
		return "", false
	}
	return before0, true
}

// withThousands writes 1982 the way the page does.
func withThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Every writer that can add a container to someone else's config, seeded twice:
// once where the reader has no such block, once where they have one of their own
// with an entry in it. Install, uninstall, and the file has to come back byte
// for byte.
//
// #2604 gave the JSON writers provenance for the block they add and #2672 gave
// it to hermes' plugins — found by a whole-machine round trip somebody happened
// to run. Each fix is pinned on its own writer and nothing pinned the set, which
// is the state the ignore rule was in before #2661 (#2678).
//
// The reader's own entries are written expanded rather than inline: an inline
// object is still re-flowed by the JSON writers (#2640), which is a separate
// complaint and would drown this one.

func TestEveryWriterGivesBackTheBlockItMade(t *testing.T) {
	for _, tc := range []struct {
		name, target, rel, body string
	}{
		{"cline, no block", "cline", ".cline/data/settings/cline_mcp_settings.json", "{\n  \"theme\": \"dark\"\n}\n"},
		{"cline, their own server", "cline", ".cline/data/settings/cline_mcp_settings.json",
			"{\n  \"mcpServers\": {\n    \"theirs\": {\n      \"command\": \"x\"\n    }\n  }\n}\n"},
		{"openclaw, no block", "openclaw", ".openclaw/openclaw.json", "{\n  \"theme\": \"dark\"\n}\n"},
		{"openclaw, their own server", "openclaw", ".openclaw/openclaw.json",
			"{\n  \"mcpServers\": {\n    \"theirs\": {\n      \"command\": \"x\"\n    }\n  }\n}\n"},
		{"pi, no block", "pi", ".pi/agent/mcp.json", "{\n  \"theme\": \"dark\"\n}\n"},
		{"omp, no block", "omp", ".omp/agent/mcp.json", "{\n  \"theme\": \"dark\"\n}\n"},
		{"kimi, no block", "kimi", ".kimi-code/mcp.json", "{\n  \"theme\": \"dark\"\n}\n"},
		// Zed keeps its settings where the platform puts them rather than
		// under ~/.config, so this row asks rather than spells (#2808).
		// Zed keeps its settings where the platform puts them rather than under
		// ~/.config, and the answer depends on the home this subtest sets — so
		// the row names nothing and the path is asked for inside the run
		// (#2808).
		{"zed, no block", "zed", "", "{\n  \"theme\": \"dark\"\n}\n"},
		{"deepseek, no patch list", "deepseek", ".dsh/cordis.patch.yml", "# my patches\n"},
		{"deepseek, their own patch", "deepseek", ".dsh/cordis.patch.yml",
			"# my patches\n- insert:\n    - id: mine\n      name: \"@me/thing\"\n"},
		{"goose, no extensions block", "goose", ".config/goose/config.yaml", "GOOSE_MODEL: gpt-5\n"},
		{"goose, their own extension", "goose", ".config/goose/config.yaml",
			"GOOSE_MODEL: gpt-5\n\nextensions:\n  mine:\n    enabled: true\n"},
		{"hermes, no mcp block", "hermes", ".hermes/config.yaml", "profile: default\n"},
		{"hermes, their own server", "hermes", ".hermes/config.yaml",
			"profile: default\n\nmcp_servers:\n  theirs:\n    command: \"x\"\n"},
		{"codex, no mcp block", "codex", ".codex/config.toml", "[tools]\nweb = true\n"},
		{"grok, no mcp block", "grok", ".grok/config.toml", "[tools]\nweb = true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setTestHome(t, home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			path := homeConfigPath(home, tc.rel)
			if tc.rel == "" {
				path = sources.ZedSettingsPath()
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(tc.target, "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			wired, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(wired) == tc.body {
				t.Fatalf("install changed nothing, so the uninstall proves nothing:\n%s", wired)
			}
			if _, err = installTarget(tc.target, "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			back, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("the reader's config is gone: %v", err)
			}
			if string(back) != tc.body {
				t.Fatalf("the config did not come back as it was:\nwant:\n%s\ngot:\n%s", tc.body, back)
			}
		})
	}
}

// A day bucket's date is the day the index was built in; the times inside are
// this machine's. Read in another zone the two disagree, and the screen that
// shows them together said nothing (#1006).
func TestBucketDayIsExplainedOnlyWhenItDiffers(t *testing.T) {
	at := time.Date(2026, 8, 4, 6, 30, 0, 0, time.UTC)
	s := model.Session{Harness: "deja", ID: "deja-2026-08-04-w20", Project: "w20",
		Messages: []model.Message{{Role: "user", Text: "morning: the pool cap is 20", Time: at}}}

	saved := time.Local
	t.Cleanup(func() { time.Local = saved })

	// Read where the record falls on the day the id names: no line.
	time.Local = time.UTC
	if note := bucketDayNote(s); note != "" {
		t.Errorf("a bucket read in its own zone was explained anyway: %q", note)
	}

	// Seven hours west, the same record is the previous day.
	time.Local = time.FixedZone("west", -7*60*60)
	note := bucketDayNote(s)
	if note == "" {
		t.Fatal("nothing explains an id dated a day after the line inside it")
	}
	if !strings.Contains(note, "2026-08-04") || !strings.Contains(note, "day the index was built in") {
		t.Errorf("the note does not say what the id's date is: %q", note)
	}

	// A transcript whose id merely looks like a bucket is not one: only deja's
	// own notes are grouped by day.
	other := s
	other.Harness = "claude"
	if n := bucketDayNote(other); n != "" {
		t.Errorf("a transcript picked up the bucket note: %q", n)
	}
}

// Every line deja shows while a build runs promised "in a few seconds".
// In practice, a full rebuild is 59 seconds — and
// the progress the same line carries says so itself: 3% of the sessions read is
// not a few seconds from done. The sentences an agent sees while waiting should
// not claim what deja can already see is false (#2598).
func TestTheBuildLinesDoNotPromiseSecondsTheyCannotKnow(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   warmupStatus
		want string
	}{
		{"just started", warmupStatus{Phase: "sessions", Done: 3, Total: 100}, "3%"},
		{"nearly done", warmupStatus{Phase: "sessions", Done: 97, Total: 100}, "97%"},
		{"no count yet", warmupStatus{Phase: "sessions"}, "indexing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := tc.st.line()
			if !strings.Contains(line, tc.want) {
				t.Fatalf("line = %q, want it to carry %q", line, tc.want)
			}
			if strings.Contains(line, "few seconds") {
				t.Errorf("the line promises seconds it cannot know: %q", line)
			}
			// And it still says what it is doing and what the reader waits for.
			if !strings.Contains(line, "recall") {
				t.Errorf("the line stopped saying what the reader is waiting for: %q", line)
			}
		})
	}

	// The same claim lived on four surfaces; none of them may make it.
	t.Run("the other surfaces", func(t *testing.T) {
		hermeticEnv(t)
		for _, s := range []string{
			emptyRecallAnswerPolicy(t.TempDir(), "anything", 0),
			rememberSavedNote(t.TempDir()),
		} {
			if strings.Contains(s, "few seconds") {
				t.Errorf("still promising seconds: %q", s)
			}
		}
	})
}

// Every "memory is on its way" surface reads <index>.warmup, and the file did
// not exist until the build proper began — the walk over the stores comes
// first and, on a slow volume, takes the longest. Measured on 6000 sessions:
// first published at 0.76s before, 0.02s after (#1021).
func TestProgressIsPublishedBeforeTheFreshnessWalk(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")

	stop := publishBuildProgress(dir)
	if _, err := os.Stat(dir + ".warmup"); err != nil {
		t.Fatalf("nothing published before the walk: %v", err)
	}
	if st := readWarmupStatus(dir); st == nil {
		t.Error("the published file does not read back as a build in flight")
	}
	stop()
	if _, err := os.Stat(dir + ".warmup"); err == nil {
		t.Error("the progress file outlived the command")
	}

	// Without the sentinel this is an ordinary foreground run and publishes
	// nothing, as before.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	stop = publishBuildProgress(dir)
	if _, err := os.Stat(dir + ".warmup"); err == nil {
		t.Error("a foreground run published a warmup status")
	}
	stop()
}

// The walk itself reports, so the phase name is not "starting" for the seconds
// it takes on a network volume (#1021).
func TestTheStoreWalkReportsItsPhase(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a session"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"s","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &phaseRecorder{}
	index.SetProgress(p)
	t.Cleanup(func() { index.SetProgress(nil) })
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if !p.seen("finding transcripts") {
		t.Errorf("the store walk published no phase: %v", p.phases)
	}
}

type phaseRecorder struct{ phases []string }

func (p *phaseRecorder) Phase(name string, _ int) { p.phases = append(p.phases, name) }
func (p *phaseRecorder) Advance(int)              {}
func (p *phaseRecorder) Harness(string, int, int) {}
func (p *phaseRecorder) seen(name string) bool {
	return slices.Contains(p.phases, name)
}

// Per-harness build lines count transcripts. Once any of them share an id the
// lines add up to more than the index holds, and the line that would settle it
// was TTY-only — not where anyone counting is looking (#1091).
func TestRebuildReconcilesWhenTranscriptsCollapse(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	const turn = `{"type":"user","sessionId":"dup1","timestamp":"2026-05-01T10:00:00Z","message":{"role":"user","content":"the pool cap decision"}}`
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-dup", "a.jsonl"), "dup1", []string{turn})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-dup", "b.jsonl"), "dup1", []string{turn})

	out, err := captureRunStderr(t, "index", "--rebuild")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "an id with another transcript") {
		t.Fatalf("fixture did not collapse anything:\n%s", out)
	}
	re := regexp.MustCompile(`deja: indexed (\d+) sessions?, (\d+) messages?`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("a build that merged rows never says what the index actually holds:\n%s", out)
	}

	// The reconciling numbers must be the smaller, post-merge ones.
	perHarness := regexp.MustCompile(`deja: \w+: (\d+) sessions?, (\d+) messages?`).FindAllStringSubmatch(out, -1)
	sumSessions := 0
	for _, p := range perHarness {
		n, _ := strconv.Atoi(p[1])
		sumSessions += n
	}
	indexed, _ := strconv.Atoi(m[1])
	if indexed >= sumSessions {
		t.Errorf("reconciling line says %d sessions, per-harness lines sum to %d — expected the smaller number:\n%s",
			indexed, sumSessions, out)
	}
}

// Three transcripts of one id are one session sharing its id, not two, and
// --quiet keeps the line out like the other counts.
func TestTheSharedIDLineCountsSessionsAndKeepsQuiet(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	const turn = `{"type":"user","sessionId":"dup1","timestamp":"2026-05-01T10:00:00Z","message":{"role":"user","content":"the pool cap decision"}}`
	for _, f := range []string{"a.jsonl", "b.jsonl", "c.jsonl"} {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-dup", f), "dup1", []string{turn})
	}
	out, err := captureRunStderr(t, "index", "--rebuild")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deja: 1 session shares an id with another transcript") {
		t.Errorf("three transcripts of one id, want 1 session in the line:\n%s", out)
	}
	out, err = captureRunStderr(t, "index", "--rebuild", "--quiet")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "an id with another transcript") {
		t.Errorf("--quiet still prints the shared-id line:\n%s", out)
	}
}

// Both commands stopped at their limit without a word, so eight of thirteen
// read as thirteen — the misread #1608 fixed on the search screen, in the two
// places it was still true (#1632).
func TestHowSaysWhenItCutTheList(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 13 {
		id := "0000000" + string(rune('a'+i)) + "-1111-4000-8000-d6e7f8a9b0c1"
		line := `{"type":"assistant","timestamp":"2026-07-0` + string(rune('1'+i%9)) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./... -run Case` + string(rune('a'+i)) + `"}}]}}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer
	if err := runHow(dir, []string{"go", "test", "--limit", "3"}, &b); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(b.String(), "$ go test"); n != 3 {
		t.Fatalf("expected three entries, got %d:\n%s", n, b.String())
	}
	// The note goes where search puts its own: stderr, so stdout stays the list.
	note, err := captureRunStderr(t, "how", "go", "test", "--limit", "3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "showing 3 of 13") || !strings.Contains(note, "--limit") {
		t.Errorf("the cut is silent — nothing says how many there were or how to see them:\n%s", note)
	}
	// The control: an uncut list says nothing extra.
	note, err = captureRunStderr(t, "how", "go", "test", "--limit", "20")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "showing") {
		t.Errorf("a complete answer still advertised the flag:\n%s", note)
	}
}

// files caps the same way and was equally silent (#1632).
func TestFilesSaysWhenItCutTheList(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Real files under a real repository: files skips paths it cannot find on
	// this disk, and the cap is only reached once rows survive that.
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, 7)
	for i := range 6 {
		name := filepath.Join(repo, "file"+string(rune('a'+i))+".go")
		if err := os.WriteFile(name, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// The path goes in as JSON, not as text: on Windows it starts `D:\a\`
		// and a pasted backslash is an invalid escape, so the whole record was
		// dropped and the test saw an uncut list (#1676).
		quoted, err := json.Marshal(name)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, `{"type":"assistant","timestamp":"2026-07-01T10:0`+string(rune('0'+i))+`:00Z","sessionId":"aaaa0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":`+string(quoted)+`}}]}}`)
	}
	lines = append(lines, `{"type":"user","timestamp":"2026-07-01T10:09:00Z","sessionId":"aaaa0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"user","content":"the retry loop keeps firing"}}`)
	if err := os.WriteFile(filepath.Join(store, "aaaa0001.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	note, err := captureRunStderr(t, "files", "retry", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "showing 2 of 6") {
		t.Errorf("files cut the list in silence:\n%s", note)
	}
	// The control: nothing cut, nothing said.
	note, err = captureRunStderr(t, "files", "retry", "--limit", "50")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "showing") {
		t.Errorf("a complete answer still advertised the flag:\n%s", note)
	}
}

type capRegistry struct {
	Harnesses []struct {
		ID           string `json:"id"`
		DisplayName  string `json:"display_name"`
		Capabilities *struct {
			MCP     bool   `json:"mcp"`
			Auto    bool   `json:"auto"`
			Skill   bool   `json:"skill"`
			Command bool   `json:"command"`
			Resume  bool   `json:"resume"`
			Handoff string `json:"handoff"`
		} `json:"capabilities"`
		Gaps map[string]struct {
			State  string `json:"state"`
			Why    string `json:"why"`
			Source string `json:"source"`
		} `json:"gaps"`
	} `json:"harnesses"`
}

// A capability we do not have is one of four different things, and a bare
// "false" in the matrix reads as all of them at once. Only "todo" is work;
// "impossible" is a fact about the harness, "blocked" decays into work when
// someone else fixes their bug, and "unknown" is an admission that nobody has
// looked. Every false capability carries one, so no dash goes unexplained.
var gapStates = map[string]bool{"todo": true, "impossible": true, "blocked": true, "unknown": true}

// The capability matrix in README/site is generated from the registry; this
// test pins the registry to what the code actually does, so the published
// matrix cannot drift from behavior.
func TestCapabilityRegistryMatchesCode(t *testing.T) {
	hermeticEnv(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg capRegistry
	if err = json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	// The registry names a harness; install names a target. Where the two differ
	// the checks below have to ask under the target's id, or they answer about a
	// harness deja does not wire under that name — copilot-chat's guidance is
	// written by the vscode target, and without this the skill check read false
	// on a harness that gets a file.
	installID := map[string]string{"claude": "claude-code", "copilot-chat": "vscode"}
	// aider is auto-capable without an -auto target: the wrapper refreshes the
	// read-only file, which aider re-reads on every message.
	autoCapable := map[string]bool{"claude": true, "codex": true, "opencode": true, "aider": true}
	seen := 0
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		if h.Capabilities == nil {
			t.Fatalf("registry entry %q has no capabilities block", h.ID)
		}
		if h.DisplayName == "" {
			t.Fatalf("registry entry %q has no display_name", h.ID)
		}
		seen++
		c := h.Capabilities

		// MCP: an install target must exist and write real wiring.
		id := h.ID
		if v, ok := installID[h.ID]; ok {
			id = v
		}
		r, installErr := installTarget(id, "/bin/deja", false)
		gotMCP := installErr == nil && r.Action != "" && r.Action != "guidance-only"
		if h.ID == "aider" {
			// aider has an install target but no MCP client: what it writes is
			// the read: key, and recall arrives through `deja aider`.
			gotMCP = false
		}
		if gotMCP != c.MCP {
			t.Fatalf("%s: registry mcp=%v, code says %v", h.ID, c.MCP, gotMCP)
		}

		// Auto-recall hooks exist only where an -auto target installs.
		if c.Auto != autoCapable[h.ID] {
			if _, autoErr := installTarget(h.ID+"-auto", "/bin/deja", false); (autoErr == nil) != c.Auto {
				t.Fatalf("%s: registry auto=%v disagrees with install targets", h.ID, c.Auto)
			}
		}

		// Skill: guidance is a skill file only where install owns the whole
		// file. Everywhere else it is a marked block inside a file the user
		// owns, which is loaded for the whole session rather than on demand.
		gotSkill := guidanceOwnsWholeFile(id)
		// Grok keeps its own guidance file and gets the shared skill besides,
		// so the whole-file check does not describe it.
		if h.ID == "grok" {
			gotSkill = true
		}
		// Kilo Code, gajae-code, Command Code and Cherry Studio read skills
		// from a directory rather than from an instructions file, and their own
		// install target writes the file — the same table doctor's guidance
		// column reads. Kiro is in it too and is not a skill: a steering file
		// is always included rather than opened by name, so the path has to say
		// which kind it is.
		if own := ownGuidanceFile(h.ID); own != "" {
			gotSkill = strings.Contains(own, filepath.Join("skills", "deja-search")) ||
				strings.Contains(own, filepath.Join("skills", "deja-history"))
		}
		// Cline has no user-level instructions file at all, so its skill rides
		// inside the plugin deja generates. Read that off the generated
		// manifest rather than trusting the registry.
		if h.ID == "cline" {
			gotSkill = strings.Contains(clinePluginJS("/bin/deja"), `"skills"`)
		}
		// MiniMax Code reads no user-level skill directory either: the skill
		// rides in the plugin package, and the manifest is what names it.
		if h.ID == "mcode" {
			gotSkill = strings.Contains(string(mcodePluginManifest()), "skills/deja-history/SKILL.md")
		}
		if gotSkill != c.Skill {
			t.Fatalf("%s: registry skill=%v, code says %v", h.ID, c.Skill, gotSkill)
		}

		// Command: read it off the artifact install actually generates, not a
		// list kept beside it — a list would only ever agree with itself.
		// Claude Code gets a file in commands/; cline, hermes and pi register
		// theirs inside the plugin deja writes for them.
		var gotCommand bool
		switch h.ID {
		case "claude":
			gotCommand = strings.Contains(claudeCommandMD("/bin/deja"), "deja")
		case "cline":
			gotCommand = strings.Contains(clinePluginJS("/bin/deja"), "registerCommand")
		case "hermes":
			gotCommand = strings.Contains(hermesPluginManifest, "provides_commands")
		case "pi", "senpi", "kimchi":
			// Senpi loads pi's extension unchanged — measured on a live
			// install, where its `/` palette lists the extension's own `deja`
			// command (#3670).
			gotCommand = strings.Contains(piExtensionTS("/bin/deja"), "registerCommand")
		case "amp":
			// Amp's command palette entry is registered by the plugin, the way
			// pi's is.
			gotCommand = strings.Contains(ampPluginTS("/bin/deja"), "registerCommand")
		case "prime":
			gotCommand = strings.Contains(primeExtensionTS("/bin/deja"), "registerCommand")
		case "deepseek":
			// dsh registers commands in code, so deja ships a plugin file the
			// profile row names by path.
			gotCommand = strings.Contains(dshCommandJS("/bin/deja"), "ctx.commands.register")
		case "goose":
			// Goose declares commands in config.yaml, not a commands directory.
			gotCommand = strings.Contains(gooseRecipe("/bin/deja"), "title: deja")
		case "reasonix":
			// The command rides in deja's plugin package, beside the manifest
			// that lists its directory.
			files := reasonixPackageFiles("/bin/deja", false)
			gotCommand = len(files["commands/deja.md"]) > 0 && strings.Contains(string(files["reasonix-plugin.json"]), `"commands"`)
		case "continue":
			// Continue declares its slash commands in the assistant config, as
			// `prompts:`, so the artifact to read is the config deja writes.
			gotCommand = strings.Contains(continueInstalledConfig(t, "/bin/deja"), "- name: deja")
		case "copilot-chat":
			// Copilot Chat has no commands directory; its command is a prompt
			// file, which is what the artifact check reads.
			gotCommand = strings.Contains(copilotChatPrompt("/bin/deja"), "description:")
		default:
			// Where a skill is invocable by name the skill deja installs is
			// the command, and a file beside it would only add another entry —
			// which Gemini says out loud by renaming one of the two. That list
			// lives in the product, beside the report that prints those rows,
			// so the two cannot drift (#3667).
			if skillIsTheCommand(h.ID) {
				gotCommand = gotSkill
				break
			}
			// The rest read a command from a file, so the claim is whether we
			// know where to write one for them.
			gotCommand = commandFilePath(id) != ""
		}
		if gotCommand != c.Command {
			t.Fatalf("%s: registry command=%v, generated install artifact says %v", h.ID, c.Command, gotCommand)
		}

		// Every capability we do not have needs a reason, and the reason has to
		// be one of the four kinds — otherwise the matrix prints a dash that
		// could mean anything and nobody can tell work from a dead end.
		// Fixed order, not a map range: with a map the first of several bad
		// entries to fail is whichever came up, and the rest stay hidden until
		// the next run reports a different one.
		have := map[string]bool{"mcp": c.MCP, "auto": c.Auto, "skill": c.Skill, "command": c.Command,
			"resume": c.Resume}
		for _, capability := range []string{"mcp", "auto", "skill", "command", "resume"} {
			have := have[capability]
			g, ok := h.Gaps[capability]
			if have {
				if ok {
					t.Fatalf("%s: %s is supported but still carries a gap entry", h.ID, capability)
				}
				continue
			}
			if !ok {
				t.Fatalf("%s: %s is false with no gaps entry saying why", h.ID, capability)
			}
			if !gapStates[g.State] {
				t.Fatalf("%s: %s gap state %q is not one of todo/impossible/blocked/unknown", h.ID, capability, g.State)
			}
			why := strings.TrimSpace(g.Why)
			if why == "" {
				t.Fatalf("%s: %s gap has no why", h.ID, capability)
			}
			// A reason has to say something. "No slash command yet." passed the
			// check above on eight entries and told a reader nothing they could
			// act on — not the surface, not what is missing, not what would
			// close it. The floor is a sentence that names the shape of the
			// problem, and a reference so the trail is followable.
			if len(why) < 60 {
				t.Fatalf("%s: %s gap why is %d characters (%q) — say what the surface is and what is missing",
					h.ID, capability, len(why), why)
			}
			// Work has to be followable: a `todo` somebody could pick up, and an
			// `unknown` somebody could go and find out, both need an issue or a
			// link. `impossible` is a fact about the harness and carries its own
			// explanation; `blocked` already needs the source link above.
			if g.State == "todo" || g.State == "unknown" {
				if !strings.Contains(why, "#") && !strings.HasPrefix(g.Source, "http") {
					t.Fatalf("%s: %s is %s and cites neither an issue nor a source: %q",
						h.ID, capability, g.State, why)
				}
			}
			// "Blocked" is a claim about someone else's bug, so it has to point
			// at it — otherwise nobody can tell when it stops being true.
			if g.State == "blocked" && !strings.HasPrefix(g.Source, "http") {
				t.Fatalf("%s: %s is blocked upstream but has no source link", h.ID, capability)
			}
		}

		// Resume: resumeCommand must succeed for a plausible session.
		s := plausibleSession(t, h.ID)
		_, _, rerr := resumeCommand(s)
		if (rerr == nil) != c.Resume {
			t.Fatalf("%s: registry resume=%v, resumeCommand err=%v", h.ID, c.Resume, rerr)
		}

		// Handoff: exec targets come from the command table; paste-only is the rest.
		_, execOK := handoffCommand(h.ID, "P")
		switch c.Handoff {
		case "exec":
			if !execOK {
				t.Fatalf("%s: registry says handoff exec, command table disagrees", h.ID)
			}
			if handoffPasteOnly[h.ID] {
				t.Fatalf("%s: registry says handoff exec, but handoffPasteOnly lists it", h.ID)
			}
		case "paste":
			if execOK {
				t.Fatalf("%s: registry says paste-only, but an exec entry exists", h.ID)
			}
			if !handoffPasteOnly[h.ID] {
				t.Fatalf("%s: registry says paste-only, but handoffPasteOnly does not list it", h.ID)
			}
		default:
			t.Fatalf("%s: unknown handoff kind %q", h.ID, c.Handoff)
		}
	}
	if seen != len(handoffTargets())+len(handoffPasteOnly) {
		t.Fatalf("registry covers %d harnesses, handoff targets %d + %d paste-only", seen, len(handoffTargets()), len(handoffPasteOnly))
	}

	// The published README matrix must contain a row for every harness.
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		if !strings.Contains(string(readme), "| "+h.DisplayName+" |") {
			t.Fatalf("README matrix missing row for %s — run `go run ./scripts/genmatrix`", h.DisplayName)
		}
	}
}

// plausibleSession builds the session the resume invariant is checked against.
// Most harnesses need nothing but an id; openclaw's command comes out of its
// session store, so a bare path would fail the check for the right reason and
// the wrong one at once.
func plausibleSession(t *testing.T, harness string) model.Session {
	t.Helper()
	s := model.Session{ID: "abc123", Harness: harness, Project: "p", Path: "/tmp/x.jsonl"}
	if harness == "grok" {
		// Grok Build sessions are directories of updates.jsonl; the other rows
		// under this harness come from the grok-dev database and cannot resume.
		s.Path = filepath.Join(t.TempDir(), "sessions", "workspace%2Fp", "abc123", "updates.jsonl")
	}
	if harness == "junie" {
		// Junie's ids are session-<yymmdd>-<hhmmss>-<suffix>; nothing else goes
		// on its command line.
		s.ID = "session-260901-101500-k3m9"
	}
	if harness == "zcode" {
		// The CLI database's sessions resume, and carry the directory they
		// ran in as their path; a JSONL transcript does not (#4430).
		s.Path = t.TempDir()
	}
	if harness == "roo" {
		// Only the CLI's own store resumes, and the command carries the
		// workspace out of history_item.json — a bare path would fail the
		// check for the right reason and the wrong one at once.
		root := filepath.Join(t.TempDir(), "vscode-mock", "global-storage")
		id := "01a07bf9-8882-7703-a3fa-245deb8ea752"
		dir := filepath.Join(root, "tasks", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		ws := mustMarshal(t, t.TempDir())
		item := `{"id":"` + id + `","ts":1,"task":"t","workspace":` + string(ws) + `}`
		if err := os.WriteFile(filepath.Join(dir, "history_item.json"), []byte(item), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEJA_ROO_CLI_ROOT", root)
		s.Path = filepath.Join(dir, "api_conversation_history.json")
	}
	if harness == "cursor" {
		// cursor-agent opens a chat from chats/<md5 of its directory>/<id>,
		// so the directory and the chat both have to be there (#4193).
		cli := t.TempDir()
		cwd := filepath.Join(cli, "app")
		chat := filepath.Join(cli, "chats", sources.CursorChatBucket(cwd), s.ID)
		if err := os.MkdirAll(chat, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"meta.json": `{"cwd":` + jsonString(cwd) + `}`, "store.db": ""} {
			if err := os.WriteFile(filepath.Join(chat, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
		t.Setenv("CURSOR_CONFIG_DIR", cli)
		s.Path = filepath.Join(cli, "projects", "app", "agent-transcripts", s.ID, s.ID+".jsonl")
	}
	if harness == "qwen" {
		// qwen finds a session only from the directory it ran in, so that
		// directory has to be there (#4259).
		tmp := t.TempDir()
		cwd := filepath.Join(tmp, "app")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
		s.Path = qwenTranscriptIn(t, filepath.Join(tmp, "qwen"), cwd, s.ID, true)
	}
	if harness == "codebuddy" {
		// Like qwen, `codebuddy -r` finds a session only from the directory
		// it ran in, which the transcript records (#4707).
		tmp := t.TempDir()
		cwd := filepath.Join(tmp, "app")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		s.Path = filepath.Join(tmp, "projects", "app", s.ID+".jsonl")
		if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.Path, []byte(`{"type":"message","role":"user","cwd":`+jsonString(cwd)+"}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if harness == "kilocode" {
		// Only the CLI half of Kilo's store resumes, and the reader tells the
		// two apart by the path: the database is the CLI's, a task file under
		// globalStorage is the extension's.
		s.Path = sources.KiloDB()
	}
	if harness == "crush" {
		// Crush names sessions with a uuid and runs `--session` in the
		// project the store sits under, so both have to be real here.
		s.ID = "942cbc1e-78c7-41cb-aa8a-78c3baab018c"
		s.Path = filepath.Join(t.TempDir(), "app", ".crush", "crush.db")
		if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if harness == "openclaw" {
		dir := filepath.Join(t.TempDir(), "agents", "main", "sessions")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		store := `{"agent:main:main":{"sessionId":"abc123"}}`
		if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(store), 0o644); err != nil {
			t.Fatal(err)
		}
		s.Path = filepath.Join(dir, "abc123.jsonl")
	}
	return s
}

// The capture helpers have to drain the pipe while the callback is still
// writing. A windows anonymous pipe buffers 4096 bytes, and `printSources`
// crossed that at twenty-five harnesses — 4316 bytes — so the sequential
// read-after-call in its test deadlocked the whole cmd/deja package on the
// windows leg of main for twelve minutes before the timeout killed it.
//
// This pins the helper rather than the caller: a payload far past any
// platform's buffer must come back whole. A helper that stops draining
// concurrently fails here — verified by making it sequential, which hangs this
// test until the package timeout rather than returning short, because the write
// it blocks in cannot be abandoned.
func TestCaptureStdoutOutlastsAPipeBuffer(t *testing.T) {
	const lines = 20000
	done := make(chan string, 1)
	go func() {
		done <- captureStdout(t, func() {
			for i := range lines {
				fmt.Printf("line %d of a payload no pipe buffer holds\n", i)
			}
		})
	}()
	select {
	case out := <-done:
		if got := strings.Count(out, "\n"); got != lines {
			t.Fatalf("captured %d lines of %d", got, lines)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("captureStdout did not return: the callback is writing into a pipe nobody is reading")
	}
}

// Four surfaces print this week's recalls — `deja stats`, `--json`, the card and
// the page — and deja had two rules for what a week is until #1921. They agree
// now because each renders the field the report carries rather than asking the
// usage log again. This holds that: hand the renderers a figure and it is the
// figure they show.
func TestTheCardAndThePagePrintTheWeekTheyWereHanded(t *testing.T) {
	r := stats.Report{TotalSessions: 40, TotalMessages: 400, WeekRecalls: 37}

	card := renderStatsCard(r)
	if !strings.Contains(card, ">37</text>") || !strings.Contains(card, "this week") {
		t.Errorf("the card does not show the week it was given")
	}

	page := renderPage(t, r)
	if !strings.Contains(page, "37 times this week") {
		t.Errorf("the page does not show the week it was given")
	}

	// A four-figure week, since formatStatNumber groups thousands: the
	// renderers must still be showing what they were handed, and a test that
	// only ever passes two-digit numbers would not notice a renderer that
	// formatted its own.
	r.WeekRecalls = 1234
	if card = renderStatsCard(r); !strings.Contains(card, ">1,234</text>") {
		t.Errorf("the card does not group thousands the way the report's other figures are grouped")
	}
	if page = renderPage(t, r); !strings.Contains(page, "1,234 times this week") {
		t.Errorf("the page does not group thousands")
	}

	// A different figure in, a different figure out — so neither row above can
	// be a constant that happens to match.
	r.WeekRecalls = 2
	card = renderStatsCard(r)
	if !strings.Contains(card, ">2</text>") {
		t.Errorf("the card ignored the second figure")
	}
	if strings.Contains(card, ">37</text>") {
		t.Errorf("the card still shows the first figure")
	}
	page = renderPage(t, r)
	if !strings.Contains(page, "2 times this week") || strings.Contains(page, "37 times this week") {
		t.Errorf("the page ignored the second figure")
	}
}

func renderPage(t *testing.T, r stats.Report) string {
	t.Helper()
	path, err := writeStatsHTML(filepath.Join(t.TempDir(), "stats.html"), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The plain-text surface, which the test above leaves out because it renders
// through a different path. It also renders the figure differently — %d rather
// than the grouped form the card and the page use — so a four-figure week reads
// "1234" here and "1,234" there. Pinned as it is rather than changed: what a
// count looks like on the screen is not a thing to alter in a test's name.
func TestThePlainTextStatsPrintsTheWeekItWasHanded(t *testing.T) {
	var out strings.Builder
	printStats(&out, stats.Report{TotalSessions: 40, TotalMessages: 400, WeekRecalls: 37})
	if !strings.Contains(out.String(), "This week        37 recalls") {
		t.Errorf("the line does not show the week it was given:\n%s", out.String())
	}

	out.Reset()
	printStats(&out, stats.Report{TotalSessions: 40, TotalMessages: 400, WeekRecalls: 1234})
	if !strings.Contains(out.String(), "1234 recalls") {
		t.Errorf("the four-figure week is not printed as %%d here:\n%s", out.String())
	}
	if strings.Contains(out.String(), "1,234 recalls") {
		t.Errorf("this surface has started grouping thousands; the card and page already do, " +
			"so make them agree deliberately rather than by accident")
	}
}

// A log that holds only this week made the week line a copy of Memory served.
func TestThePlainTextStatsDropsAWeekThatRepeatsTheTotal(t *testing.T) {
	var out strings.Builder
	r := stats.Report{TotalSessions: 40, TotalMessages: 400, WeekRecalls: 3, WeekInjected: 4}
	r.Recall.Recalls, r.Recall.Injections = 3, 4
	printStats(&out, r)
	if strings.Contains(out.String(), "This week") {
		t.Errorf("the week line repeats the total:\n%s", out.String())
	}
}

func TestStatRangeNamesBothYears(t *testing.T) {
	if got := statRange("2025-07-08", "2026-06-29"); got != "Jul 8 2025 → Jun 29 2026" {
		t.Errorf("statRange across years = %q", got)
	}
}

// #4624: the cat has a moment beside an empty search and beside a first recall
// that finds something, in a terminal only. logoWanted's own gating is tested
// in logo_devnull_test.go; TestCatMomentsAtTheSearch below covers the call
// sites, and these pin the rendering they show.
func TestCatMomentShowsOnEmptySearch(t *testing.T) {
	var b bytes.Buffer
	mood, line := searchCatMoment(true) // empty search
	if line != "" {
		t.Fatalf("the empty-search cat says %q, which a filter or the trust policy can make untrue", line)
	}
	printLogoMood(&b, nil, mood)
	out := b.String()
	if !strings.Contains(out, "█") && !strings.Contains(out, "▀") && !strings.Contains(out, "▄") {
		t.Fatalf("empty-search cat rendered no sprite: %q", out)
	}
}

// At the search itself: a terminal gets the cat on an empty search and once
// on the first that finds something; --json never does.
func TestCatMomentsAtTheSearch(t *testing.T) {
	withTempStores(t)
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	user := mustMarshal(t, map[string]any{"type": "user", "sessionId": "a1", "cwd": "/work/pay", "timestamp": "2026-03-01T10:00:00Z",
		"message": map[string]any{"role": "user", "content": "the retry reused the idempotency key"}})
	writeClaudeFixture(t, filepath.Join(root, "pay", "a1.jsonl"), "a1", []string{string(user)})
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	// The build greets with a logo of its own; this is about the search.
	index.LastBuild = index.BuildSummary{}
	saved := logoWanted
	logoWanted = func(*os.File) bool { return true }
	t.Cleanup(func() { logoWanted = saved })
	run := func(args ...string) string {
		return captureStdout(t, func() { _ = searchWithOptions(dir, args, "", false) })
	}
	cat := "▀"
	if out := run("zzqqxx", "--json"); strings.Contains(out, cat) {
		t.Errorf("--json drew the cat:\n%s", out)
	}
	if out := run("zzqqxx", "--since", "1d"); !strings.Contains(out, cat) || strings.Contains(out, "matches that") {
		t.Errorf("empty search under a filter:\n%s", out)
	}
	if out := run("idempotency", "--json"); strings.Contains(out, cat) {
		t.Errorf("--json drew the first-recall cat:\n%s", out)
	}
	if out := run("idempotency"); !strings.Contains(out, "found something") {
		t.Errorf("first recall got no cat:\n%s", out)
	}
	if out := run("idempotency"); strings.Contains(out, "found something") {
		t.Errorf("second recall got the cat again:\n%s", out)
	}
}

func TestCatMomentShowsOnFirstRecall(t *testing.T) {
	var b bytes.Buffer
	mood, line := searchCatMoment(false) // a recall that found something
	printLogoMood(&b, []string{line}, mood)
	out := b.String()
	if !strings.Contains(out, "█") && !strings.Contains(out, "▀") && !strings.Contains(out, "▄") {
		t.Fatalf("first-recall cat rendered no sprite: %q", out)
	}
	if !strings.Contains(out, "found") {
		t.Fatalf("first-recall cat should say something was found: %q", out)
	}
}

// The two moments carry different moods, so the empty search does not look like
// a win. (moodReady vs moodSurprised draw the sprite differently.)
func TestCatMomentsUseDistinctMoods(t *testing.T) {
	emptyMood, _ := searchCatMoment(true)
	recallMood, _ := searchCatMoment(false)
	if emptyMood == recallMood {
		t.Fatalf("empty search and first recall should use different moods, both %v", emptyMood)
	}
}

// The ready cat greets only the first recall that finds something on an index,
// not every result list after it.
func TestFirstRecallIsOncePerIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if !firstRecall(dir) {
		t.Fatal("the first recall on a fresh index should get the cat")
	}
	if firstRecall(dir) {
		t.Fatal("a second recall on the same index should not get the cat again")
	}
	if !firstRecall(filepath.Join(t.TempDir(), "other")) {
		t.Fatal("another index has its own first recall")
	}
}

// recall_context is the "read this session" tool, and for a coding session
// the part worth reading is the code it left. The digest reads from the top,
// where the task and the code reading are, so the fix of a session whose
// conversation outgrows the budget never reached the agent: on 48
// SWE-ContextBench tasks the digest of the past session that fixed the same
// code carried none of it. The code now comes ahead of the conversation, as
// the session left it, without the agent's scratch script or a draft it
// rewrote.
func TestRecallContextShowsTheCodeTheSessionLeft(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "cccc0001-1111-4000-8000-d6e7f8a9b0c1"
	var b strings.Builder
	n := 0
	line := func(typ string, content any) {
		n++
		raw := mustMarshal(t, map[string]any{
			"type": typ, "sessionId": id, "cwd": "/proj",
			"timestamp": fmt.Sprintf("2026-07-01T10:%02d:%02dZ", n/60, n%60),
			"message":   map[string]any{"role": typ, "content": content},
		})
		b.Write(raw)
		b.WriteByte('\n')
	}
	tool := func(name string, input map[string]string) []any {
		return []any{map[string]any{"type": "tool_use", "id": fmt.Sprintf("t%d", n), "name": name, "input": input}}
	}
	line("user", "squashed migrations still carry index_together")
	for i := range 30 {
		line("assistant", []any{map[string]any{"type": "text", "text": fmt.Sprintf("reading the optimizer, step %d: %s", i, strings.Repeat("the operations reduce in pairs ", 12))}})
	}
	line("assistant", tool("Write", map[string]string{"file_path": "/proj/reproduce_bug.py", "content": "print('SCRATCHPAD')"}))
	line("assistant", tool("Edit", map[string]string{"file_path": "/proj/django/ops.py", "old_string": "def reduce(self):\n    pass", "new_string": "def reduce(self):\n    return DRAFT"}))
	line("assistant", tool("Edit", map[string]string{"file_path": "/proj/django/ops.py", "old_string": "return DRAFT", "new_string": "return [AddIndex(self.name)]"}))
	line("assistant", tool("Edit", map[string]string{"file_path": "/proj/tests/test_ops.py", "old_string": "def test_a(): pass", "new_string": "def test_a(): assert reduce()"}))
	line("assistant", []any{map[string]any{"type": "text", "text": "Fixed: reduce turns the rename into an AddIndex."}})
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	raw, n1, _, _, err := recallContextResult(dir, id, "")
	if err != nil || n1 != 1 {
		t.Fatalf("n=%d err=%v", n1, err)
	}
	// What the MCP tool sends: cut to the reply's budget.
	text := fitContextDigest(raw, id, id, contextMCPBudget-recallFrameOverhead)
	at := strings.Index(text, "## what it changed")
	if at < 0 {
		t.Fatalf("no changes block:\n%s", text)
	}
	if conv := strings.Index(text, "reading the optimizer"); conv >= 0 && conv < at {
		t.Fatalf("the code comes after the conversation, where the cut takes it:\n%s", text)
	}
	lib, test := strings.Index(text, "+     return [AddIndex(self.name)]"), strings.Index(text, "assert reduce()")
	if lib < 0 || test < 0 || lib > test {
		t.Fatalf("the library change and then its test must show:\n%s", text)
	}
	for _, bad := range []string{"SCRATCHPAD", "DRAFT"} {
		if strings.Contains(text, bad) {
			t.Fatalf("shows %q:\n%s", bad, text)
		}
	}
	if !strings.Contains(text, "django/ops.py:") || strings.Contains(text, "/proj/django") {
		t.Fatalf("paths should read relative to the project:\n%s", text)
	}
}

// A session that changed nothing reads as it did.
func TestRecallContextWithoutEditsHasNoChangesBlock(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "cccc0002-1111-4000-8000-d6e7f8a9b0c1"
	line := `{"type":"user","message":{"role":"user","content":"why is the pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, _, _, _, err := recallContextResult(dir, id, "")
	if err != nil || strings.Contains(text, "what it changed") {
		t.Fatalf("err=%v\n%s", err, text)
	}
}

func TestChangeDiffKeepsOnlyWhatChanged(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf"
	got := changeDiff(old, "a\nb\nc\nX\ne\nf")
	want := "  b\n  c\n- d\n+ X\n  e\n  f"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// A Codex session's edits are apply_patch calls; reading the session back
// shows them the same way (#4950).
func TestRecallContextShowsACodexSessionsPatch(t *testing.T) {
	tmp := hermeticEnv(t)
	day := filepath.Join(tmp, "codex", "sessions", "2026", "07", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "019a0001-1111-7000-8000-d6e7f8a9b0c1"
	rec := func(at, typ string, payload any) string {
		b := mustMarshal(t, map[string]any{"timestamp": at, "type": typ, "payload": payload})
		return string(b) + "\n"
	}
	patch := "*** Begin Patch\n*** Update File: /proj/django/ops.py\n@@\n def reduce(self):\n-    pass\n+    return [AddIndex(self.name)]\n*** End Patch\n"
	body := rec("2026-07-01T10:00:00Z", "session_meta", map[string]any{"id": id, "cwd": "/proj"}) +
		rec("2026-07-01T10:00:01Z", "response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "squashed migrations still carry index_together"}}}) +
		rec("2026-07-01T10:00:02Z", "response_item", map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": patch}) +
		rec("2026-07-01T10:00:03Z", "response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Fixed: reduce turns the rename into an AddIndex."}}})
	if err := os.WriteFile(filepath.Join(day, "rollout-2026-07-01T10-00-00-"+id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, n, _, _, err := recallContextResult(dir, id, "")
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !strings.Contains(text, "## what it changed") || !strings.Contains(text, "django/ops.py:") || !strings.Contains(text, "+     return [AddIndex(self.name)]") {
		t.Fatalf("the patch is not in the session read back:\n%s", text)
	}
}

func TestRecallIncludesCompactCodeDiffForMatchedSymbol(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "cccc0003-1111-4000-8000-d6e7f8a9b0c1"
	user := `{"type":"user","message":{"role":"user","content":"fix MaxIdleConns retry in pool.go"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	edit := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/proj/internal/db/pool.go","old_string":"MaxIdleConns: 2,\n","new_string":"MaxIdleConns: 16,\n"}},{"type":"text","text":"We decided to raise the idle connection floor in pool.go so bursts reuse sockets."}]},"timestamp":"2026-07-01T10:01:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(user+edit), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, _, _, _, err := recallTextResult(dir, "MaxIdleConns pool", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "code it changed (internal/db/pool.go):") || !strings.Contains(text, "+ MaxIdleConns: 16,") {
		t.Fatalf("expected compact code diff in recall output, got:\n%s", text)
	}
}

func TestRecallDiversifiesUncoveredQueryTerms(t *testing.T) {
	s1 := model.Session{
		ID:      "s1",
		Harness: "claude",
		Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "check pgbouncer pooling configuration"},
		},
	}
	s2 := model.Session{
		ID:      "s2",
		Harness: "claude",
		Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "another pgbouncer pooling tune"},
		},
	}
	s3 := model.Session{
		ID:      "s3",
		Harness: "claude",
		Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "quasar failover regression check"},
		},
	}
	hits := []search.Hit{
		{Session: s1, Tier: search.TierRelevance},
		{Session: s2, Tier: search.TierRelevance},
		{Session: s3, Tier: search.TierRelevance},
	}
	idf := map[string]float64{"pgbouncer": 2.0, "quasar": 5.0}
	out := diversifyUncoveredTerms(hits, "pgbouncer quasar", idf, "")
	if len(out) != 3 || out[0].Session.ID != "s1" || out[1].Session.ID != "s3" {
		t.Fatalf("expected s3 covering uncovered term quasar promoted to slot 2, got [%s, %s, %s]",
			out[0].Session.ID, out[1].Session.ID, out[2].Session.ID)
	}

	// When added names slot 1, promote into slot 2 instead.
	s4 := model.Session{
		ID:      "s4",
		Harness: "claude",
		Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "another pgbouncer note"},
		},
	}
	hitsWithAdded := []search.Hit{
		{Session: s1, Tier: search.TierRelevance},
		{Session: s2, Tier: search.TierRelevance},
		{Session: s4, Tier: search.TierRelevance},
		{Session: s3, Tier: search.TierRelevance},
	}
	outAdded := diversifyUncoveredTerms(hitsWithAdded, "pgbouncer quasar", idf, "claude:s2")
	if len(outAdded) != 4 || outAdded[1].Session.ID != "s2" || outAdded[2].Session.ID != "s3" {
		t.Fatalf("expected s3 promoted to slot 3 when slot 2 is machine-best, got [%s, %s, %s, %s]",
			outAdded[0].Session.ID, outAdded[1].Session.ID, outAdded[2].Session.ID, outAdded[3].Session.ID)
	}

	// RoleEdit/RoleWrote fallback in sessionChanges & recallCodeChangedLines.
	inMem := model.Session{
		ID:      "in-mem",
		Harness: "aider",
		Messages: []model.Message{
			{Role: "edit", Text: "internal/db/pool.go\nMaxIdleConns: 2"},
			{Role: "wrote", Text: "internal/db/pool.go\n+ MaxIdleConns: 16"},
		},
	}
	diffLines := recallCodeChangedLines(inMem, "MaxIdleConns")
	if len(diffLines) == 0 || !strings.Contains(strings.Join(diffLines, ""), "+ MaxIdleConns: 16") {
		t.Fatalf("expected RoleEdit/RoleWrote fallback diff, got %v", diffLines)
	}
}

// 485 gave `check -` a sentence for each way it comes back empty and left one
// case it could not tell apart: an index this build will not read is not a
// missing index, and saying so tells someone who has used deja for months that
// their history was never indexed. doctor has had the right words all along
// (#2680).
func TestCheckTellsAnOlderIndexFromNoIndex(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ready   bool
		older   bool
		damaged bool
		want    string
	}{
		{name: "no index at all", want: "no index to check against yet"},
		{name: "an index an older build wrote", older: true, want: "written by an older deja"},
		// The third condition planIndexReady folds in. doctor says "integrity
		// damaged" on the same store while search answers from the snapshot
		// and rebuilds behind it (#2682).
		{name: "a damaged index", damaged: true, want: "the index is damaged"},
		// It reports walls, not decisions, and says so: a plan reversing a
		// recorded decision came back with a bare "nothing found".
		{name: "a healthy index with nothing to say", ready: true, want: "nothing found for this plan — it checks repeated errors, not decisions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			restoreReady := planIndexReady
			restoreOlder := indexFormatDirection
			restoreDamaged := indexDamaged
			t.Cleanup(func() {
				planIndexReady = restoreReady
				indexFormatDirection = restoreOlder
				indexDamaged = restoreDamaged
			})
			planIndexReady = func(string) bool { return tc.ready }
			indexDamaged = func(string) bool { return tc.damaged }
			indexFormatDirection = func(string) int {
				if tc.older {
					return -1
				}
				return 0
			}
			var out, errOut strings.Builder
			if err := runCheckTo("", []string{"-"}, strings.NewReader("rewrite the widget pipeline"), &out, &errOut); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("want %q, got:\n%s", tc.want, errOut.String())
			}
			if out.String() != "" {
				t.Fatalf("findings keep stdout to themselves (#2566), got:\n%s", out.String())
			}
		})
	}
}

// `deja check -` is the form a person types; the hook it shares its body with
// is the one that must stay silent. Today both are: an empty plan, a plan with
// nothing to search on, a store with no index, and a plan deja looked up and
// found nothing for all print nothing and exit 0, so the reader cannot tell
// which of the four happened (#2564).
func TestCheckSaysWhyItFoundNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")

	say := func(plan string) string {
		t.Helper()
		var out, errs bytes.Buffer
		if err := runCheckTo(dir, []string{"-"}, strings.NewReader(plan), &out, &errs); err != nil {
			t.Fatalf("check: %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("stdout is for findings only, got %q", out.String())
		}
		return errs.String()
	}

	// No index at all: deja never looked, and saying nothing claims it did.
	if got := say("Plan: change the pgbouncer pool size."); !strings.Contains(got, "index") {
		t.Errorf("with no index, check said %q", strings.TrimSpace(got))
	}

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// A plan with nothing in it to search on.
	if got := say("   \n  "); !strings.Contains(got, "plan") {
		t.Errorf("on an empty plan, check said %q", strings.TrimSpace(got))
	}
	// And a plan it really did look up.
	got := say("Plan: change the pgbouncer pool size and the retry budget.")
	if !strings.Contains(got, "nothing") {
		t.Errorf("after looking and finding nothing, check said %q", strings.TrimSpace(got))
	}
}

func TestRunCheckUsesTheHookPlanCore(t *testing.T) {
	withPlanLookup(t, samplePlanMatches(`C:\work\repo`))
	dir := filepath.Join(t.TempDir(), "index.db")
	plan := "1. Run timeout around deploy_probe"

	var hookOut bytes.Buffer
	hookInput := `{
		"hook_event_name":"PreToolUse",
		"tool_name":"ExitPlanMode",
		"tool_input":{"plan":"1. Run timeout around deploy_probe"},
		"session_id":""
	}`
	if err := runHookPlan(dir, strings.NewReader(hookInput), &hookOut); err != nil {
		t.Fatal(err)
	}

	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(hookOut.Bytes(), &resp); err != nil {
		t.Fatalf("hook JSON = %q: %v", hookOut.String(), err)
	}
	hookPlain := strings.TrimPrefix(resp.HookSpecificOutput.AdditionalContext, recallFrameHeader)
	hookPlain = strings.TrimSuffix(hookPlain, recallFrameFooter)

	var checkOut bytes.Buffer
	if err := runCheck(dir, []string{"-"}, strings.NewReader(plan), &checkOut); err != nil {
		t.Fatal(err)
	}
	checkPlain := strings.TrimSuffix(checkOut.String(), "\n")
	if checkPlain != hookPlain {
		t.Fatalf("check and hook differ:\ncheck: %q\nhook:  %q", checkPlain, hookPlain)
	}
	if strings.Contains(checkOut.String(), "<deja-recall>") {
		t.Fatalf("check output contains a recall frame: %q", checkOut.String())
	}
	if strings.Count(strings.TrimSpace(checkOut.String()), "\n") != 0 {
		t.Fatalf("one finding was not printed as one line: %q", checkOut.String())
	}
}

func TestRunCheckSilentMiss(t *testing.T) {
	withPlanLookup(t, nil)
	var out bytes.Buffer
	if err := runCheck(
		filepath.Join(t.TempDir(), "index.db"),
		[]string{"-"},
		strings.NewReader("1. Run timeout around deploy_probe"),
		&out,
	); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("miss produced %q", out.String())
	}
}

func TestRunCheckRequiresDash(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"PLAN.md"},
		{"PLAN.md", "extra"},
	} {
		var out bytes.Buffer
		err := runCheck(
			filepath.Join(t.TempDir(), "index.db"),
			args,
			strings.NewReader("1. Run timeout around deploy_probe"),
			&out,
		)
		if err == nil {
			t.Fatalf("args %v returned no usage error", args)
		}
		if !strings.Contains(err.Error(), "check needs '-'") {
			t.Fatalf("args %v error = %q", args, err)
		}
		if out.Len() != 0 {
			t.Fatalf("args %v produced output %q", args, out.String())
		}
	}
}

// Given the '-' it asks for plus one more argument, check used to say the '-'
// was missing (#5077). It names the extra argument instead.
func TestRunCheckNamesAnExtraArgument(t *testing.T) {
	var out bytes.Buffer
	err := runCheck(
		filepath.Join(t.TempDir(), "index.db"),
		[]string{"-", "extra"},
		strings.NewReader("1. Run timeout around deploy_probe"),
		&out,
	)
	if err == nil {
		t.Fatal("check - extra returned no usage error")
	}
	if want := `check takes only '-', got an extra "extra"`; err.Error() != want {
		t.Fatalf("check - extra error = %q, want %q", err, want)
	}
	if out.Len() != 0 {
		t.Fatalf("check - extra produced output %q", out.String())
	}
}

// Chinese numerals for 10-99, which is the whole range a harness count will
// live in.
func chineseTens(n int) string {
	digits := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九"}
	if n < 10 || n > 99 {
		return ""
	}
	tens, unit := n/10, n%10
	out := "十"
	if tens > 1 {
		out = digits[tens] + "十"
	}
	if unit > 0 {
		out += digits[unit]
	}
	return out
}

// The English pages have two tests counting the registry for them; the Chinese
// ones had none, and a third of this repository's readers arrive on them. A
// count that is three harnesses stale is the same defect in either language,
// and harder to notice in the one you do not read.
//
// The rule is narrow on purpose: a count near the current one is the drift
// (thirty-four written when the registry holds thirty-five), while a different
// tens is some other number the page is entitled to say — forty sessions on
// one topic, eleven tools in the comparison.
func TestTheChinesePagesCountTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	current := chineseTens(n) + "个"
	if current == "个" {
		t.Fatalf("no Chinese numeral for %d harnesses", n)
	}

	var stale []string
	for d := -3; d <= 3; d++ {
		if d == 0 {
			continue
		}
		if s := chineseTens(n + d); s != "" {
			stale = append(stale, s+"个")
		}
	}

	pages := []string{filepath.Join(root, "docs", "readme", "README.zh.md")}
	err := filepath.WalkDir(filepath.Join(root, "docs", "zh"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".html") {
			pages = append(pages, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 10 {
		t.Fatalf("only %d Chinese pages found — this test has lost its subject", len(pages))
	}

	saysTheCount := 0
	for _, page := range pages {
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		rel, _ := filepath.Rel(root, page)
		rel = filepath.ToSlash(rel)
		if strings.Contains(text, current) {
			saysTheCount++
		}
		for _, s := range stale {
			if strings.Contains(text, s) {
				t.Errorf("%s says %q; the registry has %d, so it is %q", rel, s, n, current)
			}
		}
	}
	if saysTheCount == 0 {
		t.Errorf("no Chinese page states the harness count (%q) — the phrasing changed and this test stopped seeing it", current)
	}
}

// The sentence the agent is told to say aloud has to name the thing the user
// can see in the digest. Seen on a real kimi screen: the digest quoted a
// session about token rotation while the citation under it named "migration
// locked the table" — the first user line of the matched window, which after
// focusing a long session is whatever chatter began it.
func TestOpenerNamesWhatWasMatched(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "work-02", Project: "goprojects/deja-vu",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "migration locked the table"},
			{Role: "assistant", Text: "it rewrote the whole table"},
			{Role: "user", Text: "how often does the deploy token for the billing gateway rotate?"},
			{Role: "assistant", Text: "every 47 days, pinned to the vault lease"},
		},
	}

	got := openerLine(s, []string{"deploy", "token", "rotate"})
	if !strings.Contains(got, "deploy token") {
		t.Fatalf("citation names something the digest never showed:\n%s", got)
	}
	if strings.Contains(got, "migration locked") {
		t.Fatalf("citation still takes the opening line:\n%s", got)
	}

	// Without terms — the session-start path — the opening line is still the
	// best summary there is.
	plain := openerLine(s, nil)
	if !strings.Contains(plain, "migration locked") {
		t.Fatalf("the no-terms fallback changed:\n%s", plain)
	}
}

// repeatLead sits above the untrusted line of the agent-facing block, and it
// quotes the earlier question straight from a session — display-unsafe. A
// hostile question carrying an escape sequence, a bidi override or an
// invisible tag-block character must not ride into the agent's context intact.
func TestRepeatLeadStripsDisplayControls(t *testing.T) {
	s := model.Session{
		Harness: "claude",
		ID:      "8f2c19ab77d40e6b5c31",
		Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	got := repeatLead(s, "deploy \u202eplan\u200b now\x1b[31m red\ttab\nline")
	// The lead ends with a newline of its own; every other rune must be
	// plain text.
	body := strings.TrimSuffix(got, "\n")
	for _, r := range body {
		if unicode.IsControl(r) {
			t.Fatalf("lead carried a control rune %U: %q", r, got)
		}
	}
	for _, bad := range []rune{'\u202e', '\u200b'} {
		if strings.ContainsRune(got, bad) {
			t.Fatalf("lead carried %U: %q", bad, got)
		}
	}
	if !strings.Contains(got, "deploy") || !strings.Contains(got, "plan") {
		t.Fatalf("lead lost its readable words: %q", got)
	}
	opener := repeatOpener(s, "deploy \u202eplan\u200b now\x1b[31m red\ttab\nline \"quoted\"")
	for _, r := range strings.TrimPrefix(opener, "\n") {
		if unicode.IsControl(r) {
			t.Fatalf("opener carried a control rune %U: %q", r, opener)
		}
	}
	// Citation dates are deliberately local (the surrounding user sees local
	// dates too). This safety test must assert provenance, not a UTC calendar
	// day that shifts for readers west of Greenwich.
	if !strings.Contains(opener, "deja:8f2c19ab77d") || !strings.Contains(opener, "(claude") || !strings.Contains(opener, "'quoted'") {
		t.Fatalf("opener lost its provenance or kept a quote that closes the line: %q", opener)
	}
}

// The opener quotes the matched line straight from a session, above the
// untrusted line, so it gets the same treatment as the repeat lead — and a
// quote inside the title must not close the quoted line early.
func TestOpenerLineStripsDisplayControls(t *testing.T) {
	s := model.Session{
		Harness: "claude",
		ID:      "8f2c19ab77d40e6b5c31",
		Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Title:   "deploy \u202eplan\u200b now\x1b[31m red\ttab\nline \"quoted\"",
	}
	got := openerLine(s, nil)
	// The line opens with a newline of its own; every other rune must be
	// plain text.
	for _, r := range strings.TrimPrefix(got, "\n") {
		if unicode.IsControl(r) {
			t.Fatalf("opener carried a control rune %U: %q", r, got)
		}
	}
	for _, bad := range []rune{'\u202e', '\u200b'} {
		if strings.ContainsRune(got, bad) {
			t.Fatalf("opener carried %U: %q", bad, got)
		}
	}
	if !strings.Contains(got, "deploy") || !strings.Contains(got, "'quoted'") {
		t.Fatalf("opener lost its readable words or kept a quote that closes the line: %q", got)
	}
}

// Claude Code 2.1.287 fires PostToolUseFailure for a Bash call that exits
// non-zero, with the output under `error`, and rejects a reply whose
// hookEventName is not the event it sent: "Hook returned incorrect event name:
// expected 'PostToolUseFailure' but got 'PostToolUse'" (#4488).
func TestToolAfterAnswersTheFailureEventItWasSent(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	// The shape captured from a live claude -p run, error text swapped in.
	payload := mustMarshal(t, map[string]any{
		"session_id":      "agent-fail",
		"cwd":             "/work/app",
		"hook_event_name": "PostToolUseFailure",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "make", "description": "build"},
		"tool_use_id":     "toolu_1",
		"error":           "Exit code 2\ngoroutine 1 [running]:\npanic: sql: database is closed",
		"is_interrupt":    false,
	})
	var strike1, out bytes.Buffer
	if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &strike1); err != nil {
		t.Fatal(err)
	}
	if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("undecodable hook response %q: %v", out.String(), err)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "make clean && make CGO_ENABLED=0") {
		t.Fatalf("the failure payload got no fix pair:\n%s", out.String())
	}
	if got := resp.HookSpecificOutput.HookEventName; got != "PostToolUseFailure" {
		t.Errorf("hookEventName = %q, Claude Code drops a reply that does not name PostToolUseFailure", got)
	}
}

func claudeHookCommands(t *testing.T, event string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(sources.ClaudeConfigDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("settings.json is not JSON: %v\n%s", err, b)
	}
	var got []string
	for _, e := range cfg.Hooks[event] {
		for _, h := range e.Hooks {
			got = append(got, e.Matcher+" "+h.Command)
		}
	}
	return got
}

// A failing Bash never fires PostToolUse in Claude Code, so the fix pair has to
// sit on PostToolUseFailure as well. PostToolUse stays: `go test ./... | tail`
// exits 0 and still prints the error.
func TestClaudeAutoWiresTheFailureEvent(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"model": "opus", "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "/tmp/notify.sh"}]}]}}` + "\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHook("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	fail := claudeHookCommands(t, "PostToolUseFailure")
	if len(fail) != 1 || !strings.HasPrefix(fail[0], "Bash|PowerShell ") || !strings.HasSuffix(fail[0], " hook-tool-after") {
		t.Fatalf("PostToolUseFailure = %q, want one Bash|PowerShell entry running hook-tool-after", fail)
	}
	if post := claudeHookCommands(t, "PostToolUse"); len(post) != 1 {
		t.Fatalf("PostToolUse = %q, want it kept", post)
	}
	if _, err := installClaudeHook("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("install then uninstall changed the file:\n%s\n%s", before, after)
	}
}

// Settings an older deja wrote, every event but the failure one, are out of
// date: doctor says so, and the post-upgrade refresh adds the missing entry.
func TestClaudeWiringWithoutTheFailureEventIsRefreshed(t *testing.T) {
	withStatsStores(t)
	old := []string{"SessionStart", "PreCompact", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SessionEnd"}
	writeClaudeSettings(t, old...)
	var out bytes.Buffer
	doctorHooks(&out)
	if !strings.Contains(out.String(), "PostToolUseFailure") {
		t.Errorf("doctor does not name the missing failure event:\n%s", out.String())
	}

	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), []byte(`{"version":"0.14.0","targets":["claude-auto"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshWiringAfterUpgrade()
	if got := claudeHookCommands(t, "PostToolUseFailure"); len(got) != 1 || !strings.HasSuffix(got[0], " hook-tool-after") {
		t.Fatalf("after the upgrade refresh PostToolUseFailure = %q", got)
	}
}

// Claude Code before 2.0.56 has no PostToolUseFailure, and its settings schema
// is a closed list of events: one key it does not know fails the whole of
// ~/.claude/settings.json — "Found invalid settings files … They will be
// ignored" — the user's permissions and model with it. So install leaves the
// event out for a claude that old, takes out one it finds, and doctor does not
// ask for it.
func TestClaudeAutoLeavesTheFailureEventOutForAnOldClaude(t *testing.T) {
	withStatsStores(t)
	t.Cleanup(func() { claudeVersion = claudeVersionReal })
	claudeVersion = func() ([3]int, bool) { return [3]int{2, 0, 55}, true }
	writeClaudeSettings(t, "SessionStart", "PreCompact", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "SessionEnd")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHook(exe, false); err != nil {
		t.Fatal(err)
	}
	if got := claudeHookCommands(t, "PostToolUseFailure"); len(got) != 0 {
		t.Errorf("Claude Code 2.0.55 got PostToolUseFailure = %q; it would ignore the whole settings.json", got)
	}
	if got := claudeHookCommands(t, "PostToolUse"); len(got) != 1 {
		t.Errorf("PostToolUse = %q, want it kept", got)
	}
	var out bytes.Buffer
	doctorHooks(&out)
	if strings.Contains(out.String(), "PostToolUseFailure") || !strings.Contains(out.String(), "wired") {
		t.Errorf("doctor asks an old Claude Code for an event it cannot load:\n%s", out.String())
	}

	claudeVersion = func() ([3]int, bool) { return [3]int{2, 0, 56}, true }
	if _, err := installClaudeHook(exe, false); err != nil {
		t.Fatal(err)
	}
	if got := claudeHookCommands(t, "PostToolUseFailure"); len(got) != 1 {
		t.Errorf("Claude Code 2.0.56 got PostToolUseFailure = %q, want it wired", got)
	}
}

// The plugin manifest goes through the same closed list: on Claude Code 2.0.55
// `claude plugin validate` fails it with "hooks: Invalid input" and the plugin
// does not load. hooks/hooks.json is read on its own, and a file that fails
// there is skipped with the manifest's hooks still loaded, so an event newer
// than that lives in hooks.json.
func TestPluginManifestHooksOnlyEventsOldClaudeKnows(t *testing.T) {
	// The event list of Claude Code 2.0.55's settings schema.
	known := map[string]bool{"PreToolUse": true, "PostToolUse": true, "Notification": true,
		"UserPromptSubmit": true, "SessionStart": true, "SessionEnd": true, "Stop": true,
		"SubagentStart": true, "SubagentStop": true, "PreCompact": true, "PermissionRequest": true}
	for event := range hookMatchers(t, repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), "deja.sh") {
		if !known[event] {
			t.Errorf("plugin.json hooks %s, which Claude Code 2.0.55 rejects along with the whole plugin; put it in hooks/hooks.json", event)
		}
	}
	extra := hookMatchers(t, repoFile(t, "claude-plugin/hooks/hooks.json"), "deja.sh")
	if extra["PostToolUseFailure"] != "Bash|PowerShell" {
		t.Errorf("hooks/hooks.json = %v, want PostToolUseFailure on Bash|PowerShell", extra)
	}
}

// Claude Code 2.1.300 and later keep the context a SessionStart hook added as
// an attachment in the transcript and replay it on resume, so a resumed session
// got the start digest a second time (#5122). A transcript that does not hold
// one, or holds one only before a compaction, still gets a digest.
func TestClaudeResumeGetsNoSecondDigest(t *testing.T) {
	dir, fork := forkStore(t, false)
	transcript := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", fork+".jsonl")
	run := func(src string) string {
		payload, err := json.Marshal(map[string]string{"session_id": fork, "source": src, "transcript_path": transcript, "cwd": "/w/p"})
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(payload))
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	appendLine := func(v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	digest := run("startup")
	if !strings.Contains(digest, "deja-recall") {
		t.Fatalf("the claude session got no digest at startup, so this proves nothing:\n%s", digest)
	}
	if out := run("resume"); !strings.Contains(out, "deja-recall") {
		t.Fatalf("a resumed transcript that holds no digest got none:\n%s", out)
	}
	// What Claude Code 2.1.302 writes for the startup hook's answer.
	appendLine(map[string]any{
		"type": "attachment", "sessionId": fork, "uuid": "att-1",
		"attachment": map[string]any{
			"type": "hook_additional_context", "hookName": "SessionStart", "hookEvent": "SessionStart",
			"toolUseID": "SessionStart", "content": []string{strings.TrimSpace(digest)},
		},
	})
	if out := run("resume"); strings.TrimSpace(out) != "" {
		t.Errorf("a resumed Claude Code session got its start digest again:\n%s", out)
	}
	// A compaction drops what came before it from the context.
	appendLine(map[string]any{"type": "system", "subtype": "compact_boundary", "sessionId": fork})
	if out := run("resume"); !strings.Contains(out, "deja-recall") {
		t.Errorf("a resumed session whose digest was compacted away got none:\n%s", out)
	}
}

// A message longer than maxIndexedText is stored clipped. The session stays
// searchable, so a query over the missing tail answers "no matches" — which
// reads as "you never said that". The manifest already promises the opposite:
// silent loss must be diagnosable (#1093).
func TestClippedMessageIsCounted(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	big := strings.Repeat("lorem ipsum dolor sit amet ", 4000) // ~108 KB, past the 64 KB cap
	lines := []string{
		`{"type":"user","sessionId":"s-huge","cwd":"/tmp/big","timestamp":"2026-06-01T09:00:00Z","message":{"role":"user","content":"gudgeonpin opening line"}}`,
		fmt.Sprintf(`{"type":"user","sessionId":"s-huge","cwd":"/tmp/big","timestamp":"2026-06-01T09:01:00Z","message":{"role":"user","content":"%s tailneedle"}}`, big),
	}
	writeClaudeFixture(t, filepath.Join(root, "projects", "-tmp-big", "s-huge.jsonl"), "s-huge", lines)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: the tail really is gone, so the count is the only way to know.
	out, err := captureRun(t, "search", "--json", "tailneedle")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Total int `json:"total"`
	}
	if err = json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if res.Total != 0 {
		t.Fatalf("the fixture did not clip anything; total = %d", res.Total)
	}

	health := index.IngestHealth(dir)
	if got := health["claude"].ClippedMessages; got != 1 {
		t.Errorf("clipped_messages = %d, want 1 — the clip left no trace", got)
	}

	doctor, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doctor, "stored short of the transcript") {
		t.Errorf("doctor does not mention the clipped message:\n%s", doctor)
	}
}

// A dry run where every file was skipped — the transcript still being written
// — ended "0 values in 0 files — `deja secrets --scrub` to rewrite them",
// sending the reader to rewrite nothing.
func TestScrubDryRunWithNothingToRewriteSaysSo(t *testing.T) {
	var out bytes.Buffer
	printScrub(&out, []scrubOutcome{{
		target:  scrubTarget{path: "/tmp/x/s.jsonl", found: 1},
		skipped: "still being written",
	}}, nil, true)
	got := out.String()
	if strings.Contains(got, "to rewrite them") || !strings.Contains(got, "nothing would be rewritten") {
		t.Fatalf("dry run with nothing to rewrite said:\n%s", got)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func installClaude(exe string, uninstall bool) (installResult, error) {
	path := sources.ClaudeJSONPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	var note string
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if configIsJSONC(old) {
		res, werr := writeJSONCEntry(path, old, "mcpServers", mcpServerEntry(exe), uninstall)
		if !entrySwitchHonored("claude-code") {
			res.Note = dropNotePart(res.Note, switchedOffNote)
		}
		return res, werr
	} else if uerr := json.Unmarshal(old, &root); uerr != nil {
		return installResult{}, configParseError(path, uerr)
	}
	m, _, err := mcpBlock(root, "mcpServers", path)
	if err != nil {
		// On the way out there is nothing of deja's in a block it never wrote,
		// and refusing here would leave the rest of the target wired (#2399).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if m == nil {
		// Adding the empty block on the way out rewrites a config that never
		// mentioned deja, and leaves a .bak of it besides (#676).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		m = map[string]any{}
		root["mcpServers"] = m
		noteBlockAdded(path, "mcpServers")
	}
	if uninstall {
		delete(m, "deja")
		removeAdoptedDejaEntries(path, "mcpServers", m)
		note = leftDejaEntriesNote(m)
		// And the block itself, when deja is what put it there: a config the
		// reader owned came back with an empty `mcpServers` they never wrote,
		// while the copy install promised sat beside it holding the real thing
		// (#2604). A block they wrote stays, empty or not.
		if len(m) == 0 && blockWasAdded(path, "mcpServers") {
			delete(root, "mcpServers")
			forgetBlockAdded(path, "mcpServers")
		}
	} else {
		key := dejaEntryKey(m)
		if key != "deja" {
			noteBlockAdded(path, "mcpServers."+key)
		}
		m[key], note = mergeDejaEntry(m[key], mcpServerEntry(exe))
		if !entrySwitchHonored("claude-code") {
			note = dropNotePart(note, switchedOffNote)
		}
		note = withOtherDejaEntries(note, m, key)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

// claudeHookWiring is every event deja installs into Claude Code, in one place
// because two commands read it: install writes them, and doctor says which of
// them the machine actually has. They used to be a list of calls here and a
// single check over there, so a settings.json written by an older deja — three
// events where there are now five — was reported as "wired" and the features
// added since reached nobody until they happened to reinstall.
var claudeHookWiring = []struct{ Event, Sub, Matcher string }{
	{"SessionStart", "hook-context", ""},
	{"PreCompact", "hook-precompact", "manual|auto"},
	{"UserPromptSubmit", "hook-prompt", ""},
	// Recall at the moment of the action: before an edit or a command,
	// hook-tool names the file's or command's prior decision. Scoped to the
	// tools that change something so it never fires on a Read or a Glob.
	//
	// Task and Agent are the same event under two names: the parent spawning a
	// subagent. That agent gets no session start and sends no user prompt, so
	// its instructions are the only place memory can reach it, and hook-tool
	// answers this one by rewriting them rather than by speaking to the parent.
	{"PreToolUse", "hook-tool", "Bash|PowerShell|Edit|Write|MultiEdit|NotebookEdit|Task|Agent"},
	// The other half of the point of action: the pre-tool line speaks before a
	// command runs, this one speaks when it failed and the store knows what
	// followed that error before. The shells only — a failed edit does not
	// carry a shell error signature. PowerShell is the shell on Windows (#4489).
	{"PostToolUse", "hook-tool-after", "Bash|PowerShell"},
	// A command that exited non-zero fires this instead, never PostToolUse —
	// measured on Claude Code 2.1.287, output under `error` (#4488).
	// PostToolUse stays for the failure that exits 0: `go test ./... | tail`.
	{"PostToolUseFailure", "hook-tool-after", "Bash|PowerShell"},
	// The session is over, so its live stamp goes and the next session's MCP
	// recall can answer with it (#4210).
	{"SessionEnd", "hook-session-end", ""},
}

func installClaudeHook(exe string, uninstall bool) (installResult, error) {
	wiring := claudeWiring()
	events := make([]string, 0, len(wiring))
	for _, h := range wiring {
		events = append(events, h.Event)
	}
	return installClaudeHooksAt(filepath.Join(sources.ClaudeConfigDir(), "settings.json"), exe, events, uninstall)
}

// installClaudeHooksAt writes deja's Claude Code hooks into one settings.json,
// keeping the events named and taking every other deja hook out.
func installClaudeHooksAt(path, exe string, events []string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if uerr := json.Unmarshal(old, &root); uerr != nil {
		return installResult{}, configParseError(path, uerr)
	}
	nextRoot := root
	// An event the installed Claude Code would reject is taken out, not
	// written: one it does not know fails the whole file (#4488).
	keep := map[string]bool{}
	if !uninstall {
		for _, e := range events {
			keep[e] = true
		}
	}
	for _, h := range claudeHookWiring {
		nextRoot = updateClaudeHook(nextRoot, h.Event, hookRun(exe, h.Sub), h.Matcher, uninstall || !keep[h.Event])
	}
	// In the shape the reader wrote it, like every other JSON writer: this was
	// the last one still marshalling straight, so an install that added hooks
	// also alphabetised their keys, reindented the file and expanded every
	// block they had written on one line — in the file that holds their
	// permissions and their model (#1665, the shape #2641 and #2704 gave the
	// others).
	next, err := marshalConfigLike(old, nextRoot)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func updateClaudeSessionStartHook(root map[string]any, exe string, uninstall bool) map[string]any {
	root = updateClaudeHook(root, "SessionStart", hookRun(exe, "hook-context"), "", uninstall)
	return root
}

// hookStatusMessage labels the hook while it runs. Codex carries it through
// to its hook run summary; Claude Code 2.1.220 parses the field but shows
// nothing for it — there the receipt in systemMessage is what the user sees,
// which was checked by rendering the actual TUI rather than by reading docs.
func hookStatusMessage(event string) string {
	switch event {
	case "SessionStart":
		return "Recalling past sessions…"
	case "UserPromptSubmit":
		return "Searching past sessions…"
	case "PreCompact":
		return "Saving this session to memory…"
	case "PreToolUse":
		return "Checking what this touches…"
	case "PostToolUse", "PostToolUseFailure":
		return "Checking what fixed this before…"
	case "SessionEnd":
		return "Marking this session as ended…"
	}
	return ""
}

// hookCommandKind says whose a hook command is. The word "deja" anywhere in the
// line is not the test: a tool living under /home/deja is somebody else's, and
// a line the reader wrote around deja's own invocation is theirs even though it
// runs ours (#2477). What decides it is the token in front of the subcommand.
type hookCommandKind int

const (
	// hookNotDejas: deja did not write this and does not run in it.
	hookNotDejas hookCommandKind = iota
	// hookDejas: the line deja writes — the binary and the subcommand, nothing
	// else. Install repoints it at the current path; uninstall takes it out.
	hookDejas
	// hookWrapsDejas: the reader's own line, which runs deja's hook as part of
	// something larger. It is already installed, so adding deja's line beside
	// it would run the hook twice at every session start; and it is not deja's
	// to rewrite or to delete.
	hookWrapsDejas
)

// retiredDejaHook reports whether a command line is deja's own, calling a hook
// this build no longer has: `<binary> hook-something` and nothing else.
//
// The writers recognise the subcommand they are installing and nothing else, so
// a line deja wrote under a name that has since gone was invisible to them. It
// sat beside the new one and ran on every session start forever, and since the
// build has no such command it did nothing but cost a process (#2719).
//
// Deja's own line only. A wrapper someone built around a hook is theirs, and so
// is a line with anything else on it.
func retiredDejaHook(existing any) bool {
	s, ok := existing.(string)
	if !ok {
		return false
	}
	fields := strings.Fields(s)
	if len(fields) != 2 || !strings.HasPrefix(fields[1], "hook-") {
		return false
	}
	// The whole field has to be the name and nothing else. A reader's line can
	// end in a redirect — deja hook-context>>/tmp/deja.log — which splits the
	// same way and is theirs to keep.
	for _, r := range fields[1] {
		if (r < 'a' || r > 'z') && r != '-' {
			return false
		}
	}
	if !isDejaBinaryToken(fields[0]) {
		return false
	}
	if hookNames[fields[1]] {
		return false
	}
	// A name that merely extends one of ours is not ours: `hook-context-extra`
	// is somebody else's subcommand that happens to start like deja's, and
	// TestALongerSubcommandIsNotOurs has held that line since before this.
	for name := range hookNames {
		if strings.HasPrefix(fields[1], name) {
			return false
		}
	}
	return true
}

// hookNames is every hook subcommand this build has. Written out rather than
// derived from the dispatch table, which cannot be read from here without an
// initialisation cycle; TestHookNamesMatchTheDispatchTable is what keeps the
// two from drifting, the same arrangement helpHidden uses.
var hookNames = map[string]bool{
	"hook-antigravity":  true,
	"hook-codewhale":    true,
	"hook-context":      true,
	"hook-goose":        true,
	"hook-goose-prompt": true,
	"hook-mcp-call":     true,
	"hook-plan":         true,
	"hook-precompact":   true,
	"hook-prompt":       true,
	"hook-refresh":      true,
	"hook-session-end":  true,
	"hook-stop":         true,
	"hook-tool":         true,
	"hook-tool-after":   true,
}

func hookCommandKindOf(existing any, cmd string) hookCommandKind {
	s, ok := existing.(string)
	if !ok || s == "" {
		return hookNotDejas
	}
	if s == cmd {
		return hookDejas
	}
	// A quoted path is one token whatever is in it, so the line is deja's
	// own when the path names deja and nothing follows but the subcommand.
	// Read as a wrapper, an install from a new path left the old line and
	// its dead path in place, and CodeBuddy's PowerShell form on Windows
	// stacked beside the quoted line it replaces (#4728).
	if bin, rest, ok := quotedHookLine(s); ok && rest == hookLineRest(cmd) && hookTokenIsDejas(bin) {
		return hookDejas
	}
	s, cmd = unwrapPowerShellHook(s), unwrapPowerShellHook(cmd)
	sub := cmd[strings.LastIndex(cmd, " ")+1:]
	// A line that ends in flags is told apart by all of them: Junie runs
	// `hook-context --plain --once --junie` and `hook-prompt --plain --junie`
	// on one event, and the last word alone made each of them the other's.
	if i := strings.LastIndex(cmd, " hook-"); i >= 0 && strings.Contains(cmd[i+1:], " ") {
		sub = cmd[i+1:]
	}
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], " "+sub)
		if j < 0 {
			return hookNotDejas
		}
		at := i + j
		end := at + 1 + len(sub)
		// Every candidate, not the first that matches: an unquoted path with a
		// space in it makes the last token look like a wrapper around deja's
		// hook while the whole prefix is deja's own binary, and reading it as
		// a wrapper is what left those entries unrepaired (#3692).
		kind := hookNotDejas
		for _, tok := range hookBinariesBefore(s[:at]) {
			if !hookTokenIsDejas(tok) || !subcommandEndsAt(s[end:]) {
				continue
			}
			if strings.TrimSpace(s) == strings.TrimSpace(tok+" "+sub) {
				return hookDejas
			}
			kind = hookWrapsDejas
		}
		if kind != hookNotDejas {
			return kind
		}
		i = end
	}
	return hookNotDejas
}

// hookBinariesBefore is what the binary could be in the text before a
// subcommand: the last shell token, and — when the whole of that text names a
// file on disk — the text itself.
//
// The second is for a path nothing quoted. An unquoted path with a space in it
// is several tokens, so the token test read `/tmp/deja spacey/…/deja-hook
// hook-prompt` as somebody else's command wrapping deja's, and an install left
// the entry it could not run exactly where it was (#3692). The file has to be
// there for that reading: `env FOO=1 /usr/bin/deja-hook hook-prompt` is a
// wrapper somebody wrote and is not deja's line to rewrite.
func hookBinariesBefore(prefix string) []string {
	out := []string{lastShellToken(prefix)}
	if whole := strings.TrimSpace(prefix); strings.Contains(whole, " ") && fileExists(whole) {
		out = append(out, whole)
	}
	return out
}

// hookTokenIsDejas is isDejaBinaryToken for the binary in a hook line, where
// the subcommand beside it has already been matched against one of deja's own.
// That pair is what makes a differently-named build recognisable: `go build -o
// deja-cont` and a probe run leave `<path>/deja-cont hook-prompt` in the
// settings, and the record of paths deja has installed from does not always
// hold them — on the machine this was found it had three of the six entries'
// binaries — so the name test has to answer too, or the entry reads as a
// stranger's and a new one stacks beside it on every install (#3681).
//
// Deliberately narrow: the token must be named like a deja build *and* be
// running one of deja's hook subcommands. A line that merely contains deja's
// hook inside something bigger is still hookWrapsDejas and is left alone.
//
// A build is `deja` or `deja-<name>`, bare or .exe, and `deja.test` is the
// test binary. A script is not: `deja-wrapper.cmd`, `deja-run.sh` and
// `dejavu-notify` are somebody's own programs that happen to take deja's
// subcommand, and reading them as ours rewrote them on install and deleted
// them on uninstall (#4728 review).
func hookTokenIsDejas(tok string) bool {
	if isDejaBinaryToken(tok) {
		return true
	}
	tok = strings.Trim(strings.TrimSpace(tok), `"'`)
	if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
		tok = tok[i+1:]
	}
	tok = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(tok), ".exe"), ".test")
	if strings.Contains(tok, ".") {
		return false
	}
	return tok == "deja" || strings.HasPrefix(tok, "deja-")
}

// isDejaHookCommand reports whether deja's hook runs in this command at all,
// whether as the line deja wrote or inside one the reader did. Callers that go
// on to rewrite the command ask for hookDejas instead.
func isDejaHookCommand(existing any, cmd string) bool {
	return hookCommandKindOf(existing, cmd) != hookNotDejas
}

// powerShellHookHead and powerShellHookTail are how codeBuddyHookRun's Windows
// line opens and closes.
const (
	powerShellHookHead = `powershell -NoProfile -Command "`
	powerShellHookTail = `; exit (Get-Variable LASTEXITCODE -ValueOnly)"`
)

// unwrapPowerShellHook is the command inside codeBuddyHookRun's Windows line,
// and the line itself when it is not one.
func unwrapPowerShellHook(s string) string {
	if len(s) > len(powerShellHookHead)+len(powerShellHookTail) && strings.HasPrefix(s, powerShellHookHead) && strings.HasSuffix(s, powerShellHookTail) {
		return s[len(powerShellHookHead) : len(s)-len(powerShellHookTail)]
	}
	return s
}

// quotedHookLine splits a hook line whose binary is quoted — `"<path>" rest`,
// `'<path>' rest`, PowerShell's `& '<path>' rest`, or that inside
// codeBuddyHookRun's Windows line — into the path and what follows it.
func quotedHookLine(s string) (bin, rest string, ok bool) {
	s = strings.TrimSpace(unwrapPowerShellHook(strings.TrimSpace(s)))
	s = strings.TrimPrefix(s, "& ")
	if len(s) < 2 || (s[0] != '"' && s[0] != '\'') {
		return "", "", false
	}
	q := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] != q {
			b.WriteByte(s[i])
			continue
		}
		// PowerShell writes a single quote inside single quotes twice.
		if q == '\'' && i+1 < len(s) && s[i+1] == '\'' {
			b.WriteByte('\'')
			i++
			continue
		}
		if i+1 >= len(s) || s[i+1] != ' ' {
			return "", "", false
		}
		return b.String(), strings.TrimSpace(s[i+1:]), true
	}
	return "", "", false
}

// hookLineRest is what a hook line deja writes runs after the binary: the
// subcommand and its flags.
func hookLineRest(cmd string) string {
	if _, rest, ok := quotedHookLine(cmd); ok {
		return rest
	}
	if _, after, ok := strings.Cut(cmd, " "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// lastShellToken is the word a command name would occupy: the last run of
// non-space characters before the subcommand.
func lastShellToken(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// dejaWrittenExes are the binary paths deja has written into configs, read
// once per process. A build under another name is deja's own when the record
// says deja installed from that path — the name test below cannot know it, and
// without this an install from such a build stacked a second entry beside the
// first (#3421).
var (
	writtenExesOnce sync.Once
	writtenExes     map[string]bool
)

func dejaWrittenExes() map[string]bool {
	writtenExesOnce.Do(func() {
		st := readWiringState()
		writtenExes = make(map[string]bool, len(st.Exes)+1)
		for _, p := range append(append([]string(nil), st.Exes...), st.Exe) {
			if p != "" {
				writtenExes[p] = true
			}
		}
	})
	return writtenExes
}

// forgetWrittenExes drops the cached record so a test — or a process that has
// just installed — reads it again.
func forgetWrittenExes() {
	writtenExesOnce = sync.Once{}
	writtenExes = nil
}

// isDejaBinaryToken reports whether a token names the deja binary, quoted or
// not, under any directory, on either platform's separator.
func isDejaBinaryToken(tok string) bool {
	tok = strings.Trim(strings.TrimSpace(tok), `"'`)
	// A path deja itself installed from is deja's own entry whatever the file
	// was called — checked on the whole path, before the name is taken out of
	// it (#3421).
	if dejaWrittenExes()[tok] {
		return true
	}
	// Both separators whatever this build was compiled for, because configs
	// travel between machines, and folded because a filesystem that does not
	// care about case still runs /opt/Deja (#2713).
	if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
		tok = tok[i+1:]
	}
	tok = strings.ToLower(tok)
	// deja-hook is the launcher deja installs so a config never has to name a
	// build (#3422). It is deja's own file under deja's own name, and an entry
	// running it is ours to adopt, rewrite and remove.
	return tok == "deja" || tok == "deja.exe" || tok == "deja-hook"
}

// subcommandEndsAt reports whether the subcommand really ended where it was
// found, rather than being the head of a longer word: "hook-prompt" must not
// match inside "hook-prompt-extra". A quote, a shell separator or the end of
// the line all end it.
func subcommandEndsAt(rest string) bool {
	if rest == "" {
		return true
	}
	switch rest[0] {
	case ' ', '\t', '\'', '"', ';', '&', '|', ')', '`', '\n':
		return true
	// A redirect glued to the word ends it the same way a pipe does, and no
	// subcommand contains one. Left out at first, so `deja hook-context>>/tmp/x`
	// read as somebody else's command and deja installed its hook beside it —
	// two session starts, memory injected twice (#2493).
	case '>', '<':
		return true
	}
	return false
}

func updateClaudeHook(root map[string]any, event, cmd, matcher string, uninstall bool) map[string]any {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	entries, _ := hooks[event].([]any)
	var out []any
	// A line the reader built around deja's hook already runs it, and it is not
	// ours to touch — so the collapse below has to know about it before it
	// reaches its own entry, whichever order the file happens to keep them in.
	found := !uninstall && eventWrapsDejaHook(entries, cmd)
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry == nil {
			out = append(out, entryAny)
			continue
		}
		hs, hasHooks := entry["hooks"].([]any)
		// Self-heal: an entry whose hooks are null/absent is invalid — Claude
		// Code rejects the whole file over it. Earlier deja versions could
		// leave one behind on uninstall when the entry carried a matcher.
		if !hasHooks || len(hs) == 0 {
			continue
		}
		var kept []any
		removed := false
		for _, hAny := range hs {
			h, _ := hAny.(map[string]any)
			kind := hookNotDejas
			if h != nil && h["type"] == "command" {
				// A line of ours calling a hook this build dropped is not a
				// third-party entry to leave alone; it is ours to take out.
				if retiredDejaHook(h["command"]) {
					removed = true
					continue
				}
				kind = hookCommandKindOf(h["command"], cmd)
			}
			if kind == hookWrapsDejas {
				// This hook is already installed, inside a line deja did not
				// write. Leaving it alone is the whole of what to do: a second
				// entry would inject memory twice, and rewriting it would throw
				// away whatever else the reader put on that line.
				found = true
				kept = append(kept, hAny)
				continue
			}
			if kind == hookDejas {
				if uninstall {
					removed = true
					continue
				}
				// A file that already collected several of ours converges here:
				// the first line becomes this binary's and the rest go, rather
				// than being rewritten into byte-identical copies that each
				// fire on every prompt (#3421). Same as qwen's writer (#2745)
				// and cursor's (#2691); this one was the last that stacked.
				if found {
					removed = true
					continue
				}
				// An entry of ours for this event already exists. Take it over
				// rather than adding a second one: installing from a new path
				// used to leave the old entry behind, and both would fire.
				found = true
				h["command"] = cmd
				if msg := hookStatusMessage(event); msg != "" {
					h["statusMessage"] = msg
				}
				adoptMatcher(entry, hs, matcher)
			}
			kept = append(kept, hAny)
		}
		if len(kept) != len(hs) || found {
			entry["hooks"] = kept
		}
		if removed {
			if len(kept) == 0 {
				continue
			}
			entry["hooks"] = kept
		}
		out = append(out, entry)
	}
	if !uninstall && !found {
		h := map[string]any{"type": "command", "command": cmd}
		if msg := hookStatusMessage(event); msg != "" {
			h["statusMessage"] = msg
		}
		entry := map[string]any{"hooks": []any{h}}
		if matcher != "" {
			entry["matcher"] = matcher
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = out
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	return root
}

// eventWrapsDejaHook reports whether any entry for this event runs deja's hook
// inside a command deja did not write.
func eventWrapsDejaHook(entries []any, cmd string) bool {
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry == nil {
			continue
		}
		hs, _ := entry["hooks"].([]any)
		for _, hAny := range hs {
			h, _ := hAny.(map[string]any)
			if h == nil || h["type"] != "command" {
				continue
			}
			if hookCommandKindOf(h["command"], cmd) == hookWrapsDejas {
				return true
			}
		}
	}
	return false
}

// adoptMatcher brings an entry deja already owns up to the matcher this build
// wires. Without it a matcher is written once and never again: grok's session
// hook carried `startup|resume` copied from Claude Code, grok names its sources
// `new` and `load`, and the corrected matcher would have reached only machines
// installing deja for the first time.
//
// It moves only when deja's hook is alone in the entry. A matcher covers every
// hook beside it, so rewriting a shared one would silently change when someone
// else's hook runs.
func adoptMatcher(entry map[string]any, hooks []any, matcher string) {
	if len(hooks) != 1 {
		return
	}
	if matcher == "" {
		delete(entry, "matcher")
		return
	}
	entry["matcher"] = matcher
}

// combinedStatusline builds a command that runs the existing statusline and
// deja's, separated by a middle dot. Each output goes through $(…), which
// drops its trailing newline: printed as is, a script ending in one put deja
// on a second row, and Kimi and CodeBuddy, which show the first row only, lost
// it (stands on Kimi 2.1.1 and CodeBuddy 2.16.0).
func combinedStatusline(existing, exe string) string {
	return fmt.Sprintf(`sh -c 'json=$(cat); printf "%%s · %%s\n" "$(printf "%%s" "$json" | %s)" "$(printf "%%s" "$json" | %s statusline)"'`, existing, exe)
}

// statuslineIsDejas reports whether Claude's settings carry a status bar deja
// wrote, whatever binary that entry names.
func statuslineIsDejas() bool {
	b, err := os.ReadFile(filepath.Join(sources.ClaudeConfigDir(), "settings.json"))
	if err != nil {
		return false
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return false
	}
	entry, _ := root["statusLine"].(map[string]any)
	cmd, _ := entry["command"].(string)
	return cmd != "" && linesRunDeja([]byte(cmd))
}

// installStatusline wires `deja statusline` as the Claude Code status bar.
// It refuses to replace a statusline the user already configured (many run
// ccstatusline or their own script) — printing how to combine instead — and
// rewrites one of deja's own whatever binary that entry names.
func installStatusline(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if uerr := json.Unmarshal(old, &root); uerr != nil {
		return installResult{}, configParseError(path, uerr)
	}
	cmd := exe + " statusline"
	existing, _ := root["statusLine"].(map[string]any)
	// Which entry is here, by the same test the hooks use: deja's own line
	// whatever binary it names, deja's inside one the reader built, or a
	// stranger's. Comparing against this binary's command alone meant a
	// statusline deja wrote from a path that has since moved read as the
	// reader's: the install refused to touch it, printing a combine line that
	// piped the missing binary into this one, the uninstall left it, and
	// doctor's "run `deja install claude-auto`" fixed nothing — the status bar
	// ran a file that was not there on every refresh (#3684).
	kind := hookNotDejas
	if existing != nil {
		kind = hookCommandKindOf(existing["command"], cmd)
	}
	note := ""
	if uninstall {
		// Only when deja's line is the whole line: one the reader combined
		// with theirs is theirs to edit.
		if kind != hookDejas {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		delete(root, "statusLine")
	} else {
		if kind == hookWrapsDejas {
			// deja's statusline already runs inside a line the reader built —
			// the combine the error below hands out. Rewriting it would throw
			// their half away.
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if existing != nil && kind == hookNotDejas {
			// Most people already run something here. Replacing it silently
			// would be rude, and "append our output" is not actionable — so
			// hand over the line that runs both. Claude pipes session JSON to
			// the command, so it is captured once and fed to each in turn.
			prev, _ := existing["command"].(string)
			return installResult{}, fmt.Errorf("a statusline is already configured — to keep both, set statusLine.command to:\n\n  %s", combinedStatusline(prev, exe))
		}
		if prev, _ := existing["command"].(string); prev != "" && prev != cmd {
			note = "replaced the deja statusline that was already here, which ran " + strings.Fields(prev)[0]
		}
		// refreshInterval makes Claude Code re-run the command on a timer
		// instead of only after a turn, so the first index build shows a
		// moving bar rather than a number frozen at whatever it was when the
		// user last typed.
		root["statusLine"] = map[string]any{"type": "command", "command": cmd, "refreshInterval": 1000}
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

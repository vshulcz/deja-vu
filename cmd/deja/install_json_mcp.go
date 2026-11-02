package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// dejaEntryKey keeps one MCP server when somebody wired deja by hand under a
// different name — `deja-vu` is the obvious choice. Install must update that
// entry rather than add a sibling that starts the server twice (#2269).
func dejaEntryKey(m map[string]any) string {
	if _, ok := m["deja"]; ok {
		return "deja"
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		if key != "deja" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	// Deja's server, not any entry with the binary's name in it: a filesystem
	// server serving ~/code/deja was taken over and lost its command (#4556).
	for _, key := range keys {
		if entry, ok := m[key].(map[string]any); ok && entryIsDejaServer(entry) {
			return key
		}
	}
	return "deja"
}

// leftDejaEntriesNote names what uninstall deliberately leaves behind: an
// entry deja never wrote, still running deja under another name. Deleting a
// hand add is worse than leaving it, and leaving it without a word looks like
// an uninstall that missed one (#2269).
func leftDejaEntriesNote(m map[string]any) string {
	var keys []string
	for key, entry := range m {
		if key != "deja" && mcpEntryRunsDeja(entry) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return leftNamedDejaEntriesNote(keys)
}

func leftNamedDejaEntriesNote(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = fmt.Sprintf("%q", key)
	}
	if len(quoted) == 1 {
		return fmt.Sprintf("left %s, which also runs deja — deja did not write it, so uninstall does not remove it", quoted[0])
	}
	return fmt.Sprintf("left %s, which also run deja — deja did not write them, so uninstall does not remove them", naturalList(quoted))
}

func naturalList(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	case 2:
		return xs[0] + " and " + xs[1]
	default:
		return strings.Join(xs[:len(xs)-1], ", ") + ", and " + xs[len(xs)-1]
	}
}

// replacedEntryNote names the deja wiring someone had in a config before
// install overwrote it. Replacing it is install's job — a stale entry is what
// the command exists to fix — but reporting it in the same sentence deja
// prints for a config that never mentioned deja reads as nothing having been
// there (#2390). Empty when the entry is absent or already what deja writes.
func replacedEntryNote(prev, entry any) string {
	if prev == nil || sameEntry(prev, entry) {
		return ""
	}
	was := entryCommandName(prev)
	if was == "" {
		return "replaced the deja entry that was already here"
	}
	// The same command is not a replacement anyone would want told about. An
	// entry an older deja wrote differs from this one by a field or two the
	// format gained since, and saying "replaced … which ran /bin/deja" of
	// /bin/deja is noise sitting where a real takeover should stand out
	// (#2479).
	if sameDejaCommand(was, entryCommandName(entry)) {
		return ""
	}
	return "replaced the deja entry that was already here, which ran " + safeForStatusline(was, 200)
}

// sameDejaCommand reports whether an entry that ran was runs the deja that now
// does: the same path or, now being the launcher, the binary it wraps or a
// launcher an older install wrote. The MCP entries moved to the launcher
// (#5323) said "replaced … which ran <deja>" on every upgrade from this very
// binary — the noise #2479 took out.
func sameDejaCommand(was, now string) bool {
	if was == now {
		return true
	}
	l := dejaLauncherPath()
	if l == "" || now != l {
		return false
	}
	if filepath.Base(was) == filepath.Base(l) {
		return true
	}
	cur, err := os.ReadFile(l)
	if err != nil {
		return false
	}
	if string(cur) == dejaLauncherScript(was) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(was)
	return err == nil && string(cur) == dejaLauncherScript(resolved)
}

// entryRunsDeja reports whether an MCP entry runs the deja binary, wherever the
// config keeps it: a command string, a command list, or the args behind a
// wrapper like Windows' `cmd /c`.
func entryRunsDeja(entry map[string]any) bool {
	// The shape a client nests: doctor read it and install did not, so one
	// command called a file wired while the other wrote a second server into
	// it (#2713).
	if t, ok := entry["transport"].(map[string]any); ok && entryRunsDeja(t) {
		return true
	}
	var words []string
	switch c := entry["command"].(type) {
	case string:
		words = append(words, c)
	case []any:
		for _, v := range c {
			if s, ok := v.(string); ok {
				words = append(words, s)
			}
		}
	}
	if args, ok := entry["args"].([]any); ok {
		for _, v := range args {
			if s, ok := v.(string); ok {
				words = append(words, s)
			}
		}
	}
	return slices.ContainsFunc(words, isDejaBinaryToken)
}

// withOtherDejaEntries folds the note about a second deja server into whatever
// the write already had to say. Every MCP writer needs it: the file it edits is
// the one that would hold the other entry.
func withOtherDejaEntries(note string, servers map[string]any, mine string) string {
	// Everything past the entry install just took over. One of them is now
	// deja's own — adopting it is what keeps the harness from starting two
	// servers — so naming it here would report deja's own wiring as a stranger.
	other := otherDejaEntriesNote(servers, mine)
	if other == "" {
		return note
	}
	if note != "" {
		return note + "; " + other
	}
	return other
}

// otherDejaEntriesNote names entries in the same file that also run deja under
// another key.
//
// doctor already reads past the key at what an entry runs, and calls `deja-vu`
// deja's wiring; install did not, so it wrote a second server beside one it
// recognised and said only "updated". The harness then starts two on every
// session, one of them possibly naming a binary that is gone (#2712).
//
// Said, not taken: a second entry can be deliberate — another index directory,
// a pinned older binary — and removing one deja did not write is the damage
// #2583 and #2600 were about. What the reader cannot do is act on a file whose
// state nothing reports.
func otherDejaEntriesNote(servers map[string]any, mine string) string {
	var names []string
	for name, entry := range servers {
		if name == mine {
			continue
		}
		m, _ := entry.(map[string]any)
		if m != nil && entryIsDejaServer(m) {
			names = append(names, name)
		}
	}
	return otherDejaNamesNote(names)
}

// withOtherDejaNames is withOtherDejaEntries for a writer that found the other
// entries itself: a list of servers, a YAML block, a file edited as text
// (#4556).
func withOtherDejaNames(note string, names []string) string {
	other := otherDejaNamesNote(names)
	if other == "" {
		return note
	}
	if note != "" {
		return note + "; " + other
	}
	return other
}

func otherDejaNamesNote(names []string) string {
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	noun, verb, it := "the entry ", "runs", "it"
	if len(names) > 1 {
		noun, verb, it = "the entries ", "run", "them"
	}
	return fmt.Sprintf("%s%s also %s deja — this harness will start %s alongside deja's own; remove what you do not want",
		noun, quotedList(names, 3), verb, it)
}

// entryIsDejaServer is entryRunsDeja for a claim about somebody else's entry.
//
// entryRunsDeja answers "may this be merged onto", where a false positive costs
// a merge onto an entry deja then overwrites anyway. This answers "should the
// reader be told to remove it", where a false positive is deja pointing at a
// config line that is none of its business: a filesystem server told to serve
// `~/code/deja` has the binary's name in its arguments and is not deja.
//
// So the name has to be the command — or, for the wrapper shapes where it
// cannot be (`cmd /c … deja mcp`), it has to be running deja's server rather
// than appearing in a path.
func entryIsDejaServer(entry map[string]any) bool {
	// The nested shape, on the same terms: this asks whether the entry is
	// deja's server, so the answer has to reach wherever the launch is written
	// (#2713).
	if t, ok := entry["transport"].(map[string]any); ok && entryIsDejaServer(t) {
		return true
	}
	switch c := entry["command"].(type) {
	case string:
		if isDejaBinaryToken(c) {
			return dejaServerArgs(entry)
		}
	case []any:
		if len(c) > 0 {
			if head, ok := c[0].(string); ok && isDejaBinaryToken(head) {
				return dejaServerArgs(entry)
			}
			for _, v := range c {
				if s, ok := v.(string); ok && isDejaBinaryToken(s) {
					return commandListRunsMCP(c)
				}
			}
		}
	}
	if args, ok := entry["args"].([]any); ok {
		for _, v := range args {
			if s, ok := v.(string); ok && isDejaBinaryToken(s) {
				return commandListRunsMCP(args)
			}
		}
	}
	return false
}

// dejaServerArgs reports whether an entry whose command is deja runs the MCP
// server rather than something else someone wired by hand.
func dejaServerArgs(entry map[string]any) bool {
	args, ok := entry["args"].([]any)
	if !ok {
		// A command with no arguments at all is deja's own shape from before
		// the subcommand was written into the entry.
		return entry["args"] == nil
	}
	return commandListRunsMCP(args)
}

// commandListRunsMCP reports whether `mcp` is one of these words.
func commandListRunsMCP(words []any) bool {
	for _, v := range words {
		if s, ok := v.(string); ok && s == "mcp" {
			return true
		}
	}
	return false
}

// mergeDejaEntry writes deja's wiring onto the entry that was already there.
// deja owns the command, the args and the type; an env pointing at a store on
// another disk, a timeout, a `disabled` the reader set — those are theirs, and
// replacing the whole entry threw them away on every reinstall (#2479).
func mergeDejaEntry(prev any, entry map[string]any) (map[string]any, string) {
	note := replacedEntryNote(prev, entry)
	old, ok := prev.(map[string]any)
	// Only onto an entry that was deja's. Keeping the fields of somebody
	// else's entry — a `url` of a remote server, an env built for another
	// program — would leave a mixed shape whose client has to guess which half
	// to believe.
	if !ok || !entryRunsDeja(old) {
		return entry, note
	}
	out := make(map[string]any, len(old)+len(entry))
	maps.Copy(out, old)
	maps.Copy(out, entry)
	// deja owns how the entry is launched, and it writes that at the top level.
	// An entry written in the nested shape kept its `transport` beside the
	// command deja had just written, and a client that prefers the nested one
	// went on launching the old binary while install reported success — the
	// mixed shape this function exists to avoid, reached through the wider gate
	// #2713 opened (#2716).
	if _, ok := out["command"]; ok {
		delete(out, "transport")
	}
	if entrySwitchedOff(out) {
		// Switching it back on silently would undo a decision deja was not
		// asked to revisit; leaving it off without a word looks like a install
		// that worked.
		if note != "" {
			note += "; "
		}
		note += switchedOffNote
	}
	return out, note
}

// installClientOffNotes are the lines an install adds when the client it just
// wrote has deja switched off by a switch install does not touch, so a plain
// `updated` does not read as working (#4468, #4469). The entry's own switch
// is left to the writer, which knows whether it kept it.
func installClientOffNotes(target string) []string {
	base := strings.TrimSuffix(target, "-auto")
	mcp, hooks := base, base
	switch base {
	case "claude", "claude-code":
		mcp, hooks = "claude-code", "claude-code"
	case "codex":
		hooks = "codex-hook"
	}
	var notes []string
	if base != target {
		if n := clientHooksOff(hooks); n != "" {
			notes = append(notes, n)
		}
	}
	if n := clientMCPDenied(mcp); n != "" {
		notes = append(notes, n)
	}
	if mcp == "workbuddy" {
		if n := workBuddyUntrustedNote(); n != "" {
			notes = append(notes, n)
		}
	}
	return notes
}

// claudeCleanupNote is what install says over Claude Code wiring when its
// cleanupPeriodDays sweep runs at the default 30 days or sooner: it deletes
// transcripts without a word, and nobody learns deja kept them until one is
// missing. The setting is the person's, so it is named and left alone.
func claudeCleanupNote(target string) string {
	if base := strings.TrimSuffix(target, "-auto"); base != "claude" && base != "claude-code" {
		return ""
	}
	days, ok := claudeCleanupDays(filepath.Join(sources.ClaudeConfigDir(), "settings.json"))
	if !ok || days > 30 {
		return ""
	}
	return fmt.Sprintf("Claude Code deletes transcripts after %d day%s; deja keeps a copy of each one it indexes from now on", days, pluralS(days))
}

// switchedOffNote is what install says over an entry the reader turned off.
const switchedOffNote = "left the entry switched off, the way it was — deja will not answer until you turn it back on"

// keepSwitch carries the reader's off switch from the entry deja is replacing
// onto the fresh one, for the writers that rebuild the entry rather than merge
// into it. They dropped it, so the next client start ran deja again for
// somebody who had switched it off (#4467). The note is "" when it was on.
func keepSwitch(prev any, next map[string]any) string {
	old, ok := prev.(map[string]any)
	if !ok || !entrySwitchedOff(old) {
		return ""
	}
	for _, k := range []string{"disabled", "enabled"} {
		if v, ok := old[k]; ok {
			next[k] = v
		}
	}
	return switchedOffNote
}

// entrySwitchHonored reports whether a client reads the entry's own
// `disabled`/`enabled` key. cursor-agent does not: 2026.09.02 parses mcp.json
// through a schema of command/args/env/cwd (or url/headers/auth/tls) that
// drops every other key, and `cursor-agent mcp list` shows an entry with
// `"disabled": true` ready. Its switch is `cursor-agent mcp disable`, which
// writes the project's mcp-disabled.json, and clientMCPDenied reads that.
// Claude Code does not either: 2.1.302 starts an entry with `"disabled":
// true`, and its switch is disabledMcpServers in the project's config, which
// clientMCPDenied reads too. Calling the entry off told the person recall was
// off while it ran (#5162, #5280; #4303 assumed both read Kiro's key).
func entrySwitchHonored(client string) bool {
	return len(entrySwitchKeys(client)) > 0
}

// entrySwitchKeys is which of the entry's own keys a client reads as off.
// Kimi Code 2.2 reads `enabled` and drops the rest, so `/mcp` lists an entry
// with "disabled": true connected and runs it.
func entrySwitchKeys(client string) []string {
	switch client {
	case "cursor", "claude-code":
		return nil
	case "kimi":
		return []string{"enabled"}
	}
	return []string{"disabled", "enabled"}
}

// entrySwitchedOffFor is entrySwitchedOff with only the keys the client reads.
func entrySwitchedOffFor(client string, entry map[string]any) bool {
	for _, k := range entrySwitchKeys(client) {
		switch k {
		case "disabled":
			if off, ok := entry[k].(bool); ok && off {
				return true
			}
		case "enabled":
			if on, ok := entry[k].(bool); ok && !on {
				return true
			}
		}
	}
	return false
}

// dropNotePart removes one "; "-joined part from an install note.
func dropNotePart(note, part string) string { //nolint:unparam // the part to drop is the caller's to name
	var keep []string
	for p := range strings.SplitSeq(note, "; ") {
		if p != part {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "; ")
}

// entrySwitchedOff reports whether the reader has turned this entry off.
//
// One switch, two spellings: most hosts write `disabled: true`, opencode
// writes `enabled: false`. The note was keyed to the first, so a config
// carrying the second reported a clean install for wiring that will never run
// (#2757).
func entrySwitchedOff(entry map[string]any) bool {
	if off, ok := entry["disabled"].(bool); ok && off {
		return true
	}
	if on, ok := entry["enabled"].(bool); ok && !on {
		return true
	}
	return false
}

// sameEntry compares an entry read out of a config with the one deja is about
// to write. Both sides go through the encoder first: a config's args decode as
// []any while deja builds []string, and those two are the same wiring.
func sameEntry(a, b any) bool {
	x, err := json.Marshal(a)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(x, y)
}

// entryCommandName reads the command out of an MCP entry deja did not write.
// Configs spell it either as a string beside separate args or as one list, and
// anything else is a shape deja has no name for.
func entryCommandName(entry any) string {
	m, ok := entry.(map[string]any)
	if !ok {
		return ""
	}
	switch c := m["command"].(type) {
	case string:
		return c
	case []any:
		if len(c) > 0 {
			if first, ok := c[0].(string); ok {
				return first
			}
		}
	}
	return ""
}

// mcpServerEntry is the JSON mcpServers value for deja. Windows stdio MCP
// clients (Claude Code among them) spawn through cmd, so the entry uses the
// cmd /c wrapper there; elsewhere it is the executable directly.
func mcpServerEntry(exe string) map[string]any {
	command, args := mcpCommandArgs(exe)
	return map[string]any{"type": "stdio", "command": command, "args": args}
}

func mcpCommandArgs(exe string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", exe, "mcp"}
	}
	return exe, []string{"mcp"}
}

// installCursor wires the MCP server into Cursor's global config
// (~/.cursor/mcp.json). Gemini CLI and Antigravity use the identical
// mcpServers shape in their own files.
func installCursor(exe string, uninstall bool) (installResult, error) {
	res, err := installMCPJSON(filepath.Join(sources.CursorCLIHome(), "mcp.json"), exe, uninstall)
	if !entrySwitchHonored("cursor") {
		res.Note = dropNotePart(res.Note, switchedOffNote)
	}
	return res, err
}

// copilotMCPConfigPath is where Copilot CLI reads its MCP servers. Doctor reads
// the same file install writes; it used to read the guidance skill instead and
// said wired with no server registered (#4232).
func copilotMCPConfigPath() string {
	return filepath.Join(sources.CopilotHome(), "mcp-config.json")
}

// installCopilotMCP wires deja into GitHub Copilot CLI's MCP registry
// (mcp-config.json under $COPILOT_HOME, or ~/.copilot). Copilot's schema
// differs from the common mcpServers shape: entries carry a type and an
// enabled-tools list.
func installCopilotMCP(exe string, uninstall bool) (installResult, error) {
	path := copilotMCPConfigPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	var note string
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if configIsJSONC(old) {
		// A comment is not a broken file, and refusing the target over one is
		// how somebody who annotated their config could not install deja at
		// all (#2783, the two targets #1664 did not reach).
		command, args := mcpCommandArgs(exe)
		return writeJSONCEntry(path, old, "mcpServers",
			map[string]any{"type": "local", "command": command, "args": args, "tools": []string{"*"}}, uninstall)
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
		// And the block, when deja is what put it there (#2604).
		if len(m) == 0 && blockWasAdded(path, "mcpServers") {
			delete(root, "mcpServers")
			forgetBlockAdded(path, "mcpServers")
		}
	} else {
		command, args := mcpCommandArgs(exe)
		key := dejaEntryKey(m)
		if key != "deja" {
			noteBlockAdded(path, "mcpServers."+key)
		}
		m[key], note = mergeDejaEntry(m[key], map[string]any{"type": "local", "command": command, "args": args, "tools": []string{"*"}})
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

// installOpenClawMCP wires deja into openclaw.json — OpenClaw keeps MCP
// servers under mcp.servers, not the common mcpServers root.
func installOpenClawMCP(exe string, uninstall bool) (installResult, error) {
	if !uninstall {
		if err := openclawIncludeRefuse(exe, false); err != nil {
			return installResult{}, err
		}
	}
	res, err := installOpenClawMCPEntry(exe, uninstall)
	if err == nil && uninstall {
		res.Note = joinNotesWith("; ", res.Note, openclawIncludedLeftNote(false))
	}
	return res, err
}

func installOpenClawMCPEntry(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	var note string
	switch {
	case len(bytes.TrimSpace(old)) == 0:
		root = map[string]any{}
	case configIsJSONC(old):
		// Two keys deep, which the text writer takes as a dotted block key
		// (#2783).
		command, args := mcpLaunchCommandArgs(exe, uninstall)
		return writeJSONCEntry(path, old, "mcp.servers",
			map[string]any{"command": command, "args": args}, uninstall)
	case json.Unmarshal(old, &root) != nil:
		return installResult{}, openclawParseError(path, old)
	}
	mcp, _, err := mcpBlock(root, "mcp", path)
	if err != nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if mcp == nil {
		mcp = map[string]any{}
		root["mcp"] = mcp
	}
	servers, _, err := mcpBlock(mcp, "servers", path)
	if err != nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if servers == nil {
		servers = map[string]any{}
		mcp["servers"] = servers
		noteBlockAdded(path, "mcp.servers")
	}
	if uninstall {
		delete(servers, "deja")
		removeAdoptedDejaEntries(path, "mcp.servers", servers)
		note = leftDejaEntriesNote(servers)
		if len(servers) == 0 && blockWasAdded(path, "mcp.servers") {
			delete(mcp, "servers")
			if len(mcp) == 0 {
				delete(root, "mcp")
			}
			forgetBlockAdded(path, "mcp.servers")
		}
	} else {
		command, args := mcpLaunchCommandArgs(exe, uninstall)
		key := dejaEntryKey(servers)
		if key != "deja" {
			noteBlockAdded(path, "mcp.servers."+key)
		}
		servers[key], note = mergeDejaEntry(servers[key], map[string]any{"command": command, "args": args})
		note = withOtherDejaEntries(note, servers, key)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

// firstDirThatTakesAWrite is the directory a write would actually land in: the
// config's own directory once it exists, and otherwise the nearest parent that
// does, since install creates what is missing under it. os.Stat rather than a
// Lstat: the write follows a symlinked directory, so the question has to
// follow it too.
func firstDirThatTakesAWrite(dir string) string {
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// mcpConfigReadable asks only whether the config can be read and parsed —
// deliberately not whether its block is a shape deja edits. On the way out,
// a block deja never wrote is left alone and reported unchanged (#2399), so
// naming it as a host deja gave up on would be wrong.
func mcpConfigReadable(path string) error {
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(old)) == 0 {
		return nil
	}
	_, err = parseConfigRoot(path, old)
	return err
}

// parseConfigRoot decodes a config the writers edit, comments and all. It
// returns a nil root only for an empty file; `null` is an error, since it
// decodes into a nil map without one and the writer then assigns into it.
func parseConfigRoot(path string, old []byte) (map[string]any, error) {
	text := string(old)
	if configIsJSONC(old) {
		text = stripJSONComments(text)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(text), &root); err != nil {
		return nil, configParseError(path, err)
	}
	if root == nil {
		return nil, configParseError(path, errors.New("the file holds null, not an object"))
	}
	return root, nil
}

// mcpEntryWritable asks whether installMCPJSON could write its entry into this
// config, without writing anything.
//
// It has to accept exactly what the writer accepts: a comment is not a broken
// file (#1664), so a JSONC config passes here and is edited as text. What it
// catches is the config the writer would refuse — one that does not parse, or
// whose block is something other than an object.
func mcpEntryWritable(path, blockKey string) error {
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(old)) == 0 {
		return nil
	}
	jsonc := configIsJSONC(old)
	root, err := parseConfigRoot(path, old)
	if err != nil {
		return err
	}
	if jsonc {
		// The text writer inserts rather than replaces, so a key holding
		// anything but an object would end up in the file twice with the
		// reader's value winning — writeJSONCEntry refuses those, and so must
		// this (#2740). It also needs a top-level object to insert into.
		if v, present := root[blockKey]; present {
			if _, isObject := v.(map[string]any); !isObject {
				return fmt.Errorf("%s: %q is not an object deja can edit — left as it was", path, blockKey)
			}
		}
		if zedTopLevelOpen(string(old)) < 0 {
			return fmt.Errorf("%s: does not look like a settings object; add %q by hand", path, blockKey)
		}
	}
	_, _, err = mcpBlock(root, blockKey, path)
	return err
}

func installMCPJSON(path, exe string, uninstall bool) (installResult, error) {
	command, args := mcpCommandArgs(exe)
	return installMCPJSONEntry(path, "mcpServers", map[string]any{"command": command, "args": args}, uninstall)
}

// installMCPJSONEntry is installMCPJSON for a client whose block has another
// name or whose entry carries more than the command: Muse reads the legacy
// mcp_servers as well as mcpServers, and wants a type and a mode (#4709).
func installMCPJSONEntry(path, blockKey string, entry map[string]any, uninstall bool) (installResult, error) {
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	var note string
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if configIsJSONC(old) {
		// A comment is not a broken file, and refusing the target over one is
		// how somebody who annotated their config could not install deja at
		// all (#1664).
		return writeJSONCEntry(path, old, blockKey, entry, uninstall)
	} else if uerr := json.Unmarshal(old, &root); uerr != nil {
		return installResult{}, configParseError(path, uerr)
	}
	m, _, err := mcpBlock(root, blockKey, path)
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
		root[blockKey] = m
		noteBlockAdded(path, blockKey)
	}
	if uninstall {
		delete(m, "deja")
		removeAdoptedDejaEntries(path, blockKey, m)
		note = leftDejaEntriesNote(m)
		// And the block, when deja is what put it there (#2604).
		if len(m) == 0 && blockWasAdded(path, blockKey) {
			delete(root, blockKey)
			forgetBlockAdded(path, blockKey)
		}
	} else {
		key := dejaEntryKey(m)
		if key != "deja" {
			noteBlockAdded(path, blockKey+"."+key)
		}
		m[key], note = mergeDejaEntry(m[key], entry)
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

// autoTargetFor maps a detected harness to the deepest integration it has: the
// -auto target where one exists, the plain one otherwise (the IDE extensions,
// where an MCP server is as far as it goes).
//
// This was a hand-written switch, and the two harnesses added after it was
// written fell through to the default — so `deja install --auto` wired their
// MCP server and quietly left auto-recall off on a machine that has it.
func autoTargetFor(detected string) string {
	// Detection reports Claude as "claude-code" while its target is "claude".
	base := detected
	if base == "claude-code" {
		base = "claude"
	}
	for _, name := range installTargetNames() {
		if name == base+"-auto" {
			return name
		}
	}
	return detected
}

// withWiredAutoTargets appends the -auto sibling of each target the record
// says is installed, so `deja install --all` refreshes the hook layer it finds
// rather than writing only the server one.
//
// It exists for DeepSeek Harness, where both layers are entries in one patch
// list: installing the plain target there takes the auto row out and deletes
// the plugin, which is the documented way to drop back from --auto — and it
// meant `install --all`, the ordinary "wire everything again" command, ended
// auto-recall for anyone who had it, with the report saying only "updated"
// (#3687). Added after the target rather than instead of it, because most
// -auto writers write hooks alone and the server layer still has to be run.
func withWiredAutoTargets(targets []string) []string {
	wired := map[string]bool{}
	for _, t := range readWiringState().Targets {
		wired[t] = true
	}
	// `deja install dsh-auto` went unrecorded until the alias was fixed, so
	// those machines' record says nothing about deepseek-auto and --all took
	// auto.js away (#3687). deja's own auto.js on disk says it was wired.
	if dshGeneratedAutoOnDisk() {
		wired["deepseek-auto"] = true
	}
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t)
		// Claude's detected name is not its hook target's stem, the one case
		// withAutoTargets also has to spell out.
		auto := strings.TrimSuffix(t, "-auto") + "-auto"
		if t == "claude-code" {
			auto = "claude-auto"
		}
		if auto != t && wired[auto] && !slices.Contains(targets, auto) {
			out = append(out, auto)
		}
	}
	return out
}

// withAutoTargets pairs each target with its -auto sibling where one exists,
// keeping the original order so the report reads harness by harness.
func withAutoTargets(targets []string) []string {
	known := map[string]bool{}
	for _, name := range installTargetNames() {
		known[name] = true
	}
	// Detection reports Claude as "claude-code" while its hook target is
	// "claude-auto"; without this the slash command and hooks survive a
	// full uninstall.
	autoName := map[string]string{"claude-code": "claude-auto"}
	out := make([]string, 0, len(targets)*2)
	seen := map[string]bool{}
	for _, t := range targets {
		// `deja install dsh-auto` works, so `deja uninstall dsh-auto` has to:
		// an alias missed the name list here and the run was refused.
		if canonical, ok := installTargetAliases[t]; ok {
			t = canonical
		}
		base := strings.TrimSuffix(t, "-auto")
		if alias, ok := autoName[base]; ok {
			for _, candidate := range []string{base, alias} {
				if !seen[candidate] {
					seen[candidate] = true
					out = append(out, candidate)
				}
			}
			continue
		}
		for _, candidate := range []string{base, base + "-auto"} {
			if !known[candidate] || seen[candidate] {
				continue
			}
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

// targetUnknownError marks a refusal for a name that is no target at all, so a
// run made only of those can say so without the partial-run summary.
type targetUnknownError struct{ error }

func allUnknownTargets(errs []error) bool {
	for _, e := range errs {
		var u targetUnknownError
		if !errors.As(e, &u) {
			return false
		}
	}
	return len(errs) > 0
}

// unknownTargetError names what would have worked. There are two dozen targets
// and the difference between them is a few characters, so a bare "unknown
// target" leaves someone who typed `claud` guessing — while `deja completion`
// two commands away lists its three valid values.
func unknownTargetError(target string) error {
	return targetUnknownError{unknownTargetMessage(target)}
}

func unknownTargetMessage(target string) error {
	names := installTargetNames()
	// `kiro-auto` is one edit from `kimi-auto`, and following that hint wires a
	// different agent. A known target with `-auto` on it is someone asking for
	// recall wiring that target does not have (#4301).
	if base, ok := strings.CutSuffix(strings.ToLower(strings.TrimSpace(target)), "-auto"); ok && base != "" {
		if slices.Contains(names, base) {
			return fmt.Errorf("unknown target %q — %s has no -auto target; `deja install %s` wires it", target, base, base)
		}
	}
	if near := nearestTarget(target, names); near != "" {
		return fmt.Errorf("unknown target %q — did you mean %q?", target, near)
	}
	return fmt.Errorf("unknown target %q — try one of: %s, or --all / --auto", target, strings.Join(names, ", "))
}

// nearestTarget finds a target within one edit of what was typed, which covers
// the ways a name is actually got wrong: a dropped letter, a doubled one, a
// transposition.
func nearestTarget(typed string, names []string) string {
	typed = strings.ToLower(strings.TrimSpace(typed))
	if typed == "" {
		return ""
	}
	// A truncated name is the commonest miss — `claud` for claude-code — and it
	// is far from the full string by edit distance, so check prefixes first.
	for _, n := range names {
		if strings.HasPrefix(n, typed) {
			return n
		}
	}
	best, bestDist := "", 3
	for _, n := range names {
		if d := editDistance(typed, n); d < bestDist {
			best, bestDist = n, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// installTargetNames is the one list of what `deja install` accepts. Shell
// completion and doctor's coverage test both read it, so a harness added to
// installTarget without appearing here is caught rather than quietly missing
// from both.
func installTargetNames() []string {
	names := []string{
		"claude-code", "claude-auto",
		"codex", "codex-auto",
		"opencode", "opencode-auto",
		"cursor", "cursor-auto",
		"gemini", "gemini-auto",
		"antigravity", "antigravity-auto",
		"qwen", "qwen-auto",
		"codebuddy", "codebuddy-auto",
		"workbuddy", "workbuddy-auto",
		"trae", "trae-auto",
		"trae-ide", "trae-ide-auto",
		"muse", "muse-auto",
		// MiniMax Code: the MCP entry alone, and the package that adds the
		// hooks beside it.
		"mcode", "mcode-auto",
		"kimi", "kimi-auto",
		"hermes", "hermes-auto",
		"pi", "pi-auto",
		"omp", "omp-auto",
		"amp", "amp-auto",
		"prime", "prime-auto",
		"deepseek", "deepseek-auto",
		"openclaw", "openclaw-auto",
		"reasonix", "reasonix-auto",
		"cline", "cline-auto",
		"goose", "goose-auto",
		"crush", "crush-auto",
		"grok", "grok-auto", "copilot", "copilot-auto", "roo", "kilocode", "kilocode-auto", "cherrystudio", "cherrystudio-auto", "kiro", "kiro-auto", "senpi", "senpi-auto", "kimchi", "kimchi-auto", "codewhale", "codewhale-auto", "junie", "junie-auto", "jetbrains", "windsurf", "gjc", "gjc-auto", "zcode", "zcode-auto", "commandcode", "commandcode-auto", "aider",
		// Continue keeps the server and the slash command in one assistant
		// config, and its skill in the folder beside it; there is no hook to
		// wire, so there is nothing an -auto target would add (#3062).
		"continue",
		// VS Code Copilot Chat's hooks are the file Copilot CLI's are in, so
		// vscode-auto and copilot-auto write the same one.
		"vscode", "vscode-auto",
		// Zed's agent takes MCP servers and nothing else: no CLI to hand a
		// prompt to, so there is no -auto pair to install.
		"zed",
		// Devin CLI reads Claude-shaped hooks from its own config.json and
		// MCP servers from mcp_config.json — the pair exists.
		"devin", "devin-auto",
		"statusline",
	}
	// Not a harness, and deliberately not "-auto": that suffix means a
	// harness's auto-recall hook, and this is the timer that keeps this
	// machine's memory in step with the others. It belongs in this list
	// because `deja install --all` is where someone setting up a second
	// machine looks — but only where deja can actually schedule anything.
	// Offering a target that always fails is worse than not offering it.
	if syncTimerSchedulable(runtime.GOOS) {
		names = append(names, "sync-timer")
	}
	return names
}

// syncTimerSchedulable reports whether this platform has a service manager the
// timer knows how to write for. Taking the platform as an argument keeps the
// answer checkable from any machine.
func syncTimerSchedulable(goos string) bool {
	return goos == "darwin" || goos == "linux"
}

// removeAdoptedDejaEntries removes only renamed entries an install claimed;
// hand wiring without that ownership record must survive uninstall (#2648).
func removeAdoptedDejaEntries(path, container string, m map[string]any) {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !mcpEntryRunsDeja(m[key]) {
			continue
		}
		name := container + "." + key
		if blockWasAdded(path, name) {
			delete(m, key)
			forgetBlockAdded(path, name)
		}
	}
}

// noAgentConfigHere answers the machine that has never run one of these agents.
//
// Every other empty answer in this binary says what it looked for and what to do
// next — a bare word someone has to go look up is the worst possible answer in
// this position (#830), and `deja last` on the same machine prints "no agent
// history was found on this machine; `deja sources` shows where deja looked".
// `--auto` and `--all` printed one line, "no known agent config directories
// found", and stopped. It is the second command the README hands someone, so
// that line was read as a failure with nothing after it.
func noAgentConfigHere(uninstall bool) {
	fmt.Printf("no agent config found on this machine — deja looked for the config of all %d agents it can wire; `deja sources` lists every store it reads\n", len(existingTargetChecks()))
	if uninstall {
		fmt.Println("nothing to remove.")
		return
	}
	fmt.Println("nothing to wire yet: run this again once an agent has written its first session, or name one now to wire it ahead of time — `deja install claude`")
}

// existingTargetChecks is what says a harness is on this machine: one path the
// harness itself creates, per install target. Named rather than inline so the
// test that holds it to the target list can ask for a path instead of
// repeating the platform switches (#3192).
func existingTargetChecks() map[string]string {
	return map[string]string{
		"claude-code": sources.ClaudeConfigDir(),
		"codex":       sources.CodexHome(),
		"opencode":    filepath.Join(opencodeConfigHome(), "opencode"),
		"cursor":      sources.CursorCLIHome(),
		"gemini":      filepath.Join(sources.GeminiHome(), "settings.json"),
		"antigravity": antigravityConfigHome(),
		"copilot":     sources.CopilotHome(),
		"grok":        sources.GrokRoot(),
		"qwen":        sources.QwenConfigDir(),
		"kimi":        sources.KimiConfigDir(),
		"cline":       sources.ClineConfigDir(),
		"hermes":      sources.HermesHome(),
		"pi":          sources.PiConfigDir(),
		"omp":         sources.OmpConfigDir(),
		"deepseek":    sources.DSHHome(),
		"openclaw":    sources.OpenClawStateDir(),
		// aider keeps no config directory: its history file is what says it
		// has been used here. The binary alone would match every machine that
		// merely has it on PATH.
		"aider": filepath.Join(homeDir(), ".aider.chat.history.md"),
		// The session store, not the config directory: deja creates the
		// latter itself, which would make every machine look like a Goose
		// machine after one install.
		"goose": sources.GooseRoot(),
		"roo":   rooFirstRoot(),
		// Kilo Code is keyed on its own globalStorage rather than on the
		// editor's: a VS Code install without the extension is not a Kilo
		// machine, and --auto must not write settings for one.
		"kilocode": kilocodeFirstRoot(),
		// Cherry Studio keeps its servers in its own database, so --auto only
		// writes the import file where the app itself has a store.
		"cherrystudio": cherryStudioFirstRoot(),
		"kiro":         sources.KiroRoot(),
		"senpi":        sources.SenpiRoot(),
		"kimchi":       sources.KimchiRoot(),
		"codewhale":    sources.CodeWhaleRoot(), // its sessions; deja creates mcp.json
		"junie":        sources.JunieRoot(),     // its sessions; deja creates config.json and mcp/
		"jetbrains":    sources.JetBrainsRoot(), // the IDEs' config; deja writes ~/.ai/mcp
		"windsurf":     filepath.Join(sources.WindsurfRoot(), "cascade"),
		"gjc":          sources.GjcRoot(),
		"zcode":        sources.ZCodeRoot(),
		"commandcode":  commandCodeFirstRoot(),
		"codebuddy":    sources.CodeBuddyRoot(), // its session store; deja creates the config dir
		"workbuddy":    filepath.Join(sources.WorkBuddyConfigDir(), "projects"),
		"trae":         filepath.Join(sources.TraeRoot(), "sessions"), // the same: deja creates traecli.toml
		"muse":         sources.MuseRoot(),                            // the same: deja creates ~/.config/muse
		// The session store, not the config: deja creates mcp.json and the
		// plugin package itself, so keying on the config would make every
		// machine a MiniMax Code machine after one install.
		"mcode": sources.McodeRoot(),
		// The IDE's own globalStorage, not User/: deja writes User/mcp.json.
		"trae-ide": filepath.Join(traeIDEUserDir(), "globalStorage"),
		// Reasonix's own config.toml, which it writes on first run. deja
		// writes beside it — plugins/ and plugin-packages.json — and never
		// into it, so keying on the home itself would make every machine a
		// Reasonix machine after one install.
		"reasonix": filepath.Join(sources.ReasonixHome(), "config.toml"),
		// These six have install targets and were in the matrix with nothing
		// looking for them, so `--auto` wired the other nineteen and said
		// nothing about Amp, prime-agent, Crush, Continue, Zed or VS Code on a
		// machine that had them (#3192). Each is keyed on something the harness
		// writes for itself, for the reason goose's entry gives.
		//
		// Amp's thread store rather than its settings file: deja writes into
		// the settings file.
		"amp": sources.AmpRoot(),
		// prime-agent's sessions directory, and it honours the harness's own
		// relocation variables, so a moved store is still found.
		"prime": sources.PrimeRoot(),
		// Crush's project registry, in its data home. Its config directory is
		// where deja writes crush.json, so keying on that would make every
		// machine a Crush machine after one install.
		"crush": filepath.Join(sources.CrushDataHome(), "projects.json"),
		// Continue's session directory. The folder above it holds the config
		// deja writes, and Continue creates the sessions directory on its
		// first conversation — so a Continue that has been installed and never
		// used is not detected, which is the same bar the aider entry sets.
		"continue": filepath.Join(sources.ContinueRoot(), "sessions"),
		// Zed's data directory, not the config file deja edits.
		"zed": sources.ZedRoot(),
		// The CLI's own versions live under config/devin/cli — a directory
		// `devin` creates and deja only reads beside, so a machine that merely
		// installed deja does not look like a Devin machine.
		"devin": filepath.Join(devinConfigDir(), "cli"),
		// VS Code's own User folder, which the editor creates on first run;
		// deja only ever writes inside it.
		"vscode": vsCodeFirstRoot(),
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The hosts below run a command for their status line the way Claude Code
// does, so their -auto targets put `deja statusline` there. Claude's is a
// target of its own because most people there already run one; here it rides
// -auto, and a status line somebody did configure is left as it is, with a
// note saying how to run both. Each host's file, key and payload were read
// off a live stand that rendered a probe's output.

// hostStatuslineCommand is the line a host runs for deja's status line.
func hostStatuslineCommand(exe string, uninstall bool) string {
	return hookRun(hookExeFor(exe, uninstall), "statusline")
}

// statuslineKeptNote is what a target says when it left someone's own line.
// The combined command holds both kinds of quote, so a TOML host gets the
// whole line as a literal string, ready to paste.
func statuslineKeptNote(prev, cmd string, toml bool) string {
	if prev == "" {
		return "left the status line that was already set up there"
	}
	both := combinedStatusline(prev, strings.TrimSuffix(cmd, " statusline"))
	if toml {
		return "left the status line that was already there; to show deja's beside it, make its command line:\n\n  command = '''" + both + "'''"
	}
	return "left the status line that was already there; to show deja's beside it, set its command to:\n\n  " + both
}

// statuslineAt is the object at keys in root, creating the parents when create
// is set. nil when a parent is missing or not an object.
func statuslineAt(root map[string]any, keys []string, create bool) map[string]any {
	cur := root
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			if !create || cur[k] != nil {
				return nil
			}
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	return cur
}

// installJSONStatusline writes entry under keys in a JSON settings file. The
// entry's "command" is deja's line; one deja wrote from another path is
// rewritten, a stranger's is left alone with a note, and uninstall takes only
// deja's own.
func installJSONStatusline(path string, keys []string, entry map[string]any, uninstall bool) (installResult, error) {
	return jsonStatuslineUpdate(path, keys, entry, uninstall, true)
}

// jsonStatuslineUpdate is installJSONStatusline with the write optional: unset,
// it returns the refusal the write would meet and changes nothing, for a
// target's pre-flight.
func jsonStatuslineUpdate(path string, keys []string, entry map[string]any, uninstall, write bool) (installResult, error) {
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	// Read with comments blanked, the way the hook writer beside it reads the
	// same file: a status line it has nothing to change in is "unchanged",
	// and refusing there failed the uninstall of a file install had just
	// edited.
	jsonc := configIsJSONC(old)
	source := old
	if jsonc {
		source = []byte(jsoncToJSON(string(old)))
	}
	root := map[string]any{}
	if len(bytes.TrimSpace(source)) > 0 {
		if uerr := json.Unmarshal(source, &root); uerr != nil {
			return installResult{}, configParseError(path, uerr)
		}
	}
	before, err := json.Marshal(root)
	if err != nil {
		return installResult{}, err
	}
	cmd, _ := entry["command"].(string)
	last := keys[len(keys)-1]
	// A null parent in a commented file: the text writer would put a second
	// key of that name beside it, and the reader's null wins the read.
	if jsonc && !uninstall {
		cur := root
		for _, k := range keys[:len(keys)-1] {
			v, held := cur[k]
			if held && v == nil {
				return installResult{}, fmt.Errorf("%s: %s is null, so there is nowhere to put the status line", path, k)
			}
			if cur, _ = v.(map[string]any); cur == nil {
				break
			}
		}
	}
	parent := statuslineAt(root, keys, !uninstall)
	if parent == nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, fmt.Errorf("%s: %s is not an object, so there is nowhere to put the status line", path, strings.Join(keys[:len(keys)-1], "."))
	}
	existing, _ := parent[last].(map[string]any)
	// `"statusLine": null` is a key the text writer has to write over, not
	// beside: the last of two keys wins the read.
	_, held := parent[last]
	nullHeld := held && parent[last] == nil
	kind := hookNotDejas
	if existing != nil {
		kind = hookCommandKindOf(existing["command"], cmd)
	}
	switch {
	case uninstall && kind != hookDejas:
		return installResult{Path: path, Action: "unchanged"}, nil
	case uninstall:
		delete(parent, last)
		// A parent only the status line needed goes with it, so an install
		// and uninstall give the file back as it was.
		for i := len(keys) - 1; i > 0; i-- {
			if obj := statuslineAt(root, keys[:i+1], false); obj != nil && len(obj) == 0 {
				if up := statuslineAt(root, keys[:i], false); up != nil {
					delete(up, keys[i-1])
				}
			}
		}
	case kind == hookWrapsDejas:
		return installResult{Path: path, Action: "unchanged"}, nil
	case parent[last] != nil && kind == hookNotDejas:
		prev, _ := existing["command"].(string)
		return installResult{Path: path, Action: "unchanged", Note: statuslineKeptNote(prev, cmd, false)}, nil
	default:
		parent[last] = entry
	}
	if jsonc {
		return writeJSONCStatusline(path, old, root, keys, entry, uninstall, nullHeld, write)
	}
	if !write {
		after, merr := json.Marshal(root)
		if merr != nil {
			return installResult{}, merr
		}
		if bytes.Equal(after, before) {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{Path: path}, configWritable(path)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

// writeJSONCStatusline makes the edit decided above as text, for a settings
// file that carries comments or trailing commas. Copilot CLI, Cursor and the
// rest read theirs as JSONC, and refusing one failed the whole -auto target
// after its MCP entry and hooks were written, or, on the way out, left a status
// line running a deja-hook uninstall had just removed.
//
// root is the file as it will read afterwards: on uninstall, a parent the
// status line was the only thing in is already gone from it, and goes from
// the text too.
//
// write false only checks, as the JSON path does: a pre-flight that wrote
// here left the status line wired in a target it then refused.
func writeJSONCStatusline(path string, old []byte, root map[string]any, keys []string, entry map[string]any, uninstall, nullHeld, write bool) (installResult, error) {
	text := lfText(old)
	parents := keys[:len(keys)-1]
	last := keys[len(keys)-1]
	var next string
	var err error
	if uninstall {
		dropFrom := len(parents)
		for i := range parents {
			if statuslineAt(root, keys[:i+2], false) == nil {
				dropFrom = i
				break
			}
		}
		next, err = jsoncSetEntry(text, joinBlockKey(parents), last, "", true, dropFrom)
	} else {
		var body string
		if body, err = jsoncEntryText(entry); err != nil {
			return installResult{}, err
		}
		if nullHeld {
			next, err = jsoncReplaceNull(text, parents, last, body)
		} else {
			next, err = jsoncSetEntry(text, joinBlockKey(parents), last, body, false, len(parents))
		}
	}
	if err != nil {
		return installResult{}, configParseError(path, err)
	}
	if !write {
		if next == text {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{Path: path}, configWritable(path)
	}
	a, err := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a}, err
}

// jsoncReplaceNull writes body over a key's null value under parents.
func jsoncReplaceNull(text string, parents []string, key, body string) (string, error) {
	open := zedTopLevelOpen(text)
	if open < 0 {
		return "", errors.New("does not look like a settings object")
	}
	block := &zedSpan{keyStart: open, valueOpen: open, valueEnd: zedObjectEnd(text, open)}
	if len(parents) > 0 {
		block, _ = walkJSONCKeys(text, open, parents)
	}
	if block == nil || block.valueEnd < 0 {
		return "", fmt.Errorf("cannot find %q to set", strings.Join(append(append([]string{}, parents...), key), "."))
	}
	at := jsoncScalarValue(text, block, key)
	if at == nil || at[0] == at[1] {
		return "", fmt.Errorf("cannot find %q to set", strings.Join(append(append([]string{}, parents...), key), "."))
	}
	return text[:at[0]] + entryAtDepth(body, len(parents)) + text[at[1]:], nil
}

// installTOMLStatusline writes the [table] block in a TOML config, with the
// same rules: deja's own block is rewritten, anyone else's left with a note.
// A table set any other way (inline, dotted keys) counts as someone else's.
func installTOMLStatusline(path, table, block, cmd string, uninstall bool) (installResult, error) {
	old, next, res, err := tomlStatuslineUpdate(path, table, block, cmd, uninstall)
	if err != nil || next == nil {
		return res, err
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

// tomlStatuslineUpdate is installTOMLStatusline without the write: next is
// nil when the answer is res as it stands.
func tomlStatuslineUpdate(path, table, block, cmd string, uninstall bool) (old, next []byte, res installResult, err error) {
	old, err = readConfig(path)
	if err != nil {
		return nil, nil, installResult{}, err
	}
	text := lfText(old)
	if err := tomlHeadersClose(text); err != nil {
		return nil, nil, installResult{}, configParseError(path, err)
	}
	lines := strings.Split(text, "\n")
	code := tomlCodeLines(text)
	start, end := -1, len(lines)
	for i, line := range code {
		h := tomlCode(line)
		if !strings.HasPrefix(h, "[") {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		if strings.TrimSpace(strings.Trim(h, "[]")) == table {
			start = i
		}
	}
	prev := ""
	if start >= 0 {
		for _, line := range code[start+1 : end] {
			if k, v, ok := tomlLineKeyValue(line); ok && k == "command" {
				prev = v
				if u, err := strconv.Unquote(v); err == nil {
					prev = u
				} else if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
					prev = v[1 : len(v)-1]
				}
			}
		}
	}
	kind := hookCommandKindOf(prev, cmd)
	elsewhere := start < 0 && tomlNamesKeyOutsideTable(code, table)
	switch {
	case uninstall && (start < 0 || kind != hookDejas):
		return old, nil, installResult{Path: path, Action: "unchanged"}, nil
	case !uninstall && kind == hookWrapsDejas:
		return old, nil, installResult{Path: path, Action: "unchanged"}, nil
	case !uninstall && (elsewhere || start >= 0 && kind == hookNotDejas):
		return old, nil, installResult{Path: path, Action: "unchanged", Note: statuslineKeptNote(prev, cmd, true)}, nil
	}
	// The keys deja's block sets. Anything else in the table is the client's
	// own: Kimi keeps `items` beside the command and writes both, and
	// rebuilding the table from deja's block dropped them.
	blockLines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	ours := map[string]string{}
	for _, line := range blockLines[1:] {
		if k, _, ok := tomlLineKeyValue(line); ok {
			ours[k] = line
		}
	}
	var out []string
	switch {
	case start >= 0:
		keep := end
		for keep > start+1 && strings.TrimSpace(lines[keep-1]) == "" {
			keep--
		}
		var body []string
		foreign := false
		written := map[string]bool{}
		for i := start + 1; i < keep; i++ {
			k, _, ok := tomlLineKeyValue(code[i])
			if _, mine := ours[k]; ok && mine {
				if !uninstall && !written[k] {
					body = append(body, ours[k])
					written[k] = true
				}
				continue
			}
			// A comment is not a key: on its own it would keep an empty
			// table, which grok warns about.
			if strings.TrimSpace(tomlCode(code[i])) != "" {
				foreign = true
			}
			body = append(body, lines[i])
		}
		if !uninstall {
			// Rewritten where it stands. Moved to the end, it traded places
			// with the MCP block the same target writes, which also goes to
			// the end, so every repeat install rewrote the file twice.
			for _, line := range blockLines[1:] {
				if k, _, ok := tomlLineKeyValue(line); ok && !written[k] {
					body = append(body, line)
					written[k] = true
				}
			}
			out = append(append(append(append(out, lines[:start]...), lines[start]), body...), lines[keep:]...)
			s := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
			return old, []byte(s), installResult{}, nil
		}
		if foreign {
			out = append(append(append(append(out, lines[:start]...), lines[start]), body...), lines[keep:]...)
		} else {
			out = append(append(out, lines[:start]...), lines[end:]...)
		}
	default:
		out = lines
	}
	s := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if !uninstall {
		if s != "" {
			s += "\n\n"
		}
		s += strings.TrimRight(block, "\n")
	}
	if s != "" {
		s += "\n"
	}
	return old, []byte(s), installResult{}, nil
}

// tomlNamesKeyOutsideTable reports whether a.b is set some way other than a
// [a.b] header: `b = {…}` or `b.x = …` under [a], or a dotted key at the top.
func tomlNamesKeyOutsideTable(code []string, table string) bool {
	parent, leaf := "", table
	if i := strings.LastIndex(table, "."); i >= 0 {
		parent, leaf = table[:i], table[i+1:]
	}
	current := ""
	for _, line := range code {
		h := tomlCode(line)
		if strings.HasPrefix(h, "[") {
			current = strings.TrimSpace(strings.Trim(h, "[]"))
			if strings.HasPrefix(current, table+".") {
				return true
			}
			continue
		}
		k, _, ok := tomlLineKeyValue(line)
		if !ok {
			continue
		}
		full := k
		if current != "" {
			full = current + "." + k
		}
		if full == parent+"."+leaf || strings.HasPrefix(full, table+".") || parent == "" && current == "" && (k == leaf || strings.HasPrefix(k, leaf+".")) {
			return true
		}
	}
	return false
}

// cursorCLIConfigPath is where cursor-agent reads cli-config.json:
// CURSOR_CONFIG_DIR, then $XDG_CONFIG_HOME/cursor, then ~/.cursor
// (cursor-config paths.js, 2026.09.02). XDG_CONFIG_HOME moves it on macOS too.
func cursorCLIConfigPath() string {
	if v := strings.TrimSpace(os.Getenv("CURSOR_CONFIG_DIR")); v != "" {
		return filepath.Join(v, "cli-config.json")
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return filepath.Join(v, "cursor", "cli-config.json")
	}
	return filepath.Join(sources.Home(), ".cursor", "cli-config.json")
}

// installCursorStatusline: cursor-agent splits the command into argv and runs
// it without a shell, piping Claude's payload with transcript_path. It re-runs
// on turn and token changes; there is no timer.
func installCursorStatusline(exe string, uninstall bool) (installResult, error) {
	cmd := hostStatuslineCommand(exe, uninstall)
	return installJSONStatusline(cursorCLIConfigPath(), []string{"statusLine"},
		map[string]any{"type": "command", "command": cmd}, uninstall)
}

// installCopilotStatusline: Copilot CLI reads statusLine from settings.json;
// config.json says "User settings belong in settings.json" and moves one found
// there. refreshInterval is seconds: without it the line runs on events only,
// and a first index build would sit at one number (1.0.79). transcript_path is
// the session's directory, named by its id.
func installCopilotStatusline(exe string, uninstall bool) (installResult, error) {
	cmd := hostStatuslineCommand(exe, uninstall)
	return installJSONStatusline(filepath.Join(sources.CopilotHome(), "settings.json"), []string{"statusLine"},
		map[string]any{"type": "command", "command": cmd, "refreshInterval": 1}, uninstall)
}

// installQwenStatusline: Qwen Code 0.20 reads ui.statusLine and pipes the
// session id and workspace, no transcript path. refreshInterval is seconds.
func installQwenStatusline(exe string, uninstall bool) (installResult, error) {
	cmd := hostStatuslineCommand(exe, uninstall)
	return installJSONStatusline(filepath.Join(sources.QwenConfigDir(), "settings.json"), []string{"ui", "statusLine"},
		map[string]any{"type": "command", "command": cmd, "refreshInterval": 1}, uninstall)
}

// installCodeBuddyStatusline: CodeBuddy Code reads statusLine.command alone
// and pipes Claude's payload; it re-runs on session and settings changes.
func installCodeBuddyStatusline(exe string, uninstall bool) (installResult, error) {
	cmd := hostStatuslineCommand(exe, uninstall)
	return installJSONStatusline(codeBuddySettingsPath(), []string{"statusLine"},
		map[string]any{"type": "command", "command": cmd}, uninstall)
}

// installGrokStatusline: Grok Build 1.0.41 reads [ui.status_line] from the
// user config.toml only; type = "command" is required. It pipes Claude's
// payload, transcript_path included once the first prompt is in.
func installGrokStatusline(exe string, uninstall bool) (installResult, error) {
	cmd := hostStatuslineCommand(exe, uninstall)
	block := "[ui.status_line]\ntype = \"command\"\ncommand = " + strconv.Quote(cmd) + "\n"
	return installTOMLStatusline(filepath.Join(sources.GrokHome(), "config.toml"), "ui.status_line", block, cmd, uninstall)
}

// installTraeStatusline: TRAE CLI reads [tui.statusline] type = "command" from
// $TRAE_HOME/traecli.toml (0.207.1 and 0.208.1-alpha.5 both load it, and refuse
// an unknown type). It pipes workspace, model and usage JSON with no session
// id. A live check needs an enterprise login, which the TUI asks for first.
func installTraeStatusline(exe string, uninstall bool) (installResult, error) {
	cmd := hostStatuslineCommand(exe, uninstall)
	block := "[tui.statusline]\ntype = \"command\"\ncommand = " + strconv.Quote(cmd) + "\n"
	return installTOMLStatusline(traeConfigPath(), "tui.statusline", block, cmd, uninstall)
}

// installAntigravityStatusline: agy reads statusLine from
// ~/.gemini/antigravity-cli/settings.json (agy 1.3.1, /statusline <command>)
// and pipes conversation_id, session_id and transcript_path on every agent
// state change. stack_with_default keeps agy's own line above deja's.
func installAntigravityStatusline(exe string, uninstall bool) (installResult, error) {
	cmd := hostStatuslineCommand(exe, uninstall)
	return installJSONStatusline(filepath.Join(homeDir(), ".gemini", "antigravity-cli", "settings.json"), []string{"statusLine"},
		map[string]any{"type": "command", "command": cmd, "stack_with_default": true}, uninstall)
}

// installKimiStatusline: Kimi Code reads [status_line] command from tui.toml
// since 0.30.0 (0.28 and 0.29 have no status line and ignore the file). It
// pipes camelCase JSON with sessionId and no transcript path, and waits 300 ms
// for the answer.
func installKimiStatusline(exe string, uninstall bool) (installResult, error) {
	cmd := hostStatuslineCommand(exe, uninstall)
	return installTOMLStatusline(filepath.Join(sources.KimiConfigDir(), "tui.toml"), "status_line", kimiStatuslineBlock(cmd), cmd, uninstall)
}

func kimiStatuslineBlock(cmd string) string {
	return "[status_line]\ncommand = " + strconv.Quote(cmd) + "\n"
}

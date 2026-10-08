package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// CodeWhale keeps its MCP servers in mcp.json under `servers`, not
// `mcpServers`, and boots them lazily: a server is started only for a turn
// whose `tools.always_load` names one of its tools, or once the model finds it
// through tool search. On a 0.10.0 stand the deja tool was in the request with
// always_load set and absent without it, so install sets both.
func codewhaleMCPPath() string {
	return filepath.Join(sources.CodeWhaleConfigDir(), "mcp.json")
}

func codewhaleConfigPath() string {
	return filepath.Join(sources.CodeWhaleConfigDir(), "config.toml")
}

// codewhaleToolName is the name CodeWhale gives deja's tool: mcp_<server>_<tool>.
const codewhaleToolName = "mcp_deja_deja"

func installCodeWhale(exe string, uninstall bool) (installResult, error) {
	command, args := mcpCommandArgs(exe)
	res, err := installMCPJSONEntry(codewhaleMCPPath(), "servers", map[string]any{"command": command, "args": args}, uninstall)
	if err != nil {
		return res, err
	}
	load, err := codewhaleAlwaysLoad(uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(res, load), nil
}

func codewhaleAlwaysLoad(uninstall bool) (installResult, error) {
	path := codewhaleConfigPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	if uninstall && len(old) == 0 {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	next, err := tomlListedUnder(lfText(old), "tools", "always_load", codewhaleToolName, uninstall)
	if err != nil {
		return installResult{}, configParseError(path, err)
	}
	a, err := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a}, err
}

// tomlListedUnder adds value to the string array key in [table], or takes it
// out, and leaves every other line as it was. A table without the key gets it
// under its header; a file without the table gets the table at the end. An
// array spread over several lines is the reader's to edit.
func tomlListedUnder(text, table, key, value string, remove bool) (string, error) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	code := tomlCodeLines(strings.Join(lines, "\n"))
	header, keyLine, end := -1, -1, len(lines)
	for i := range lines {
		c := tomlCode(code[i])
		if strings.HasPrefix(c, "[") {
			if header >= 0 {
				end = i
				break
			}
			if c == "["+table+"]" {
				header = i
			}
			continue
		}
		k, _, ok := tomlLineKeyValue(code[i])
		if !ok {
			continue
		}
		if header >= 0 && k == key {
			keyLine = i
		}
		if header < 0 && k == table+"."+key {
			return "", fmt.Errorf("%s.%s is set as a dotted key; add %q to it by hand", table, key, value)
		}
	}
	finish := func(ls []string) string {
		if len(ls) == 0 {
			return ""
		}
		return strings.Join(ls, "\n") + "\n"
	}
	if keyLine < 0 {
		if remove {
			return finish(lines), nil
		}
		entry := key + " = " + tomlStringArray([]string{value})
		if header < 0 {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			return finish(append(lines, "["+table+"]", entry)), nil
		}
		out := append([]string{}, lines[:header+1]...)
		out = append(out, entry)
		return finish(append(out, lines[header+1:]...)), nil
	}
	_, raw, _ := tomlLineKeyValue(code[keyLine])
	if tomlArrayDelta(raw) != 0 || !strings.HasPrefix(raw, "[") {
		return "", fmt.Errorf("[%s] %s is not a one-line array; add %q to it by hand", table, key, value)
	}
	var values []string
	has := false
	for _, v := range tomlStringValues(raw) {
		if v == value {
			has = true
			if remove {
				continue
			}
		}
		values = append(values, v)
	}
	if !remove && has || remove && !has {
		return finish(lines), nil
	}
	if !remove {
		values = append(values, value)
	}
	if remove && len(values) == 0 {
		out := append([]string{}, lines[:keyLine]...)
		out = append(out, lines[keyLine+1:]...)
		// A table deja added and that now holds nothing goes with the key.
		if keyLine == header+1 && (keyLine+1 >= end || strings.TrimSpace(lines[keyLine+1]) == "" || strings.HasPrefix(tomlCode(code[keyLine+1]), "[")) {
			out = append(append([]string{}, lines[:header]...), lines[keyLine+1:]...)
			for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
				out = out[:len(out)-1]
			}
		}
		return finish(out), nil
	}
	lines[keyLine] = key + " = " + tomlStringArray(values)
	return finish(lines), nil
}

// codewhaleHookMarker opens each of deja's [[hooks.hooks]] entries, so
// removal takes deja's and leaves the reader's own.
const codewhaleHookMarker = "# deja: auto-recall (managed by `deja install codewhale-auto`)"

// codewhaleHookEvents are the events deja answers. message_submit carries the
// digest and the prompt's recall, tool_call_before the line about a file or a
// command, and session_end ends the session's live stamp. tool_call_after's
// stdout is discarded, so it parks a failed command's fix pair for the next of
// the first two. CodeWhale has no compaction event.
var codewhaleHookEvents = []string{"message_submit", "tool_call_before", "tool_call_after", "session_end"}

func installCodeWhaleAuto(exe string, uninstall bool) (installResult, error) {
	base, err := installCodeWhale(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hooks, err := installCodeWhaleHooks(hookExeFor(exe, uninstall), uninstall)
	if err != nil {
		return installResult{}, err
	}
	out := wroteAll(base, hooks)
	if !uninstall {
		out.Note = joinNotes(out.Note, "the hooks run in CodeWhale's TUI; `codewhale exec` and its servers fire none")
	}
	return out, nil
}

func installCodeWhaleHooks(exe string, uninstall bool) (installResult, error) {
	path := codewhaleConfigPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	if !uninstall {
		if err := tomlInlineKey(lfText(old), "[[hooks.hooks]]"); err != nil {
			return installResult{}, configParseError(path, err)
		}
	}
	s := strings.TrimRight(removeMarkedTOMLBlocks(lfText(old), codewhaleHookMarker), "\n")
	if !uninstall {
		var blocks []string
		for _, ev := range codewhaleHookEvents {
			blocks = append(blocks, codewhaleHookMarker+"\n[[hooks.hooks]]\nname = "+strconv.Quote("deja-"+strings.ReplaceAll(ev, "_", "-"))+
				"\nevent = "+strconv.Quote(ev)+"\ncommand = "+strconv.Quote(hookRun(exe, "hook-codewhale", ev))+"\ntimeout_secs = 30\n")
		}
		if s != "" {
			s += "\n\n"
		}
		s += strings.Join(blocks, "\n")
	} else if s != "" {
		s += "\n"
	}
	if uninstall && len(old) == 0 {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	a, err := writeIfChanged(path, old, []byte(s))
	return installResult{Path: path, Action: a}, err
}

// removeMarkedTOMLBlocks drops every block that opens with marker, up to the
// next table header or comment, the way removeKimiHookBlock does for kimi.
func removeMarkedTOMLBlocks(s, marker string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != marker {
			out = append(out, lines[i])
			continue
		}
		i++
		if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "[[") {
			i++
		}
		for i < len(lines) {
			t := strings.TrimSpace(lines[i])
			if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "#") {
				break
			}
			i++
		}
		i--
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

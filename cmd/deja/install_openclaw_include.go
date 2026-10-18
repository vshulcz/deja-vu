package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenClaw lets a section of openclaw.json live in a file of its own:
// `"mcp": {"$include": "./mcp.json5"}`. Its own writes go through to that
// file while the section holds only the $include, and stop doing so once a
// key sits beside it: from then on they land in openclaw.json as overrides.
// deja wrote its entry beside the $include, so `openclaw config set` and
// `openclaw mcp add` stopped reaching the included file, and stayed that way
// after uninstall (#5258). deja writes neither into a file the config includes
// nor beside one; it says what to add and where.

// openclawSection is one place openclaw-auto writes: the keys down to the
// object deja edits, and what deja would put there.
type openclawSection struct {
	keys []string
	want string
}

// openclawIncludeRefusal is the error for the sections, of those given, that
// openclaw.json keeps in an included file, or nil. A config that does not
// parse is left to the writers, which say so in their own words.
func openclawIncludeRefusal(sections ...openclawSection) error {
	path := openclawConfigPath()
	old, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(old)) == 0 {
		return nil
	}
	var root map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(old))), &root) != nil {
		return nil
	}
	var lines []string
	for _, s := range sections {
		at, inc := openclawIncludeOn(root, s.keys)
		if at == "" {
			continue
		}
		rest := strings.Join(s.keys[strings.Count(at, ".")+1:], ".")
		where := openclawIncludeFiles(path, inc)
		if rest != "" {
			lines = append(lines, fmt.Sprintf("%s comes from %s; add under %s there: %s", at, where, rest, s.want))
		} else {
			lines = append(lines, fmt.Sprintf("%s comes from %s; add there: %s", at, where, s.want))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return fmt.Errorf("%s keeps a section deja writes in an included file ($include), and deja writes neither into one nor beside it — a key beside the $include turns OpenClaw's own edits of that section into overrides in openclaw.json. "+
		"Put the section's contents back in openclaw.json in place of the $include and run this again, or add deja by hand: %s", shortHome(path), strings.Join(lines, "; "))
}

// openclawIncludeOn finds the first object on the path, below the root, that
// carries a $include: the dotted section and what it names.
func openclawIncludeOn(root map[string]any, keys []string) (string, any) {
	m := root
	for i, k := range keys {
		next, ok := m[k].(map[string]any)
		if !ok {
			return "", nil
		}
		if inc, ok := next["$include"]; ok {
			return strings.Join(keys[:i+1], "."), inc
		}
		m = next
	}
	return "", nil
}

// openclawIncludeFiles names what a $include points at, resolved against the
// config's directory the way OpenClaw resolves it.
func openclawIncludeFiles(config string, inc any) string {
	var names []string
	add := func(v any) {
		if s, ok := v.(string); ok && s != "" {
			if !filepath.IsAbs(s) {
				s = filepath.Join(filepath.Dir(config), s)
			}
			names = append(names, shortHome(s))
		}
	}
	if list, ok := inc.([]any); ok {
		for _, v := range list {
			add(v)
		}
	} else {
		add(inc)
	}
	if len(names) == 0 {
		return "an $include"
	}
	return strings.Join(names, ", ")
}

// openclawMCPSection is where `deja install openclaw` writes.
func openclawMCPSection(exe string) openclawSection {
	command, args := mcpCommandArgs(exe)
	entry, _ := json.Marshal(map[string]any{"command": command, "args": args})
	return openclawSection{keys: []string{"mcp", "servers"}, want: `"deja": ` + string(entry)}
}

// openclawAutoSections are the places openclaw-auto writes besides the MCP
// entry: the plugin's entry and load path, and the hook pack's switches.
func openclawAutoSections() []openclawSection {
	entry, _ := json.Marshal(map[string]any{"enabled": true, "hooks": map[string]any{openclawAccessKey: true}})
	paths, _ := json.Marshal([]string{openclawPluginDir()})
	return []openclawSection{
		{keys: []string{"plugins", "entries"}, want: `"` + openclawPluginID + `": ` + string(entry)},
		{keys: []string{"plugins", "load"}, want: `"paths": ` + string(paths) + ``},
		{keys: []string{"hooks", "internal"}, want: `"enabled": true, "entries": {"` + openclawHookName + `": {"enabled": true}}`},
	}
}

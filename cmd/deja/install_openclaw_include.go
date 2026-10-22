package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// OpenClaw lets a section of openclaw.json live in a file of its own:
// `"mcp": {"$include": "./mcp.json5"}`. Its own writes go through to that
// file while the section holds only the $include, and stop doing so once a
// key sits beside it: from then on they land in openclaw.json as overrides.
// deja wrote its entry beside the $include, so `openclaw config set` and
// `openclaw mcp add` stopped reaching the included file, and stayed that way
// after uninstall (#5258). deja writes neither into a file the config includes
// nor beside one; it says what to add and where, and writes the files of its
// own that those entries name, so following it gives a working wiring: the
// entries named a launcher, plugin and hook pack nothing had written, and a
// rerun refused again while the $include stayed (#5331).

// openclawSection is one place openclaw-auto writes: the keys down to the
// object deja edits, and what deja would put there.
type openclawSection struct {
	keys []string
	want string
}

// openclawIncludeRefuse is the refusal for openclaw (auto false) or
// openclaw-auto, or nil. Before refusing it writes deja's own files — the
// launcher, and for auto the plugin and hook pack — so every path the refusal
// names is there; openclaw.json and the files it includes stay untouched.
func openclawIncludeRefuse(exe string, auto bool) error {
	sections := func(hookExe string) []openclawSection {
		s := []openclawSection{openclawMCPSection(hookExe)}
		if auto {
			s = append(s, openclawAutoSections()...)
		}
		return s
	}
	if openclawIncludeRefusal(nil, sections(preflightHookExe(exe))...) == nil {
		return nil
	}
	hookExe := hookCommandExe(exe)
	wrote := []string{}
	if hookExe != exe {
		wrote = append(wrote, shortHome(hookExe))
	}
	if auto {
		if _, _, err := writeOpenClawPluginFiles(hookExe); err != nil {
			return err
		}
		if _, err := writeOpenClawHookFiles(hookExe); err != nil {
			return err
		}
		wrote = append(wrote, shortHome(openclawPluginDir()), shortHome(openclawHookDir()))
	}
	return openclawIncludeRefusal(wrote, sections(hookExe)...)
}

// openclawIncludeRefusal is the error for the sections, of those given, that
// openclaw.json keeps in an included file, or nil. Once one is, the others are
// named too, in openclaw.json, since nothing of the target is written there.
// wrote is the files of deja's that the entries run. A config that does not
// parse is left to the writers, which say so in their own words.
func openclawIncludeRefusal(wrote []string, sections ...openclawSection) error {
	path := openclawConfigPath()
	old, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(old)) == 0 {
		return nil //nolint:nilerr // an unreadable config is left to the writers
	}
	var root map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(old))), &root) != nil {
		return nil //nolint:nilerr // an unparsable config is left to the writers
	}
	var lines, beside []string
	for _, s := range sections {
		at, inc := openclawIncludeOn(root, s.keys)
		if at == "" {
			beside = append(beside, fmt.Sprintf("under %s in %s: %s", strings.Join(s.keys, "."), shortHome(path), s.want))
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
	lines = append(lines, beside...)
	files := ""
	if len(wrote) > 0 {
		files = " (deja wrote the files these run: " + strings.Join(wrote, ", ") + ")"
	}
	return fmt.Errorf("%s keeps a section deja writes in an included file ($include), and deja writes neither into one nor beside it — a key beside the $include turns OpenClaw's own edits of that section into overrides in openclaw.json. "+
		"Put the section's contents back in openclaw.json in place of the $include and run this again, or add deja by hand: %s%s", shortHome(path), strings.Join(lines, "; "), files)
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
	paths := openclawIncludePaths(filepath.Dir(config), inc)
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, shortHome(p))
	}
	if len(names) == 0 {
		return "an $include"
	}
	return strings.Join(names, ", ")
}

// openclawIncludePaths are the files a $include names, one or a list,
// relative ones against dir.
func openclawIncludePaths(dir string, inc any) []string {
	var out []string
	add := func(v any) {
		if s, ok := v.(string); ok && s != "" {
			if !filepath.IsAbs(s) {
				s = filepath.Join(dir, s)
			}
			out = append(out, s)
		}
	}
	if list, ok := inc.([]any); ok {
		for _, v := range list {
			add(v)
		}
	} else {
		add(inc)
	}
	return out
}

// openclawConfigResolved is openclaw.json with every $include read in, the
// way OpenClaw merges them: the files in order, then the keys beside. Doctor
// reads this, so entries added by hand to an included file count. complete is
// false when an included file could not be read as JSON with comments —
// OpenClaw takes JSON5 — and the merged view may be missing what it holds.
func openclawConfigResolved() (root map[string]any, complete bool) {
	path := openclawConfigPath()
	b, err := readConfig(path)
	if err != nil {
		return nil, true
	}
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) != nil {
		return nil, true
	}
	complete = true
	resolved := openclawResolveIncludes(root, filepath.Dir(path), 0, &complete)
	return resolved, complete
}

// doctorReadConfig is readConfig for doctor's readers of a client config:
// openclaw.json comes back with its $include files read in, so an entry or
// switch the user keeps in an included file is seen where OpenClaw sees it
// (#5339). An included file that does not read leaves the merged view
// without it, which still holds everything openclaw.json says itself. Every
// other file, and an openclaw.json without $include, is read as it is.
func doctorReadConfig(path string) ([]byte, error) {
	b, err := readConfig(path)
	if err != nil || path != openclawConfigPath() || !bytes.Contains(b, []byte("$include")) {
		return b, err
	}
	root, _ := openclawConfigResolved()
	if root == nil {
		return b, nil
	}
	out, err := json.Marshal(root)
	if err != nil {
		return b, nil //nolint:nilerr // the bytes go back as read
	}
	return out, nil
}

// openclawDoctorRoot is openclaw.json as doctor reads it, $include files and
// all, or nil.
func openclawDoctorRoot() map[string]any {
	b, err := doctorReadConfig(openclawConfigPath())
	if err != nil {
		return nil
	}
	m, _ := parseJSONValue(b).(map[string]any)
	return m
}

// openclawFileFor is the file a doctor line names for a key: openclaw.json
// when the key is set there, else the last file the key's section includes
// that sets it — the one whose value wins — or, when none does, the section's
// only included file, where the key would go.
func openclawFileFor(keys ...string) string {
	path := openclawConfigPath()
	raw := readJSONConfig(path)
	if jsonAt(raw, keys...) != nil {
		return path
	}
	at, inc := openclawIncludeOn(raw, keys)
	if at == "" {
		return path
	}
	rest := keys[strings.Count(at, ".")+1:]
	ps := openclawIncludePaths(filepath.Dir(path), inc)
	for _, p := range slices.Backward(ps) {
		if len(rest) > 0 && jsonAt(readJSONConfig(p), rest...) != nil {
			return p
		}
	}
	if len(ps) == 1 {
		return ps[0]
	}
	return path
}

func openclawResolveIncludes(m map[string]any, dir string, depth int, complete *bool) map[string]any {
	out := map[string]any{}
	if inc, ok := m["$include"]; ok {
		for _, p := range openclawIncludePaths(dir, inc) {
			var v map[string]any
			b, err := os.ReadFile(p)
			if depth > 8 || err != nil || json.Unmarshal([]byte(jsoncToJSON(string(b))), &v) != nil {
				*complete = false
				continue
			}
			openclawMergeInto(out, openclawResolveIncludes(v, filepath.Dir(p), depth+1, complete))
		}
	}
	for k, v := range m {
		if k == "$include" {
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			v = openclawResolveIncludes(sub, dir, depth, complete)
		}
		openclawMergeInto(out, map[string]any{k: v})
	}
	return out
}

// openclawMergeInto lays src over dst, objects merged key by key.
func openclawMergeInto(dst, src map[string]any) {
	for k, v := range src {
		a, aok := dst[k].(map[string]any)
		b, bok := v.(map[string]any)
		if aok && bok {
			openclawMergeInto(a, b)
			continue
		}
		dst[k] = v
	}
}

// openclawIncludedWiringMissing is, when openclaw.json keeps a section
// openclaw-auto writes in an included file — and so the install was refused — the entries of the plugin and hook
// pack that are in neither file yet: the install wrote deja's own files and
// left these to the reader, so the files alone do not make it wired. "" when
// nothing is included, or when an included file could not be read.
func openclawIncludedWiringMissing() string {
	raw := readJSONConfig(openclawConfigPath())
	included := false
	for _, s := range append(openclawAutoSections(), openclawSection{keys: []string{"mcp", "servers"}}) {
		if at, _ := openclawIncludeOn(raw, s.keys); at != "" {
			included = true
		}
	}
	if !included {
		return ""
	}
	root, complete := openclawConfigResolved()
	if !complete {
		return ""
	}
	var missing []string
	if jsonAt(root, "plugins", "entries", openclawPluginID) == nil {
		missing = append(missing, "plugins.entries."+openclawPluginID)
	}
	if jsonAt(root, "hooks", "internal", "entries", openclawHookName) == nil {
		missing = append(missing, "hooks.internal.entries."+openclawHookName)
	} else if jsonAt(root, "hooks", "internal", "enabled") == nil {
		missing = append(missing, "hooks.internal.enabled")
	}
	return strings.Join(missing, ", ")
}

// openclawMCPSection is where `deja install openclaw` writes.
func openclawMCPSection(exe string) openclawSection {
	command, args := mcpCommandArgs(exe)
	entry, _ := json.Marshal(map[string]any{"command": command, "args": args}) //nolint:errchkjson // strings always marshal
	return openclawSection{keys: []string{"mcp", "servers"}, want: `"deja": ` + string(entry)}
}

// openclawAutoSections are the places openclaw-auto writes besides the MCP
// entry: the plugin's entry and load path, and the hook pack's switches.
func openclawAutoSections() []openclawSection {
	entry, _ := json.Marshal(map[string]any{"enabled": true, "hooks": map[string]any{openclawAccessKey: true}}) //nolint:errchkjson // bools always marshal
	paths, _ := json.Marshal([]string{openclawPluginDir()})                                                     //nolint:errchkjson // strings always marshal
	return []openclawSection{
		{keys: []string{"plugins", "entries"}, want: `"` + openclawPluginID + `": ` + string(entry)},
		{keys: []string{"plugins", "load"}, want: `"paths": ` + string(paths) + ``},
		{keys: []string{"hooks", "internal"}, want: `"enabled": true, "entries": {"` + openclawHookName + `": {"enabled": true}}`},
	}
}

// openclawIncludedLeftNote names deja's entries an uninstall leaves in files
// openclaw.json includes — added there by hand, as the install asked, and
// pointing at files the uninstall just took away: the MCP entry, and for
// openclaw-auto the plugin's and the hook pack's.
func openclawIncludedLeftNote(auto bool) string {
	raw := readJSONConfig(openclawConfigPath())
	root, _ := openclawConfigResolved()
	type left struct {
		keys []string
		has  bool
	}
	checks := []left{{[]string{"mcp", "servers", "deja"}, jsonAt(root, "mcp", "servers", "deja") != nil}}
	if auto {
		checks = append(checks, []left{
			{[]string{"plugins", "entries", openclawPluginID}, jsonAt(root, "plugins", "entries", openclawPluginID) != nil},
			{[]string{"plugins", "load", "paths"}, slices.Contains(jsonStrings(jsonAt(root, "plugins", "load", "paths")), openclawPluginDir())},
			{[]string{"hooks", "internal", "entries", openclawHookName}, jsonAt(root, "hooks", "internal", "entries", openclawHookName) != nil},
		}...)
	}
	var out []string
	for _, c := range checks {
		at, inc := openclawIncludeOn(raw, c.keys)
		if at == "" || !c.has {
			continue
		}
		out = append(out, strings.Join(c.keys, ".")+" in "+openclawIncludeFiles(openclawConfigPath(), inc))
	}
	if len(out) == 0 {
		return ""
	}
	return "left deja's entries in the files openclaw.json includes, which deja does not edit — take them out there: " + strings.Join(out, "; ")
}

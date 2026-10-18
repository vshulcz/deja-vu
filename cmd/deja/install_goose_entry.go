package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// gooseSwitchOff reports whether goose skips an extension for this `enabled:`
// value. goose reads it through serde_yaml into a bool, and YAML 1.2 has three
// spellings of false; `disabled:` is not a field goose has (#5192).
func gooseSwitchOff(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "false", "False", "FALSE":
		return true
	}
	return false
}

// gooseDejaEntries is every deja key under goose's `extensions:`, spelled any
// way YAML reads it.
func gooseDejaEntries(lines []string) []hermesEntry {
	return yamlDejaKeyEntries(lines, "extensions:")
}

// gooseMCPOffNote is doctor's line for a deja extension goose will not start.
func gooseMCPOffNote(p string) string {
	b, err := readConfig(p)
	if err != nil {
		return ""
	}
	lines := strings.Split(lfText(b), "\n")
	entries := gooseDejaEntries(lines)
	if len(entries) != 1 {
		return ""
	}
	v, ok := hermesEntryEnabled(lines, entries[0])
	if !ok || !gooseSwitchOff(v) {
		return ""
	}
	return mcpOffNote("goose", "`extensions.deja.enabled: "+v+"`", p)
}

// gooseEditEntry points the deja extension the reader already has at this
// binary, in place: `cmd` and `args` change when they say something else,
// `type` and `enabled` are added when missing, and every other key — envs, a
// timeout, a tool filter, the reader's switch — stays. Rebuilding the entry
// whole dropped them (#5191), and matching only a bare `deja:` wrote a second
// key beside a quoted one, which goose refuses along with the whole file
// (#5190). done is false when there is no entry to edit.
func gooseEditEntry(lines []string, exe string) (out []string, note string, done bool, err error) {
	entries := gooseDejaEntries(lines)
	if len(entries) > 1 {
		// The shape #5190 left: deja's own block beside the reader's. goose
		// read neither; keep the reader's.
		var keep []hermesEntry
		for _, e := range entries {
			if !gooseGeneratedEntry(lines, e) {
				keep = append(keep, e)
			}
		}
		if len(keep) == 1 {
			lines = append([]string(nil), lines...)
			for i := len(entries) - 1; i >= 0; i-- {
				if entries[i] != keep[0] {
					lines = append(lines[:entries[i].start], lines[entries[i].end:]...)
				}
			}
			entries = gooseDejaEntries(lines)
		}
	}
	switch {
	case len(entries) == 0:
		return lines, "", false, nil
	case len(entries) > 1:
		return nil, "", false, fmt.Errorf("extensions.deja is written %d times, and goose refuses a config that names a key twice — keep one and run this again", len(entries))
	}
	e := entries[0]
	cmd, args := mcpCommandArgs(exe)
	if v, ok := hermesEntryEnabled(lines, e); ok && gooseSwitchOff(v) {
		note = switchedOffNote
	}
	if e.inline != "" {
		m, ok := hermesFlowMap(e.inline)
		if ok && len(m) == 0 || e.inline == "~" || strings.EqualFold(e.inline, "null") {
			return nil, "", false, nil
		}
		if ok && yamlScalar(m["cmd"]) == cmd && gooseSameArgs(hermesArgs(nil, hermesField{value: m["args"]}), args) {
			return lines, note, true, nil
		}
		return nil, "", false, fmt.Errorf("extensions.deja is written on one line (%s), and deja edits only the block form — rewrite it that way and run this again", strings.TrimSpace(lines[e.start]))
	}
	if !hermesSpanPlain(lines, e.start, e.end) {
		return nil, "", false, fmt.Errorf("extensions.deja holds a value that runs over several lines (a flow collection or a quoted string), and deja edits only one-line values — rewrite it that way and run this again")
	}
	fields, indent := hermesFields(lines, e)
	if len(fields) == 0 {
		return nil, "", false, nil
	}
	argsField, hasArgs := fields["args"]
	cmdField, hasCmd := fields["cmd"]
	for _, f := range []hermesField{argsField, cmdField} {
		if v := f.value; strings.HasPrefix(v, "[") && !strings.HasSuffix(v, "]") || strings.HasPrefix(v, "{") && !strings.HasSuffix(v, "}") {
			return nil, "", false, fmt.Errorf("extensions.deja has a flow value over several lines (%s), and deja edits only one-line or block values — rewrite it that way and run this again", strings.TrimSpace(lines[f.start]))
		}
	}
	type edit struct {
		from, to int
		with     []string
	}
	var edits []edit
	// Items at the indent the reader's own list uses: goose writes them at
	// the key's, a hand-written file further in.
	item := indent + "  - "
	if hasArgs {
		// The first item, not a blank or a comment at column 0 before it:
		// an item written there ends the entry, and goose refuses the file.
		for _, l := range lines[argsField.start+1 : argsField.end] {
			if !hermesNoContent(l) {
				item = l[:yamlIndentWidth(l)] + "- "
				break
			}
		}
	}
	argLines := []string{indent + "args:"}
	for _, a := range args {
		argLines = append(argLines, item+yamlQuote(a))
	}
	cmdLine := indent + "cmd: " + yamlQuote(cmd)
	switch {
	case !hasArgs:
		at := e.end
		if hasCmd {
			at = cmdField.end
		}
		edits = append(edits, edit{at, at, argLines})
	case !gooseSameArgs(hermesArgs(lines, argsField), args):
		edits = append(edits, edit{argsField.start, argsField.end, argLines})
	}
	switch {
	case !hasCmd:
		edits = append(edits, edit{e.start + 1, e.start + 1, []string{cmdLine}})
	case yamlScalar(cmdField.value) != cmd:
		edits = append(edits, edit{cmdField.start, cmdField.end, []string{cmdLine}})
	}
	// goose skips an entry without `enabled` or `type` (both required by its
	// ExtensionEntry); one that runs deja is a stdio server.
	if t, ok := fields["type"]; !ok {
		edits = append(edits, edit{e.start + 1, e.start + 1, []string{indent + "type: stdio"}})
	} else if yamlScalar(t.value) != "stdio" {
		edits = append(edits, edit{t.start, t.end, []string{indent + "type: stdio"}})
	}
	if _, ok := fields["enabled"]; !ok {
		edits = append(edits, edit{e.start + 1, e.start + 1, []string{indent + "enabled: true"}})
	}
	// A replace and an insert at one line: the replace goes first, or it
	// lands on the inserted line and the field it meant to fix stays.
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].from != edits[j].from {
			return edits[i].from > edits[j].from
		}
		return edits[i].to > edits[j].to
	})
	out = append([]string(nil), lines...)
	for _, ed := range edits {
		out = append(out[:ed.from], append(append([]string(nil), ed.with...), out[ed.to:]...)...)
	}
	return out, note, true, nil
}

// gooseGeneratedEntry reports an entry exactly as deja's fresh writer leaves
// it: enabled, type stdio, name deja, cmd, args and timeout, nothing else.
func gooseGeneratedEntry(lines []string, e hermesEntry) bool {
	if e.inline != "" {
		return false
	}
	fields, _ := hermesFields(lines, e)
	for _, k := range []string{"enabled", "type", "name", "cmd", "args", "timeout"} {
		if _, ok := fields[k]; !ok {
			return false
		}
	}
	return len(fields) == 6 && yamlScalar(fields["type"].value) == "stdio" && yamlScalar(fields["name"].value) == "deja" &&
		fields["timeout"].value == "60"
}

func gooseSameArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// removeGooseExtensionEntries drops every deja key under `extensions:` that
// the bare-line remover left: a quoted key, one with a comment, a flow value.
func removeGooseExtensionEntries(s string) string {
	lines := strings.Split(s, "\n")
	entries := gooseDejaEntries(lines)
	for i := len(entries) - 1; i >= 0; i-- {
		lines = append(lines[:entries[i].start], lines[entries[i].end:]...)
	}
	return strings.Join(lines, "\n")
}

// gooseDejaSlashCommandPaths is the recipe_path of each `slash_commands:`
// item whose command is deja, read by value: goose saves the file through
// serde_yaml, which drops the quotes deja wrote and puts the items at the
// key's indent, and a match on deja's own text then called its entry someone
// else's and rewrote it (#5193).
func gooseDejaSlashCommandPaths(s string) []string {
	lines := strings.Split(s, "\n")
	var out []string
	in := false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(stripYAMLComment(l))
		if t == "" {
			continue
		}
		w := yamlIndentWidth(l)
		if w == 0 && !strings.HasPrefix(t, "-") {
			in = yamlKeyLine(l, "slash_commands:")
			continue
		}
		rest, ok := strings.CutPrefix(t, "- ")
		if !in || !ok {
			continue
		}
		path, found := "", false
		read := func(line string) {
			key, val, ok := hermesKeyLine(line)
			if !ok {
				return
			}
			switch hermesKeyName(key) {
			case "command":
				found = yamlScalar(strings.TrimSpace(stripYAMLComment(val))) == "deja"
			case "recipe_path":
				path = yamlScalar(strings.TrimSpace(stripYAMLComment(val)))
			}
		}
		read(rest)
		j := i + 1
		for ; j < len(lines); j++ {
			lt := strings.TrimSpace(lines[j])
			if lt == "" || strings.HasPrefix(lt, "#") {
				continue
			}
			if yamlIndentWidth(lines[j]) <= w {
				break
			}
			read(lines[j])
		}
		if found {
			out = append(out, path)
		}
		i = j - 1
	}
	return out
}

// gooseSlashCommandOurs reports whether config.yaml holds deja's /deja, one
// item, pointing at this recipe.
func gooseSlashCommandOurs(s, recipe string) (found, ours bool) {
	paths := gooseDejaSlashCommandPaths(s)
	for _, p := range paths {
		if p != "" && filepath.Clean(p) == filepath.Clean(recipe) {
			ours = true
		}
	}
	return len(paths) > 0, ours && len(paths) == 1
}

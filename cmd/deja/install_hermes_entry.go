package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// hermesEntry is one `deja` key under the top-level `mcp_servers:` of a Hermes
// config.yaml: lines[start] is the key, lines[start+1:end] its body, and
// inline the value on the key's own line, comment off ("" for a block).
type hermesEntry struct {
	start, end int
	pad        string
	inline     string
}

// hermesDejaEntries finds every `deja` key at the servers' indent, spelled
// any way YAML reads it: quoted, with a space before the colon, a comment
// after it, or a flow value on the same line. Matching only a bare `deja:`
// line missed those, and install wrote a second `deja:` above the reader's
// (#5172).
func hermesDejaEntries(lines []string) []hermesEntry {
	return yamlDejaKeyEntries(lines, "mcp_servers:")
}

// yamlDejaKeyEntries is hermesDejaEntries under any top-level key: goose
// keeps its servers under `extensions:` and refuses a file that names one
// twice (#5190).
func yamlDejaKeyEntries(lines []string, topKey string) []hermesEntry {
	var out []hermesEntry
	inBlock, child := false, -1
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimSpace(line)
		// A comment is at whatever indent its writer liked, so it neither
		// ends the block nor sets the servers' indent (#4289).
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		w := yamlIndentWidth(line)
		if w == 0 {
			inBlock, child = yamlKeyLine(line, topKey), -1
			continue
		}
		if !inBlock {
			continue
		}
		if child < 0 {
			child = w
		}
		if w != child {
			continue
		}
		key, rest, ok := hermesKeyLine(line)
		if !ok || hermesKeyName(key) != "deja" {
			continue
		}
		e := hermesEntry{start: i, pad: line[:w], inline: strings.TrimSpace(stripYAMLComment(rest))}
		e.end = hermesBodyEnd(lines, i, w, false)
		out = append(out, e)
		i = e.end - 1
	}
	return out
}

// hermesBodyEnd is the line after the body of the key at lines[at], indented
// w. A blank or comment line is inside the body only if the body goes on
// after it: a comment sits at whatever column its editor put it, and the
// blank that separates the block from what follows belongs to the file. With
// items, a `- x` at the key's own indent is body too, as YAML allows for a
// list under a mapping key.
func hermesBodyEnd(lines []string, at, w int, items bool) int {
	in := func(line string) bool {
		lw := yamlIndentWidth(line)
		return lw > w || items && lw == w && strings.HasPrefix(strings.TrimSpace(line), "-")
	}
	j := at + 1
	for j < len(lines) {
		if hermesNoContent(lines[j]) {
			k := j
			for k < len(lines) && hermesNoContent(lines[k]) {
				k++
			}
			if k < len(lines) && in(lines[k]) {
				j = k
				continue
			}
			break
		}
		if !in(lines[j]) {
			break
		}
		j++
	}
	return j
}

func hermesNoContent(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// hermesField is a key directly in deja's block entry.
type hermesField struct {
	start, end int
	value      string
}

// hermesFields reads the keys of a block entry at its field indent, and that
// indent ("" when the body holds none).
func hermesFields(lines []string, e hermesEntry) (map[string]hermesField, string) {
	fields := map[string]hermesField{}
	indent := ""
	for i := e.start + 1; i < e.end; i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		w := yamlIndentWidth(lines[i])
		if indent == "" {
			indent = lines[i][:w]
		}
		if w != len(indent) {
			continue
		}
		key, rest, ok := hermesKeyLine(lines[i])
		if !ok {
			continue
		}
		end := hermesBodyEnd(lines, i, w, true)
		// The last one, the one YAML keeps.
		fields[hermesKeyName(key)] = hermesField{start: i, end: end, value: strings.TrimSpace(stripYAMLComment(rest))}
		i = end - 1
	}
	return fields, indent
}

// hermesArgs is the list an `args:` holds, flow or block.
func hermesArgs(lines []string, f hermesField) []string {
	if strings.HasPrefix(f.value, "[") && strings.HasSuffix(f.value, "]") {
		var out []string
		for _, item := range hermesFlowSplit(f.value[1 : len(f.value)-1]) {
			out = append(out, yamlScalar(item))
		}
		return out
	}
	if f.value != "" {
		// A string, not the list Hermes passes as argv.
		return nil
	}
	var out []string
	for i := f.start + 1; i < f.end; i++ {
		t := strings.TrimSpace(stripYAMLComment(lines[i]))
		if t == "" {
			continue
		}
		if t != "-" && !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "-\t") {
			// `-mcp` is the string "-mcp", not an item.
			return nil
		}
		out = append(out, yamlScalar(strings.TrimSpace(t[1:])))
	}
	return out
}

// hermesFlowSplit splits the inside of a flow collection at its own commas,
// not those inside quotes or a nested collection.
func hermesFlowSplit(s string) []string {
	var out []string
	depth, quote, from := 0, byte(0), 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[from:i]))
			from = i + 1
		}
	}
	if t := strings.TrimSpace(s[from:]); t != "" {
		out = append(out, t)
	}
	return out
}

// hermesFlowMap reads a one-line flow mapping, `{command: x, args: [mcp]}`.
func hermesFlowMap(v string) (map[string]string, bool) {
	if !strings.HasPrefix(v, "{") || !strings.HasSuffix(v, "}") {
		return nil, false
	}
	m := map[string]string{}
	for _, pair := range hermesFlowSplit(v[1 : len(v)-1]) {
		key, rest, ok := hermesKeyLine(pair)
		if !ok {
			return nil, false
		}
		m[hermesKeyName(key)] = strings.TrimSpace(rest)
	}
	return m, true
}

// hermesSwitchOff reports whether Hermes will not start a server for this
// `enabled:` value. PyYAML reads false/no/off (any case) as false, and Hermes'
// own parser takes the strings "false", "0", "no" and "off" the same way; a
// value outside those it starts. Reading only `false` turned `no` and `off`
// back on (#5171). `hermes mcp list` also lists 0, ~ and other strings as
// disabled, but the runtime starts them, so they are not called off here.
func hermesSwitchOff(raw string) bool {
	v := strings.TrimSpace(raw)
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		switch strings.ToLower(strings.TrimSpace(yamlScalar(v))) {
		case "false", "0", "no", "off":
			return true
		}
		return false
	}
	switch strings.ToLower(v) {
	case "false", "no", "off":
		return true
	}
	return false
}

// hermesEntryEnabled is the `enabled:` deja's entry carries, and whether it
// carries one. `disabled:` is goose's spelling; Hermes does not read it.
func hermesEntryEnabled(lines []string, e hermesEntry) (string, bool) {
	if m, ok := hermesFlowMap(e.inline); ok {
		v, ok := m["enabled"]
		return v, ok
	}
	if e.inline != "" {
		return "", false
	}
	fields, _ := hermesFields(lines, e)
	f, ok := fields["enabled"]
	return f.value, ok
}

// hermesMCPOffNote is doctor's line for a deja entry Hermes will not start.
func hermesMCPOffNote(p string) string {
	b, err := readConfig(p)
	if err != nil {
		return ""
	}
	lines := strings.Split(lfText(b), "\n")
	entries := hermesDejaEntries(lines)
	if len(entries) == 0 {
		return ""
	}
	v, ok := hermesEntryEnabled(lines, entries[len(entries)-1])
	if !ok || !hermesSwitchOff(v) {
		return ""
	}
	return mcpOffNote("hermes", "`mcp_servers.deja.enabled: "+v+"`", p)
}

// hermesEditEntry points the deja entry the reader already has at this
// binary, in place: `command` and `args` change when they say something
// else, and every other key — timeout, env, tools, the reader's own switch —
// stays. Rebuilding the entry whole dropped them (#5173). done is false when
// there is no entry to edit; a flow entry that runs something else, and a
// key written twice, are refused rather than guessed at.
//
// A one-line entry that needs an edit is first written out as a block with
// the same keys, values and comment, in their order (#5338).
func hermesEditEntry(lines []string, exe string) (out []string, note string, done bool, err error) {
	entries := hermesDejaEntries(lines)
	if len(entries) > 1 {
		// The shape #5172 left: deja's own block above the reader's. Drop
		// the copies deja wrote and edit the one Hermes reads.
		var keep []hermesEntry
		for _, e := range entries {
			if !hermesGeneratedEntry(lines, e) {
				keep = append(keep, e)
			}
		}
		// Only when the generated copies sit above it: the last key is the
		// one Hermes reads, and dropping that one would change what runs.
		if len(keep) == 1 && keep[0] == entries[len(entries)-1] {
			lines = append([]string(nil), lines...)
			for i := len(entries) - 1; i >= 0; i-- {
				if entries[i] != keep[0] {
					lines = append(lines[:entries[i].start], lines[entries[i].end:]...)
				}
			}
			entries = hermesDejaEntries(lines)
		}
	}
	switch {
	case len(entries) == 0:
		return lines, "", false, nil
	case len(entries) > 1:
		return nil, "", false, fmt.Errorf("mcp_servers.deja is written %d times, and Hermes reads only the last — keep one and run this again", len(entries))
	}
	e := entries[0]
	if v, ok := hermesEntryEnabled(lines, e); ok && hermesSwitchOff(v) {
		note = switchedOffNote
	}
	if e.inline != "" {
		m, ok := hermesFlowMap(e.inline)
		if ok && len(m) == 0 || e.inline == "~" || strings.EqualFold(e.inline, "null") {
			// Nothing of the reader's in it: written as deja writes it.
			return nil, "", false, nil
		}
		if ok && yamlScalar(m["command"]) == exe && hermesArgsSay(hermesArgs(nil, hermesField{value: m["args"]})) {
			return lines, note, true, nil
		}
		// Anything else is edited the way the block form is, once written as
		// one: a one-line entry naming the binary kept deja mcp on Hermes'
		// stripped env, away from DEJA_INDEX_DIR (#5338).
		block, ok := hermesFlowToBlock(lines, e)
		if !ok {
			return nil, "", false, fmt.Errorf("mcp_servers.deja is written on one line (%s), and deja edits only the block form — rewrite it that way and run this again", strings.TrimSpace(lines[e.start]))
		}
		out, n, done, err := hermesEditEntry(block, exe)
		if err == nil && done {
			n = strings.TrimPrefix(n+"; ", "; ") + hermesFlowRewrittenNote
		}
		return out, n, done, err
	}
	if !hermesSpanPlain(lines, e.start, e.end) {
		return nil, "", false, fmt.Errorf("mcp_servers.deja holds a value that runs over several lines (a flow collection or a quoted string), and deja edits only one-line values — rewrite it that way and run this again")
	}
	fields, indent := hermesFields(lines, e)
	if len(fields) == 0 {
		// A bare `deja:`, or a body deja cannot read keys in.
		return nil, "", false, nil
	}
	if indent == "" {
		indent = e.pad + e.pad
	}
	step := indent[len(e.pad):]
	if step == "" {
		step = "  "
	}
	args, hasArgs := fields["args"]
	cmd, hasCmd := fields["command"]
	for _, f := range []hermesField{args, cmd} {
		if v := f.value; strings.HasPrefix(v, "[") && !strings.HasSuffix(v, "]") || strings.HasPrefix(v, "{") && !strings.HasSuffix(v, "}") {
			return nil, "", false, fmt.Errorf("mcp_servers.deja has a flow value over several lines (%s), and deja edits only one-line or block values — rewrite it that way and run this again", strings.TrimSpace(lines[f.start]))
		}
	}
	// Each edit replaces a span of the original lines; applied from the last
	// span back, so the earlier line numbers hold whatever the key order.
	type edit struct {
		from, to int
		with     []string
	}
	var edits []edit
	argLines := []string{indent + "args:", indent + step + "- mcp"}
	cmdLine := indent + "command: " + yamlQuote(exe)
	switch {
	case !hasArgs:
		at := e.start + 1
		if hasCmd {
			at = cmd.end
		}
		edits = append(edits, edit{at, at, argLines})
	case !hermesArgsSay(hermesArgs(lines, args)):
		edits = append(edits, edit{args.start, args.end, argLines})
	}
	switch {
	case !hasCmd:
		edits = append(edits, edit{e.start + 1, e.start + 1, []string{cmdLine}})
	case yamlScalar(cmd.value) != exe:
		// The whole value: a plain or folded one may go on below the key.
		edits = append(edits, edit{cmd.start, cmd.end, []string{cmdLine}})
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].from > edits[j].from })
	out = append([]string(nil), lines...)
	for _, ed := range edits {
		out = append(out[:ed.from], append(append([]string(nil), ed.with...), out[ed.to:]...)...)
	}
	return out, note, true, nil
}

// hermesGeneratedEntry reports an entry exactly as deja's fresh writer
// leaves it: command, args [mcp] and enabled: true, nothing else.
func hermesGeneratedEntry(lines []string, e hermesEntry) bool {
	if e.inline != "" {
		return false
	}
	fields, _ := hermesFields(lines, e)
	_, cmd := fields["command"]
	args, hasArgs := fields["args"]
	return len(fields) == 3 && cmd && hasArgs && hermesArgsSay(hermesArgs(lines, args)) && fields["enabled"].value == "true"
}

func hermesArgsSay(args []string) bool {
	return len(args) == 1 && args[0] == "mcp"
}

// hermesPluginsPreflight refuses before anything is written: the plugins
// half of hermes-auto checked its shape only after the MCP entry and the
// plugin directory were in place, so a refused target was left half wired
// (#5174).
func hermesPluginsPreflight() error {
	path := filepath.Join(sources.HermesHome(), "config.yaml")
	old, err := readConfig(path)
	if err != nil || len(old) == 0 || hermesPluginDisabled() {
		return err
	}
	e := hermesPluginsEdit{on: true}
	text := lfText(old)
	next, how := e.apply(text)
	if how == hermesPluginsUnreadable {
		return hermesPluginsShapeError(path)
	}
	// A file the edit would change has to be writable before the plugin
	// files go in, or a read-only config leaves the install half done.
	if next != text || how == hermesPluginsMissing {
		return configWritable(path)
	}
	return nil
}

func hermesPluginsShapeError(path string) error {
	return fmt.Errorf("%s: plugins.enabled is not written in a shape deja can edit safely; add deja to it by hand", path)
}

// hermesProviderLeftNote is said when uninstall took the provider away while
// memory.provider still names it: the key is the reader's, set by `hermes
// memory setup`, so it stays, and Hermes then reports the provider as not
// installed until they pick another (#5175).
func hermesProviderLeftNote() string {
	b, err := readConfig(filepath.Join(sources.HermesHome(), "config.yaml"))
	if err != nil {
		return ""
	}
	if v, _, _ := yamlLookup(lfText(b), "memory", "provider"); v != hermesMemoryDir {
		return ""
	}
	return "memory.provider still names " + hermesMemoryDir + ", which is gone now; `hermes memory off` goes back to built-in memory"
}

// hermesSpanPlain reports whether every line of an entry, and the line after
// it, closes the brackets and quotes it opens. Where it does not, YAML reads
// the value on across lines whatever their indent, and an edit by indent
// would cut it in two.
func hermesSpanPlain(lines []string, from, to int) bool {
	if to < len(lines) {
		to++
	}
	for _, line := range lines[from:to] {
		depth, quote := 0, byte(0)
		v := stripYAMLComment(line)
		for i := 0; i < len(v); i++ {
			c := v[i]
			switch {
			case quote == '"' && c == '\\':
				i++
			case quote != 0:
				if c == quote {
					quote = 0
				}
			case (c == '"' || c == '\'') && (i == 0 || strings.ContainsRune(" \t[{,", rune(v[i-1]))):
				quote = c
			case c == '[' || c == '{':
				depth++
			case c == ']' || c == '}':
				depth--
			}
		}
		if depth != 0 || quote != 0 {
			return false
		}
	}
	return true
}

// hermesEntriesRemovable refuses to take out a deja entry when doing so by
// indent would break the file: a value running over lines at any indent, or
// an anchor on the entry that something else still names.
func hermesEntriesRemovable(text string) error {
	return yamlDejaEntriesRemovable(text, "mcp_servers:")
}

func yamlDejaEntriesRemovable(text, topKey string) error {
	lines := strings.Split(text, "\n")
	name := strings.TrimSuffix(topKey, ":") + ".deja"
	for _, e := range yamlDejaKeyEntries(lines, topKey) {
		if !hermesSpanPlain(lines, e.start, e.end) {
			return fmt.Errorf("%s holds a value that runs over several lines, and taking it out by hand is safer — remove the deja entry and run this again", name)
		}
		_, rest, _ := hermesKeyLine(lines[e.start])
		for _, tok := range strings.Fields(stripYAMLComment(rest)) {
			if a, ok := strings.CutPrefix(tok, "&"); ok && a != "" && strings.Contains(text, "*"+a) {
				return fmt.Errorf("%s carries the anchor &%s, which *%s still uses — remove the deja entry by hand and run this again", name, a, a)
			}
		}
	}
	return nil
}

// hermesFlowRewrittenNote is what install says after writing a one-line entry
// out as a block: the reader's file changed shape, not only its command.
const hermesFlowRewrittenNote = "wrote mcp_servers.deja out as a block to point it at the launcher, which carries DEJA_INDEX_DIR; its keys and comment are kept"

// hermesFlowToBlock is lines with the one-line entry e, `deja: {k: v, …}`,
// written as the block Hermes reads the same: each key on its own line in
// order, the values as written, a trailing comment after `deja:`. false when
// the value is not a flow mapping deja reads whole, names a key twice, or
// lines other than comments follow it in its span.
func hermesFlowToBlock(lines []string, e hermesEntry) ([]string, bool) {
	v := e.inline
	if !strings.HasPrefix(v, "{") || !strings.HasSuffix(v, "}") {
		return nil, false
	}
	for _, l := range lines[e.start+1 : e.end] {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			return nil, false
		}
	}
	key, rest, ok := hermesKeyLine(lines[e.start])
	if !ok {
		return nil, false
	}
	head := e.pad + strings.TrimSpace(key) + ":"
	if c := strings.TrimSpace(rest[len(stripYAMLComment(rest)):]); c != "" {
		head += " " + c
	}
	indent := e.pad + e.pad
	if indent == "" {
		indent = "  "
	}
	block := []string{head}
	seen := map[string]bool{}
	for _, pair := range hermesFlowSplit(v[1 : len(v)-1]) {
		k, val, ok := hermesKeyLine(pair)
		// A key written twice is the reader's to settle, not deja's to pick.
		if !ok || seen[hermesKeyName(k)] {
			return nil, false
		}
		seen[hermesKeyName(k)] = true
		line := indent + strings.TrimSpace(k) + ":"
		if val = strings.TrimSpace(val); val != "" {
			line += " " + val
		}
		block = append(block, line)
	}
	out := append([]string(nil), lines[:e.start]...)
	out = append(out, block...)
	return append(out, lines[e.start+1:]...), true
}

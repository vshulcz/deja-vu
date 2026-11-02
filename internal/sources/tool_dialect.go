package sources

import "strings"

// toolDialect names one harness's tool-call vocabulary. The wire shape is the
// same everywhere — a `tool_use` part with a name and an input object — but the
// vocabulary is not, and it is not guessable: Claude names the file key
// `file_path` and its shell tool `Bash`, Cursor names them `path` and `Shell`.
// Both were read off transcripts the vendor's own CLI had just written.
type toolDialect struct {
	pathKey string
	// pathKeyAlt is a second name for the file argument, read when pathKey is
	// absent. Roo's newer edit tools take `file_path` where its older ones take
	// `path` (#4419). Empty means pathKey alone.
	pathKeyAlt string
	pathTools  map[string]bool
	shellTool  string
	// shellTools names every alias the shell tool answers to, when a harness
	// has more than one. CodeWhale's canonical name is exec_shell and it also
	// takes bash and Bash, so a run recorded under an alias was no command at
	// all. Empty means shellTool alone.
	shellTools map[string]bool
	// editTools bounds which calls carry a replaced span. Empty means any call
	// with a path and an old_string, which is how the Claude decoder has always
	// read MultiEdit's sub-edits.
	editTools map[string]bool
	// oldKey names the argument holding the text an edit replaced. Copilot
	// calls it old_str where Claude calls it old_string, and reading the wrong
	// one loses the only record of what stopped existing. Empty means
	// old_string.
	oldKey string
	// newKey names the argument holding the text an edit wrote, the mirror of
	// oldKey: cline calls it new_text, Copilot new_str. Empty means
	// new_string. A dialect whose key is not set records no written side —
	// nothing wrong, just nothing to attribute from.
	newKey string
	// newKeyAlt is a second name for the written text, read beside newKey:
	// Claude's NotebookEdit writes a cell under `new_source` (#4489). Empty
	// means newKey alone.
	newKeyAlt string
	// editsOldKey and editsNewKey name the same two sides inside each element
	// of an `edits` array, when they differ from oldKey and newKey: CodeWhale's
	// edit takes edits[{oldText,newText}] while its legacy edit_file takes
	// old_string (#4360). Empty means oldKey and newKey.
	editsOldKey, editsNewKey string
	// contentKey names the argument of a whole-file write. Empty means
	// "content". A commit that adds a file has no replaced text at all, so
	// this is the only evidence such a line was ever in a session.
	contentKey string
	// commandKey names the argument holding the command. Empty means
	// "command"; cline's run_commands takes "commands", a list.
	commandKey string
	// commandKeyAlt is a second name for it, read when commandKey is absent:
	// Amp's Bash takes `cmd` and its shell_command `command` (#4527). Empty
	// means commandKey alone.
	commandKeyAlt string
	// argsKey names an argument list that follows the command, the way the
	// client shows it: Command Code's shell_command {command: "go", args:
	// ["test", "./..."]} ran `go test ./...` (#4540). Empty means none.
	argsKey string
	// pathListKey names an argument holding several files at once — cline's
	// read_files takes "files", whose elements each name a path under pathKey.
	// Empty means a call names at most one file.
	pathListKey string
	// pathListGlobs says that list mixes globs with paths, as gemini's
	// read_many_files include does; a glob names no file the session
	// touched (#4494).
	pathListGlobs bool
}

// isShellTool reports whether a call is the shell, under any name the harness
// gives it.
func (d toolDialect) isShellTool(name string) bool {
	if len(d.shellTools) > 0 {
		return d.shellTools[name]
	}
	return name == d.shellTool
}

// oldSpanKey is oldKey with its default applied.
func (d toolDialect) oldSpanKey() string {
	if d.oldKey == "" {
		return "old_string"
	}
	return d.oldKey
}

func (d toolDialect) newSpanKey() string {
	if d.newKey == "" {
		return "new_string"
	}
	return d.newKey
}

func (d toolDialect) editsOldSpanKey() string {
	if d.editsOldKey == "" {
		return d.oldSpanKey()
	}
	return d.editsOldKey
}

func (d toolDialect) editsNewSpanKey() string {
	if d.editsNewKey == "" {
		return d.newSpanKey()
	}
	return d.editsNewKey
}

func (d toolDialect) contentSpanKey() string {
	if d.contentKey == "" {
		return "content"
	}
	return d.contentKey
}

var claudeDialect = toolDialect{
	pathKey:    "file_path",
	pathKeyAlt: "notebook_path",
	pathTools:  pathTools,
	shellTools: claudeShellTools,
	newKeyAlt:  "new_source",
}

// callPath is the file one call names, under the dialect's key or its
// second name.
func (d toolDialect) callPath(in map[string]any) string {
	if p, _ := in[d.pathKey].(string); p != "" {
		return p
	}
	if d.pathKeyAlt == "" {
		return ""
	}
	p, _ := in[d.pathKeyAlt].(string)
	return p
}

func toolPart(it any, _ toolDialect) (name string, in map[string]any, ok bool) {
	m, ok := it.(map[string]any)
	if !ok {
		return "", nil, false
	}
	if t, _ := m["type"].(string); t != "tool_use" {
		return "", nil, false
	}
	name, _ = m["name"].(string)
	in, _ = m["input"].(map[string]any)
	return name, in, in != nil
}

// toolPathsFromContent is claudeToolPaths for the reference parser, which walks
// decoded maps rather than raw JSON. The two must agree or the differential
// test in #502 stops meaning anything.
func toolPathsFromContent(v any) string { return toolPathsIn(v, claudeDialect) }

func toolPathsIn(v any, d toolDialect) string {
	items, ok := v.([]any)
	if !ok {
		return ""
	}
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		name, in, ok := toolPart(it, d)
		if !ok || !d.pathTools[name] {
			continue
		}
		for _, p := range toolPathStrings(in, d) {
			// The record is one path per line and the index splits it back on
			// the newline, so a path carrying one arrives as two files the
			// session never touched — which reaches the files listing, blame,
			// and the project a session is filed under (#2042).
			if seen[p] || strings.ContainsAny(p, "\n\r") {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// toolPathStrings pulls the paths out of one call. A call can name one file
// under the dialect's key, or carry a list of read requests — cline's
// read_files takes `files`, an array whose elements each name a path — and
// reading only the scalar key indexed none of those.
func toolPathStrings(in map[string]any, d toolDialect) []string {
	if p := d.callPath(in); p != "" {
		return []string{p}
	}
	if d.pathListKey == "" {
		return nil
	}
	items, _ := in[d.pathListKey].([]any)
	var out []string
	for _, it := range items {
		switch e := it.(type) {
		case string:
			if e != "" && (!d.pathListGlobs || !strings.ContainsAny(e, "*?[{")) {
				out = append(out, e)
			}
		case map[string]any:
			if p, _ := e[d.pathKey].(string); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// editSpansFromContent is claudeEditSpans for the reference parser.
func editSpansFromContent(v any) []string { return editSpansIn(v, claudeDialect) }

func editSpansIn(v any, d toolDialect) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range items {
		name, in, ok := toolPart(it, d)
		if !ok {
			continue
		}
		if len(d.editTools) > 0 && !d.editTools[name] {
			continue
		}
		path := d.callPath(in)
		if path == "" {
			continue
		}
		// The record is "path\nspan" and restore splits it on the first
		// newline, so a path carrying one puts the rest of itself at the head
		// of the span — handed back as the exact bytes that stopped existing
		// (#2042). The format cannot hold such a path, so the span is not
		// recorded rather than recorded wrong.
		if strings.ContainsAny(path, "\n\r") {
			continue
		}
		old, _ := in[d.oldSpanKey()].(string)
		spans := []string{old}
		if edits, ok := in["edits"].([]any); ok {
			for _, e := range edits {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				o, _ := em[d.editsOldSpanKey()].(string)
				spans = append(spans, o)
			}
		}
		for _, span := range spans {
			if span == "" {
				continue
			}
			if len(span) > editSpanMax {
				span = span[:editSpanMax]
			}
			out = append(out, path+"\n"+span)
		}
	}
	return out
}

// wroteRecordsFromContent is claudeWroteRecords for the reference parser: the
// written side of the same calls editSpansIn reads the replaced side of.
func wroteRecordsFromContent(v any) []string { return wroteRecordsIn(v, claudeDialect) }

func wroteRecordsIn(v any, d toolDialect) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range items {
		name, in, ok := toolPart(it, d)
		if !ok {
			continue
		}
		if len(d.editTools) > 0 && !d.editTools[name] {
			continue
		}
		path := d.callPath(in)
		if path == "" {
			continue
		}
		newText, _ := in[d.newSpanKey()].(string)
		content, _ := in[d.contentSpanKey()].(string)
		written := []string{newText, content}
		// A NotebookEdit delete carries new_source and writes none of it.
		if mode, _ := in["edit_mode"].(string); d.newKeyAlt != "" && mode != "delete" {
			alt, _ := in[d.newKeyAlt].(string)
			written = append(written, alt)
		}
		if edits, ok := in["edits"].([]any); ok {
			for _, e := range edits {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				n, _ := em[d.editsNewSpanKey()].(string)
				written = append(written, n)
			}
		}
		for _, w := range written {
			if rec := WroteRecord(path, w); rec != "" {
				out = append(out, rec)
			}
		}
	}
	return out
}

// commandsFromContent is claudeCommands for the reference parser.
func commandsFromContent(v any) []string { return commandsIn(v, claudeDialect) }

// commandCallsFromContent is commandsFromContent with the call id each command
// will be answered under, so the reference parser can stamp an outcome the same
// way the typed one does. The two must agree or the differential test in #502
// stops meaning anything.
func commandCallsFromContent(v any) []claudeCommand { return commandCallsIn(v, claudeDialect) }

// toolOutcomesFromContent is claudeToolOutcomes for the reference parser.
func toolOutcomesFromContent(v any) []claudeToolOutcome {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []claudeToolOutcome
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "tool_result" {
			continue
		}
		id, _ := m["tool_use_id"].(string)
		if id == "" {
			continue
		}
		bad, _ := m["is_error"].(bool)
		out = append(out, claudeOutcome(id, bad, m["content"]))
	}
	return out
}

func commandsIn(v any, d toolDialect) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range items {
		name, in, ok := toolPart(it, d)
		if !ok || !d.isShellTool(name) {
			continue
		}
		// One call can carry a list rather than a single command: cline's
		// run_commands takes `commands`, an array of complete shell strings.
		// Reading only the singular key indexed none of them.
		for _, cmd := range commandStrings(in, d) {
			if !worthIndexing(cmd) {
				continue
			}
			out = append(out, "$ "+cmd)
		}
	}
	return out
}

// commandStrings pulls the commands out of one call, singular or plural.
func commandStrings(in map[string]any, d toolDialect) []string {
	key := d.commandKey
	if key == "" {
		key = "command"
	}
	if _, ok := in[key]; !ok && d.commandKeyAlt != "" {
		key = d.commandKeyAlt
	}
	switch v := in[key].(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		if d.argsKey != "" {
			v += CommandArgs(in[d.argsKey])
		}
		return []string{v}
	case []any:
		var out []string
		for _, it := range v {
			s, _ := it.(string)
			if strings.TrimSpace(s) == "" {
				continue
			}
			out = append(out, s)
		}
		return out
	}
	return nil
}

// CommandArgs is an argument list as the suffix it puts on a command line,
// " a b", read as Command Code's formatArgsSuffix shows it: a list joined by
// spaces, or a string as it is. The tool hooks read the same arguments.
func CommandArgs(v any) string {
	switch a := v.(type) {
	case string:
		if a != "" {
			return " " + a
		}
	case []any:
		var parts []string
		for _, it := range a {
			if s, ok := it.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			return " " + strings.Join(parts, " ")
		}
	}
	return ""
}

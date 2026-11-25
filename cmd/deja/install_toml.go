package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func installCodex(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.CodexHome(), "config.toml")
	cmd, args := mcpLaunchCommandArgs(exe, uninstall)
	// No `type` key: codex picks stdio from `command`, and 0.163 warns that
	// `mcp_servers.deja.type` is a setting it does not know (#5590).
	block := fmt.Sprintf("[mcp_servers.deja]\ncommand = %q\nargs = %s\n", cmd, tomlStringArray(args))
	return installTOML(path, block, uninstall)
}

// tomlStringArray renders a Go string slice as a TOML inline array, e.g.
// ["/c", "deja", "mcp"], so the Windows cmd /c shim survives the config write.
func tomlStringArray(xs []string) string {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = fmt.Sprintf("%q", x)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// installGrok wires the MCP server into Grok Build's config.toml.
// Same [mcp_servers.NAME] TOML shape as Codex; Grok's hook stdout is ignored
// on passive events, so MCP is the deepest integration available.
func installGrok(exe string, uninstall bool) (installResult, error) {
	if !uninstall {
		if err := grokPreflight(exe, false); err != nil {
			return installResult{}, err
		}
	}
	res, err := installGrokConfig(exe, uninstall)
	if err != nil {
		return res, err
	}
	// The other CLI sharing this directory reads a different file entirely.
	// Both go through wroteAll, so the pair is accounted for the way every
	// other target's pair is — including on a run that changes neither.
	user, uerr := installGrokUserSettings(exe, uninstall)
	if uerr != nil {
		return res, uerr
	}
	return wroteAll(res, user), nil
}

// installGrokConfig is Grok Build's half: [mcp_servers.deja] in config.toml.
func installGrokConfig(exe string, uninstall bool) (installResult, error) {
	cmd, args := mcpLaunchCommandArgs(exe, uninstall)
	block := fmt.Sprintf("[mcp_servers.deja]\ncommand = %q\nargs = %s\n", cmd, tomlStringArray(args))
	return installTOML(filepath.Join(sources.GrokHome(), "config.toml"), block, uninstall)
}

// tomlHeadersClose is the smallest check worth making on a TOML file deja is
// about to edit: every table header opens and closes on its own line. deja
// ships no TOML parser — it splices text so a hand-written file keeps its
// comments and its order — and a full parse is not needed to catch what
// actually breaks. `[mcp_servers.theirs` with the bracket missing is the shape
// a hand-edit leaves, and the one this refuses.
func tomlHeadersClose(text string) error {
	// tomlCodeLines: a line of a multi-line string is text. Codex takes
	// markdown in developer_instructions, and `[style]: https://…` there was
	// refused as a header that never closes (#5158).
	for i, line := range tomlCodeLines(text) {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "[") {
			continue
		}
		// A header may carry a trailing comment: [a.b]  # why. tomlCode cuts
		// it without mistaking a `#` inside a quoted key for its start —
		// codex's hook trust tables are spelled
		// [hooks.state."...plugin.json#hooks[0]:subagent_stop:0:0"], and
		// cutting at the internal `#` made every such config look broken.
		t = tomlCode(t)
		if !strings.HasSuffix(t, "]") {
			return fmt.Errorf("line %d is a table header that never closes: %s", i+1, strings.TrimSpace(line))
		}
	}
	return nil
}

// tomlInlineKey refuses a config that gives the table deja appends — header,
// `[mcp_servers.deja]` or `[[hooks]]` — or a table above it a value by a key:
// `mcp_servers = { mine = {…} }`, `hooks = []`, `deja = {…}` under
// `[mcp_servers]`, `mcp_servers.deja.command = …`. deja's header then defines
// it a second time, which TOML forbids: codex stopped loading its config at
// all, and kimi and grok refused the file (#4554). A dotted key beside deja's
// (`mcp_servers.mine.command = …`) is a table a header may extend.
func tomlInlineKey(text, header string) error {
	want := tomlKeyPath(strings.Trim(header, "[]"))
	wantArray := strings.HasPrefix(header, "[[")
	table := []string(nil)
	for i, line := range tomlCodeLines(text) {
		code := tomlCode(line)
		if strings.HasPrefix(code, "[") && strings.HasSuffix(code, "]") {
			table = tomlKeyPath(strings.Trim(code, "[]"))
			// An array of tables is not the table deja adds: declaring a key as
			// both is the same double definition, so `[[mcp_servers.deja]]` is
			// refused too (#5297). Only when deja adds a plain table; its own
			// `[[hooks]]` is an array by design and the reader's entries stay legal.
			if strings.HasPrefix(code, "[[") && !wantArray && tomlPathHasPrefix(want, table) {
				return fmt.Errorf("line %d declares %s as an array of tables, and the %s table deja adds would define it twice — write it as %s tables or add deja by hand", i+1, strings.Join(table, "."), header, header)
			}
			continue
		}
		k, _, ok := strings.Cut(code, "=")
		if !ok || tomlPathHasPrefix(table, want) {
			continue
		}
		path := append(append([]string(nil), table...), tomlKeyPath(k)...)
		if tomlPathHasPrefix(want, path) || tomlPathHasPrefix(path, want) {
			return fmt.Errorf("line %d sets %s inline, and the %s table deja adds would define it twice — write it as %s tables or add deja by hand", i+1, strings.Join(path, "."), header, header)
		}
	}
	return nil
}

// tomlKeyPath splits a dotted TOML key into its parts, unquoted. The split
// is outside quotes, so `"deja.old"` stays one name, and whitespace around a
// dot is TOML's to allow: `[ mcp_servers . deja ]` is deja's table (#5159).
func tomlKeyPath(k string) []string {
	var out []string
	var part strings.Builder
	var quote byte
	flush := func() {
		p := strings.TrimSpace(part.String())
		if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
			if u, err := strconv.Unquote(p); err == nil {
				p = u
			}
		} else {
			p = strings.Trim(p, `"'`)
		}
		out = append(out, p)
		part.Reset()
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case quote != 0:
			if quote == '"' && c == '\\' && i+1 < len(k) {
				part.WriteByte(c)
				i++
				c = k[i]
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '.':
			flush()
			continue
		}
		part.WriteByte(c)
	}
	flush()
	return out
}

func tomlPathHasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

type tomlMCPBlock struct {
	key        string
	start, end int
}

func installTOML(path, block string, uninstall bool) (installResult, error) {
	return runTOMLMCPSpec(path, "[mcp_servers.deja]", block, nil, uninstall)
}

// tomlMCPBlocks takes the file's text as read, not tomlCodeLines output: it
// blanks the lines of multi-line strings itself, and a second pass over text
// whose closing delimiters are already gone would read the rest as string.
func tomlMCPBlocks(s string) []tomlMCPBlock {
	lines := tomlCodeLines(s)
	var out []tomlMCPBlock
	for i, line := range lines {
		key, ok := tomlMCPHeader(line)
		if !ok {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "[") {
				end = j
				break
			}
		}
		out = append(out, tomlMCPBlock{key: key, start: i, end: end})
	}
	return out
}

// tomlMCPHeader reads the key out of an `[mcp_servers.X]` line. Through
// tomlCode first: a header is as entitled to a trailing comment as a value
// line, and requiring the bracket to end the raw line dropped the whole block
// — install then appended the duplicate this change exists to stop.
//
// The header is read as a key path, so `[ mcp_servers.deja ]`, `[mcp_servers .
// deja]` and `[mcp_servers."deja"]` are all deja's: matching the literal
// `[mcp_servers.` prefix missed the spaced ones, install appended a second
// table, and codex refused the file as a duplicate key (#5159). A sub-table
// keeps its dotted tail (`deja.env`); a name with a dot of its own stays
// quoted, so it is never mistaken for one.
func tomlMCPHeader(line string) (string, bool) {
	line = tomlCode(line)
	if !strings.HasPrefix(line, "[") || strings.HasPrefix(line, "[[") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	path := tomlKeyPath(line[1 : len(line)-1])
	if len(path) < 2 || path[0] != "mcp_servers" {
		return "", false
	}
	parts := make([]string, 0, len(path)-1)
	for _, p := range path[1:] {
		if p == "" {
			return "", false
		}
		if strings.ContainsAny(p, ".[]") {
			p = strconv.Quote(p)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "."), true
}

// tomlBlockRunsDeja is entryIsDejaServer for a [mcp_servers.X] table: deja as
// the command, or deja in the args beside `mcp`. A filesystem server serving
// ~/code/deja ends its args with the binary's name too, and was adopted and
// overwritten with deja's command (#4556).
func tomlBlockRunsDeja(lines []string, block tomlMCPBlock) bool {
	command, inArgs, hasArgs, mcp := false, false, false, false
	for i := block.start + 1; i < block.end; i++ {
		key, value, ok := tomlLineKeyValue(lines[i])
		if !ok {
			continue
		}
		switch key {
		case "command":
			for _, value := range tomlStringValues(value) {
				if commandIsDeja(value) {
					command = true
				}
			}
		case "args":
			hasArgs = true
			value, i = tomlArrayValue(lines, i, block.end, value)
			for _, value := range tomlStringValues(value) {
				inArgs = inArgs || commandIsDeja(value)
				mcp = mcp || value == "mcp"
			}
		}
	}
	return (command && (!hasArgs || mcp)) || (inArgs && mcp)
}

func tomlLineKeyValue(line string) (string, string, bool) {
	line = tomlCode(line)
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

// tomlCode strips a line's trailing comment without misreading a `#` inside a
// quoted value — a Windows path or an arg can carry one, and cutting there
// would hand back half a string.
func tomlCode(line string) string {
	var out strings.Builder
	var quote byte
	escaped := false
	for i := range len(line) {
		c := line[i]
		if quote != 0 {
			out.WriteByte(c)
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '#' {
			break
		}
		if c == '"' || c == '\'' {
			quote = c
		}
		out.WriteByte(c)
	}
	return strings.TrimSpace(out.String())
}

func tomlArrayValue(lines []string, start, end int, value string) (string, int) {
	depth := tomlArrayDelta(value)
	if depth <= 0 {
		return value, start
	}
	var out strings.Builder
	out.WriteString(value)
	i := start
	for i+1 < end && depth > 0 {
		i++
		line := tomlCode(lines[i])
		out.WriteByte('\n')
		out.WriteString(line)
		depth += tomlArrayDelta(line)
	}
	return out.String(), i
}

func tomlArrayDelta(s string) int {
	depth := 0
	var quote byte
	escaped := false
	for i := range len(s) {
		c := s[i]
		if quote != 0 {
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '[':
			depth++
		case ']':
			depth--
		}
	}
	return depth
}

func tomlStringValues(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		quote := s[i]
		if quote != '"' && quote != '\'' {
			continue
		}
		start := i
		escaped := false
		for i++; i < len(s); i++ {
			c := s[i]
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c != quote {
				continue
			}
			if quote == '\'' {
				out = append(out, s[start+1:i])
			} else if value, err := strconv.Unquote(s[start : i+1]); err == nil {
				out = append(out, value)
			}
			break
		}
	}
	return out
}

// tomlRecordedNames is every name an install may have recorded for a
// hand-named table. Before #5159 the name was the header as written, quotes
// and all — `mcp_servers."deja-vu"` — and an uninstall that only asked for the
// unquoted one left the table it had adopted behind.
func tomlRecordedNames(key string) []string {
	bare := key
	if u, err := strconv.Unquote(key); err == nil {
		bare = u
	}
	names := []string{"mcp_servers." + key}
	for _, k := range []string{strconv.Quote(bare), "'" + bare + "'", bare} {
		if k != key {
			names = append(names, "mcp_servers."+k)
		}
	}
	return names
}

// removeTOMLMCPBlock removes one named MCP table without disturbing its
// siblings; reading the header as TOML code keeps adopted commented blocks
// removable on uninstall (#2648).
func removeTOMLMCPBlock(s, key string) string {
	lines := strings.Split(s, "\n")
	code := tomlCodeLines(s)
	var out []string
	for i := 0; i < len(lines); i++ {
		found, ok := tomlMCPHeader(code[i])
		if !ok || !tomlBlockOwnedBy(found, key) {
			out = append(out, lines[i])
			continue
		}
		// To the next header, whatever it is. A sub-table of this same server
		// opens one of its own, and the loop above takes it on the next turn
		// because it belongs to the key being removed — which is the whole of
		// the fix for #2716, and why there is no second skip here.
		i++
		for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(tomlCode(code[i])), "[") {
			i++
		}
		i--
	}
	return strings.Join(out, "\n")
}

// tomlBlockOwnedBy reports whether a header names the block being removed or
// one of its sub-tables.
//
// TOML lets a server carry sub-tables, and `[mcp_servers.deja.env]` opens with
// `[` like any other header — so removing the block by walking to the next one
// stopped there and left the sub-table behind, naming a server that had just
// gone (#2716). Reading the sub-table as part of the block is enough: the walk
// stops at its header and the caller's next turn removes it in the same way.
//
// The dot is what decides. `deja.env` belongs to `deja`; `deja-vu` does not,
// and a bare prefix test would take a hand-wired server with it.

func tomlBlockOwnedBy(found, key string) bool {
	return found == key || strings.HasPrefix(found, key+".")
}

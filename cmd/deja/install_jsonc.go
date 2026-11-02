package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func installOpencode(exe string, uninstall bool) (installResult, error) {
	return installOpencodeShaped(filepath.Join(opencodeConfigHome(), "opencode"), "opencode", exe, uninstall)
}

// installOpencodeShaped writes the `mcp` block into a config of OpenCode's
// shape. Kilo Code's CLI is OpenCode vendored — `packages/opencode` inside the
// Kilo repository — and keeps its own config at `<config>/kilo/kilo.jsonc`,
// with the same `{"type":"local","command":[…]}` entries. Measured: with that
// block written, `kilo mcp list` prints `✓ deja connected` and the command it
// runs (#3672).
func installOpencodeShaped(dir, base, exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(dir, base+".json")
	if _, err := os.Stat(path); err != nil {
		if _, e := os.Stat(filepath.Join(dir, base+".jsonc")); e == nil {
			path = filepath.Join(dir, base+".jsonc")
		}
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var next []byte
	var note string
	// The content, not the name: opencode reads either file as JSONC, and
	// picking the writer by extension meant the same commented config was
	// edited as text under one name and refused under the other (#1664). The
	// same holds the other way: plain JSON in opencode.jsonc — `{}`, the
	// $schema line on one line, a minified config — was refused by the text
	// writer while the decoding writer edits those bytes under the other name.
	if configIsJSONC(old) {
		next, note, err = updateOpencodeJSONC(old, exe, uninstall)
	} else {
		next, note, err = updateOpencodeJSON(old, path, exe, uninstall)
	}
	if err != nil {
		return installResult{}, configParseError(path, err)
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

func updateOpencodeJSON(old []byte, path, exe string, uninstall bool) ([]byte, string, error) {
	var root map[string]any
	var note string
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return nil, "", err
	}
	m, _, err := mcpBlock(root, "mcp", "")
	if err != nil {
		if uninstall {
			return old, "", nil
		}
		return nil, "", err
	}
	if m == nil {
		// The same guard as every other MCP writer: nothing to take out of a
		// block deja never wrote, and adding it on the way out rewrote a config
		// that never mentioned deja (#676).
		if uninstall {
			return old, "", nil
		}
		m = map[string]any{}
		root["mcp"] = m
		noteBlockAdded(path, "mcp")
	}
	// OpenCode 2.x accepts the MCP servers one level below the 1.x `mcp`
	// block. Keep the entry in the shape the reader already uses instead of
	// creating a second, invisible server beside it.
	servers := m
	blockKey := "mcp"
	if nested, ok := opencodeNestedServers(m); ok {
		servers = nested
		blockKey = "mcp.servers"
	}
	if uninstall {
		delete(servers, "deja")
		removeAdoptedDejaEntries(path, blockKey, servers)
		note = leftDejaEntriesNote(servers)
		// And the block, when deja is what put it there (#2604).
		if len(m) == 0 && blockWasAdded(path, "mcp") {
			delete(root, "mcp")
			forgetBlockAdded(path, "mcp")
		}
	} else {
		key := dejaEntryKey(servers)
		if key != "deja" {
			noteBlockAdded(path, blockKey+"."+key)
		}
		servers[key], note = mergeDejaEntry(servers[key], map[string]any{"type": "local", "command": []string{exe, "mcp"}})
		note = withOtherDejaEntries(note, servers, key)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return nil, "", err
	}
	return append(next, '\n'), note, nil
}

// opencodeNestedServers reports the OpenCode 2.x shape only when the config
// has no direct MCP entry that already owns deja. A mixed config can be a
// deliberate 1.x setup with a server named `servers`; keeping the direct
// entry in that case preserves the behaviour of the older writer.
func opencodeNestedServers(m map[string]any) (map[string]any, bool) {
	servers, ok := m["servers"].(map[string]any)
	if !ok || servers == nil {
		return nil, false
	}
	for key, value := range m {
		if key == "servers" {
			continue
		}
		if key == "deja" || mcpEntryRunsDeja(value) {
			return nil, false
		}
	}
	return servers, true
}

// updateOpencodeJSONCNested edits an existing `mcp.servers` object while
// retaining JSONC comments and the fields already present on deja's entry.
// The generic JSONC writer is used here because it already handles dotted
// object paths, comments, trailing commas, aliases and nested indentation.
func updateOpencodeJSONCNested(old []byte, exe string, uninstall bool) ([]byte, string, error) {
	text := lfText(old)
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(text)), &root); err != nil {
		return nil, "", err
	}
	mcp, ok := root["mcp"].(map[string]any)
	if !ok {
		return nil, "", errors.New(`"mcp" is not an object`)
	}
	servers, ok := opencodeNestedServers(mcp)
	if !ok {
		return nil, "", errors.New(`"mcp.servers" is not an object`)
	}
	key := dejaEntryKey(servers)
	var note string
	var next string
	if uninstall {
		delete(servers, key)
		note = leftDejaEntriesNote(servers)
		var err error
		next, err = jsoncSetEntry(text, "mcp.servers", key, "", true, 2)
		if err != nil {
			return nil, "", err
		}
	} else {
		merged, mergeNote := mergeDejaEntry(servers[key], map[string]any{"type": "local", "command": []string{exe, "mcp"}})
		note = withOtherDejaEntries(mergeNote, servers, key)
		entry, err := jsoncEntryText(merged)
		if err != nil {
			return nil, "", err
		}
		next, err = jsoncSetEntry(text, "mcp.servers", key, entry, false, 2)
		if err != nil {
			return nil, "", err
		}
	}
	return []byte(next), note, nil
}

// dropJSONCEntry takes one entry out of an "mcp" block written as lines, and
// hands back what is left beside what it removed. The key is the caller's:
// somebody who wired deja by hand may have written it under another name, and
// install has to update that entry rather than add a sibling (#2742). An entry
// spelled across
// lines carries `"deja"` on its first one only; dropping just that line left
// the rest of it in the block and the config stopped parsing (#2394), so the
// entry is bounded by counting braces the way the block itself is.
func dropJSONCEntry(lines []string, key string) (body, dropped []string, wasLast bool, at int) {
	at = -1
	name := `"` + key + `"`
	// On the code a parser reads, not the raw line. A comment that only names
	// deja — a parked entry someone commented out, a note saying who wrote the
	// block — is not an entry to replace, and dropping its first line leaves a
	// /* … */ closing on its own, which costs the reader every server in the
	// file (#2473). The same reading keeps a brace inside a comment out of the
	// depth count. jsoncLastCodeLine already reads the block this way to place
	// its comma.
	inBlock := false
	for i := 0; i < len(lines); i++ {
		code, next, _ := jsoncCodeOf(lines[i], inBlock)
		inBlock = next
		if !strings.Contains(code, name) {
			body = append(body, lines[i])
			if code != "" {
				wasLast = false
			}
			continue
		}
		if at < 0 {
			at = len(body)
		}
		dropped = append(dropped, lines[i])
		wasLast = true
		depth := jsoncBraceDelta(code)
		for depth > 0 && i+1 < len(lines) {
			i++
			c, nb, _ := jsoncCodeOf(lines[i], inBlock)
			inBlock = nb
			dropped = append(dropped, lines[i])
			depth += jsoncBraceDelta(c)
		}
	}
	return body, dropped, wasLast, at
}

// jsoncEntryIsNested reports whether the key opens an entry below the top
// level of these block lines, and not at the top level itself. The drop
// below matches the key wherever it sits, so an entry that lives in a
// nesting this writer cannot edit in place must be refused rather than
// pulled up (#3929). A key that also sits at the top level is left for the
// normal path — the writer edits that one.
//
// The scan walks every quoted token, not just the one a line starts with:
// an inline `"servers": { "deja": {…} }` hides the nested key mid-line,
// and a line-start-only check let it through and deleted the whole
// `servers` line, the reader's other servers included.
func jsoncEntryIsNested(lines []string, key string) bool {
	want := `"` + key + `"`
	depth := 0
	inBlock := false
	atTop, below := false, false
	for _, l := range lines {
		code, next, _ := jsoncCodeOf(l, inBlock)
		inBlock = next
		for i := 0; i < len(code); {
			if code[i] == '"' {
				end := zedStringEnd(code, i)
				if end < 0 {
					break
				}
				if code[i:end] == want && jsoncIsKey(code, end) {
					if depth > 0 {
						below = true
					} else {
						atTop = true
					}
				}
				i = end
				continue
			}
			switch code[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			i++
		}
	}
	return below && !atTop
}

// jsoncBlockEntries reads the servers out of an "mcp" block written as lines:
// the key each one is written under, and the value a parser would read there.
//
// The text writer knew the literal key "deja" and nothing else, so an entry
// wired by hand under another name was invisible to it — install wrote a
// sibling and the harness started two servers, one of them possibly naming a
// binary that is gone (#2742). Reading past the key at what an entry runs is
// what doctor and the .json writer already do; this is the same reading, off
// text that may carry comments and trailing commas.
//
// Entries whose text does not parse are left out. They are the shapes this
// writer cannot act on anyway, and guessing at one is how a config stops
// loading.
func jsoncBlockEntries(lines []string) map[string]any {
	out := map[string]any{}
	inBlock := false
	for i := 0; i < len(lines); i++ {
		code, next, _ := jsoncCodeOf(lines[i], inBlock)
		inBlock = next
		key, rest := jsoncEntryKeyOf(code)
		if key == "" {
			continue
		}
		parts := []string{rest}
		depth := jsoncBraceDelta(rest)
		for depth > 0 && i+1 < len(lines) {
			i++
			c, nb, _ := jsoncCodeOf(lines[i], inBlock)
			inBlock = nb
			parts = append(parts, c)
			depth += jsoncBraceDelta(c)
		}
		var v any
		text := strings.TrimSuffix(strings.TrimSpace(strings.Join(parts, " ")), ",")
		if json.Unmarshal([]byte(text), &v) != nil {
			continue
		}
		out[key] = v
	}
	return out
}

// jsoncEntryKeyOf splits a line of code into the key it opens and the value
// text after the colon. It answers empty for anything that does not start with
// a quoted key, which inside an "mcp" block means a line that is not the start
// of a server.
func jsoncEntryKeyOf(code string) (key, rest string) {
	t := strings.TrimSpace(code)
	if !strings.HasPrefix(t, `"`) {
		return "", ""
	}
	escaped := false
	for i := 1; i < len(t); i++ {
		switch {
		case escaped:
			escaped = false
		case t[i] == '\\':
			escaped = true
		case t[i] == '"':
			after := strings.TrimSpace(t[i+1:])
			if !strings.HasPrefix(after, ":") {
				return "", ""
			}
			k, err := strconv.Unquote(t[:i+1])
			if err != nil {
				return "", ""
			}
			return k, strings.TrimSpace(after[1:])
		}
	}
	return "", ""
}

// jsoncFirstCodeLine finds the first line of a .jsonc block that a parser
// would read as code. It returns -1 when the block holds no code at all.
func jsoncFirstCodeLine(body []string) int {
	inBlock := false
	for i, line := range body {
		c, nextBlock, _ := jsoncCodeOf(line, inBlock)
		inBlock = nextBlock
		if c != "" {
			return i
		}
	}
	return -1
}

// jsoncLineOpensInBlock reports whether line i starts inside a /* */ comment.
func jsoncLineOpensInBlock(body []string, i int) bool {
	inBlock := false
	for j := range i {
		_, inBlock, _ = jsoncCodeOf(body[j], inBlock)
	}
	return inBlock
}

// jsoncBlockCommentStart is the line that opens the block comment line i
// starts inside.
func jsoncBlockCommentStart(body []string, i int) int {
	for j := i - 1; j >= 0; j-- {
		if !jsoncLineOpensInBlock(body, j) {
			return j
		}
	}
	return 0
}

// jsoncCommentAbove walks back from line i over the `//` lines directly above
// it: a comment there is about that entry, and an entry written between the
// two took it over (#4203). A line inside a block comment is not one of them.
func jsoncCommentAbove(body []string, i int) int {
	opensInBlock := make([]bool, len(body))
	inBlock := false
	for j, line := range body {
		opensInBlock[j] = inBlock
		_, inBlock, _ = jsoncCodeOf(line, inBlock)
	}
	for i > 0 && !opensInBlock[i-1] && strings.HasPrefix(strings.TrimSpace(body[i-1]), "//") {
		i--
	}
	return i
}

// jsoncLastCodeLine finds the last line of a .jsonc block that a parser would
// read as code, and where that code ends on it. It returns -1 when the block
// holds nothing but comments and blank lines.
//
// The comma that joins deja's entry to the previous one has to land at the end
// of the code, not the end of the line. Appended after a trailing // comment it
// is stripped with the comment and the two entries lose their separator;
// appended inside a /* … */ block it either vanishes with the block or is left
// behind as a comma of its own (#1695).
func jsoncLastCodeLine(body []string) (idx, end int, code string) {
	idx = -1
	inBlock := false
	for i, line := range body {
		c, nextBlock, e := jsoncCodeOf(line, inBlock)
		if c != "" {
			idx, end, code = i, e, c
		}
		inBlock = nextBlock
	}
	return idx, end, code
}

// jsoncCodeOf returns the code a parser reads on one line, whether the line
// leaves a /* … */ block open, and the offset where that code ends.
func jsoncCodeOf(line string, inBlock bool) (code string, stillInBlock bool, end int) {
	var b strings.Builder
	inString, escaped := false, false
	end = 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inBlock {
			if c == '*' && i+1 < len(line) && line[i+1] == '/' {
				inBlock, i = false, i+1
			}
			continue
		}
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '/' && i+1 < len(line) && line[i+1] == '/':
			return strings.TrimSpace(b.String()), false, end
		case !inString && c == '/' && i+1 < len(line) && line[i+1] == '*':
			inBlock, i = true, i+1
			continue
		}
		b.WriteByte(c)
		// A CR is the end of a CRLF line, not code: a comma after it sat
		// alone at the start of the next line in an editor.
		if c != ' ' && c != '\t' && c != '\r' {
			end = i + 1
		}
	}
	return strings.TrimSpace(b.String()), inBlock, end
}

// openInlineBlock rewrites `"key": { … }` written on one line into the
// multi-line shape the rest of this writer reads, keeping whatever the line
// already held. A line carrying a comment is left alone: the split would have
// to decide which side of it the entry belongs on, and refusing is the honest
// answer for a shape nobody writes.
func openInlineBlock(lines []string, key string) ([]string, bool) {
	inBlock := false
	for i, l := range lines {
		// The comment state carries between lines: read on its own, a line
		// inside a /* … */ looks like code, and splitting it rewrote a block
		// the reader had commented out.
		code, next, _ := jsoncCodeOf(l, inBlock)
		inBlock = next
		if !strings.Contains(code, `"`+key+`"`) || !strings.Contains(code, "{") {
			continue
		}
		if jsoncBraceDelta(code) > 0 {
			// The block spans lines, but its first server may sit on the
			// opening line — `"mcp": { "a": {…}` — and then the lines between
			// the ends hold nothing, so deja's entry went in with no comma
			// after the reader's and the file stopped parsing.
			open := braceOutsideString(l, '{')
			if open < 0 {
				return lines, false
			}
			// What a parser reads, not what the line holds: a comment after the
			// opening brace — `"mcp": { // my servers` — is not a server on
			// the opening line, and refusing it took an ordinary shape away
			// from exactly the readers #1664 was opened for.
			codeOpen := braceOutsideString(code, '{')
			if codeOpen < 0 || strings.TrimSpace(code[codeOpen+1:]) == "" {
				return lines, true
			}
			rest := strings.TrimSpace(l[open+1:])
			if strings.TrimSpace(code) != strings.TrimSpace(l) {
				return lines, false
			}
			indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			out := append([]string{}, lines[:i]...)
			out = append(out, l[:open+1], indent+"  "+rest)
			return append(out, lines[i+1:]...), true
		}
		if strings.TrimSpace(code) != strings.TrimSpace(l) {
			// A comment on the line the block opens and closes on. Splitting
			// it would have to decide which side of the comment the entry
			// belongs on, and there is no answer that keeps the reader's
			// meaning — so the caller refuses rather than writing past the
			// block, which is what a silent pass-through did (#2777).
			return lines, false
		}
		open := braceOutsideString(l, '{')
		end := matchingBrace(l, open)
		if open < 0 || end < 0 {
			return lines, false
		}
		indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		inner := strings.TrimSpace(l[open+1 : end])
		out := append([]string{}, lines[:i]...)
		out = append(out, l[:open+1])
		if inner != "" {
			out = append(out, indent+"  "+inner)
		}
		out = append(out, indent+l[end:])
		return append(out, lines[i+1:]...), true
	}
	return lines, true
}

// matchingBrace is the `}` that closes the `{` at open, counting depth and
// skipping strings. Taking the first structural `}` instead split a non-empty
// inline block inside its first server: it still parsed, but it left the
// reader's file reindented into something that reads as broken.
func matchingBrace(line string, open int) int {
	if open < 0 || open >= len(line) || line[open] != '{' {
		return -1
	}
	depth, inString, escaped := 0, false, false
	for i := open; i < len(line); i++ {
		switch c := line[i]; {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// braceOutsideString is the index of the first brace of that kind a parser
// would read as structure — a path holding one is ordinary (#2475).
func braceOutsideString(line string, want byte) int {
	inString, escaped := false, false
	for i := range len(line) {
		switch c := line[i]; {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == want:
			return i
		}
	}
	return -1
}

// jsoncBraceDelta counts the braces of one line's code that a parser reads as
// structure. Comments are already out — jsoncCodeOf strips them, and keeps
// string contents because the `"deja"` match and the comma offset both need
// them — so what is left to skip here is quoted text. A command path holding a
// brace is ordinary (`${VENDOR}` left literal, `/opt/{a,b}/bin`, a Windows GUID
// directory), and counting one as structure moved the end of the "mcp" block:
// deja's entry landed outside it, or inside it without a comma, or the drop
// loop swallowed the server underneath (#2475).
func jsoncBraceDelta(code string) int {
	depth := 0
	inString, escaped := false, false
	for i := range len(code) {
		switch c := code[i]; {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
		}
	}
	return depth
}

// emptyJSONCBlockRE matches an "mcp" block with nothing in it, however the
// reader spaced the braces.
var emptyJSONCBlockRE = regexp.MustCompile(`"mcp"\s*:\s*\{\s*\}`)

// mergedJSONCEntryLine builds the entry line to write, carrying over whatever
// the reader had on deja's own entry. Falls back to the plain line when the
// dropped text is not one entry this can read: a shape it cannot parse is not a
// reason to refuse the install, only to write the entry deja knows.
func mergedJSONCEntryLine(dropped []string, key, exe string) (string, string) {
	plain := fmt.Sprintf(`    %q: {"type":"local","command":[%q,"mcp"]}`, key, exe)
	next := map[string]any{"type": "local", "command": []string{exe, "mcp"}}
	joined := strings.TrimSpace(strings.Join(dropped, " "))
	at := strings.Index(joined, ":")
	if len(dropped) == 0 || at < 0 {
		return plain, ""
	}
	value := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(joined[at+1:]), ","))
	var was any
	if err := json.Unmarshal([]byte(value), &was); err != nil {
		return plain, ""
	}
	merged, note := mergeDejaEntry(was, next)
	if sameEntry(merged, was) {
		// Merging changed nothing, so the reader's own bytes are the answer:
		// re-encoding reorders the keys, and a repeat install then rewrote a
		// config it had nothing to say about and reported having replaced
		// somebody's entry — deja's own, from the run before (#2757).
		//
		// The switch line still holds: what is on file is still off, and that
		// is true whether or not this run changed anything.
		off := ""
		if entrySwitchedOff(merged) {
			off = switchedOffNote
		}
		return strings.TrimSuffix(strings.TrimRight(strings.Join(dropped, "\n"), " \t"), ","), off
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return plain, ""
	}
	return fmt.Sprintf("    %q: ", key) + string(b), note
}

func updateOpencodeJSONC(old []byte, exe string, uninstall bool) ([]byte, string, error) {
	line := fmt.Sprintf(`    "deja": {"type":"local","command":[%q,"mcp"]}`, exe)
	// In LF, like the other writers that splice lines: split on '\n' alone,
	// every line kept its '\r', the comma check missed it and the second
	// install wrote `},\r,` (#4553). writeIfChanged puts the CRs back.
	s := lfText(old)
	if strings.TrimSpace(s) == "" {
		if uninstall {
			return []byte("{}\n"), "", nil
		}
		return []byte("{\n  \"mcp\": {\n" + line + "\n  }\n}\n"), "", nil
	}
	// OpenCode 2.x stores servers under `mcp.servers`. Route that shape through
	// the dotted JSONC writer before the legacy line editor can mistake a nested
	// entry for one at the top of `mcp`.
	if opencodeJSONCNestedServers(old) {
		return updateOpencodeJSONCNested(old, exe, uninstall)
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	// A block that opens and closes on one line has no end line to find, so
	// the insert landed after its closing brace and the config stopped
	// parsing (#2777). `"mcp": {}` is what a config nobody has added a server
	// to looks like.
	lines, canOpen := openInlineBlock(lines, "mcp")
	if !canOpen {
		if uninstall {
			// The file as it was, byte for byte: this is a config deja has
			// just declined to edit, and rejoining the lines wrote the split
			// back out — and gave a file with no trailing newline one.
			return old, "", nil
		}
		return nil, "", errors.New("opencode config has an \"mcp\" block deja could not edit without risking it — add the deja server by hand")
	}
	start, end := -1, -1
	sameLine := false
	inBlock := false
	for i, l := range lines {
		code, next, _ := jsoncCodeOf(l, inBlock)
		inBlock = next
		if strings.Contains(code, `"mcp"`) && strings.Contains(code, "{") {
			start = i
			depth := jsoncBraceDelta(code)
			if depth <= 0 {
				// The block opens and closes on the line that names it. There
				// are no lines between the braces to insert between, and the
				// general path below wrote the entry after the block instead —
				// outside it, and leaving a file no parser would read (#2742).
				sameLine = true
				end = i
				break
			}
			block := inBlock
			for j := i + 1; j < len(lines); j++ {
				c, nb, _ := jsoncCodeOf(lines[j], block)
				block = nb
				depth += jsoncBraceDelta(c)
				if depth <= 0 {
					end = j
					break
				}
			}
			break
		}
	}
	if sameLine && !uninstall {
		code, _, _ := jsoncCodeOf(lines[start], false)
		// Only an empty one. A block with entries already on that line is a
		// shape this text path cannot edit safely, and the refusal below says
		// so rather than guessing where the commas go.
		if !emptyJSONCBlockRE.MatchString(code) {
			return nil, "", errors.New("opencode config has an \"mcp\" block deja could not edit without risking it — add the deja server by hand")
		}
		indent := lines[start][:len(lines[start])-len(strings.TrimLeft(lines[start], " \t"))]
		at := strings.LastIndex(lines[start], "{")
		head := lines[start][:at+1]
		tail := lines[start][at+1:]
		// What is left of the line after the brace deja took over: a trailing
		// comment stays, the spacing that used to sit between the two braces
		// does not — "mcp": { } left a line ending in a space of deja's making.
		tail = strings.TrimRight(strings.Replace(tail, "}", "", 1), " \t")
		out := append([]string{}, lines[:start]...)
		out = append(out, head, line, indent+"}"+tail)
		out = append(out, lines[start+1:]...)
		return []byte(strings.Join(out, "\n") + "\n"), "", nil
	}
	if start >= 0 && end > start {
		// The lines this drops are the entry that was already here. Naming what
		// it ran is the same sentence the .json writer prints (#2390); without
		// it, what install told you depended on which of the two names the
		// config had (#2392).
		block := lines[start+1 : end]
		// Which entry is deja's. Somebody may have wired it by hand under
		// another name, and updating that one is what keeps the harness from
		// starting two servers (#2269). Uninstall stays on deja's own key: an
		// entry deja never wrote is not deja's to delete, and the note below
		// says it is still there.
		servers := jsoncBlockEntries(block)
		key := "deja"
		if !uninstall {
			key = dejaEntryKey(servers)
		}
		// OpenCode 2.x keeps its servers under `mcp.servers`, one level below
		// the top of the `mcp` block this writer edits. The drop below matches
		// the key at any depth, so on that shape it cut deja's entry out of
		// the nesting and wrote ours at the top of `mcp` — moving the entry
		// out of the place the harness reads and stranding whatever the
		// reader had put under it, while reporting a normal replacement
		// (#3929). This writer speaks the 1.x shape only; refuse rather than
		// relocate.
		if !uninstall && jsoncEntryIsNested(block, key) {
			return nil, "", errors.New("opencode config keeps its servers under \"mcp.servers\"; deja edits the top of the \"mcp\" block and would move the entry there — add the deja server by hand")
		}
		body, dropped, wasLast, droppedAt := dropJSONCEntry(block, key)
		if uninstall && len(dropped) == 0 {
			// No entry of deja's to take out, so the file as it was: the lines
			// here are the ones openInlineBlock split, and joining them wrote
			// a one-line block back over two lines into a config deja had
			// never touched.
			return old, leftDejaEntriesNote(servers), nil
		}
		// What the reader put on our own entry is theirs: an environment
		// pointing at a store on another disk, an entry switched off. The
		// parsed path has merged those since #2479; this one rewrote the line
		// and dropped them without a word (#2742).
		mergeNote := ""
		if !uninstall {
			line, mergeNote = mergedJSONCEntryLine(dropped, key, exe)
		}
		// Our entry used to go last, so the comma joining it to the block sat
		// on the line above — the reader's. Taking ours out leaves that comma
		// with nothing after it, which a strict reader of the file rejects, so
		// a comma we put there once has to come back out with the entry it was
		// written for (#2617). Only where ours ended the block: a comma
		// anywhere else is still separating two things.
		if wasLast {
			if i, code, trim := jsoncLastCodeLine(body); i >= 0 && strings.HasSuffix(trim, ",") {
				body[i] = body[i][:code-1] + body[i][code:]
			}
		}
		var note string
		// This path merges the whole entry (mergedJSONCEntryLine above), so
		// whatever the reader had on it — an env pointing at a store on
		// another disk, a switch they set — comes across with it, and
		// mergeDejaEntry says so in its own note. The hunt branch carried the
		// switch alone through the text; merging carries it and the rest.
		entryLine := line
		if uninstall {
			note = leftDejaEntriesNote(servers)
		} else {
			// mergeDejaEntry says the same sentence when it takes an entry
			// over, so prefer its note: it also carries what merging found.
			note = mergeNote
			if note == "" {
				note = replacedJSONCLineNote(dropped, line)
			}
			note = withOtherDejaEntries(note, servers, key)
		}
		if !uninstall {
			// Ours goes first, carrying its own comma. Written last it needed
			// one on the line above — the reader's — and uninstall has no way
			// to tell that comma from one they wrote, so it stayed behind in a
			// file that was not ours to change (#2617). A comma of ours leaves
			// with our line, and the placement rules that a comma on someone
			// else's line needed — not on an opening brace, not after a
			// trailing comment, not inside a block comment (#1695) — stop
			// applying at all.
			entry := entryLine
			at := len(body)
			if i := jsoncFirstCodeLine(body); i >= 0 {
				entry += ","
				switch {
				case droppedAt >= 0 && droppedAt <= i:
					// Ours was already first: back where it was, under the
					// comment the reader wrote on it.
					at = droppedAt
				case jsoncLineOpensInBlock(body, i):
					// The first code follows a block comment that closes on
					// its line; above that comment, not inside it.
					at = jsoncBlockCommentStart(body, i)
				default:
					at = jsoncCommentAbove(body, i)
				}
			}
			body = append(body[:at:at], append([]string{entry}, body[at:]...)...)
		}
		out := append([]string{}, lines[:start+1]...)
		out = append(out, body...)
		out = append(out, lines[end:]...)
		return []byte(strings.Join(out, "\n") + "\n"), note, nil
	}
	if uninstall {
		return old, "", nil
	}
	// An "mcp" key exists but brace-counting could not bound it — its opening
	// brace is on a later line, or the braces are unbalanced. Adding a fresh
	// block here would leave the file with two "mcp" keys, which drops the
	// user's other servers. Refuse rather than corrupt a config that cannot be
	// rebuilt; the caller surfaces this so the user can wire deja by hand.
	if start >= 0 || opencodeHasMCPKey(lines) {
		return nil, "", errors.New("opencode config has an \"mcp\" block deja could not edit without risking it — add the deja server by hand")
	}
	insert := len(lines) - 1
	// opencode puts its $schema line into a config that lacks one, and into
	// `{}` that gives `"$schema": "…",}` — the closing brace at the end of
	// the last line of code (1.18.35). That brace goes on a line of its own so
	// the block has somewhere to go above it.
	if insert >= 1 && stripJSONComments(lines[insert]) == lines[insert] {
		l := strings.TrimRight(lines[insert], " \t")
		if code, _, _ := jsoncCodeOf(l, false); code != "}" && strings.HasSuffix(code, "}") && jsoncBraceDelta(code) < 0 {
			lines = append(lines[:insert:insert], strings.TrimRight(l[:len(l)-1], " \t"), "}")
			insert++
		}
	}
	// The block is spliced in above the file's closing brace, so the file has
	// to have one of its own on that line. On a config written as a single
	// line the splice put the new block *before* the object (#2777).
	if insert < 1 || strings.TrimSpace(stripJSONComments(lines[insert])) != "}" {
		return nil, "", errors.New("opencode config is not laid out in a way deja can add to — add the deja server by hand")
	}
	// The comma goes on the last line of code above the brace, and only when
	// that code has none yet. Walking up past every line that already ended
	// in one ran past a trailing comma on the last key and put a second comma
	// on the line above it — in a nested config an opening brace, `"shim": {,`,
	// which Kilo and opencode refuse to start with (#4399). At the end of the
	// code rather than the line: after a // comment it is not a comma (#1695).
	if i, end, code := jsoncLastCodeLine(lines[:insert]); i >= 0 &&
		!strings.HasSuffix(code, ",") && !strings.HasSuffix(code, "{") {
		lines[i] = lines[i][:end] + "," + lines[i][end:]
	}
	mcp := []string{`  "mcp": {`, line, "  }"}
	out := append([]string{}, lines[:insert]...)
	out = append(out, mcp...)
	out = append(out, lines[insert:]...)
	return []byte(strings.Join(out, "\n") + "\n"), "", nil
}

func opencodeJSONCNestedServers(old []byte) bool {
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(string(old))), &root); err != nil {
		return false
	}
	mcp, ok := root["mcp"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = opencodeNestedServers(mcp)
	return ok
}

// replacedJSONCLineNote names the deja entry a line-editing write dropped.
// The lines are text rather than a decoded entry, so the comparison is the
// text deja would have written: same line, nothing replaced. Empty when the
// entry was absent or already deja's own.
func replacedJSONCLineNote(dropped []string, line string) string {
	if len(dropped) == 0 {
		return ""
	}
	if len(dropped) == 1 && strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(dropped[0]), ",")) ==
		strings.TrimSpace(line) {
		return ""
	}
	was := jsoncEntryCommand(strings.Join(dropped, " "))
	if was == "" {
		return "replaced the deja entry that was already here"
	}
	if sameDejaCommand(was, jsoncEntryCommand(line)) {
		return ""
	}
	return "replaced the deja entry that was already here, which ran " + safeForStatusline(was, 200)
}

// jsoncEntryCommand pulls the command out of an entry deja did not write. It
// reads the text rather than the JSON: the block may carry comments and
// trailing commas, which is why this writer exists at all.
func jsoncEntryCommand(text string) string {
	_, after, ok := strings.Cut(text, `"command"`)
	if !ok {
		return ""
	}
	rest := after
	if j := strings.IndexAny(rest, `"`); j >= 0 {
		rest = rest[j+1:]
		if k := strings.IndexByte(rest, '"'); k > 0 {
			return rest[:k]
		}
	}
	return ""
}

// opencodeHasMCPKey reports whether any line declares an "mcp" key, so the
// brace-counting editor can tell "there is a block I failed to bound" from
// "there is no block, add one".
func opencodeHasMCPKey(lines []string) bool {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, `"mcp"`) && strings.Contains(t, ":") {
			return true
		}
	}
	return false
}

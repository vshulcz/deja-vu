package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// A config with a `//` line in it is not JSON, and five writers refused the
// whole target over one: someone who had annotated `~/.cursor/mcp.json` could
// not install deja at all, and the message sent them to look at permissions
// (#1664). Zed's config is edited as text for exactly this reason since #1285 —
// "comments, key order, indentation, trailing commas — survives untouched" —
// and the scanners that do it are already general. This is the same edit under
// a key of the caller's choosing.

// configIsJSONC reports whether a config fails to parse as JSON only because it
// carries comments. A file that is broken some other way is still refused: deja
// does not guess at what a reader meant to write.
func configIsJSONC(b []byte) bool {
	var probe any
	if json.Unmarshal(b, &probe) == nil {
		return false
	}
	stripped := stripJSONComments(string(b))
	return json.Unmarshal([]byte(stripped), &probe) == nil
}

// stripJSONComments blanks out comments, keeping every other byte where it is
// so an offset into the result is an offset into the original.
func stripJSONComments(text string) string {
	out := []byte(text)
	for i := 0; i < len(text); {
		switch text[i] {
		case '"':
			end := zedStringEnd(text, i)
			if end < 0 {
				return string(out)
			}
			i = end
		case '/':
			end := zedSkipComment(text, i)
			if end == i {
				i++
				continue
			}
			for j := i; j < end && j < len(out); j++ {
				if out[j] != '\n' {
					out[j] = ' '
				}
			}
			i = end
		default:
			i++
		}
	}
	return string(out)
}

// jsoncSetEntry writes deja's entry into a JSONC object under blockKey, or
// takes it out, leaving every other byte of the file alone.
// dropFrom says how much of the chain an uninstall takes with the entry: the
// index of the outermost key to remove, or len(keys) for the entry alone.
func jsoncSetEntry(text, blockKey, id, entry string, uninstall bool, dropFrom int) (string, error) {
	open := zedTopLevelOpen(text)
	if open < 0 {
		return "", fmt.Errorf("does not look like a settings object; add %q by hand", blockKey)
	}
	keys := strings.Split(blockKey, ".")
	block, have := walkJSONCKeys(text, open, keys)
	if block == nil {
		if uninstall {
			return text, nil
		}
		// The keys that are missing, nested inside the deepest one that is
		// there: openclaw keeps its servers under `mcp.servers`, so a config
		// with an `mcp` block and no `servers` in it gets one key, and a config
		// with neither gets both (#2783).
		at, comma, indent := open, rootComma(text, open), "  "
		if have > 0 {
			parent, _ := walkJSONCKeys(text, open, keys[:have])
			at, comma, indent = parent.valueOpen, blockComma(text, parent), strings.Repeat("  ", have+1)
		}
		insert := nestedBlocks(keys[have:], id, entry, indent)
		// A newline before what the parent closes with, or the chain ends up
		// against the parent's own brace: `}}` on one line, which parses and
		// reads as a mistake.
		if have > 0 && comma == "" && !strings.HasPrefix(text[at+1:], "\n") {
			insert += "\n" + strings.Repeat("  ", have)
		}
		return text[:at+1] + insert + comma + text[at+1:], nil
	}
	found := zedFindKey(text, block.valueOpen+1, id)
	if found == nil {
		if uninstall {
			return text, nil
		}
		// A newline after the entry when the block was written on one line, so
		// the reader's first entry keeps a line of its own rather than being
		// pushed against deja's closing brace.
		rest := text[block.valueOpen+1:]
		tail := ""
		if rest != "" && rest[0] != '\n' && rest[0] != '}' {
			tail = "\n   "
		}
		// And no comma into an empty block: `{"deja": {…},}` is not JSON, and
		// the file was then refused on every later run with a message pointing
		// at the reader's comment (#2740).
		comma := ","
		if strings.TrimSpace(stripJSONComments(text[block.valueOpen+1:block.valueEnd-1])) == "" {
			comma, tail = "", "\n  "
			// Unless what follows opens a line of its own — a block whose body
			// is a comment. The closing brace is already where it belongs, and
			// a tail here left a blank line behind on the way out, one more on
			// every round trip.
			if rest != "" && rest[0] == '\n' {
				tail = ""
			}
		}
		depth := strings.Repeat("  ", len(keys)+1)
		insert := fmt.Sprintf("\n%s%q: %s%s%s", depth, id, entryAtDepth(entry, len(keys)), comma, tail)
		return text[:block.valueOpen+1] + insert + text[block.valueOpen+1:], nil
	}
	if uninstall {
		if dropFrom < len(keys) {
			// The chain deja created, from the outermost key it wrote: the
			// parsed path deletes an emptied `mcp` along with its `servers`,
			// and a round trip has to leave the file as it found it (#2783).
			if dropFrom < len(keys)-1 {
				chain, _ := walkJSONCKeys(text, open, keys[:dropFrom+1])
				cut := zedEntrySpan(text, chain)
				return closeEmptied(text[:cut[0]]+text[cut[1]:], keys), nil
			}
			return closeEmptied(zedDropEntry(text, block, found), keys), nil
		}
		// The entry alone. zedDropEntry takes the block with the last entry in
		// it, which is right for a block deja created and wrong for one the
		// reader wrote — the rule #2604 and #2583 settled for the other
		// writers (#2740).
		cut := zedEntrySpan(text, found)
		return closeEmptied(text[:cut[0]]+text[cut[1]:], keys), nil
	}
	return text[:found.valueOpen] + entryAtDepth(entry, len(keys)) + text[found.valueEnd:], nil
}

// walkJSONCKeys follows a dotted block key from the top-level object, and
// reports how many of its keys were found: a caller that has to create the rest
// needs to know where to put them.
func walkJSONCKeys(text string, open int, keys []string) (*zedSpan, int) {
	at := open + 1
	var block *zedSpan
	for i, key := range keys {
		found := zedFindKey(text, at, key)
		if found == nil {
			return nil, i
		}
		block, at = found, found.valueOpen+1
	}
	return block, len(keys)
}

// entryAtDepth re-indents an entry to the block it is going into.
// jsoncEntryText renders at the depth a single-key writer uses, so an entry two
// keys deep — openclaw's mcp.servers — came out aligned with the block above
// it, along with the brace that closes it.
func entryAtDepth(entry string, depth int) string {
	if depth <= 1 {
		return entry
	}
	return strings.ReplaceAll(entry, "\n    ", "\n"+strings.Repeat("  ", depth+1))
}

// closeEmptied puts a block deja emptied back the way it found it: writing an
// entry into `"servers": {}` opens the braces onto their own lines, and taking
// the entry out again left them there, so an install followed by an uninstall
// did not give the reader their file back.
//
// Whitespace only, checked on the raw text: a block holding a comment and
// nothing else is one the reader wrote something in, and it keeps its shape.
func closeEmptied(text string, keys []string) string {
	for i := len(keys); i > 0; i-- {
		open := zedTopLevelOpen(text)
		if open < 0 {
			return text
		}
		block, have := walkJSONCKeys(text, open, keys[:i])
		if block == nil || have < i {
			continue
		}
		body := text[block.valueOpen+1 : block.valueEnd-1]
		if body != "" && strings.TrimSpace(body) == "" {
			text = text[:block.valueOpen+1] + text[block.valueEnd-1:]
		}
	}
	return text
}

// nestedBlocks is the text of the keys that are missing, one inside the next,
// with the entry at the bottom.
func nestedBlocks(keys []string, id, entry, indent string) string {
	entry = strings.ReplaceAll(entry, "\n    ", "\n"+indent+strings.Repeat("  ", len(keys)))
	body := fmt.Sprintf("%q: %s", id, entry)
	for i := len(keys) - 1; i >= 0; i-- {
		inner := indent + strings.Repeat("  ", i+1)
		body = fmt.Sprintf("%q: {\n%s%s\n%s}", keys[i], inner, body, indent+strings.Repeat("  ", i))
	}
	return "\n" + indent + body
}

// blockComma is rootComma for a block rather than the whole file: no comma
// after a key inserted into an object that holds nothing else.
func blockComma(text string, block *zedSpan) string {
	if strings.TrimSpace(stripJSONComments(text[block.valueOpen+1:block.valueEnd-1])) == "" {
		return ""
	}
	return ","
}

// rootComma is the comma after a block inserted at the top of an object, or
// nothing when that object holds no other key.
//
// Unconditional, it wrote `{"mcpServers": {…},}` into a config whose only line
// was a comment — not JSON, so every later run refused the target with a
// message pointing at the reader's own comment. The same shape #2740 fixed one
// level down, for a block with no entries in it.
func rootComma(text string, open int) string {
	if strings.TrimSpace(stripJSONComments(text[open+1:])) == "}" {
		return ""
	}
	return ","
}

// jsoncEntryText renders an entry the way the writers build it, so the text
// path and the parsed path put the same thing in the file.
func jsoncEntryText(entry map[string]any) (string, error) {
	b, err := json.MarshalIndent(entry, "    ", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// writeJSONCEntry is the install path for a config edited as text.
//
// want is the entry the caller would have written through the parsed path, so
// the two paths put the same thing in the file: claude's entry carries a
// `type`, and building the entry here instead meant a commented .claude.json
// got one without it.
//
// What to write is decided the same way as for a config without them — the
// same block reader, the same merge onto an entry that is already there, the
// same notes — and only the writing is done by text, so the reader's comments,
// key order and formatting stay where they are. Deciding it twice is what left
// this path dropping an env block, flipping `disabled`, writing a second entry
// beside one under another name, and saying none of it (#2740).
func writeJSONCEntry(path string, old []byte, blockKey string, want map[string]any, uninstall bool) (installResult, error) {
	text := string(old)
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(text)), &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	// A key that is there but holds something else — null, a list, a string.
	// mcpBlock reads null as "no block", which is right where the writer can
	// replace the value; here the write is a text insert, so it would leave a
	// second key of the same name with the reader's value winning (#2740).
	keys := strings.Split(blockKey, ".")
	holder := root
	have := 0
	for _, key := range keys[:len(keys)-1] {
		v, present := holder[key]
		if !present {
			holder = nil
			break
		}
		next, isObject := v.(map[string]any)
		if !isObject {
			if uninstall {
				return installResult{Path: path, Action: "unchanged"}, nil
			}
			return installResult{}, fmt.Errorf("%s: %q is not an object deja can edit — left as it was", path, key)
		}
		holder = next
		have++
	}
	if holder != nil {
		if v, present := holder[keys[len(keys)-1]]; present {
			if _, isObject := v.(map[string]any); !isObject {
				if uninstall {
					return installResult{Path: path, Action: "unchanged"}, nil
				}
				return installResult{}, fmt.Errorf("%s: %q is not an object deja can edit — left as it was", path, blockKey)
			}
		}
	}
	if holder == nil {
		holder = map[string]any{}
	}
	m, _, err := mcpBlock(holder, keys[len(keys)-1], path)
	if err != nil {
		// A block that is not an object is a config deja does not understand,
		// and writing a second key of the same name would leave the reader's
		// value winning and deja unwired (#2399).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if m == nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		m = map[string]any{}
		// Every level deja writes, not only the last: an uninstall that knows
		// it created `mcp` as well as `servers` can leave the file as it found
		// it (#2783).
		for i := have; i < len(keys); i++ {
			noteBlockAdded(path, strings.Join(keys[:i+1], "."))
		}
	}
	key := dejaEntryKey(m)
	var note string
	var next string
	if uninstall {
		delete(m, key)
		removeAdoptedDejaEntries(path, blockKey, m)
		note = leftDejaEntriesNote(m)
		dropFrom := len(keys)
		if len(m) == 0 && blockWasAdded(path, blockKey) {
			dropFrom = len(keys) - 1
			forgetBlockAdded(path, blockKey)
			// And up, while each level holds nothing but the one below it and
			// deja is what put it there.
			holders := chainHolders(root, keys)
			for i := len(keys) - 2; i >= 0; i-- {
				prefix := strings.Join(keys[:i+1], ".")
				if len(holders[i+1]) != 1 || !blockWasAdded(path, prefix) {
					break
				}
				dropFrom = i
				forgetBlockAdded(path, prefix)
			}
		}
		next, err = jsoncSetEntry(text, blockKey, key, "", true, dropFrom)
	} else {
		var merged map[string]any
		merged, note = mergeDejaEntry(m[key], want)
		note = withOtherDejaEntries(note, m, key)
		var entry string
		entry, err = jsoncEntryText(merged)
		if err != nil {
			return installResult{}, err
		}
		next, err = jsoncSetEntry(text, blockKey, key, entry, false, len(keys))
	}
	if err != nil {
		return installResult{}, configParseError(path, err)
	}
	a, werr := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a, Note: note}, werr
}

// chainHolders is the object each key of a dotted block key lives in, so an
// uninstall can ask what a level would hold once the one below it is gone. A
// level that is not there yet is an empty map, which holds nothing.
func chainHolders(root map[string]any, keys []string) []map[string]any {
	out := make([]map[string]any, len(keys))
	at := root
	for i, key := range keys {
		out[i] = at
		next, _ := at[key].(map[string]any)
		if next == nil {
			next = map[string]any{}
		}
		at = next
	}
	return out
}

// jsoncSetFlag turns a boolean setting on inside a block, in a config that
// carries comments. The switch is one key deep — `hooksConfig.enabled` — and
// it lives in the same file the MCP entry does, so refusing it there meant a
// target that wrote half its wiring and then reported itself refused (#2744).
func jsoncSetFlag(text, blockKey, key string, value bool) (string, error) {
	open := zedTopLevelOpen(text)
	if open < 0 {
		return "", fmt.Errorf("does not look like a settings object; set %q by hand", blockKey+"."+key)
	}
	rendered := "false"
	if value {
		rendered = "true"
	}
	// A dotted block key, for a switch that lives deeper than the top level:
	// openclaw's is `hooks.internal.enabled` (#2811).
	keys := strings.Split(blockKey, ".")
	block, have := walkJSONCKeys(text, open, keys)
	if block == nil {
		at, comma, indent := open, rootComma(text, open), "  "
		if have > 0 {
			parent, _ := walkJSONCKeys(text, open, keys[:have])
			at, comma, indent = parent.valueOpen, blockComma(text, parent), strings.Repeat("  ", have+1)
		}
		insert := nestedBlocks(keys[have:], key, rendered, indent)
		if have > 0 && comma == "" && !strings.HasPrefix(text[at+1:], "\n") {
			insert += "\n" + strings.Repeat("  ", have)
		}
		return text[:at+1] + insert + comma + text[at+1:], nil
	}
	if at := jsoncScalarValue(text, block, key); at != nil {
		return text[:at[0]] + rendered + text[at[1]:], nil
	}
	tail := ""
	if rest := text[block.valueOpen+1:]; rest != "" && rest[0] != '\n' && rest[0] != '}' {
		tail = "\n   "
	}
	comma := ","
	if strings.TrimSpace(stripJSONComments(text[block.valueOpen+1:block.valueEnd-1])) == "" {
		comma, tail = "", "\n  "
	}
	insert := fmt.Sprintf("\n%s%q: %s%s%s", strings.Repeat("  ", len(keys)+1), key, rendered, comma, tail)
	return text[:block.valueOpen+1] + insert + text[block.valueOpen+1:], nil
}

// jsoncRemoveKey takes a scalar setting back out, and the blocks it was the
// last thing in when deja is what created them. It is the other half of
// jsoncSetFlag: a switch deja turned on has to go off the same way the entry
// beside it goes (#2811).
func jsoncRemoveKey(text, blockKey, key string, dropFrom int) (string, error) {
	open := zedTopLevelOpen(text)
	if open < 0 {
		return text, nil
	}
	keys := strings.Split(blockKey, ".")
	if dropFrom < len(keys) {
		chain, have := walkJSONCKeys(text, open, keys[:dropFrom+1])
		if chain == nil || have <= dropFrom {
			return text, nil
		}
		cut := zedEntrySpan(text, chain)
		return closeEmptied(text[:cut[0]]+text[cut[1]:], keys), nil
	}
	block, have := walkJSONCKeys(text, open, keys)
	if block == nil || have < len(keys) {
		return text, nil
	}
	at := jsoncScalarValue(text, block, key)
	if at == nil {
		return text, nil
	}
	// From the key's own quote to the end of its value, plus the comma behind
	// it and the whitespace in front, the way an entry is taken out.
	start := strings.LastIndex(text[:at[0]], `"`+key+`"`)
	if start < 0 {
		return text, nil
	}
	end := at[1]
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	tookComma := false
	if end < len(text) && text[end] == ',' {
		end++
		tookComma = true
	}
	for start > block.valueOpen+1 && (text[start-1] == ' ' || text[start-1] == '\t' || text[start-1] == '\n') {
		start--
	}
	// The last key in a block has no comma after it, and the one in front of it
	// is what would be left dangling — `{…,\n  }` is not JSON, and the file is
	// then refused on every later run with a message pointing at the reader's
	// own comment (#2740 again, one key over).
	if !tookComma {
		for i := start - 1; i > block.valueOpen; i-- {
			if c := text[i]; c == ' ' || c == '\t' || c == '\n' {
				continue
			} else if c == ',' {
				start = i
			}
			break
		}
	}
	return closeEmptied(text[:start]+text[end:], keys), nil
}

// jsoncScalarValue is where a scalar setting's value sits inside a block, or
// nil when the block does not have that key. Scalars, because zedFindKey looks
// for an object and a switch is a bare `true`.
func jsoncScalarValue(text string, block *zedSpan, key string) *[2]int {
	want := `"` + key + `"`
	depth := 0
	for i := block.valueOpen + 1; i < block.valueEnd-1; i++ {
		if j := zedSkipComment(text, i); j != i {
			i = j - 1
			continue
		}
		switch text[i] {
		case '"':
			end := zedStringEnd(text, i)
			if end < 0 {
				return nil
			}
			if depth == 0 && text[i:end] == want && jsoncIsKey(text, end) {
				v := end
				for v < len(text) && (text[v] == ' ' || text[v] == '\t' || text[v] == '\n' || text[v] == ':') {
					v++
				}
				// A scalar, and only a scalar: an object or a list under this
				// key would be spliced through the middle, leaving a file no
				// parser reads and an install that said it worked (#2745).
				if v >= block.valueEnd-1 || text[v] == '{' || text[v] == '[' {
					return nil
				}
				stop := v
				for stop < block.valueEnd-1 && text[stop] != ',' && text[stop] != '}' && text[stop] != '\n' {
					// A comment after the value belongs to the reader, not to
					// the value.
					if text[stop] == '/' && zedSkipComment(text, stop) != stop {
						break
					}
					stop++
				}
				for stop > v && (text[stop-1] == ' ' || text[stop-1] == '\t') {
					stop--
				}
				span := [2]int{v, stop}
				return &span
			}
			i = end - 1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return nil
}

// jsoncIsKey reports whether the string that ends at `end` is a key rather
// than a value. A string value equal to the key's name is not the key, and
// writing over it left `{"label": "enabled"true}` behind (#2745).
func jsoncIsKey(text string, end int) bool {
	for i := end; i < len(text); i++ {
		if j := zedSkipComment(text, i); j != i {
			i = j - 1
			continue
		}
		switch text[i] {
		case ' ', '\t', '\n', '\r':
		case ':':
			return true
		default:
			return false
		}
	}
	return false
}

// readableStrictJSON refuses before anything is written when a config the
// target is about to edit cannot be parsed.
//
// A target that wires more than one file wrote the first and refused the
// second, so a run reported as refused had already changed a config and left a
// snapshot beside it (#2744). The writers that edit an entry take a file with
// comments; the hook writers cannot, and this is where that is found out —
// before the first write rather than after it.
func readableStrictJSON(paths ...string) error {
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		// An object, the way the writers read it: `null` decodes into a nil
		// map without an error and the writer then panics assigning into it,
		// and a list refuses one write too late.
		var probe map[string]any
		if err := json.Unmarshal(b, &probe); err != nil {
			return configParseError(path, err)
		}
		if probe == nil {
			return configParseError(path, errors.New("the file holds null, not an object"))
		}
	}
	return nil
}

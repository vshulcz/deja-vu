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
func jsoncSetEntry(text, blockKey, id, entry string, uninstall, dropBlock bool) (string, error) {
	open := zedTopLevelOpen(text)
	if open < 0 {
		return "", fmt.Errorf("does not look like a settings object; add %q by hand", blockKey)
	}
	block := zedFindKey(text, open+1, blockKey)
	if block == nil {
		if uninstall {
			return text, nil
		}
		insert := fmt.Sprintf("\n  %q: {\n    %q: %s\n  },", blockKey, id, entry)
		return text[:open+1] + insert + text[open+1:], nil
	}
	found := zedFindKey(text, block.valueOpen+1, id)
	if found == nil {
		if uninstall {
			return text, nil
		}
		// A newline after the entry when the block was written on one line, so
		// the reader's first entry keeps a line of its own rather than being
		// pushed against deja's closing brace.
		tail := ""
		if rest := text[block.valueOpen+1:]; rest != "" && rest[0] != '\n' && rest[0] != '}' {
			tail = "\n   "
		}
		// And no comma into an empty block: `{"deja": {…},}` is not JSON, and
		// the file was then refused on every later run with a message pointing
		// at the reader's comment (#2740).
		comma := ","
		if strings.TrimSpace(stripJSONComments(text[block.valueOpen+1:block.valueEnd-1])) == "" {
			comma, tail = "", "\n  "
		}
		insert := fmt.Sprintf("\n    %q: %s%s%s", id, entry, comma, tail)
		return text[:block.valueOpen+1] + insert + text[block.valueOpen+1:], nil
	}
	if uninstall {
		if dropBlock {
			return zedDropEntry(text, block, found), nil
		}
		// The entry alone. zedDropEntry takes the block with the last entry in
		// it, which is right for a block deja created and wrong for one the
		// reader wrote — the rule #2604 and #2583 settled for the other
		// writers (#2740).
		cut := zedEntrySpan(text, found)
		return text[:cut[0]] + text[cut[1]:], nil
	}
	return text[:found.valueOpen] + entry + text[found.valueEnd:], nil
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
	if v, present := root[blockKey]; present {
		if _, isObject := v.(map[string]any); !isObject {
			if uninstall {
				return installResult{Path: path, Action: "unchanged"}, nil
			}
			return installResult{}, fmt.Errorf("%s: %q is not an object deja can edit — left as it was", path, blockKey)
		}
	}
	m, _, err := mcpBlock(root, blockKey, path)
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
		noteBlockAdded(path, blockKey)
	}
	key := dejaEntryKey(m)
	var note string
	var next string
	if uninstall {
		delete(m, key)
		removeAdoptedDejaEntries(path, blockKey, m)
		note = leftDejaEntriesNote(m)
		if len(m) == 0 && blockWasAdded(path, blockKey) {
			forgetBlockAdded(path, blockKey)
		}
		next, err = jsoncSetEntry(text, blockKey, key, "", true, blockWasAdded(path, blockKey))
	} else {
		var merged map[string]any
		merged, note = mergeDejaEntry(m[key], want)
		note = withOtherDejaEntries(note, m, key)
		var entry string
		entry, err = jsoncEntryText(merged)
		if err != nil {
			return installResult{}, err
		}
		next, err = jsoncSetEntry(text, blockKey, key, entry, false, false)
	}
	if err != nil {
		return installResult{}, configParseError(path, err)
	}
	a, werr := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a, Note: note}, werr
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
	block := zedFindKey(text, open+1, blockKey)
	if block == nil {
		insert := fmt.Sprintf("\n  %q: {\n    %q: %s\n  },", blockKey, key, rendered)
		return text[:open+1] + insert + text[open+1:], nil
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
	insert := fmt.Sprintf("\n    %q: %s%s%s", key, rendered, comma, tail)
	return text[:block.valueOpen+1] + insert + text[block.valueOpen+1:], nil
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

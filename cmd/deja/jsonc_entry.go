package main

import (
	"encoding/json"
	"fmt"
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
func jsoncSetEntry(text, blockKey, id, entry string, uninstall bool) (string, error) {
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
		insert := fmt.Sprintf("\n    %q: %s,%s", id, entry, tail)
		return text[:block.valueOpen+1] + insert + text[block.valueOpen+1:], nil
	}
	if uninstall {
		return zedDropEntry(text, block, found), nil
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

// writeJSONCEntry is the install path for a config that carries comments: the
// entry is edited as text and every other byte stays where the reader put it.
func writeJSONCEntry(path string, old []byte, blockKey, exe string, uninstall bool) (installResult, error) {
	command, args := mcpCommandArgs(exe)
	entry, err := jsoncEntryText(map[string]any{"command": command, "args": args})
	if err != nil {
		return installResult{}, err
	}
	next, err := jsoncSetEntry(string(old), blockKey, "deja", entry, uninstall)
	if err != nil {
		return installResult{}, configParseError(path, err)
	}
	a, werr := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a}, werr
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Zed's agent reads MCP servers from the same settings.json a person edits by
// hand, and that file is JSONC: Zed ships it with a comment header and tolerates
// trailing commas. Decoding it and writing the result back — what
// installMCPJSON does for the harnesses whose MCP config is a file of its own —
// would either fail on the first `//` or silently delete every comment the
// reader wrote. So this edits the text and leaves the rest of the file byte for
// byte as it was, in the spirit of #1285.
const zedServerKey = "context_servers"

// zedServerID is the id both halves of deja use in Zed: the extension declares
// its context server under it, and `deja install zed` writes its own entry
// under the same one. Zed keys servers by id and takes them unique across
// settings and the extension registry, so one id is one server however the user
// arrived — CLI first, extension first, or both. Two ids would be two servers
// each starting `deja mcp`, which is the shape this used to have.
const zedServerID = "deja-context-server"

// zedLegacyServerID is what deja wrote before that. Installing again renames
// it, so a machine that ended up with both stops running deja twice without
// anyone having to be told about it.
const zedLegacyServerID = "deja"

// installZedMCP adds or removes deja's entry in Zed's settings.
func installZedMCP(path, exe string, uninstall bool) (installResult, error) {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	entry, err := zedEntryJSON(exe)
	if err != nil {
		return installResult{}, err
	}

	if len(strings.TrimSpace(string(old))) == 0 {
		// Nothing to preserve. Uninstalling from a file that does not exist
		// must not create one (#676).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return installResult{}, err
		}
		fresh := fmt.Sprintf("{\n  %q: {\n    %q: %s\n  }\n}\n", zedServerKey, zedServerID, entry)
		a, werr := writeIfChanged(path, old, []byte(fresh))
		return installResult{Path: path, Action: a}, werr
	}

	next, err := zedSettingsWith(string(old), entry, uninstall)
	if err != nil {
		return installResult{}, err
	}
	a, werr := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a}, werr
}

// zedEntryJSON is the server object, indented to sit at the depth Zed's own
// settings use.
func zedEntryJSON(exe string) (string, error) {
	command, args := mcpCommandArgs(exe)
	b, err := json.MarshalIndent(map[string]any{"command": command, "args": args}, "    ", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// zedSettingsWith returns the settings text with deja's entry added, replaced
// or removed. The file is not re-serialised: every byte outside the entry —
// comments, key order, indentation, trailing commas — survives untouched.
func zedSettingsWith(text, entry string, uninstall bool) (string, error) {
	open := zedTopLevelOpen(text)
	if open < 0 {
		return "", fmt.Errorf("zed: %s does not look like a settings object; add %q by hand", "settings.json", zedServerKey)
	}
	// The id deja used to write is dropped first, in both directions: on
	// install it becomes the current one, on uninstall it goes with it. A file
	// carrying both ran two servers.
	if legacy := zedLocate(text, zedLegacyServerID); legacy != nil {
		text = zedDropEntry(text, legacy.block, legacy.entry)
	}

	found := zedLocate(text, zedServerID)
	if found == nil {
		if uninstall {
			return text, nil
		}
		if block := zedFindKey(text, open+1, zedServerKey); block != nil {
			insert := fmt.Sprintf("\n    %q: %s,", zedServerID, entry)
			return text[:block.valueOpen+1] + insert + text[block.valueOpen+1:], nil
		}
		open = zedTopLevelOpen(text)
		insert := fmt.Sprintf("\n  %q: {\n    %q: %s\n  },", zedServerKey, zedServerID, entry)
		return text[:open+1] + insert + text[open+1:], nil
	}
	// The same id is the extension's, and Zed writes its own entry there when
	// the extension is installed. That entry is the user's, not ours: leave it
	// exactly as it is, in both directions.
	if !zedEntryIsOurs(text, found.entry) {
		return text, nil
	}
	if uninstall {
		return zedDropEntry(text, found.block, found.entry), nil
	}
	return text[:found.entry.valueOpen] + entry + text[found.entry.valueEnd:], nil
}

// zedLocation is deja's entry and the context_servers block holding it.
type zedLocation struct{ block, entry *zedSpan }

func zedLocate(text, id string) *zedLocation {
	open := zedTopLevelOpen(text)
	if open < 0 {
		return nil
	}
	block := zedFindKey(text, open+1, zedServerKey)
	if block == nil {
		return nil
	}
	entry := zedFindKey(text, block.valueOpen+1, id)
	if entry == nil {
		return nil
	}
	return &zedLocation{block: block, entry: entry}
}

// zedEntryIsOurs reports whether an entry is the one deja writes — a command to
// run — rather than the extension's, which carries `settings` and no command.
//
// The key is looked for on its own rather than through zedFindKey: that one
// wants an object value, and `"command"` holds a string. Asking it here reads
// as a working check and quietly answers "no" for every entry deja ever wrote.
func zedEntryIsOurs(text string, entry *zedSpan) bool {
	return zedHasKey(text, entry.valueOpen+1, "command")
}

// zedHasKey reports whether a key exists at the current object depth. Deeper
// objects are skipped, so a "command" nested in some other setting inside the
// entry is not mistaken for the entry's own.
func zedHasKey(text string, from int, key string) bool {
	want := `"` + key + `"`
	depth := 0
	for i := from; i < len(text); i++ {
		if j := zedSkipComment(text, i); j != i {
			i = j - 1
			continue
		}
		switch text[i] {
		case '"':
			end := zedStringEnd(text, i)
			if end < 0 {
				return false
			}
			if depth == 0 && text[i:end] == want && jsoncIsKey(text, end) {
				return true
			}
			i = end - 1
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return false // end of the object being searched
			}
			depth--
		}
	}
	return false
}

// zedDropEntry removes one server entry, and the context_servers block with it
// when nothing else was in there: a settings file that gains an empty
// "context_servers": {} after an uninstall is not the file the reader had
// before install. Anything else left — another server, or a comment someone
// wrote — keeps the block.
func zedDropEntry(text string, block, entry *zedSpan) string {
	cut := zedEntrySpan(text, entry)
	if strings.TrimSpace(text[block.valueOpen+1:cut[0]]+text[cut[1]:block.valueEnd-1]) == "" {
		cut = zedEntrySpan(text, block)
	}
	return text[:cut[0]] + text[cut[1]:]
}

// zedSpan is where a key and its object value sit in the text.
type zedSpan struct {
	keyStart  int // the opening quote of the key
	valueOpen int // the '{' that opens the value
	valueEnd  int // one past the '}' that closes it
}

// zedEntrySpan widens a key's span to what removing it should take with it:
// the whitespace in front of it and the comma behind, so the object left
// behind is still well formed.
func zedEntrySpan(text string, s *zedSpan) [2]int {
	start := s.keyStart
	for start > 0 && (text[start-1] == ' ' || text[start-1] == '\t' || text[start-1] == '\n') {
		start--
	}
	end := s.valueEnd
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	if end < len(text) && text[end] == ',' {
		end++
	}
	return [2]int{start, end}
}

// zedTopLevelOpen is the '{' that opens the settings object, skipping the
// comment header Zed ships.
func zedTopLevelOpen(text string) int {
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '{':
			return i
		case text[i] == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case text[i] == '/' && i+1 < len(text) && text[i+1] == '*':
			i += 2
			for i+1 < len(text) && (text[i] != '*' || text[i+1] != '/') {
				i++
			}
			i++
		case text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r':
		default:
			return -1
		}
	}
	return -1
}

// zedFindKey locates a key at the current object depth and returns where its
// object value begins and ends. Keys nested deeper are skipped, so a "deja"
// under some other setting is not mistaken for ours.
func zedFindKey(text string, from int, key string) *zedSpan {
	want := `"` + key + `"`
	depth := 0
	for i := from; i < len(text); i++ {
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
			if depth == 0 && text[i:end] == want {
				open := zedValueOpen(text, end)
				if open < 0 {
					return nil
				}
				close := zedObjectEnd(text, open)
				if close < 0 {
					return nil
				}
				return &zedSpan{keyStart: i, valueOpen: open, valueEnd: close}
			}
			i = end - 1
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return nil // end of the object we were searching
			}
			depth--
		}
	}
	return nil
}

// zedSkipComment returns the index one past a comment starting at i, or i
// itself when there is none. Both shapes, in one place: handling `//` in three
// scanners and forgetting `/* */` in the fourth is how a settings file with a
// block comment gets its object end read wrong and its contents mangled.
func zedSkipComment(text string, i int) int {
	if i+1 >= len(text) || text[i] != '/' {
		return i
	}
	switch text[i+1] {
	case '/':
		for i < len(text) && text[i] != '\n' {
			i++
		}
		return i
	case '*':
		i += 2
		for i+1 < len(text) && (text[i] != '*' || text[i+1] != '/') {
			i++
		}
		return i + 2
	}
	return i
}

// zedStringEnd is one past the closing quote of the string starting at i.
func zedStringEnd(text string, i int) int {
	for j := i + 1; j < len(text); j++ {
		switch text[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return -1
}

// zedValueOpen is the '{' after a key, or -1 when the value is not an object.
func zedValueOpen(text string, from int) int {
	seenColon := false
	for i := from; i < len(text); i++ {
		if j := zedSkipComment(text, i); j != i {
			i = j - 1
			continue
		}
		switch text[i] {
		case ':':
			seenColon = true
		case '{':
			if !seenColon {
				return -1
			}
			return i
		case ' ', '\t', '\n', '\r':
		default:
			return -1
		}
	}
	return -1
}

// zedObjectEnd is one past the '}' matching the '{' at open.
func zedObjectEnd(text string, open int) int {
	depth := 0
	for i := open; i < len(text); i++ {
		if j := zedSkipComment(text, i); j != i {
			i = j - 1
			continue
		}
		switch text[i] {
		case '"':
			end := zedStringEnd(text, i)
			if end < 0 {
				return -1
			}
			i = end - 1
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

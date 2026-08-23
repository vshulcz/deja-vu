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

	// The Zed extension declares a context server of its own, and Zed records
	// it in this same file under its extension id. Adding ours next to it
	// starts `deja mcp` twice and shows the agent every tool twice, so the
	// extension wins: it is the thing the user installed from Zed's UI.
	if !uninstall && zedExtensionPresent(string(old)) {
		return installResult{Path: path, Action: "skipped: the Zed extension already provides it"}, nil
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
		fresh := fmt.Sprintf("{\n  %q: {\n    \"deja\": %s\n  }\n}\n", zedServerKey, entry)
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
	block := zedFindKey(text, open+1, zedServerKey)
	if block == nil {
		if uninstall {
			return text, nil
		}
		insert := fmt.Sprintf("\n  %q: {\n    \"deja\": %s\n  },", zedServerKey, entry)
		return text[:open+1] + insert + text[open+1:], nil
	}
	inner := zedFindKey(text, block.valueOpen+1, "deja")
	if inner == nil {
		if uninstall {
			return text, nil
		}
		insert := fmt.Sprintf("\n    \"deja\": %s,", entry)
		return text[:block.valueOpen+1] + insert + text[block.valueOpen+1:], nil
	}
	if uninstall {
		cut := zedEntrySpan(text, inner)
		// If deja was the only server, the key goes too: a settings file that
		// gains an empty "context_servers": {} after an uninstall is not the
		// file the reader had before install. Anything else left in there —
		// another server, or a comment someone wrote — keeps the block.
		if strings.TrimSpace(text[block.valueOpen+1:cut[0]]+text[cut[1]:block.valueEnd-1]) == "" {
			cut = zedEntrySpan(text, block)
		}
		return text[:cut[0]] + text[cut[1]:], nil
	}
	return text[:inner.valueOpen] + entry + text[inner.valueEnd:], nil
}

// zedExtensionPresent reports whether Zed's own extension already declares the
// server. Zed writes the extension's id into context_servers when the user
// installs it, so the id in the settings is the signal — the extension
// directory moved between Zed versions, the settings key did not.
func zedExtensionPresent(text string) bool {
	open := zedTopLevelOpen(text)
	if open < 0 {
		return false
	}
	block := zedFindKey(text, open+1, zedServerKey)
	if block == nil {
		return false
	}
	return zedFindKey(text, block.valueOpen+1, zedExtensionServerID) != nil
}

// zedExtensionServerID is the id in extensions/zed/extension.toml. Zed keys the
// settings entry by it, so the two have to stay the same string.
const zedExtensionServerID = "deja-context-server"

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

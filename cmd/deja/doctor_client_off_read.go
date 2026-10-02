package main

import (
	"encoding/json"
	"strings"
)

type jsonKeyValue struct {
	key   string
	value any
}

// jsonObjectInOrder is the top-level object at key in a JSON or JSONC
// config, its entries in the order the file has them, for a client that
// lets a later key overrule an earlier one. Nil when it cannot be read.
func jsonObjectInOrder(b []byte, key string) []jsonKeyValue {
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &top) != nil || top[key] == nil {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(top[key])))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var out []jsonKeyValue
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil
		}
		k, _ := t.(string)
		var v any
		if dec.Decode(&v) != nil {
			return nil
		}
		out = append(out, jsonKeyValue{k, v})
	}
	return out
}

// Small readers for the client switches in doctor_client_off.go. They answer
// "is this key set to that", and a file they cannot read is a file with no
// switch in it: doctor's other rows say when a config is broken.

func readJSONConfig(p string) map[string]any {
	b, err := readConfig(p)
	if err != nil {
		return nil
	}
	m, _ := parseJSONValue(b).(map[string]any)
	return m
}

func parseJSONValue(b []byte) any {
	var v any
	if json.Unmarshal([]byte(jsoncToJSON(strings.TrimPrefix(string(b), string(utf8BOM)))), &v) != nil {
		return nil
	}
	return v
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func jsonAt(root map[string]any, keys ...string) any {
	var v any = root
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func jsonStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// nameIsDeja is a server or plugin name as clients compare them: exactly.
// Gemini lowercases ids only in the commands that write them, and copilot,
// hermes, goose and claude-code look names up as they are.
func nameIsDeja(s string) bool {
	return s == "deja"
}

func anyDeja(list []string) bool {
	for _, s := range list {
		if nameIsDeja(s) {
			return true
		}
	}
	return false
}

func listHasDeja(v any) bool {
	return anyDeja(jsonStrings(v))
}

// yamlSwitchOff is the note for a `<keys>: false` in a YAML config.
func yamlSwitchOff(p, client string, keys ...string) string {
	b, err := readConfig(p)
	if err != nil {
		return ""
	}
	if v, _, _ := yamlLookup(string(b), keys...); v == "false" {
		return mcpOffNote(client, "`"+strings.Join(keys, ".")+": false`", p)
	}
	return ""
}

// yamlLookup finds the key at keys in a block-style YAML document and returns
// its scalar, or the items of the list under it, block or flow. Enough for the
// mappings and lists clients keep their switches in; anchors, multi-line
// scalars and lists of mappings are not switches.
func yamlLookup(text string, keys ...string) (scalar string, items []string, found bool) {
	type frame struct {
		indent int
		key    string
	}
	var stack []frame
	at := func() bool {
		if len(stack) != len(keys) {
			return false
		}
		for i, f := range stack {
			if f.key != keys[i] {
				return false
			}
		}
		return true
	}
	// quote is the quote a scalar opened and has not closed yet: the lines
	// until it closes are text, not keys.
	var quote byte
	content := false
	for _, raw := range strings.Split(strings.TrimPrefix(text, string(utf8BOM)), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if quote != 0 {
			if yamlQuoteEnd(line, quote) >= 0 {
				quote = 0
			}
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" || t[0] == '#' {
			continue
		}
		if t == "---" || strings.HasPrefix(t, "--- ") {
			// A second document: the clients' loaders refuse the file
			// rather than read either, so stop at the first.
			if content {
				break
			}
			continue
		}
		content = true
		w := yamlIndentWidth(line)
		if t == "-" || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "-\t") {
			// An item may sit at its key's own indent.
			for len(stack) > 0 && stack[len(stack)-1].indent > w {
				stack = stack[:len(stack)-1]
			}
			quote = yamlOpenQuote(strings.TrimSpace(t[1:]))
			if found && at() {
				items = append(items, hermesYAMLScalar(t[1:]))
			}
			continue
		}
		key, rest, ok := hermesKeyLine(line)
		if !ok {
			continue
		}
		quote = yamlOpenQuote(strings.TrimSpace(rest))
		for len(stack) > 0 && stack[len(stack)-1].indent >= w {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, frame{w, hermesKeyName(key)})
		if at() {
			// The last one wins, as YAML loaders read a repeated key. A
			// flow list over several lines is not read, rather than read
			// as empty.
			v := strings.TrimSpace(stripYAMLComment(rest))
			found, scalar, items = !strings.HasPrefix(v, "[") || strings.HasSuffix(v, "]"), "", nil
			if strings.HasPrefix(v, "[") {
				for _, s := range strings.Split(strings.Trim(v, "[]"), ",") {
					if s = hermesYAMLScalar(s); s != "" {
						items = append(items, s)
					}
				}
			} else {
				scalar = hermesYAMLScalar(v)
			}
		}
	}
	return scalar, items, found
}

// yamlOpenQuote is the quote a value starts and does not close on its line,
// 0 when it closes or is not quoted.
func yamlOpenQuote(v string) byte {
	if v == "" || v[0] != '"' && v[0] != '\'' {
		return 0
	}
	if yamlQuoteEnd(v[1:], v[0]) >= 0 {
		return 0
	}
	return v[0]
}

// yamlQuoteEnd is the index of the quote that closes a scalar in s, or -1: a
// `"` not escaped by a backslash, a `'` not doubled.
func yamlQuoteEnd(s string, quote byte) int {
	for i := 0; i < len(s); i++ {
		switch {
		case quote == '"' && s[i] == '\\':
			i++
		case s[i] == quote && quote == '\'' && i+1 < len(s) && s[i+1] == '\'':
			i++
		case s[i] == quote:
			return i
		}
	}
	return -1
}

// tomlCodeLines is text's lines with those inside a multi-line string
// blanked, so a line of a string never reads as a key or a table.
func tomlCodeLines(text string) []string {
	lines := strings.Split(text, "\n")
	open := ""
	for i, line := range lines {
		if open != "" {
			lines[i] = ""
			if strings.Contains(line, open) {
				open = ""
			}
			continue
		}
		for _, d := range []string{`"""`, `'''`} {
			if strings.Count(tomlCode(line), d)%2 == 1 {
				open = d
			}
		}
	}
	return lines
}

// dshRowDisabled reports whether the row with this id in a dsh patch layer
// carries `disabled: true`, the switch dsh reads per row.
func dshRowDisabled(text, id string) bool {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "- ") {
			continue
		}
		dash := yamlIndentWidth(line)
		inRow, off := false, false
		check := func(kv string) {
			k, v, _ := strings.Cut(kv, ":")
			switch strings.TrimSpace(k) {
			case "id":
				inRow = hermesYAMLScalar(v) == id
			case "disabled":
				off = hermesYAMLScalar(v) == "true"
			}
		}
		check(strings.TrimSpace(t[2:]))
		for _, next := range lines[i+1:] {
			nt := strings.TrimSpace(next)
			if nt == "" || nt[0] == '#' {
				continue
			}
			if yamlIndentWidth(next) <= dash {
				break
			}
			if yamlIndentWidth(next) == dash+2 {
				check(nt)
			}
		}
		if inRow {
			return off
		}
	}
	return false
}

// tomlDejaEntriesOff reports whether every [mcp_servers.X] block that runs
// deja says `enabled = false`. One left on is enough for recall to work.
func tomlDejaEntriesOff(text string) bool {
	lines := tomlCodeLines(text)
	off := false
	for _, b := range tomlMCPBlocks(strings.Join(lines, "\n")) {
		if b.key != "deja" && !tomlBlockRunsDeja(lines, b) {
			continue
		}
		enabled := ""
		for i := b.start + 1; i < b.end; i++ {
			if k, v, ok := tomlLineKeyValue(lines[i]); ok && k == "enabled" {
				enabled = v
			}
		}
		if enabled != "false" {
			return false
		}
		off = true
	}
	return off
}

// tomlTableValue is the raw value of key under [table], or of `table.key` at
// the top level; "" when it is not set.
func tomlTableValue(text, table, key string) string {
	current, value := "", ""
	for _, line := range tomlCodeLines(text) {
		code := tomlCode(line)
		if strings.HasPrefix(code, "[") {
			current = strings.TrimSpace(strings.Trim(code, "[]"))
			continue
		}
		k, v, ok := tomlLineKeyValue(line)
		if !ok {
			continue
		}
		if current == table && k == key || current == "" && k == table+"."+key {
			value = v
		}
	}
	return value
}

// tomlTopLevelStrings is the strings of an array set before the first table.
func tomlTopLevelStrings(text, key string) []string {
	lines := tomlCodeLines(text)
	for i, line := range lines {
		if strings.HasPrefix(tomlCode(line), "[") {
			return nil
		}
		if k, v, ok := tomlLineKeyValue(line); ok && k == key {
			v, _ = tomlArrayValue(lines, i, len(lines), v)
			return tomlStringValues(v)
		}
	}
	return nil
}

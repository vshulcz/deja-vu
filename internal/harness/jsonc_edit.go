package harness

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

func detectIndentUnit(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if len(line) == 0 {
			continue
		}
		if line[0] == '\t' {
			return "\t"
		}
		if line[0] == ' ' {
			n := 0
			for n < len(line) && line[n] == ' ' {
				n++
			}
			if n < len(line) {
				return line[:n]
			}
		}
	}
	return "  "
}

func lineIndentAt(s string, pos int) string {
	lineStart := strings.LastIndexByte(s[:pos], '\n') + 1
	end := lineStart
	for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
		end++
	}
	return s[lineStart:end]
}

func formatValueAtIndent(v any, indent, unit string) (string, error) {
	b, err := json.MarshalIndent(v, "", unit)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(b), "\n", "\n"+indent), nil
}

func setJSONCEntry(raw []byte, blockPath, entryKey string, entry map[string]any, recordAdded func(string)) ([]byte, error) {
	s := string(normalizeLF(raw))
	unit := detectIndentUnit(s)
	objStart, objEnd, err := findRootObjectBounds(s)
	if err != nil {
		return nil, err
	}
	parts := splitDotPath(blockPath)
	curStart, curEnd := objStart, objEnd
	var walked []string
	var entries []jsoncEntry

	for idx, part := range parts {
		entries, err = parseObjectEntries(s, curStart, curEnd)
		if err != nil {
			return nil, err
		}
		var found *jsoncEntry
		for i := range entries {
			if entries[i].key == part {
				found = &entries[i]
				break
			}
		}
		walked = append(walked, part)
		if found == nil {
			nested := buildNestedEntry(parts[idx+1:], entryKey, entry)
			if recordAdded != nil {
				for k := idx; k < len(parts); k++ {
					recordAdded(joinDotPath(parts[:k+1]))
				}
			}
			out, iErr := insertObjectEntry(s, curStart, curEnd, entries, part, nested, unit)
			return []byte(out), iErr
		}
		if s[found.valStart] != '{' {
			return nil, fmt.Errorf("%s is not an object", joinDotPath(walked))
		}
		curStart, curEnd = found.valStart, found.valEnd
	}

	entries, err = parseObjectEntries(s, curStart, curEnd)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.key == entryKey {
			indent := lineIndentAt(s, e.keyStart)
			rendered, fErr := formatValueAtIndent(entry, indent, unit)
			if fErr != nil {
				return nil, fErr
			}
			return []byte(s[:e.valStart] + rendered + s[e.valEnd:]), nil
		}
	}
	out, iErr := insertObjectEntry(s, curStart, curEnd, entries, entryKey, entry, unit)
	return []byte(out), iErr
}

func buildNestedEntry(rem []string, entryKey string, entry map[string]any) map[string]any {
	cur := map[string]any{entryKey: entry}
	for _, seg := range slices.Backward(rem) {
		cur = map[string]any{seg: cur}
	}
	return cur
}

func insertObjectEntry(s string, objStart, objEnd int, entries []jsoncEntry, key string, val any, unit string) (string, error) {
	containerIndent := lineIndentAt(s, objStart)
	memberIndent := containerIndent + unit
	if len(entries) > 0 {
		memberIndent = lineIndentAt(s, entries[0].keyStart)
	}
	rendered, err := formatValueAtIndent(val, memberIndent, unit)
	if err != nil {
		return "", err
	}
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	pair := memberIndent + string(keyJSON) + ": " + rendered

	if len(entries) == 0 {
		inner := "\n" + pair + "\n" + containerIndent
		return s[:objStart+1] + inner + s[objEnd-1:], nil
	}
	last := entries[len(entries)-1]
	if last.commaPos >= 0 {
		insertAt := last.commaPos + 1
		return s[:insertAt] + "\n" + pair + "," + s[insertAt:], nil
	}
	return s[:last.valEnd] + ",\n" + pair + s[last.valEnd:], nil
}

func removeJSONCKeys(
	raw []byte,
	blockPath string,
	keysToRemove []string,
	wasAdded func(string) bool,
	forgetAdded func(string),
) ([]byte, []string, error) {
	s := string(normalizeLF(raw))
	objStart, objEnd, err := findRootObjectBounds(s)
	if err != nil {
		return nil, nil, err
	}
	parts := splitDotPath(blockPath)
	var chain []jsoncParentFrame
	curStart, curEnd := objStart, objEnd
	for idx, part := range parts {
		entries, pErr := parseObjectEntries(s, curStart, curEnd)
		if pErr != nil {
			return nil, nil, pErr
		}
		var found *jsoncEntry
		for i := range entries {
			if entries[i].key == part {
				found = &entries[i]
				break
			}
		}
		if found == nil || s[found.valStart] != '{' {
			return raw, nil, nil
		}
		chain = append(chain, jsoncParentFrame{path: joinDotPath(parts[:idx+1]), key: part})
		curStart, curEnd = found.valStart, found.valEnd
	}

	removeSet := map[string]bool{}
	for _, k := range keysToRemove {
		if k != "" {
			removeSet[k] = true
		}
	}
	var removed []string
	for {
		entries, pErr := parseObjectEntries(s, curStart, curEnd)
		if pErr != nil {
			return nil, nil, pErr
		}
		idx := -1
		for i, e := range entries {
			if removeSet[e.key] {
				idx = i
				removed = append(removed, e.key)
				delete(removeSet, e.key)
				break
			}
		}
		if idx < 0 {
			break
		}
		deltaLen := len(s)
		s = cutJSONCEntry(s, entries, idx)
		curEnd -= deltaLen - len(s)
	}
	if len(removed) == 0 {
		return raw, nil, nil
	}

	s = collapseEmptyJSONCParents(s, parts, chain, wasAdded, forgetAdded)
	return []byte(s), removed, nil
}

type jsoncParentFrame struct {
	path string
	key  string
}

func collapseEmptyJSONCParents(
	s string,
	parts []string,
	chain []jsoncParentFrame,
	wasAdded func(string) bool,
	forgetAdded func(string),
) string {
	if wasAdded == nil {
		return s
	}
	for depth, frame := range slices.Backward(chain) {
		cStart, cEnd, found := findNestedObjectSpan(s, parts[:depth+1])
		if !found {
			break
		}
		entries, err := parseObjectEntries(s, cStart, cEnd)
		if err != nil || len(entries) > 0 || !wasAdded(frame.path) {
			break
		}
		pStart, pEnd, ok := findNestedObjectSpan(s, parts[:depth])
		if !ok {
			break
		}
		pEntries, pErr := parseObjectEntries(s, pStart, pEnd)
		if pErr != nil {
			break
		}
		for i, e := range pEntries {
			if e.key == frame.key {
				s = cutJSONCEntry(s, pEntries, i)
				if forgetAdded != nil {
					forgetAdded(frame.path)
				}
				break
			}
		}
	}
	return s
}

func findNestedObjectSpan(s string, parts []string) (int, int, bool) {
	start, end, err := findRootObjectBounds(s)
	if err != nil {
		return 0, 0, false
	}
	for _, p := range parts {
		entries, err := parseObjectEntries(s, start, end)
		if err != nil {
			return 0, 0, false
		}
		matched := false
		for _, e := range entries {
			if e.key == p && s[e.valStart] == '{' {
				start, end = e.valStart, e.valEnd
				matched = true
				break
			}
		}
		if !matched {
			return 0, 0, false
		}
	}
	return start, end, true
}

func cutJSONCEntry(s string, entries []jsoncEntry, idx int) string {
	e := entries[idx]
	delStart := e.keyStart
	lineStart := strings.LastIndexByte(s[:delStart], '\n') + 1
	onlyWSBefore := strings.TrimSpace(s[lineStart:delStart]) == ""
	delEnd := e.entryEnd
	if onlyWSBefore {
		delStart = lineStart
		if delEnd < len(s) {
			j := delEnd
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			if j < len(s) && s[j] == '\n' && strings.TrimSpace(s[delEnd:j]) == "" {
				delEnd = j + 1
			}
		}
	} else {
		for delEnd < len(s) && (s[delEnd] == ' ' || s[delEnd] == '\t') {
			delEnd++
		}
	}
	s = s[:delStart] + s[delEnd:]
	if idx == len(entries)-1 && idx > 0 && e.commaPos < 0 && entries[idx-1].commaPos >= 0 {
		cpos := entries[idx-1].commaPos
		if cpos < len(s) && s[cpos] == ',' {
			s = s[:cpos] + s[cpos+1:]
		}
	}
	return s
}

func splitDotPath(path string) []string {
	if strings.TrimSpace(path) == "" {
		return []string{"mcpServers"}
	}
	parts := strings.Split(strings.ReplaceAll(path, `\.`, "\x00"), ".")
	for i, part := range parts {
		parts[i] = strings.ReplaceAll(part, "\x00", ".")
	}
	return parts
}

func joinDotPath(parts []string) string {
	escaped := make([]string, len(parts))
	for i, part := range parts {
		escaped[i] = strings.ReplaceAll(part, ".", `\.`)
	}
	return strings.Join(escaped, ".")
}

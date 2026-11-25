package harness

import (
	"sort"
	"strconv"
	"strings"
)

func collectPrefixBlocks(blocks []tomlBlock, targetSegs []string) []tomlBlock {
	var drop []tomlBlock
	for _, b := range blocks {
		if !b.arrayTable && tomlPathHasPrefix(b.headerPath, targetSegs) {
			drop = append(drop, b)
		}
	}
	return drop
}

func shouldRemoveAdoptedTOMLKey(ops resolvedOps, blocks []tomlBlock, segs []string) bool {
	for _, b := range blocks {
		if !b.arrayTable && tomlPathEquals(b.headerPath, segs) {
			return isTOMLBlockDeja(ops, b.body)
		}
	}
	return false
}

func findTOMLProbeBlocks(ops resolvedOps, blocks []tomlBlock, targetSegs []string) []tomlBlock {
	prefixSegs := targetSegs[:len(targetSegs)-1]
	primary := targetSegs[len(targetSegs)-1]
	var own []tomlBlock
	var rest []tomlBlock
	seen := map[string]bool{}
	for _, b := range blocks {
		if b.arrayTable || len(b.headerPath) != len(targetSegs) || !tomlPathHasPrefix(b.headerPath, prefixSegs) {
			continue
		}
		k := b.entryKey()
		if k == primary {
			if !seen[k] {
				seen[k] = true
				own = append(own, b)
			}
			continue
		}
		if !seen[k] && isTOMLBlockProbeDeja(ops, b.body) {
			seen[k] = true
			rest = append(rest, b)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].entryKey() < rest[j].entryKey() })
	return append(own, rest...)
}

func findTOMLDejaBlocks(ops resolvedOps, blocks []tomlBlock, targetSegs []string) []tomlBlock {
	prefixSegs := targetSegs[:len(targetSegs)-1]
	var out []tomlBlock
	for _, b := range blocks {
		if b.arrayTable || len(b.headerPath) != len(targetSegs) || !tomlPathHasPrefix(b.headerPath, prefixSegs) {
			continue
		}
		if isTOMLBlockDeja(ops, b.body) {
			out = append(out, b)
		}
	}
	return out
}

func isTOMLBlockDeja(ops resolvedOps, body string) bool {
	kv := parseTOMLBlockKV(body)
	var cmdStrings []string
	if rawCmd, hasCmd := kv["command"]; hasCmd {
		if strings.HasPrefix(rawCmd, "[") {
			cmdStrings = parseTOMLStringArray(rawCmd)
		} else if s := unquoteTOMLString(rawCmd); s != "" {
			cmdStrings = []string{s}
		}
	}
	rawArgs, hasArgs := kv["args"]
	argStrings := parseTOMLStringArray(rawArgs)
	if len(cmdStrings) > 0 {
		if ops.isDejaCommand(cmdStrings[0], append(cmdStrings[1:], argStrings...)) {
			return true
		}
		if !hasArgs && ops.isDejaCommand(cmdStrings[0], []string{"mcp"}) {
			return true
		}
	}
	return ops.isDejaCommand("", argStrings)
}

func isTOMLBlockProbeDeja(ops resolvedOps, body string) bool {
	if isTOMLBlockDeja(ops, body) {
		return true
	}
	kv := parseTOMLBlockKV(body)
	for _, tok := range parseTOMLValueStrings(kv["command"]) {
		if ops.isDejaCommand(tok, []string{"mcp"}) {
			return true
		}
	}
	for _, tok := range parseTOMLValueStrings(kv["args"]) {
		if ops.isDejaCommand(tok, []string{"mcp"}) {
			return true
		}
	}
	return false
}

func isTOMLBlockForeign(ops resolvedOps, body string) bool {
	if isTOMLBlockDeja(ops, body) {
		return false
	}
	kv := parseTOMLBlockKV(body)
	hasCmd := kv["command"] != ""
	hasURL := kv["url"] != ""
	hasMCP := false
	for _, tok := range append(parseTOMLValueStrings(kv["command"]), parseTOMLValueStrings(kv["args"])...) {
		if tok == "mcp" {
			hasMCP = true
			break
		}
	}
	return (hasURL && !hasCmd) || (hasCmd && !hasMCP)
}

func tomlBlockHasKey(body, key string) bool {
	kv := parseTOMLBlockKV(body)
	_, ok := kv[key]
	return ok
}

func parseTOMLValueStrings(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		return parseTOMLStringArray(raw)
	}
	if s := unquoteTOMLString(raw); s != "" {
		return []string{s}
	}
	return nil
}

func updateTOMLBlockOwned(s string, target tomlBlock, replacement string, owned map[string]bool, stripDisabled bool) string {
	var ownedLines []string
	repLines := strings.Split(strings.TrimRight(replacement, "\n"), "\n")
	for _, line := range repLines[1:] {
		clean := strings.TrimSpace(stripTOMLComment(line))
		if k, _, ok := splitTOMLKeyVal(clean); ok && owned[strings.TrimSpace(k)] {
			ownedLines = append(ownedLines, line)
		}
	}

	rawBody := strings.TrimSuffix(target.body, "\n")
	var bodyLines []string
	if rawBody != "" {
		bodyLines = strings.Split(rawBody, "\n")
	}
	outBody := make([]string, 0, len(bodyLines)+len(ownedLines))
	inserted := false
	inStr := byte(0)
	triple := false
	for i := 0; i < len(bodyLines); i++ {
		if inStr == 0 {
			clean := strings.TrimSpace(stripTOMLComment(bodyLines[i]))
			if k, v, ok := splitTOMLKeyVal(clean); ok {
				ks := strings.TrimSpace(k)
				if owned[ks] || (stripDisabled && (ks == "enabled" || ks == "disabled")) {
					if owned[ks] && !inserted {
						outBody = append(outBody, ownedLines...)
						inserted = true
					}
					val := strings.TrimSpace(v)
					if strings.HasPrefix(val, "[") {
						depth := bracketDepth(val)
						for depth > 0 && i+1 < len(bodyLines) {
							i++
							depth += bracketDepth(stripTOMLComment(bodyLines[i]))
						}
					}
					continue
				}
			}
		}
		outBody = append(outBody, bodyLines[i])
		inStr, triple = advanceTOMLStringState(bodyLines[i], inStr, triple)
	}
	if !inserted {
		outBody = append(ownedLines, outBody...)
	}
	headerLine := s[target.start:target.bodyStart]
	rebuilt := headerLine
	if len(outBody) > 0 {
		rebuilt += strings.Join(outBody, "\n") + "\n"
	}
	res := s[:target.start] + rebuilt + s[target.bodyEnd:]
	return strings.TrimRight(res, "\n") + "\n"
}

func resolveTOMLTarget(
	ops resolvedOps,
	path, prefixKey, primary string,
	allBlocks, dejaBlocks []tomlBlock,
	prefixSegs []string,
) (string, string, *tomlBlock, []tomlBlock, []string) {
	primarySegs := append(append([]string(nil), prefixSegs...), primary)
	var own []tomlBlock
	for _, b := range allBlocks {
		if !b.arrayTable && tomlPathEquals(b.headerPath, primarySegs) {
			own = append(own, b)
		}
	}
	var activeBlock *tomlBlock
	if len(own) > 0 {
		activeBlock = &own[0]
	}
	activeKey := primary
	adoptedKey := ""
	if activeBlock == nil {
		for _, prev := range ops.adoptedKeys(path, prefixKey) {
			prevSegs := append(append([]string(nil), prefixSegs...), prev)
			for i := range allBlocks {
				if !allBlocks[i].arrayTable && tomlPathEquals(allBlocks[i].headerPath, prevSegs) {
					activeBlock = &allBlocks[i]
					activeKey = prev
					adoptedKey = prev
					break
				}
			}
			if activeBlock != nil {
				break
			}
		}
	}
	if activeBlock == nil && len(dejaBlocks) > 0 {
		sorted := append([]tomlBlock(nil), dejaBlocks...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].entryKey() < sorted[j].entryKey() })
		b := sorted[0]
		activeBlock = &b
		activeKey = b.entryKey()
		adoptedKey = activeKey
	}
	if adoptedKey != "" {
		ops.recordAdoptedKey(path, prefixKey, adoptedKey)
	}
	var leftKeys []string
	seenLeft := map[string]bool{activeKey: true}
	for _, b := range dejaBlocks {
		k := b.entryKey()
		if !seenLeft[k] {
			seenLeft[k] = true
			leftKeys = append(leftKeys, k)
		}
	}
	sort.Strings(leftKeys)
	return activeKey, adoptedKey, activeBlock, own, leftKeys
}

func spliceTOMLBlock(s string, existing *tomlBlock, blockText string) string {
	if existing == nil {
		trimmed := strings.TrimRight(s, " \t\r\n")
		if trimmed == "" {
			return ensureTrailingLF(blockText)
		}
		return trimmed + "\n\n" + ensureTrailingLF(blockText)
	}
	sep := ""
	if existing.bodyEnd < len(s) {
		sep = "\n"
	}
	before := s[:existing.start]
	after := strings.TrimLeft(s[existing.bodyEnd:], "\n")
	if after == "" {
		return before + ensureTrailingLF(blockText)
	}
	return before + ensureTrailingLF(blockText) + sep + after
}

func removeTOMLRanges(s string, drop []tomlBlock) string {
	sort.Slice(drop, func(i, j int) bool { return drop[i].start < drop[j].start })
	var b strings.Builder
	cur := 0
	for _, d := range drop {
		if d.start > cur {
			b.WriteString(s[cur:d.start])
		}
		if d.bodyEnd > cur {
			cur = d.bodyEnd
		}
	}
	if cur < len(s) {
		b.WriteString(s[cur:])
	}
	res := b.String()
	for strings.Contains(res, "\n\n\n") {
		res = strings.ReplaceAll(res, "\n\n\n", "\n\n")
	}
	res = strings.TrimSpace(res)
	if res == "" {
		return ""
	}
	return res + "\n"
}

func replaceTOMLHeaderLine(block, newHeader string) string {
	idx := strings.IndexByte(block, '\n')
	if idx < 0 {
		return newHeader + "\n"
	}
	return newHeader + block[idx:]
}

func formatTOMLHeader(segs []string) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		needQuote := s == ""
		for _, r := range s {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
				needQuote = true
				break
			}
		}
		if needQuote {
			parts[i] = strconv.Quote(s)
		} else {
			parts[i] = s
		}
	}
	return "[" + strings.Join(parts, ".") + "]"
}

func injectTOMLDisabledFlags(block string, flags []string) string {
	kv := parseTOMLBlockKV(block)
	out := strings.TrimRight(block, "\n")
	for _, f := range flags {
		if _, exists := kv[f]; exists {
			continue
		}
		switch f {
		case "enabled":
			out += "\nenabled = false"
		case "disabled":
			out += "\ndisabled = true"
		}
	}
	return out + "\n"
}

func ensureTrailingLF(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

package harness

import (
	"fmt"
	"strconv"
	"strings"
)

type tomlBlock struct {
	headerPath []string
	arrayTable bool
	start      int
	startLine  int
	bodyStart  int
	bodyEnd    int
	body       string
}

func (b tomlBlock) entryKey() string {
	if len(b.headerPath) == 0 {
		return ""
	}
	return b.headerPath[len(b.headerPath)-1]
}

func (b tomlBlock) disabledFlags() []string {
	kv := parseTOMLBlockKV(b.body)
	var flags []string
	if strings.EqualFold(kv["enabled"], "false") {
		flags = append(flags, "enabled")
	}
	if strings.EqualFold(kv["disabled"], "true") {
		flags = append(flags, "disabled")
	}
	return flags
}

func validateTOMLSyntaxAndArrayConflict(s string, targetSegs []string) error {
	if err := checkTOMLHeadersClosed(s); err != nil {
		return err
	}
	blocks, err := parseTOMLBlocks(s)
	if err != nil {
		return err
	}
	hdr := formatTOMLHeader(targetSegs)
	for _, b := range blocks {
		if b.arrayTable && tomlPathHasPrefix(targetSegs, b.headerPath) {
			return fmt.Errorf(
				"line %d declares %s as an array of tables, and the %s table deja adds would define it twice — write it as %s tables or add deja by hand",
				b.startLine, strings.Join(b.headerPath, "."), hdr, hdr,
			)
		}
	}
	return nil
}

func validateTOMLForWire(s string, targetSegs []string) error {
	if err := validateTOMLSyntaxAndArrayConflict(s, targetSegs); err != nil {
		return err
	}
	return checkTOMLInlineConflict(s, targetSegs)
}

func checkTOMLHeadersClosed(s string) error {
	lines := strings.Split(s, "\n")
	inStr := byte(0)
	triple := false
	for idx, line := range lines {
		if inStr == 0 {
			rawTrim := strings.TrimSpace(line)
			if strings.HasPrefix(rawTrim, "[") {
				clean := strings.TrimSpace(stripTOMLComment(line))
				_, _, ok := parseTOMLHeaderLine(clean)
				if !strings.HasSuffix(clean, "]") || !ok {
					return fmt.Errorf("line %d is a table header that never closes: %s", idx+1, rawTrim)
				}
			}
		}
		inStr, triple = advanceTOMLStringState(line, inStr, triple)
	}
	return nil
}

func checkTOMLInlineConflict(s string, targetSegs []string) error {
	var curTable []string
	lines := strings.Split(s, "\n")
	inStr := byte(0)
	triple := false
	hdr := formatTOMLHeader(targetSegs)
	for idx, line := range lines {
		if inStr == 0 {
			clean := strings.TrimSpace(stripTOMLComment(line))
			if clean == "" {
				inStr, triple = advanceTOMLStringState(line, inStr, triple)
				continue
			}
			if strings.HasPrefix(clean, "[") {
				if segs, _, ok := parseTOMLHeaderLine(clean); ok {
					curTable = segs
				}
			} else if !tomlPathHasPrefix(curTable, targetSegs) {
				if key, _, ok := splitTOMLKeyVal(clean); ok {
					keySegs := parseTOMLDottedKey(key)
					fullSegs := append(append([]string(nil), curTable...), keySegs...)
					if tomlPathHasPrefix(targetSegs, fullSegs) || tomlPathHasPrefix(fullSegs, targetSegs) {
						return fmt.Errorf(
							"line %d sets %s inline, and the %s table deja adds would define it twice — write it as %s tables or add deja by hand",
							idx+1, strings.Join(fullSegs, "."), hdr, hdr,
						)
					}
				}
			}
		}
		inStr, triple = advanceTOMLStringState(line, inStr, triple)
	}
	return nil
}

func parseTOMLBlocks(s string) ([]tomlBlock, error) {
	var out []tomlBlock
	offset := 0
	inStr := byte(0)
	triple := false
	lines := strings.SplitAfter(s, "\n")
	for lineIdx, rawLine := range lines {
		lineLen := len(rawLine)
		if inStr == 0 {
			clean := strings.TrimSpace(stripTOMLComment(strings.TrimRight(rawLine, "\r\n")))
			if strings.HasPrefix(clean, "[") {
				if segs, isArr, ok := parseTOMLHeaderLine(clean); ok {
					if len(out) > 0 {
						out[len(out)-1].bodyEnd = offset
						out[len(out)-1].body = s[out[len(out)-1].bodyStart:offset]
					}
					out = append(out, tomlBlock{
						headerPath: segs,
						arrayTable: isArr,
						start:      offset,
						startLine:  lineIdx + 1,
						bodyStart:  offset + lineLen,
					})
				}
			}
		}
		inStr, triple = advanceTOMLStringState(strings.TrimRight(rawLine, "\r\n"), inStr, triple)
		offset += lineLen
	}
	if len(out) > 0 {
		out[len(out)-1].bodyEnd = len(s)
		out[len(out)-1].body = s[out[len(out)-1].bodyStart:]
	}
	return out, nil
}

func parseTOMLHeaderLine(trimmed string) ([]string, bool, bool) {
	if strings.HasPrefix(trimmed, "[[") {
		if !strings.HasSuffix(trimmed, "]]") || len(trimmed) < 4 {
			return nil, true, false
		}
		segs := parseTOMLDottedKey(strings.TrimSpace(trimmed[2 : len(trimmed)-2]))
		return segs, true, len(segs) > 0
	}
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") || len(trimmed) < 2 {
		return nil, false, false
	}
	segs := parseTOMLDottedKey(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
	return segs, false, len(segs) > 0
}

func parseTOMLDottedKey(raw string) []string {
	var segs []string
	var cur strings.Builder
	inQuote := byte(0)
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inQuote != 0 {
			if inQuote == '"' && !escaped && c == '\\' {
				escaped = true
				cur.WriteByte(c)
				continue
			}
			if c == inQuote && !escaped {
				s := cur.String()
				if c == '"' {
					if uq, err := strconv.Unquote(`"` + s + `"`); err == nil {
						s = uq
					}
				}
				inQuote = 0
				segs = append(segs, s)
				cur.Reset()
				continue
			}
			escaped = false
			cur.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			cur.Reset()
			continue
		}
		if c == '.' {
			if p := strings.TrimSpace(cur.String()); p != "" {
				segs = append(segs, p)
			}
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if p := strings.TrimSpace(cur.String()); p != "" {
		segs = append(segs, p)
	}
	return segs
}

func tomlPathEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tomlPathHasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	return tomlPathEquals(path[:len(prefix)], prefix)
}

func parseTOMLBlockKV(body string) map[string]string {
	kv := map[string]string{}
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		clean := strings.TrimSpace(stripTOMLComment(lines[i]))
		if clean == "" {
			continue
		}
		k, v, ok := splitTOMLKeyVal(clean)
		if !ok {
			continue
		}
		for strings.HasPrefix(strings.TrimSpace(v), "[") && bracketDepth(v) > 0 && i+1 < len(lines) {
			i++
			v += "\n" + stripTOMLComment(lines[i])
		}
		kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return kv
}

func parseTOMLStringArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil
	}
	inner := raw[1 : len(raw)-1]
	var out []string
	i := 0
	for i < len(inner) {
		for i < len(inner) && (inner[i] == ' ' || inner[i] == '\t' || inner[i] == '\n' || inner[i] == '\r' || inner[i] == ',') {
			i++
		}
		if i >= len(inner) {
			break
		}
		q := inner[i]
		if q != '"' && q != '\'' {
			for i < len(inner) && inner[i] != ',' {
				i++
			}
			continue
		}
		j := i + 1
		escaped := false
		for j < len(inner) {
			if q == '"' && !escaped && inner[j] == '\\' {
				escaped = true
				j++
				continue
			}
			if inner[j] == q && !escaped {
				j++
				break
			}
			escaped = false
			j++
		}
		out = append(out, unquoteTOMLString(inner[i:j]))
		i = j
	}
	return out
}

func unquoteTOMLString(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		if uq, err := strconv.Unquote(raw); err == nil {
			return uq
		}
		return raw[1 : len(raw)-1]
	}
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1]
	}
	return ""
}

func splitTOMLKeyVal(clean string) (string, string, bool) {
	inQuote := byte(0)
	escaped := false
	for i := 0; i < len(clean); i++ {
		c := clean[i]
		if inQuote != 0 {
			if inQuote == '"' && !escaped && c == '\\' {
				escaped = true
				continue
			}
			if c == inQuote && !escaped {
				inQuote = 0
			}
			escaped = false
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			continue
		}
		if c == '=' {
			return strings.TrimSpace(clean[:i]), strings.TrimSpace(clean[i+1:]), true
		}
	}
	return "", "", false
}

func stripTOMLComment(line string) string {
	inQuote := byte(0)
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inQuote != 0 {
			if inQuote == '"' && !escaped && c == '\\' {
				escaped = true
				continue
			}
			if c == inQuote && !escaped {
				inQuote = 0
			}
			escaped = false
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			continue
		}
		if c == '#' {
			return line[:i]
		}
	}
	return line
}

func bracketDepth(s string) int {
	depth := 0
	inQuote := byte(0)
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if inQuote == '"' && !escaped && c == '\\' {
				escaped = true
				continue
			}
			if c == inQuote && !escaped {
				inQuote = 0
			}
			escaped = false
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			continue
		}
		if c == '[' {
			depth++
		} else if c == ']' {
			depth--
		}
	}
	return depth
}

func advanceTOMLStringState(line string, inStr byte, triple bool) (byte, bool) {
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inStr != 0 {
			if inStr == '"' && !escaped && c == '\\' {
				escaped = true
				continue
			}
			if triple && i+2 < len(line) && line[i] == inStr && line[i+1] == inStr && line[i+2] == inStr && !escaped {
				inStr = 0
				triple = false
				i += 2
				continue
			}
			if !triple && c == inStr && !escaped {
				inStr = 0
			}
			escaped = false
			continue
		}
		if c == '#' {
			break
		}
		if c == '"' || c == '\'' {
			if i+2 < len(line) && line[i+1] == c && line[i+2] == c {
				inStr = c
				triple = true
				i += 2
				continue
			}
			inStr = c
			triple = false
		}
	}
	if !triple {
		inStr = 0
	}
	return inStr, triple
}

package userconfig

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type tomlValueKind uint8

const (
	kindString tomlValueKind = iota + 1
	kindBool
	kindInt
	kindStringArray
)

type tomlValue struct {
	kind tomlValueKind
	str  string
	b    bool
	num  int64
	arr  []string
	line int
}

type tomlEntry struct {
	table string
	key   string
	val   tomlValue
}

func parseTOML(path, src string) ([]tomlEntry, error) {
	p := &tomlParser{path: path, src: src, line: 1}
	return p.parse()
}

type tomlParser struct {
	path string
	src  string
	pos  int
	line int
}

func (p *tomlParser) errAt(line int, format string, args ...any) error {
	return fmt.Errorf("%s:%d: %s", p.path, line, fmt.Sprintf(format, args...))
}

func (p *tomlParser) parse() ([]tomlEntry, error) {
	var out []tomlEntry
	currentTable := ""
	seenTables := map[string]bool{}
	seenKeys := map[string]bool{}

	for {
		p.skipSpaceAndComments()
		if p.pos < len(p.src) && (p.src[p.pos] == '\n' || p.src[p.pos] == '\r') {
			p.consumeNewline()
			continue
		}
		if p.pos >= len(p.src) {
			break
		}

		stmtLine := p.line
		if p.src[p.pos] == '[' {
			tbl, err := p.parseTableHeader()
			if err != nil {
				return nil, err
			}
			if seenTables[tbl] {
				return nil, p.errAt(stmtLine, "duplicate table [%s]", tbl)
			}
			seenTables[tbl] = true
			currentTable = tbl
			if err := p.expectLineEnd(stmtLine); err != nil {
				return nil, err
			}
			continue
		}

		key, err := p.parseKey()
		if err != nil {
			return nil, err
		}
		p.skipSpaces()
		if p.pos >= len(p.src) || p.src[p.pos] != '=' {
			return nil, p.errAt(stmtLine, "expected '=' after key %q", key)
		}
		p.pos++
		p.skipSpaces()

		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if err := p.expectLineEnd(stmtLine); err != nil {
			return nil, err
		}

		full := key
		if currentTable != "" {
			full = currentTable + "." + key
		}
		if seenKeys[full] {
			return nil, p.errAt(stmtLine, "duplicate key %q", full)
		}
		seenKeys[full] = true
		out = append(out, tomlEntry{table: currentTable, key: key, val: val})
	}
	return out, nil
}

func (p *tomlParser) parseTableHeader() (string, error) {
	line := p.line
	p.pos++ // '['
	p.skipSpaces()
	if p.pos < len(p.src) && p.src[p.pos] == '[' {
		return "", p.errAt(line, "arrays of tables '[[...]]' are not supported")
	}
	name, err := p.parseKey()
	if err != nil {
		return "", err
	}
	p.skipSpaces()
	if p.pos >= len(p.src) || p.src[p.pos] != ']' {
		return "", p.errAt(line, "expected ']' to close table [%s]", name)
	}
	p.pos++
	return name, nil
}

func (p *tomlParser) parseKey() (string, error) {
	line := p.line
	if p.pos >= len(p.src) {
		return "", p.errAt(line, "unexpected end of file")
	}
	switch p.src[p.pos] {
	case '"':
		return p.parseBasicString()
	case '\'':
		return p.parseLiteralString()
	}
	start := p.pos
	for p.pos < len(p.src) && isBareKeyChar(p.src[p.pos]) {
		p.pos++
	}
	if p.pos == start {
		return "", p.errAt(line, "invalid key")
	}
	return p.src[start:p.pos], nil
}

func (p *tomlParser) parseValue() (tomlValue, error) {
	line := p.line
	if p.pos >= len(p.src) || p.src[p.pos] == '\n' || p.src[p.pos] == '\r' || p.src[p.pos] == '#' {
		return tomlValue{}, p.errAt(line, "missing value")
	}
	switch p.src[p.pos] {
	case '"':
		s, err := p.parseBasicString()
		if err != nil {
			return tomlValue{}, err
		}
		return tomlValue{kind: kindString, str: s, line: line}, nil
	case '\'':
		s, err := p.parseLiteralString()
		if err != nil {
			return tomlValue{}, err
		}
		return tomlValue{kind: kindString, str: s, line: line}, nil
	case '[':
		arr, err := p.parseStringArray()
		if err != nil {
			return tomlValue{}, err
		}
		return tomlValue{kind: kindStringArray, arr: arr, line: line}, nil
	}

	token := p.readBareToken()
	switch token {
	case "true":
		return tomlValue{kind: kindBool, b: true, line: line}, nil
	case "false":
		return tomlValue{kind: kindBool, b: false, line: line}, nil
	}
	if n, ok := parseTOMLInt(token); ok {
		return tomlValue{kind: kindInt, num: n, line: line}, nil
	}
	return tomlValue{}, p.errAt(line, "invalid value %q", token)
}

func (p *tomlParser) parseStringArray() ([]string, error) {
	openLine := p.line
	p.pos++ // '['
	var out []string
	for {
		p.skipSpaceCommentsNewlines()
		if p.pos >= len(p.src) {
			return nil, p.errAt(openLine, "unterminated array")
		}
		if p.src[p.pos] == ']' {
			p.pos++
			return out, nil
		}
		itemLine := p.line
		var s string
		var err error
		switch p.src[p.pos] {
		case '"':
			s, err = p.parseBasicString()
		case '\'':
			s, err = p.parseLiteralString()
		default:
			return nil, p.errAt(itemLine, "expected quoted string inside array")
		}
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		p.skipSpaceCommentsNewlines()
		if p.pos >= len(p.src) {
			return nil, p.errAt(openLine, "unterminated array")
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return out, nil
		default:
			return nil, p.errAt(p.line, "expected ',' or ']' in array")
		}
	}
}

func (p *tomlParser) parseBasicString() (string, error) {
	openLine := p.line
	p.pos++ // '"'
	if strings.HasPrefix(p.src[p.pos:], `""`) {
		return "", p.errAt(openLine, "multiline basic strings are not supported")
	}
	var b strings.Builder
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == '"' {
			p.pos++
			return b.String(), nil
		}
		if ch == '\n' || ch == '\r' {
			return "", p.errAt(openLine, "unterminated string literal")
		}
		if ch != '\\' {
			r, w := utf8.DecodeRuneInString(p.src[p.pos:])
			b.WriteRune(r)
			p.pos += w
			continue
		}
		p.pos++
		if p.pos >= len(p.src) {
			return "", p.errAt(openLine, "unterminated escape sequence")
		}
		esc := p.src[p.pos]
		p.pos++
		switch esc {
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u', 'U':
			hexLen := 4
			if esc == 'U' {
				hexLen = 8
			}
			if p.pos+hexLen > len(p.src) {
				return "", p.errAt(openLine, "invalid \\%c unicode escape", esc)
			}
			r, ok := parseUnicodeEscape(p.src[p.pos : p.pos+hexLen])
			if !ok {
				return "", p.errAt(openLine, "invalid \\%c unicode escape", esc)
			}
			b.WriteRune(r)
			p.pos += hexLen
		default:
			return "", p.errAt(openLine, "invalid escape sequence \\%c", esc)
		}
	}
	return "", p.errAt(openLine, "unterminated string literal")
}

func (p *tomlParser) parseLiteralString() (string, error) {
	openLine := p.line
	p.pos++ // '\''
	if strings.HasPrefix(p.src[p.pos:], `''`) {
		return "", p.errAt(openLine, "multiline literal strings are not supported")
	}
	start := p.pos
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == '\'' {
			s := p.src[start:p.pos]
			p.pos++
			return s, nil
		}
		if ch == '\n' || ch == '\r' {
			return "", p.errAt(openLine, "unterminated literal string")
		}
		p.pos++
	}
	return "", p.errAt(openLine, "unterminated literal string")
}

func (p *tomlParser) readBareToken() string {
	start := p.pos
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '#' {
			break
		}
		p.pos++
	}
	return p.src[start:p.pos]
}

func (p *tomlParser) expectLineEnd(stmtLine int) error {
	p.skipSpaceAndComments()
	if p.pos >= len(p.src) {
		return nil
	}
	if p.src[p.pos] == '\n' || p.src[p.pos] == '\r' {
		p.consumeNewline()
		return nil
	}
	return p.errAt(stmtLine, "unexpected trailing content")
}

func (p *tomlParser) skipSpaces() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *tomlParser) skipSpaceAndComments() {
	p.skipSpaces()
	if p.pos < len(p.src) && p.src[p.pos] == '#' {
		for p.pos < len(p.src) && p.src[p.pos] != '\n' && p.src[p.pos] != '\r' {
			p.pos++
		}
	}
}

func (p *tomlParser) skipSpaceCommentsNewlines() {
	for p.pos < len(p.src) {
		p.skipSpaceAndComments()
		if p.pos < len(p.src) && (p.src[p.pos] == '\n' || p.src[p.pos] == '\r') {
			p.consumeNewline()
			continue
		}
		break
	}
}

func (p *tomlParser) consumeNewline() {
	if p.pos >= len(p.src) {
		return
	}
	if p.src[p.pos] == '\r' {
		p.pos++
	}
	if p.pos < len(p.src) && p.src[p.pos] == '\n' {
		p.pos++
	}
	p.line++
}

func isBareKeyChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') || ch == '_' || ch == '-'
}

func parseTOMLInt(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	digits := s
	if digits[0] == '+' || digits[0] == '-' {
		digits = digits[1:]
	}
	if len(digits) == 0 || digits[0] == '_' || digits[len(digits)-1] == '_' {
		return 0, false
	}
	var cleaned strings.Builder
	if s[0] == '-' {
		cleaned.WriteByte('-')
	}
	prevUnderscore := false
	for i := range len(digits) {
		c := digits[i]
		if c == '_' {
			if prevUnderscore {
				return 0, false
			}
			prevUnderscore = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, false
		}
		prevUnderscore = false
		cleaned.WriteByte(c)
	}
	n, err := strconv.ParseInt(cleaned.String(), 10, 64)
	return n, err == nil
}

func parseUnicodeEscape(s string) (rune, bool) {
	var r rune
	for i := range len(s) {
		c := s[i]
		var d rune
		switch {
		case c >= '0' && c <= '9':
			d = rune(c - '0')
		case c >= 'a' && c <= 'f':
			d = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = rune(c-'A') + 10
		default:
			return 0, false
		}
		r = (r << 4) | d
		if r > utf8.MaxRune {
			return 0, false
		}
	}
	if !utf8.ValidRune(r) {
		return 0, false
	}
	return r, true
}

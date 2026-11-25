package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type jsoncEntry struct {
	key      string
	keyStart int
	valStart int
	valEnd   int
	commaPos int
	entryEnd int
}

// isJSONC reports whether raw contains JSON comments (// or /* */) or
// trailing commas before closing delimiters outside string literals.
func isJSONC(raw []byte) bool {
	s := string(raw)
	n := len(s)
	inStr := false
	escaped := false
	for i := range n {
		c := s[i]
		if inStr {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '/':
			if i+1 < n && (s[i+1] == '/' || s[i+1] == '*') {
				return true
			}
		case ',':
			j := skipWS(s, i+1)
			if j < n && (s[j] == '}' || s[j] == ']') {
				return true
			}
		}
	}
	return false
}

// toStrictJSON strips line comments, block comments, and trailing commas
// outside string literals so standard encoding/json can parse JSONC documents.
func toStrictJSON(raw []byte) ([]byte, error) {
	s := string(raw)
	var withoutComments strings.Builder
	withoutComments.Grow(len(s))
	n := len(s)
	inStr := false
	escaped := false
	for i := 0; i < n; i++ {
		c := s[i]
		if inStr {
			withoutComments.WriteByte(c)
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			withoutComments.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < n && s[i+1] == '/' {
			i += 2
			for i < n && s[i] != '\n' {
				i++
			}
			if i < n && s[i] == '\n' {
				withoutComments.WriteByte('\n')
			}
			continue
		}
		if c == '/' && i+1 < n && s[i+1] == '*' {
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return nil, errors.New("unclosed block comment")
			}
			i = i + 2 + end + 1
			continue
		}
		withoutComments.WriteByte(c)
	}
	return stripTrailingCommas([]byte(withoutComments.String())), nil
}

func stripTrailingCommas(raw []byte) []byte {
	s := string(raw)
	var out strings.Builder
	out.Grow(len(s))
	n := len(s)
	inStr := false
	escaped := false
	for i := range n {
		c := s[i]
		if inStr {
			out.WriteByte(c)
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out.WriteByte(c)
			continue
		}
		if c == ',' {
			j := skipWS(s, i+1)
			if j < n && (s[j] == '}' || s[j] == ']') {
				continue
			}
		}
		out.WriteByte(c)
	}
	return []byte(out.String())
}

func parseJSONObject(raw []byte) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	payload := raw
	if isJSONC(raw) {
		var err error
		payload, err = toStrictJSON(raw)
		if err != nil {
			return nil, err
		}
	}
	var root map[string]any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("config root must be a JSON object")
	}
	return root, nil
}

func skipWS(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	return i
}

func skipWSAndComments(s string, i int) (int, error) {
	for i < len(s) {
		i = skipWS(s, i)
		if i+1 >= len(s) || s[i] != '/' {
			return i, nil
		}
		if s[i+1] == '/' {
			i += 2
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if s[i+1] == '*' {
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return -1, errors.New("unclosed block comment")
			}
			i = i + 2 + end + 2
			continue
		}
		return i, nil
	}
	return i, nil
}

func scanStringEnd(s string, start int) (int, bool) {
	if start >= len(s) || s[start] != '"' {
		return -1, false
	}
	escaped := false
	for i := start + 1; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		if s[i] == '"' {
			return i + 1, true
		}
	}
	return -1, false
}

func scanCompositeEnd(s string, start int, open, closeChar byte) (int, error) {
	if start >= len(s) || s[start] != open {
		return -1, errors.New("expected opening delimiter")
	}
	depth := 0
	for i := start; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			end, ok := scanStringEnd(s, i)
			if !ok {
				return -1, errors.New("unterminated string")
			}
			i = end - 1
			continue
		}
		if c == '/' && i+1 < len(s) {
			next, err := skipWSAndComments(s, i)
			if err != nil {
				return -1, err
			}
			if next > i {
				i = next - 1
				continue
			}
		}
		switch c {
		case open:
			depth++
		case closeChar:
			depth--
			if depth == 0 {
				return i + 1, nil
			}
		}
	}
	return -1, errors.New("unclosed composite value")
}

func scanValueSpan(s string, start int) (int, error) {
	if start >= len(s) {
		return -1, errors.New("unexpected end of JSON value")
	}
	switch s[start] {
	case '"':
		end, ok := scanStringEnd(s, start)
		if !ok {
			return -1, errors.New("unterminated string value")
		}
		return end, nil
	case '{':
		return scanCompositeEnd(s, start, '{', '}')
	case '[':
		return scanCompositeEnd(s, start, '[', ']')
	}
	i := start
	for i < len(s) {
		c := s[i]
		if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '/' {
			break
		}
		i++
	}
	if i == start {
		return -1, fmt.Errorf("invalid value near offset %d", start)
	}
	return i, nil
}

func findRootObjectBounds(s string) (int, int, error) {
	i, err := skipWSAndComments(s, 0)
	if err != nil {
		return -1, -1, err
	}
	if i >= len(s) || s[i] != '{' {
		return -1, -1, errors.New("config root must be a JSON object")
	}
	end, err := scanCompositeEnd(s, i, '{', '}')
	if err != nil {
		return -1, -1, err
	}
	return i, end, nil
}

func parseObjectEntries(s string, objStart, objEnd int) ([]jsoncEntry, error) {
	var out []jsoncEntry
	i := objStart + 1
	for i < objEnd-1 {
		var err error
		i, err = skipWSAndComments(s, i)
		if err != nil {
			return nil, err
		}
		if i >= objEnd-1 || s[i] == '}' {
			break
		}
		if s[i] != '"' {
			return nil, fmt.Errorf("expected object key at offset %d", i)
		}
		keyEnd, ok := scanStringEnd(s, i)
		if !ok {
			return nil, errors.New("unterminated object key")
		}
		key, err := strconv.Unquote(s[i:keyEnd])
		if err != nil {
			return nil, err
		}
		colon, err := skipWSAndComments(s, keyEnd)
		if err != nil {
			return nil, err
		}
		if colon >= objEnd-1 || s[colon] != ':' {
			return nil, fmt.Errorf("expected ':' after key %q", key)
		}
		valStart, err := skipWSAndComments(s, colon+1)
		if err != nil {
			return nil, err
		}
		valEnd, err := scanValueSpan(s, valStart)
		if err != nil {
			return nil, err
		}
		after, err := skipWSAndComments(s, valEnd)
		if err != nil {
			return nil, err
		}
		commaPos := -1
		entryEnd := valEnd
		next := after
		if after < objEnd-1 && s[after] == ',' {
			commaPos = after
			entryEnd = after + 1
			next = after + 1
		}
		out = append(out, jsoncEntry{
			key:      key,
			keyStart: i,
			valStart: valStart,
			valEnd:   valEnd,
			commaPos: commaPos,
			entryEnd: entryEnd,
		})
		i = next
	}
	return out, nil
}

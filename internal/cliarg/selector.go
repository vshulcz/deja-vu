// Package cliarg parses and normalizes <harness>:<id> CLI session selectors.
package cliarg

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// ErrEmptySelector reports that a session selector resolved to nothing after
// stripping surrounding whitespace and paste wrappers.
var ErrEmptySelector = errors.New("empty session selector")

// Selector is a cleaned session selector split into optional harness prefix
// and session ID / prefix.
type Selector struct {
	Clean   string
	Harness string
	ID      string
}

// CleanSelector removes surrounding whitespace and matching quote/backtick
// pairs that terminals or chat UIs wrap around pasted session IDs.
// An unmatched wrapper on one side is preserved as part of the token.
func CleanSelector(raw string) string {
	s := strings.TrimSpace(raw)
	for s != "" {
		first, firstSize := utf8.DecodeRuneInString(s)
		last, lastSize := utf8.DecodeLastRuneInString(s)
		if firstSize+lastSize > len(s) || !isQuotePair(first, last) {
			break
		}
		s = strings.TrimSpace(s[firstSize : len(s)-lastSize])
	}
	return s
}

func isQuotePair(first, last rune) bool {
	switch first {
	case '"', '\'', '`':
		return last == first
	case '\u201c': // “
		return last == '\u201d' // ”
	case '\u2018': // ‘
		return last == '\u2019' // ’
	default:
		return false
	}
}

// SplitSelector splits a cleaned "harness:id" selector into its harness and
// ID parts when both halves are non-empty and the harness contains only
// ASCII identifier runes.
func SplitSelector(clean string) (harness, id string) {
	i := strings.IndexByte(clean, ':')
	if i <= 0 || i == len(clean)-1 {
		return "", clean
	}
	for _, r := range clean[:i] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "", clean
		}
	}
	return clean[:i], clean[i+1:]
}

// ParseSelector cleans raw, rejects empty selectors with ErrEmptySelector,
// and splits any harness prefix.
func ParseSelector(raw string) (Selector, error) {
	clean := CleanSelector(raw)
	if clean == "" {
		return Selector{}, ErrEmptySelector
	}
	h, id := SplitSelector(clean)
	return Selector{
		Clean:   clean,
		Harness: h,
		ID:      id,
	}, nil
}

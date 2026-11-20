package search

import (
	"regexp"
	"strings"
	"unicode"
)

var multiBlankLines = regexp.MustCompile(`\n{3,}`)

func stripClosedTagBlock(s, openTag, closeTag string) string {
	for {
		start := strings.Index(s, openTag)
		if start < 0 {
			return s
		}
		endRel := strings.Index(s[start+len(openTag):], closeTag)
		if endRel < 0 {
			return s
		}
		end := start + len(openTag) + endRel + len(closeTag)
		s = s[:start] + s[end:]
	}
}

// StripToolProtocolNoise removes harness envelopes, thinking blocks, and
// tool-call framing from assistant turns while keeping real error and prose lines.
func StripToolProtocolNoise(s string) string {
	if !strings.Contains(s, "[thinking]") &&
		!strings.Contains(s, "[tool_use") &&
		!strings.Contains(s, "[tool_result]") &&
		!strings.Contains(s, "<function_calls>") &&
		!strings.Contains(s, "<system-reminder>") {
		return s
	}
	orig := s
	s = stripClosedTagBlock(s, "<system-reminder>", "</system-reminder>")
	s = stripClosedTagBlock(s, "<function_calls>", "</function_calls>")

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	inThinking := false
	skipToolJSON := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipToolJSON {
			skipToolJSON = false
			if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
				continue
			}
		}
		if inThinking {
			if trimmed == "" {
				inThinking = false
				continue
			}
			if !strings.HasPrefix(trimmed, "[tool_") {
				continue
			}
			inThinking = false
		}
		if trimmed == "[thinking]" || strings.HasPrefix(trimmed, "[thinking] ") {
			inThinking = true
			continue
		}
		if strings.HasPrefix(trimmed, "[tool_use ") || trimmed == "[tool_use]" {
			skipToolJSON = true
			continue
		}
		if trimmed == "[tool_result]" {
			continue
		}
		if strings.HasPrefix(trimmed, "[tool_result] ") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "[tool_result]"))
			if rest != "" {
				out = append(out, rest)
			}
			continue
		}
		out = append(out, line)
	}
	cleaned := multiBlankLines.ReplaceAllString(strings.Join(out, "\n"), "\n\n")
	if !strings.HasPrefix(orig, "\n") {
		cleaned = strings.TrimLeft(cleaned, "\r\n")
	}
	if !strings.HasSuffix(orig, "\n") {
		cleaned = strings.TrimRight(cleaned, "\r\n")
	}
	return cleaned
}

var narrationPrefixes = []string{
	"let me check",
	"let me read",
	"let me look",
	"let me search",
	"let me inspect",
	"let me examine",
	"i'll check",
	"i'll read",
	"i'll look",
	"i will check",
	"i will read",
	"now let's check",
	"now let me",
}

var narrationTechnicalWords = map[string]bool{
	"error": true, "errors": true,
	"fail": true, "fails": true, "failed": true, "failing": true,
	"fix": true, "fixes": true, "fixed": true, "fixing": true,
	"bug": true, "bugs": true,
	"cause": true, "causes": true, "caused": true, "causing": true,
	"because": true,
	"return":  true, "returns": true, "returned": true, "returning": true,
	"panic": true, "panics": true, "panicked": true,
	"exception": true, "exceptions": true,
	"test": true, "tests": true, "tested": true, "testing": true,
	"pass": true, "passes": true, "passed": true, "passing": true,
	"revert": true, "reverts": true, "reverted": true, "reverting": true,
	"instead": true,
	"should":  true,
	"must":    true,
	"never":   true,
}

// IsPureProcessNarration reports whether a short assistant line only announces
// a next inspection step without technical evidence or conclusions.
func IsPureProcessNarration(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || len(trimmed) > 140 || strings.ContainsAny(trimmed, "/`\"=()") {
		return false
	}
	low := strings.ToLower(strings.ReplaceAll(trimmed, "’", "'"))
	matchedPrefix := false
	for _, pre := range narrationPrefixes {
		if strings.HasPrefix(low, pre) {
			matchedPrefix = true
			break
		}
	}
	if !matchedPrefix {
		return false
	}
	for _, w := range strings.FieldsFunc(low, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if narrationTechnicalWords[w] {
			return false
		}
	}
	return true
}

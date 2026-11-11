package index

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
)

const fixWhyMaxRunes = 160

var (
	goMethodDeclRE = regexp.MustCompile(`(?m)^\s*(?:[+-]\s*)?func\s*\(\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)?\*?([A-Za-z_][A-Za-z0-9_]*)(?:\[[^\]]*\])?\s*\)\s*([A-Za-z_][A-Za-z0-9_]*)\s*[\(\[]`)
	goFuncDeclRE   = regexp.MustCompile(`(?m)^\s*(?:[+-]\s*)?func\s+([A-Za-z_][A-Za-z0-9_]*)\s*[\(\[]`)
	goTypeDeclRE   = regexp.MustCompile(`(?m)^\s*(?:[+-]\s*)?type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(?:struct|interface)\b`)
	pyDefDeclRE    = regexp.MustCompile(`(?m)^\s*(?:[+-]\s*)?(?:async\s+)?(?:def|class)\s+([A-Za-z_][A-Za-z0-9_]*)\s*[\(:]`)
	rsFnDeclRE     = regexp.MustCompile(`(?m)^\s*(?:[+-]\s*)?(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:fn|struct|enum)\s+([A-Za-z_][A-Za-z0-9_]*)\s*[<\(\{]`)
	tsFuncDeclRE   = regexp.MustCompile(`(?m)^\s*(?:[+-]\s*)?(?:export\s+)?(?:async\s+)?(?:function|class)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*[<\(\{]`)
	qualifiedCallRE = regexp.MustCompile(`(?:([A-Za-z_][A-Za-z0-9_]*)\.)?([A-Za-z_][A-Za-z0-9_]{5,})\s*\(`)
	backtickSymRE   = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*(?:\\.[A-Za-z_][A-Za-z0-9_]*)?)(?:\\(\\))?`")
)

var trivialCallNames = map[string]bool{
	"append": true, "delete": true, "len": true, "cap": true, "make": true,
	"panic": true, "recover": true, "close": true, "copy": true, "print": true,
	"println": true, "printf": true, "sprintf": true, "fprintf": true, "errorf": true, "fatalf": true,
	"return": true, "switch": true, "select": true, "typeof": true, "require": true,
	"import": true, "assert": true, "expect": true, "string": true, "strings": true,
	"errors": true, "format": true, "contains": true, "hasprefix": true, "hassuffix": true,
	"trimspace": true, "trimprefix": true, "trimsuffix": true, "replaceall": true,
	"tolower": true, "toupper": true, "splitseq": true, "fieldsseq": true,
	"mustcompile": true, "findstring": true, "findallstring": true, "matchstring": true,
	"readfile": true, "writefile": true, "mkdirall": true, "removeall": true,
}

var stdlibQualifiers = map[string]bool{
	"strings": true, "strconv": true, "filepath": true, "path": true, "fmt": true,
	"bytes": true, "slices": true, "maps": true, "sort": true, "time": true,
	"os": true, "io": true, "sync": true, "atomic": true, "utf8": true,
	"unicode": true, "json": true, "gob": true, "regexp": true, "math": true,
	"context": true, "http": true, "url": true, "errors": true, "fs": true,
	"reflect": true, "binary": true, "bufio": true, "hex": true, "sha256": true,
	"exec": true, "testing": true, "t": true, "b": true,
}

// mineEditContext extracts the primary symbol in the edited span and a concise
// causal explanation spoken by the assistant right around editIdx.
func mineEditContext(ms []model.Message, errIdx, editIdx int, editedPath, errLine string) (symbol, why string) {
	if editIdx < 0 || editIdx >= len(ms) {
		return "", ""
	}
	symbol = extractEditedSymbol(ms, errIdx, editIdx, editedPath)
	why = extractEditWhy(ms, errIdx, editIdx, editedPath, symbol, errLine)
	return symbol, why
}

// extractEditedSymbol parses the declaration, assistant-named symbol, or
// domain call identifier from the edit span around editIdx.
func extractEditedSymbol(ms []model.Message, errIdx, editIdx int, editedPath string) string {
	var bodies []string
	if _, body, ok := strings.Cut(ms[editIdx].Text, "\n"); ok && strings.TrimSpace(body) != "" {
		bodies = append(bodies, body)
	}
	for k := max(0, editIdx-1); k <= min(len(ms)-1, editIdx+2); k++ {
		if k == editIdx || ms[k].Role != roleEdit {
			continue
		}
		p, body, ok := strings.Cut(ms[k].Text, "\n")
		if ok && strings.TrimSpace(p) == editedPath && strings.TrimSpace(body) != "" {
			bodies = append(bodies, body)
		}
	}
	joined := strings.Join(bodies, "\n")
	if joined != "" {
		if m := goMethodDeclRE.FindStringSubmatch(joined); len(m) == 3 && !isTestSymbolName(m[2]) {
			return m[1] + "." + m[2]
		}
		for _, re := range []*regexp.Regexp{goFuncDeclRE, goTypeDeclRE, pyDefDeclRE, rsFnDeclRE, tsFuncDeclRE} {
			if m := re.FindStringSubmatch(joined); len(m) == 2 && !isTestSymbolName(m[1]) {
				return m[1]
			}
		}
	}
	if sym := extractBacktickSymbolNearEdit(ms, errIdx, editIdx, joined); sym != "" {
		return sym
	}
	if joined == "" {
		return ""
	}
	for _, m := range qualifiedCallRE.FindAllStringSubmatch(joined, 16) {
		qual, cand := m[1], m[2]
		if stdlibQualifiers[strings.ToLower(qual)] {
			continue
		}
		low := strings.ToLower(cand)
		if trivialCallNames[low] || isTestSymbolName(cand) {
			continue
		}
		if isMultiWordIdent(cand) {
			return cand
		}
	}
	return ""
}

func extractBacktickSymbolNearEdit(ms []model.Message, errIdx, editIdx int, editBody string) string {
	start := max(errIdx, editIdx-1)
	end := min(len(ms)-1, editIdx+3)
	var fallback string
	for k := start; k <= end; k++ {
		if ms[k].Role != "assistant" {
			continue
		}
		for _, m := range backtickSymRE.FindAllStringSubmatch(ms[k].Text, 12) {
			cand := m[1]
			if !isPlausibleBacktickSymbol(cand) {
				continue
			}
			short := cand
			if _, method, ok := strings.Cut(cand, "."); ok {
				short = method
			}
			if editBody != "" && strings.Contains(editBody, short) {
				return cand
			}
			if fallback == "" && isMultiWordIdent(short) {
				fallback = cand
			}
		}
	}
	return fallback
}

func isPlausibleBacktickSymbol(cand string) bool {
	if len(cand) < 5 || len(cand) > 64 || isTestSymbolName(cand) {
		return false
	}
	if qual, name, ok := strings.Cut(cand, "."); ok {
		lowExt := strings.ToLower(name)
		switch lowExt {
		case "go", "py", "ts", "tsx", "js", "jsx", "rs", "md", "json", "jsonl", "gob", "bin", "sql", "toml", "yaml", "yml", "sh", "txt", "lock":
			return false
		}
		if stdlibQualifiers[strings.ToLower(qual)] || trivialCallNames[lowExt] || isTestSymbolName(name) {
			return false
		}
		return len(qual) >= 2 && len(name) >= 3
	}
	if trivialCallNames[strings.ToLower(cand)] {
		return false
	}
	return isMultiWordIdent(cand)
}

func isTestSymbolName(name string) bool {
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "test_")
}

// isMultiWordIdent accepts snake_case, camelCase, or multi-hump PascalCase
// identifiers while rejecting single-word PascalCase calls such as Split or Close.
func isMultiWordIdent(s string) bool {
	if idx := strings.IndexByte(s, '_'); idx > 0 && idx < len(s)-1 {
		return true
	}
	upperCount := 0
	seenLower := false
	camelTransition := false
	for _, r := range s {
		if unicode.IsUpper(r) {
			upperCount++
			if seenLower {
				camelTransition = true
			}
		} else if unicode.IsLower(r) {
			seenLower = true
		}
	}
	if !seenLower {
		return false
	}
	return camelTransition || upperCount >= 2
}

// extractEditWhy scans nearby assistant speech around editIdx for a single
// sentence explaining the root cause or invariant behind the fix.
func extractEditWhy(ms []model.Message, errIdx, editIdx int, editedPath, symbol, errLine string) string {
	base := strings.ToLower(filepath.Base(editedPath))
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	symLow := strings.ToLower(symbol)
	symShort := symLow
	if _, m, ok := strings.Cut(symLow, "."); ok {
		symShort = m
	}
	errTerms := significantErrorTerms(errLine)

	start := max(errIdx, editIdx-2)
	end := min(len(ms)-1, editIdx+6)
	var fallback string
	for k := start; k <= end; k++ {
		if ms[k].Role != "assistant" {
			continue
		}
		for rawLine := range strings.SplitSeq(ms[k].Text, "\n") {
			sent := cleanCausalSentence(rawLine)
			if sent == "" {
				continue
			}
			if !carriesCausalMarker(sent) || isNoisyStatusSentence(sent) {
				continue
			}
			low := strings.ToLower(sent)
			anchors := (len(base) >= 5 && strings.Contains(low, base)) ||
				(len(stem) >= 5 && strings.Contains(low, stem)) ||
				(len(symShort) >= 4 && strings.Contains(low, symShort)) ||
				mentionsAnyErrorTerm(low, errTerms)
			if !anchors {
				continue
			}
			redacted, _ := redact.Text(sent)
			clipped := clipCausalSentence(strings.TrimSpace(redacted), fixWhyMaxRunes)
			if clipped == "" {
				continue
			}
			if symShort != "" && strings.Contains(low, symShort) {
				return clipped
			}
			if fallback == "" {
				fallback = clipped
			}
		}
	}
	return fallback
}

func significantErrorTerms(errLine string) []string {
	var out []string
	for _, t := range fixTermRE.FindAllString(strings.ToLower(errLine), -1) {
		t = trimTermEdges(t)
		if len(t) >= 5 && !fixCommonTerms[t] {
			out = append(out, t)
		}
	}
	return out
}

func mentionsAnyErrorTerm(low string, terms []string) bool {
	for _, t := range terms {
		if strings.Contains(low, t) {
			return true
		}
	}
	return false
}

func cleanCausalSentence(raw string) string {
	s := strings.TrimSpace(raw)
	for _, bullet := range []string{"- ", "* ", "> ", "1. ", "2. ", "3. "} {
		if after, ok := strings.CutPrefix(s, bullet); ok {
			s = strings.TrimSpace(after)
			break
		}
	}
	if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "|") {
		return ""
	}
	s = strings.ReplaceAll(s, "**", "")
	for _, prefix := range []string{"Root cause:", "root cause:", "Cause:", "Why:", "Bug:"} {
		if after, ok := strings.CutPrefix(s, prefix); ok {
			s = strings.TrimSpace(after)
			break
		}
	}
	if idx := strings.Index(s, ". "); idx >= 35 && idx <= 200 {
		s = s[:idx+1]
	}
	n := utf8.RuneCountInString(s)
	if n < 35 || n > 260 {
		return ""
	}
	return s
}

func carriesCausalMarker(s string) bool {
	low := strings.ToLower(s)
	for _, m := range []string{
		"because ", "caused by ", "root cause", "due to ", "raced", "race condition",
		"data race", "deadlock", "nil pointer", "nil map", "nil slice", "was nil", "is nil",
		"off-by-one", "underflow", "overflow", "stale ", "without holding", "without locking",
		"before closing", "after closing", "instead of ", "so that ", "otherwise ",
		"prevent ", "prevents ", "avoid ", "avoids ", "broke when", "broke because",
		"failed because", "fails when", "failed when", "panicked", "missing ", "dropped ",
		"leaked ", "swallowed ", "overwrote ", "truncated ", "mismatch", "must hold", "must be ",
		"потому что ", "из-за ", "причина", "иначе ", "чтобы не ", "гонка", "паника",
	} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

func isNoisyStatusSentence(s string) bool {
	low := strings.ToLower(s)
	for _, p := range []string{
		"let me ", "let's ", "i'll ", "i will ", "i need to ", "we need to ",
		"now let", "now run", "running ", "checking ", "looking at ",
		"all tests pass", "ci checks pass",
	} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	for _, sub := range []string{"(merged)", "(closed)", "http://", "https://"} {
		if strings.Contains(low, sub) {
			return true
		}
	}
	if strings.HasSuffix(strings.TrimSpace(s), "?") {
		return true
	}
	return false
}

func clipCausalSentence(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	cut := string(runes[:maxRunes])
	if sp := strings.LastIndexByte(cut, ' '); sp > maxRunes/2 {
		cut = cut[:sp]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

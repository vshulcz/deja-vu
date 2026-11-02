package search

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

func isSymbolName(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i, r := range s {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

func resolveSymbolLineRange(path, symbol string) (int, int, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > 2<<20 {
		return 0, 0, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(string(raw), "\n")
	startLine := -1
	for i, line := range lines {
		if lineDeclaresSymbol(line, symbol) {
			startLine = i + 1
			break
		}
	}
	if startLine < 0 {
		return 0, 0, false
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".py" {
		endLine := pythonSymbolEndLine(lines, startLine-1)
		return startLine, endLine, true
	}
	endLine := braceSymbolEndLine(lines, startLine-1)
	return startLine, endLine, true
}

func lineDeclaresSymbol(line, symbol string) bool {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "/*") || strings.HasPrefix(s, "*") {
		return false
	}
	for _, kw := range []string{
		"func ", "type ", "var ", "const ",
		"async def ", "def ", "class ",
		"export default function ", "export async function ", "export function ",
		"export class ", "export interface ", "export type ", "export const ",
		"async function ", "function ", "interface ",
		"pub async fn ", "pub fn ", "async fn ", "fn ",
		"pub struct ", "struct ", "pub enum ", "enum ", "pub trait ", "trait ",
	} {
		if strings.HasPrefix(s, kw) {
			rest := strings.TrimSpace(s[len(kw):])
			if kw == "func " && strings.HasPrefix(rest, "(") {
				closeIdx := strings.IndexByte(rest, ')')
				if closeIdx < 0 {
					return false
				}
				rest = strings.TrimSpace(rest[closeIdx+1:])
			}
			return hasLeadingIdent(rest, symbol)
		}
	}
	return false
}

func hasLeadingIdent(s, symbol string) bool {
	if !strings.HasPrefix(s, symbol) {
		return false
	}
	if len(s) == len(symbol) {
		return true
	}
	next, _ := utf8.DecodeRuneInString(s[len(symbol):])
	return !unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_'
}

func braceSymbolEndLine(lines []string, startIdx int) int {
	depth := 0
	opened := false
	limit := min(len(lines), startIdx+400)
	for i := startIdx; i < limit; i++ {
		for _, r := range lines[i] {
			switch r {
			case '{':
				depth++
				opened = true
			case '}':
				depth--
				if opened && depth <= 0 {
					return i + 1
				}
			}
		}
		if !opened && i == startIdx {
			return startIdx + 1
		}
	}
	return limit
}

func pythonSymbolEndLine(lines []string, startIdx int) int {
	baseIndent := leadingIndentWidth(lines[startIdx])
	endIdx := startIdx
	limit := min(len(lines), startIdx+400)
	for i := startIdx + 1; i < limit; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if leadingIndentWidth(lines[i]) <= baseIndent {
			break
		}
		endIdx = i
	}
	return endIdx + 1
}

func leadingIndentWidth(s string) int {
	w := 0
	for _, r := range s {
		switch r {
		case ' ':
			w++
		case '\t':
			w += 4
		default:
			return w
		}
	}
	return w
}

func sessionMentionsSymbol(s model.Session, symbol string) bool {
	want := strings.ToLower(symbol)
	if want == "" {
		return false
	}
	for _, m := range s.Messages {
		if strings.Contains(strings.ToLower(m.Text), want) {
			return true
		}
	}
	return false
}

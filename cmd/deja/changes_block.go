package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The code a session changed takes what the conversation leaves of a context
// digest, between these bounds. The digest reads a session from the top, and
// a coding session's top is the task and then reading code: on 48
// SWE-ContextBench tasks the digest of the past session that fixed the same
// code held none of the fix. The conversation keeps at least the rest of the
// 8 KB.
const (
	changesBlockMin = 3000
	changesBlockMax = 5000
)

const (
	changeOldMax = 400
	changeNewMax = 1800
)

// changesBlock is the code a session left, read back from its transcript:
// each change from the code as it was to the code as the session left it,
// the project's code before its tests, the agent's scratch files left out.
// Empty for a session that changed nothing, or whose harness it cannot read.
func changesBlock(s model.Session, room int) string {
	return changesBlockFor(s, room, "", false)
}

// changesBlockFor formats a session's code changes prioritized for query q.
// When requireBind is true on a weak tier match, it omits the block unless the
// changed code or file paths bind to q.
func changesBlockFor(s model.Session, room int, q string, requireBind bool) string {
	return changesBlockForIDF(s, room, q, requireBind, nil)
}

func changesBlockForIDF(s model.Session, room int, q string, requireBind bool, termIDF map[string]float64) string {
	return changesBlockForTopIDF(s, room, q, requireBind, termIDF, 0)
}

func changesBlockForTopIDF(s model.Session, room int, q string, requireBind bool, termIDF map[string]float64, topIDF float64) string {
	return changesBlockForSearch(s, room, q, requireBind, termIDF, nil, topIDF)
}

func changesBlockForSearch(s model.Session, room int, q string, requireBind bool, termIDF, sessionIDF map[string]float64, topIDF float64) string {
	room = min(max(room, changesBlockMin), changesBlockMax)
	cs := sessionChanges(s)
	if len(cs) == 0 {
		return ""
	}
	if strings.TrimSpace(q) != "" {
		if requireBind && !changesBindToQueryWithSessionIDF(cs, q, termIDF, sessionIDF, topIDF) {
			return ""
		}
		cs = rankChangesForQueryWithIDF(cs, q, termIDF)
	}
	var b strings.Builder
	hasUnverified := false
	for _, c := range cs {
		if c.TestFailed {
			hasUnverified = true
			break
		}
	}
	b.WriteString("## what it changed\n")
	if hasUnverified {
		b.WriteString("\n(unverified — later test failed)\n")
	}
	// The changes that did not fit are counted, not dropped without a word:
	// the block read as everything the session changed (#5392).
	const leftOutRoom = 40
	shown := 0
	for i, c := range cs {
		perChangeMax := changeNewMax + changeOldMax
		if len(cs) == 1 {
			perChangeMax = max(perChangeMax, room-b.Len()-16)
		} else if i == 0 && len(cs) <= 3 {
			perChangeMax = max(perChangeMax, (room-b.Len())/len(cs))
		}
		body := formatChangeDiffBounded(c, q, perChangeMax)
		var one string
		if c.Old == "" {
			one = fmt.Sprintf("\n%s, new file:\n%s\n", c.Path, body)
		} else {
			one = fmt.Sprintf("\n%s:\n%s\n", c.Path, body)
		}
		limit := room
		if i < len(cs)-1 {
			limit -= leftOutRoom
		}
		if b.Len()+len(one) > limit {
			if shown == 0 {
				// The first change alone is over: show what fits of it rather
				// than nothing.
				b.WriteString(clipToFit(one, limit-b.Len()))
				shown++
			}
			break
		}
		b.WriteString(one)
		shown++
	}
	// Written after the redaction and the trim, so neither can take it.
	count := ""
	if left := len(cs) - shown; left > 0 {
		count = fmt.Sprintf("\n(%d more change%s not shown)\n", left, pluralS(left))
	}
	text, _ := redact.Text(b.String())
	return trimUTF8(redact.SafeForDisplay(text), max(0, room-len(count))) + count + "\n"
}

// clipToFit cuts one change to n bytes and says so.
func clipToFit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return trimUTF8(s, max(0, n-len("\n…\n"))) + "\n…\n"
}

func clipChange(s string, n int) string {
	s = strings.Trim(s, "\n")
	if len(s) <= n {
		return s
	}
	return trimUTF8(s, n) + "\n…"
}

// withChanges puts the block right under the digest's header line, ahead of
// the conversation, so a digest cut to its budget loses the end of the
// conversation and keeps the code.
func withChanges(digest string, s model.Session) string {
	return withChangesFor(digest, s, "", false)
}

func withChangesFor(digest string, s model.Session, q string, requireBind bool) string {
	return withChangesForIDF(digest, s, q, requireBind, nil)
}

func withChangesForIDF(digest string, s model.Session, q string, requireBind bool, termIDF map[string]float64) string {
	return withChangesForTopIDF(digest, s, q, requireBind, termIDF, 0)
}

func withChangesForTopIDF(digest string, s model.Session, q string, requireBind bool, termIDF map[string]float64, topIDF float64) string {
	return withChangesForSearch(digest, s, q, requireBind, termIDF, nil, topIDF)
}

func withChangesForSearch(digest string, s model.Session, q string, requireBind bool, termIDF, sessionIDF map[string]float64, topIDF float64) string {
	blk := changesBlockForSearch(s, search.ContextBudget-len(digest), q, requireBind, termIDF, sessionIDF, topIDF)
	if blk == "" {
		return digest
	}
	head, rest, ok := strings.Cut(digest, "\n")
	if !ok {
		return digest + "\n\n" + blk
	}
	return head + "\n\n" + blk + rest
}

type changeQueryGroup struct {
	whole   string
	parts   []string
	isIdent bool
}

func changeIdentParts(s string) []string {
	seen := map[string]bool{}
	var out []string
	emit := func(raw string) {
		low := query.Lower(strings.Trim(raw, "_"))
		if len([]rune(low)) < 3 || query.IsStopWord(low) || seen[low] {
			return
		}
		seen[low] = true
		out = append(out, low)
	}
	for _, field := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if strings.ContainsRune(field, '_') {
			emit(field)
		}
	}
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if !unicode.IsLetter(runes[i]) && !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j])) {
			j++
		}
		word := runes[i:j]
		emit(string(word))
		subStart := 0
		for k := 1; k < len(word); k++ {
			prev, cur := word[k-1], word[k]
			splitLowerToUpper := (unicode.IsLower(prev) || unicode.IsDigit(prev)) && unicode.IsUpper(cur)
			splitUpperAcronym := k+1 < len(word) && unicode.IsUpper(prev) && unicode.IsUpper(cur) && unicode.IsLower(word[k+1])
			if splitLowerToUpper || splitUpperAcronym {
				if splitUpperAcronym && k-subStart == 1 {
					nextEnd := k + 2
					for nextEnd < len(word) && (unicode.IsLower(word[nextEnd]) || unicode.IsDigit(word[nextEnd])) {
						nextEnd++
					}
					emit(string(word[subStart:nextEnd]))
				}
				emit(string(word[subStart:k]))
				subStart = k
			}
		}
		if subStart > 0 {
			emit(string(word[subStart:]))
		}
		i = j
	}
	return out
}

func changeQueryTerms(q string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		low := query.Lower(t)
		if len([]rune(low)) < 3 || query.IsStopWord(low) || seen[low] {
			return
		}
		seen[low] = true
		out = append(out, low)
	}
	for _, t := range query.RelevanceTerms(q) {
		add(t)
	}
	for _, t := range changeIdentParts(q) {
		add(t)
	}
	if len(out) == 0 {
		for _, t := range query.Tokens(q) {
			add(t)
		}
	}
	return out
}

func changeRawQueryFields(q string) []string {
	return strings.FieldsFunc(q, func(r rune) bool {
		if r < 128 {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
				r != '-' && r != '_' && r != '.' && r != '/'
		}
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func hasInternalIdentShape(raw string) bool {
	if strings.ContainsAny(raw, "._/-") {
		return true
	}
	hasUpper, hasLower, hasDigit, hasLetter := false, false, false, false
	for i, r := range raw {
		if unicode.IsLetter(r) {
			hasLetter = true
			if unicode.IsUpper(r) && i > 0 {
				hasUpper = true
			}
			if unicode.IsLower(r) {
				hasLower = true
			}
		} else if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	return (hasUpper && hasLower) || (hasLetter && hasDigit)
}

func stripChangeHTMLComments(s string) string {
	if !strings.Contains(s, "<!--") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		rel := strings.Index(s[i:], "<!--")
		if rel < 0 {
			b.WriteString(s[i:])
			break
		}
		start := i + rel
		b.WriteString(s[i:start])
		endRel := strings.Index(s[start+4:], "-->")
		if endRel < 0 {
			b.WriteString(s[start:])
			break
		}
		b.WriteByte(' ')
		i = start + 4 + endRel + 3
	}
	return b.String()
}

func isChangeURLBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f', ')', ']', '>', '"', '\'', '`':
		return true
	}
	return false
}

func stripChangeURLs(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		var schemeLen int
		if i+8 <= len(s) && strings.EqualFold(s[i:i+8], "https://") {
			schemeLen = 8
		} else if i+7 <= len(s) && strings.EqualFold(s[i:i+7], "http://") {
			schemeLen = 7
		}
		if schemeLen > 0 {
			j := i + schemeLen
			for j < len(s) && !isChangeURLBoundary(s[j]) {
				j++
			}
			b.WriteByte(' ')
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func cleanChangeQuery(q string) string {
	if withoutComments := stripChangeHTMLComments(q); withoutComments != q && len(query.RelevanceTerms(withoutComments)) >= 2 {
		q = withoutComments
	}
	if strings.Contains(q, "\n") || len(query.RelevanceTerms(q)) >= 8 {
		if withoutURLs := stripChangeURLs(q); withoutURLs != q && len(query.RelevanceTerms(withoutURLs)) >= 2 {
			q = withoutURLs
		}
	}
	return q
}

func changeQueryGroups(q string) []changeQueryGroup {
	q = cleanChangeQuery(q)
	rel := query.RelevanceTerms(q)
	if len(rel) == 0 {
		rel = append(rel, changeQueryTerms(q)...)
	}
	if len(rel) == 0 {
		return nil
	}
	relSet := make(map[string]bool, len(rel))
	for _, t := range rel {
		relSet[t] = true
	}
	groups := make([]changeQueryGroup, 0, len(rel))
	seen := make(map[string]bool, len(rel))
	for _, raw := range changeRawQueryFields(q) {
		low := query.Lower(raw)
		if !relSet[low] || seen[low] {
			continue
		}
		seen[low] = true
		var parts []string
		for _, p := range changeIdentParts(raw) {
			if p != low {
				parts = append(parts, p)
			}
		}
		groups = append(groups, changeQueryGroup{
			whole:   low,
			parts:   parts,
			isIdent: hasInternalIdentShape(raw),
		})
	}
	for _, t := range rel {
		if seen[t] {
			continue
		}
		seen[t] = true
		groups = append(groups, changeQueryGroup{whole: t})
	}
	return groups
}

func changeChangedOnly(c sources.Change) ([]string, string) {
	var rawLines []string
	switch {
	case c.Old != "" && c.New != "":
		for l := range strings.SplitSeq(changeDiff(c.Old, c.New), "\n") {
			if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "+ ") {
				rawLines = append(rawLines, l)
			}
		}
	case c.Old != "":
		for l := range strings.SplitSeq(strings.TrimSpace(c.Old), "\n") {
			rawLines = append(rawLines, "- "+l)
		}
	case c.New != "":
		for l := range strings.SplitSeq(strings.TrimSpace(c.New), "\n") {
			rawLines = append(rawLines, "+ "+l)
		}
	}
	if len(rawLines) == 0 {
		return nil, ""
	}
	return rawLines, strings.Join(rawLines, "\n")
}

func isTestChangePath(p string) bool {
	low := strings.ToLower(filepath.ToSlash(p))
	for _, seg := range []string{"/tests/", "/test/", "/__tests__/", "/testdata/", "/testing/"} {
		if strings.Contains(low, seg) || strings.HasPrefix(low, seg[1:]) {
			return true
		}
	}
	base := filepath.Base(low)
	return base == "tests.py" ||
		strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, "_test.py") ||
		strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".test.js") ||
		strings.HasSuffix(base, ".spec.ts") ||
		strings.HasSuffix(base, ".spec.js") ||
		strings.HasSuffix(base, "_spec.rb")
}

func isDocChangePath(p string) bool {
	low := strings.ToLower(filepath.ToSlash(p))
	for _, seg := range []string{"/docs/", "/doc/", "/changelog/", "/changes/", "/news/", "/releasenotes/"} {
		if strings.Contains(low, seg) || strings.HasPrefix(low, seg[1:]) {
			return true
		}
	}
	switch filepath.Ext(low) {
	case ".rst", ".md", ".txt", ".adoc":
		return true
	}
	return false
}

func changeTokenSet(tokens []string) map[string]bool {
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		set[t] = true
	}
	return set
}

func changeTermHit(term, low string, idents map[string]bool) bool {
	if idents[term] {
		return true
	}
	if len(term) < 5 || !strings.Contains(low, term) {
		return false
	}
	if strings.ContainsAny(term, "._/-") {
		return true
	}
	for id := range idents {
		if len(id) > len(term) && (strings.HasPrefix(id, term) || strings.HasSuffix(id, term)) {
			return true
		}
	}
	return false
}

func changeGroupHit(g changeQueryGroup, low string, idents map[string]bool) bool {
	if changeTermHit(g.whole, low, idents) {
		return true
	}
	if len(g.parts) == 0 {
		return false
	}
	hits := 0
	longHit := false
	for _, p := range g.parts {
		if changeTermHit(p, low, idents) {
			hits++
			if len([]rune(p)) >= 4 {
				longHit = true
			}
		}
	}
	if strings.ContainsAny(g.whole, "._/-") {
		return longHit || hits >= 2
	}
	return len(g.parts) >= 2 && hits == len(g.parts)
}

func isGenericInitFile(baseLow string) bool {
	switch baseLow {
	case "__init__.py", "index.ts", "index.tsx", "index.js", "index.jsx", "mod.rs", "main.go":
		return true
	}
	return false
}

func stripChangeLayoutDirs(parts []string) []string {
	for len(parts) >= 2 {
		switch parts[0] {
		case "src", "lib", "pkg", "internal", "python":
			parts = parts[1:]
		default:
			return parts
		}
	}
	return parts
}

func changeNonRootPath(pathLow string) string {
	pathLow = strings.TrimLeft(pathLow, "/")
	if pathLow == "" {
		return ""
	}
	parts := strings.Split(pathLow, "/")
	if len(parts) <= 1 {
		return pathLow
	}
	// Strip container directories so /testbed/repo/pkg drops both container
	// and repo root instead of exposing the repository name as a path token.
	for len(parts) > 1 {
		switch parts[0] {
		case "private", "tmp", "var", "opt", "usr", "mnt", "srv",
			"testbed", "workspace", "work", "repo", "app", "project", "projects", "code", "coding":
			parts = parts[1:]
		case "users", "home":
			if len(parts) > 2 {
				parts = parts[2:]
			} else {
				parts = parts[1:]
			}
		default:
			goto stripped
		}
	}
stripped:
	parts = stripChangeLayoutDirs(parts)
	for len(parts) >= 3 && parts[0] == parts[1] {
		parts = parts[1:]
	}
	if len(parts) >= 3 || (len(parts) == 2 && !isGenericInitFile(parts[1])) {
		parts = parts[1:]
	}
	parts = stripChangeLayoutDirs(parts)
	return strings.Join(parts, "/")
}

type changeEvidenceView struct {
	baseLow          string
	nonRoot          string
	changedLow       string
	hunkLow          string
	baseIdents       map[string]bool
	nonRootIdents    map[string]bool
	changedIdents    map[string]bool
	hunkIdents       map[string]bool
	enclosingIdents  map[string]bool
	changedDefIdents map[string]bool
}

var changeDefHeaderPrefixes = []string{
	"export async function ",
	"export function ",
	"export class ",
	"async function ",
	"function ",
	"async def ",
	"def ",
	"class ",
	"pub async fn ",
	"pub fn ",
	"async fn ",
	"fn ",
	"func ",
}

func changeLineIndent(s string) int {
	if strings.TrimSpace(s) == "" {
		return -1
	}
	n := 0
	for i := range len(s) {
		switch s[i] {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return -1
}

func trimChangeDefPrefix(trimmed string) (string, bool) {
	for _, p := range changeDefHeaderPrefixes {
		if strings.HasPrefix(trimmed, p) {
			return trimmed[len(p):], true
		}
	}
	return "", false
}

func isChangeDefHeaderLine(trimmed string) bool {
	_, ok := trimChangeDefPrefix(trimmed)
	return ok
}

func declaredDefSymbol(trimmed string) string {
	rest, ok := trimChangeDefPrefix(trimmed)
	if !ok {
		return ""
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "(") {
		if closeIdx := strings.IndexByte(rest, ')'); closeIdx >= 0 {
			rest = strings.TrimSpace(rest[closeIdx+1:])
		}
	}
	end := len(rest)
	for i, r := range rest {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			end = i
			break
		}
	}
	return rest[:end]
}

func enclosingChangeHeaders(oldText, newText string) string {
	if oldText == "" || newText == "" {
		return ""
	}
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	if prefix == 0 {
		return ""
	}
	firstIndent := func(lines []string) int {
		for _, l := range lines {
			if ind := changeLineIndent(l); ind >= 0 {
				return ind
			}
		}
		return -1
	}
	indOld := firstIndent(oldLines[prefix:])
	indNew := firstIndent(newLines[prefix:])
	var targetIndent int
	switch {
	case indOld >= 0 && indNew >= 0:
		targetIndent = min(indOld, indNew)
	case indOld >= 0:
		targetIndent = indOld
	case indNew >= 0:
		targetIndent = indNew
	default:
		return ""
	}
	var headers []string
	for i := prefix - 1; i >= 0 && prefix-i <= 80; i-- {
		ind := changeLineIndent(oldLines[i])
		if ind < 0 {
			continue
		}
		trimmed := strings.TrimSpace(oldLines[i])
		if ind < targetIndent && isChangeDefHeaderLine(trimmed) {
			headers = append(headers, trimmed)
			targetIndent = ind
			if ind == 0 {
				break
			}
		}
	}
	return strings.Join(headers, "\n")
}

func boundHunkPart(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := 0
	for i := range s {
		if i > maxBytes {
			break
		}
		cut = i
	}
	return s[:cut]
}

func newChangeEvidenceView(c sources.Change) changeEvidenceView {
	pathLow := strings.ToLower(filepath.ToSlash(c.Path))
	nonRoot := changeNonRootPath(pathLow)
	baseLow := strings.ToLower(filepath.Base(pathLow))
	changedLines, changed := changeChangedOnly(c)
	changedLow := strings.ToLower(changed)
	var defSyms []string
	for _, line := range changedLines {
		raw := strings.TrimPrefix(line, "- ")
		raw = strings.TrimPrefix(raw, "+ ")
		if sym := declaredDefSymbol(strings.TrimSpace(raw)); sym != "" {
			defSyms = append(defSyms, sym)
		}
	}
	changedDefIdents := changeTokenSet(changeIdentParts(strings.Join(defSyms, " ")))
	enclosing := enclosingChangeHeaders(c.Old, c.New)
	var encSyms []string
	if enclosing != "" {
		for line := range strings.SplitSeq(enclosing, "\n") {
			if sym := declaredDefSymbol(strings.TrimSpace(line)); sym != "" {
				encSyms = append(encSyms, sym)
			}
		}
	}
	enclosingIdents := changeTokenSet(changeIdentParts(strings.Join(encSyms, " ")))
	// Extract the actual edited hunk(s) instead of head-slicing c.Old/c.New so
	// edits deep in large files keep their local context for query binding.
	var hunkSrc string
	if c.Old == "" {
		hunkSrc = boundHunkPart(c.New, 4096)
	} else {
		diff := changeDiff(c.Old, c.New)
		if diff == "" {
			diff = c.New
		}
		hunkSrc = boundHunkPart(diff, 4096)
		if enclosing != "" {
			hunkSrc += "\n" + enclosing
		}
	}
	hunkLow := strings.ToLower(hunkSrc)
	return changeEvidenceView{
		baseLow:          baseLow,
		nonRoot:          nonRoot,
		changedLow:       changedLow,
		hunkLow:          hunkLow,
		baseIdents:       changeTokenSet(changeIdentParts(baseLow)),
		nonRootIdents:    changeTokenSet(changeIdentParts(nonRoot)),
		changedIdents:    changeTokenSet(changeIdentParts(changed)),
		hunkIdents:       changeTokenSet(changeIdentParts(hunkSrc)),
		enclosingIdents:  enclosingIdents,
		changedDefIdents: changedDefIdents,
	}
}

func (v changeEvidenceView) codeHit(g changeQueryGroup) bool {
	return changeGroupHit(g, v.changedLow, v.changedIdents) || changeGroupHit(g, v.baseLow, v.baseIdents)
}

func (v changeEvidenceView) hunkHit(g changeQueryGroup) bool {
	return v.codeHit(g) || changeGroupHit(g, v.hunkLow, v.hunkIdents)
}

func isGenericPathToken(w string) bool {
	switch w {
	case "init", "main", "index", "utils", "util", "helper", "helpers",
		"common", "base", "core", "types", "errors", "error", "config",
		"tests", "test", "testing", "fixture", "fixtures", "contrib",
		"models", "model", "views", "fields", "field", "forms", "admin",
		"client", "server", "query", "queries", "parser", "internal", "python":
		return true
	}
	return false
}

func changePathStemHit(g changeQueryGroup, pathIdents map[string]bool) bool {
	for id := range pathIdents {
		if isGenericPathToken(id) {
			continue
		}
		stem := strings.TrimSuffix(id, "s")
		if len(stem) < 5 {
			stem = id
		}
		if len(stem) >= 5 && len(g.whole) >= len(stem) && strings.Contains(g.whole, stem) {
			return true
		}
	}
	return false
}

func (v changeEvidenceView) pathHit(g changeQueryGroup) bool {
	if isGenericPathToken(g.whole) {
		return false
	}
	return changeGroupHit(g, v.nonRoot, v.nonRootIdents) || changePathStemHit(g, v.nonRootIdents)
}

func (v changeEvidenceView) wholeCodeHit(whole string) bool {
	return changeTermHit(whole, v.changedLow, v.changedIdents) || changeTermHit(whole, v.baseLow, v.baseIdents)
}

func rankChangesForQuery(cs []sources.Change, q string) []sources.Change {
	return rankChangesForQueryWithIDF(cs, q, nil)
}

func changeGroupIDF(g changeQueryGroup, termIDF map[string]float64) (idf float64, known bool) {
	if len(termIDF) == 0 {
		return 0, false
	}
	if v, ok := termIDF[g.whole]; ok {
		return v, true
	}
	for _, p := range g.parts {
		if v, ok := termIDF[p]; ok && (!known || v > idf) {
			idf = v
			known = true
		}
	}
	return idf, known
}

func canonChangeToken(tok string) string {
	c := strings.TrimPrefix(tok, "self.")
	c = strings.TrimPrefix(c, "cls.")
	c = strings.TrimLeft(c, "-_")
	if c == "" {
		return tok
	}
	return c
}

func isUbiquitousDocPlotToken(tok string) bool {
	switch tok {
	case "plt", "fig", "figure", "axes", "env", "docname",
		"problem", "minimal", "version", "example", "output",
		"warning", "backward", "compatibility":
		return true
	}
	return false
}

func changeIDFFloors(topIDF float64, groupCount int) (base, prose, ubiq, direct float64) {
	if topIDF <= 0 || math.IsInf(topIDF, 1) || topIDF >= index.InformativeIDF {
		return index.InformativeIDF, 2.35, 2.8, 2.6
	}
	// Tiny store (N <= 13 sessions, topIDF < 2.0): rankIDF(N, 1) cannot reach 2.0.
	if groupCount < 12 {
		f := max(0.40, topIDF-1e-6)
		return f, f, f, f
	}
	// Long issue/document query on a tiny store: require df=1 code identifiers,
	// keeping prose/ubiquitous floors above topIDF so plain prose cannot bind.
	f := max(1.05, topIDF-1e-6)
	return f, 2.35, 2.8, f
}

func changeAnchorTokenValid(tok string, isIdent bool, groupCount int, allowUnknown bool, inHunk bool, termIDF, sessionIDF map[string]float64, topIDF float64) bool {
	if len([]rune(tok)) < 3 || query.IsStopWord(tok) || isBoilerplateCodeToken(tok) || isGenericExceptionIdent(tok) || isGenericPathToken(tok) {
		return false
	}
	tinyLong := topIDF > 0 && !math.IsInf(topIDF, 1) && topIDF < index.InformativeIDF && groupCount >= 12
	if tinyLong && (!isIdent || !inHunk) {
		return false
	}
	baseFloor, proseFloor, ubiqFloor, _ := changeIDFFloors(topIDF, groupCount)
	sessProseFloor := 1.95
	sessUbiqFloor := 2.40
	if topIDF > 0 && !math.IsInf(topIDF, 1) {
		sessProseFloor = min(sessProseFloor, max(1.05, topIDF-1e-6))
		sessUbiqFloor = min(sessUbiqFloor, max(1.05, topIDF-1e-6))
	}
	if groupCount >= 6 && isUbiquitousDocPlotToken(tok) {
		if len(termIDF) > 0 && termIDF[tok] < ubiqFloor {
			return false
		}
		if len(sessionIDF) > 0 {
			if sv, ok := sessionIDF[tok]; !ok || sv < sessUbiqFloor {
				return false
			}
		}
	}
	if len(sessionIDF) > 0 && groupCount >= 6 && !isIdent && !strings.ContainsRune(tok, '_') {
		if sv, ok := sessionIDF[tok]; !ok || sv < sessProseFloor {
			return false
		}
	}
	if len(termIDF) > 0 {
		v, ok := termIDF[tok]
		if !ok {
			return allowUnknown && !tinyLong
		}
		if groupCount >= 15 && !isIdent && !strings.ContainsRune(tok, '_') {
			return v >= proseFloor
		}
		return v >= baseFloor
	}
	return len([]rune(tok)) >= 4 || isIdent
}

func changeGroupRankWeight(g changeQueryGroup, termIDF map[string]float64) int {
	if isBoilerplateCodeToken(g.whole) || isGenericExceptionIdent(g.whole) {
		return 0
	}
	if idf, known := changeGroupIDF(g, termIDF); known {
		switch {
		case idf >= 3.0:
			return 4
		case idf >= index.InformativeIDF:
			return 3
		case idf >= 1.0:
			return 2
		default:
			return 1
		}
	}
	return 2
}

func rankChangesForQueryWithIDF(cs []sources.Change, q string, termIDF map[string]float64) []sources.Change {
	groups := changeQueryGroups(q)
	if len(cs) <= 1 || len(groups) == 0 {
		return cs
	}
	type scoredChange struct {
		c     sources.Change
		tier  int
		score int
	}
	items := make([]scoredChange, len(cs))
	fileScore := make(map[string]int, len(cs))
	for i, c := range cs {
		ev := newChangeEvidenceView(c)
		score := 0
		for _, g := range groups {
			w := changeGroupRankWeight(g, termIDF)
			if w == 0 {
				continue
			}
			switch {
			case ev.codeHit(g):
				score += 10 * w
				if ev.wholeCodeHit(g.whole) {
					score += 4 * w
				}
			case ev.hunkHit(g):
				score += 6 * w
			case ev.pathHit(g):
				score += 5 * w
			}
		}
		if c.TestFailed && score > 0 {
			score -= 2
		}
		norm := filepath.ToSlash(c.Path)
		if score > fileScore[norm] {
			fileScore[norm] = score
		}
		tier := 0
		if isTestChangePath(c.Path) {
			tier = 2
		} else if isDocChangePath(c.Path) {
			tier = 1
		}
		items[i] = scoredChange{c: c, tier: tier, score: score}
	}
	for i := range items {
		items[i].score = fileScore[filepath.ToSlash(items[i].c.Path)]
	}
	slices.SortStableFunc(items, func(a, b scoredChange) int {
		if a.tier != b.tier {
			if a.tier < b.tier {
				return -1
			}
			return 1
		}
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		return 0
	})
	out := make([]sources.Change, len(items))
	for i, it := range items {
		out[i] = it.c
	}
	return out
}

func isGenericExceptionIdent(w string) bool {
	switch w {
	case "typeerror", "valueerror", "importerror", "keyerror", "indexerror",
		"attributeerror", "runtimeerror", "assertionerror", "notimplementederror",
		"zerodivisionerror", "oserror", "ioerror", "stopiteration",
		"deprecationwarning", "userwarning", "futurewarning", "runtimewarning":
		return true
	}
	return false
}

func isBoilerplateCodeToken(w string) bool {
	switch w {
	case "true", "false", "none", "null", "return", "self", "other", "item", "items",
		"class", "def", "func", "function", "method", "module", "import", "from",
		"export", "default", "const", "var", "let", "async", "await", "yield",
		"raise", "throw", "catch", "except", "finally", "while", "break", "continue",
		"pass", "else", "elif", "switch", "case", "struct", "interface", "enum",
		"type", "impl", "pub", "public", "private", "static", "void", "super", "this",
		"args", "kwargs", "data", "value", "values", "name", "names", "path", "file",
		"line", "lines", "list", "dict", "set", "map", "slice", "str", "string",
		"bytes", "int", "float", "bool", "error", "err", "msg", "result", "results",
		"res", "ret", "out", "obj", "cls", "init", "test", "tests", "expected",
		"actual", "check", "assert", "should", "issue", "bug", "fix", "fixed",
		"change", "changed", "update", "updated":
		return true
	}
	return false
}

func changesBindToQuery(cs []sources.Change, q string) bool {
	return changesBindToQueryWithIDF(cs, q, nil)
}

func changesBindToQueryWithIDF(cs []sources.Change, q string, termIDF map[string]float64) bool {
	return changesBindToQueryWithTopIDF(cs, q, termIDF, 0)
}

func changesBindToQueryWithTopIDF(cs []sources.Change, q string, termIDF map[string]float64, topIDF float64) bool {
	return changesBindToQueryWithSessionIDF(cs, q, termIDF, nil, topIDF)
}

type changeBindState struct {
	groups            []changeQueryGroup
	termIDF           map[string]float64
	sessionIDF        map[string]float64
	topIDF            float64
	directFloor       float64
	tinyLong          bool
	hunkHit           []bool
	anyHit            []bool
	infoHunkSet       map[string]bool
	infoAnySet        map[string]bool
	specificDirectHit bool
	rareChangedDefHit bool
	substantiveHit    bool
}

func (st *changeBindState) recordAnchor(canon string, inHunk bool) {
	if inHunk {
		st.infoHunkSet[canon] = true
	}
	st.infoAnySet[canon] = true
}

func (st *changeBindState) noteHunkDirectAndDef(ev changeEvidenceView, g changeQueryGroup) {
	core := canonChangeToken(g.whole)
	wholeMatch := ev.wholeCodeHit(g.whole) || (len(st.groups) < 12 && (changeTermHit(g.whole, ev.hunkLow, ev.hunkIdents) || changeTermHit(g.whole, ev.baseLow, ev.baseIdents)))
	if st.tinyLong {
		wholeMatch = changeTermHit(g.whole, ev.changedLow, ev.changedIdents) || changeTermHit(g.whole, ev.hunkLow, ev.hunkIdents)
	}
	if g.isIdent && len(core) >= 7 && !isGenericExceptionIdent(core) && !isBoilerplateCodeToken(core) && !isGenericPathToken(core) && wholeMatch {
		idf, known := changeGroupIDF(g, st.termIDF)
		enclosingOrChangedDef := ev.enclosingIdents[core] || ev.changedDefIdents[core]
		tinyMultiSegDef := st.tinyLong && enclosingOrChangedDef && len(g.parts) >= 3 && len(core) >= 15
		if len(st.termIDF) == 0 || (known && idf >= st.directFloor) || tinyMultiSegDef || (!known && !st.tinyLong && ev.wholeCodeHit(g.whole) && !strings.ContainsAny(core, "./-")) {
			st.specificDirectHit = true
		}
	}
	if !st.tinyLong && len([]rune(core)) >= 4 && ev.changedDefIdents[core] &&
		!query.IsStopWord(core) && !isBoilerplateCodeToken(core) && !isGenericExceptionIdent(core) &&
		!isGenericPathToken(core) && !isUbiquitousDocPlotToken(core) && len(st.termIDF) > 0 {
		idf, known := st.termIDF[core]
		sessOK := len(st.sessionIDF) == 0 || st.sessionIDF[core] >= 2.40
		if known && idf >= st.directFloor && sessOK {
			st.rareChangedDefHit = true
		}
	}
}

func (st *changeBindState) matchGroupPartsFallback(ev changeEvidenceView, idx int, g changeQueryGroup) {
	for _, p := range g.parts {
		if len([]rune(p)) < 4 || isBoilerplateCodeToken(p) || isGenericExceptionIdent(p) || isGenericPathToken(p) {
			continue
		}
		pHunk := changeTermHit(p, ev.hunkLow, ev.hunkIdents)
		pPath := !isGenericPathToken(p) && changeTermHit(p, ev.nonRoot, ev.nonRootIdents)
		if !pHunk && !pPath {
			continue
		}
		st.anyHit[idx] = true
		st.substantiveHit = true
		if pHunk {
			st.hunkHit[idx] = true
		}
		pIsIdent := hasInternalIdentShape(p) || strings.ContainsRune(p, '_') || (g.isIdent && !st.tinyLong)
		pAllowUnknown := !st.tinyLong && g.isIdent && len(st.termIDF) == 0
		if changeAnchorTokenValid(p, pIsIdent, len(st.groups), pAllowUnknown, pHunk, st.termIDF, st.sessionIDF, st.topIDF) {
			st.recordAnchor(p, pHunk)
		}
	}
}

func (st *changeBindState) recordMatchingPartAnchors(ev changeEvidenceView, parts []string) bool {
	counted := false
	for _, p := range parts {
		pHunk := changeTermHit(p, ev.hunkLow, ev.hunkIdents)
		pPath := changeTermHit(p, ev.nonRoot, ev.nonRootIdents)
		if (pHunk || pPath) && changeAnchorTokenValid(p, hasInternalIdentShape(p) || strings.ContainsRune(p, '_'), len(st.groups), false, pHunk, st.termIDF, st.sessionIDF, st.topIDF) {
			st.recordAnchor(p, pHunk)
			counted = true
		}
	}
	return counted
}

func (st *changeBindState) recordPathStemAnchor(ev changeEvidenceView, g changeQueryGroup, pHit bool) {
	if !pHit || !changePathStemHit(g, ev.nonRootIdents) {
		return
	}
	canon := canonChangeToken(g.whole)
	if changeAnchorTokenValid(canon, g.isIdent, len(st.groups), false, false, st.termIDF, st.sessionIDF, st.topIDF) {
		st.recordAnchor(canon, false)
	}
}

func (st *changeBindState) recordGroupAnchors(ev changeEvidenceView, g changeQueryGroup, pHit bool) {
	wholeHunk := changeTermHit(g.whole, ev.hunkLow, ev.hunkIdents)
	wholePath := !isGenericPathToken(g.whole) && changeTermHit(g.whole, ev.nonRoot, ev.nonRootIdents)
	switch {
	case wholeHunk || wholePath:
		canon := canonChangeToken(g.whole)
		allowUnknown := ev.wholeCodeHit(g.whole) && len(g.whole) >= 6 && !strings.ContainsAny(canon, "./-")
		if changeAnchorTokenValid(canon, g.isIdent, len(st.groups), allowUnknown, wholeHunk, st.termIDF, st.sessionIDF, st.topIDF) {
			st.recordAnchor(canon, wholeHunk)
		} else if _, ok := st.termIDF[g.whole]; !ok && strings.ContainsAny(g.whole, "._/-") {
			st.recordMatchingPartAnchors(ev, g.parts)
		}
	case len(g.parts) > 0:
		if !st.recordMatchingPartAnchors(ev, g.parts) {
			st.recordPathStemAnchor(ev, g, pHit)
		}
	default:
		st.recordPathStemAnchor(ev, g, pHit)
	}
}

func (st *changeBindState) observeChange(ev changeEvidenceView) {
	for i, g := range st.groups {
		hHit := ev.hunkHit(g)
		pHit := ev.pathHit(g)
		if hHit {
			st.hunkHit[i] = true
			st.noteHunkDirectAndDef(ev, g)
		}
		if !hHit && !pHit {
			st.matchGroupPartsFallback(ev, i, g)
			continue
		}
		st.anyHit[i] = true
		if !isBoilerplateCodeToken(g.whole) && !isGenericExceptionIdent(g.whole) {
			st.substantiveHit = true
		}
		st.recordGroupAnchors(ev, g, pHit)
	}
}

func changesBindToQueryWithSessionIDF(cs []sources.Change, q string, termIDF, sessionIDF map[string]float64, topIDF float64) bool {
	groups := changeQueryGroups(q)
	if len(groups) == 0 {
		return true
	}
	_, _, _, directFloor := changeIDFFloors(topIDF, len(groups))
	primary := make([]sources.Change, 0, len(cs))
	for _, c := range cs {
		if !isTestChangePath(c.Path) && !isDocChangePath(c.Path) {
			primary = append(primary, c)
		}
	}
	if len(primary) == 0 {
		primary = cs
	}
	st := changeBindState{
		groups:      groups,
		termIDF:     termIDF,
		sessionIDF:  sessionIDF,
		topIDF:      topIDF,
		directFloor: directFloor,
		tinyLong:    topIDF > 0 && !math.IsInf(topIDF, 1) && topIDF < index.InformativeIDF && len(groups) >= 12,
		hunkHit:     make([]bool, len(groups)),
		anyHit:      make([]bool, len(groups)),
		infoHunkSet: map[string]bool{},
		infoAnySet:  map[string]bool{},
	}
	for _, c := range primary {
		st.observeChange(newChangeEvidenceView(c))
	}
	if len(groups) >= 6 && !st.substantiveHit {
		return false
	}
	hunkCount, anyCount := 0, 0
	for i := range groups {
		if st.hunkHit[i] {
			hunkCount++
		}
		if st.anyHit[i] {
			anyCount++
		}
	}
	infoHunkCount, infoAnyCount := len(st.infoHunkSet), len(st.infoAnySet)
	switch len(groups) {
	case 1:
		return anyCount >= 1
	case 2:
		if len(termIDF) > 0 {
			return infoHunkCount >= 2 || (infoHunkCount >= 1 && (infoAnyCount >= 2 || st.specificDirectHit))
		}
		return hunkCount >= 2 || (hunkCount >= 1 && (anyCount >= 2 || st.specificDirectHit))
	case 3, 4, 5:
		if len(termIDF) > 0 {
			return infoHunkCount >= 2 || (infoHunkCount >= 1 && infoAnyCount >= 2) || st.specificDirectHit
		}
		return hunkCount >= 2 || (hunkCount >= 1 && anyCount >= 2) || st.specificDirectHit
	default:
		if st.specificDirectHit && (len(groups) < 15 || infoAnyCount >= 2 || (st.tinyLong && anyCount >= 2)) {
			return true
		}
		if st.rareChangedDefHit && infoHunkCount >= 1 && anyCount >= 2 {
			return true
		}
		if len(termIDF) > 0 {
			return infoHunkCount >= 2 || (infoHunkCount >= 1 && infoAnyCount >= 3)
		}
		return infoHunkCount >= 2 || (infoHunkCount >= 1 && infoAnyCount >= 2)
	}
}

// changeContext is how many unchanged lines a diff keeps on each side.
const changeContext = 2

type diffMatch struct {
	ai, bi int
}

type diffEdit struct {
	aStart, aEnd int
	bStart, bEnd int
}

func patienceLISMatches(a []string, aOff int, b []string, bOff int) []diffMatch {
	type occ struct {
		count int
		idx   int
	}
	aOcc := make(map[string]occ, len(a))
	for i, s := range a {
		o := aOcc[s]
		o.count++
		o.idx = i
		aOcc[s] = o
	}
	bOcc := make(map[string]occ, len(b))
	for j, s := range b {
		o := bOcc[s]
		o.count++
		o.idx = j
		bOcc[s] = o
	}
	var cands []diffMatch
	for i, s := range a {
		ao := aOcc[s]
		if ao.count != 1 {
			continue
		}
		bo := bOcc[s]
		if bo.count == 1 {
			cands = append(cands, diffMatch{i, bo.idx})
		}
	}
	if len(cands) == 0 {
		return nil
	}
	tails := make([]int, 0, len(cands))
	prev := make([]int, len(cands))
	for i, c := range cands {
		lo, hi := 0, len(tails)
		for lo < hi {
			mid := (lo + hi) / 2
			if cands[tails[mid]].bi < c.bi {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo > 0 {
			prev[i] = tails[lo-1]
		} else {
			prev[i] = -1
		}
		if lo == len(tails) {
			tails = append(tails, i)
		} else {
			tails[lo] = i
		}
	}
	res := make([]diffMatch, len(tails))
	cur := tails[len(tails)-1]
	for k := len(tails) - 1; k >= 0; k-- {
		res[k] = diffMatch{aOff + cands[cur].ai, bOff + cands[cur].bi}
		cur = prev[cur]
	}
	return res
}

func dpLCSMatches(a []string, aOff int, b []string, bOff int) []diffMatch {
	const maxDPLines = 4096
	m, n := len(a), len(b)
	if m <= 0 || m > maxDPLines || n <= 0 || n > maxDPLines || m*n > 250000 {
		return nil
	}
	cols := n + 1
	dp := make([]int32, (m+1)*cols)
	for i := m - 1; i >= 0; i-- {
		row := i * cols
		nextRow := (i + 1) * cols
		for j := n - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[row+j] = dp[nextRow+j+1] + 1
			case dp[nextRow+j] >= dp[row+j+1]:
				dp[row+j] = dp[nextRow+j]
			default:
				dp[row+j] = dp[row+j+1]
			}
		}
	}
	matches := make([]diffMatch, 0, 16)
	i, j := 0, 0
	for i < m && j < n {
		switch {
		case a[i] == b[j]:
			matches = append(matches, diffMatch{aOff + i, bOff + j})
			i++
			j++
		case dp[(i+1)*cols+j] >= dp[i*cols+j+1]:
			i++
		default:
			j++
		}
	}
	return matches
}

func collectLCSMatches(a []string, aOff int, b []string, bOff int, out *[]diffMatch) {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		*out = append(*out, diffMatch{aOff + pre, bOff + pre})
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	subA := a[pre : len(a)-suf]
	subB := b[pre : len(b)-suf]
	aSubOff, bSubOff := aOff+pre, bOff+pre
	if len(subA) > 0 && len(subB) > 0 {
		anchors := patienceLISMatches(subA, 0, subB, 0)
		if len(anchors) > 0 {
			prevA, prevB := 0, 0
			for _, anc := range anchors {
				if anc.ai > prevA && anc.bi > prevB {
					collectLCSMatches(subA[prevA:anc.ai], aSubOff+prevA, subB[prevB:anc.bi], bSubOff+prevB, out)
				}
				*out = append(*out, diffMatch{aSubOff + anc.ai, bSubOff + anc.bi})
				prevA, prevB = anc.ai+1, anc.bi+1
			}
			if prevA < len(subA) && prevB < len(subB) {
				collectLCSMatches(subA[prevA:], aSubOff+prevA, subB[prevB:], bSubOff+prevB, out)
			}
		} else {
			*out = append(*out, dpLCSMatches(subA, aSubOff, subB, bSubOff)...)
		}
	}
	for k := range suf {
		*out = append(*out, diffMatch{aOff + len(a) - suf + k, bOff + len(b) - suf + k})
	}
}

// changeDiff shows an edit as the lines it removed and added, with a little of
// the unchanged code around them. Distant edits inside the same file are split
// into separate context hunks joined by "  …" instead of quoting unchanged
// lines between them as removed and re-added.
func changeDiff(old, updated string) string {
	a, b := strings.Split(old, "\n"), strings.Split(updated, "\n")
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	if pre == len(a) && pre == len(b) {
		return ""
	}
	midA := a[pre : len(a)-suf]
	midB := b[pre : len(b)-suf]
	if len(midA) == 0 || len(midB) == 0 || len(midA)+len(midB) <= 8 {
		out := make([]string, 0, 16)
		for _, l := range a[max(0, pre-changeContext):pre] {
			out = append(out, "  "+l)
		}
		for _, l := range midA {
			out = append(out, "- "+l)
		}
		for _, l := range midB {
			out = append(out, "+ "+l)
		}
		for _, l := range a[len(a)-suf : min(len(a), len(a)-suf+changeContext)] {
			out = append(out, "  "+l)
		}
		return strings.Join(out, "\n")
	}

	matches := make([]diffMatch, 0, 8)
	collectLCSMatches(midA, pre, midB, pre, &matches)
	matches = append(matches, diffMatch{len(a) - suf, len(b) - suf})
	edits := make([]diffEdit, 0, len(matches))
	prevA, prevB := pre, pre
	for _, m := range matches {
		if m.ai > prevA || m.bi > prevB {
			edits = append(edits, diffEdit{aStart: prevA, aEnd: m.ai, bStart: prevB, bEnd: m.bi})
		}
		prevA, prevB = m.ai+1, m.bi+1
	}
	if len(edits) == 0 {
		return ""
	}
	out := make([]string, 0, 32)
	for _, l := range a[max(0, edits[0].aStart-changeContext):edits[0].aStart] {
		out = append(out, "  "+l)
	}
	for i, e := range edits {
		for _, l := range a[e.aStart:e.aEnd] {
			out = append(out, "- "+l)
		}
		for _, l := range b[e.bStart:e.bEnd] {
			out = append(out, "+ "+l)
		}
		if i+1 < len(edits) {
			next := edits[i+1]
			gap := next.aStart - e.aEnd
			if gap <= 2*changeContext {
				for _, l := range a[e.aEnd:next.aStart] {
					out = append(out, "  "+l)
				}
			} else {
				for _, l := range a[e.aEnd : e.aEnd+changeContext] {
					out = append(out, "  "+l)
				}
				out = append(out, "  …")
				for _, l := range a[next.aStart-changeContext : next.aStart] {
					out = append(out, "  "+l)
				}
			}
		} else {
			for _, l := range a[e.aEnd:min(len(a), e.aEnd+changeContext)] {
				out = append(out, "  "+l)
			}
		}
	}
	return strings.Join(out, "\n")
}

func collapseLongDeletions(diff string, maxDelRun int) string {
	lines := strings.Split(diff, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if !strings.HasPrefix(lines[i], "- ") {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && strings.HasPrefix(lines[j], "- ") {
			j++
		}
		run := j - i
		if run > maxDelRun {
			const (
				keepHead = 4
				keepTail = 2
			)
			out = append(out, lines[i:i+keepHead]...)
			out = append(out, fmt.Sprintf("- … (%d lines deleted)", run-keepHead-keepTail))
			out = append(out, lines[j-keepTail:j]...)
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return strings.Join(out, "\n")
}

func centerTextOnQuery(text, q string, maxBytes int) string {
	trimmed := strings.Trim(text, "\n")
	if len(trimmed) <= maxBytes || strings.TrimSpace(q) == "" {
		return clipChange(trimmed, maxBytes)
	}
	groups := changeQueryGroups(q)
	if len(groups) == 0 {
		return clipChange(trimmed, maxBytes)
	}
	lines := strings.Split(trimmed, "\n")
	bestLine, bestScore := -1, 0
	for i, l := range lines {
		low := strings.ToLower(l)
		idents := changeTokenSet(changeIdentParts(l))
		score := 0
		for _, g := range groups {
			if isBoilerplateCodeToken(g.whole) {
				continue
			}
			if changeGroupHit(g, low, idents) {
				if g.isIdent {
					score += 3
				} else {
					score++
				}
			}
		}
		if score > bestScore {
			bestScore = score
			bestLine = i
		}
	}
	if bestLine <= 0 {
		return clipChange(trimmed, maxBytes)
	}
	start := max(0, bestLine-4)
	end := start
	used := 0
	for end < len(lines) {
		add := len(lines[end])
		if end > start {
			add++
		}
		if used+add > maxBytes-8 {
			break
		}
		used += add
		end++
	}
	for start > 0 && end == len(lines) {
		add := len(lines[start-1]) + 1
		if used+add > maxBytes-8 {
			break
		}
		used += add
		start--
	}
	if end <= start {
		return clipChange(trimmed, maxBytes)
	}
	out := strings.Join(lines[start:end], "\n")
	if start > 0 {
		out = "…\n" + out
	}
	if end < len(lines) {
		out += "\n…"
	}
	return out
}

func reorderHunksByQuery(diff, q string) string {
	hunks := strings.Split(diff, "\n  …\n")
	if len(hunks) <= 1 || strings.TrimSpace(q) == "" {
		return diff
	}
	groups := changeQueryGroups(q)
	type scoredHunk struct {
		h     string
		idx   int
		score int
	}
	scored := make([]scoredHunk, len(hunks))
	for i, h := range hunks {
		score := 0
		if strings.Contains(h, "\n+ ") || strings.HasPrefix(h, "+ ") {
			score++
		}
		low := strings.ToLower(h)
		idents := changeTokenSet(changeIdentParts(h))
		for _, g := range groups {
			if isBoilerplateCodeToken(g.whole) {
				continue
			}
			if changeGroupHit(g, low, idents) {
				if g.isIdent {
					score += 4
				} else {
					score += 2
				}
			}
		}
		scored[i] = scoredHunk{h: h, idx: i, score: score}
	}
	slices.SortStableFunc(scored, func(a, b scoredHunk) int {
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		return a.idx - b.idx
	})
	ordered := make([]string, len(scored))
	for i, s := range scored {
		ordered[i] = s.h
	}
	return strings.Join(ordered, "\n  …\n")
}

func formatChangeDiffBounded(c sources.Change, q string, maxBytes int) string {
	if c.Old == "" {
		return centerTextOnQuery(c.New, q, maxBytes)
	}
	diff := changeDiff(c.Old, c.New)
	if diff == "" {
		return ""
	}
	diff = collapseLongDeletions(diff, 8)
	if len(diff) > maxBytes {
		diff = reorderHunksByQuery(diff, q)
	}
	return clipChange(diff, maxBytes)
}

// sessionChanges returns the final code changes of s, reading from the raw
// transcript when present and falling back to indexed RoleEdit/RoleWrote
// records when the session carries only in-index messages.
func sessionChanges(s model.Session) []sources.Change {
	if cs := sources.FinalChanges(sources.Changes(s)); len(cs) > 0 {
		return cs
	}
	if hasExistingTranscript(s) {
		return nil
	}
	var raw []sources.Change
	for i := 0; i < len(s.Messages); i++ {
		m := s.Messages[i]
		if m.Role != sources.RoleEdit && m.Role != sources.RoleWrote {
			continue
		}
		p, body, ok := strings.Cut(m.Text, "\n")
		p = strings.TrimSpace(p)
		body = strings.TrimSpace(body)
		if !ok || p == "" || body == "" {
			continue
		}
		if m.Role == sources.RoleEdit {
			var newText string
			if i+1 < len(s.Messages) && s.Messages[i+1].Role == sources.RoleWrote {
				if wp, wbody, wok := strings.Cut(s.Messages[i+1].Text, "\n"); wok && strings.TrimSpace(wp) == p {
					stripped := stripWrotePrefix(wbody)
					if !isHashedWrote(stripped) {
						newText = stripped
					}
					i++
				}
			}
			raw = append(raw, sources.Change{Path: p, Old: body, New: newText})
			continue
		}
		stripped := stripWrotePrefix(body)
		if isHashedWrote(stripped) {
			continue
		}
		raw = append(raw, sources.Change{Path: p, New: stripped})
	}
	return sources.FinalChanges(raw)
}

func hasExistingTranscript(s model.Session) bool {
	if s.Path == "" {
		return false
	}
	_, err := os.Stat(s.Path)
	return err == nil
}

// isHashedWrote reports whether a RoleWrote body holds space-separated 64-bit
// line hashes produced by sources.WroteRecord instead of raw code lines.
func isHashedWrote(body string) bool {
	if body == "" || strings.ContainsAny(body, "\n\r") {
		return false
	}
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return false
	}
	minLen := 8
	if len(fields) == 1 {
		minLen = 10
	}
	for _, f := range fields {
		if len(f) < minLen || len(f) > 16 {
			return false
		}
		for i := range len(f) {
			c := f[i]
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

func stripWrotePrefix(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		if after, ok := strings.CutPrefix(l, "+ "); ok {
			lines[i] = after
		}
	}
	return strings.Join(lines, "\n")
}

const recallCodeDiffMax = 240

// recallCodeChangedLines returns a compact diff excerpt from the first file edit
// whose removed or added code matches one of the query's terms.
func recallCodeChangedLines(s model.Session, q string) []string {
	terms := changeQueryTerms(q)
	if len(terms) == 0 {
		return nil
	}
	cs := rankChangesForQuery(sessionChanges(s), q)
	for _, c := range cs {
		lowOld, lowNew, lowPath := strings.ToLower(c.Old), strings.ToLower(c.New), strings.ToLower(c.Path)
		matched := false
		for _, t := range terms {
			if strings.Contains(lowOld, t) || strings.Contains(lowNew, t) || strings.Contains(lowPath, t) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		rawLines, _ := changeChangedOnly(c)
		if len(rawLines) == 0 {
			continue
		}
		bestIdx, bestHits, bestAdd := 0, 0, false
		for i, l := range rawLines {
			lowLine := strings.ToLower(l)
			hits := 0
			for _, t := range terms {
				if strings.Contains(lowLine, t) {
					hits++
				}
			}
			isAdd := strings.HasPrefix(l, "+ ")
			if hits > bestHits || (hits == bestHits && hits > 0 && isAdd && !bestAdd) {
				bestIdx, bestHits, bestAdd = i, hits, isAdd
			}
		}
		start := 0
		if bestHits > 0 && bestIdx > 0 && len(rawLines) > 4 {
			start = max(0, min(bestIdx-1, len(rawLines)-4))
		}
		out := []string{"  code it changed (" + recallListingLine(c.Path) + "):\n"}
		used := len(out[0])
		for _, l := range rawLines[start:] {
			if len(out) >= 5 {
				break
			}
			clean := "    " + recallListingLine(l) + "\n"
			if used+len(clean) > recallCodeDiffMax && len(out) > 1 {
				break
			}
			out = append(out, clean)
			used += len(clean)
		}
		if len(out) > 1 {
			return out
		}
	}
	return nil
}

// fixEditDiffLines extracts up to 4 compact diff lines (- old / + new) for the
// file edit that followed an error in p.Key.
func fixEditDiffLines(dir string, p index.FixPair) []string {
	if dir == "" || p.Edit == "" || p.Key == "" {
		return nil
	}
	harness, id, ok := strings.Cut(p.Key, ":")
	if !ok || harness == "" || id == "" {
		return nil
	}
	s, found, err := index.FindByIdentity(dir, harness, id)
	if err != nil || !found {
		return nil
	}
	cs := sessionChanges(s)
	wantBase := filepath.Base(p.Edit)
	for _, c := range slices.Backward(cs) {
		if c.Path != p.Edit && filepath.Base(c.Path) != wantBase {
			continue
		}
		var rawLines []string
		switch {
		case c.Old != "" && c.New != "":
			for l := range strings.SplitSeq(changeDiff(c.Old, c.New), "\n") {
				if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "+ ") {
					rawLines = append(rawLines, l)
				}
			}
		case c.Old != "":
			for l := range strings.SplitSeq(strings.TrimSpace(c.Old), "\n") {
				rawLines = append(rawLines, "- "+l)
			}
		case c.New != "":
			for l := range strings.SplitSeq(strings.TrimSpace(c.New), "\n") {
				rawLines = append(rawLines, "+ "+l)
			}
		}
		if len(rawLines) == 0 {
			continue
		}
		var out []string
		used := 0
		for _, l := range rawLines {
			if len(out) >= 4 {
				break
			}
			clean := recallListingLine(l)
			if clean == "" {
				continue
			}
			if used+len(clean) > recallCodeDiffMax && len(out) > 0 {
				break
			}
			out = append(out, clean)
			used += len(clean)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

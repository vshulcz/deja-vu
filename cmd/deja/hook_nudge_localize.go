package main

import (
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type repoFileEntry struct {
	rel       string
	stem      string
	pkg       string
	stemWords map[string]bool
	declExact map[string]bool
	declWords map[string]bool
	bodyWords map[string]int
}

type projectNamesCacheEntry struct {
	names []string
	at    time.Time
}

var projectNamesCache sync.Map

func cachedProjectNameCandidates(cwd string) []string {
	if cwd == "" {
		return nil
	}
	if v, ok := projectNamesCache.Load(cwd); ok {
		if cached, ok := v.(projectNamesCacheEntry); ok && time.Since(cached.at) < 2*time.Second {
			return cached.names
		}
	}
	names := digest.ProjectNameCandidates(cwd)
	projectNamesCache.Store(cwd, projectNamesCacheEntry{names: names, at: time.Now()})
	return names
}

type cachedSessionRelFiles struct {
	id      string
	project string
	rels    []string
}

type fileSessionDFCacheEntry struct {
	at        time.Time
	metaCount int
	projKey   string
	rows      []cachedSessionRelFiles
}

var fileSessionDFCache sync.Map

type symbolDeclLoc struct {
	rel  string
	line int
	sig  string
}

type repoSymbolMap struct {
	at         time.Time
	files      []repoFileEntry
	byStem     map[string][]string
	byDir      map[string]int
	stemDF     map[string]int
	declDF     map[string]int
	structDF   map[string]int
	bodyDF     map[string]int
	firstDecl  map[string]symbolDeclLoc
	dirMtimes  map[string]int64
	fileMtimes map[string]int64
	fileSizes  map[string]int64
	numFiles   int
	capped     bool
}

var (
	repoSymbolMapCache                   sync.Map
	maxRepoSymbolFileBytes               int64 = 256 << 10
	repoSymbolScanMaxFiles                     = 2000
	repoSymbolScanMaxBytes               int64 = 16 << 20
	repoSymbolScanBudget                       = 2 * time.Second
	repoSymbolScanMinFilesBeforeDeadline       = 256
	repoSymbolScanHook                   func(string)
)

func repoSymbolMapFresh(cached repoSymbolMap) bool {
	if cached.at.IsZero() || time.Since(cached.at) >= 5*time.Second || len(cached.dirMtimes) == 0 {
		return false
	}
	for p, mt := range cached.dirMtimes {
		fi, err := os.Stat(p)
		if err != nil || fi.ModTime().UnixNano() != mt {
			return false
		}
	}
	for p, mt := range cached.fileMtimes {
		fi, err := os.Stat(p)
		if err != nil || fi.ModTime().UnixNano() != mt {
			return false
		}
		if sz, ok := cached.fileSizes[p]; ok && fi.Size() != sz {
			return false
		}
	}
	return true
}

func loadRepoSymbolMap(cwd string) repoSymbolMap {
	if cwd == "" || policy.SourceHintsOff() {
		return repoSymbolMap{}
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" {
		return repoSymbolMap{}
	}
	if v, ok := repoSymbolMapCache.Load(scanRoot); ok {
		if cached, ok := v.(repoSymbolMap); ok && repoSymbolMapFresh(cached) {
			return cached
		}
	}
	fi, err := os.Stat(scanRoot)
	if err != nil || !fi.IsDir() {
		return repoSymbolMap{}
	}
	idx := repoSymbolMap{
		byStem:     map[string][]string{},
		byDir:      map[string]int{},
		stemDF:     map[string]int{},
		declDF:     map[string]int{},
		structDF:   map[string]int{},
		bodyDF:     map[string]int{},
		firstDecl:  map[string]symbolDeclLoc{},
		dirMtimes:  map[string]int64{},
		fileMtimes: map[string]int64{},
		fileSizes:  map[string]int64{},
	}
	scanStart := time.Now()
	var bytesRead int64
	_ = filepath.WalkDir(scanRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // unreadable paths skipped during best-effort walk
		}
		if d.IsDir() {
			name := d.Name()
			if p != scanRoot && (isSkippedScanDir(name) || name == "testdata") {
				return filepath.SkipDir
			}
			if info, infoErr := d.Info(); infoErr == nil {
				idx.dirMtimes[p] = info.ModTime().UnixNano()
			}
			return nil
		}
		if len(idx.files) >= repoSymbolScanMaxFiles {
			idx.capped = true
			return filepath.SkipAll
		}
		if repoSymbolScanMaxBytes > 0 && bytesRead >= repoSymbolScanMaxBytes {
			idx.capped = true
			return filepath.SkipAll
		}
		if repoSymbolScanBudget > 0 && len(idx.files) >= repoSymbolScanMinFilesBeforeDeadline && time.Since(scanStart) >= repoSymbolScanBudget {
			idx.capped = true
			return filepath.SkipAll
		}
		if !indexWalkedRepoFileBounded(cwd, scanRoot, p, d, &idx, &bytesRead) {
			return filepath.SkipAll
		}
		return nil
	})
	idx.numFiles = len(idx.files)
	if idx.numFiles > 64 {
		idx.fileMtimes = nil
		idx.fileSizes = nil
	}
	idx.at = time.Now()
	repoSymbolMapCache.Store(scanRoot, idx)
	return idx
}

func ensureRepoFileByRel(cwd string, qWords map[string]bool, symIdx *repoSymbolMap, byRel *map[string]repoFileEntry) {
	if policy.SourceHintsOff() || len(qWords) == 0 || *byRel != nil || symIdx.numFiles != 0 {
		return
	}
	*symIdx = loadRepoSymbolMap(cwd)
	*byRel = buildRepoFileByRel(*symIdx)
}

func indexWalkedRepoFileBounded(cwd, scanRoot, p string, d fs.DirEntry, idx *repoSymbolMap, bytesRead *int64) bool {
	if !isLocalizableSourcePath(p, d.Name()) {
		return true
	}
	rel, err := filepath.Rel(cwd, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		r2, err2 := filepath.Rel(scanRoot, p)
		if err2 != nil || strings.HasPrefix(r2, "..") {
			return true
		}
		rel = r2
	}
	info, infoErr := d.Info()
	if len(idx.files) < 64 && infoErr == nil {
		idx.fileMtimes[p] = info.ModTime().UnixNano()
		idx.fileSizes[p] = info.Size()
	}
	maxFile := maxRepoSymbolFileBytes
	if infoErr == nil && info.Mode().IsRegular() && info.Size() >= 0 && (maxFile <= 0 || info.Size() <= maxFile) {
		if repoSymbolScanMaxBytes > 0 && bytesRead != nil && *bytesRead+info.Size() > repoSymbolScanMaxBytes {
			idx.capped = true
			return false
		}
	}
	rel = filepath.ToSlash(rel)
	ext := filepath.Ext(d.Name())
	stem := strings.ToLower(strings.TrimSuffix(d.Name(), ext))
	pkg := strings.ToLower(filepath.Base(filepath.Dir(rel)))
	if pkg == "." {
		pkg = ""
	}
	entry := repoFileEntry{
		rel:       rel,
		stem:      stem,
		pkg:       pkg,
		stemWords: map[string]bool{},
		declExact: map[string]bool{},
		declWords: map[string]bool{},
		bodyWords: map[string]int{},
	}
	seenStruct := map[string]bool{}
	seenBody := map[string]bool{}
	for _, w := range splitIdentifierWords(stem) {
		addIndexedStructWord(w, entry.stemWords, seenStruct)
	}
	addIndexedStructWord(pkg, nil, seenStruct)
	if infoErr == nil && info.Mode().IsRegular() && info.Size() >= 0 && (maxFile <= 0 || info.Size() <= maxFile) {
		if repoSymbolScanHook != nil {
			repoSymbolScanHook(p)
		}
		if content, n, ok := readBoundedRepoSource(p, maxFile); ok {
			if bytesRead != nil {
				*bytesRead += n
			}
			indexRepoFileDecls(rel, content, &entry, seenStruct, idx.firstDecl)
			indexFileBodyWords(content, entry.bodyWords, seenBody)
		}
	}
	idx.files = append(idx.files, entry)
	idx.byStem[stem] = append(idx.byStem[stem], rel)
	if dirRel := filepath.ToSlash(filepath.Dir(rel)); dirRel != "." {
		idx.byDir[dirRel]++
	}
	idx.stemDF[stem]++
	for dName := range entry.declExact {
		idx.declDF[dName]++
	}
	for w := range seenStruct {
		idx.structDF[w]++
	}
	for w := range seenBody {
		idx.bodyDF[w]++
	}
	return true
}

func readBoundedRepoSource(p string, maxFile int64) (string, int64, bool) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, false
	}
	defer f.Close()
	var r io.Reader = f
	if maxFile > 0 {
		r = io.LimitReader(f, maxFile+1)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", 0, false
	}
	n := int64(len(raw))
	if maxFile > 0 && n > maxFile {
		return "", n, false
	}
	return string(raw), n, true
}

func addIndexedStructWord(w string, target, seenStruct map[string]bool) {
	if len(w) < 3 || localizeStopWords[w] || commonThreeLetterStopWords[w] {
		return
	}
	if target != nil {
		target[w] = true
	}
	seenStruct[w] = true
	if r := asciiMorphRoot(w); r != w && len(r) >= 4 && !localizeStopWords[r] {
		if target != nil {
			target[r] = true
		}
		seenStruct[r] = true
	}
}

func indexRepoFileDecls(rel, content string, entry *repoFileEntry, seenStruct map[string]bool, firstDecl map[string]symbolDeclLoc) {
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		collapsed, endIdx := collapseDeclarationWindow(lines, i)
		if id, sig := parseDeclarationLine(collapsed); id != "" && sig != "" {
			lowID := strings.ToLower(id)
			entry.declExact[lowID] = true
			seenStruct[lowID] = true
			if prev, exists := firstDecl[lowID]; !exists || len(rel) < len(prev.rel) {
				firstDecl[lowID] = symbolDeclLoc{
					rel:  rel,
					line: i + 1,
					sig:  clipDeclSignature(sig),
				}
			}
			for _, w := range splitIdentifierWords(id) {
				addIndexedStructWord(w, entry.declWords, seenStruct)
			}
		}
		if endIdx > i {
			i = endIdx
		}
	}
}

var commonFourLetterBodyStop = map[string]bool{
	"func": true, "type": true, "true": true, "null": true, "none": true,
	"self": true, "this": true, "else": true, "case": true, "goto": true,
	"byte": true, "uint": true, "bool": true, "char": true, "void": true,
	"long": true, "enum": true, "impl": true, "move": true, "loop": true,
	"main": true, "init": true, "test": true, "mock": true, "stub": true,
	"todo": true, "errs": true, "args": true, "opts": true, "name": true,
	"path": true, "data": true, "item": true, "list": true, "text": true,
	"line": true, "user": true, "call": true, "want": true, "have": true,
	"chan": true, "iota": true, "into": true, "from": true, "with": true,
	"when": true, "then": true, "that": true, "what": true, "only": true,
	"each": true, "more": true, "some": true, "used": true, "note": true,
	"done": true, "prev": true, "next": true, "last": true,
}

func indexFileBodyWords(content string, bodyWords map[string]int, seenBody map[string]bool) {
	addBodyToken := func(w string) {
		if localizeStopWords[w] || commonFourLetterBodyStop[w] || bodyWords[w] >= 8 {
			return
		}
		bodyWords[w]++
		seenBody[w] = true
	}
	var cur []rune
	flush := func() {
		if len(cur) < 4 || len(cur) > 48 {
			cur = cur[:0]
			return
		}
		tok := string(cur)
		cur = cur[:0]
		low := strings.ToLower(tok)
		addBodyToken(low)
		if r := asciiMorphRoot(low); r != low && len(r) >= 5 {
			addBodyToken(r)
		}
		if hasMixedCaseOrUnderscore(tok) {
			for _, sub := range splitIdentifierWords(tok) {
				if len(sub) >= 5 {
					addBodyToken(sub)
					if r := asciiMorphRoot(sub); r != sub && len(r) >= 5 {
						addBodyToken(r)
					}
				}
			}
		}
	}
	for _, r := range content {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
}

func symbolBriefAlreadyCoversTarget(sbLine, locLine string) bool {
	rest, ok := strings.CutPrefix(locLine, "likely target: ")
	if !ok {
		return false
	}
	head, _, _ := strings.Cut(rest, " — ")
	firstTarget, _, _ := strings.Cut(head, ", ")
	firstTarget = strings.TrimSpace(firstTarget)
	return firstTarget != "" && strings.Contains(sbLine, firstTarget)
}

func isLocalizableSourcePath(path, base string) bool {
	low := strings.ToLower(base)
	if strings.HasPrefix(low, ".") || isTestFilePath(path) {
		return false
	}
	if strings.HasSuffix(low, ".pb.go") || strings.HasSuffix(low, ".gen.go") || strings.HasSuffix(low, ".min.js") || strings.HasSuffix(low, ".d.ts") {
		return false
	}
	switch strings.ToLower(filepath.Ext(low)) {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs":
		return true
	}
	return false
}

func splitIdentifierWords(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		if r == '_' || r == '-' || r == '.' || r == '/' {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prevLower := unicode.IsLower(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if prevLower || (unicode.IsUpper(runes[i-1]) && nextLower) {
				flush()
			}
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

var localizeStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "into": true,
	"this": true, "that": true, "what": true, "when": true, "where": true, "which": true,
	"have": true, "has": true, "had": true, "been": true, "were": true, "will": true,
	"would": true, "should": true, "could": true, "about": true, "after": true, "before": true,
	"under": true, "over": true, "between": true, "without": true, "also": true, "only": true,
	"make": true, "made": true, "keep": true, "take": true, "give": true, "look": true,
	"looks": true, "good": true, "thanks": true, "thank": true, "please": true, "need": true,
	"needs": true, "want": true, "check": true, "checks": true, "work": true, "works": true,
	"test": true, "tests": true, "testing": true, "file": true, "files": true, "code": true,
	"line": true, "lines": true, "string": true, "strings": true, "error": true, "errors": true,
	"data": true, "type": true, "types": true, "func": true, "function": true, "method": true,
	"value": true, "values": true, "time": true, "main": true, "init": true, "util": true,
	"utils": true, "helper": true, "helpers": true, "common": true, "base": true, "core": true,
	"read": true, "write": true, "open": true, "close": true, "start": true, "stop": true,
	"build": true, "run": true, "runs": true, "pass": true, "fail": true, "fix": true, "bug": true,
	"commit": true, "push": true, "pull": true, "merge": true, "branch": true, "rebase": true,
	"status": true, "diff": true, "review": true, "issue": true, "issues": true, "release": true,
	"change": true, "changes": true, "update": true, "updated": true, "using": true, "used": true,
	"first": true, "last": true, "next": true, "same": true, "some": true, "more": true, "most": true,
	"each": true, "every": true, "other": true, "there": true, "their": true, "these": true, "those": true,
}

var commonThreeLetterStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true, "not": true, "you": true, "all": true,
	"any": true, "can": true, "had": true, "her": true, "was": true, "one": true, "our": true, "out": true,
	"day": true, "get": true, "has": true, "him": true, "his": true, "how": true, "its": true, "may": true,
	"new": true, "now": true, "old": true, "see": true, "two": true, "way": true, "who": true, "did": true,
	"let": true, "put": true, "say": true, "she": true, "too": true, "use": true, "add": true, "fix": true,
	"run": true, "set": true, "try": true, "why": true, "yes": true, "yet": true, "per": true, "via": true,
	"off": true, "own": true, "end": true, "top": true, "big": true, "bad": true, "far": true, "few": true,
	"lot": true, "bit": true, "got": true, "non": true, "pre": true, "sub": true, "nor": true, "etc": true,
}

// asciiMorphRoot folds common English inflectional suffixes on ASCII tokens
// so prompt inflections match repository identifiers and session titles.
func asciiMorphRoot(w string) string {
	if len(w) < 5 {
		return w
	}
	for _, r := range w {
		if r > 127 {
			return w
		}
	}
	collapseDoubled := func(s string) string {
		if len(s) >= 4 && s[len(s)-1] == s[len(s)-2] {
			c := s[len(s)-1]
			if c != 's' && c != 'l' && c != 'f' {
				return s[:len(s)-1]
			}
		}
		return s
	}
	var stem string
	switch {
	case strings.HasSuffix(w, "ies") && len(w) >= 6:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "ied") && len(w) >= 6:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "ing") && len(w) >= 7:
		stem = collapseDoubled(w[:len(w)-3])
	case strings.HasSuffix(w, "ed") && len(w) >= 6:
		stem = collapseDoubled(w[:len(w)-2])
	case strings.HasSuffix(w, "es") && len(w) >= 5:
		base := w[:len(w)-2]
		if strings.HasSuffix(base, "ch") || strings.HasSuffix(base, "sh") || strings.HasSuffix(base, "s") || strings.HasSuffix(base, "x") || strings.HasSuffix(base, "z") {
			stem = base
		} else {
			stem = w[:len(w)-1]
		}
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is") && len(w) >= 5:
		stem = w[:len(w)-1]
	default:
		stem = w
	}
	if strings.HasSuffix(stem, "e") && len(stem) >= 5 && !strings.HasSuffix(stem, "ee") && !strings.HasSuffix(stem, "le") {
		return stem[:len(stem)-1]
	}
	return stem
}

func extractPromptASCIIWords(asked string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) >= 3 {
			words = append(words, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	for _, r := range asked {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

func extractPromptCrossScriptASCIIWords(s string) []string {
	var nonASCII []string
	var cur []rune
	flush := func() {
		if len(cur) >= 3 {
			nonASCII = append(nonASCII, string(cur))
		}
		cur = cur[:0]
	}
	for _, r := range s {
		if r > 127 && unicode.IsLetter(r) {
			cur = append(cur, unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	if len(nonASCII) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	out = appendCrossScriptASCII(out, seen, index.CrossScriptTerms(nonASCII), 8)
	if len(out) < 4 {
		if stems := nonASCIIStemVariants(nonASCII); len(stems) > 0 {
			out = appendCrossScriptASCII(out, seen, index.CrossScriptTerms(stems), 8)
		}
	}
	return out
}

func appendCrossScriptASCII(out []string, seen map[string]bool, cands []string, limit int) []string {
	for _, cand := range cands {
		if limit > 0 && len(out) >= limit {
			break
		}
		low := strings.ToLower(cand)
		if len(low) < 4 || seen[low] || !isASCIIAlnumWord(low) {
			continue
		}
		seen[low] = true
		out = append(out, low)
	}
	return out
}

func isASCIIAlnumWord(s string) bool {
	for i := range len(s) {
		b := s[i]
		if (b < 'a' || b > 'z') && (b < '0' || b > '9') {
			return false
		}
	}
	return s != ""
}

func nonASCIIStemVariants(words []string) []string {
	var stems []string
	seen := map[string]bool{}
	for _, w := range words {
		runes := []rune(w)
		n := len(runes)
		if n < 6 {
			continue
		}
		for _, cut := range []int{2, 3} {
			if cut == 3 && n < 9 {
				continue
			}
			stem := string(runes[:n-cut])
			if !seen[stem] {
				seen[stem] = true
				stems = append(stems, stem)
			}
		}
	}
	return stems
}

// promptTaskTargetsLine maps natural-language module phrases, rare repository
// identifiers, and historical prompt-to-edit correlations onto live repository
// source files and their verified test recipes at Turn 0.
func promptTaskTargetsLine(dir, cwd, asked string, seen, skip map[string]bool) (string, []string) {
	if cwd == "" || policy.SourceHintsOff() || !policy.Load().Allows(policy.ActivationAuto, "") {
		return "", nil
	}
	asked = strings.TrimSpace(digest.StripHarnessBlocks(asked))
	if asked == "" {
		return "", nil
	}
	focused, codeSpans, quotedPhrases := focusTaskPrompt(asked)
	words := append(extractPromptASCIIWords(focused), extractPromptCrossScriptASCIIWords(focused)...)
	if len(words) == 0 && len(codeSpans) == 0 && len(quotedPhrases) == 0 {
		return "", nil
	}
	idx := loadRepoSymbolMap(cwd)
	if idx.numFiles == 0 {
		return "", nil
	}
	explicitDirFiles := matchExplicitSmallRepoDirs(focused, words, &idx)
	phraseFiles := matchAdjacentStemPhrases(&idx, words)
	matchedFiles := appendUniqueRel(explicitDirFiles, phraseFiles...)

	// 2. Score files by separate structural (stem/declaration) DF, rare body DF,
	// literal diagnostic strings, NPMI co-occurrence neighbors, confirmed FixPairs,
	// and historical Title/Settled->Touched consensus.
	if len(matchedFiles) == 0 {
		matchedFiles = scoreTaskTargetCandidates(dir, cwd, asked, focused, codeSpans, quotedPhrases, &idx, skip)
	}

	if len(matchedFiles) == 0 {
		return "", nil
	}
	if len(matchedFiles) > 3 {
		matchedFiles = matchedFiles[:3]
	}
	line := formatLocalizedTargetsLine(dir, cwd, "likely target: ", matchedFiles, skip)
	if line == "" {
		return "", nil
	}
	pTok := "prompt:loc:" + shortHash(line)
	toks := []string{pTok}
	var tTok string
	if fact := dedupeFact(line); fact != line {
		tTok = "tool:" + shortHash(fact)
		toks = append(toks, tTok)
	}
	if seen != nil && (seen[pTok] || (tTok != "" && seen[tTok])) {
		return "", nil
	}
	return line, toks
}

func scoreExplicitDirFile(f repoFileEntry, wSet map[string]bool) int {
	p := 0
	switch f.stem {
	case "index", "main", "mod", "init":
		p += 4
	case "lib", "client", "plugin", "handler":
		p += 3
	}
	if wSet[f.stem] {
		p += 5
	}
	for sw := range f.stemWords {
		if wSet[sw] {
			p += 2
		}
	}
	return p
}

func matchExplicitSmallRepoDirs(focused string, words []string, idx *repoSymbolMap) []string {
	scopes := extractExplicitRepoDirScopes(focused, idx)
	if len(scopes) == 0 {
		return nil
	}
	var dirs []string
	for d, n := range scopes {
		if n >= 1 && n <= 3 {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	lowFocused := strings.ToLower(focused)
	slices.SortFunc(dirs, func(a, b string) int {
		ia, ib := strings.Index(lowFocused, a), strings.Index(lowFocused, b)
		if ia != ib {
			return ia - ib
		}
		return strings.Compare(a, b)
	})
	wSet := make(map[string]bool, len(words))
	for _, w := range words {
		if len(w) >= 3 && !localizeStopWords[w] {
			wSet[w] = true
		}
	}
	type dirCand struct {
		rel  string
		prio int
	}
	var out []string
	for _, d := range dirs {
		var cands []dirCand
		for _, f := range idx.files {
			if filepath.ToSlash(filepath.Dir(f.rel)) != d || len(f.declExact) > 40 {
				continue
			}
			cands = append(cands, dirCand{rel: f.rel, prio: scoreExplicitDirFile(f, wSet)})
		}
		slices.SortFunc(cands, func(a, b dirCand) int {
			if a.prio != b.prio {
				return b.prio - a.prio
			}
			return strings.Compare(a.rel, b.rel)
		})
		for i := range min(2, len(cands)) {
			out = appendUniqueRel(out, cands[i].rel)
		}
	}
	if len(out) > 2 {
		out = out[:2]
	}
	return out
}

func isTechnicalStemStopWord(w string) bool {
	switch w {
	case "hook", "bench", "tool", "after", "before", "file", "files",
		"line", "lines", "time", "read", "write", "open", "close",
		"start", "stop", "build", "status", "diff", "review", "release",
		"init", "main", "core", "base":
		return true
	}
	return false
}

func lookupStemVariants(idx *repoSymbolMap, stem string, minLen int) []string {
	cands := []string{stem, stem + "s", strings.TrimSuffix(stem, "s")}
	if strings.HasSuffix(stem, "y") && len(stem) >= 6 {
		cands = append(cands, strings.TrimSuffix(stem, "y")+"ies")
	}
	if strings.HasSuffix(stem, "ies") && len(stem) >= 8 {
		cands = append(cands, strings.TrimSuffix(stem, "ies")+"y")
	}
	var out []string
	for _, cand := range cands {
		if cand == "" {
			continue
		}
		files := idx.byStem[cand]
		if len(files) == 0 || len(files) > 2 {
			continue
		}
		if len(cand) >= minLen || (len(cand) >= 7 && len(files) == 1) {
			out = appendUniqueRel(out, files...)
			if len(out) >= 3 {
				return out[:3]
			}
		}
	}
	return out
}

func lookupStemSuffixVariants(idx *repoSymbolMap, suffix string) []string {
	if len(suffix) < 10 {
		return nil
	}
	sufA := "_" + suffix
	sufB := "_" + suffix + "s"
	sufC := "_" + strings.TrimSuffix(suffix, "s")
	var hits []string
	for _, f := range idx.files {
		if strings.HasSuffix(f.stem, sufA) || strings.HasSuffix(f.stem, sufB) || (len(sufC) >= 11 && strings.HasSuffix(f.stem, sufC)) {
			hits = append(hits, f.rel)
			if len(hits) > 2 {
				return nil
			}
		}
	}
	return hits
}

func matchAdjacentStemPhrases(idx *repoSymbolMap, words []string) []string {
	var matchedFiles []string
	for i := range words {
		if i+2 < len(words) {
			matchedFiles = appendUniqueRel(matchedFiles, matchTrigramRepoFiles(idx, words[i], words[i+1], words[i+2])...)
			w0, w2 := words[i], words[i+2]
			if !localizeStopWords[w0] && !localizeStopWords[w2] && !workflowMetaWords[w0] && !workflowMetaWords[w2] {
				b2 := w0 + "_" + w2
				if len(b2) >= 9 {
					if files := lookupStemVariants(idx, b2, 9); len(files) > 0 && len(files) <= 2 {
						matchedFiles = appendUniqueRel(matchedFiles, files...)
					}
				}
			}
		}
		if i+1 < len(words) {
			matchedFiles = appendUniqueRel(matchedFiles, matchBigramRepoFiles(idx, words[i], words[i+1])...)
		}
	}
	return matchedFiles
}

func matchTrigramRepoFiles(idx *repoSymbolMap, w0, w1, w2 string) []string {
	stopCount := 0
	for _, w := range []string{w0, w1, w2} {
		if localizeStopWords[w] {
			stopCount++
			if !isTechnicalStemStopWord(w) {
				return nil
			}
		} else if workflowMetaWords[w] {
			return nil
		}
	}
	if stopCount > 1 {
		return nil
	}
	s3 := w0 + "_" + w1 + "_" + w2
	if files := lookupStemVariants(idx, s3, 10); len(files) > 0 {
		return files
	}
	mr3 := asciiMorphRoot(w0) + "_" + asciiMorphRoot(w1) + "_" + asciiMorphRoot(w2)
	if mr3 != s3 {
		if files := lookupStemVariants(idx, mr3, 10); len(files) > 0 {
			return files
		}
	}
	if stopCount == 0 {
		return lookupStemSuffixVariants(idx, s3)
	}
	return nil
}

func matchBigramRepoFiles(idx *repoSymbolMap, w0, w1 string) []string {
	stop0, stop1 := localizeStopWords[w0], localizeStopWords[w1]
	if stop0 || stop1 {
		if stop0 == stop1 {
			return nil
		}
		nonStop, stopW := w0, w1
		if stop0 {
			nonStop, stopW = w1, w0
		}
		if len(nonStop) >= 4 && !workflowMetaWords[nonStop] && isTechnicalStemStopWord(stopW) {
			return appendUniqueRel(nil, lookupStemVariants(idx, w0+"_"+w1, 8)...)
		}
		return nil
	}
	return matchNonStopBigramFiles(idx, w0, w1)
}

func matchNonStopBigramFiles(idx *repoSymbolMap, w0, w1 string) []string {
	s2 := w0 + "_" + w1
	var matchedFiles []string
	if files := lookupStemVariants(idx, s2, 8); len(files) > 0 {
		matchedFiles = appendUniqueRel(matchedFiles, files...)
	} else {
		if mr2 := asciiMorphRoot(w0) + "_" + asciiMorphRoot(w1); mr2 != s2 {
			if files := lookupStemVariants(idx, mr2, 8); len(files) > 0 {
				matchedFiles = appendUniqueRel(matchedFiles, files...)
			}
		}
		if len(matchedFiles) == 0 && !workflowMetaWords[w0] && !workflowMetaWords[w1] {
			matchedFiles = appendUniqueRel(matchedFiles, lookupStemSuffixVariants(idx, s2)...)
		}
	}
	if len(matchedFiles) == 0 && !workflowMetaWords[w0] && !workflowMetaWords[w1] {
		matchedFiles = appendUniqueRel(matchedFiles, matchReverseBigramFiles(idx, w0, w1)...)
	}
	comp := w0 + w1
	if len(comp) >= 10 && idx.declDF[comp] > 0 && idx.declDF[comp] <= 2 {
		for _, f := range idx.files {
			if f.declExact[comp] {
				matchedFiles = appendUniqueRel(matchedFiles, f.rel)
			}
		}
	}
	return matchedFiles
}

func matchReverseBigramFiles(idx *repoSymbolMap, w0, w1 string) []string {
	rev := w1 + "_" + w0
	if len(rev) < 8 {
		return nil
	}
	if files := lookupStemVariants(idx, rev, 8); len(files) > 0 && len(files) <= 2 {
		return files
	}
	if mrRev := asciiMorphRoot(w1) + "_" + asciiMorphRoot(w0); mrRev != rev {
		if files := lookupStemVariants(idx, mrRev, 8); len(files) > 0 && len(files) <= 2 {
			return files
		}
	}
	return nil
}

type taskScoreParams struct {
	idx               *repoSymbolMap
	qWords            map[string]bool
	boostWords        map[string]bool
	bridgedOnly       map[string]bool
	dirScopes         map[string]int
	diagBoost         map[string]float64
	memBoost          map[string]float64
	fixBoost          map[string]float64
	fixHits           map[string]int
	histBoost         map[string]float64
	histSessions      map[string]int
	bm25Boost         map[string]float64
	bm25Sessions      map[string]int
	cooccurVotes      map[string]int
	effectiveN        int
	structRareCap     int
	structVeryRareCap int
	bodyRareCap       int
}

func collectCooccurNeighborVotes(dir, asked string, idx *repoSymbolMap, qWords, projSelf map[string]bool, structVeryRareCap int) map[string]int {
	votes := map[string]int{}
	if dir == "" || !planIndexReady(dir) {
		return votes
	}
	for _, qt := range extractHistoricalQueryTerms(asked, projSelf) {
		if idx.structDF[qt] > 0 || idx.bodyDF[qt] > 0 {
			continue
		}
		nbs := index.CooccurNeighbors(dir, qt)
		if len(nbs) > 5 {
			nbs = nbs[:5]
		}
		seenNB := map[string]bool{}
		for _, nb := range nbs {
			nb = strings.Trim(strings.ToLower(nb), "._-/")
			if len(nb) < 4 || seenNB[nb] || qWords[nb] || localizeStopWords[nb] || projSelf[nb] {
				continue
			}
			if idx.structDF[nb] > 0 && idx.structDF[nb] <= structVeryRareCap {
				seenNB[nb] = true
				votes[nb]++
			}
		}
	}
	return votes
}

func collectBridgedOnlyTaskWords(focused string, codeSpans, quotedPhrases []string, qWords map[string]bool) map[string]bool {
	if len(qWords) == 0 {
		return nil
	}
	literal := map[string]bool{}
	mark := func(s string) {
		for _, w := range extractPromptASCIIWords(s) {
			literal[w] = true
			for _, sub := range splitIdentifierWords(w) {
				literal[sub] = true
			}
		}
	}
	for _, span := range codeSpans {
		mark(span)
	}
	for _, qp := range quotedPhrases {
		mark(qp)
	}
	mark(focused)
	var out map[string]bool
	for qw := range qWords {
		if !literal[qw] {
			if out == nil {
				out = map[string]bool{}
			}
			out[qw] = true
		}
	}
	return out
}

func scoreSingleRepoFile(f repoFileEntry, p *taskScoreParams) (float64, bool) {
	var score float64
	rareHits := 0
	anchorHit := false
	idx := p.idx
	var skipStem string
	bridgedStem := len(p.bridgedOnly) > 0 && p.bridgedOnly[f.stem]
	if !bridgedStem && p.qWords[f.stem] && idx.structDF[f.stem] <= p.structVeryRareCap && !workflowMetaWords[f.stem] {
		switch {
		case len(f.stem) >= 7 && idx.stemDF[f.stem] == 1:
			score += 7.5
			rareHits += 2
			anchorHit = true
			skipStem = f.stem
		case len(f.stem) >= 7 && idx.stemDF[f.stem] <= 3:
			score += 5.8
			rareHits += 2
			anchorHit = true
			skipStem = f.stem
		case len(f.stem) >= 5 && idx.stemDF[f.stem] <= 3:
			score += 5.0
			rareHits++
			anchorHit = true
			skipStem = f.stem
		}
	}
	if db := p.diagBoost[f.rel]; db > 0 {
		score += db
		rareHits += 2
		anchorHit = true
	}
	qwScore, qwRareHits, qwAnchor := scoreQueryWordsForFileExcluding(f, p, skipStem)
	score += qwScore
	rareHits += qwRareHits
	if qwAnchor {
		anchorHit = true
	}
	if rareHits >= 1 && len(f.pkg) >= 4 && p.qWords[f.pkg] && idx.structDF[f.pkg] > 0 && idx.structDF[f.pkg] <= max(18, idx.numFiles/12) {
		score += 1.4
	}
	addScore, addRare, dirAnchor, dirMatched := scoreRepoDirScope(f.rel, p.dirScopes, rareHits)
	score += addScore
	rareHits += addRare
	if dirAnchor {
		anchorHit = true
	}
	if mb := p.memBoost[f.rel]; mb > 0 {
		score += mb
		rareHits += 2
		anchorHit = true
	}
	if fb := p.fixBoost[f.rel]; fb > 0 {
		score += fb
		if p.fixHits[f.rel] >= 2 && (rareHits >= 1 || fb >= 6.5 || p.fixHits[f.rel] >= 3) {
			rareHits += 2
			anchorHit = true
		} else if rareHits >= 1 {
			rareHits++
			anchorHit = true
		}
	}
	sessScore, sessRare, sessAnchor := scoreSessionBoostSignals(f.rel, p, rareHits, score, anchorHit)
	score += sessScore
	rareHits += sessRare
	if sessAnchor {
		anchorHit = true
	}
	coScore, coRare, coAnchor := scoreCooccurVotes(f, p, rareHits)
	score += coScore
	rareHits += coRare
	if coAnchor {
		anchorHit = true
	}
	if len(p.dirScopes) > 0 && !dirMatched && !anchorHit {
		score *= 0.78
	}
	return score, rareHits >= 2 && anchorHit && score >= 6.4
}

func scoreSessionBoostSignals(rel string, p *taskScoreParams, rareHits int, score float64, anchorHit bool) (float64, int, bool) {
	baseScore := score
	baseRare := rareHits
	if addScore, addRare, anchor := evalSessionBoostTier(p.histBoost[rel], p.histSessions[rel], 3.8, 4.2, 11.0, rareHits, score); addScore > 0 || addRare > 0 {
		score += addScore
		rareHits += addRare
		if anchor {
			anchorHit = true
		}
	}
	if addScore, addRare, anchor := evalSessionBoostTier(p.bm25Boost[rel], p.bm25Sessions[rel], 4.0, 4.3, 10.5, rareHits, score); addScore > 0 || addRare > 0 {
		score += addScore
		rareHits += addRare
		if anchor {
			anchorHit = true
		}
	}
	if !anchorHit && p.histSessions[rel] >= 1 && p.bm25Sessions[rel] >= 1 {
		combined := p.histBoost[rel] + p.bm25Boost[rel]
		if combined >= 5.2 {
			anchorHit = true
			if rareHits < 2 {
				rareHits = 2
			}
			if score < 6.6 {
				score = 6.6
			} else {
				score += min(1.4, combined*0.18)
			}
		}
	}
	return score - baseScore, rareHits - baseRare, anchorHit
}

func scoreRepoDirScope(rel string, dirScopes map[string]int, rareHits int) (addScore float64, addRare int, anchor, dirMatched bool) {
	if len(dirScopes) == 0 {
		return 0, 0, false, false
	}
	fDir := filepath.ToSlash(filepath.Dir(rel))
	if dirCount, ok := dirScopes[fDir]; ok {
		switch {
		case dirCount <= 8:
			return 4.6, 2, true, true
		case dirCount <= 30:
			return 2.6, 1, rareHits+1 >= 2, true
		default:
			return 1.5, 0, false, true
		}
	}
	for sd := range dirScopes {
		if strings.HasPrefix(fDir, sd+"/") || strings.HasPrefix(sd, fDir+"/") {
			return 0, 0, false, true
		}
	}
	return 0, 0, false, false
}

func scoreCooccurVotes(f repoFileEntry, p *taskScoreParams, rareHits int) (addScore float64, addRare int, anchor bool) {
	for nb, votes := range p.cooccurVotes {
		if !f.stemWords[nb] && !f.declWords[nb] && !f.declExact[nb] {
			continue
		}
		idf := math.Log(1.0 + float64(p.effectiveN)/float64(p.idx.structDF[nb]))
		if votes >= 2 {
			addScore += idf * 1.15
			addRare++
			anchor = true
		} else if rareHits+addRare >= 1 {
			addScore += idf * 0.55
			addRare++
		}
	}
	return addScore, addRare, anchor
}

func evalSessionBoostTier(boost float64, sessions int, multiMin, singleMin, multiCap float64, rareHits int, curScore float64) (addScore float64, addRare int, anchor bool) {
	if boost <= 0 {
		return 0, 0, false
	}
	switch {
	case sessions >= 2 && boost >= multiMin:
		return max(min(boost, multiCap), 6.5), 2, true
	case sessions == 1 && boost >= singleMin:
		return max(min(boost, 8.5), 6.4), 2, true
	case rareHits >= 1 && boost >= 2.2:
		return min(boost, 5.5), 1, true
	case rareHits > 0 || curScore > 0:
		return min(boost*0.45, 2.5), 0, false
	default:
		return 0, 0, false
	}
}

func resolveStructuralWordMatch(f repoFileEntry, idx *repoSymbolMap, qw string) (matchW string, sDF int, inStem, inDecl, inPkg bool) {
	matchW = qw
	sDF = idx.structDF[matchW]
	inStem = f.stemWords[matchW]
	inDecl = f.declWords[matchW] || f.declExact[matchW]
	inPkg = f.pkg == matchW
	if inStem || inDecl || inPkg {
		return matchW, sDF, inStem, inDecl, inPkg
	}
	r := asciiMorphRoot(qw)
	if r == qw || len(r) < 4 || idx.structDF[r] <= 0 {
		return matchW, sDF, inStem, inDecl, inPkg
	}
	if f.stemWords[r] || f.declWords[r] || f.declExact[r] || f.pkg == r {
		return r, idx.structDF[r], f.stemWords[r], f.declWords[r] || f.declExact[r], f.pkg == r
	}
	return matchW, sDF, inStem, inDecl, inPkg
}

func scoreQueryWordsForFileExcluding(f repoFileEntry, p *taskScoreParams, skipStem string) (score float64, rareHits int, anchorHit bool) {
	seenStem := map[string]bool{}
	if skipStem != "" {
		seenStem[skipStem] = true
	}
	breadth := 1.0 + 0.25*math.Log1p(float64(max(0, len(f.declWords)+len(f.bodyWords)/5-40))/40.0)
	type wordHit struct {
		score  float64
		rare   int
		anchor bool
	}
	var hits []wordHit
	bridgedStructHits := 0
	for qw := range p.qWords {
		if skipStem != "" && qw == skipStem {
			continue
		}
		isBridged := len(p.bridgedOnly) > 0 && p.bridgedOnly[qw]
		mult := 1.0
		if isBridged {
			mult = 0.65
		} else if p.boostWords[qw] {
			mult = 1.28
		}
		structCap := p.structRareCap
		anchorCap := p.structVeryRareCap
		bodyCap := p.bodyRareCap
		if isBridged {
			structCap = max(5, p.structRareCap/2)
			anchorCap = max(3, p.structVeryRareCap/2)
			bodyCap = max(3, p.bodyRareCap/2)
		}
		sScore, sRare, sAnchor, handled := scoreStructuralQueryWordMatch(f, p, seenStem, &bridgedStructHits, qw, mult, breadth, structCap, anchorCap, isBridged)
		if handled {
			if sScore > 0 {
				hits = append(hits, wordHit{score: sScore, rare: sRare, anchor: sAnchor})
			}
			continue
		}
		bScore, bRare, bAnchor := scoreBodyQueryWordMatch(f, p, seenStem, qw, mult, bodyCap, isBridged)
		if bScore > 0 {
			hits = append(hits, wordHit{score: bScore / breadth, rare: bRare, anchor: bAnchor})
		}
	}
	slices.SortFunc(hits, func(a, b wordHit) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return 0
	})
	for i, h := range hits {
		if i >= 8 {
			break
		}
		if i < 5 {
			score += h.score
			rareHits += h.rare
			if h.anchor {
				anchorHit = true
			}
		} else {
			score += h.score * 0.22
		}
	}
	return score, rareHits, anchorHit
}

func scoreStructuralQueryWordMatch(
	f repoFileEntry,
	p *taskScoreParams,
	seenStem map[string]bool,
	bridgedStructHits *int,
	qw string,
	mult, breadth float64,
	structCap, anchorCap int,
	isBridged bool,
) (score float64, rareHits int, anchorHit bool, handled bool) {
	idx := p.idx
	matchW, sDF, inStem, inDecl, inPkg := resolveStructuralWordMatch(f, idx, qw)
	if !inStem && !inDecl && !inPkg {
		return 0, 0, false, false
	}
	if seenStem[matchW] {
		return 0, 0, false, true
	}
	if sDF <= 0 || sDF > structCap {
		return 0, 0, false, false
	}
	seenStem[matchW] = true
	idf := math.Log(1.0+float64(p.effectiveN)/float64(sDF)) * mult
	if !isBridged || inStem || (inDecl && sDF <= anchorCap) {
		rareHits++
	}
	if isBridged && (inStem || (inDecl && sDF <= anchorCap)) {
		*bridgedStructHits++
	}
	switch {
	case inStem:
		score += idf * 1.75
		if (!isBridged && sDF <= anchorCap) || *bridgedStructHits >= 2 {
			anchorHit = true
		}
	case !isBridged && f.declExact[matchW] && len(matchW) >= 6 && idx.declDF[matchW] >= 1 && idx.declDF[matchW] <= 3:
		score += idf * 2.2
		rareHits++
		anchorHit = true
	case f.declExact[matchW] && len(matchW) >= 7 && idx.declDF[matchW] <= 2:
		score += idf * 2.0
		if !isBridged || idx.declDF[matchW] == 1 {
			anchorHit = true
		}
	case !isBridged && inPkg && len(matchW) >= 5 && sDF <= anchorCap:
		score += idf * 1.45
		anchorHit = true
	case inDecl:
		score += (idf * 1.25) / breadth
		if (!isBridged && sDF <= anchorCap) || *bridgedStructHits >= 2 {
			anchorHit = true
		}
	default:
		score += idf * 0.75
	}
	return score, rareHits, anchorHit, true
}

func scoreBodyQueryWordMatch(
	f repoFileEntry,
	p *taskScoreParams,
	seenStem map[string]bool,
	qw string,
	mult float64,
	bodyCap int,
	isBridged bool,
) (score float64, rareHits int, anchorHit bool) {
	idx := p.idx
	bDF := idx.bodyDF[qw]
	bodyTF := f.bodyWords[qw]
	bodyOK := bodyTF > 0 && bDF > 0 && ((len(qw) >= 5 && bDF <= bodyCap) || (!isBridged && len(qw) == 4 && bDF <= max(4, bodyCap/2) && !commonFourLetterBodyStop[qw]))
	if !bodyOK {
		if r := asciiMorphRoot(qw); r != qw && len(r) >= 5 && !seenStem[r] && idx.bodyDF[r] > 0 && idx.bodyDF[r] <= bodyCap && f.bodyWords[r] > 0 {
			bDF = idx.bodyDF[r]
			bodyTF = f.bodyWords[r]
			bodyOK = true
		}
	}
	if !bodyOK {
		return 0, 0, false
	}
	idf := math.Log(1.0+float64(p.effectiveN)/float64(bDF)) * mult
	tfBoost := 1.0 + 0.15*math.Min(float64(bodyTF-1), 4)
	score += idf * 0.80 * tfBoost
	if !isBridged && bDF == 1 {
		score += 0.70 * mult
	}
	if !isBridged || bDF <= max(2, bodyCap/2) {
		rareHits++
	}
	if !isBridged && ((bDF <= 2 && bodyTF >= 1) || (bDF <= max(3, idx.numFiles/90) && bodyTF >= 2)) {
		anchorHit = true
	}
	return score, rareHits, anchorHit
}

func explicitFileRefBasenames(asked string) map[string]bool {
	out := map[string]bool{}
	for tok := range strings.FieldsSeq(asked) {
		tok = strings.Trim(tok, "\"'`(),:;!?[]{}<>")
		if idx := strings.IndexByte(tok, ':'); idx > 0 {
			tok = tok[:idx]
		}
		if tok == "" || !looksLikePromptFileRef(tok) {
			continue
		}
		base := strings.ToLower(filepath.Base(tok))
		if base != "" {
			out[base] = true
		}
	}
	return out
}

func extractExplicitRepoDirScopes(asked string, idx *repoSymbolMap) map[string]int {
	if asked == "" || idx == nil || len(idx.byDir) == 0 {
		return nil
	}
	var out map[string]int
	for tok := range strings.FieldsSeq(asked) {
		cand := strings.Trim(tok, "\"'`()[]{}.,:;!?<>")
		if colon := strings.IndexByte(cand, ':'); colon > 0 {
			cand = cand[:colon]
		}
		cand = strings.TrimPrefix(cand, "./")
		cand = strings.TrimSuffix(cand, "/")
		if cand == "" || strings.HasPrefix(cand, "/") || strings.Contains(cand, "..") || !strings.Contains(cand, "/") {
			continue
		}
		if looksLikePromptFileRef(cand) {
			continue
		}
		if n := idx.byDir[cand]; n > 0 {
			if out == nil {
				out = map[string]int{}
			}
			out[cand] = n
		}
	}
	return out
}

func addPromptIdentifierTaskWords(
	asked string,
	idx *repoSymbolMap,
	projSelf map[string]bool,
	structVeryRareCap int,
	qWords, boostWords map[string]bool,
) {
	for _, tok := range identifierTokens(asked) {
		low := strings.ToLower(tok)
		if len(low) < 5 || projSelf[low] || workflowMetaWords[low] || isAllDigitsWord(low) {
			continue
		}
		rareStruct := (idx.declDF[low] >= 1 && idx.declDF[low] <= 3) || (idx.stemDF[low] >= 1 && idx.stemDF[low] <= 3)
		rareCompound := strings.Contains(low, "_") && len(low) >= 10 && idx.bodyDF[low] >= 1 && idx.bodyDF[low] <= 2
		if !rareStruct && !rareCompound {
			continue
		}
		qWords[low] = true
		boostWords[low] = true
		if !strings.Contains(low, "_") {
			continue
		}
		for part := range strings.SplitSeq(low, "_") {
			if len(part) >= 5 && !projSelf[part] && !localizeStopWords[part] && !workflowMetaWords[part] &&
				idx.structDF[part] >= 1 && idx.structDF[part] <= structVeryRareCap {
				qWords[part] = true
			}
		}
	}
}

func scoreTaskTargetCandidates(dir, cwd, asked, focused string, codeSpans, quotedPhrases []string, idx *repoSymbolMap, skip map[string]bool) []string {
	histBoost, histSessions := historicalTitleTouchedBoost(dir, cwd, asked, skip)
	var bm25Boost map[string]float64
	var bm25Sessions map[string]int
	if dir != "" {
		bm25Boost, bm25Sessions = bm25SessionTouchedBoost(dir, cwd, asked, skip)
	}
	fixBoost, fixHits := fixPairTargetBoost(dir, cwd, asked, skip)
	structRareCap := max(10, idx.numFiles/30)
	structVeryRareCap := max(5, idx.numFiles/55)
	bodyRareCap := max(6, idx.numFiles/42)
	projSelf := selfProjectWordSet(cwd)
	qWords, boostWords := selectPrioritizedTaskWords(focused, codeSpans, quotedPhrases, projSelf, idx, structVeryRareCap, 52)
	addPromptIdentifierTaskWords(asked, idx, projSelf, structVeryRareCap, qWords, boostWords)
	namedBases := explicitFileRefBasenames(asked)
	params := taskScoreParams{
		idx:               idx,
		qWords:            qWords,
		boostWords:        boostWords,
		bridgedOnly:       collectBridgedOnlyTaskWords(focused, codeSpans, quotedPhrases, qWords),
		dirScopes:         extractExplicitRepoDirScopes(focused, idx),
		diagBoost:         matchDiagnosticPhraseFiles(cwd, idx, quotedPhrases, projSelf),
		memBoost:          repoMemoryTargetBoost(cwd, asked, projSelf),
		fixBoost:          fixBoost,
		fixHits:           fixHits,
		histBoost:         histBoost,
		histSessions:      histSessions,
		bm25Boost:         bm25Boost,
		bm25Sessions:      bm25Sessions,
		cooccurVotes:      collectCooccurNeighborVotes(dir, asked, idx, qWords, projSelf, structVeryRareCap),
		effectiveN:        max(idx.numFiles, 24),
		structRareCap:     structRareCap,
		structVeryRareCap: structVeryRareCap,
		bodyRareCap:       bodyRareCap,
	}
	type cand struct {
		rel   string
		score float64
	}
	var cands []cand
	for _, f := range idx.files {
		if namedBases[strings.ToLower(filepath.Base(f.rel))] {
			continue
		}
		if score, ok := scoreSingleRepoFile(f, &params); ok {
			cands = append(cands, cand{rel: f.rel, score: score})
		}
	}
	if len(cands) == 0 {
		return nil
	}
	slices.SortFunc(cands, func(a, b cand) int {
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		return strings.Compare(a.rel, b.rel)
	})
	maxSpecificCands := max(12, idx.numFiles/35)
	corroboratedTop := hasTargetCorroboration(cands[0].rel, &params) &&
		cands[0].score >= 7.2 &&
		(len(cands) <= 8 ||
			cands[0].score >= cands[min(8, len(cands)-1)].score*1.15 ||
			(params.histSessions[cands[0].rel] >= 2 && cands[0].score >= 8.0))
	concentrated := len(cands) <= maxSpecificCands ||
		cands[0].score >= cands[maxSpecificCands-1].score*1.28 ||
		(cands[0].score >= 11.0 && len(cands) > 5 && cands[0].score >= cands[5].score*1.35) ||
		corroboratedTop
	if !concentrated {
		return nil
	}
	var matchedFiles []string
	matchedFiles = appendUniqueRel(matchedFiles, cands[0].rel)
	if len(cands) > 1 && ((cands[1].score >= cands[0].score*0.58 && cands[1].score >= 6.8) ||
		(hasTargetCorroboration(cands[1].rel, &params) && cands[1].score >= cands[0].score*0.45 && cands[1].score >= 5.6)) {
		matchedFiles = appendUniqueRel(matchedFiles, cands[1].rel)
	}
	if len(cands) > 2 && ((cands[2].score >= cands[0].score*0.52 && cands[2].score >= 6.8) ||
		(hasTargetCorroboration(cands[2].rel, &params) && cands[2].score >= cands[0].score*0.42 && cands[2].score >= 5.6)) {
		matchedFiles = appendUniqueRel(matchedFiles, cands[2].rel)
	}
	return matchedFiles
}

func hasTargetCorroboration(rel string, p *taskScoreParams) bool {
	return p.fixHits[rel] >= 1 ||
		p.histSessions[rel] >= 1 ||
		p.bm25Sessions[rel] >= 1 ||
		p.memBoost[rel] > 0 ||
		p.diagBoost[rel] > 0
}

type repoRemoteCacheEntry struct {
	words []string
	at    time.Time
}

var repoRemoteWordsCache sync.Map

func cachedRepoRemoteWords(cwd string) []string {
	if cwd == "" {
		return nil
	}
	if v, ok := repoRemoteWordsCache.Load(cwd); ok {
		if cached, ok := v.(repoRemoteCacheEntry); ok && time.Since(cached.at) < 10*time.Second {
			return cached.words
		}
	}
	root := orientMainRepoRoot(cwd)
	if root == "" {
		if g := gitRootOf(filepath.Join(cwd, "x")); g != "" {
			if main := orientMainRepoRoot(g); main != "" {
				root = main
			} else {
				root = g
			}
		}
	}
	seen := map[string]bool{}
	var words []string
	addWord := func(w string) {
		w = strings.ToLower(strings.TrimSpace(w))
		if len(w) < 3 || seen[w] {
			return
		}
		seen[w] = true
		words = append(words, w)
	}
	if root != "" {
		if raw, err := os.ReadFile(filepath.Join(root, ".git", "config")); err == nil && len(raw) <= 128*1024 {
			inRemote := false
			for line := range strings.SplitSeq(string(raw), "\n") {
				tr := strings.TrimSpace(line)
				if strings.HasPrefix(tr, "[") {
					inRemote = strings.HasPrefix(strings.ToLower(tr), "[remote ")
					continue
				}
				if !inRemote {
					continue
				}
				k, v, ok := strings.Cut(tr, "=")
				if !ok || strings.ToLower(strings.TrimSpace(k)) != "url" {
					continue
				}
				u := strings.TrimSuffix(strings.TrimSpace(v), ".git")
				for part := range strings.FieldsFuncSeq(u, func(r rune) bool {
					return r == ':' || r == '/' || r == '@' || r == '.' || r == '-' || r == '_'
				}) {
					addWord(part)
				}
			}
		}
		for dir, depth := filepath.Dir(root), 0; depth < 4 && dir != "" && dir != "." && dir != string(filepath.Separator); dir, depth = filepath.Dir(dir), depth+1 {
			base := strings.ToLower(filepath.Base(dir))
			if len(base) < 4 || strings.HasPrefix(base, "test") || base == "folders" || base == "temp" || base == "code" || base == "repos" {
				continue
			}
			addWord(base)
		}
	}
	repoRemoteWordsCache.Store(cwd, repoRemoteCacheEntry{words: words, at: time.Now()})
	return words
}

func selfProjectWordSet(cwd string) map[string]bool {
	out := map[string]bool{
		"github": true, "gitlab": true, "bitbucket": true,
		"origin": true, "upstream": true, "master": true,
		"users": true, "home": true, "claude": true, "codex": true,
		"gemini": true, "opencode": true, "anthropic": true, "openai": true,
	}
	for _, w := range cachedRepoRemoteWords(cwd) {
		out[w] = true
		for _, tok := range extractPromptASCIIWords(w) {
			if len(tok) >= 3 {
				out[tok] = true
			}
		}
	}
	for _, p := range cachedProjectNameCandidates(cwd) {
		low := strings.ToLower(p)
		out[low] = true
		for _, tok := range extractPromptASCIIWords(low) {
			if len(tok) >= 3 {
				out[tok] = true
			}
		}
		for part := range strings.FieldsFuncSeq(low, func(r rune) bool {
			return r == '/' || r == '.'
		}) {
			if len(part) >= 3 {
				out[part] = true
			}
		}
	}
	return out
}

var conversationalHistStopWords = map[string]bool{
	"давай": true, "дава": true, "дальше": true, "дальш": true, "давайте": true,
	"изучать": true, "изуча": true, "изучи": true, "вопрос": true, "вопро": true, "вопросы": true,
	"низкоуровнево": true, "низкоуровн": true, "улучшать": true, "улучш": true,
	"улучшения": true, "улучшени": true, "инструмент": true, "инструм": true,
	"работы": true, "работ": true, "работа": true, "работать": true, "работает": true, "работают": true,
	"агент": true, "агентов": true, "агенты": true, "агента": true,
	"думать": true, "думай": true, "думаю": true, "подумать": true, "подумай": true, "подумаем": true,
	"механизм": true, "механизмы": true,
	"лечить": true, "помогать": true, "помочь": true, "людей": true, "людям": true,
	"разработчик": true, "разработчикам": true, "сделать": true, "сделай": true, "сделаем": true, "сделано": true,
	"посмотрим": true, "посмотреть": true, "посмотри": true, "продолжай": true, "продолжать": true,
	"проверь": true, "проверить": true, "проверим": true, "правильно": true,
	"покажи": true, "показать": true, "расскажи": true, "подскажи": true, "напиши": true,
	"исправь": true, "почини": true, "понять": true, "понятно": true,
	"нужно": true, "нужен": true, "нужна": true, "нужны": true,
	"можно": true, "может": true, "можем": true, "можешь": true, "сможешь": true, "могу": true,
	"сейчас": true, "теперь": true, "потом": true, "опять": true, "снова": true,
	"вообще": true, "просто": true, "реально": true, "полезно": true, "лучше": true, "хуже": true,
	"неделю": true, "последнюю": true, "какие": true, "почему": true,
	"когда": true, "через": true, "после": true, "перед": true, "сначала": true, "порядку": true,
	"какой": true, "какая": true, "какое": true, "каким": true, "каких": true,
	"которые": true, "который": true, "которая": true, "которое": true,
	"должен": true, "должна": true, "должны": true, "должно": true,
	"будет": true, "будем": true, "будут": true, "хочется": true, "хотим": true,
	"только": true, "очень": true, "более": true, "менее": true,
	"чтобы": true, "потому": true, "зачем": true, "кстати": true, "прямо": true, "вместе": true,
	"этап": true, "этапа": true, "этапе": true, "этапу": true, "этапы": true, "этапов": true,
	"план": true, "плана": true, "плане": true, "плану": true, "планы": true,
	"релиз": true, "релиза": true, "релизе": true, "релизу": true, "релизы": true,
	"задача": true, "задачи": true, "задачу": true, "задач": true, "задаче": true,
	"пункт": true, "пункта": true, "пункты": true, "пунктов": true,
	"ветка": true, "ветки": true, "ветку": true, "ветке": true, "веток": true,
	"коммит": true, "коммита": true, "коммиты": true, "коммитов": true, "коммитить": true,
	"мерж": true, "мержить": true, "смержить": true,
	"репо": true, "репозиторий": true, "репозитория": true, "репозитории": true,
	"проект": true, "проекта": true, "проекте": true, "проекту": true, "проекты": true,
	"сессия": true, "сессии": true, "сессию": true, "сессий": true,
	"бенч": true, "бенчи": true, "бенчах": true, "бенчмарк": true, "бенчмарки": true,
	"метрик": true, "метрика": true, "метрики": true, "метрику": true,
	"цифры": true, "цифр": true, "замеры": true, "замер": true,
	"версия": true, "версии": true, "версию": true,
	"пример": true, "примера": true, "например": true, "контекст": true, "контекста": true,
	"готов": true, "готово": true, "стоит": true, "получить": true, "получилось": true,
	"объясни": true, "опиши": true, "сравни": true, "оцени": true, "добей": true, "доделай": true, "допиши": true,
	"прогони": true, "прогнать": true, "запусти": true, "запустить": true, "разберись": true,
}

var conversationalHistPrefixes = []string{
	"задач", "этап", "ветк", "веток", "комми", "релиз", "репозитор", "бенчм",
	"метрик", "замер", "цифр", "работ", "улучш", "инструм", "вопрос", "подум",
	"продолж", "разбир", "сдела", "делае", "делат", "делал", "хотел", "хочет",
	"нужн", "можн", "котор", "следу", "сначал", "наприм", "сесси",
}

func isConversationalHistStop(t string) bool {
	if conversationalHistStopWords[t] {
		return true
	}
	for _, pref := range conversationalHistPrefixes {
		if strings.HasPrefix(t, pref) {
			return true
		}
	}
	return false
}

func isHexHashLike(t string) bool {
	if len(t) < 6 || len(t) > 64 {
		return false
	}
	hasDigit := false
	for i := range len(t) {
		b := t[i]
		switch {
		case b >= '0' && b <= '9':
			hasDigit = true
		case b >= 'a' && b <= 'f':
		default:
			return false
		}
	}
	return hasDigit || len(t) >= 12
}

func isLiteralASCIITechToken(t string) bool {
	if len(t) == 3 {
		return isThreeCharASCIIToken(t)
	}
	if len(t) < 4 {
		return false
	}
	hasLetter := false
	for i := range len(t) {
		b := t[i]
		switch {
		case b >= 'a' && b <= 'z':
			hasLetter = true
		case (b >= '0' && b <= '9') || b == '_':
		default:
			return false
		}
	}
	return hasLetter && !isHexHashLike(t)
}

func isThreeCharASCIIToken(t string) bool {
	if len(t) != 3 || len([]rune(t)) != 3 {
		return false
	}
	hasLetter := false
	for i := range len(t) {
		b := t[i]
		if b >= 'a' && b <= 'z' {
			hasLetter = true
			continue
		}
		if b < '0' || b > '9' {
			return false
		}
	}
	return hasLetter
}

func extractHistoricalQueryTerms(asked string, projSelf map[string]bool) []string {
	focused, codeSpans, quotedPhrases := focusTaskPrompt(asked)
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.Trim(strings.ToLower(t), "._-/")
		rCount := len([]rune(t))
		if rCount < 3 || (rCount == 3 && !isThreeCharASCIIToken(t)) {
			return
		}
		if localizeStopWords[t] || commonThreeLetterStopWords[t] || workflowMetaWords[t] || isConversationalHistStop(t) || projSelf[t] || isHexHashLike(t) || seen[t] {
			return
		}
		allDigits := true
		for _, r := range t {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	addWords := func(s string) {
		for _, w := range extractPromptASCIIWords(s) {
			add(w)
			for _, sub := range splitIdentifierWords(w) {
				add(sub)
			}
		}
	}
	for _, span := range codeSpans {
		addWords(span)
	}
	for _, qp := range quotedPhrases {
		addWords(qp)
	}
	for _, t := range prompt.Terms(focused) {
		if !projSelf[strings.ToLower(t)] {
			add(t)
		}
	}
	// Prioritize the opening problem statement before trailing instructions.
	for i, line := range strings.Split(focused, "\n") {
		if i >= 3 {
			break
		}
		addWords(line)
	}
	// Extract non-ASCII words and cross-script ASCII bridges before body filler can saturate out[:24].
	var cur []rune
	flushNonASCII := func() {
		if len(cur) >= 4 {
			add(string(cur))
		}
		cur = cur[:0]
	}
	for _, r := range focused {
		if r > 127 && unicode.IsLetter(r) {
			cur = append(cur, unicode.ToLower(r))
		} else {
			flushNonASCII()
		}
	}
	flushNonASCII()
	for _, w := range extractPromptCrossScriptASCIIWords(focused) {
		add(w)
	}
	addWords(focused)
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

var workflowMetaWords = map[string]bool{
	"worktree": true, "worktrees": true, "branched": true, "commits": true,
	"committed": true, "pushed": true, "pushing": true, "merged": true,
	"merging": true, "deliverables": true, "contributor": true,
	"blanket": true, "feat": true, "chore": true, "docs": true,
	"refactor": true, "perf": true, "style": true, "git": true,
	"head": true, "tmp": true, "temp": true, "goal": true,
	"real": true, "near": true, "slot": true, "approved": true,
	"current": true, "follow": true, "hunt": true, "offline": true,
}

func isAllDigitsWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var workflowClausePrefixes = []string{
	"report:", "report pr ", "report branch", "report ",
	"return a ", "return 1", "return:", "return ",
	"commit ", "commits:", "commits ", "checks:", "hard rules:", "constraints:", "constraints ",
	"deliverables:", "deliverables", "output format:", "output:", "output report",
	"verification:", "verification",
	"non-goals:", "success criteria:",
	"run golangci-lint", "gofmt ",
	"do not push", "don't push", "do not post ", "do not merge", "never push",
	"never touch", "never run deja", "never go clean",
	"stop if free disk", "keep disk", "clean up", "when done",
	"save the final patch", "stage named paths", "use a temp home", "live dirs",
	"privacy:", "writing in issue",
	"work in your own worktree", "work from current origin/", "branch from origin/",
	"create branch ", "blanket ok to ", "house rule:", "keep disk low",
	"this is a read-only review", "post nothing to github",
}

func isWorkflowMetaSpan(span string) bool {
	low := strings.ToLower(strings.TrimSpace(span))
	for _, pfx := range []string{
		"gh issue ", "gh pr ", "gh run ", "gh api ",
		"git fetch", "git checkout", "git switch", "git branch", "git worktree",
		"git status", "git diff", "git log", "git show", "git rev-parse",
		"go test ", "pytest ", "cargo test", "npm test", "pnpm test",
	} {
		if strings.HasPrefix(low, pfx) {
			return true
		}
	}
	return false
}

func parseBranchSlugToken(prevLow, low string) string {
	for _, pfx := range []string{"fix/", "feat/", "perf/", "refactor/", "bugfix/", "hotfix/"} {
		if after, ok := strings.CutPrefix(low, pfx); ok {
			if len(after) < 4 || len(after) > 48 || strings.Contains(after, "/") {
				return ""
			}
			return after
		}
	}
	if prevLow != "branch" {
		return ""
	}
	ns, slug, ok := strings.Cut(low, "/")
	if !ok || len(ns) < 2 || len(slug) < 4 || len(low) > 56 ||
		strings.Contains(slug, "/") || strings.Contains(ns, ".") || strings.Contains(slug, ".") {
		return ""
	}
	switch ns {
	case "origin", "upstream", "refs", "heads":
		return ""
	}
	return ns + "-" + slug
}

func extractBranchFeatureSlugs(asked string) []string {
	var out []string
	seen := map[string]bool{}
	prevLow := ""
	for tok := range strings.FieldsSeq(asked) {
		trimmed := strings.Trim(tok, "\"'`(),:;!?[]{}<>.")
		low := strings.ToLower(trimmed)
		rest := parseBranchSlugToken(prevLow, low)
		prevLow = low
		if rest == "" {
			continue
		}
		var words []string
		for w := range strings.FieldsSeq(strings.NewReplacer("-", " ", "_", " ").Replace(rest)) {
			if len(w) < 3 || localizeStopWords[w] || workflowMetaWords[w] || isAllDigitsWord(w) {
				continue
			}
			words = append(words, w)
		}
		if len(words) < 2 && (len(words) == 0 || len(words[0]) < 6) {
			continue
		}
		phrase := strings.Join(words, " ")
		if !seen[phrase] {
			seen[phrase] = true
			out = append(out, phrase)
		}
	}
	return out
}

func hasWorkflowClausePrefix(low string) bool {
	for _, pfx := range workflowClausePrefixes {
		if strings.HasPrefix(low, pfx) {
			return true
		}
	}
	return false
}

// focusTaskPrompt isolates the technical problem description, non-boilerplate
// backticked code/CLI spans, and quoted error diagnostics from multi-paragraph
// task specifications while stripping git/PR/reporting workflow instructions.
func focusTaskPrompt(asked string) (focused string, codeSpans []string, quotedPhrases []string) {
	cleaned := asked
	branchSlugs := extractBranchFeatureSlugs(asked)
	var keptSpans []string
	for rest := asked; ; {
		i := strings.IndexByte(rest, '`')
		if i < 0 {
			break
		}
		j := strings.IndexByte(rest[i+1:], '`')
		if j < 0 {
			break
		}
		span := strings.TrimSpace(rest[i+1 : i+1+j])
		full := rest[i : i+2+j]
		rest = rest[i+2+j:]
		if span == "" || len(span) > 140 || strings.Contains(span, "\n") {
			continue
		}
		if isWorkflowMetaSpan(span) {
			cleaned = strings.ReplaceAll(cleaned, full, " ")
			continue
		}
		keptSpans = append(keptSpans, span)
	}
	for rest := cleaned; ; {
		i := strings.IndexByte(rest, '"')
		if i < 0 {
			break
		}
		j := strings.IndexByte(rest[i+1:], '"')
		if j < 0 {
			break
		}
		phrase := strings.TrimSpace(rest[i+1 : i+1+j])
		rest = rest[i+2+j:]
		if len(phrase) >= 14 && len(phrase) <= 120 && !strings.ContainsAny(phrase, "\n`") && strings.Contains(phrase, " ") {
			quotedPhrases = append(quotedPhrases, phrase)
		}
	}
	var keptLines []string
	for rawLine := range strings.SplitSeq(cleaned, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if hasWorkflowClausePrefix(strings.ToLower(line)) {
			continue
		}
		var keptClauses []string
		for sent := range strings.SplitSeq(line, ". ") {
			sTrim := strings.TrimSpace(sent)
			if sTrim == "" {
				continue
			}
			if hasWorkflowClausePrefix(strings.ToLower(sTrim)) {
				continue
			}
			var keptSubs []string
			for sub := range strings.SplitSeq(sTrim, "; ") {
				subTrim := strings.TrimSpace(sub)
				if subTrim == "" || hasWorkflowClausePrefix(strings.ToLower(subTrim)) {
					continue
				}
				keptSubs = append(keptSubs, subTrim)
			}
			if len(keptSubs) > 0 {
				keptClauses = append(keptClauses, strings.Join(keptSubs, "; "))
			}
		}
		if len(keptClauses) > 0 {
			keptLines = append(keptLines, strings.Join(keptClauses, ". "))
		}
	}
	focused = strings.TrimSpace(strings.Join(keptLines, "\n"))
	if focused == "" {
		focused = asked
	}
	if len(branchSlugs) > 0 {
		lowFocused := strings.ToLower(focused)
		var missing []string
		for _, slug := range branchSlugs {
			keptSpans = append(keptSpans, slug)
			if !strings.Contains(lowFocused, slug) {
				missing = append(missing, slug)
			}
		}
		if len(missing) > 0 {
			focused += "\n" + strings.Join(missing, " ")
		}
	}
	return focused, keptSpans, quotedPhrases
}

var taskRecallPreamblePrefixes = []string{
	"read /", "read ~/.", "read and follow ", "work in ", "create a git worktree",
	"branch from ", "repo:", "workspace:", "cwd:",
}

func stripLeadingBriefPointer(line string) string {
	low := strings.ToLower(line)
	if strings.HasPrefix(low, "read ") {
		if idx := strings.Index(low, ".md"); idx > 0 && idx < 110 {
			if dot := strings.Index(line[idx:], ". "); dot >= 0 {
				return strings.TrimSpace(line[idx+dot+2:])
			}
		}
	}
	if len(line) < 110 {
		for _, pfx := range taskRecallPreamblePrefixes {
			if strings.HasPrefix(low, pfx) {
				return ""
			}
		}
	}
	return line
}

// focusPromptForTaskRecall narrows long task prompts to their opening technical
// problem statement and key code spans so prompt.Terms does not lock onto
// trailing git, lint, or reporting boilerplate.
func focusPromptForTaskRecall(asked string) string {
	trimmed := strings.TrimSpace(asked)
	if len(trimmed) < 240 || strings.HasSuffix(trimmed, "?") {
		return asked
	}
	focused, spans, _ := focusTaskPrompt(trimmed)
	var kept []string
	total := 0
	for rawLine := range strings.SplitSeq(focused, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if len(kept) == 0 {
			line = stripLeadingBriefPointer(line)
			if line == "" {
				continue
			}
		}
		if total > 0 && total+len(line) > 420 {
			if total < 220 {
				remain := 420 - total - 1
				if remain > 60 {
					kept = append(kept, strings.TrimSpace(line[:min(len(line), remain)]))
				}
			}
			break
		}
		kept = append(kept, line)
		total += len(line) + 1
	}
	if len(kept) == 0 {
		return focused
	}
	out := strings.Join(kept, "\n")
	var extraSpans []string
	outLow := strings.ToLower(out)
	for _, sp := range spans {
		if len(sp) < 4 || len(sp) > 40 || strings.Contains(sp, "/") || strings.ContainsAny(sp, " \t") {
			continue
		}
		if !strings.Contains(outLow, strings.ToLower(sp)) {
			extraSpans = append(extraSpans, sp)
			if len(extraSpans) >= 4 {
				break
			}
		}
	}
	if len(extraSpans) > 0 {
		out += "\n" + strings.Join(extraSpans, " ")
	}
	return out
}

func selectPrioritizedTaskWords(focused string, codeSpans, quotedPhrases []string, projSelf map[string]bool, idx *repoSymbolMap, structVeryRareCap, maxWords int) (qWords, boostWords map[string]bool) {
	qWords = map[string]bool{}
	boostWords = map[string]bool{}
	addToken := func(w string, boost bool) {
		if len(qWords) >= maxWords {
			return
		}
		if len(w) < 3 || localizeStopWords[w] || commonThreeLetterStopWords[w] || workflowMetaWords[w] || projSelf[w] || isAllDigitsWord(w) || isHexHashLike(w) {
			return
		}
		if len(w) == 3 && (idx == nil || idx.structDF[w] == 0 || idx.structDF[w] > structVeryRareCap) {
			return
		}
		qWords[w] = true
		if boost {
			boostWords[w] = true
		}
	}
	addText := func(s string, boost bool) {
		for _, w := range extractPromptASCIIWords(s) {
			for _, sub := range splitIdentifierWords(w) {
				addToken(sub, boost)
			}
			addToken(w, boost)
		}
		for _, w := range extractPromptCrossScriptASCIIWords(s) {
			addToken(w, boost)
		}
	}
	for _, span := range codeSpans {
		addText(span, true)
	}
	for _, qp := range quotedPhrases {
		addText(qp, true)
	}
	lines := strings.Split(focused, "\n")
	multiLine := len(lines) > 2
	for i, line := range lines {
		addText(line, multiLine && i < 5)
		if len(qWords) >= maxWords {
			break
		}
	}
	return qWords, boostWords
}

func matchDiagnosticPhraseFiles(cwd string, idx *repoSymbolMap, phrases []string, projSelf map[string]bool) map[string]float64 {
	if len(phrases) == 0 || idx == nil || idx.numFiles == 0 {
		return nil
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" {
		return nil
	}
	var out map[string]float64
	for _, ph := range phrases {
		lowPh := strings.ToLower(strings.TrimSpace(ph))
		if len(lowPh) < 16 {
			continue
		}
		var reqWords []string
		for _, w := range extractPromptASCIIWords(lowPh) {
			if len(w) >= 5 && !localizeStopWords[w] && !projSelf[w] {
				reqWords = append(reqWords, w)
			}
		}
		if len(reqWords) == 0 {
			continue
		}
		var prefiltered []string
		for _, f := range idx.files {
			ok := true
			for _, rw := range reqWords {
				if f.bodyWords[rw] == 0 {
					ok = false
					break
				}
			}
			if ok {
				prefiltered = append(prefiltered, f.rel)
			}
		}
		if len(prefiltered) == 0 || len(prefiltered) > 48 {
			continue
		}
		var hits []string
		for _, rel := range prefiltered {
			raw, err := os.ReadFile(filepath.Join(scanRoot, rel))
			if err != nil || len(raw) > 256<<10 {
				continue
			}
			if strings.Contains(strings.ToLower(string(raw)), lowPh) {
				hits = append(hits, rel)
				if len(hits) > 3 {
					break
				}
			}
		}
		if len(hits) >= 1 && len(hits) <= 3 {
			if out == nil {
				out = map[string]float64{}
			}
			for _, rel := range hits {
				out[rel] = max(out[rel], 10.5)
			}
		}
	}
	return out
}

func hayMatchesHistTerm(hay, qt string) bool {
	runes := []rune(qt)
	if len(runes) <= 3 {
		return containsWholeWord(hay, qt)
	}
	if strings.Contains(hay, qt) {
		return true
	}
	nonASCII := false
	for _, r := range runes {
		if r > 127 {
			nonASCII = true
			break
		}
	}
	if nonASCII {
		stemLen := 0
		switch {
		case len(runes) >= 9:
			stemLen = len(runes) - 3
		case len(runes) >= 6:
			stemLen = len(runes) - 2
		case len(runes) == 5:
			stemLen = 4
		}
		if stemLen >= 4 {
			return containsWordPrefix(hay, string(runes[:stemLen]))
		}
		return false
	}
	if stem := asciiMorphRoot(qt); stem != qt && len(stem) >= 4 {
		return containsWordPrefix(hay, stem)
	}
	return false
}

// containsWholeWord reports whether hay contains word bounded by non-alphanumeric runes.
func containsWholeWord(hay, word string) bool {
	if hay == "" || word == "" {
		return false
	}
	for off := 0; off < len(hay); {
		idx := strings.Index(hay[off:], word)
		if idx < 0 {
			return false
		}
		start := off + idx
		end := start + len(word)
		leftOK := start == 0
		if !leftOK {
			r, _ := utf8.DecodeLastRuneInString(hay[:start])
			leftOK = !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}
		rightOK := end == len(hay)
		if !rightOK {
			r, _ := utf8.DecodeRuneInString(hay[end:])
			rightOK = !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}
		if leftOK && rightOK {
			return true
		}
		off = start + len(word)
	}
	return false
}

func containsWordPrefix(hay, prefix string) bool {
	if prefix == "" {
		return false
	}
	offset := 0
	for {
		idx := strings.Index(hay[offset:], prefix)
		if idx < 0 {
			return false
		}
		pos := offset + idx
		if pos == 0 {
			return true
		}
		prev, _ := utf8.DecodeLastRuneInString(hay[:pos])
		if !unicode.IsLetter(prev) && !unicode.IsDigit(prev) {
			return true
		}
		offset = pos + len(prefix)
	}
}

// repoMemoryTargetBoost scores repository files referenced in .deja/memory.jsonl
// whose entry text or file paths match the prompt's technical vocabulary.
func repoMemoryTargetBoost(cwd, asked string, projSelf map[string]bool) map[string]float64 {
	root := sources.RepoRoot(cwd)
	if root == "" {
		return nil
	}
	if !policy.Load().Allows(policy.ActivationAuto, sources.RepoMemoryProject(root)) {
		return nil
	}
	entries, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil || len(entries) == 0 {
		return nil
	}
	qTerms := extractHistoricalQueryTerms(asked, projSelf)
	if len(qTerms) < 2 {
		return nil
	}
	boost := map[string]float64{}
	for _, e := range entries {
		if len(e.Files) == 0 || len(e.Files) > 4 {
			continue
		}
		live := resolveRepoTouchedTargets(cwd, e.Files)
		if len(live) == 0 || len(live) > 4 {
			continue
		}
		hay := strings.ToLower(e.Kind + " " + e.Text + " " + strings.Join(e.Files, " "))
		matched := 0
		for _, qt := range qTerms {
			if hayMatchesHistTerm(hay, qt) {
				matched++
			}
		}
		if matched < 2 {
			continue
		}
		w := 6.6 + 1.2*float64(min(matched-2, 2))
		for _, rel := range live {
			if w > boost[rel] {
				boost[rel] = w
			}
		}
	}
	return boost
}

func isASCIIOnlyToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

func isStrongTechHistTerm(qt string, literal bool, dfVal, veryRareCap int) bool {
	if isThreeCharASCIIToken(qt) || (isASCIIOnlyToken(qt) && len(qt) >= 4) {
		return true
	}
	if dfVal > veryRareCap {
		return false
	}
	rLen := len([]rune(qt))
	return rLen >= 7 || (literal && rLen >= 6)
}

// fixPairTargetBoost consults in-scope FixPair edit remedies in RAM so prompts
// naming an error family, panic kind, or causal failure vocabulary inherit the
// exact repository file that previously settled it, even without pasted stderr.
func fixPairTargetBoost(dir, cwd, asked string, skip map[string]bool) (map[string]float64, map[string]int) {
	if dir == "" || cwd == "" || !planIndexReady(dir) {
		return nil, nil
	}
	projSelf := selfProjectWordSet(cwd)
	qTerms := extractHistoricalQueryTerms(asked, projSelf)
	if len(qTerms) < 2 {
		return nil, nil
	}
	pairs := index.ReadFixes(dir)
	if len(pairs) == 0 {
		return nil, nil
	}
	pol := policy.Load()
	projNames := cachedProjectNameCandidates(cwd)
	type fixDoc struct {
		rel     string
		sessID  string
		matched []string
		literal map[string]bool
	}
	var docs []fixDoc
	df := map[string]int{}
	pairFileDF := map[string]int{}
	totalPairs := 0
	for _, p := range pairs {
		if p.Edit == "" || p.Candidate {
			continue
		}
		sessID := ""
		if p.Key != "" {
			if _, id, ok := strings.Cut(p.Key, ":"); ok {
				sessID = id
				if skip != nil && skip[id] {
					continue
				}
			}
		}
		if p.Project != "" {
			if !pol.Allows(policy.ActivationAuto, p.Project) {
				continue
			}
			if len(projNames) > 0 && !slices.Contains(projNames, p.Project) {
				continue
			}
		}
		targets := resolveRepoTouchedTargets(cwd, []string{p.Edit})
		if len(targets) == 0 {
			continue
		}
		rel := targets[0]
		pairFileDF[rel]++
		totalPairs++
		hay := strings.ToLower(p.Error + " " + p.Symbol + " " + p.Why)
		var matched []string
		var literal map[string]bool
		for _, qt := range qTerms {
			if hayMatchesHistTerm(hay, qt) {
				matched = append(matched, qt)
				df[qt]++
				if containsWholeWord(hay, qt) {
					if literal == nil {
						literal = map[string]bool{}
					}
					literal[qt] = true
				}
			}
		}
		if len(matched) >= 2 {
			docs = append(docs, fixDoc{rel: rel, sessID: sessID, matched: matched, literal: literal})
		}
	}
	if totalPairs == 0 || len(docs) == 0 {
		return nil, nil
	}
	effectiveTotal := max(totalPairs, 8)
	pairRareCap := max(4, totalPairs/8)
	pairVeryRareCap := max(2, totalPairs/20)
	hubPairCap := max(6, totalPairs/10)
	fileSessCap := max(4, totalPairs/10)
	fileSessionDF := projectFileSessionDF(dir, cwd, skip)
	bestBoost := map[string]float64{}
	bestHits := map[string]int{}
	pairSess := map[string]map[string]bool{}
	hasASCIILit := map[string]bool{}
	for _, d := range docs {
		rareMatched := 0
		veryRareMatched := 0
		literalRare := 0
		hasStrongTech := false
		docASCIILit := false
		var score float64
		for _, qt := range d.matched {
			dfVal := df[qt]
			if dfVal > 0 && dfVal <= pairRareCap {
				rareMatched++
				score += math.Log(1.0 + float64(effectiveTotal)/float64(dfVal))
				if dfVal <= pairVeryRareCap {
					veryRareMatched++
				}
				if d.literal[qt] {
					literalRare++
					if isLiteralASCIITechToken(qt) {
						docASCIILit = true
					}
				}
				if isStrongTechHistTerm(qt, d.literal[qt], dfVal, pairVeryRareCap) {
					hasStrongTech = true
				}
			}
		}
		if rareMatched < 2 || !hasStrongTech || (literalRare < 1 && veryRareMatched < 1 && len(d.matched) < 3) {
			continue
		}
		if d.sessID != "" {
			if pairSess[d.rel] == nil {
				pairSess[d.rel] = map[string]bool{}
			}
			pairSess[d.rel][d.sessID] = true
		}
		if docASCIILit {
			hasASCIILit[d.rel] = true
		}
		w := min(9.5, 2.0+1.65*score)
		if fdf := pairFileDF[d.rel]; fdf > hubPairCap {
			w *= float64(hubPairCap) / float64(fdf)
		}
		if fdf := fileSessionDF[d.rel]; fdf > fileSessCap {
			w *= float64(fileSessCap) / float64(fdf)
		}
		if w > bestBoost[d.rel] {
			bestBoost[d.rel] = w
		}
		if rareMatched > bestHits[d.rel] {
			bestHits[d.rel] = rareMatched
		}
	}
	boost := map[string]float64{}
	hits := map[string]int{}
	for rel, w := range bestBoost {
		if len(pairSess[rel]) < 2 && !hasASCIILit[rel] {
			continue
		}
		boost[rel] = w
		hits[rel] = bestHits[rel]
	}
	return boost, hits
}

func bm25SessionTouchedBoost(dir, cwd, asked string, skip map[string]bool) (map[string]float64, map[string]int) {
	if dir == "" || cwd == "" || !planIndexReady(dir) {
		return nil, nil
	}
	terms := prompt.Terms(focusPromptForTaskRecall(asked))
	if len(terms) == 0 {
		return nil, nil
	}
	pol := policy.Load()
	rawProjects := cachedProjectNameCandidates(cwd)
	projNames := make([]string, 0, len(rawProjects))
	for _, p := range rawProjects {
		if pol.Allows(policy.ActivationAuto, p) {
			projNames = append(projNames, p)
		}
	}
	if len(projNames) == 0 {
		return nil, nil
	}
	ranked, matched, strong, _, _, err := index.ProjectRelevantNamingCut(dir, projNames, terms, 18, skip, nil)
	if err != nil || len(ranked) == 0 {
		return nil, nil
	}
	metas, _ := index.AllMetaShared(dir)
	totalProj := 0
	for _, m := range metas {
		if slices.Contains(projNames, m.Project) {
			totalProj++
		}
	}
	fileSessionDF := projectFileSessionDF(dir, cwd, skip)
	hubFileCap := max(6, totalProj/14)
	qWords := extractTargetQueryWords(cwd, asked)
	var symIdx repoSymbolMap
	var byRel map[string]repoFileEntry
	boost := map[string]float64{}
	sessCount := map[string]int{}
	for i, s := range ranked {
		if i >= 15 {
			break
		}
		if (skip != nil && skip[s.ID]) || s.Kind == "cron" {
			continue
		}
		m, sc := 0, 0
		if i < len(matched) {
			m = matched[i]
		}
		if i < len(strong) {
			sc = strong[i]
		}
		if m < 2 && (sc < 1 || (len(terms) > 8 && i >= 2)) {
			continue
		}
		meta, ok := sessionMetaForTouched(dir, s)
		if !ok || len(meta.Touched) == 0 {
			continue
		}
		targets, okT := filterAndRankTouchedTargetsWithDF(cwd, &symIdx, &byRel, qWords, fileSessionDF, meta.Touched, 28)
		if !okT {
			continue
		}
		docScore := 2.8 + 0.90*float64(min(m, 4)) + 1.40*float64(min(sc, 3))
		if i == 0 {
			docScore *= 1.15
		} else {
			docScore /= 1.0 + 0.12*float64(i)
		}
		capScore := 7.8
		if sc >= 2 || (sc >= 1 && m >= 3) || m >= 4 {
			capScore = 8.8
		}
		if docScore > capScore {
			docScore = capScore
		}
		div := math.Pow(float64(max(1, min(len(targets), 8))), 0.25)
		for pos, rel := range targets {
			if pos >= 6 {
				break
			}
			w := docScore * 0.85
			switch pos {
			case 0:
				w *= 1.45
			case 1:
				if fdf := fileSessionDF[rel]; fdf == 0 || fdf <= hubFileCap {
					w *= 1.12
				}
			case 2:
				w *= 0.75
			case 3:
				w *= 0.55
			case 4:
				w *= 0.42
			case 5:
				w *= 0.35
			}
			w /= div
			if fdf := fileSessionDF[rel]; fdf > hubFileCap {
				w *= float64(hubFileCap) / float64(fdf)
			}
			boost[rel] += w
			sessCount[rel]++
		}
	}
	if len(boost) == 0 {
		return nil, nil
	}
	return boost, sessCount
}

func extractTargetQueryWords(cwd, asked string) map[string]bool {
	if strings.TrimSpace(asked) == "" {
		return nil
	}
	projSelf := selfProjectWordSet(cwd)
	qWords := map[string]bool{}
	addWord := func(w string) {
		if len(w) < 4 || localizeStopWords[w] || workflowMetaWords[w] || projSelf[w] || isAllDigitsWord(w) || isHexHashLike(w) {
			return
		}
		qWords[w] = true
		for _, sub := range splitIdentifierWords(w) {
			if len(sub) >= 4 && !localizeStopWords[sub] && !workflowMetaWords[sub] && !projSelf[sub] && !isAllDigitsWord(sub) && !isHexHashLike(sub) {
				qWords[sub] = true
			}
		}
	}
	for _, w := range extractPromptASCIIWords(asked) {
		addWord(w)
	}
	for _, w := range extractPromptCrossScriptASCIIWords(asked) {
		addWord(w)
	}
	return qWords
}

func buildRepoFileByRel(idx repoSymbolMap) map[string]repoFileEntry {
	if idx.numFiles == 0 {
		return nil
	}
	byRel := make(map[string]repoFileEntry, len(idx.files))
	for _, f := range idx.files {
		byRel[f.rel] = f
	}
	return byRel
}

func projectFileSessionDF(dir, cwd string, skip map[string]bool) map[string]int {
	if dir == "" || cwd == "" || !planIndexReady(dir) {
		return nil
	}
	metas, err := index.AllMetaShared(dir)
	if err != nil || len(metas) == 0 {
		return nil
	}
	pol := policy.Load()
	projNames := cachedProjectNameCandidates(cwd)
	projKey := strings.Join(projNames, "\x00")
	cacheKey := dir + "\x00" + cwd
	var rows []cachedSessionRelFiles
	if v, ok := fileSessionDFCache.Load(cacheKey); ok {
		if cached, ok := v.(fileSessionDFCacheEntry); ok && cached.metaCount == len(metas) && cached.projKey == projKey && time.Since(cached.at) < 2*time.Second {
			rows = cached.rows
		}
	}
	if rows == nil {
		rows = make([]cachedSessionRelFiles, 0, len(metas))
		for _, m := range metas {
			if len(m.Touched) == 0 {
				continue
			}
			if len(projNames) > 0 && !slices.Contains(projNames, m.Project) {
				continue
			}
			seenRel := make(map[string]bool, len(m.Touched))
			var rels []string
			for _, raw := range m.Touched {
				rel := filepath.ToSlash(orientPath(raw, cwd))
				if rel != "" && !seenRel[rel] {
					seenRel[rel] = true
					rels = append(rels, rel)
				}
			}
			if len(rels) > 0 {
				rows = append(rows, cachedSessionRelFiles{id: m.ID, project: m.Project, rels: rels})
			}
		}
		fileSessionDFCache.Store(cacheKey, fileSessionDFCacheEntry{
			at:        time.Now(),
			metaCount: len(metas),
			projKey:   projKey,
			rows:      rows,
		})
	}
	fileSessionDF := map[string]int{}
	for _, row := range rows {
		if skip != nil && skip[row.id] {
			continue
		}
		if !pol.Allows(policy.ActivationAuto, row.project) {
			continue
		}
		for _, rel := range row.rels {
			fileSessionDF[rel]++
		}
	}
	return fileSessionDF
}

func scoreRepoFileEntryWords(f repoFileEntry, qWords map[string]bool) float64 {
	var sc float64
	for qw := range qWords {
		switch {
		case f.stemWords[qw] || f.stem == qw:
			sc += 3.0
		case f.declExact[qw] || f.declWords[qw]:
			sc += 2.0
		case f.pkg == qw:
			sc += 1.0
		case f.bodyWords[qw] > 0 && (len(qw) >= 5 || (len(qw) == 4 && !commonFourLetterBodyStop[qw])):
			sc += 0.5
		default:
			if r := asciiMorphRoot(qw); r != qw && len(r) >= 5 {
				switch {
				case f.stemWords[r]:
					sc += 3.0
				case f.declWords[r]:
					sc += 2.0
				case f.bodyWords[r] > 0:
					sc += 0.5
				}
			}
		}
	}
	return sc
}

func fileMatchesQueryWords(byRel map[string]repoFileEntry, rel string, qWords map[string]bool) bool {
	if len(qWords) == 0 || len(byRel) == 0 {
		return false
	}
	f, ok := byRel[rel]
	if !ok {
		return false
	}
	return scoreRepoFileEntryWords(f, qWords) > 0
}

func projectHubFileCap(dir, cwd string, skip map[string]bool) int {
	if dir == "" || cwd == "" {
		return 6
	}
	metas, err := index.AllMetaShared(dir)
	if err != nil || len(metas) == 0 {
		return 6
	}
	pol := policy.Load()
	projNames := cachedProjectNameCandidates(cwd)
	totalProj := 0
	for _, m := range metas {
		if (skip != nil && skip[m.ID]) || !pol.Allows(policy.ActivationAuto, m.Project) {
			continue
		}
		if len(projNames) > 0 && !slices.Contains(projNames, m.Project) {
			continue
		}
		totalProj++
	}
	return max(6, totalProj/14)
}

func preferNonHubTargets(targets []string, byRel map[string]repoFileEntry, qWords map[string]bool, fileSessionDF map[string]int, hubFileCap int) []string {
	if len(targets) <= 1 || len(fileSessionDF) == 0 || hubFileCap <= 0 {
		return targets
	}
	var preferred, hubs []string
	for _, rel := range targets {
		if fileSessionDF[rel] > hubFileCap && !fileMatchesQueryWords(byRel, rel, qWords) {
			hubs = append(hubs, rel)
			continue
		}
		preferred = append(preferred, rel)
	}
	if len(preferred) == 0 || len(hubs) == 0 {
		return targets
	}
	out := make([]string, 0, len(targets))
	out = append(out, preferred...)
	out = append(out, hubs...)
	return out
}

func rankTouchedWithSymbolMapAndDF(idx *repoSymbolMap, byRel map[string]repoFileEntry, qWords map[string]bool, fileSessionDF map[string]int, resolved []string) (ranked []string, topScore float64, matchedCount int) {
	if len(resolved) == 0 {
		return resolved, 0, 0
	}
	hasScoreSignals := len(qWords) > 0 && len(byRel) > 0
	if !hasScoreSignals && (len(fileSessionDF) == 0 || len(resolved) <= 1) {
		return resolved, 0, 0
	}
	_ = idx
	if len(resolved) == 1 {
		if hasScoreSignals {
			if f, ok := byRel[resolved[0]]; ok {
				sc := scoreRepoFileEntryWords(f, qWords)
				if sc > 0 {
					return resolved, sc, 1
				}
			}
		}
		return resolved, 0, 0
	}
	type scoredRel struct {
		rel   string
		pos   int
		df    int
		score float64
	}
	scored := make([]scoredRel, len(resolved))
	for pos, rel := range resolved {
		var sc float64
		if hasScoreSignals {
			if f, ok := byRel[rel]; ok {
				sc = scoreRepoFileEntryWords(f, qWords)
			}
		}
		scored[pos] = scoredRel{rel: rel, pos: pos, df: fileSessionDF[rel], score: sc}
	}
	slices.SortFunc(scored, func(a, b scoredRel) int {
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		if a.df != b.df {
			if a.df == 0 {
				return 1
			}
			if b.df == 0 {
				return -1
			}
			if a.df < b.df {
				return -1
			}
			return 1
		}
		return a.pos - b.pos
	})
	out := make([]string, len(resolved))
	for i, sr := range scored {
		out[i] = sr.rel
		if sr.score > 0 {
			matchedCount++
		}
	}
	return out, scored[0].score, matchedCount
}

func sessionMetaForTouched(dir string, s model.Session) (index.SessionMeta, bool) {
	meta, ok := index.MetaForKey(dir, s.Key())
	if ok && len(meta.Touched) > 0 {
		return meta, true
	}
	if noteID := s.PromotedNoteID(); noteID != "" {
		if srcKey, okSrc := model.PromotedNoteSourceKey(noteID); okSrc {
			if srcMeta, okM := index.MetaForKey(dir, srcKey); okM && len(srcMeta.Touched) > 0 {
				return srcMeta, true
			}
		}
	}
	return meta, ok
}

func selectLowDFTouchedTargets(ranked []string, fileSessionDF map[string]int, limit int) []string {
	if len(fileSessionDF) == 0 {
		return nil
	}
	var lowDF []string
	hubCount := 0
	for _, rel := range ranked {
		df := fileSessionDF[rel]
		if df > 5 {
			hubCount++
		} else if df > 0 {
			lowDF = append(lowDF, rel)
		}
	}
	if hubCount == 0 || len(lowDF) == 0 || len(lowDF) > limit {
		return nil
	}
	return lowDF
}

const maxUnrankedTouchedTargets = 8

func filterAndRankTouchedTargetsWithDF(
	cwd string,
	symIdx *repoSymbolMap,
	byRel *map[string]repoFileEntry,
	qWords map[string]bool,
	fileSessionDF map[string]int,
	rawTouched []string,
	maxRanked int,
) ([]string, bool) {
	resolved := resolveRepoTouchedTargets(cwd, rawTouched)
	if len(resolved) == 0 || len(resolved) > maxRanked {
		return nil, false
	}
	if len(qWords) == 0 && len(fileSessionDF) == 0 {
		if len(resolved) > maxUnrankedTouchedTargets {
			return nil, false
		}
		return resolved, true
	}
	if len(resolved) <= 1 {
		return resolved, true
	}
	if !policy.SourceHintsOff() && len(qWords) > 0 && *byRel == nil && symIdx.numFiles == 0 {
		*symIdx = loadRepoSymbolMap(cwd)
		*byRel = buildRepoFileByRel(*symIdx)
	}
	ranked, topScore, matchedCount := rankTouchedWithSymbolMapAndDF(symIdx, *byRel, qWords, fileSessionDF, resolved)
	if len(resolved) <= maxUnrankedTouchedTargets {
		return ranked, true
	}
	if topScore >= 2.0 && matchedCount > 0 {
		return ranked[:min(matchedCount, 6)], true
	}
	if lowDF := selectLowDFTouchedTargets(ranked, fileSessionDF, 4); len(lowDF) > 0 {
		return lowDF, true
	}
	return nil, false
}

type histMatchedDoc struct {
	touched   []string
	matched   []string
	titleHits map[string]bool
}

type scoredHistDoc struct {
	targets  []string
	docScore float64
}

func collectHistoricalMatchedDocs(dir, cwd string, qTerms []string, skip map[string]bool) ([]histMatchedDoc, map[string]int, int) {
	metas, err := index.AllMetaShared(dir)
	if err != nil || len(metas) == 0 {
		return nil, nil, 0
	}
	pol := policy.Load()
	projNames := cachedProjectNameCandidates(cwd)
	var projDocs []histMatchedDoc
	df := map[string]int{}
	totalProj := 0
	for _, m := range metas {
		if (skip != nil && skip[m.ID]) || len(m.Touched) == 0 || !pol.Allows(policy.ActivationAuto, m.Project) {
			continue
		}
		if len(projNames) > 0 && !slices.Contains(projNames, m.Project) {
			continue
		}
		totalProj++
		titleLow := strings.ToLower(m.Title)
		hay := titleLow + " " + strings.ToLower(m.Settled)
		var hits []string
		var tHits map[string]bool
		for _, qt := range qTerms {
			if !hayMatchesHistTerm(hay, qt) {
				continue
			}
			hits = append(hits, qt)
			df[qt]++
			if titleLow != "" && hayMatchesHistTerm(titleLow, qt) {
				if tHits == nil {
					tHits = map[string]bool{}
				}
				tHits[qt] = true
			}
		}
		if len(hits) > 0 {
			projDocs = append(projDocs, histMatchedDoc{touched: m.Touched, matched: hits, titleHits: tHits})
		}
	}
	return projDocs, df, totalProj
}

func aggregateQualifiedHistBoosts(qualifiedDocs []scoredHistDoc, fileSessionDF map[string]int, hubFileCap int) (map[string]float64, map[string]int) {
	boost := map[string]float64{}
	sessCount := map[string]int{}
	qualifyingDocs := len(qualifiedDocs)
	for _, qd := range qualifiedDocs {
		for pos, rel := range qd.targets {
			if pos >= 6 {
				break
			}
			w := qd.docScore * 0.85
			switch pos {
			case 0:
				w *= 1.45
			case 1:
				if fdf := fileSessionDF[rel]; fdf == 0 || fdf <= hubFileCap {
					w *= 1.12
				}
			case 2:
				w *= 0.75
			case 3:
				w *= 0.55
			case 4:
				w *= 0.42
			case 5:
				w *= 0.35
			}
			w /= math.Pow(float64(max(1, len(qd.targets))), 0.25)
			if fdf := fileSessionDF[rel]; fdf > hubFileCap {
				w *= float64(hubFileCap) / float64(fdf)
			}
			boost[rel] += w
			sessCount[rel]++
		}
	}
	if qualifyingDocs >= 4 {
		var maxBoost float64
		for _, b := range boost {
			if b > maxBoost {
				maxBoost = b
			}
		}
		for rel, count := range sessCount {
			if float64(count)/float64(qualifyingDocs) < 0.20 && boost[rel] < maxBoost*0.45 {
				delete(boost, rel)
				delete(sessCount, rel)
			}
		}
	}
	return boost, sessCount
}

// historicalTitleTouchedBoost scans cached SessionMeta rows of the current
// project in RAM (0 disk reads) to transfer domain/multilingual prompt terms
// from prior coding sessions onto the live files those sessions edited.
func historicalTitleTouchedBoost(dir, cwd, asked string, skip map[string]bool) (map[string]float64, map[string]int) {
	if dir == "" || cwd == "" || !planIndexReady(dir) {
		return nil, nil
	}
	projSelf := selfProjectWordSet(cwd)
	qTerms := extractHistoricalQueryTerms(asked, projSelf)
	if len(qTerms) == 0 || (len(qTerms) == 1 && len([]rune(qTerms[0])) < 5) {
		return nil, nil
	}
	projDocs, df, totalProj := collectHistoricalMatchedDocs(dir, cwd, qTerms, skip)
	if totalProj < 3 || len(projDocs) == 0 {
		return nil, nil
	}
	rareSessionCap := max(6, totalProj/10)
	veryRareCap := max(4, totalProj/25)
	ultraRareCap := max(2, totalProj/35)
	fileSessionDF := projectFileSessionDF(dir, cwd, skip)
	hubFileCap := max(6, totalProj/14)
	qWords := extractTargetQueryWords(cwd, asked)
	var symIdx repoSymbolMap
	var byRel map[string]repoFileEntry
	var qualifiedDocs []scoredHistDoc
	for _, d := range projDocs {
		rareHits := 0
		veryRareHits := 0
		ultraRareHits := 0
		rareTitleHits := 0
		var docScore float64
		for _, qt := range d.matched {
			if df[qt] <= rareSessionCap {
				rareHits++
				if d.titleHits[qt] {
					rareTitleHits++
				}
				if df[qt] <= veryRareCap {
					veryRareHits++
				}
				if df[qt] <= ultraRareCap && len([]rune(qt)) >= 5 {
					ultraRareHits++
				}
				docScore += math.Log(1.0 + float64(totalProj)/float64(df[qt]))
			}
		}
		if (veryRareHits < 1 || rareHits < 2) && rareHits < 3 && veryRareHits < 2 && ultraRareHits < 1 {
			continue
		}
		if ultraRareHits >= 1 && docScore < 4.2 {
			docScore = 4.2
		}
		if rareTitleHits >= 1 {
			docScore *= 1.35
		}
		if docScore > 6.5 {
			docScore = 6.5
		}
		targets, okT := filterAndRankTouchedTargetsWithDF(cwd, &symIdx, &byRel, qWords, fileSessionDF, d.touched, 28)
		if !okT {
			continue
		}
		qualifiedDocs = append(qualifiedDocs, scoredHistDoc{targets: targets, docScore: docScore})
	}
	slices.SortStableFunc(qualifiedDocs, func(a, b scoredHistDoc) int {
		if b.docScore > a.docScore {
			return 1
		}
		if a.docScore > b.docScore {
			return -1
		}
		return 0
	})
	if len(qualifiedDocs) > 12 {
		qualifiedDocs = qualifiedDocs[:12]
	}
	return aggregateQualifiedHistBoosts(qualifiedDocs, fileSessionDF, hubFileCap)
}

func sessionActionMapLineForPrompt(dir, cwd, asked string, ss []model.Session, skip map[string]bool) string {
	if dir == "" || cwd == "" || len(ss) == 0 {
		return ""
	}
	qWords := extractTargetQueryWords(cwd, asked)
	var idx repoSymbolMap
	var byRel map[string]repoFileEntry
	if !policy.SourceHintsOff() && len(qWords) > 0 {
		idx = loadRepoSymbolMap(cwd)
		byRel = buildRepoFileByRel(idx)
	}
	fileSessionDF := projectFileSessionDF(dir, cwd, skip)
	hubFileCap := projectHubFileCap(dir, cwd, skip)
	var targets []string
	for _, s := range ss {
		meta, ok := sessionMetaForTouched(dir, s)
		if !ok || len(meta.Touched) == 0 {
			continue
		}
		resolved, okT := filterAndRankTouchedTargetsWithDF(cwd, &idx, &byRel, qWords, fileSessionDF, meta.Touched, 36)
		if !okT {
			continue
		}
		resolved = preferNonHubTargets(resolved, byRel, qWords, fileSessionDF, hubFileCap)
		for _, rel := range resolved {
			targets = appendUniqueRel(targets, rel)
			if len(targets) >= 3 {
				break
			}
		}
		if len(targets) >= 2 {
			break
		}
	}
	if len(targets) == 0 {
		return ""
	}
	targets = preferNonHubTargets(targets, byRel, qWords, fileSessionDF, hubFileCap)
	if len(targets) > 3 {
		targets = targets[:3]
	}
	return formatLocalizedTargetsLine(dir, cwd, "files edited there: ", targets, skip)
}

// rankedTouchedConsensusLine checks whether >=2 strongly matching past sessions
// of the project independently converged on editing the same live repository
// source file, recovering localization even when conversational prose was too
// terse to quote or written in another human language.
func rankedTouchedConsensusLine(dir, cwd string, ranked []model.Session, matched, strong []int, seen, skip map[string]bool) (string, string) {
	return rankedTouchedConsensusLineForPrompt(dir, cwd, "", ranked, matched, strong, seen, skip)
}

func rankedTouchedConsensusLineForPrompt(dir, cwd, asked string, ranked []model.Session, matched, strong []int, seen, skip map[string]bool) (string, string) {
	if dir == "" || cwd == "" || len(ranked) == 0 {
		return "", ""
	}
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return "", ""
	}
	pol := policy.Load()
	projNames := cachedProjectNameCandidates(cwd)
	qWords := extractTargetQueryWords(cwd, asked)
	fileSessionDF := projectFileSessionDF(dir, cwd, skip)
	hubFileCap := projectHubFileCap(dir, cwd, skip)
	var symIdx repoSymbolMap
	var byRel map[string]repoFileEntry
	type tally struct {
		sessions int
		weight   float64
	}
	counts := map[string]*tally{}
	contributingSessions := 0
	for i := 0; i < len(ranked) && contributingSessions < 12; i++ {
		s := ranked[i]
		if skip != nil && skip[s.ID] {
			continue
		}
		if s.Kind == "cron" || !pol.Allows(policy.ActivationAuto, s.Project) {
			continue
		}
		if len(projNames) > 0 && s.Project != "" && !slices.Contains(projNames, s.Project) {
			continue
		}
		mCount, sCount := 0, 0
		if i < len(matched) {
			mCount = matched[i]
		}
		if i < len(strong) {
			sCount = strong[i]
		}
		if mCount < 2 && sCount < 1 {
			continue
		}
		meta, ok := sessionMetaForTouched(dir, s)
		if !ok || len(meta.Touched) == 0 {
			continue
		}
		touched, okT := filterAndRankTouchedTargetsWithDF(cwd, &symIdx, &byRel, qWords, fileSessionDF, meta.Touched, 36)
		if !okT {
			continue
		}
		contributingSessions++
		baseW := float64(mCount + 2*sCount)
		if i < 3 {
			baseW *= 1.25
		}
		for pos, rel := range touched {
			if pos >= 6 {
				break
			}
			t := counts[rel]
			if t == nil {
				t = &tally{}
				counts[rel] = t
			}
			t.sessions++
			w := baseW
			if pos == 0 {
				w *= 1.4
			} else if pos >= 4 {
				w *= 0.7
			}
			if fdf := fileSessionDF[rel]; fdf > 10 {
				w *= 10.0 / float64(fdf)
			}
			t.weight += w
		}
	}
	type cand struct {
		rel      string
		sessions int
		weight   float64
	}
	var cands []cand
	if contributingSessions >= 2 {
		ensureRepoFileByRel(cwd, qWords, &symIdx, &byRel)
		for rel, t := range counts {
			if fileSessionDF[rel] > hubFileCap && !fileMatchesQueryWords(byRel, rel, qWords) {
				continue
			}
			if t.sessions < 2 {
				continue
			}
			if t.sessions < 3 && float64(t.sessions)/float64(contributingSessions) < 0.20 && fileSessionDF[rel] > 8 {
				continue
			}
			cands = append(cands, cand{rel: rel, sessions: t.sessions, weight: t.weight})
		}
	}
	if len(cands) == 0 {
		return singleStrongSessionTouchedLineWithDF(dir, cwd, asked, ranked, matched, strong, seen, skip, pol, fileSessionDF, hubFileCap)
	}
	slices.SortFunc(cands, func(a, b cand) int {
		if a.weight != b.weight {
			if a.weight > b.weight {
				return -1
			}
			return 1
		}
		return strings.Compare(a.rel, b.rel)
	})
	targets := []string{cands[0].rel}
	if len(cands) > 1 && cands[1].weight >= cands[0].weight*0.58 {
		targets = append(targets, cands[1].rel)
	}
	if len(cands) > 2 && cands[2].weight >= cands[0].weight*0.68 {
		targets = append(targets, cands[2].rel)
	}
	line := formatLocalizedTargetsLine(dir, cwd, "files edited in related sessions: ", targets, skip)
	if line == "" {
		return "", ""
	}
	tok := "prompt:rc:" + shortHash(line)
	if seen != nil && seen[tok] {
		return "", ""
	}
	return line, tok
}

func hasNonASCIILetter(s string) bool {
	for _, r := range s {
		if r > 127 && unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func allowTwoTermLeafMatch(asked string, qWords map[string]bool, touched []string, fileSessionDF map[string]int, byRel map[string]repoFileEntry, hubFileCap int) bool {
	if len(touched) == 0 || len(touched) > 4 || len(fileSessionDF) == 0 {
		return false
	}
	df := fileSessionDF[touched[0]]
	if df <= 0 || df > hubFileCap {
		return false
	}
	if len(qWords) > 0 {
		return fileMatchesQueryWords(byRel, touched[0], qWords)
	}
	return hasNonASCIILetter(asked)
}

func singleStrongSessionTouchedLineWithDF(dir, cwd, asked string, ranked []model.Session, matched, strong []int, seen, skip map[string]bool, pol policy.Policy, fileSessionDF map[string]int, hubFileCap int) (string, string) {
	projNames := cachedProjectNameCandidates(cwd)
	qWords := extractTargetQueryWords(cwd, asked)
	var symIdx repoSymbolMap
	var byRel map[string]repoFileEntry
	checkedTouched := 0
	for i := 0; i < len(ranked) && i < 20 && checkedTouched < 5; i++ {
		s0 := ranked[i]
		if (skip != nil && skip[s0.ID]) || s0.Kind == "cron" || !pol.Allows(policy.ActivationAuto, s0.Project) {
			continue
		}
		if len(projNames) > 0 && !slices.Contains(projNames, s0.Project) {
			continue
		}
		m0, s0Count := 0, 0
		if i < len(matched) {
			m0 = matched[i]
		}
		if i < len(strong) {
			s0Count = strong[i]
		}
		allowTwoTermLeaf := i == 0 && m0 >= 2 && s0Count == 0 && (len(qWords) > 0 || hasNonASCIILetter(asked))
		if m0 < 2 || (!allowTwoTermLeaf && s0Count < 1 && m0 < 3) {
			continue
		}
		softBM25 := s0Count < 2 && m0 < 3
		if softBM25 && i >= 3 {
			continue
		}
		meta0, ok0 := sessionMetaForTouched(dir, s0)
		if !ok0 || len(meta0.Touched) == 0 {
			continue
		}
		touched0, okT := filterAndRankTouchedTargetsWithDF(cwd, &symIdx, &byRel, qWords, fileSessionDF, meta0.Touched, 36)
		if !okT || (softBM25 && len(touched0) > 8) {
			continue
		}
		if allowTwoTermLeaf {
			ensureRepoFileByRel(cwd, qWords, &symIdx, &byRel)
			if !allowTwoTermLeafMatch(asked, qWords, touched0, fileSessionDF, byRel, hubFileCap) {
				continue
			}
		}
		if softBM25 && len(fileSessionDF) > 0 {
			hasNonHub := false
			for _, rel := range touched0 {
				if fileSessionDF[rel] <= hubFileCap {
					hasNonHub = true
					break
				}
			}
			if !hasNonHub {
				continue
			}
		}
		checkedTouched++
		touched0 = preferNonHubTargets(touched0, byRel, qWords, fileSessionDF, hubFileCap)
		line := formatLocalizedTargetsLine(dir, cwd, "files edited in related session: ", touched0[:min(3, len(touched0))], skip)
		if line == "" {
			continue
		}
		tok := "prompt:rc:" + shortHash(line)
		if seen == nil || !seen[tok] {
			return line, tok
		}
	}
	return "", ""
}

func resolveRepoTouchedTargets(cwd string, touched []string) []string {
	var primary, tests []string
	seen := map[string]bool{}
	for _, raw := range touched {
		rel := orientPath(raw, cwd)
		if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		if seen[rel] {
			continue
		}
		base := filepath.Base(rel)
		if strings.HasPrefix(base, ".") {
			continue
		}
		abs := filepath.Join(cwd, filepath.FromSlash(rel))
		if !fileThere(abs) {
			continue
		}
		seen[rel] = true
		if isLocalizableSourcePath(abs, base) {
			primary = append(primary, rel)
		} else if isTestFilePath(abs) {
			tests = append(tests, rel)
		}
	}
	if len(primary) > 0 {
		return primary
	}
	return tests
}

func formatLocalizedTargetsLine(dir, cwd, prefix string, targets []string, skip map[string]bool) string {
	if len(targets) == 0 {
		return ""
	}
	safePaths := make([]string, 0, len(targets))
	for _, rel := range targets {
		safePaths = append(safePaths, search.SafePath(rel))
	}
	head := prefix + strings.Join(safePaths, ", ")
	primary := targets[0]
	inScope := scopedFileSessionsForPrompt(dir, cwd, primary, skip)
	dec := turn0FileGuardrail(dir, cwd, primary, inScope, skip)
	var co string
	if len(targets) == 1 {
		co = fileHookCoChangeLineIn(cwd, primary, inScope)
		if co == "" {
			co = coupledCommitGuardForFiles(cwd, []string{primary})
		}
	}
	ran := fileHookRanLineFor(dir, primary, inScope)
	if ran == "" {
		_, fileSyms := fileHookSymbols(cwd, primary, "")
		ran = siblingTestLineFor(cwd, primary, fileSyms)
	}
	if dec == "" && co == "" && ran == "" {
		return head
	}
	if assembled := assembleFileHookLine(head, dec, co, ran); assembled != "" {
		return assembled
	}
	return head
}

func scopedFileSessionsForPrompt(dir, cwd, rel string, skip map[string]bool) []index.SessionMeta {
	if dir == "" || !planIndexReady(dir) {
		return nil
	}
	pol := policy.Load()
	projects := cachedProjectNameCandidates(cwd)
	var inScope []index.SessionMeta
	for _, meta := range index.FileSessions(dir, rel) {
		if skip != nil && skip[meta.ID] {
			continue
		}
		if !pol.Allows(policy.ActivationAuto, meta.Project) {
			continue
		}
		if !fileMetaInScope(meta, rel, nil) && !fileMetaInScope(meta, rel, projects) {
			continue
		}
		inScope = append(inScope, meta)
	}
	return inScope
}

func appendUniqueRel(list []string, items ...string) []string {
	for _, it := range items {
		if it != "" && !slices.Contains(list, it) {
			list = append(list, it)
		}
	}
	return list
}

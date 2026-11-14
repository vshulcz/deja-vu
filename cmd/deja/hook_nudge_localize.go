package main

import (
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

type repoSymbolMap struct {
	at       time.Time
	files    []repoFileEntry
	byStem   map[string][]string
	stemDF   map[string]int
	declDF   map[string]int
	structDF map[string]int
	bodyDF   map[string]int
	numFiles int
}

var repoSymbolMapCache sync.Map

func loadRepoSymbolMap(cwd string) repoSymbolMap {
	if cwd == "" {
		return repoSymbolMap{}
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" {
		return repoSymbolMap{}
	}
	if v, ok := repoSymbolMapCache.Load(scanRoot); ok {
		if cached, ok := v.(repoSymbolMap); ok && time.Since(cached.at) < 2*time.Second {
			return cached
		}
	}
	fi, err := os.Stat(scanRoot)
	if err != nil || !fi.IsDir() {
		return repoSymbolMap{}
	}
	idx := repoSymbolMap{
		at:       time.Now(),
		byStem:   map[string][]string{},
		stemDF:   map[string]int{},
		declDF:   map[string]int{},
		structDF: map[string]int{},
		bodyDF:   map[string]int{},
	}
	_ = filepath.WalkDir(scanRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // unreadable paths skipped during best-effort walk
		}
		if d.IsDir() {
			name := d.Name()
			if p != scanRoot && (isSkippedScanDir(name) || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(idx.files) >= 2000 {
			return filepath.SkipAll
		}
		if !isLocalizableSourcePath(p, d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(cwd, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			if r2, err2 := filepath.Rel(scanRoot, p); err2 == nil && !strings.HasPrefix(r2, "..") {
				rel = r2
			} else {
				return nil
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
			if len(w) >= 3 && !localizeStopWords[w] {
				entry.stemWords[w] = true
				seenStruct[w] = true
			}
		}
		if len(pkg) >= 4 && !localizeStopWords[pkg] {
			seenStruct[pkg] = true
		}
		if raw, err := os.ReadFile(p); err == nil && len(raw) <= 256<<10 { //nolint:gosec // G122: local repo source indexing under user cwd
			content := string(raw)
			for line := range strings.SplitSeq(content, "\n") {
				if id, sig := parseDeclarationLine(line); id != "" && sig != "" {
					lowID := strings.ToLower(id)
					entry.declExact[lowID] = true
					seenStruct[lowID] = true
					for _, w := range splitIdentifierWords(id) {
						if len(w) >= 4 && !localizeStopWords[w] {
							entry.declWords[w] = true
							seenStruct[w] = true
						}
					}
				}
			}
			indexFileBodyWords(content, entry.bodyWords, seenBody)
		}
		idx.files = append(idx.files, entry)
		idx.byStem[stem] = append(idx.byStem[stem], rel)
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
		return nil
	})
	idx.numFiles = len(idx.files)
	repoSymbolMapCache.Store(scanRoot, idx)
	return idx
}

func indexFileBodyWords(content string, bodyWords map[string]int, seenBody map[string]bool) {
	var cur []rune
	flush := func() {
		if len(cur) < 5 || len(cur) > 48 {
			cur = cur[:0]
			return
		}
		tok := string(cur)
		cur = cur[:0]
		low := strings.ToLower(tok)
		if !localizeStopWords[low] && bodyWords[low] < 8 {
			bodyWords[low]++
			seenBody[low] = true
		}
		if hasMixedCaseOrUnderscore(tok) {
			for _, sub := range splitIdentifierWords(tok) {
				if len(sub) >= 5 && !localizeStopWords[sub] && bodyWords[sub] < 8 {
					bodyWords[sub]++
					seenBody[sub] = true
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

// promptTaskTargetsLine maps natural-language module phrases, rare repository
// identifiers, and historical prompt-to-edit correlations onto live repository
// source files and their verified test recipes at Turn 0.
func promptTaskTargetsLine(dir, cwd, asked string, seen, skip map[string]bool) (string, []string) {
	if cwd == "" || !policy.Load().Allows(policy.ActivationAuto, "") {
		return "", nil
	}
	idx := loadRepoSymbolMap(cwd)
	if idx.numFiles == 0 {
		return "", nil
	}
	asked = strings.TrimSpace(digest.StripHarnessBlocks(asked))
	if asked == "" {
		return "", nil
	}
	focused, codeSpans, quotedPhrases := focusTaskPrompt(asked)
	words := extractPromptASCIIWords(focused)
	var matchedFiles []string

	// 1. Check adjacent 3-word and 2-word phrases matching exact file stems or declared symbols.
	matchedFiles = matchAdjacentStemPhrases(&idx, words)

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

func matchAdjacentStemPhrases(idx *repoSymbolMap, words []string) []string {
	var matchedFiles []string
	for i := range words {
		if localizeStopWords[words[i]] {
			continue
		}
		if i+2 < len(words) && !localizeStopWords[words[i+1]] && !localizeStopWords[words[i+2]] {
			s3 := words[i] + "_" + words[i+1] + "_" + words[i+2]
			if len(s3) >= 10 && len(idx.byStem[s3]) > 0 && len(idx.byStem[s3]) <= 2 {
				matchedFiles = appendUniqueRel(matchedFiles, idx.byStem[s3]...)
			}
		}
		if i+1 < len(words) && !localizeStopWords[words[i+1]] {
			s2 := words[i] + "_" + words[i+1]
			if len(s2) >= 8 && len(idx.byStem[s2]) > 0 && len(idx.byStem[s2]) <= 2 {
				matchedFiles = appendUniqueRel(matchedFiles, idx.byStem[s2]...)
			}
			comp := words[i] + words[i+1]
			if len(comp) >= 10 && idx.declDF[comp] > 0 && idx.declDF[comp] <= 2 {
				for _, f := range idx.files {
					if f.declExact[comp] {
						matchedFiles = appendUniqueRel(matchedFiles, f.rel)
					}
				}
			}
		}
	}
	return matchedFiles
}

type taskScoreParams struct {
	idx               *repoSymbolMap
	qWords            map[string]bool
	boostWords        map[string]bool
	diagBoost         map[string]float64
	fixBoost          map[string]float64
	fixHits           map[string]int
	histBoost         map[string]float64
	histSessions      map[string]int
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

func scoreSingleRepoFile(f repoFileEntry, p *taskScoreParams) (float64, bool) {
	var score float64
	rareHits := 0
	anchorHit := false
	idx := p.idx
	if len(f.stem) >= 7 && p.qWords[f.stem] && idx.stemDF[f.stem] == 1 && idx.structDF[f.stem] <= p.structVeryRareCap {
		score += 7.5
		rareHits += 2
		anchorHit = true
	}
	if db := p.diagBoost[f.rel]; db > 0 {
		score += db
		rareHits += 2
		anchorHit = true
	}
	for qw := range p.qWords {
		mult := 1.0
		if p.boostWords[qw] {
			mult = 1.20
		}
		sDF := idx.structDF[qw]
		inStem := f.stemWords[qw]
		inDecl := f.declWords[qw] || f.declExact[qw]
		if (inStem || inDecl || f.pkg == qw) && sDF > 0 && sDF <= p.structRareCap {
			idf := math.Log(1.0+float64(p.effectiveN)/float64(sDF)) * mult
			rareHits++
			switch {
			case inStem:
				score += idf * 1.75
				if sDF <= p.structVeryRareCap {
					anchorHit = true
				}
			case f.declExact[qw] && len(qw) >= 7 && idx.declDF[qw] <= 2:
				score += idf * 2.0
				anchorHit = true
			case inDecl:
				score += idf * 1.25
				if sDF <= p.structVeryRareCap {
					anchorHit = true
				}
			default:
				score += idf * 0.75
			}
			continue
		}
		bDF := idx.bodyDF[qw]
		bodyTF := f.bodyWords[qw]
		if bodyTF > 0 && bDF > 0 && bDF <= p.bodyRareCap && len(qw) >= 5 {
			idf := math.Log(1.0+float64(p.effectiveN)/float64(bDF)) * mult
			tfBoost := 1.0 + 0.15*math.Min(float64(bodyTF-1), 4)
			score += idf * 0.80 * tfBoost
			rareHits++
			if bDF <= max(3, idx.numFiles/90) && bodyTF >= 2 {
				anchorHit = true
			}
		}
	}
	if fb := p.fixBoost[f.rel]; fb > 0 {
		score += fb
		if p.fixHits[f.rel] >= 2 {
			rareHits += 2
			anchorHit = true
		} else if rareHits >= 1 {
			rareHits++
			anchorHit = true
		}
	}
	if hb := p.histBoost[f.rel]; hb > 0 {
		score += hb
		if p.histSessions[f.rel] >= 2 || hb >= 5.2 {
			rareHits += 2
			anchorHit = true
		} else if rareHits >= 1 {
			rareHits++
			anchorHit = true
		}
	}
	for nb, votes := range p.cooccurVotes {
		if !f.stemWords[nb] && !f.declWords[nb] && !f.declExact[nb] {
			continue
		}
		idf := math.Log(1.0 + float64(p.effectiveN)/float64(idx.structDF[nb]))
		if votes >= 2 {
			score += idf * 1.15
			rareHits++
			anchorHit = true
		} else if rareHits >= 1 {
			score += idf * 0.55
			rareHits++
		}
	}
	return score, rareHits >= 2 && anchorHit && score >= 6.4
}

func scoreTaskTargetCandidates(dir, cwd, asked, focused string, codeSpans, quotedPhrases []string, idx *repoSymbolMap, skip map[string]bool) []string {
	histBoost, histSessions := historicalTitleTouchedBoost(dir, cwd, asked, skip)
	fixBoost, fixHits := fixPairTargetBoost(dir, cwd, asked, skip)
	structRareCap := max(10, idx.numFiles/22)
	structVeryRareCap := max(5, idx.numFiles/55)
	bodyRareCap := max(6, idx.numFiles/45)
	projSelf := selfProjectWordSet(cwd)
	qWords, boostWords := selectPrioritizedTaskWords(focused, codeSpans, quotedPhrases, projSelf, 28)
	params := taskScoreParams{
		idx:               idx,
		qWords:            qWords,
		boostWords:        boostWords,
		diagBoost:         matchDiagnosticPhraseFiles(cwd, idx, quotedPhrases, projSelf),
		fixBoost:          fixBoost,
		fixHits:           fixHits,
		histBoost:         histBoost,
		histSessions:      histSessions,
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
	concentrated := len(cands) <= maxSpecificCands ||
		cands[0].score >= cands[maxSpecificCands-1].score*1.28 ||
		(cands[0].score >= 11.0 && len(cands) > 5 && cands[0].score >= cands[5].score*1.35)
	if !concentrated {
		return nil
	}
	var matchedFiles []string
	matchedFiles = appendUniqueRel(matchedFiles, cands[0].rel)
	if len(cands) > 1 && cands[1].score >= cands[0].score*0.75 {
		matchedFiles = appendUniqueRel(matchedFiles, cands[1].rel)
	}
	if len(cands) > 2 && cands[2].score >= cands[0].score*0.82 {
		matchedFiles = appendUniqueRel(matchedFiles, cands[2].rel)
	}
	return matchedFiles
}

func selfProjectWordSet(cwd string) map[string]bool {
	out := map[string]bool{
		"github": true, "gitlab": true, "bitbucket": true,
		"origin": true, "upstream": true, "master": true,
	}
	for _, p := range digest.ProjectNameCandidates(cwd) {
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
	"давай": true, "дава": true, "дальше": true, "дальш": true,
	"изучать": true, "изуча": true, "вопрос": true, "вопро": true,
	"низкоуровнево": true, "низкоуровн": true, "улучшать": true, "улучш": true,
	"улучшения": true, "улучшени": true, "инструмент": true, "инструм": true,
	"работы": true, "работ": true, "работа": true, "работать": true,
	"агент": true, "агентов": true, "агенты": true, "агента": true,
	"думать": true, "подумать": true, "механизм": true, "механизмы": true,
	"лечить": true, "помогать": true, "помочь": true, "людей": true, "людям": true,
	"разработчик": true, "разработчикам": true, "сделать": true,
	"посмотрим": true, "посмотреть": true, "продолжай": true, "продолжать": true,
	"нужно": true, "можно": true, "сейчас": true, "вообще": true,
	"просто": true, "реально": true, "полезно": true, "лучше": true, "хуже": true,
	"неделю": true, "последнюю": true, "какие": true, "почему": true,
	"когда": true, "через": true, "после": true, "перед": true,
}

func extractHistoricalQueryTerms(asked string, projSelf map[string]bool) []string {
	focused, codeSpans, quotedPhrases := focusTaskPrompt(asked)
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.Trim(strings.ToLower(t), "._-/")
		if len([]rune(t)) < 4 || localizeStopWords[t] || workflowMetaWords[t] || conversationalHistStopWords[t] || projSelf[t] || seen[t] {
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
	addWords(focused)
	// Non-ASCII words (e.g. Cyrillic/European inflected nouns/verbs) beyond the 6-term prompt cap.
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
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

var workflowMetaWords = map[string]bool{
	"worktree": true, "worktrees": true, "branched": true, "commits": true,
	"committed": true, "pushed": true, "pushing": true, "merged": true,
	"merging": true, "deliverables": true, "contributor": true,
	"blanket": true,
}

var workflowClausePrefixes = []string{
	"report:", "report pr ", "report branch", "deliverables:", "output format:",
	"do not push", "don't push", "do not post ", "do not merge", "never push",
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

// focusTaskPrompt isolates the technical problem description, non-boilerplate
// backticked code/CLI spans, and quoted error diagnostics from multi-paragraph
// task specifications while stripping git/PR/reporting workflow instructions.
func focusTaskPrompt(asked string) (focused string, codeSpans []string, quotedPhrases []string) {
	cleaned := asked
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
		lowLine := strings.ToLower(line)
		skipLine := false
		for _, pfx := range workflowClausePrefixes {
			if strings.HasPrefix(lowLine, pfx) {
				skipLine = true
				break
			}
		}
		if skipLine {
			continue
		}
		var keptClauses []string
		for sent := range strings.SplitSeq(line, ". ") {
			sTrim := strings.TrimSpace(sent)
			if sTrim == "" {
				continue
			}
			lowSent := strings.ToLower(sTrim)
			dropSent := false
			for _, pfx := range workflowClausePrefixes {
				if strings.HasPrefix(lowSent, pfx) {
					dropSent = true
					break
				}
			}
			if !dropSent {
				keptClauses = append(keptClauses, sTrim)
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
	return focused, keptSpans, quotedPhrases
}

func selectPrioritizedTaskWords(focused string, codeSpans, quotedPhrases []string, projSelf map[string]bool, maxWords int) (qWords, boostWords map[string]bool) {
	qWords = map[string]bool{}
	boostWords = map[string]bool{}
	addToken := func(w string, boost bool) {
		if len(qWords) >= maxWords {
			return
		}
		if len(w) < 4 || localizeStopWords[w] || workflowMetaWords[w] || projSelf[w] {
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
		addText(line, multiLine && i < 2)
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
	if strings.Contains(hay, qt) {
		return true
	}
	runes := []rune(qt)
	if len(runes) >= 6 {
		nonASCII := false
		for _, r := range runes {
			if r > 127 {
				nonASCII = true
				break
			}
		}
		if nonASCII {
			stemLen := len(runes) - 2
			if len(runes) >= 8 {
				stemLen = len(runes) - 3
			}
			if stemLen >= 5 {
				return containsWordPrefix(hay, string(runes[:stemLen]))
			}
		}
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
	projNames := digest.ProjectNameCandidates(cwd)
	boost := map[string]float64{}
	hits := map[string]int{}
	for _, p := range pairs {
		if p.Edit == "" || p.Candidate {
			continue
		}
		if p.Key != "" && skip != nil {
			if _, id, ok := strings.Cut(p.Key, ":"); ok && skip[id] {
				continue
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
		hay := strings.ToLower(p.Error + " " + p.Symbol + " " + p.Why)
		matched := 0
		for _, qt := range qTerms {
			if hayMatchesHistTerm(hay, qt) {
				matched++
			}
		}
		if matched >= 2 {
			w := 3.6 + 1.4*float64(min(matched-2, 2))
			if w > boost[rel] {
				boost[rel] = w
			}
			if matched > hits[rel] {
				hits[rel] = matched
			}
		}
	}
	return boost, hits
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
	if len(qTerms) < 2 {
		return nil, nil
	}
	metas, err := index.AllMetaShared(dir)
	if err != nil || len(metas) == 0 {
		return nil, nil
	}
	pol := policy.Load()
	projNames := digest.ProjectNameCandidates(cwd)
	type doc struct {
		touched []string
		matched []string
	}
	var projDocs []doc
	df := map[string]int{}
	totalProj := 0
	for _, m := range metas {
		if skip != nil && skip[m.ID] {
			continue
		}
		if len(m.Touched) == 0 || !pol.Allows(policy.ActivationAuto, m.Project) {
			continue
		}
		if len(projNames) > 0 && !slices.Contains(projNames, m.Project) {
			continue
		}
		totalProj++
		hay := strings.ToLower(m.Title + " " + m.Settled)
		var hits []string
		for _, qt := range qTerms {
			if hayMatchesHistTerm(hay, qt) {
				hits = append(hits, qt)
				df[qt]++
			}
		}
		if len(hits) > 0 {
			projDocs = append(projDocs, doc{touched: m.Touched, matched: hits})
		}
	}
	if totalProj < 3 || len(projDocs) == 0 {
		return nil, nil
	}
	rareSessionCap := min(max(5, totalProj/12), 18)
	fileSessionDF := map[string]int{}
	for _, m := range metas {
		if skip != nil && skip[m.ID] {
			continue
		}
		if len(m.Touched) == 0 || !pol.Allows(policy.ActivationAuto, m.Project) {
			continue
		}
		if len(projNames) > 0 && !slices.Contains(projNames, m.Project) {
			continue
		}
		seenRel := map[string]bool{}
		for _, raw := range m.Touched {
			rel := filepath.ToSlash(orientPath(raw, cwd))
			if rel != "" && !seenRel[rel] {
				seenRel[rel] = true
				fileSessionDF[rel]++
			}
		}
	}
	hubFileCap := max(8, totalProj/8)
	boost := map[string]float64{}
	sessCount := map[string]int{}
	qualifyingDocs := 0
	for _, d := range projDocs {
		rareHits := 0
		var docScore float64
		for _, qt := range d.matched {
			if df[qt] <= rareSessionCap {
				rareHits++
				docScore += math.Log(1.0 + float64(totalProj)/float64(df[qt]))
			}
		}
		if rareHits < 2 && (rareHits < 1 || len(d.matched) < 3) {
			continue
		}
		qualifyingDocs++
		targets := resolveRepoTouchedTargets(cwd, d.touched)
		if len(targets) == 0 || len(targets) > 6 {
			continue
		}
		for pos, rel := range targets {
			if pos >= 3 {
				break
			}
			w := docScore * 0.85
			if pos == 0 {
				w *= 1.35
			}
			if fdf := fileSessionDF[rel]; fdf > hubFileCap {
				w *= float64(hubFileCap) / float64(fdf)
			}
			boost[rel] += w
			sessCount[rel]++
		}
	}
	if qualifyingDocs >= 4 {
		for rel, count := range sessCount {
			if float64(count)/float64(qualifyingDocs) < 0.25 {
				delete(boost, rel)
				delete(sessCount, rel)
			}
		}
	}
	return boost, sessCount
}

func sessionActionMapLineForPrompt(dir, cwd, asked string, ss []model.Session, skip map[string]bool) string {
	if dir == "" || cwd == "" || len(ss) == 0 {
		return ""
	}
	var qWords map[string]bool
	var idx repoSymbolMap
	if strings.TrimSpace(asked) != "" {
		projSelf := selfProjectWordSet(cwd)
		qWords = map[string]bool{}
		for _, w := range extractPromptASCIIWords(asked) {
			if len(w) < 4 || localizeStopWords[w] || projSelf[w] {
				continue
			}
			qWords[w] = true
			for _, sub := range splitIdentifierWords(w) {
				if len(sub) >= 4 && !localizeStopWords[sub] && !projSelf[sub] {
					qWords[sub] = true
				}
			}
		}
		if len(qWords) > 0 {
			idx = loadRepoSymbolMap(cwd)
		}
	}
	var byRel map[string]repoFileEntry
	if idx.numFiles > 0 {
		byRel = make(map[string]repoFileEntry, len(idx.files))
		for _, f := range idx.files {
			byRel[f.rel] = f
		}
	}
	var targets []string
	for _, s := range ss {
		meta, ok := index.MetaForKey(dir, s.Key())
		if !ok || len(meta.Touched) == 0 {
			continue
		}
		resolved := resolveRepoTouchedTargets(cwd, meta.Touched)
		if len(resolved) == 0 || len(resolved) > 8 {
			continue
		}
		if len(resolved) > 1 && len(qWords) > 0 && len(byRel) > 0 {
			type scoredRel struct {
				rel   string
				pos   int
				score float64
			}
			scored := make([]scoredRel, len(resolved))
			for pos, rel := range resolved {
				var sc float64
				if f, ok := byRel[rel]; ok {
					for qw := range qWords {
						switch {
						case f.stemWords[qw] || f.stem == qw:
							sc += 3.0
						case f.declExact[qw] || f.declWords[qw]:
							sc += 2.0
						case f.pkg == qw:
							sc += 1.0
						case f.bodyWords[qw] > 0 && len(qw) >= 5:
							sc += 0.5
						}
					}
				}
				scored[pos] = scoredRel{rel: rel, pos: pos, score: sc}
			}
			slices.SortFunc(scored, func(a, b scoredRel) int {
				if a.score != b.score {
					if a.score > b.score {
						return -1
					}
					return 1
				}
				return a.pos - b.pos
			})
			for i, sr := range scored {
				resolved[i] = sr.rel
			}
		}
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
	if dir == "" || cwd == "" || len(ranked) == 0 {
		return "", ""
	}
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return "", ""
	}
	pol := policy.Load()
	type tally struct {
		sessions int
		weight   float64
	}
	counts := map[string]*tally{}
	contributingSessions := 0
	for i := 0; i < len(ranked) && contributingSessions < 10; i++ {
		s := ranked[i]
		if skip != nil && skip[s.ID] {
			continue
		}
		if s.Kind == "cron" || !pol.Allows(policy.ActivationAuto, s.Project) {
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
		meta, ok := index.MetaForKey(dir, s.Key())
		if !ok || len(meta.Touched) == 0 {
			continue
		}
		touched := resolveRepoTouchedTargets(cwd, meta.Touched)
		if len(touched) == 0 || len(touched) > 8 {
			continue
		}
		contributingSessions++
		baseW := float64(mCount + 2*sCount)
		if i < 3 {
			baseW *= 1.25
		}
		for pos, rel := range touched {
			if pos >= 4 {
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
			}
			t.weight += w
		}
	}
	if contributingSessions < 2 {
		return "", ""
	}
	type cand struct {
		rel      string
		sessions int
		weight   float64
	}
	var cands []cand
	for rel, t := range counts {
		if t.sessions < 2 {
			continue
		}
		if float64(t.sessions)/float64(contributingSessions) < 0.28 {
			continue
		}
		cands = append(cands, cand{rel: rel, sessions: t.sessions, weight: t.weight})
	}
	if len(cands) == 0 {
		return "", ""
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
	if len(cands) > 1 && cands[1].weight >= cands[0].weight*0.70 {
		targets = append(targets, cands[1].rel)
	}
	if len(cands) > 2 && cands[2].weight >= cands[0].weight*0.80 {
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
	projects := digest.ProjectNameCandidates(cwd)
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

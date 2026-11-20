package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Negative knowledge — "we tried X, it did not work" — is the most expensive
// thing a store can lose, and deja already models it: `promote --state
// rejected`. On a real 1165-session store, exactly zero sessions carry that
// state, because it is set by hand after the fact and nobody goes back.
//
// Asking for it is hopeless too: "what did we decide" is 0.4% of user turns on
// that store. People do not query decisions; they state outcomes, in the
// moment, in the middle of the work — 2.2% of sessions say in so many words
// that something was backed out.
//
// So the nudge goes where the evidence appears: the sentence where the user
// says it failed. One line, once every twenty minutes at most, and only when
// the user's own words carry the report. It asks for `remember` rather than
// `promote --state rejected` because in that moment the session that failed is
// the one being written and has no id yet.

const nudgeGap = 20 * time.Minute

// failureNudge returns the line to add when the user has just reported backing
// something out, or "" for the silence that is the default.
func failureNudge(dir, prompt string) string {
	if !reportsFailure(prompt) {
		return ""
	}
	if !nudgeDue(dir) {
		return ""
	}
	return "The user just said an approach did not hold. Dead ends are the one thing this store never records — if that is what happened, save it now with `deja remember \"<what was tried, why it failed>\"`, in one line, before moving on."
}

func joinPromptAdvisories(parts ...string) string {
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// promptDirectContext performs live correction capture into .deja/memory.jsonl
// and returns any high-precision repo-memory guardrail or verified error fix
// matching the user's prompt.
func promptDirectContext(dir, cwd, sessionID, asked string) string {
	return promptDirectContextIn(dir, cwd, sessionID, asked, false, nil)
}

type promptAdvisoryItem struct {
	line   string
	tokens []string
}

func joinAdvisoryItems(items []promptAdvisoryItem) string {
	lines := make([]string, 0, len(items))
	for _, it := range items {
		if it.line != "" {
			lines = append(lines, it.line)
		}
	}
	return strings.Join(lines, "\n")
}

func nonEmptyTokens(toks ...string) []string {
	var out []string
	for _, tok := range toks {
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

func filterFreshPromptAdvisories(items []promptAdvisoryItem, latestSeen map[string]bool) ([]promptAdvisoryItem, []string) {
	kept := make([]promptAdvisoryItem, 0, len(items))
	var freshToks []string
	seenTok := map[string]bool{}
	for _, it := range items {
		dup := false
		for _, tok := range it.tokens {
			if tok != "" && latestSeen[tok] {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		kept = append(kept, it)
		for _, tok := range it.tokens {
			if tok != "" && !seenTok[tok] {
				seenTok[tok] = true
				freshToks = append(freshToks, tok)
			}
		}
	}
	return kept, freshToks
}

func promptDirectContextIn(dir, cwd, sessionID, asked string, readOnly bool, skip map[string]bool) string {
	var seen map[string]bool
	if !readOnly {
		seen = alreadyInjected(dir, sessionID)
	}
	items := collectPromptDirectAdvisories(dir, cwd, sessionID, asked, readOnly, seen, skip)
	if !readOnly && sessionID != "" {
		if _, tokens := filterFreshPromptAdvisories(items, nil); len(tokens) > 0 {
			rememberInjectedIDs(dir, sessionID, tokens...)
		}
	}
	return joinAdvisoryItems(items)
}

func collectPromptDirectAdvisories(dir, cwd, sessionID, asked string, readOnly bool, seen, skip map[string]bool, prevAssistant ...string) []promptAdvisoryItem {
	if strings.TrimSpace(asked) == "" || digest.IsAgentArtifact(asked) {
		return nil
	}
	if !readOnly {
		autoRecordPromptCorrection(cwd, sessionID, asked)
	}
	var items []promptAdvisoryItem
	if srLine, srTok := sessionStandingRuleLine(dir, cwd, sessionID, asked, seen, prevAssistant...); srLine != "" {
		items = append(items, promptAdvisoryItem{line: srLine, tokens: nonEmptyTokens(srTok)})
	}
	if rmLine, rmTok := promptRepoMemoryLine(cwd, asked, seen); rmLine != "" {
		items = append(items, promptAdvisoryItem{line: rmLine, tokens: nonEmptyTokens(rmTok)})
	}
	if fxLine, fxTok := promptErrorFixLine(dir, cwd, asked, seen); fxLine != "" {
		items = append(items, promptAdvisoryItem{line: fxLine, tokens: nonEmptyTokens(fxTok)})
	}
	if isSpawnedReader(sessionID) {
		return items
	}
	namedSourceFile := false
	if fbLine, fbToks := promptFileBriefLineIn(dir, cwd, asked, seen, skip); fbLine != "" {
		items = append(items, promptAdvisoryItem{line: fbLine, tokens: nonEmptyTokens(fbToks...)})
		if head, _, _ := strings.Cut(fbLine, " — "); isLocalizableSourcePath(head, filepath.Base(head)) {
			namedSourceFile = true
		}
	}
	if len(items) < 4 {
		sbLine, sbTok := promptSymbolBriefLine(cwd, asked, seen)
		joined := joinAdvisoryItems(items)
		if sbLine != "" && (!namedSourceFile || lineAddsNewTargetPath(joined, sbLine)) {
			if nudgeHasLocalizedGuidance(joined) && textHasLiveTestRecipe(joined) {
				sbLine = dropLiveTestRecipeSuffix(sbLine)
			}
			items = append(items, promptAdvisoryItem{line: sbLine, tokens: nonEmptyTokens(sbTok)})
		}
	}
	if len(items) < 5 {
		if locLine, locToks := promptTaskTargetsLine(dir, cwd, asked, seen, skip); locLine != "" {
			joined := joinAdvisoryItems(items)
			if !symbolBriefAlreadyCoversTarget(joined, locLine) && lineAddsNewTargetPath(joined, locLine) {
				if nudgeHasLocalizedGuidance(joined) {
					locLine = compactTargetSupplementLineWithPrefix(joined, locLine, "likely target: ", 3)
				}
				if locLine != "" {
					items = append(items, promptAdvisoryItem{line: locLine, tokens: nonEmptyTokens(locToks...)})
				}
			}
		}
	}
	return items
}

func emitCommittedNudgeOnly(
	dir, cwd, sessionID, asked string,
	items []promptAdvisoryItem,
	failNudge string,
	ranked []model.Session,
	matched, strong []int,
	skip map[string]bool,
	tryConsensus, plain bool,
	stdout io.Writer,
) error {
	if len(items) == 0 && failNudge == "" && !tryConsensus {
		return nil
	}
	var rcLine, rcTok string
	if tryConsensus && len(ranked) > 0 {
		snapSeen := alreadyInjected(dir, sessionID)
		rcLine, rcTok = rankedTouchedConsensusLineForPrompt(dir, cwd, asked, ranked, matched, strong, snapSeen, skip)
	}
	if len(items) == 0 && failNudge == "" && rcLine == "" {
		return nil
	}
	releaseSeenLock, _ := holdHookseenLock(dir)
	latest := alreadyInjected(dir, sessionID)
	kept, freshToks := filterFreshPromptAdvisories(items, latest)
	nudge := joinPromptAdvisories(joinAdvisoryItems(kept), failNudge)
	if tryConsensus && rcLine != "" && (rcTok == "" || !latest[rcTok]) && lineAddsNewTargetPath(nudge, rcLine) {
		if compact := compactTargetSupplementLine(nudge, rcLine); compact != "" {
			nudge = joinPromptAdvisories(nudge, compact)
			if rcTok != "" {
				freshToks = append(freshToks, rcTok)
			}
		}
	}
	if sessionID != "" && len(freshToks) > 0 {
		rememberInjectedIDs(dir, sessionID, freshToks...)
	}
	releaseSeenLock()
	return emitNudgeOnly(stdout, plain, nudge)
}

func dropLiveTestRecipeSuffix(s string) string {
	parts := strings.Split(s, " — ")
	if len(parts) <= 1 {
		return s
	}
	kept := []string{parts[0]}
	for _, seg := range parts[1:] {
		if textHasLiveTestRecipe(" — " + seg) {
			continue
		}
		kept = append(kept, seg)
	}
	return strings.Join(kept, " — ")
}

// promptSymbolBriefLine locates a symbol declared in the repository when a
// user prompt names a specific function, method, or type without naming its
// file, giving the agent exact file:line, signature, and sibling test target at
// Turn 0 without a grep/locate round-trip.
func promptSymbolBriefLine(cwd, asked string, seen map[string]bool) (string, string) {
	if cwd == "" || policy.SourceHintsOff() || !policy.Load().Allows(policy.ActivationAuto, "") {
		return "", ""
	}
	checked := map[string]bool{}
	type candToken struct {
		tok   string
		low   string
		order int
	}
	var eligible []candToken
	for i, tok := range identifierTokens(asked) {
		low := strings.ToLower(tok)
		if len(low) < 6 || checked[low] || !isSymbolLikeCaseOrUnderscore(tok) || isCommonKeyword(low) {
			continue
		}
		checked[low] = true
		eligible = append(eligible, candToken{tok: tok, low: low, order: i})
	}
	if len(eligible) == 0 {
		return "", ""
	}
	pol := policy.Load()
	idx := loadRepoSymbolMap(cwd)
	type symbolCand struct {
		tok   string
		df    int
		order int
		rel   string
		line  int
		sig   string
	}
	var cands []symbolCand
	useIdx := idx.numFiles > 0 || idx.capped
	for _, item := range eligible {
		if useIdx {
			df := idx.declDF[item.low]
			if df == 0 || df > 3 {
				continue
			}
			loc, ok := idx.firstDecl[item.low]
			if !ok || loc.rel == "" || loc.sig == "" {
				continue
			}
			cands = append(cands, symbolCand{tok: item.tok, df: df, order: item.order, rel: loc.rel, line: loc.line, sig: loc.sig})
			continue
		}
		rel, lineNo, sig := findSymbolDeclarationInRepo(cwd, item.tok)
		if rel != "" && sig != "" {
			cands = append(cands, symbolCand{tok: item.tok, df: 1, order: item.order, rel: rel, line: lineNo, sig: sig})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].df != cands[j].df {
			return cands[i].df < cands[j].df
		}
		return cands[i].order < cands[j].order
	})
	for _, cand := range cands {
		line := fmt.Sprintf("defined in %s:%d: %s", search.SafePath(cand.rel), cand.line, clipDecision(search.SafeText(cand.sig), 140))
		if repoDec := repoMemoryDecisionForFile(cwd, cand.rel, []string{cand.tok}, pol); repoDec != "" {
			line += repoDecisionLabel + repoDec
		}
		if idx.numFiles <= 250 {
			if callers := crossFileCallersLine(cwd, cand.rel, []string{cand.tok}); callers != "" {
				line += callers
			}
		} else if idx.numFiles <= 1500 {
			if callers := crossFileCallersFromIndexLine(cwd, &idx, cand.rel, cand.tok, cand.sig); callers != "" {
				line += callers
			}
		}
		if ran := siblingTestLineFor(cwd, cand.rel, []string{cand.tok}); ran != "" && !strings.Contains(line, ran) {
			line += ran
		}
		pTok := "prompt:sb:" + shortHash(line)
		if seen != nil && seen[pTok] {
			continue
		}
		return line, pTok
	}
	return "", ""
}

func crossFileCallersFromIndexLine(cwd string, idx *repoSymbolMap, defRel, sym, sig string) string {
	lowSym := strings.ToLower(strings.TrimSpace(sym))
	if cwd == "" || idx == nil || len(lowSym) < 4 {
		return ""
	}
	defSlash := filepath.ToSlash(defRel)
	defDir := filepath.Dir(defSlash)
	ext := strings.ToLower(filepath.Ext(defSlash))
	pkgBase := strings.ToLower(filepath.Base(defDir))
	isMethod := strings.HasPrefix(strings.TrimSpace(sig), "func (")

	type candFile struct {
		rel     string
		sameDir bool
		hits    int
	}
	var cands []candFile
	for _, f := range idx.files {
		if f.rel == defSlash || f.bodyWords[lowSym] == 0 || isTestFilePath(f.rel) {
			continue
		}
		cands = append(cands, candFile{
			rel:     f.rel,
			sameDir: filepath.Dir(f.rel) == defDir,
			hits:    f.bodyWords[lowSym],
		})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].sameDir != cands[j].sameDir {
			return cands[i].sameDir
		}
		if cands[i].hits != cands[j].hits {
			return cands[i].hits > cands[j].hits
		}
		return len(cands[i].rel) < len(cands[j].rel)
	})
	var callers []string
	checked := 0
	for _, cf := range cands {
		if checked >= 8 {
			break
		}
		checked++
		candPath := filepath.Join(cwd, filepath.FromSlash(cf.rel))
		needle := lowSym
		if !cf.sameDir && ext == ".go" && pkgBase != "" && pkgBase != "." && !isMethod {
			needle = pkgBase + "." + lowSym
		}
		lineNo := firstCallLineInFile(candPath, sym, needle)
		if lineNo <= 0 {
			continue
		}
		callers = append(callers, fmt.Sprintf("%s:%d", search.SafePath(cf.rel), lineNo))
		if len(callers) >= 3 {
			break
		}
	}
	if len(callers) == 0 && defSlash != "" {
		defPath := filepath.Join(cwd, filepath.FromSlash(defSlash))
		if lineNo := firstCallLineInFile(defPath, sym, lowSym); lineNo > 0 {
			callers = append(callers, fmt.Sprintf("%s:%d", search.SafePath(defSlash), lineNo))
		}
	}
	if len(callers) == 0 {
		return ""
	}
	return " — callers of " + search.SafeText(sym) + ": " + strings.Join(callers, ", ")
}

// promptFileBriefLine checks whether the user's prompt explicitly names a file
// in the repository and returns a compact 1-line brief with its coupled
// companion file and verified test command so the agent has both at Turn 0.
func promptFileBriefLine(dir, cwd, asked string, seen map[string]bool) (string, []string) {
	return promptFileBriefLineIn(dir, cwd, asked, seen, nil)
}

func promptFileBriefLineIn(dir, cwd, asked string, seen, skip map[string]bool) (string, []string) {
	if cwd == "" {
		return "", nil
	}
	getTree := sync.OnceValue(func() repoFileTree {
		if policy.SourceHintsOff() {
			return repoFileTree{}
		}
		return orientRepoTree(cwd)
	})
	projects := sync.OnceValue(func() []string { return cachedProjectNameCandidates(cwd) })
	pol := policy.Load()
	seenFiles := map[string]bool{}
	var docFallbackLine string
	var docFallbackToks []string
	for tok := range strings.FieldsSeq(asked) {
		tok = strings.Trim(tok, "\"'`(),:;!?[]{}<>")
		if idx := strings.IndexByte(tok, ':'); idx > 0 {
			tok = tok[:idx]
		}
		if tok == "" || !looksLikePromptFileRef(tok) {
			continue
		}
		cleanTok := filepath.ToSlash(filepath.Clean(tok))
		rel := tok
		if cleanTok != "." && !filepath.IsAbs(tok) && !strings.HasPrefix(cleanTok, "../") && fileThere(filepath.Join(cwd, cleanTok)) {
			rel = cleanTok
		} else if resolved := getTree().resolveRel(tok); resolved != "" {
			rel = resolved
		} else if !fileThere(filepath.Join(cwd, tok)) {
			continue
		}
		if seenFiles[rel] {
			continue
		}
		seenFiles[rel] = true
		var inScope []index.SessionMeta
		if dir != "" && planIndexReady(dir) {
			for _, meta := range index.FileSessions(dir, rel) {
				if skip != nil && skip[meta.ID] {
					continue
				}
				if !pol.Allows(policy.ActivationAuto, meta.Project) {
					continue
				}
				if !fileMetaInScope(meta, rel, nil) && !fileMetaInScope(meta, rel, projects()) {
					continue
				}
				inScope = append(inScope, meta)
			}
		}
		co := fileHookCoChangeLineIn(cwd, rel, inScope)
		if co == "" {
			co = coupledCommitGuardForFiles(cwd, []string{rel})
		}
		ran := fileHookRanLineFor(dir, rel, inScope)
		if ran == "" && !policy.SourceHintsOff() {
			_, fileSyms := fileHookSymbols(cwd, rel, "")
			ran = siblingTestLineFor(cwd, rel, fileSyms)
		}
		dec := turn0FileGuardrail(dir, cwd, rel, inScope, skip)
		line := assembleFileHookLine(search.SafePath(rel), dec, co, ran)
		if line == "" {
			continue
		}
		pTok := "prompt:fb:" + shortHash(line)
		tTok := "tool:" + shortHash(dedupeFact(line))
		if seen != nil && (seen[pTok] || seen[tTok]) {
			continue
		}
		if !isLocalizableSourcePath(rel, filepath.Base(rel)) {
			if docFallbackLine == "" {
				docFallbackLine = line
				docFallbackToks = []string{pTok, tTok}
			}
			continue
		}
		return line, []string{pTok, tTok}
	}
	return docFallbackLine, docFallbackToks
}

// liveRecallBrief constructs a live repository intelligence brief when an agent
// queries `deja recall` with a symbol name, file reference, compiler diagnostic,
// or failing test output, giving all 44 agents live repo facts even with zero history.
func liveRecallBrief(dir, cwd, query string) string {
	return liveRecallBriefScoped(dir, cwd, "", 0, query)
}

func liveRecallBriefScoped(dir, cwd, project string, offset int, query string) string {
	if policy.SourceHintsOff() || offset > 0 {
		return ""
	}
	if cwd == "" {
		cwd = howCwd()
	}
	query = strings.TrimSpace(query)
	if cwd == "" || query == "" || looksLikeSessionID(query) || !cwdMatchesRecallProject(cwd, project) {
		return ""
	}
	pol := policy.Load()
	var parts []string

	if fb, _ := promptFileBriefLine(dir, cwd, query, nil); fb != "" {
		parts = append(parts, strings.TrimSpace(fb))
	}
	if sb, _ := promptSymbolBriefLine(cwd, query, nil); sb != "" {
		parts = append(parts, strings.TrimSpace(sb))
	}
	if len(parts) == 0 {
		if loc, _ := promptTaskTargetsLine(dir, cwd, query, nil, nil); loc != "" && liveLikelyTargetMatchesQuery(loc, query) {
			parts = append(parts, strings.TrimSpace(loc))
		}
	}
	if comp := compilerSymbolSignatureLine(cwd, query, pol); comp != "" {
		parts = append(parts, strings.TrimSpace(comp))
	}
	if fail := failingTestSourceLine(cwd, query, pol); fail != "" {
		parts = append(parts, strings.TrimSpace(fail))
	}

	if len(parts) == 0 {
		return ""
	}
	return "live repo brief: " + strings.Join(parts, " — ")
}

// liveLikelyTargetMatchesQuery drops speculative localized targets whose
// path/stem does not share concrete tokens with the recall query.
func liveLikelyTargetMatchesQuery(loc, query string) bool {
	const prefix = "likely target: "
	rest, ok := strings.CutPrefix(strings.TrimSpace(loc), prefix)
	if !ok {
		return true
	}
	if head, _, hasSep := strings.Cut(rest, " — "); hasSep {
		rest = head
	}
	lowQuery := strings.ToLower(query)
	qTokens := map[string]bool{}
	for _, tok := range extractPromptASCIIWords(lowQuery) {
		if len(tok) >= 3 && !localizeStopWords[tok] {
			qTokens[tok] = true
		}
	}
	for rawTarget := range strings.SplitSeq(rest, ",") {
		rel := strings.TrimSpace(rawTarget)
		if rel == "" {
			continue
		}
		lowRel := strings.ToLower(filepath.ToSlash(rel))
		base := strings.ToLower(filepath.Base(lowRel))
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if len(stem) >= 4 && (strings.Contains(lowQuery, lowRel) || strings.Contains(lowQuery, base) || strings.Contains(lowQuery, stem)) {
			return true
		}
		var stemMatched int
		var longMatch bool
		seenTok := map[string]bool{}
		for _, tok := range splitTargetStemTokens(stem) {
			if seenTok[tok] || !qTokens[tok] {
				continue
			}
			seenTok[tok] = true
			stemMatched++
			if len(tok) >= 6 {
				longMatch = true
			}
		}
		if stemMatched >= 2 || longMatch {
			return true
		}
	}
	return false
}

func splitTargetStemTokens(stem string) []string {
	parts := strings.FieldsFunc(stem, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/'
	})
	var out []string
	for _, p := range parts {
		if len(p) >= 3 && !localizeStopWords[p] {
			out = append(out, p)
		}
	}
	return out
}

func cwdMatchesRecallProject(cwd, project string) bool {
	project = strings.TrimSpace(project)
	if project == "" {
		return true
	}
	if cwd == "" {
		return false
	}
	if index.ProjectInScopeStrict(cwd, project) || index.ProjectInScopeStrict(project, cwd) {
		return true
	}
	if !strings.ContainsAny(project, `/\:`) {
		if strings.EqualFold(filepath.Base(filepath.Clean(cwd)), project) {
			return true
		}
		if root := safeScanRoot(cwd); root != "" && strings.EqualFold(filepath.Base(filepath.Clean(root)), project) {
			return true
		}
	}
	return false
}

func looksLikePromptFileRef(tok string) bool {
	if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") || strings.HasPrefix(tok, "-") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(tok))
	switch ext {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".rb", ".java", ".c", ".cc", ".cpp", ".h", ".hpp",
		".cs", ".swift", ".kt", ".scala", ".sh", ".bash", ".zsh", ".fish", ".yaml", ".yml", ".toml", ".json",
		".jsonl", ".sql", ".proto", ".graphql", ".vue", ".svelte", ".astro", ".css", ".scss", ".html", ".md",
		".png", ".svg", ".jpg", ".jpeg", ".webp", ".gif":
		return len(tok) > len(ext)
	}
	return false
}

// autoRecordPromptCorrection captures a user's standing prohibition or correction
// from a live prompt directly into .deja/memory.jsonl when it names a repo file
// or path, so subsequent tool hooks in the same session enforce it immediately.
func autoRecordPromptCorrection(cwd, sessionID, asked string) {
	if cwd == "" {
		return
	}
	root := sources.RepoRoot(cwd)
	if root == "" || sources.RepoAutoDisabled(root) {
		return
	}
	memPath := sources.RepoMemoryPath(root)
	if !fileThere(memPath) {
		return
	}
	raw := strings.TrimSpace(asked)
	if raw == "" || strings.HasPrefix(raw, "!") || pseudoUserRe.MatchString(raw) || !isStandingCorrection(raw) {
		return
	}
	text := cleanRepoCandidateLine(raw)
	if text == "" || len([]rune(text)) > 260 || !usableRepoCandidateLine(text) {
		return
	}
	files := extractMentionedRepoFiles(root, text, nil)
	if len(files) == 0 {
		return
	}
	kind := inferRepoKind(text)
	if kind == "decision" {
		kind = "gotcha"
	}
	text = trimRepoCandidateText(text)
	entry := sources.RepoEntry{
		ID:       sources.RepoEntryID(kind, text),
		Kind:     kind,
		Text:     text,
		Files:    files,
		Sessions: 1,
		From:     repoFrom(sessionID, time.Now()),
		Date:     time.Now().UTC().Format("2006-01-02"),
	}
	_, _, _, _ = sources.EvolveRepoEntries(root, []sources.RepoEntry{entry}, 50)
}

func promptRepoMemoryLine(cwd, asked string, seen map[string]bool) (string, string) {
	if cwd == "" {
		return "", ""
	}
	root := sources.RepoRoot(cwd)
	if root == "" {
		return "", ""
	}
	pol := policy.Load()
	if !pol.Allows(policy.ActivationAuto, sources.RepoMemoryProject(root)) {
		return "", ""
	}
	entries, _, err := sources.ReadRepoMemory(sources.RepoMemoryPath(root))
	if err != nil || len(entries) == 0 {
		return "", ""
	}
	rank := map[string]int{"rejected": 0, "gotcha": 1, "decision": 2}
	sort.SliceStable(entries, func(i, j int) bool {
		if rank[entries[i].Kind] != rank[entries[j].Kind] {
			return rank[entries[i].Kind] < rank[entries[j].Kind]
		}
		return entries[i].Date > entries[j].Date
	})
	for _, e := range entries {
		matched := commandTouchesRepoEntryFiles(asked, e.Files)
		if !matched {
			for _, f := range e.Files {
				if repoEntryMentionsFile(root, f, asked) {
					matched = true
					break
				}
			}
		}
		if !matched && len(e.Files) == 0 {
			matched = repoEntryMentionsCommand(asked, e.Text)
		}
		if !matched {
			continue
		}
		scope := ""
		if len(e.Files) > 0 {
			scope = " (" + strings.Join(e.Files, ", ") + ")"
		}
		line := fmt.Sprintf("in %s%s: [%s] %s", repoFileShown, scope, e.Kind, search.SafeText(clipDecision(e.Text, 240)))
		tok := "prompt:rm:" + shortHash(line)
		if seen[tok] {
			continue
		}
		return line, tok
	}
	return "", ""
}

func promptErrorFixLine(dir, cwd, asked string, seen map[string]bool) (string, string) {
	if !planIndexReady(dir) || !promptCarriesErrorOrTest(asked) {
		return "", ""
	}
	var probes []string
	probes = append(probes, asked)
	for tok := range strings.FieldsSeq(asked) {
		tok = strings.Trim(tok, "\"'`(),:;!?[]{}<>")
		if strings.HasPrefix(tok, "Test") && len(tok) > 8 && hasMixedCaseOrUnderscore(tok) {
			probes = append(probes, "--- FAIL: "+tok, tok)
		}
	}
	for _, probe := range probes {
		line := fixPairLine(dir, cwd, probe)
		if line == "" || strings.Contains(line, "one session ") {
			continue
		}
		if idx := strings.Index(line, "changed next: "); idx >= 0 {
			editPart := strings.TrimSpace(line[idx+len("changed next: "):])
			pathOnly, rest := splitEditTargetAndDetail(editPart)
			if norm := orientPath(pathOnly, cwd); norm != "" {
				line = line[:idx+len("changed next: ")] + norm + rest
			}
		}
		tok := "prompt:fx:" + shortHash(line)
		if seen[tok] {
			return "", ""
		}
		return line, tok
	}
	return "", ""
}

func promptCarriesErrorOrTest(asked string) bool {
	low := strings.ToLower(asked)
	for _, marker := range []string{
		"--- fail:", "fail\t", "panic:", "fatal:", "level=error", "error:",
		"command not found:", "does not contain main module", "undefined:",
		"exit status ", "segmentation fault",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	for tok := range strings.FieldsSeq(asked) {
		tok = strings.Trim(tok, "\"'`(),:;!?[]{}<>")
		if strings.HasPrefix(tok, "Test") && len(tok) > 8 && hasMixedCaseOrUnderscore(tok) {
			return true
		}
	}
	return false
}

// reportsFailure reads the user's own words, line by line, using the same rule
// the index uses when it marks a session as reporting a dead end.
func reportsFailure(prompt string) bool {
	for line := range strings.SplitSeq(prompt, "\n") {
		if _, ok := index.GiveUpLine(line); ok {
			return true
		}
	}
	return false
}

// nudgeDue rate-limits the line the way the déjà vu line is rate-limited: a
// prompt hook is paid on every message, and advice repeated on every message
// is noise within a minute.
func nudgeDue(dir string) bool {
	p := dir + ".nudge"
	if b, err := os.ReadFile(p); err == nil {
		if ts, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && time.Since(time.Unix(ts, 0)) < nudgeGap {
			return false
		}
	}
	_ = atomicfile.Write(p, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
	return true
}

// emitNudgeOnly writes the nudge when there is no recall to carry it. It
// returns nil either way: this hook's miss contract is silence.
func emitNudgeOnly(stdout io.Writer, plain bool, nudge string) error {
	if nudge == "" {
		return nil
	}
	out := frameRecall(nudge)
	if plain {
		fmt.Fprintln(stdout, out)
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	resp.HookSpecificOutput.AdditionalContext = out
	writePromptResponse(stdout, resp)
	return nil
}

func splitEditTargetAndDetail(editPart string) (pathOnly, rest string) {
	cut := -1
	for _, sep := range []string{" (", " — "} {
		if idx := strings.Index(editPart, sep); idx >= 0 && (cut < 0 || idx < cut) {
			cut = idx
		}
	}
	if cut < 0 {
		return editPart, ""
	}
	return editPart[:cut], editPart[cut:]
}

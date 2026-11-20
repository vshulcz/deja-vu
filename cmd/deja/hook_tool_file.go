package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
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

func fileHookLineOutsideWithEdit(dir, cwd, path, editText string, self func(index.SessionMeta, string) bool) string {
	// FileSessions matches on the file's basename, so without scoping "main.go"
	// or "README.md" collects every project's file of that name — the line then
	// claims a history this file does not have and points `deja blame` at a
	// pile of other repos. Count only sessions in the project being worked in,
	// unless the stored path is the exact one (which cannot collide). Asked
	// for only when a session needs them: the names list the repository's
	// worktrees, which is a subprocess on every edit.
	projects := sync.OnceValue(func() []string {
		return resolveFileHookProjectNames(dir, cwd, path)
	})
	// A hook that fires unasked is the auto activation, so a session the trust
	// policy withholds must not even be counted here.
	pol := policy.Load()
	var sessions int
	var last time.Time
	var inScope []index.SessionMeta
	if planIndexReady(dir) {
		for _, meta := range index.FileSessions(dir, path) {
			if !pol.Allows(policy.ActivationAuto, meta.Project) {
				continue
			}
			if !fileMetaInScope(meta, path, nil) && !fileMetaInScope(meta, path, projects()) {
				continue
			}
			if !fileMetaMatchesRepoSubpath(meta, cwd, path) {
				continue
			}
			if self != nil && self(meta, path) {
				continue
			}
			sessions++
			inScope = append(inScope, meta)
			if meta.Updated.After(last) {
				last = meta.Updated
			}
		}
	}
	name := search.SafePath(baseName(path))
	editSymbols, fileSymbols := fileHookSymbols(cwd, path, editText)
	allSymbols := append(slices.Clone(editSymbols), fileSymbols...)
	repoDec := repoMemoryDecisionForFile(cwd, path, allSymbols, pol)
	changedSyms := changedDeclarationSymbols(cwd, path, editText)
	sigCallers := crossFileCallersLine(cwd, path, changedSyms)
	testSyms := append(slices.Clone(editSymbols), changedSyms...)
	if len(testSyms) == 0 && strings.TrimSpace(editText) != "" {
		testSyms = fileSymbols
	}
	siblingRan := siblingTestLineFor(cwd, path, testSyms)
	if sessions < toolHookMinFileSessionsWithDecision {
		var cold string
		if repoDec != "" {
			cold = name + repoDecisionLabel + repoDec
		} else if !isTestFilePath(path) && planIndexReady(dir) {
			var promoted string
			if sessions > 0 {
				promoted = promotedDecisionForFileIn(dir, path, inScope)
			}
			if promoted != "" && (mentionsFile(path, promoted) || mentionsAnySymbol(allSymbols, promoted)) {
				cold = name + standingLabel + promoted
			} else if rev, _ := fileReversalLineForWithTime(dir, cwd, path, editSymbols, fileSymbols, inScope, projects, pol, self); rev != "" {
				cold = name + revertedLabel + rev
			}
		}
		for _, tail := range []string{sigCallers, siblingRan} {
			if tail == "" {
				continue
			}
			if cold == "" {
				cold = name + tail
			} else if len(cold)+len(tail) <= toolHookMaxBytes {
				cold += tail
			}
		}
		return cold
	}
	when := ""
	if !last.IsZero() {
		when = ", last " + last.Local().Format("2006-01-02")
	}
	// The line-safe form: the name lands inside the hook's own sentence, and a
	// newline in it would end that sentence and start one that reads as deja
	// speaking to the agent (#1863).
	head := fmt.Sprintf("%s has been worked on in %s%s", name, toolSessionCount(sessions), when)
	// The line speaks only when what it carries is about this file: a decision
	// that names it, or a command two sessions here ran on it. Read from a month
	// of real Claude Code sessions, 114 file lines and not one the agent acted
	// on: no recall or blame that followed from one, no reply that used it. 56
	// of them were the same accepted note about the repository description,
	// reached through one long session that touched every file; the
	// session-start block already carries that note as a standing decision.
	// 37 were the closing sentence of
	// the newest session ("last session on it ended") and 8 the bare `deja
	// blame` pointer, which the A/B behind this hook already found changed
	// nothing. What the same A/B found an agent reuses is the decision about the
	// file, so that is what is left.
	var dec string
	promoted := promotedDecisionForFileIn(dir, path, inScope)
	switch {
	case promoted != "" && (mentionsFile(path, promoted) || mentionsAnySymbol(allSymbols, promoted)):
		dec = standingLabel + promoted
	case repoDec != "":
		dec = repoDecisionLabel + repoDec
	default:
		dec = selectFileHookDecisionClause(dir, cwd, path, promoted, editSymbols, fileSymbols, allSymbols, inScope, projects, pol, self)
	}
	ran := fileHookRanLineFor(dir, path, inScope)
	if ran == "" {
		ran = siblingRan
	}
	co := fileHookCoChangeLineIn(cwd, path, inScope)
	if sigCallers != "" {
		if co == "" {
			co = sigCallers
		} else if len(co)+len(sigCallers) <= 140 {
			co += sigCallers
		}
	}
	return assembleFileHookLine(head, dec, co, ran)
}

func resolveFileHookProjectNames(dir, cwd, path string) []string {
	var fileRepoRoot string
	if filepath.IsAbs(path) {
		fileRepoRoot = sources.RepoRoot(filepath.Dir(filepath.Clean(path)))
	}
	if fileRepoRoot != "" && safeScanRoot(cwd) == "" {
		return digest.ProjectNameCandidates(fileRepoRoot)
	}
	names := digest.ProjectNameCandidates(cwd)
	if filepath.IsAbs(path) && cwd != "" && safeScanRoot(cwd) != "" {
		cleanCwd := filepath.Clean(cwd)
		cleanPath := filepath.Clean(path)
		if strings.HasPrefix(cleanPath, cleanCwd+string(filepath.Separator)) {
			dirPath := filepath.Dir(cleanPath)
			for _, cand := range digest.ProjectNameCandidates(dirPath) {
				if !slices.Contains(names, cand) {
					names = append(names, cand)
				}
			}
			for cur := dirPath; strings.HasPrefix(cur, cleanCwd+string(filepath.Separator)); cur = filepath.Dir(cur) {
				if fileRepoRoot != "" && !strings.HasPrefix(cur, fileRepoRoot+string(filepath.Separator)) && cur != fileRepoRoot {
					break
				}
				base := filepath.Base(cur)
				parent := filepath.Base(filepath.Dir(cur))
				for _, cand := range []string{parent + "/" + base, base} {
					if cand != "" && !slices.Contains(names, cand) {
						names = append(names, cand)
					}
				}
			}
		}
	}
	if len(names) == 0 {
		if plain := sources.PlainProjectName(cwd); plain != "" {
			names = append(names, plain)
		}
		if path != "" {
			for _, cand := range commandProjectCandidatesIn(dir, "", path) {
				if !slices.Contains(names, cand) {
					names = append(names, cand)
				}
			}
		}
	}
	return names
}

func selectFileHookDecisionClause(
	dir, cwd, path, promoted string,
	editSymbols, fileSymbols, allSymbols []string,
	inScope []index.SessionMeta,
	projects func() []string,
	pol policy.Policy,
	self func(index.SessionMeta, string) bool,
) string {
	d, decAt := fileDecisionLineWithSymbolsAndTime(dir, path, editSymbols, fileSymbols, inScope)
	if d != "" && (!digest.CarriesDecision(d) || (!mentionsFile(path, d) && !mentionsAnySymbol(allSymbols, d))) {
		d = ""
	}
	rev, revAt := fileReversalLineForWithTime(dir, cwd, path, editSymbols, fileSymbols, inScope, projects, pol, self)
	preferRev := rev != "" && (d == "" ||
		(len(editSymbols) > 0 && mentionsAnySymbol(editSymbols, rev) && !mentionsAnySymbol(editSymbols, d)) ||
		(!revAt.IsZero() && (decAt.IsZero() || revAt.After(decAt))))
	switch {
	case preferRev:
		return revertedLabel + rev
	case d != "":
		// When the scanned decision sentence is the same root-cause explanation
		// mined for a named test breakage on this file, emit the breakage line so
		// the agent receives both the failing test name and the causal reason.
		if brk := fileBreakageLineFor(dir, cwd, path, editSymbols, inScope, pol); decisionDuplicatesBreakageWhy(d, brk) {
			return breakageLabel + brk
		}
		if promoted == d {
			return decisionLabel + d
		}
		return modelLabel(decisionLabel, d) + d
	default:
		if brk := fileBreakageLineFor(dir, cwd, path, editSymbols, inScope, pol); brk != "" {
			return breakageLabel + brk
		}
		return ""
	}
}

// turn0FileGuardrail returns the highest-priority verified guardrail clause for
// path (standing rule, repo memory decision, dead-end reversal, prior file
// decision, or verified test breakage) so Turn-0 localization and file briefs
// surface architectural constraints before planning begins.
func turn0FileGuardrail(dir, cwd, path string, inScope []index.SessionMeta, skip map[string]bool) string {
	if path == "" {
		return ""
	}
	pol := policy.Load()
	_, fileSymbols := fileHookSymbols(cwd, path, "")
	repoDec := repoMemoryDecisionForFile(cwd, path, fileSymbols, pol)
	projects := func() []string { return digest.ProjectNameCandidates(cwd) }
	self := func(m index.SessionMeta, _ string) bool {
		return skip != nil && skip[m.ID]
	}
	if len(inScope) < toolHookMinFileSessionsWithDecision {
		if repoDec != "" {
			return repoDecisionLabel + repoDec
		}
		if !isTestFilePath(path) {
			if rev, _ := fileReversalLineForWithTime(dir, cwd, path, nil, fileSymbols, inScope, projects, pol, self); rev != "" {
				return revertedLabel + rev
			}
		}
		return ""
	}
	promoted := promotedDecisionForFileIn(dir, path, inScope)
	if promoted != "" && (mentionsFile(path, promoted) || mentionsAnySymbol(fileSymbols, promoted)) {
		return standingLabel + promoted
	}
	if repoDec != "" {
		return repoDecisionLabel + repoDec
	}
	if isTestFilePath(path) {
		return ""
	}
	d, decAt := fileDecisionLineWithSymbolsAndTime(dir, path, nil, fileSymbols, inScope)
	if d != "" && (!digest.CarriesDecision(d) || (!mentionsFile(path, d) && !mentionsAnySymbol(fileSymbols, d))) {
		d = ""
	}
	rev, revAt := fileReversalLineForWithTime(dir, cwd, path, nil, fileSymbols, inScope, projects, pol, self)
	preferRev := rev != "" && (d == "" || (!revAt.IsZero() && (decAt.IsZero() || revAt.After(decAt))))
	if preferRev {
		return revertedLabel + rev
	}
	if d != "" {
		if brk := fileBreakageLineForSkipping(dir, cwd, path, nil, inScope, pol, skip); decisionDuplicatesBreakageWhy(d, brk) {
			return breakageLabel + brk
		} else if promoted == d {
			return decisionLabel + d
		}
		return modelLabel(decisionLabel, d) + d
	}
	if brk := fileBreakageLineForSkipping(dir, cwd, path, nil, inScope, pol, skip); brk != "" {
		return breakageLabel + brk
	}
	return ""
}

func assembleFileHookLine(head, dec, co, ran string) string {
	if dec != "" && (strings.Contains(dec, "requires updating ") || strings.Contains(dec, "edited alongside ")) {
		co = ""
	}
	if dec != "" && ran != "" && len(head)+len(dec)+len(co)+len(ran) > toolHookMaxBytes {
		for _, label := range []string{standingLabel, repoDecisionLabel, decisionLabel, modelOrderLabel, revertedLabel, breakageLabel} {
			if strings.HasPrefix(dec, label) {
				avail := toolHookMaxBytes - len(head) - len(co) - len(ran) - len(label)
				if avail < 90 && strings.HasPrefix(ran, " — test target: ") {
					ran = ""
					avail = toolHookMaxBytes - len(head) - len(co) - len(label)
				}
				if avail >= 90 {
					clipped := trimClippedDecision(search.SafeText(clipDecision(strings.TrimPrefix(dec, label), avail)), avail)
					if clipped != "" {
						dec = label + clipped
					}
				}
				break
			}
		}
	}
	var body string
	switch {
	case dec != "" && (strings.HasPrefix(dec, standingLabel) || strings.HasPrefix(dec, repoDecisionLabel)):
		body = dec
		if co != "" && len(head)+len(body)+len(co) <= toolHookMaxBytes {
			body += co
			co = ""
		}
		if ran != "" && len(head)+len(body)+len(ran) <= toolHookMaxBytes {
			body += ran
		}
	case dec != "" && strings.HasPrefix(ran, " — test target: "):
		// A static sibling-test target is synthetically reproducible at any time;
		// a file-specific prior decision from session history outranks it when
		// both do not fit.
		body = dec
		if ran != "" && len(head)+len(body)+len(ran) <= toolHookMaxBytes {
			body += ran
		}
	case ran != "":
		// A scanned conclusion does not outrank a verification command that two
		// sessions ran here and passed; when both fit, carry both.
		body = ran
		if dec != "" && len(head)+len(body)+len(dec) <= toolHookMaxBytes {
			body = dec + ran
		}
	default:
		body = dec
	}
	if body == "" {
		if co != "" {
			return head + co
		}
		return ""
	}
	out := head + body
	if co != "" && len(out)+len(co) <= toolHookMaxBytes {
		out += co
	}
	return out
}

const decisionLabel = " — prior decision: "
const revertedLabel = " — reverted approach here: "
const breakageLabel = " — past breakage here: "

// mentionsFile reports whether the text names the file or the directory it sits
// in. Stems count: a decision about `render.go` says "renderer", and one about
// `notes.jsonl` says "notes".
func mentionsFile(path, text string) bool {
	low := strings.ToLower(text)
	slashPath := strings.ToLower(strings.TrimPrefix(filepath.ToSlash(path), "./"))
	if slashPath != "" && strings.Contains(low, slashPath) {
		return true
	}
	fullBase := strings.ToLower(filepath.Base(path))
	if len(fullBase) >= 5 && strings.ContainsRune(fullBase, '.') && !hasGenericBasename(path) && mentionsUnqualifiedOrSameDirBase(path, fullBase, low) {
		return true
	}
	if hasGenericBasename(path) {
		return false
	}
	base := fullBase
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	hasSpecificStem := false
	for _, w := range parts {
		if len(w) >= 4 && !isGenericFileStemPart(w) {
			hasSpecificStem = true
			break
		}
	}
	if !hasSpecificStem {
		dirBase := strings.ToLower(filepath.Base(filepath.Dir(path)))
		if !isGenericDirPart(dirBase) {
			parts = append(parts, dirBase)
		}
	}
	for _, w := range parts {
		if len(w) < 4 || (hasSpecificStem && isGenericFileStemPart(w)) {
			continue
		}
		if strings.Contains(low, w) {
			return true
		}
		// The plural or the agent noun: notes -> note, render -> renderer is
		// already covered by the containment above.
		if strings.HasSuffix(w, "s") && len(w) >= 5 && strings.Contains(low, strings.TrimSuffix(w, "s")) {
			return true
		}
	}
	return false
}

// hasGenericBasename reports whether path has an ambiguous single-stem filename like main.go or index.html.
func hasGenericBasename(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	base = strings.Trim(base, "_")
	for _, suf := range []string{"_test", "_spec", ".test", ".spec", "-test", "-spec"} {
		base = strings.TrimSuffix(base, suf)
	}
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	if len(parts) >= 2 && len(base) >= 9 {
		return false
	}
	for _, w := range parts {
		if len(w) >= 4 && !isGenericFileStemPart(w) {
			return false
		}
	}
	return true
}

func mentionsUnqualifiedOrSameDirBase(path, fullBase, low string) bool {
	parent := strings.ToLower(filepath.Base(filepath.Dir(path)))
	offset := 0
	for {
		idx := strings.Index(low[offset:], fullBase)
		if idx < 0 {
			return false
		}
		pos := offset + idx
		if pos == 0 || low[pos-1] != '/' {
			return true
		}
		if parent != "" && parent != "." && strings.HasSuffix(low[:pos], "/"+parent+"/") {
			return true
		}
		if parent != "" && parent != "." && pos == len(parent)+1 && strings.HasPrefix(low, parent+"/") {
			return true
		}
		offset = pos + len(fullBase)
	}
}

func isGenericFileStemPart(w string) bool {
	switch w {
	case "file", "files", "tool", "tools", "test", "tests", "main", "util", "utils",
		"helper", "helpers", "common", "types", "model", "models", "index", "data",
		"base", "core", "init", "spec", "specs", "session", "sessions",
		"hook", "hooks", "install", "compaction", "command", "commands":
		return true
	}
	return false
}

func isGenericDirPart(w string) bool {
	switch w {
	case "deja", "cmd", "internal", "pkg", "src", "lib", "app", "docs", "guide",
		"test", "tests", "scripts", "assets", "web", "api", "cli", "server", "client":
		return true
	}
	return false
}

// standingLabel introduces a promoted note, and says what it is.
//
// A note is promoted by the user in a session, not against a file, so it is
// reached here through "a session that worked on this file said it" — which is
// not the same as "this was decided about this file". Read on a real store, the
// one accepted note on the machine arrived in front of `main.go` as "prior
// decision", where it is a rule about the repository description. The note is
// worth carrying and the ordering behind it was measured (#2495); what it must
// not do is claim to be about the file the agent is holding.
const standingLabel = " — standing decision in this project: "
const repoDecisionLabel = " — in " + repoFileShown + ": "

// repoMemoryDecisionForFile returns the highest-priority entry from the
// checkout's .deja/memory.jsonl that names this file or one of its symbols.
// Because repo memory is not linked to file-touch records in the index, matching
// requires the file's basename, its multi-segment directory path, or a declared
// symbol rather than a single parent folder name like "cmd" or "deja".
func repoMemoryDecisionForFile(cwd, path string, allSymbols []string, pol policy.Policy) string {
	probe := cwd
	if probe == "" && path != "" {
		if filepath.IsAbs(path) {
			probe = filepath.Dir(path)
		}
	}
	root := sources.RepoRoot(probe)
	if root == "" && filepath.IsAbs(path) {
		root = sources.RepoRoot(filepath.Dir(path))
	}
	if root == "" || !pol.Allows(policy.ActivationAuto, sources.RepoMemoryProject(root)) {
		return ""
	}
	entries, _, err := readRepoMemoryAndNotes(root)
	if err != nil || len(entries) == 0 {
		return ""
	}
	rank := map[string]int{"rule": 0, "rejected": 1, "gotcha": 2, "decision": 3}
	sort.SliceStable(entries, func(i, j int) bool {
		fi := repoEntryHasFile(root, path, entries[i].Files)
		fj := repoEntryHasFile(root, path, entries[j].Files)
		if fi != fj {
			return fi
		}
		if rank[entries[i].Kind] != rank[entries[j].Kind] {
			return rank[entries[i].Kind] < rank[entries[j].Kind]
		}
		if entries[i].Sessions != entries[j].Sessions {
			return entries[i].Sessions > entries[j].Sessions
		}
		return entries[i].Date > entries[j].Date
	})
	for _, e := range entries {
		matched := repoEntryHasFile(root, path, e.Files) ||
			(len(e.Files) == 0 && repoEntryMentionsFile(root, path, e.Text))
		if !matched && len(e.Files) == 0 {
			matched = mentionsSpecificSymbol(allSymbols, e.Text)
		}
		if matched {
			body := "[" + e.Kind + "] " + e.Text
			return trimTrailingFragment(search.SafeText(clipDecision(body, toolHookDecisionBudget)))
		}
	}
	return ""
}

func mentionsSpecificSymbol(symbols []string, text string) bool {
	if len(symbols) == 0 || text == "" {
		return false
	}
	var specific []string
	for _, sym := range symbols {
		s := strings.TrimSpace(sym)
		if len(s) >= 8 && hasMixedCaseOrUnderscore(s) {
			specific = append(specific, s)
		}
	}
	return mentionsAnySymbol(specific, text)
}

func repoEntryHasFile(root, path string, files []string) bool {
	if path == "" || len(files) == 0 {
		return false
	}
	slashPath := strings.TrimPrefix(filepath.ToSlash(path), "./")
	if root != "" && filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			slashPath = filepath.ToSlash(rel)
		}
	}
	base := filepath.Base(slashPath)
	for _, f := range files {
		f = strings.TrimPrefix(strings.ReplaceAll(filepath.ToSlash(f), "\\", "/"), "./")
		fHasDir := strings.ContainsRune(f, '/')
		if strings.EqualFold(f, slashPath) || (!fHasDir && !hasGenericBasename(slashPath) && len(base) >= 6 && strings.EqualFold(f, base)) ||
			strings.HasSuffix(slashPath, "/"+f) || strings.HasSuffix(f, "/"+slashPath) {
			return true
		}
	}
	return false
}

func repoEntryMentionsFile(root, path, text string) bool {
	if path == "" || text == "" {
		return false
	}
	low := strings.ToLower(text)
	slashPath := filepath.ToSlash(path)
	if root != "" && filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			slashPath = filepath.ToSlash(rel)
		}
	}
	base := strings.ToLower(filepath.Base(slashPath))
	if !hasGenericBasename(slashPath) && mentionsUnqualifiedOrSameDirBase(slashPath, base, low) {
		return true
	}
	if len(slashPath) >= 5 && strings.Contains(low, strings.ToLower(slashPath)) {
		return true
	}
	stem := base
	if i := strings.LastIndex(stem, "."); i > 0 {
		stem = stem[:i]
	}
	if len(stem) >= 6 && (strings.ContainsRune(stem, '_') || strings.ContainsRune(stem, '-')) && containsIdentifierWord(low, stem) {
		return true
	}
	return false
}

// fileDecisionLineWithSymbols returns the single most relevant prior decision recorded
// about this file, or "" if none can be extracted. It reads the newest in-scope
// sessions (bounded, newest first) and takes the first that yields a conclusion
// and was not later taken back — the same decision-carrying line the SessionStart
// digest surfaces, but delivered at the moment the file is about to change.
func fileDecisionLineWithSymbolsAndTime(dir, path string, editSymbols, fileSymbols []string, metas []index.SessionMeta) (string, time.Time) {
	sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	// A promoted note first. It is the user's own statement about this work,
	// which is why the session-start block leads with it and calls it standing;
	// scanning for a conclusion instead handed back whatever the newest session
	// happened to end with — "looked at retry.go again (5)" in front of "the
	// retry budget stays at 5" (#2495).
	if line := promotedDecisionForFileIn(dir, path, metas); line != "" {
		return line, time.Now()
	}
	r := newLifecycleResolver(dir)
	var fallback string
	var fallbackAt time.Time
	for i, meta := range metas {
		if i >= toolHookDecisionScan {
			break
		}
		if !decisionUsable(metaSession(meta), r) {
			continue
		}
		// Bound the work inside a hook the agent is blocked on: a marathon
		// session's whole history is not worth scanning for one line, and a
		// decision states itself near the end anyway.
		s, ok, err := index.FindTailByIdentity(dir, meta.Harness, meta.ID, toolHookMsgTail)
		if err != nil || !ok || !decisionUsable(s, r) {
			continue
		}
		if path == "" {
			if cs := digest.Conclusions(s, toolHookDecisionBudget, 1); len(cs) > 0 {
				return trimTrailingFragment(search.SafeText(strings.TrimSpace(cs[0]))), meta.Updated
			}
			continue
		}
		for _, c := range digest.Conclusions(s, toolHookDecisionBudget*4, 6) {
			line := trimTrailingFragment(search.SafeText(strings.TrimSpace(c)))
			if line == "" || !digest.CarriesDecision(line) {
				continue
			}
			if len([]rune(line)) >= 45 && !usableRepoCandidateLine(line) {
				continue
			}
			if isEphemeralCompletionReport(line) {
				continue
			}
			if len(editSymbols) > 0 && mentionsAnySymbol(editSymbols, line) {
				return line, meta.Updated
			}
			if fallback == "" && (mentionsFile(path, line) || mentionsAnySymbol(fileSymbols, line)) {
				fallback = line
				fallbackAt = meta.Updated
				if len(editSymbols) == 0 {
					return line, meta.Updated
				}
			}
		}
	}
	if fallback != "" || path == "" {
		return fallback, fallbackAt
	}
	return fileEpisodeDecisionLine(dir, path, editSymbols, fileSymbols, metas, r)
}

// fileEpisodeDecisionLine looks up recent spoken assistant turns that directly
// mention this file's stem or specific symbols via the bucket posting list.
// In long multi-file sessions, a decision about an edited file sits at the
// episode where the file was changed rather than in the session's final tail.
func fileEpisodeDecisionLine(dir, path string, editSymbols, fileSymbols []string, metas []index.SessionMeta, r *lifecycleResolver) (string, time.Time) {
	allowedKeys := make(map[string]bool, len(metas))
	metaByKey := make(map[string]time.Time, len(metas))
	for _, meta := range metas {
		if decisionUsable(metaSession(meta), r) {
			k := meta.Key()
			allowedKeys[k] = true
			metaByKey[k] = meta.Updated
		}
	}
	if len(allowedKeys) == 0 {
		return "", time.Time{}
	}
	var lookupTerms []string
	seenTerm := map[string]bool{}
	addTerm := func(s string) {
		low := strings.ToLower(strings.TrimSpace(s))
		if len(low) < 4 || seenTerm[low] || isGenericFileStemPart(low) || isCommonKeyword(low) {
			return
		}
		seenTerm[low] = true
		lookupTerms = append(lookupTerms, low)
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	addTerm(stem)
	for _, part := range strings.FieldsFunc(stem, func(ch rune) bool { return ch == '_' || ch == '-' || ch == '.' }) {
		addTerm(part)
	}
	for _, sym := range editSymbols {
		if len(sym) >= 6 && hasMixedCaseOrUnderscore(sym) {
			addTerm(sym)
			if len(lookupTerms) >= 5 {
				break
			}
		}
	}
	if len(lookupTerms) == 0 {
		return "", time.Time{}
	}
	turns, err := index.RecentSpokenAssistantTurnsForTerms(dir, lookupTerms, allowedKeys, 32)
	if err != nil || len(turns) == 0 {
		return "", time.Time{}
	}
	var fallback string
	var fallbackAt time.Time
	for _, turn := range turns {
		if !mentionsFile(path, turn.Text) && !mentionsSpecificSymbol(fileSymbols, turn.Text) && !mentionsAnySymbol(editSymbols, turn.Text) {
			continue
		}
		if line := extractEpisodeDecisionFromTurn(turn.Text, path, editSymbols, fileSymbols); line != "" {
			at := turn.Time
			if at.IsZero() {
				at = metaByKey[turn.Key]
			}
			if len(editSymbols) > 0 && mentionsAnySymbol(editSymbols, line) {
				return line, at
			}
			if fallback == "" {
				fallback = line
				fallbackAt = at
				if len(editSymbols) == 0 {
					return fallback, fallbackAt
				}
			}
		}
	}
	return fallback, fallbackAt
}

func extractEpisodeDecisionFromTurn(turnText, path string, editSymbols, fileSymbols []string) string {
	var fallback string
	s := model.Session{Messages: []model.Message{{Role: "assistant", Text: turnText}}}
	candidates := digest.Conclusions(s, toolHookDecisionBudget*4, 6)
	for rawLine := range strings.SplitSeq(turnText, "\n") {
		candidates = append(candidates, rawLine)
	}
	for _, c := range candidates {
		line := cleanRepoCandidateLine(strings.TrimSpace(c))
		line = trimTrailingFragment(search.SafeText(strings.TrimSpace(line)))
		if line == "" || len([]rune(line)) < 35 || !digest.CarriesDecision(line) {
			continue
		}
		if len([]rune(line)) >= 45 && !usableRepoCandidateLine(line) {
			continue
		}
		if isEphemeralCompletionReport(line) {
			continue
		}
		if len(editSymbols) > 0 && mentionsAnySymbol(editSymbols, line) {
			return trimTrailingFragment(search.SafeText(clipDecision(line, toolHookDecisionBudget)))
		}
		if fallback == "" && (mentionsFile(path, line) || mentionsSpecificSymbol(fileSymbols, line)) {
			fallback = trimTrailingFragment(search.SafeText(clipDecision(line, toolHookDecisionBudget)))
		}
	}
	return fallback
}

func isEphemeralCompletionReport(s string) bool {
	trimmed := strings.TrimSpace(s)
	low := strings.ToLower(trimmed)
	for _, p := range []string{
		"i fixed the ", "i fixed all ", "i fixed every ", "i fixed ",
		"i added a ", "i updated ", "i removed ", "tests: ",
		"the first ci run failed", "current audit:", "task: ",
		"the fix is in:", "заодно поправил ", "я исправил ",
		"the command failed because gemini cli did not clone",
		"perfect! i've ", "perfect! i have ", "excellent! i've ", "excellent! i have ",
		"great! i've ", "great! i have ", "i've successfully ", "i have successfully ",
		"i successfully ", "here's what ", "here is what ",
		"let me create a summary", "let me summarize",
		"i have fixed ", "successfully fixed ", "all tests pass",
		"`#", "#",
	} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	for _, sub := range []string{
		"ci checks pass", "non-windows checks pass", "pr is open",
		"in worktree `.claude/worktrees/", "in worktree .claude/worktrees/",
		"fix/hunt-", "ci is green, not merged", "(merged)", "(closed)",
	} {
		if strings.Contains(low, sub) {
			return true
		}
	}
	return lacksProseWordsOutsideCode(trimmed)
}

func lacksProseWordsOutsideCode(s string) bool {
	var out strings.Builder
	inTick := false
	for _, r := range s {
		if r == '`' {
			inTick = !inTick
			out.WriteByte(' ')
			continue
		}
		if !inTick {
			out.WriteRune(r)
		}
	}
	proseWords := 0
	for w := range strings.FieldsSeq(out.String()) {
		w = strings.Trim(w, ".,:;!?()[]{}\"'—-")
		if len([]rune(w)) >= 2 {
			hasLetter := false
			for _, r := range w {
				if unicode.IsLetter(r) {
					hasLetter = true
					break
				}
			}
			if hasLetter {
				proseWords++
			}
		}
	}
	return proseWords < 3
}

// fileReversalLineFor extracts a concrete reverted-approach warning for path
// from in-scope sessions marked GaveUp. Unlike positive decisions (which
// decisionUsable drops on GaveUp so a reverted choice is never presented as
// standing), a line that explicitly names this file or one of its specific
// symbols and states why an approach was backed out warns the agent against
// repeating the dead end.
func fileReversalLineForWithTime(
	dir, cwd, path string,
	editSymbols, fileSymbols []string,
	metas []index.SessionMeta,
	projects func() []string,
	pol policy.Policy,
	self func(index.SessionMeta, string) bool,
) (string, time.Time) {
	if path == "" {
		return "", time.Time{}
	}
	sorted := slices.Clone(metas)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Updated.After(sorted[j].Updated) })
	var r *lifecycleResolver
	resolver := func() *lifecycleResolver {
		if r == nil {
			r = newLifecycleResolver(dir)
		}
		return r
	}
	gaveKeys := map[string]bool{}
	touchedKey := map[string]bool{}
	metaAt := map[string]time.Time{}
	for _, meta := range sorted {
		if !meta.GaveUp {
			continue
		}
		if state, _, _, _, ok := resolver().resolve(metaSession(meta)); ok && state == model.LifecycleAccepted {
			continue
		}
		k := meta.Key()
		gaveKeys[k] = true
		touchedKey[k] = true
		metaAt[k] = meta.Updated
	}
	collectUntouchedProjectGiveUpKeys(dir, path, projects, pol, self, resolver, gaveKeys, metaAt)
	if len(gaveKeys) == 0 {
		return "", time.Time{}
	}
	turns, err := index.GiveUpRecordsForKeys(dir, gaveKeys)
	if err != nil || len(turns) == 0 {
		return "", time.Time{}
	}
	// Order records newest-session-first, and within a session newest-turn-first.
	slices.SortStableFunc(turns, func(a, b index.Record) int {
		at, bt := a.Time, b.Time
		if at.IsZero() {
			at = metaAt[a.Key]
		}
		if bt.IsZero() {
			bt = metaAt[b.Key]
		}
		return bt.Compare(at)
	})
	var fallback string
	var fallbackAt time.Time
	for _, turn := range turns {
		for _, l := range index.GiveUpExcerpts(turn.Text) {
			clean := cleanRepoCandidateLine(l)
			if clean == "" || len([]rune(clean)) < 35 || !usableReversalLine(clean) || isEphemeralCompletionReport(clean) || !hasReversalRationale(clean) {
				continue
			}
			if touchedKey[turn.Key] {
				if !mentionsFileStrictForReversal(path, clean) && !mentionsSpecificSymbol(fileSymbols, clean) && !mentionsSpecificSymbol(editSymbols, clean) {
					continue
				}
			} else if !mentionsExplicitFilePathForReversal(cwd, path, clean) && !mentionsDistinctiveSymbolForReversal(fileSymbols, clean) && !mentionsDistinctiveSymbolForReversal(editSymbols, clean) {
				continue
			}
			clipped := clipReversalDecision(clean, toolHookDecisionBudget)
			if clipped == "" {
				continue
			}
			at := turn.Time
			if at.IsZero() {
				at = metaAt[turn.Key]
			}
			if len(editSymbols) > 0 && mentionsAnySymbol(editSymbols, clean) {
				return clipped, at
			}
			if fallback == "" {
				fallback = clipped
				fallbackAt = at
			}
		}
	}
	return fallback, fallbackAt
}

func mentionsExplicitFilePathForReversal(cwd, path, text string) bool {
	low := strings.ToLower(text)
	slashPath := strings.TrimPrefix(filepath.ToSlash(path), "./")
	if cwd != "" && filepath.IsAbs(path) {
		if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
			slashPath = filepath.ToSlash(rel)
		}
	}
	lowRel := strings.ToLower(slashPath)
	if strings.ContainsRune(lowRel, '/') && strings.Contains(low, lowRel) {
		return true
	}
	fullBase := strings.ToLower(filepath.Base(slashPath))
	if len(fullBase) >= 10 && strings.ContainsRune(fullBase, '.') && !strings.HasSuffix(fullBase, "_test.go") {
		if strings.Contains(low, fullBase) && !strings.Contains(low, fullBase+":") {
			return true
		}
	}
	return false
}

func collectUntouchedProjectGiveUpKeys(
	dir, path string,
	projects func() []string,
	pol policy.Policy,
	self func(index.SessionMeta, string) bool,
	resolver func() *lifecycleResolver,
	gaveKeys map[string]bool,
	metaAt map[string]time.Time,
) {
	if projects == nil {
		return
	}
	names := projects()
	if len(names) == 0 {
		return
	}
	allMetas, err := index.AllMeta(dir)
	if err != nil {
		return
	}
	for _, meta := range allMetas {
		if !meta.GaveUp || len(meta.Touched) > 0 {
			continue
		}
		if !pol.Allows(policy.ActivationAuto, meta.Project) || !fileMetaInScope(meta, path, names) {
			continue
		}
		if self != nil && self(meta, path) {
			continue
		}
		if state, _, _, _, ok := resolver().resolve(metaSession(meta)); ok && state == model.LifecycleAccepted {
			continue
		}
		k := meta.Key()
		if !gaveKeys[k] {
			gaveKeys[k] = true
			metaAt[k] = meta.Updated
		}
	}
}

func mentionsDistinctiveSymbolForReversal(symbols []string, text string) bool {
	if len(symbols) == 0 || text == "" {
		return false
	}
	var distinctive []string
	for _, sym := range symbols {
		s := strings.TrimSpace(sym)
		if len(s) >= 16 && hasMixedCaseOrUnderscore(s) {
			distinctive = append(distinctive, s)
		}
	}
	return mentionsAnySymbol(distinctive, text)
}

func clipReversalDecision(clean string, budget int) string {
	trimmed := strings.TrimSpace(clean)
	for _, pfx := range []string{"Confirmed: ", "confirmed: "} {
		if strings.HasPrefix(trimmed, pfx) {
			trimmed = strings.TrimSpace(trimmed[len(pfx):])
			break
		}
	}
	clipped := trimTrailingFragment(search.SafeText(clipDecision(trimmed, budget)))
	if clipped == "" {
		return ""
	}
	if _, ok := index.GiveUpLine(clipped); ok {
		return clipped
	}
	// Right-side truncation cut off the reversal clause in the second sentence;
	// keep the tail up to budget starting at a clean word/clause boundary.
	if len(trimmed) > budget {
		start := len(trimmed) - budget
		for start < len(trimmed) && !utf8.ValidString(trimmed[start:]) {
			start++
		}
		if sp := strings.IndexAny(trimmed[start:], " ;—"); sp > 0 && start+sp+1 < len(trimmed) {
			tail := strings.TrimLeft(trimmed[start+sp+1:], " —;-")
			if cand := trimTrailingFragment(search.SafeText(tail)); cand != "" {
				if _, ok := index.GiveUpLine(cand); ok {
					return cand
				}
			}
		}
	}
	return clipped
}

// mentionsFileStrictForReversal matches explicit basename or non-generic file
// stem tokens (>=5 chars) without falling back to the parent directory name,
// so a rollback remark about a package ("search", "index") never fans out onto
// unrelated sibling files in that package.
func mentionsFileStrictForReversal(path, text string) bool {
	low := strings.ToLower(text)
	fullBase := strings.ToLower(filepath.Base(path))
	if len(fullBase) >= 5 && strings.ContainsRune(fullBase, '.') && strings.Contains(low, fullBase) {
		return true
	}
	if hasGenericBasename(path) {
		return false
	}
	base := fullBase
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	if len(base) >= 5 && !isGenericFileStemPart(base) && containsIdentifierWord(low, base) {
		return true
	}
	for _, w := range strings.FieldsFunc(base, func(r rune) bool { return r == '_' || r == '-' || r == '.' }) {
		if len(w) < 5 || isGenericFileStemPart(w) {
			continue
		}
		if containsIdentifierWord(low, w) {
			return true
		}
		if strings.HasSuffix(w, "s") && len(w) >= 6 && containsIdentifierWord(low, strings.TrimSuffix(w, "s")) {
			return true
		}
	}
	return false
}

func usableReversalLine(line string) bool {
	low := strings.ToLower(strings.TrimSpace(line))
	for _, metaNoise := range []string{
		"hunk reverted",
		"fix reverted",
		"fix is reverted",
		"reverting the fix",
		"reverting the line",
		"reverted copy",
		"reverted tree",
		"reverted to commit",
		"reverted probes",
		"scratch copy",
		"scratchpad",
		"verified: with ",
		"verified with ",
		"would still be",
		"telling the agent",
		"tell the agent",
		"test is sound",
		"fail-before confirmed",
		"necessity confirmed",
		"expected-accept",
		"expected-reject",
		"notreports",
		"reverted_approach",
		"revertedlabel",
		"giveupline",
		"gaveup",
		"giveup.go:",
		"in session ",
	} {
		if strings.Contains(low, metaNoise) {
			return false
		}
	}
	// usableRepoCandidateLine drops bare status lines starting with "откатил ",
	// "откатил:", "откатил." for repo-wide standing rule mining. For dead-end
	// warnings, a first-person Russian rollback ("Откатил X, потому что Y") is
	// valid when hasReversalRationale confirms causal substance; validate the
	// body past that leading verb with usableRepoCandidateLine.
	check := line
	for _, pfx := range []string{"откатил ", "откатил:"} {
		if strings.HasPrefix(low, pfx) {
			check = "Мы " + line
			break
		}
	}
	return usableRepoCandidateLine(check)
}

// hasReversalRationale requires explicit causal or empirical outcome substance
// explaining why the approach was backed out, blocking bare git/status chatter
// ("reverted foo.go to origin/main", "откатил изменения в файле").
func hasReversalRationale(line string) bool {
	low := strings.ToLower(line)
	for _, marker := range []string{
		"because ", "since ", "as it ", "which ", "broke", "break", "deadlock",
		"corrupt", "worse", "regress", "contract", "cycle", "cyclic", "slower",
		"latency", "timeout", "race", "panic", "leak", "shadow", "cut ", "cuts ",
		"instead", "rather than", "fail", "wrong", "noise", "bloat", "duplicate",
		"torn read", "data loss",
		"потому что", "так как", "из-за", "поскольку", "лома", "слома",
		"хуже", "падает", "уронил", "просел", "не изменился", "не помогло",
		"замолкает", "не отдаются", "цикл", "привязало", "не срабатыва",
		"дублир", "шум", "гонк", "дедлок", "зависа", "контракт", "таймаут",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

// fileBreakageLineFor mines FixPair.Edit entries for path in in-scope sessions
// to surface named tests or panics that previously broke here and required
// editing this file to repair.
func fileBreakageLineFor(dir, cwd, path string, editSymbols []string, metas []index.SessionMeta, pol policy.Policy) string {
	return fileBreakageLineForSkipping(dir, cwd, path, editSymbols, metas, pol, nil)
}

func fileBreakageLineForSkipping(dir, cwd, path string, editSymbols []string, metas []index.SessionMeta, pol policy.Policy, skip map[string]bool) string {
	if path == "" || len(metas) == 0 || isTestFilePath(path) {
		return ""
	}
	if dir == "" {
		dir = index.DefaultDir()
	}
	inScopeKeys := make(map[string]bool, len(metas))
	for _, meta := range metas {
		inScopeKeys[meta.Key()] = true
	}
	root := sources.RepoRoot(cwd)
	if root == "" {
		root = cwd
	}
	pairs := index.ReadFixes(dir)
	if len(pairs) == 0 {
		return ""
	}
	var brokenTests []string
	seenTest := map[string]bool{}
	var bestTest, bestSym, bestWhy string
	bestRank := 0
	var panicLine, panicSym, panicWhy string
	here := hookProjectMatcher(cwd)
	for _, p := range pairs {
		if p.Edit == "" || p.Error == "" {
			continue
		}
		if skip != nil {
			if _, sid, ok := strings.Cut(p.Key, ":"); ok && skip[sid] {
				continue
			}
			if skip[p.Key] {
				continue
			}
		}
		if !inScopeKeys[p.Key] {
			if p.Project == "" || !pol.Allows(policy.ActivationAuto, p.Project) || !here(p.Project) {
				continue
			}
		}
		if !repoEntryHasFile(root, path, []string{p.Edit}) {
			continue
		}
		if pol.Ignored(p.Edit, p.Project) {
			continue
		}
		if tn := extractBrokenTestName(p.Error); tn != "" {
			if !seenTest[tn] {
				seenTest[tn] = true
				brokenTests = append(brokenTests, tn)
			}
			rank := breakageContextRank(p, editSymbols)
			if rank > bestRank {
				bestRank = rank
				bestTest = tn
				bestSym = p.Symbol
				bestWhy = p.Why
			}
			continue
		}
		if isSpecificBreakageError(p.Error) {
			if panicLine == "" || (panicWhy == "" && p.Why != "") {
				panicLine = truncateToolLine(search.SafeLine(strings.TrimSpace(p.Error)), 120)
				panicSym = p.Symbol
				panicWhy = p.Why
			}
		}
	}
	if len(brokenTests) > 0 {
		if bestWhy != "" {
			label := bestTest
			if label == "" {
				label = brokenTests[0]
			}
			if len(brokenTests) > 1 {
				label += fmt.Sprintf(" (+%d more)", len(brokenTests)-1)
			}
			why := search.SafeLine(bestWhy)
			sym := search.SafeLine(bestSym)
			if sym != "" && !whyMentionsSymbol(why, sym) {
				return truncateToolLine(label+" broke in "+sym+" — "+why, toolHookDecisionBudget)
			}
			return truncateToolLine(label+" broke here — "+why, toolHookDecisionBudget)
		}
		limit := min(len(brokenTests), 3)
		shown := strings.Join(brokenTests[:limit], ", ")
		if len(brokenTests) > limit {
			shown += fmt.Sprintf(" (+%d more)", len(brokenTests)-limit)
		}
		if bestSym != "" {
			return trimClippedDecision(shown+" failed and required editing "+search.SafeLine(bestSym)+" in this file", toolHookDecisionBudget)
		}
		return shown + " failed and required editing this file"
	}
	if panicLine != "" {
		if panicWhy != "" {
			return truncateToolLine(truncateToolLine(panicLine, 80)+" — "+search.SafeLine(panicWhy), toolHookDecisionBudget)
		}
		if panicSym != "" {
			return truncateToolLine(panicLine+" (in "+search.SafeLine(panicSym)+")", toolHookDecisionBudget)
		}
	}
	return panicLine
}

func breakageContextRank(p index.FixPair, editSymbols []string) int {
	if p.Symbol == "" && p.Why == "" {
		return 0
	}
	rank := 1
	if p.Why != "" {
		rank = 3
		if !p.Candidate {
			rank = 4
		}
	} else if !p.Candidate {
		rank = 2
	}
	if p.Symbol != "" && len(editSymbols) > 0 && mentionsAnySymbol(editSymbols, p.Symbol) {
		rank += 4
	}
	return rank
}

func whyMentionsSymbol(why, sym string) bool {
	short := sym
	if _, m, ok := strings.Cut(sym, "."); ok {
		short = m
	}
	return short != "" && strings.Contains(strings.ToLower(why), strings.ToLower(short))
}

func decisionDuplicatesBreakageWhy(decision, brk string) bool {
	_, why, ok := strings.Cut(brk, " — ")
	if !ok || len(why) < 25 {
		return false
	}
	normDec := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(decision), "…"))
	normWhy := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(why), "…"))
	for _, prefix := range []string{"root cause:", "cause:", "why:", "bug:"} {
		normDec = strings.TrimSpace(strings.TrimPrefix(normDec, prefix))
	}
	return normWhy != "" && (strings.Contains(normDec, normWhy) || strings.Contains(normWhy, normDec))
}

func extractBrokenTestName(errLine string) string {
	s := strings.TrimSpace(errLine)
	for _, prefix := range []string{"--- FAIL: ", "FAIL: "} {
		if rest, ok := strings.CutPrefix(s, prefix); ok {
			rest = strings.TrimSpace(rest)
			name := leadingIdent(rest)
			if len(name) >= 6 && (strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "test_")) {
				return name
			}
		}
	}
	return ""
}

func isSpecificBreakageError(errLine string) bool {
	s := strings.TrimSpace(errLine)
	low := strings.ToLower(s)
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") || strings.Contains(low, "timed out") || strings.Contains(low, "[build failed]") {
		return false
	}
	return strings.HasPrefix(low, "panic: ") || strings.HasPrefix(low, "fatal error: ")
}

// fileHookSymbols extracts candidate symbol names from the edit text and from
// top-level declarations in the target file on disk (when readable).
func fileHookSymbols(cwd, path, editText string) ([]string, []string) {
	var editSyms []string
	seenEdit := map[string]bool{}
	addEdit := func(sym string) {
		low := strings.ToLower(sym)
		if len(low) < 4 || seenEdit[low] || isCommonKeyword(low) {
			return
		}
		seenEdit[low] = true
		editSyms = append(editSyms, sym)
	}
	for _, sym := range declaredSymbolsInText(editText) {
		addEdit(sym)
	}
	for _, tok := range identifierTokens(editText) {
		if hasMixedCaseOrUnderscore(tok) {
			addEdit(tok)
		}
	}

	diskPath := path
	if diskPath != "" && !filepath.IsAbs(diskPath) && cwd != "" {
		diskPath = filepath.Join(cwd, diskPath)
	}
	var fileSyms []string
	seenFile := map[string]bool{}
	if diskPath != "" && !policy.SourceHintsOff() {
		if info, err := os.Stat(diskPath); err == nil && !info.IsDir() && info.Size() <= 2<<20 {
			if raw, err := os.ReadFile(diskPath); err == nil {
				content := string(raw)
				for _, sym := range declaredSymbolsInText(content) {
					low := strings.ToLower(sym)
					if len(low) >= 4 && !seenFile[low] && !isCommonKeyword(low) {
						seenFile[low] = true
						fileSyms = append(fileSyms, sym)
					}
				}
				if editText != "" {
					if enc := enclosingSymbolForSnippet(content, editText); enc != "" {
						addEdit(enc)
					}
				}
			}
		}
	}
	return editSyms, fileSyms
}

var declKeywords = []string{
	"func ", "type ", "async def ", "def ", "class ",
	"export default function ", "export async function ", "export function ",
	"export class ", "export interface ", "export type ",
	"async function ", "function ", "interface ",
	"pub async fn ", "pub fn ", "async fn ", "fn ",
	"pub struct ", "struct ", "pub enum ", "enum ", "pub trait ", "trait ",
}

func startsWithDeclKeyword(s string) bool {
	for _, kw := range declKeywords {
		if strings.HasPrefix(s, kw) {
			return true
		}
	}
	return false
}

func isDiffMetaLine(s string) bool {
	return strings.HasPrefix(s, "+++") || strings.HasPrefix(s, "---") || strings.HasPrefix(s, "@@")
}

func stripSingleDiffPrefix(line string) string {
	if isDiffMetaLine(line) {
		return ""
	}
	if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "++") {
		return line[1:]
	}
	if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--") {
		return line[1:]
	}
	return line
}

func stripInlineDeclComment(s string) string {
	var quote byte
	escaped := false
	for i := range len(s) {
		c := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' && quote != '`' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '/' && i+1 < len(s) && s[i+1] == '/' {
			return strings.TrimSpace(s[:i])
		}
		if c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

func parenDelta(s string) int {
	delta := 0
	var quote byte
	escaped := false
	for i := range len(s) {
		c := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' && quote != '`' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '/' && i+1 < len(s) && s[i+1] == '/' {
			break
		}
		if c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			break
		}
		switch c {
		case '(':
			delta++
		case ')':
			delta--
		}
	}
	return delta
}

func declHeaderTerminated(s string) bool {
	return strings.HasSuffix(s, "{") ||
		strings.HasSuffix(s, ":") ||
		strings.HasSuffix(s, ";") ||
		strings.HasSuffix(s, "=>")
}

func declReturnStarts(s string) bool {
	return strings.HasPrefix(s, "->") ||
		strings.HasPrefix(s, ":") ||
		strings.HasPrefix(s, "where ") ||
		strings.HasPrefix(s, "{") ||
		strings.HasPrefix(s, "=>")
}

func normalizeCollapsedDecl(s string) string {
	joined := strings.Join(strings.Fields(s), " ")
	joined = strings.ReplaceAll(joined, "( ", "(")
	joined = strings.ReplaceAll(joined, " )", ")")
	joined = strings.ReplaceAll(joined, ",)", ")")
	return joined
}

func collapseDeclarationWindow(lines []string, startIdx int) (string, int) {
	return collapseParenWindow(lines, startIdx, true, true)
}

func collapseMemberLineWindow(lines []string, startIdx int) (string, int) {
	return collapseParenWindow(lines, startIdx, false, false)
}

func collapseParenWindow(lines []string, startIdx int, requireKeyword, stripDiff bool) (string, int) {
	if startIdx < 0 || startIdx >= len(lines) {
		return "", startIdx
	}
	rawFirst := lines[startIdx]
	if stripDiff && isDiffMetaLine(rawFirst) {
		return "", startIdx
	}
	if stripDiff {
		rawFirst = stripSingleDiffPrefix(rawFirst)
	}
	first := stripInlineDeclComment(strings.TrimSpace(rawFirst))
	if first == "" {
		return "", startIdx
	}
	balance := parenDelta(first)
	if (requireKeyword && !startsWithDeclKeyword(first)) || balance <= 0 {
		return first, startIdx
	}
	var b strings.Builder
	b.WriteString(first)
	endIdx := startIdx
	limit := min(len(lines)-1, startIdx+8)
	for j := startIdx + 1; j <= limit; j++ {
		rawLine := lines[j]
		if stripDiff && isDiffMetaLine(rawLine) {
			break
		}
		if stripDiff {
			rawLine = stripSingleDiffPrefix(rawLine)
		}
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			break
		}
		cleaned := stripInlineDeclComment(trimmed)
		if cleaned == "" {
			endIdx = j
			continue
		}
		if balance <= 0 {
			if declHeaderTerminated(strings.TrimSpace(b.String())) || !declReturnStarts(cleaned) {
				break
			}
		}
		b.WriteByte(' ')
		b.WriteString(cleaned)
		balance += parenDelta(cleaned)
		endIdx = j
		if balance <= 0 && declHeaderTerminated(strings.TrimSpace(b.String())) {
			break
		}
	}
	return normalizeCollapsedDecl(b.String()), endIdx
}

func cleanMemberSig(sig string) string {
	if idx := declBodyBraceIndex(sig); idx > 0 {
		sig = strings.TrimSpace(sig[:idx])
	}
	return clipDeclSignature(normalizeCollapsedDecl(sig))
}

const maxDeclSigRunes = 180

func clipDeclSignature(sig string) string {
	if len(sig) <= maxDeclSigRunes {
		return sig
	}
	runes := []rune(sig)
	if len(runes) <= maxDeclSigRunes {
		return sig
	}
	return string(runes[:maxDeclSigRunes-3]) + "..."
}

func collectDeclaredSigs(text string) ([]string, map[string]string, map[string]string) {
	if text == "" {
		return nil, nil, nil
	}
	var order []string
	firstSigs := map[string]string{}
	lastSigs := map[string]string{}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		collapsed, endIdx := collapseDeclarationWindow(lines, i)
		if id, sig := parseDeclarationLine(collapsed); id != "" && sig != "" {
			if _, exists := lastSigs[id]; !exists {
				order = append(order, id)
				firstSigs[id] = sig
			}
			lastSigs[id] = sig
			i = endIdx
		}
	}
	return order, firstSigs, lastSigs
}

func splitDiffViews(text string) (string, string, bool) {
	lines := strings.Split(text, "\n")
	hasDiff := false
	beforeLines := make([]string, 0, len(lines))
	afterLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if isDiffMetaLine(line) {
			hasDiff = true
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "++") {
			hasDiff = true
			afterLines = append(afterLines, line[1:])
			continue
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--") {
			hasDiff = true
			beforeLines = append(beforeLines, line[1:])
			continue
		}
		beforeLines = append(beforeLines, line)
		afterLines = append(afterLines, line)
	}
	if !hasDiff {
		return text, text, false
	}
	return strings.Join(beforeLines, "\n"), strings.Join(afterLines, "\n"), true
}

func declaredSymbolsInText(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		collapsed, endIdx := collapseDeclarationWindow(lines, i)
		if id, _ := parseDeclarationLine(collapsed); id != "" {
			out = append(out, id)
			i = endIdx
		}
	}
	return out
}

func declBodyBraceIndex(sig string) int {
	parenDepth := 0
	var quote byte
	escaped := false
	for i := range len(sig) {
		c := sig[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' && quote != '`' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		switch c {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '{':
			if parenDepth == 0 {
				return i
			}
		}
	}
	return -1
}

func parseDeclarationLine(line string) (string, string) {
	if isDiffMetaLine(line) {
		return "", ""
	}
	s := strings.TrimSpace(stripSingleDiffPrefix(line))
	if s == "" || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "/*") || strings.HasPrefix(s, "*") {
		return "", ""
	}
	for _, kw := range declKeywords {
		if strings.HasPrefix(s, kw) {
			rest := strings.TrimSpace(s[len(kw):])
			if kw == "func " && strings.HasPrefix(rest, "(") {
				if closeIdx := strings.IndexByte(rest, ')'); closeIdx >= 0 {
					rest = strings.TrimSpace(rest[closeIdx+1:])
				} else {
					break
				}
			}
			if id := leadingIdent(rest); len(id) >= 4 {
				sig := s
				if idx := declBodyBraceIndex(sig); idx >= 0 {
					sig = strings.TrimSpace(sig[:idx])
				}
				return id, normalizeCollapsedDecl(sig)
			}
			break
		}
	}
	return "", ""
}

// changedDeclarationSymbols returns declared symbols in editText whose
// signature line differs between the before/after snippets in editText or
// differs from the declaration currently on disk in path.
func changedDeclarationSymbols(cwd, path, editText string) []string {
	if path == "" || editText == "" {
		return nil
	}
	beforeView, afterView, hasDiff := splitDiffViews(editText)
	afterOrder, firstSigs, editSigs := collectDeclaredSigs(afterView)
	_, _, beforeSigs := collectDeclaredSigs(beforeView)
	if len(afterOrder) == 0 {
		return nil
	}
	diskSigs := map[string]string{}
	diskPath := path
	if !filepath.IsAbs(diskPath) && cwd != "" {
		diskPath = filepath.Join(cwd, diskPath)
	}
	if raw, err := os.ReadFile(diskPath); err == nil && len(raw) <= 2<<20 {
		_, _, diskSigs = collectDeclaredSigs(string(raw))
	}
	var changed []string
	for _, id := range afterOrder {
		if firstSigs[id] != editSigs[id] {
			changed = append(changed, id)
			continue
		}
		if hasDiff {
			if beforeSig, ok := beforeSigs[id]; ok && beforeSig != editSigs[id] {
				changed = append(changed, id)
				continue
			}
		}
		if diskSig, ok := diskSigs[id]; ok && diskSig != editSigs[id] {
			changed = append(changed, id)
		}
	}
	return changed
}

// crossFileCallersLine scans sibling and repository source files for external
// call sites when an edit modifies an existing function or type signature.
func crossFileCallersLine(cwd, path string, syms []string) string {
	if policy.SourceHintsOff() || path == "" || len(syms) == 0 {
		return ""
	}
	diskPath := path
	if !filepath.IsAbs(diskPath) && cwd != "" {
		diskPath = filepath.Join(cwd, diskPath)
	}
	cleanTarget := filepath.Clean(diskPath)
	ext := strings.ToLower(filepath.Ext(cleanTarget))
	if ext == "" {
		return ""
	}
	pkgDir := filepath.Dir(cleanTarget)
	scanRoot := sources.RepoRoot(pkgDir)
	if scanRoot == "" && cwd != "" {
		scanRoot = sources.RepoRoot(cwd)
	}
	if scanRoot == "" {
		scanRoot = cwd
	}
	if scanRoot == "" {
		scanRoot = pkgDir
	}

	candidates := collectCallerCandidates(scanRoot, pkgDir, cleanTarget, ext)
	pkgBase := strings.ToLower(filepath.Base(pkgDir))
	for _, sym := range syms {
		lowSym := strings.ToLower(sym)
		if len(lowSym) < 4 {
			continue
		}
		var callers []string
		for _, cand := range candidates {
			needle := lowSym
			if filepath.Dir(cand) != pkgDir && ext == ".go" && pkgBase != "" && pkgBase != "." {
				needle = pkgBase + "." + lowSym
			}
			lineNo := firstCallLineInFile(cand, sym, needle)
			if lineNo <= 0 {
				continue
			}
			rel := filepath.Base(cand)
			if r, err := filepath.Rel(scanRoot, cand); err == nil && !strings.HasPrefix(r, "..") {
				rel = filepath.ToSlash(r)
			}
			callers = append(callers, fmt.Sprintf("%s:%d", search.SafePath(rel), lineNo))
			if len(callers) >= 3 {
				break
			}
		}
		if len(callers) == 0 && cleanTarget != "" {
			lineNo := firstCallLineInFile(cleanTarget, sym, lowSym)
			if lineNo > 0 {
				rel := filepath.Base(cleanTarget)
				if r, err := filepath.Rel(scanRoot, cleanTarget); err == nil && !strings.HasPrefix(r, "..") {
					rel = filepath.ToSlash(r)
				}
				callers = append(callers, fmt.Sprintf("%s:%d", search.SafePath(rel), lineNo))
			}
		}
		if len(callers) > 0 {
			return fmt.Sprintf(" — callers of %s: %s", search.SafeLine(sym), strings.Join(callers, ", "))
		}
	}
	return ""
}

func collectCallerCandidates(scanRoot, pkgDir, cleanTarget, ext string) []string {
	var candidates []string
	seenFile := map[string]bool{cleanTarget: true}
	addCand := func(p string) {
		cp := filepath.Clean(p)
		if seenFile[cp] || strings.ToLower(filepath.Ext(cp)) != ext {
			return
		}
		base := strings.ToLower(filepath.Base(cp))
		if strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
			return
		}
		seenFile[cp] = true
		candidates = append(candidates, cp)
	}
	if entries, err := os.ReadDir(pkgDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				addCand(filepath.Join(pkgDir, e.Name()))
			}
		}
	}
	if scanRoot != "" && len(candidates) < 150 {
		_ = filepath.WalkDir(scanRoot, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // best-effort caller scan skips unreadable entries
			}
			if d.IsDir() {
				name := d.Name()
				if p != scanRoot && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "dist" || name == "build" || name == "testdata") {
					return filepath.SkipDir
				}
				return nil
			}
			if len(candidates) >= 150 {
				return filepath.SkipAll
			}
			addCand(p)
			return nil
		})
	}
	return candidates
}

func firstCallLineInFile(cand, sym, needle string) int {
	raw, err := os.ReadFile(cand)
	if err != nil || len(raw) > 256<<10 {
		return 0
	}
	content := string(raw)
	if !strings.Contains(strings.ToLower(content), needle) {
		return 0
	}
	lineNo := 0
	for line := range strings.SplitSeq(content, "\n") {
		lineNo++
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if id, _ := parseDeclarationLine(trimmed); strings.EqualFold(id, sym) {
			continue
		}
		if containsIdentifierWord(strings.ToLower(trimmed), needle) {
			return lineNo
		}
	}
	return 0
}

// findSymbolDeclarationInRepo locates the non-test source file, 1-based line
// number, and declaration signature for sym inside the repository at cwd.
func findSymbolDeclarationInRepo(cwd, sym string) (string, int, string) {
	lowSym := strings.ToLower(strings.TrimSpace(sym))
	if cwd == "" || len(lowSym) < 4 || policy.SourceHintsOff() {
		return "", 0, ""
	}
	if idx := loadRepoSymbolMap(cwd); idx.numFiles > 0 || idx.capped {
		if loc, ok := idx.firstDecl[lowSym]; ok {
			return loc.rel, loc.line, loc.sig
		}
		return "", 0, ""
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" {
		return "", 0, ""
	}
	var files []string
	_ = filepath.WalkDir(scanRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable paths during symbol lookup
		}
		if d.IsDir() {
			name := d.Name()
			if p != scanRoot && (isSkippedScanDir(name) || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= 2000 {
			return filepath.SkipAll
		}
		base := strings.ToLower(d.Name())
		if strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
			return nil
		}
		switch strings.ToLower(filepath.Ext(base)) {
		case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs":
			files = append(files, p)
		}
		return nil
	})
	for _, cand := range files {
		raw, err := os.ReadFile(cand)
		if err != nil || len(raw) > 256<<10 {
			continue
		}
		content := string(raw)
		if !strings.Contains(strings.ToLower(content), lowSym) {
			continue
		}
		lines := strings.Split(content, "\n")
		for i := 0; i < len(lines); i++ {
			collapsed, endIdx := collapseDeclarationWindow(lines, i)
			if id, sig := parseDeclarationLine(collapsed); strings.EqualFold(id, sym) && sig != "" {
				rel := filepath.Base(cand)
				if r, err := filepath.Rel(scanRoot, cand); err == nil && !strings.HasPrefix(r, "..") {
					rel = filepath.ToSlash(r)
				}
				return rel, i + 1, clipDeclSignature(sig)
			}
			if endIdx > i {
				i = endIdx
			}
		}
	}
	return "", 0, ""
}

// testAssertionLiteralLine warns when an edit replaces or removes a string
// literal that a sibling test file asserts on.
func testAssertionLiteralLine(cwd, path, oldStr, newStr string) string {
	if policy.SourceHintsOff() || path == "" || oldStr == "" || newStr == "" || oldStr == newStr {
		return ""
	}
	if isTestFilePath(path) {
		return ""
	}
	removedLits := removedStringLiterals(oldStr, newStr)
	if len(removedLits) == 0 {
		return ""
	}
	diskPath := path
	if !filepath.IsAbs(diskPath) && cwd != "" {
		diskPath = filepath.Join(cwd, diskPath)
	}
	pkgDir := filepath.Dir(diskPath)
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return ""
	}
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(diskPath), filepath.Ext(diskPath)))
	var testFiles []string
	for _, e := range entries {
		if e.IsDir() || !isSiblingTestFileCandidate(e.Name()) {
			continue
		}
		low := strings.ToLower(e.Name())
		if strings.Contains(low, stem) {
			testFiles = append([]string{e.Name()}, testFiles...)
		} else {
			testFiles = append(testFiles, e.Name())
		}
	}
	if len(testFiles) > 2000 {
		testFiles = testFiles[:2000]
	}
	for _, tf := range testFiles {
		raw, err := os.ReadFile(filepath.Join(pkgDir, tf))
		if err != nil || len(raw) > 512<<10 {
			continue
		}
		content := string(raw)
		for _, lit := range removedLits {
			if !strings.Contains(content, lit) {
				continue
			}
			curTest := ""
			lineNo := 0
			for line := range strings.SplitSeq(content, "\n") {
				lineNo++
				trimmed := strings.TrimSpace(line)
				if h := testHeaderName(trimmed); h != "" {
					curTest = h
					continue
				}
				if curTest != "" && strings.Contains(trimmed, lit) {
					return fmt.Sprintf(" — test assertion in %s:%d (%s) checks %s",
						search.SafePath(tf), lineNo, search.SafeLine(curTest), search.SafeLine(lit))
				}
			}
		}
	}
	return ""
}

func removedStringLiterals(oldStr, newStr string) []string {
	var removed []string
	seen := map[string]bool{}
	for _, quote := range []byte{'"', '\''} {
		s := oldStr
		for {
			idx := strings.IndexByte(s, quote)
			if idx < 0 {
				break
			}
			rest := s[idx+1:]
			end := strings.IndexByte(rest, quote)
			if end < 0 {
				break
			}
			inner := rest[:end]
			s = rest[end+1:]
			lit := string(quote) + inner + string(quote)
			if len(inner) < 4 || len(inner) > 60 || strings.ContainsAny(inner, "\n\r\t\\") || seen[lit] {
				continue
			}
			seen[lit] = true
			if !strings.Contains(newStr, lit) && !strings.Contains(newStr, inner) {
				removed = append(removed, lit)
			}
		}
	}
	return removed
}

// testHeaderName extracts the test identifier across Go, Python, Rust, and TS/JS.
func testHeaderName(trimmed string) string {
	if strings.HasPrefix(trimmed, "func Test") {
		id := leadingIdent(strings.TrimPrefix(trimmed, "func "))
		if strings.HasPrefix(id, "Test") {
			return id
		}
	}
	if strings.HasPrefix(trimmed, "def test_") {
		rest := strings.TrimPrefix(trimmed, "def ")
		idx := strings.IndexAny(rest, "(: \t")
		if idx > 0 {
			return rest[:idx]
		}
		return rest
	}
	for _, fnPrefix := range []string{"fn test_", "pub fn test_", "pub(crate) fn test_"} {
		if strings.HasPrefix(trimmed, fnPrefix) {
			if _, rest, ok := strings.Cut(trimmed, "fn "); ok {
				ident := leadingIdent(rest)
				if strings.HasPrefix(ident, "test_") {
					return ident
				}
			}
		}
	}
	for _, itPrefix := range []string{"it(", "test(", "it.only(", "it.skip(", "test.only(", "test.skip("} {
		if rest, ok := strings.CutPrefix(trimmed, itPrefix); ok {
			if len(rest) > 0 && (rest[0] == '"' || rest[0] == '\'' || rest[0] == '`') {
				quote := rest[0]
				rest = rest[1:]
				if end := strings.IndexByte(rest, quote); end >= 0 {
					return rest[:end]
				}
			}
		}
	}
	return ""
}

func isTestFilePath(path string) bool {
	low := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(low, "_test.go") || strings.HasSuffix(low, "_test.rs") {
		return true
	}
	if strings.HasPrefix(low, "test_") || strings.HasSuffix(low, "_test.py") {
		return true
	}
	if (strings.Contains(low, ".test.") || strings.Contains(low, ".spec.")) &&
		(strings.HasSuffix(low, ".ts") || strings.HasSuffix(low, ".tsx") || strings.HasSuffix(low, ".js") || strings.HasSuffix(low, ".jsx")) {
		return true
	}
	return false
}

func isSiblingTestFileCandidate(name string) bool {
	low := strings.ToLower(name)
	if strings.HasSuffix(low, "_test.go") || strings.HasSuffix(low, "_test.rs") {
		return true
	}
	if (strings.HasPrefix(low, "test_") && strings.HasSuffix(low, ".py")) || strings.HasSuffix(low, "_test.py") {
		return true
	}
	if (strings.Contains(low, ".test.") || strings.Contains(low, ".spec.")) &&
		(strings.HasSuffix(low, ".ts") || strings.HasSuffix(low, ".tsx") || strings.HasSuffix(low, ".js") || strings.HasSuffix(low, ".jsx")) {
		return true
	}
	return false
}

func enclosingSymbolForSnippet(fileContent, editText string) string {
	for line := range strings.SplitSeq(editText, "\n") {
		probe := strings.TrimSpace(strings.TrimLeft(line, "+- "))
		if len(probe) < 12 || strings.HasPrefix(probe, "//") || strings.HasPrefix(probe, "#") || strings.HasPrefix(probe, "***") || strings.HasPrefix(probe, "@@") {
			continue
		}
		before, _, ok := strings.Cut(fileContent, probe)
		if !ok {
			continue
		}
		decls := declaredSymbolsInText(before)
		if len(decls) > 0 {
			return decls[len(decls)-1]
		}
	}
	return ""
}

func leadingIdent(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return ""
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func identifierTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

func hasMixedCaseOrUnderscore(s string) bool {
	if len(s) < 5 {
		return false
	}
	if strings.ContainsRune(s, '_') {
		return true
	}
	hasUpper, hasLower := false, false
	for _, r := range s {
		if unicode.IsUpper(r) {
			hasUpper = true
		} else if unicode.IsLower(r) {
			hasLower = true
		}
		if hasUpper && hasLower {
			return true
		}
	}
	return false
}

func isSymbolLikeCaseOrUnderscore(s string) bool {
	if len(s) < 5 {
		return false
	}
	if strings.ContainsRune(s, '_') {
		return true
	}
	seenLower, internalUpper, upperCount := false, false, 0
	for _, r := range s {
		if unicode.IsUpper(r) {
			upperCount++
			if seenLower {
				internalUpper = true
			}
		} else if unicode.IsLower(r) {
			seenLower = true
		}
	}
	// Accept camelCase (parseLine), PascalCase (ParseLine), or ACRONYMPrefix (MCPServer),
	// while rejecting plain Titlecase English sentence starters ("Change", "Corpus", "Report").
	return seenLower && (internalUpper || upperCount >= 2)
}

func isCommonKeyword(low string) bool {
	switch low {
	case "func", "type", "struct", "interface", "return", "string", "error", "bool",
		"true", "false", "context", "class", "async", "function", "export", "const",
		"import", "package", "default", "switch", "case", "break", "continue",
		"main", "test", "file", "path", "name", "text", "line", "data", "list",
		"item", "info", "user", "time", "read", "write", "open", "close", "count",
		"index", "model", "agent", "check", "build", "entry", "state", "value",
		"store", "lines", "files", "names", "paths", "items", "users", "tests",
		"event", "input", "query", "scope", "token", "usage", "match", "parse", "print":
		return true
	}
	return false
}

func mentionsAnySymbol(symbols []string, text string) bool {
	if len(symbols) == 0 || text == "" {
		return false
	}
	low := strings.ToLower(text)
	for _, sym := range symbols {
		w := strings.ToLower(strings.TrimSpace(sym))
		if len(w) < 4 {
			continue
		}
		if containsIdentifierWord(low, w) {
			return true
		}
	}
	return false
}

func containsIdentifierWord(lowText, lowSym string) bool {
	start := 0
	for {
		idx := strings.Index(lowText[start:], lowSym)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(lowSym)
		leftOK := pos == 0 || !isIdentByte(lowText[pos-1])
		rightOK := end == len(lowText) || !isIdentByte(lowText[end])
		if leftOK && rightOK {
			return true
		}
		start = pos + 1
	}
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '-' || b == '/'
}

func promotedDecisionForFileIn(dir, path string, metas []index.SessionMeta) string {
	if len(metas) == 0 {
		return ""
	}
	if dir == "" {
		dir = index.DefaultDir()
	}
	want := make(map[string]bool, len(metas))
	noteKeys := make(map[string]bool, len(metas))
	for _, meta := range metas {
		want[meta.Key()] = true
		if meta.OrigID != "" {
			want[model.SessionKey(meta.Harness, meta.OrigID)] = true
		}
		for _, id := range meta.CandidatePromotedNoteIDs() {
			noteKeys[id] = true
		}
	}
	// The note's own project as well as the session it came from. The metas
	// are already scoped by the auto activation, so a note is reached only
	// through an allowed session — but the note carries a project of its own,
	// and every other reader of these notes checks it (#2506).
	pol := policy.Load()
	var found []sources.PromotedNote
	keep := func(note sources.PromotedNote) {
		if note.State != "accepted" {
			return
		}
		if note.Project != "" && !pol.Allows(policy.ActivationAuto, note.Project) {
			return
		}
		found = append(found, note)
	}
	for _, note := range sources.LoadPromotedNotes() {
		if !want[note.Session] {
			continue
		}
		keep(note)
	}
	// And the decisions that arrived by sync. Those are not in this machine's
	// notes file at all — a promotion crosses as a session of its own, carrying
	// the state, which is how the view page reads them (#2421). It touched no
	// file, so it is never among the sessions this file has; what ties it back
	// is the id a note is built from, `deja-note-<harness>-<id>`, derived here
	// from the session rather than parsed out of the note (harness names carry
	// dashes, so the id does not invert). Without this a decision promoted on
	// one machine was a decision in search on the other and filler prose at the
	// moment of the edit (#2510).
	for _, meta := range metas {
		if meta.Lifecycle != "" {
			keep(noteFromMeta(meta))
		}
	}
	if len(noteKeys) > 0 {
		for _, note := range index.PromotedNoteMetas(dir, func(project string) bool {
			return pol.Allows(policy.ActivationAuto, project)
		}) {
			id := note.OrigID
			if id == "" {
				id = note.ID
			}
			if !noteKeys[id] {
				continue
			}
			keep(noteFromMeta(note))
		}
	}
	line := decisionLineOf(found, toolHookDecisionBudget)
	if path == "" || line == "" || mentionsFile(path, line) {
		return line
	}
	var matching []sources.PromotedNote
	for _, note := range found {
		text := strings.TrimSpace(note.Text)
		if text == "" {
			text = strings.TrimSpace(note.Title)
		}
		if mentionsFile(path, text) {
			matching = append(matching, note)
		}
	}
	return decisionLineOf(matching, toolHookDecisionBudget)
}

// decisionLineOf words what this file or command has standing, newest first.
//
// Two, when they fit. A file often has a broad rule and a narrow exception, and
// the exception is newer almost by definition — it is an exception to something
// — so showing only the newest handed the agent the half that reverses the
// rule, with nothing to say the rule existed (#2524). When the second does not
// fit, the line says one was left out rather than leaving the narrower half
// standing alone.
func decisionLineOf(notes []sources.PromotedNote, budget int) string {
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].At.After(notes[j].At) })
	var lines []string
	seen := map[string]bool{}
	for _, note := range notes {
		text := strings.TrimSpace(note.Text)
		if text == "" {
			text = strings.TrimSpace(note.Title)
		}
		if text == "" {
			continue
		}
		text = trimTrailingFragment(search.SafeText(clipDecision(text, budget)))
		if text == "" || seen[strings.ToLower(text)] {
			continue
		}
		if isEphemeralCompletionReport(text) || (len([]rune(text)) >= 45 && !usableRepoCandidateLine(text)) {
			continue
		}
		seen[strings.ToLower(text)] = true
		lines = append(lines, text)
	}
	if len(lines) == 0 {
		return ""
	}
	out := lines[0]
	rest := len(lines) - 1
	if rest > 0 {
		if joined := out + "; also: " + lines[1]; len(joined) <= budget {
			out, rest = joined, rest-1
		}
	}
	if rest > 0 {
		out += fmt.Sprintf(" (+%d more standing here)", rest)
	}
	return out
}

// noteFromMeta reads the decision a session carries as a promoted note, so the
// two sources — this machine's notes file and what arrived by sync — can be
// weighed against each other by one rule.
func noteFromMeta(meta index.SessionMeta) sources.PromotedNote {
	text := meta.LifecycleNote
	if strings.TrimSpace(text) == "" {
		text = meta.Title
	}
	src, _ := meta.PromotedNoteSourceKey()
	return sources.PromotedNote{
		Project: meta.Project,
		Session: src,
		State:   meta.Lifecycle,
		Title:   meta.Title,
		Text:    text,
		At:      meta.LifecycleTime(),
	}
}

// clipDecision keeps a promoted note inside the same budget a scanned
// conclusion is held to, so one long note cannot take the line over.
func clipDecision(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	// On a rune boundary: a note in Russian or Japanese cut mid-character puts
	// invalid UTF-8 into the payload an agent is handed.
	end := budget
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end]
}

func metaSession(meta index.SessionMeta) model.Session {
	return model.Session{
		Harness:       meta.Harness,
		ID:            meta.ID,
		OrigID:        meta.OrigID,
		GaveUp:        meta.GaveUp,
		Lifecycle:     meta.Lifecycle,
		LifecycleNote: meta.LifecycleNote,
		LifecycleAt:   meta.LifecycleAt,
	}
}

// decisionUsable rejects a session whose decision should not be reused: one that
// says in its own words it backed the approach out (GaveUp), and one a later
// state marked rejected, superseded or stale. Surfacing either at the point of an
// edit would push the agent to redo exactly what was undone.
func decisionUsable(s model.Session, r *lifecycleResolver) bool {
	if s.GaveUp {
		return false
	}
	state, _, _, _, ok := r.resolve(s)
	if !ok {
		return true
	}
	switch state {
	case model.LifecycleRejected, model.LifecycleSuperseded, model.LifecycleStale:
		return false
	}
	return true
}

// trimClippedDecision truncates s to budget and drops dangling trailing connectors before adding an ellipsis.
func trimClippedDecision(s string, budget int) string {
	s = truncateToolLine(s, budget)
	if len(s) < budget-4 {
		return s
	}
	if r := s[len(s)-1]; r == '.' || r == '!' || r == '?' {
		return s
	}
	if i := strings.LastIndexAny(s, " \t"); i > 0 {
		s = strings.TrimRight(s[:i], " \t,;:-")
	}
	for _, tail := range []string{" in this", " in", " to", " for", " with", " by", " from", " of", " and", " or", " at", " on"} {
		if strings.HasSuffix(strings.ToLower(s), tail) && len(s) > len(tail)+12 {
			s = strings.TrimRight(s[:len(s)-len(tail)], " \t,;:-")
			break
		}
	}
	return s + "…"
}

// trimTrailingFragment drops a half-word left by a byte-budget cut, so a decision
// reads as "capped retries" rather than "capped retr". Only when the text was
// actually cut (near the budget and not ending a sentence).
func trimTrailingFragment(s string) string {
	return trimClippedDecision(s, toolHookDecisionBudget)
}

// fileMetaInScope keeps a session only if it worked on this exact path (an
// absolute path cannot collide across projects) or if it belongs to the
// project being worked in now. It is what stops a same-named file in an
// unrelated repo from being counted.
func fileMetaInScope(meta index.SessionMeta, path string, projects []string) bool {
	if slices.Contains(meta.Touched, path) {
		return true
	}
	proj := meta.BareProject()
	for _, cand := range projects {
		if cand == "" {
			continue
		}
		// The shared rule, so this scope cannot drift from the one the session
		// start and the handoff use: a bare candidate is a suffix match only
		// for a peer's project, whose path is not this machine's. Taking it
		// for a local one answered an edit to /work/api/ledger.go with seven
		// sessions from a client's acme/api, and their decision (#2339).
		if index.ProjectInScope(meta.Project, cand) {
			return true
		}
		// The other direction: a store that records the bare project name
		// ("api") against a candidate that carries the parent ("work/api").
		if strings.HasSuffix(cand, "/"+proj) {
			return true
		}
	}
	return false
}

// fileMetaMatchesRepoSubpath reports whether meta.Touched agrees with the
// repository subpath of path so identically named files in sibling modules do
// not leak file decisions or counts across modules.
func fileMetaMatchesRepoSubpath(meta index.SessionMeta, cwd, path string) bool {
	if path == "" || len(meta.Touched) == 0 {
		return true
	}
	rel := path
	if filepath.IsAbs(path) {
		cleanPath := filepath.Clean(path)
		baseRoot := sources.RepoRoot(filepath.Dir(cleanPath))
		if baseRoot == "" && cwd != "" {
			baseRoot = filepath.Clean(cwd)
		}
		if baseRoot != "" {
			if r, err := filepath.Rel(baseRoot, cleanPath); err == nil && !strings.HasPrefix(r, "..") {
				rel = r
			}
		}
	}
	rel = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rel)), "./")
	if !strings.Contains(rel, "/") {
		return true
	}
	base := filepath.Base(rel)
	sawSameBaseWithDir := false
	sawBareBase := false
	for _, raw := range meta.Touched {
		t := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(raw)), "./")
		if t == rel || strings.HasSuffix(t, "/"+rel) || (strings.Contains(t, "/") && strings.HasSuffix(rel, "/"+t)) {
			return true
		}
		if filepath.Base(t) == base {
			if strings.Contains(t, "/") {
				sawSameBaseWithDir = true
			} else {
				sawBareBase = true
			}
		}
	}
	if sawSameBaseWithDir && !sawBareBase {
		return false
	}
	return true
}

func baseName(p string) string { return index.CrossBase(p) }

// truncateToolLine caps the line at max bytes on a rune boundary, so a
// non-ASCII filename near the limit is not split mid-rune into a U+FFFD.
func truncateToolLine(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// shortHash fingerprints the injected line for per-session dedupe.
func shortHash(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 16)
}

// toolSessionCount words a count for a line read inside an action.
func toolSessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

// toolCommandTerms is what the tool hook searches on for a command: its text
// with the shell noise taken off, read as a command rather than as prose.
func toolCommandTerms(cmd string) []string {
	return prompt.CommandTerms(normalizedCommandText(cmd))
}

var callExprRe = regexp.MustCompile(`\b([a-zA-Z0-9_]+)\.([A-Z][a-zA-Z0-9_]*)\(`)

type pkgDecl struct {
	name    string
	relFile string
	lineNo  int
	sigLine string
}

func hallucinatedPkgSymbolLine(cwd, path, oldStr, newStr string) string {
	if policy.SourceHintsOff() {
		return ""
	}
	if line := hallucinatedLocalImportLine(cwd, path, oldStr, newStr); line != "" {
		return line
	}
	if newStr == "" || cwd == "" {
		return ""
	}
	matches := callExprRe.FindAllStringSubmatch(newStr, -1)
	if len(matches) == 0 {
		return ""
	}
	root := sources.RepoRoot(cwd)
	if root == "" {
		root = cwd
	}
	seenCall := map[string]bool{}
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		pkg, sym := m[1], m[2]
		callKey := pkg + "." + sym
		if seenCall[callKey] {
			continue
		}
		seenCall[callKey] = true
		if oldStr != "" && strings.Contains(oldStr, callKey) {
			continue
		}
		pkgDir := locatePkgDir(cwd, root, pkg)
		if pkgDir == "" {
			continue
		}
		decls, hasExact := scanPkgDeclarations(pkgDir, root, cwd, sym)
		if hasExact || len(decls) == 0 {
			continue
		}
		best, found := findClosestPkgDecl(sym, decls)
		if !found {
			continue
		}
		return fmt.Sprintf(" — %s has no exported %s; closest match in %s:%d: %s",
			search.SafePath(pkg), search.SafeLine(sym), search.SafePath(best.relFile), best.lineNo, search.SafeLine(best.sigLine))
	}
	return ""
}

func locatePkgDir(cwd, root, pkg string) string {
	for _, base := range []string{cwd, root} {
		for _, sub := range []string{"internal/" + pkg, "cmd/" + pkg, pkg} {
			cand := filepath.Join(base, filepath.FromSlash(sub))
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				return cand
			}
		}
	}
	return ""
}

func scanPkgDeclarations(pkgDir, root, cwd, sym string) ([]pkgDecl, bool) {
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, false
	}
	var decls []pkgDecl
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".go") || strings.HasSuffix(de.Name(), "_test.go") {
			continue
		}
		fpath := filepath.Join(pkgDir, de.Name())
		content, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		relFile := filepath.ToSlash(fpath)
		if rel, err := filepath.Rel(root, fpath); err == nil && !strings.HasPrefix(rel, "..") {
			relFile = filepath.ToSlash(rel)
		} else if rel, err := filepath.Rel(cwd, fpath); err == nil && !strings.HasPrefix(rel, "..") {
			relFile = filepath.ToSlash(rel)
		}
		lines := strings.Split(string(content), "\n")
		for lineIdx := 0; lineIdx < len(lines); lineIdx++ {
			trimmed := strings.TrimSpace(lines[lineIdx])
			if trimmed == "" || strings.HasPrefix(trimmed, "//") {
				continue
			}
			collapsed, endIdx := collapseDeclarationWindow(lines, lineIdx)
			if collapsed != "" {
				trimmed = collapsed
			}
			if strings.HasPrefix(trimmed, "func "+sym+"(") ||
				strings.HasPrefix(trimmed, "type "+sym+" ") ||
				strings.HasPrefix(trimmed, "var "+sym+" ") ||
				strings.HasPrefix(trimmed, "const "+sym+" ") ||
				strings.Contains(trimmed, ") "+sym+"(") {
				return nil, true
			}
			if rest, ok := strings.CutPrefix(trimmed, "func "); ok {
				if strings.HasPrefix(rest, "(") {
					if closeParen := strings.Index(rest, ")"); closeParen >= 0 {
						rest = strings.TrimSpace(rest[closeParen+1:])
					}
				}
				if paren := strings.Index(rest, "("); paren > 0 {
					fnName := strings.TrimSpace(rest[:paren])
					if len(fnName) > 0 && unicode.IsUpper(rune(fnName[0])) {
						sig := trimmed
						if bodyBrace := declBodyBraceIndex(sig); bodyBrace >= 0 {
							sig = strings.TrimSpace(sig[:bodyBrace])
						} else {
							sig = strings.TrimRight(sig, " {")
						}
						decls = append(decls, pkgDecl{
							name:    fnName,
							relFile: relFile,
							lineNo:  lineIdx + 1,
							sigLine: clipDeclSignature(sig),
						})
					}
				}
			}
			if endIdx > lineIdx {
				lineIdx = endIdx
			}
		}
	}
	return decls, false
}

func findClosestPkgDecl(sym string, decls []pkgDecl) (pkgDecl, bool) {
	type scoredMatch struct {
		d     pkgDecl
		lev   int
		affix int
	}
	var eligible []scoredMatch
	for _, d := range decls {
		lev := levenshteinDistance(sym, d.name)
		pref := commonPrefixLength(sym, d.name)
		suff := commonSuffixLength(sym, d.name)
		affix := pref + suff
		if pref >= 4 || suff >= 4 || lev <= 5 {
			eligible = append(eligible, scoredMatch{
				d:     d,
				lev:   lev,
				affix: affix,
			})
		}
	}
	if len(eligible) == 0 {
		return pkgDecl{}, false
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].lev != eligible[j].lev {
			return eligible[i].lev < eligible[j].lev
		}
		if eligible[i].affix != eligible[j].affix {
			return eligible[i].affix > eligible[j].affix
		}
		return eligible[i].d.name < eligible[j].d.name
	})
	return eligible[0].d, true
}

func levenshteinDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	dp := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		dp[j] = j
	}
	for i := 1; i <= la; i++ {
		prev := dp[0]
		dp[0] = i
		for j := 1; j <= lb; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur := min(dp[j]+1, min(dp[j-1]+1, prev+cost))
			prev = dp[j]
			dp[j] = cur
		}
	}
	return dp[lb]
}

func commonPrefixLength(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func commonSuffixLength(a, b string) int {
	la, lb := len(a), len(b)
	n := min(la, lb)
	for i := range n {
		if a[la-1-i] != b[lb-1-i] {
			return i
		}
	}
	return n
}

const (
	missingFileLocatedLabel = " — file not at requested path, located in repo: "
	missingFileSiblingLabel = " — file does not exist here; sibling files: "
)

// missingFileHintLine inspects a read or edit target path that does not exist
// on disk and points the agent to either the file's real path in the
// repository or existing sibling files sharing the same stem in that directory.
func missingFileHintLine(cwd, path, toolName string) string {
	if cwd == "" || path == "" || isFileCreationTool(toolName) || policy.SourceHintsOff() {
		return ""
	}
	diskPath := path
	if !filepath.IsAbs(diskPath) {
		diskPath = filepath.Join(cwd, diskPath)
	}
	if _, err := os.Stat(diskPath); !os.IsNotExist(err) {
		return ""
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" {
		return ""
	}
	base := filepath.Base(diskPath)
	tree := orientRepoTree(scanRoot)
	if resolved := tree.resolveRel(base); resolved != "" {
		return missingFileLocatedLabel + search.SafePath(resolved)
	}
	dirPath := filepath.Dir(diskPath)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(base))
	stem := strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	if len(stem) < 3 {
		return ""
	}
	var siblings []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.EqualFold(filepath.Ext(name), ext) {
			continue
		}
		candStem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		if commonPrefixLength(stem, candStem) >= 4 || commonSuffixLength(stem, candStem) >= 4 || levenshteinDistance(stem, candStem) <= 2 {
			siblings = append(siblings, search.SafePath(name))
			if len(siblings) == 3 {
				break
			}
		}
	}
	if len(siblings) > 0 {
		return missingFileSiblingLabel + strings.Join(siblings, ", ")
	}
	return ""
}

// hallucinatedLocalImportLine checks newly edited Python or JS/TS relative/local
// imports against the resolved repository file's declared top-level exports.
func hallucinatedLocalImportLine(cwd, path, oldStr, newStr string) string {
	if cwd == "" || path == "" || newStr == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(path))
	isPy := ext == ".py"
	isTS := isTSOrJSExt(ext)
	if !isPy && !isTS {
		return ""
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" {
		return ""
	}
	oldSpecs := parseLocalImportSpecs(oldStr, isPy)
	oldSet := make(map[string]bool, len(oldSpecs))
	for _, s := range oldSpecs {
		oldSet[s.mod+"\x00"+s.sym] = true
	}
	fileDir := filepath.Dir(path)
	if !filepath.IsAbs(fileDir) {
		fileDir = filepath.Join(cwd, fileDir)
	}
	for _, imp := range parseLocalImportSpecs(newStr, isPy) {
		if oldSet[imp.mod+"\x00"+imp.sym] || len(imp.sym) < 2 {
			continue
		}
		targetDisk, relTarget := resolveLocalImportTarget(scanRoot, fileDir, imp.mod, isPy)
		if targetDisk == "" {
			continue
		}
		raw, err := os.ReadFile(targetDisk)
		if err != nil || len(raw) > 256<<10 {
			continue
		}
		exports, dynamic := parseLocalModuleExports(string(raw), isPy)
		if dynamic || len(exports) == 0 {
			continue
		}
		found := false
		for _, ex := range exports {
			if ex.name == imp.sym {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if hint := formatMissingLocalImportHint(relTarget, imp.sym, exports); hint != "" {
			return hint
		}
	}
	return ""
}

type localImportSpec struct {
	mod string
	sym string
}

type localExportDecl struct {
	name   string
	lineNo int
}

func isTSOrJSExt(ext string) bool {
	switch ext {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".mts", ".cjs":
		return true
	default:
		return false
	}
}

func parseLocalImportSpecs(src string, isPy bool) []localImportSpec {
	var specs []localImportSpec
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		stmt := trimmed
		if strings.Contains(stmt, "(") && !strings.Contains(stmt, ")") || strings.Contains(stmt, "{") && !strings.Contains(stmt, "}") {
			var sb strings.Builder
			sb.WriteString(trimmed)
			for j := i + 1; j < len(lines) && j <= i+20; j++ {
				sb.WriteByte(' ')
				sb.WriteString(strings.TrimSpace(lines[j]))
				if (isPy && strings.Contains(lines[j], ")")) || (!isPy && strings.Contains(lines[j], "}")) {
					i = j
					break
				}
			}
			stmt = sb.String()
		}
		if isPy {
			if mod, rest, ok := parsePythonFromImportStmt(stmt); ok {
				for _, sym := range splitImportList(rest) {
					specs = append(specs, localImportSpec{mod: mod, sym: sym})
				}
			}
			continue
		}
		if mod, rest, ok := parseTSNamedImportStmt(stmt); ok {
			for _, sym := range splitImportList(rest) {
				specs = append(specs, localImportSpec{mod: mod, sym: sym})
			}
		}
	}
	return specs
}

func parsePythonFromImportStmt(stmt string) (mod, names string, ok bool) {
	if !strings.HasPrefix(stmt, "from ") {
		return "", "", false
	}
	rest := strings.TrimSpace(stmt[len("from "):])
	modPart, afterImport, found := strings.Cut(rest, " import ")
	if !found {
		return "", "", false
	}
	modPart = strings.TrimSpace(modPart)
	if modPart == "" || modPart == "__future__" {
		return "", "", false
	}
	afterImport = strings.TrimSpace(afterImport)
	if afterImport == "" || strings.HasPrefix(afterImport, "*") {
		return "", "", false
	}
	afterImport = strings.TrimPrefix(afterImport, "(")
	if before, _, hasClose := strings.Cut(afterImport, ")"); hasClose {
		afterImport = before
	}
	return modPart, afterImport, true
}

func parseTSNamedImportStmt(stmt string) (mod, names string, ok bool) {
	if !strings.HasPrefix(stmt, "import ") {
		return "", "", false
	}
	openBrace := strings.IndexByte(stmt, '{')
	closeBrace := strings.IndexByte(stmt, '}')
	if openBrace < 0 || closeBrace <= openBrace {
		return "", "", false
	}
	afterBrace := strings.TrimSpace(stmt[closeBrace+1:])
	if !strings.HasPrefix(afterBrace, "from ") {
		return "", "", false
	}
	quoted := strings.TrimSpace(afterBrace[len("from "):])
	if len(quoted) < 3 {
		return "", "", false
	}
	q := quoted[0]
	if q != '\'' && q != '"' {
		return "", "", false
	}
	end := strings.IndexByte(quoted[1:], q)
	if end < 0 {
		return "", "", false
	}
	mod = quoted[1 : 1+end]
	if !strings.HasPrefix(mod, "./") && !strings.HasPrefix(mod, "../") && !strings.HasPrefix(mod, "@/") {
		return "", "", false
	}
	return mod, stmt[openBrace+1 : closeBrace], true
}

func splitImportList(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "type ")
		part = strings.TrimSpace(part)
		if part == "" || part == "*" {
			continue
		}
		if beforeAs, _, hasAs := strings.Cut(part, " as "); hasAs {
			part = strings.TrimSpace(beforeAs)
		}
		if id := leadingIdent(part); id != "" && id == part {
			out = append(out, id)
		}
	}
	return out
}

func resolveLocalImportTarget(scanRoot, fileDir, mod string, isPy bool) (diskPath, relPath string) {
	var candidates []string
	if isPy {
		dots := 0
		for dots < len(mod) && mod[dots] == '.' {
			dots++
		}
		if dots > 0 {
			baseDir := fileDir
			for range dots - 1 {
				baseDir = filepath.Dir(baseDir)
			}
			sub := strings.ReplaceAll(mod[dots:], ".", "/")
			if sub == "" {
				candidates = append(candidates, filepath.Join(baseDir, "__init__.py"))
			} else {
				candidates = append(candidates,
					filepath.Join(baseDir, sub+".py"),
					filepath.Join(baseDir, sub, "__init__.py"),
				)
			}
		} else {
			sub := strings.ReplaceAll(mod, ".", "/")
			candidates = append(candidates,
				filepath.Join(scanRoot, sub+".py"),
				filepath.Join(scanRoot, sub, "__init__.py"),
				filepath.Join(scanRoot, "src", sub+".py"),
				filepath.Join(scanRoot, "src", sub, "__init__.py"),
			)
		}
	} else {
		var base string
		if after, ok := strings.CutPrefix(mod, "@/"); ok {
			base = filepath.Join(scanRoot, filepath.FromSlash(after))
		} else {
			base = filepath.Join(fileDir, filepath.FromSlash(mod))
		}
		noExt := base
		for _, ext := range []string{".js", ".jsx", ".mjs", ".ts", ".tsx"} {
			if trimmedBase, ok := strings.CutSuffix(base, ext); ok {
				noExt = trimmedBase
				break
			}
		}
		candidates = append(candidates,
			base,
			noExt+".ts",
			noExt+".tsx",
			noExt+".js",
			noExt+".jsx",
			noExt+".mjs",
			filepath.Join(noExt, "index.ts"),
			filepath.Join(noExt, "index.tsx"),
			filepath.Join(noExt, "index.js"),
		)
	}
	for _, cand := range candidates {
		info, err := os.Stat(cand)
		if err != nil || info.IsDir() {
			continue
		}
		rel, rErr := filepath.Rel(scanRoot, cand)
		if rErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		return cand, filepath.ToSlash(rel)
	}
	return "", ""
}

func parseLocalModuleExports(content string, isPy bool) (decls []localExportDecl, dynamic bool) {
	seen := map[string]bool{}
	add := func(name string, lineNo int) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		decls = append(decls, localExportDecl{name: name, lineNo: lineNo})
	}
	if isPy {
		dynamic = parsePythonModuleExports(content, add)
		return decls, dynamic
	}
	dynamic = parseTSModuleExports(content, add)
	return decls, dynamic
}

func parsePythonModuleExports(content string, add func(string, int)) bool {
	lineNo := 0
	for line := range strings.SplitSeq(content, "\n") {
		lineNo++
		if strings.Contains(line, "import *") || strings.HasPrefix(strings.TrimSpace(line), "def __getattr__") {
			return true
		}
		if line == "" || line[0] == ' ' || line[0] == '\t' || strings.HasPrefix(line, "#") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if name, _ := parseDeclarationLine(trimmed); name != "" {
			add(name, lineNo)
			continue
		}
		if strings.HasPrefix(trimmed, "from ") {
			addPythonReexports(trimmed, lineNo, add)
			continue
		}
		addPythonTopLevelBinding(trimmed, lineNo, add)
	}
	return false
}

func addPythonReexports(trimmed string, lineNo int, add func(string, int)) {
	_, names, ok := parsePythonFromImportStmt(trimmed)
	if !ok {
		return
	}
	for part := range strings.SplitSeq(names, ",") {
		part = strings.TrimSpace(part)
		if _, alias, hasAs := strings.Cut(part, " as "); hasAs {
			add(leadingIdent(strings.TrimSpace(alias)), lineNo)
			continue
		}
		add(leadingIdent(part), lineNo)
	}
}

func addPythonTopLevelBinding(trimmed string, lineNo int, add func(string, int)) {
	for _, sep := range []string{":", "="} {
		left, right, ok := strings.Cut(trimmed, sep)
		if !ok {
			continue
		}
		cand := strings.TrimSpace(left)
		if sep == "=" && strings.HasPrefix(right, "=") {
			return
		}
		if id := leadingIdent(cand); id != "" && id == cand {
			add(id, lineNo)
		}
		return
	}
}

func parseTSModuleExports(content string, add func(string, int)) bool {
	lineNo := 0
	for line := range strings.SplitSeq(content, "\n") {
		lineNo++
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if strings.HasPrefix(trimmed, "export *") {
			return true
		}
		if !strings.HasPrefix(trimmed, "export ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "default "))
		if strings.HasPrefix(rest, "{") {
			addTSBracedExports(rest[1:], lineNo, add)
			continue
		}
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "declare "))
		if name, _ := parseDeclarationLine(rest); name != "" {
			add(name, lineNo)
			continue
		}
		for _, kw := range []string{"const ", "let ", "var ", "type ", "interface ", "enum ", "class ", "function ", "async function "} {
			if after, ok := strings.CutPrefix(rest, kw); ok {
				add(leadingIdent(strings.TrimSpace(after)), lineNo)
				break
			}
		}
	}
	return false
}

func addTSBracedExports(afterOpen string, lineNo int, add func(string, int)) {
	inner, _, ok := strings.Cut(afterOpen, "}")
	if !ok {
		return
	}
	for part := range strings.SplitSeq(inner, ",") {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "type "))
		if _, alias, hasAs := strings.Cut(part, " as "); hasAs {
			add(leadingIdent(strings.TrimSpace(alias)), lineNo)
			continue
		}
		add(leadingIdent(part), lineNo)
	}
}

func formatMissingLocalImportHint(relTarget, sym string, exports []localExportDecl) string {
	bestName := ""
	bestLine := 0
	bestScore := 1 << 20
	for _, ex := range exports {
		lowImp := strings.ToLower(sym)
		lowEx := strings.ToLower(ex.name)
		dist := levenshteinDistance(lowImp, lowEx)
		pref := commonPrefixLength(lowImp, lowEx)
		suf := commonSuffixLength(lowImp, lowEx)
		maxLen := max(len(lowImp), len(lowEx))
		if dist <= 2 || (maxLen >= 6 && dist <= 3 && (pref >= 3 || suf >= 4)) {
			score := dist*4 - pref*2 - suf
			if score < bestScore {
				bestScore = score
				bestName = ex.name
				bestLine = ex.lineNo
			}
		}
	}
	if bestName != "" {
		return fmt.Sprintf(" — check import from %s — %q not declared; did you mean %q (%s:%d)?",
			search.SafePath(relTarget), search.SafeLine(sym), search.SafeLine(bestName),
			search.SafePath(relTarget), bestLine)
	}
	var names []string
	for _, ex := range exports {
		if strings.HasPrefix(ex.name, "_") {
			continue
		}
		names = append(names, search.SafeLine(ex.name))
		if len(names) >= 5 {
			break
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf(" — check import from %s — %q not declared; exported: %s",
		search.SafePath(relTarget), search.SafeLine(sym), strings.Join(names, ", "))
}

func isFileCreationTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write", "write_file", "write_to_file", "create", "create_file", "fs_write", "fs_append", "notebookedit", "notebook_edit":
		return true
	}
	return false
}

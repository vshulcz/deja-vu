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
func promptDirectContext(dir, cwd, sessionID, asked string, prevAssistant ...string) string {
	if strings.TrimSpace(asked) == "" || digest.IsAgentArtifact(asked) {
		return ""
	}
	autoRecordPromptCorrection(cwd, sessionID, asked)
	seen := alreadyInjected(dir, sessionID)
	var lines []string
	var tokens []string
	if srLine, srTok := sessionStandingRuleLine(dir, cwd, sessionID, asked, seen, prevAssistant...); srLine != "" {
		lines = append(lines, srLine)
		tokens = append(tokens, srTok)
	}
	if rmLine, rmTok := promptRepoMemoryLine(cwd, asked, seen); rmLine != "" {
		lines = append(lines, rmLine)
		tokens = append(tokens, rmTok)
	}
	if fxLine, fxTok := promptErrorFixLine(dir, cwd, asked, seen); fxLine != "" {
		lines = append(lines, fxLine)
		tokens = append(tokens, fxTok)
	}
	if fbLine, fbToks := promptFileBriefLine(dir, cwd, asked, seen); fbLine != "" {
		lines = append(lines, fbLine)
		tokens = append(tokens, fbToks...)
	} else if sbLine, sbTok := promptSymbolBriefLine(cwd, asked, seen); sbLine != "" {
		lines = append(lines, sbLine)
		tokens = append(tokens, sbTok)
	}
	if sessionID != "" && len(tokens) > 0 {
		rememberInjectedIDs(dir, sessionID, tokens...)
	}
	return strings.Join(lines, "\n")
}

// promptSymbolBriefLine locates a symbol declared in the repository when a
// user prompt names a specific function, method, or type without naming its
// file, giving the agent exact file:line, signature, and sibling test target at
// Turn 0 without a grep/locate round-trip.
func promptSymbolBriefLine(cwd, asked string, seen map[string]bool) (string, string) {
	if cwd == "" || !policy.Load().Allows(policy.ActivationAuto, "") {
		return "", ""
	}
	pol := policy.Load()
	checked := map[string]bool{}
	for _, tok := range identifierTokens(asked) {
		low := strings.ToLower(tok)
		if len(low) < 6 || checked[low] || !hasMixedCaseOrUnderscore(tok) || isCommonKeyword(low) {
			continue
		}
		checked[low] = true
		rel, lineNo, sig := findSymbolDeclarationInRepo(cwd, tok)
		if rel == "" || sig == "" {
			continue
		}
		line := fmt.Sprintf("defined in %s:%d: %s", search.SafePath(rel), lineNo, clipDecision(search.SafeText(sig), 140))
		if repoDec := repoMemoryDecisionForFile(cwd, rel, []string{tok}, pol); repoDec != "" {
			line += repoDecisionLabel + repoDec
		}
		if callers := crossFileCallersLine(cwd, rel, []string{tok}); callers != "" {
			line += callers
		}
		if ran := siblingTestLineFor(cwd, rel, []string{tok}); ran != "" && !strings.Contains(line, ran) {
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

// promptFileBriefLine checks whether the user's prompt explicitly names a file
// in the repository and returns a compact 1-line brief with its coupled
// companion file and verified test command so the agent has both at Turn 0.
func promptFileBriefLine(dir, cwd, asked string, seen map[string]bool) (string, []string) {
	if cwd == "" {
		return "", nil
	}
	tree := orientRepoTree(cwd)
	projects := sync.OnceValue(func() []string { return digest.ProjectNameCandidates(cwd) })
	pol := policy.Load()
	seenFiles := map[string]bool{}
	for tok := range strings.FieldsSeq(asked) {
		tok = strings.Trim(tok, "\"'`(),:;!?[]{}<>")
		if idx := strings.IndexByte(tok, ':'); idx > 0 {
			tok = tok[:idx]
		}
		if tok == "" || !looksLikePromptFileRef(tok) {
			continue
		}
		rel := tok
		if resolved := tree.resolveRel(tok); resolved != "" {
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
		if ran == "" {
			ran = siblingTestLineFor(cwd, rel, nil)
		}
		repoDec := repoMemoryDecisionForFile(cwd, rel, nil, pol)
		var parts []string
		if repoDec != "" {
			parts = append(parts, repoDecisionLabel+repoDec)
		}
		if co != "" {
			parts = append(parts, co)
		}
		if ran != "" {
			parts = append(parts, ran)
		}
		if len(parts) == 0 {
			continue
		}
		line := search.SafePath(rel) + strings.Join(parts, "")
		pTok := "prompt:fb:" + shortHash(line)
		tTok := "tool:" + shortHash(dedupeFact(line))
		if seen != nil && (seen[pTok] || seen[tTok]) {
			continue
		}
		return line, []string{pTok, tTok}
	}
	return "", nil
}

// liveRecallBrief constructs a live repository intelligence brief when an agent
// queries `deja recall` with a symbol name, file reference, compiler diagnostic,
// or failing test output, giving all 44 agents live repo facts even with zero history.
func liveRecallBrief(dir, cwd, query string) string {
	if cwd == "" {
		cwd = howCwd()
	}
	query = strings.TrimSpace(query)
	if cwd == "" || query == "" {
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
			if norm := orientPath(editPart, cwd); norm != "" {
				line = line[:idx+len("changed next: ")] + norm
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

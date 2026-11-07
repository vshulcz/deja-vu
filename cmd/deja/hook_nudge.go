package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

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
func promptDirectContext(dir, cwd, sessionID, asked string) string {
	if strings.TrimSpace(asked) == "" || digest.IsAgentArtifact(asked) {
		return ""
	}
	autoRecordPromptCorrection(cwd, sessionID, asked)
	seen := alreadyInjected(dir, sessionID)
	var lines []string
	var tokens []string
	if rmLine, rmTok := promptRepoMemoryLine(cwd, asked, seen); rmLine != "" {
		lines = append(lines, rmLine)
		tokens = append(tokens, rmTok)
	}
	if fxLine, fxTok := promptErrorFixLine(dir, cwd, asked, seen); fxLine != "" {
		lines = append(lines, fxLine)
		tokens = append(tokens, fxTok)
	}
	if sessionID != "" && len(tokens) > 0 {
		rememberInjectedIDs(dir, sessionID, tokens...)
	}
	return strings.Join(lines, "\n")
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
		if !matched {
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
	_ = os.WriteFile(p, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
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

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func maybePinLiveUserRule(dir, cwd, sessionID, prompt string) {
	t := strings.TrimSpace(prompt)
	if len(t) < 12 || len(t) > 1200 {
		return
	}
	lower := strings.ToLower(t)
	if strings.Contains(lower, "<system_message>") || strings.Contains(lower, "<system-message>") {
		return
	}
	rules := digest.ExtractStandingRules(model.Session{
		ID:       sessionID,
		Messages: []model.Message{{Role: "user", Text: t}},
	}, nil)
	if len(rules) == 0 {
		return
	}
	for _, r := range rules {
		pinExtractedLiveRule(dir, cwd, sessionID, r.Text)
	}
}

// syncLiveSessionRules pins standing rules and turn-pair refusals across all
// harnesses (using the live transcript, in-memory messages, or harness session
// store when available) and returns the preceding assistant message text.
func syncLiveSessionRules(dir, cwd string, input promptHookInput, asked string) string {
	msgs, prevRules, prevAssistant := liveSessionRecentMessages(dir, cwd, input, asked)
	if len(msgs) == 0 && len(prevRules) == 0 {
		maybePinLiveUserRule(dir, cwd, input.SessionID, asked)
		return prevAssistant
	}
	rules := digest.ExtractStandingRules(model.Session{
		ID:       input.SessionID,
		Harness:  input.Harness,
		Messages: msgs,
	}, prevRules)
	for _, r := range rules {
		ctxTerms := turnPairContextTerms(msgs, r.Text)
		pinExtractedLiveRule(dir, cwd, input.SessionID, r.Text, ctxTerms...)
	}
	return prevAssistant
}

func liveSessionRecentMessages(dir, cwd string, input promptHookInput, asked string) ([]model.Message, []model.ContextFact, string) {
	var msgs []model.Message
	var prevRules []model.ContextFact
	ws := compactionWorkspace(cwd)
	if input.SessionID != "" {
		for _, candidateWS := range []string{cwd, sources.Home()} {
			if candidateWS == "" {
				continue
			}
			if state, found, err := index.Compaction(dir, input.SessionID, compactionWorkspace(candidateWS)); err == nil && found {
				prevRules = append(prevRules, state.Data.Rules...)
				if state.Data.Goal.Text != "" {
					msgs = append(msgs, model.Message{Role: "user", Text: state.Data.Goal.Text})
				}
				if state.Data.Objective.Text != "" {
					msgs = append(msgs, model.Message{Role: "user", Text: state.Data.Objective.Text})
				}
				break
			}
		}
	}
	msgs = append(msgs, readLiveHookTranscriptMessages(input, cwd, ws)...)
	t := strings.TrimSpace(asked)
	if t != "" {
		if len(msgs) == 0 || msgs[len(msgs)-1].Role != "user" || strings.TrimSpace(msgs[len(msgs)-1].Text) != t {
			msgs = append(msgs, model.Message{Role: "user", Text: t})
		}
	}
	prevAssistant := ""
	for _, m := range slices.Backward(msgs) {
		if m.Role == "assistant" && strings.TrimSpace(m.Text) != "" {
			prevAssistant = m.Text
			break
		}
	}
	return msgs, prevRules, prevAssistant
}

func readLiveHookTranscriptMessages(input promptHookInput, cwd, ws string) []model.Message {
	if input.TranscriptPath == "" && len(input.Messages) == 0 && input.Harness == "" && input.SessionID == "" {
		return nil
	}
	pre := precompactHookInput{
		SessionID:      input.SessionID,
		TranscriptPath: input.TranscriptPath,
		CWD:            cwd,
		Harness:        input.Harness,
		Messages:       input.Messages,
	}
	tr, reason, _ := compactionSource(pre, ws)
	if reason == "" && len(tr.Session.Messages) > 0 {
		tail := tr.Session.Messages
		if len(tail) > 24 {
			tail = tail[len(tail)-24:]
		}
		var out []model.Message
		for _, m := range tail {
			if (m.Role == "user" || m.Role == "assistant") && strings.TrimSpace(m.Text) != "" {
				out = append(out, model.Message{Role: m.Role, Text: m.Text})
			}
		}
		return out
	}
	var out []model.Message
	if input.TranscriptPath != "" {
		out = append(out, parseGenericTranscriptTail(input.TranscriptPath)...)
	}
	if len(input.Messages) > 0 {
		out = append(out, parseGenericHookMessages(input.Messages)...)
	}
	return out
}

func parseGenericTranscriptTail(path string) []model.Message {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return nil
	}
	const maxTail = 256 << 10
	offset := int64(0)
	if fi.Size() > maxTail {
		offset = fi.Size() - maxTail
	}
	if offset > 0 {
		if _, seekErr := f.Seek(offset, io.SeekStart); seekErr != nil {
			return nil
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, maxTail))
	if err != nil {
		return nil
	}
	var out []model.Message
	for line := range bytes.SplitSeq(b, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var row struct {
			Type    string         `json:"type"`
			Role    string         `json:"role"`
			Content hookPromptText `json:"content"`
			Text    string         `json:"text"`
			Message struct {
				Role    string         `json:"role"`
				Content hookPromptText `json:"content"`
				Text    string         `json:"text"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		role := firstNonEmpty(row.Message.Role, row.Role, row.Type)
		if role != "user" && role != "assistant" {
			continue
		}
		txt := firstNonEmpty(
			strings.TrimSpace(string(row.Message.Content)),
			strings.TrimSpace(row.Message.Text),
			strings.TrimSpace(string(row.Content)),
			strings.TrimSpace(row.Text),
		)
		if txt != "" {
			out = append(out, model.Message{Role: role, Text: txt})
		}
	}
	if len(out) > 24 {
		out = out[len(out)-24:]
	}
	return out
}

func parseGenericHookMessages(raw json.RawMessage) []model.Message {
	var rows []struct {
		Role    string         `json:"role"`
		Content hookPromptText `json:"content"`
		Text    string         `json:"text"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		return nil
	}
	var out []model.Message
	for _, r := range rows {
		if r.Role != "user" && r.Role != "assistant" {
			continue
		}
		txt := strings.TrimSpace(string(r.Content))
		if txt == "" {
			txt = strings.TrimSpace(r.Text)
		}
		if txt != "" {
			out = append(out, model.Message{Role: r.Role, Text: txt})
		}
	}
	if len(out) > 24 {
		out = out[len(out)-24:]
	}
	return out
}

func pinExtractedLiveRule(dir, cwd, sessionID, ruleText string, extraSyms ...string) {
	ruleText = strings.TrimSpace(ruleText)
	if ruleText == "" {
		return
	}
	paths, syms := extractLiveRulePathsAndSymbols(cwd, ruleText)
	if len(paths) == 0 && len(syms) == 0 {
		return
	}
	seenSym := map[string]bool{}
	for _, s := range syms {
		seenSym[strings.ToLower(s)] = true
	}
	for _, s := range extraSyms {
		s = strings.TrimSpace(s)
		low := strings.ToLower(s)
		if len(s) >= 3 && !seenSym[low] && !isCommonKeyword(low) && len(syms) < 12 {
			seenSym[low] = true
			syms = append(syms, s)
		}
	}
	from := "live-session"
	if strings.TrimSpace(sessionID) != "" {
		from = strings.TrimSpace(sessionID)
	}
	entry := sources.RepoEntry{
		ID:    sources.RepoEntryID("rule", ruleText),
		Kind:  "rule",
		Text:  ruleText,
		Files: paths,
		Date:  time.Now().UTC().Format("2006-01-02"),
		From:  from,
	}
	var targets []string
	if cwd != "" {
		root := sources.RepoRoot(cwd)
		if root != "" {
			targets = append(targets, filepath.Join(root, ".deja", "live_rules.jsonl"))
		} else if len(cachedProjectNameCandidates(cwd)) > 0 {
			targets = append(targets, filepath.Join(cwd, ".deja", "live_rules.jsonl"))
		}
	}
	effDir := dir
	if effDir == "" {
		effDir = index.DefaultDir()
	}
	if effDir != "" {
		targets = append(targets, filepath.Join(effDir, "live_rules.jsonl"))
	}
	for _, target := range targets {
		appendLiveRule(target, entry, syms)
	}
}

func syncAntigravitySessionRules(dir, cwd, conversationID string, steps []transcriptStep) {
	var msgs []model.Message
	var prevRules []model.ContextFact
	if conversationID != "" {
		for _, ws := range []string{cwd, sources.Home()} {
			if ws == "" {
				continue
			}
			if state, found, err := index.Compaction(dir, conversationID, compactionWorkspace(ws)); err == nil && found {
				prevRules = append(prevRules, state.Data.Rules...)
				if state.Data.Goal.Text != "" {
					msgs = append(msgs, model.Message{Role: "user", Text: state.Data.Goal.Text})
				}
				if state.Data.Objective.Text != "" {
					msgs = append(msgs, model.Message{Role: "user", Text: state.Data.Objective.Text})
				}
				break
			}
		}
	}
	for _, st := range slices.Backward(steps) {
		switch st.Type {
		case "PLANNER_RESPONSE":
			if strings.TrimSpace(st.Content) != "" {
				msgs = append(msgs, model.Message{Role: "assistant", Text: st.Content})
			}
		case "USER_INPUT":
			if q := userRequestText(st.Content); q != "" {
				msgs = append(msgs, model.Message{Role: "user", Text: q})
			}
		}
	}
	if len(msgs) == 0 && len(prevRules) == 0 {
		return
	}
	rules := digest.ExtractStandingRules(model.Session{
		ID:       conversationID,
		Harness:  "antigravity",
		Messages: msgs,
	}, prevRules)
	for _, r := range rules {
		ctxTerms := turnPairContextTerms(msgs, r.Text)
		pinExtractedLiveRule(dir, cwd, conversationID, r.Text, ctxTerms...)
	}
}

// turnPairContextTerms extracts the co-occurring content terms from the
// (assistant proposal, user refusal/rule) turn pair that produced ruleText, so
// future prompts mentioning the surrounding topic match even when the user's
// refusal did not repeat the assistant's technical vocabulary.
func turnPairContextTerms(msgs []model.Message, ruleText string) []string {
	body := ruleText
	var out []string
	if idx := strings.Index(ruleText, ": "); idx > 0 && strings.HasPrefix(ruleText, "Do not suggest ") {
		target := strings.TrimSpace(strings.TrimPrefix(ruleText[:idx], "Do not suggest "))
		if isTechnicalRuleSymbol(target) {
			out = append(out, target)
			if bare := strings.TrimLeft(target, "-/"); bare != "" && bare != target && isTechnicalRuleSymbol(bare) {
				out = append(out, bare)
			}
		}
		body = strings.TrimSpace(ruleText[idx+2:])
	}
	prevAssistant := ""
	for _, m := range msgs {
		if m.Role == "assistant" {
			prevAssistant = m.Text
			continue
		}
		if m.Role == "user" && body != "" && strings.Contains(m.Text, body) {
			_, backtickSyms := extractLiveRulePathsAndSymbols("", prevAssistant+" "+m.Text)
			out = append(out, backtickSyms...)
			return out
		}
	}
	return out
}

func isTechnicalRuleSymbol(tok string) bool {
	tok = strings.TrimSpace(tok)
	if len(tok) < 2 || strings.ContainsAny(tok, " \t\n") {
		return false
	}
	low := strings.ToLower(tok)
	if isCommonKeyword(low) {
		return false
	}
	return strings.HasPrefix(tok, "-") ||
		strings.ContainsAny(tok, "/._:") ||
		hasMixedCaseOrUnderscore(tok)
}

func sessionStandingRuleLine(dir, cwd, sessionID, asked string, seen map[string]bool, prevAssistant ...string) (string, string) {
	effDir := dir
	if effDir == "" {
		effDir = index.DefaultDir()
	}
	var paths []string
	if cwd != "" {
		if root := sources.RepoRoot(cwd); root != "" {
			paths = append(paths, filepath.Join(root, ".deja", "live_rules.jsonl"))
		} else if len(cachedProjectNameCandidates(cwd)) > 0 {
			paths = append(paths, filepath.Join(cwd, ".deja", "live_rules.jsonl"))
		}
	}
	if effDir != "" {
		paths = append(paths, filepath.Join(effDir, "live_rules.jsonl"))
	}
	askedPaths, _ := extractLiveRulePathsAndSymbols(cwd, asked)
	prevAsst := ""
	if len(prevAssistant) > 0 {
		prevAsst = strings.TrimSpace(prevAssistant[0])
	}
	seenID := map[string]bool{}
	for _, p := range paths {
		for _, rec := range slices.Backward(readLiveRuleRecords(p)) {
			e := rec.RepoEntry
			if e.Text == "" || seenID[e.ID] || isMalformedStandingRuleText(e.Text) {
				continue
			}
			seenID[e.ID] = true
			isSameSession := sessionID != "" && e.From == sessionID
			isProjectFile := effDir == "" || p != filepath.Join(effDir, "live_rules.jsonl")
			if !isSameSession && !isProjectFile {
				continue
			}
			topicMatch := repoEntryMentionsCommand(asked, e.Text) ||
				commandTouchesRepoEntryFiles(asked, e.Files) ||
				mentionsAnySymbol(rec.Symbols, asked)
			if !topicMatch && len(askedPaths) == 0 && prevAsst != "" {
				topicMatch = repoEntryMentionsCommand(prevAsst, e.Text) ||
					mentionsAnySymbol(rec.Symbols, prevAsst)
			}
			// A session-wide turn-pair refusal ("Do not suggest <X>: ...") pinned
			// in the current session applies to any non-file-specific prompt in
			// that session regardless of language or topic vocabulary, without
			// firing when the user asks to edit an unrelated file.
			if !topicMatch && isSameSession && len(e.Files) == 0 && len(askedPaths) == 0 && strings.HasPrefix(e.Text, "Do not suggest ") {
				topicMatch = true
			}
			if !topicMatch {
				continue
			}
			tok := "srule:" + shortHash(e.Text+"|"+asked)
			if seen != nil && seen[tok] {
				continue
			}
			line := "User standing rule (do not violate or re-suggest): " + search.SafeText(clipDecision(e.Text, 240))
			return line, tok
		}
	}
	return "", ""
}

// isMalformedStandingRuleText reports whether a standing rule has a punctuation-only or empty target.
func isMalformedStandingRuleText(text string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return true
	}
	countLetters := func(v string) int {
		n := 0
		for _, r := range v {
			if unicode.IsLetter(r) {
				n++
			}
		}
		return n
	}
	if rest, ok := strings.CutPrefix(s, "Do not suggest "); ok {
		subj, body, hasColon := strings.Cut(rest, ":")
		if hasColon && (countLetters(subj) < 2 || countLetters(body) < 2) {
			return true
		}
		if !hasColon && countLetters(rest) < 2 {
			return true
		}
	}
	low := strings.ToLower(s)
	for _, prefix := range []string{"do not suggest ", "do not use ", "do not ", "don't use ", "don't ", "never use ", "never ", "always use ", "always ", "avoid "} {
		if rest, ok := strings.CutPrefix(low, prefix); ok {
			return countLetters(rest) < 2
		}
	}
	return countLetters(s) < 3
}

func extractLiveRulePathsAndSymbols(cwd, text string) ([]string, []string) {
	var paths, syms []string
	seenPath := map[string]bool{}
	seenSym := map[string]bool{}
	addPath := func(p string) {
		cp := filepath.ToSlash(filepath.Clean(strings.TrimSpace(p)))
		if cp != "" && cp != "." && !seenPath[cp] {
			seenPath[cp] = true
			paths = append(paths, cp)
		}
	}
	addSym := func(s string) {
		cs := strings.TrimSpace(s)
		if cs != "" && !seenSym[cs] {
			seenSym[cs] = true
			syms = append(syms, cs)
		}
	}
	if idx := strings.Index(text, ": "); idx > 0 && strings.HasPrefix(text, "Do not suggest ") {
		target := strings.TrimSpace(strings.TrimPrefix(text[:idx], "Do not suggest "))
		if isTechnicalRuleSymbol(target) {
			addSym(target)
			if bare := strings.TrimLeft(target, "-/"); len(bare) >= 2 && isTechnicalRuleSymbol(bare) {
				addSym(bare)
			}
		}
	}

	// 1. Backtick extraction
	s := text
	for {
		_, rest, ok := strings.Cut(s, "`")
		if !ok {
			break
		}
		item, after, ok := strings.Cut(rest, "`")
		if !ok {
			break
		}
		s = after
		if strings.Contains(item, "/") || isCodeFilePath(item) {
			addPath(item)
		} else if len(item) >= 2 {
			addSym(item)
		}
	}

	// 2. Word tokens for file names or paths
	words := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == ';' || r == ':' || r == '(' || r == ')'
	})
	root := sources.RepoRoot(cwd)
	if root == "" {
		root = cwd
	}
	for _, w := range words {
		wClean := strings.Trim(w, "`'\".,;()[]{}")
		if wClean == "" {
			continue
		}
		if isCodeFilePath(wClean) {
			addPath(wClean)
			continue
		}
		// Check if exists in cwd or root
		for _, base := range []string{cwd, root} {
			p := filepath.Join(base, wClean)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				addPath(wClean)
				break
			}
		}
	}
	return paths, syms
}

func isCodeFilePath(p string) bool {
	low := strings.ToLower(p)
	exts := []string{
		".go", ".ts", ".js", ".py", ".rs", ".json", ".yaml", ".yml",
		".toml", ".md", ".sh", ".c", ".h", ".cpp", ".html", ".css",
	}
	for _, ext := range exts {
		if strings.HasSuffix(low, ext) {
			return true
		}
	}
	if strings.HasPrefix(p, "/") && !strings.Contains(p[1:], "/") {
		return false
	}
	return strings.Contains(p, "/") && !strings.Contains(p, " ")
}

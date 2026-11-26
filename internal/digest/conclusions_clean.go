package digest

import (
	"strings"
	"unicode/utf8"
)

// extractConclusionLead picks the outcome section or opening conclusion
// sentences from raw, skipping celebratory transition chatter.
func extractConclusionLead(raw string, substantial, allowShortPreambleSecondary bool) (string, bool) {
	sub, fromOutcome := preferOutcomeMarkdownSection(raw, allowShortPreambleSecondary)
	cleaned := MessageText(stripMarkdownHeadings(sub))
	return decisionLeadFromClean(cleaned, substantial, fromOutcome), fromOutcome
}

func decisionLeadFromClean(cleanText string, substantial, fromOutcome bool) string {
	head := firstSentences(cleanText, 2)
	if substantial {
		head = substantialLead(cleanText, head)
	}
	if head != "" && isCelebratoryBoilerplate(head) {
		if tail := skipCelebratoryPrefix(cleanText); tail != "" {
			nextHead := firstSentences(tail, 2)
			if substantial {
				nextHead = substantialLead(tail, nextHead)
			}
			if nextHead != "" && !isCelebratoryBoilerplate(nextHead) {
				head = nextHead
				cleanText = tail
			}
		}
	}
	if head == "" || fromOutcome || CarriesDecision(head) {
		return head
	}
	for _, sent := range sentencesOf(cleanText) {
		// "Fixed the same way." carries a marker and nothing else.
		if isCelebratoryBoilerplate(sent) {
			continue
		}
		if CarriesDecision(sent) && (!substantial || !thinSentence(sent)) {
			return sent
		}
	}
	return head
}

var primaryOutcomeSectionLabels = map[string]bool{
	"root cause": true, "the root cause": true,
	"solution": true, "the solution": true,
	"resolution": true, "what was fixed": true,
	"fix summary": true, "changes made": true,
	"причина": true, "корневая причина": true,
	"решение": true, "итог": true,
	"резюме исправления": true, "что исправлено": true, "что изменилось": true,
}

var secondaryOutcomeSectionLabels = map[string]bool{
	"fix": true, "the fix": true,
	"исправление": true, "фикс": true,
}

func sectionLabelAtLine(trimmed string) (label string, inlineLine string, ok bool) {
	if strings.HasPrefix(trimmed, "#") {
		inner := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if unwrapped, uok := stripStandaloneBoldHeading(inner); uok {
			inner = unwrapped
		}
		low := strings.ToLower(strings.TrimSpace(strings.TrimRight(inner, ":")))
		return low, "", low != ""
	}
	if inner, uok := stripStandaloneBoldHeading(trimmed); uok {
		low := strings.ToLower(inner)
		return low, "", low != ""
	}
	s := strings.TrimSpace(trimmed)
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "- "), "* "))
	if strings.HasPrefix(s, "**") {
		if end := strings.Index(s[2:], "**"); end > 0 {
			rawLbl := strings.TrimSpace(s[2 : 2+end])
			after := strings.TrimSpace(s[2+end+2:])
			if strings.HasPrefix(after, ":") || strings.HasSuffix(rawLbl, ":") {
				low := strings.ToLower(strings.TrimSpace(strings.TrimRight(rawLbl, ":")))
				if low != "" {
					return low, s, true
				}
			}
		}
	}
	return "", "", false
}

func stripFencedCodeBlocks(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if !inFence {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func preferOutcomeMarkdownSection(text string, includeSecondaryOnShortPreamble bool) (string, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return text, false
	}
	inFence := false
	seenNonEmpty := false
	firstIdx := -1
	firstReplacement := ""
	headOutcome := false
	trySection := func(idx int, replacement string) (string, bool) {
		tailLines := append([]string(nil), lines[idx:]...)
		if replacement != "" {
			tailLines[0] = replacement
		}
		tail := strings.Join(tailLines, "\n")
		prose := strings.TrimSpace(MessageText(stripMarkdownHeadings(stripFencedCodeBlocks(tail))))
		if len(strings.Fields(prose)) < 5 || !SaysSomething(prose) {
			return "", false
		}
		return tail, true
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || trimmed == "" {
			continue
		}
		lbl, inline, ok := sectionLabelAtLine(trimmed)
		if ok && (primaryOutcomeSectionLabels[lbl] || secondaryOutcomeSectionLabels[lbl]) {
			switch {
			case !seenNonEmpty:
				headOutcome = true
				if firstIdx < 0 && primaryOutcomeSectionLabels[lbl] {
					firstIdx = i
					firstReplacement = inline
				}
			case primaryOutcomeSectionLabels[lbl]:
				if tail, valid := trySection(i, inline); valid {
					return tail, true
				}
			case secondaryOutcomeSectionLabels[lbl] && firstIdx < 0:
				before := strings.TrimSpace(MessageText(stripMarkdownHeadings(stripFencedCodeBlocks(strings.Join(lines[:i], "\n")))))
				if includeSecondaryOnShortPreamble || len(sentencesOf(before)) >= 2 {
					if tail, valid := trySection(i, inline); valid {
						return tail, true
					}
				}
			}
		}
		seenNonEmpty = true
	}
	if firstIdx == 0 {
		if tail, valid := trySection(0, firstReplacement); valid {
			return tail, true
		}
	}
	return text, headOutcome
}

// IsCelebratoryBoilerplate reports whether s opens with celebratory or transition chatter.
func IsCelebratoryBoilerplate(s string) bool {
	return isCelebratoryBoilerplate(s)
}

func isCelebratoryBoilerplate(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{
		"perfect!", "excellent!", "great!", "awesome!", "wonderful!", "done!",
		"perfect,", "excellent,", "great,",
		"i've successfully ", "i have successfully ", "i successfully ",
		"successfully fixed ", "the fix has been implemented",
		"the issue has been resolved", "all tests pass", "all tests are passing",
		"the fix works", "the fix is working", "the fix is complete", "the fix looks good",
		"now let me test", "now let's test", "now let me run", "now let's run",
		"now let me create a", "now let's create a",
		"now let me add a test", "now let's add a test",
		"now let me check", "now let's check",
		"now let me verify", "now let's verify",
		"let me test the fix", "let's test the fix", "let's test this fix",
		"let me implement the fix", "now let me implement the fix",
		"i'll help you ", "i will help you ", "let me help you ",
		"let me start by ", "let's start by ", "let me look into ", "let me investigate ",
		"отлично!", "супер!", "прекрасно!", "готово!",
		"все тесты проходят", "теперь проверим", "теперь запустим",
	} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

var structuralHeadingLabels = map[string]bool{
	"": true, "summary": true, "bug summary": true, "fix summary": true, "conclusion": true, "overview": true,
	"changes": true, "changes made": true, "verification": true,
	"the problem": true, "problem": true, "the issue": true, "issue": true,
	"the fix": true, "fix": true, "the solution": true, "solution": true,
	"root cause": true, "background": true, "context": true, "details": true,
	"findings": true, "explanation": true, "what was fixed": true,
	"what changed": true, "files modified": true, "files changed": true,
	"test results": true, "tests": true,
	"итог": true, "резюме": true, "проблема": true, "описание проблемы": true,
	"причина": true, "корневая причина": true, "решение": true, "исправление": true,
	"фикс": true, "изменения": true, "проверка": true, "тестирование": true,
	"тесты": true, "детали": true, "контекст": true,
}

var metaIntroPrefixes = []string{
	"here's what ", "here is what ",
	"here's a summary", "here is a summary",
	"here are the changes",
	"here's what was done", "here is what was done",
	"below is what ", "below is a summary",
	"let me create a summary", "let me provide a summary", "let me summarize",
	"this is what i found",
	"what i found", "what was done", "what changed",
	"вот что ", "краткий итог", "ниже привед", "что было сделано",
}

func stripMetaIntroClause(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasSuffix(trimmed, ":") {
		return line
	}
	low := strings.ToLower(trimmed)
	for _, p := range metaIntroPrefixes {
		if strings.HasPrefix(low, p) {
			return ""
		}
	}
	cutAt := -1
	for _, sep := range []string{". ", "! ", "? "} {
		for _, p := range metaIntroPrefixes {
			if idx := strings.Index(low, sep+p); idx >= 0 {
				cand := idx + 1
				if cutAt < 0 || cand < cutAt {
					cutAt = cand
				}
			}
		}
	}
	if cutAt >= 0 {
		return strings.TrimSpace(trimmed[:cutAt])
	}
	return line
}

func stripStandaloneBoldHeading(trimmed string) (string, bool) {
	var inner string
	switch {
	case strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**:"):
		inner = trimmed[2 : len(trimmed)-3]
	case strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**") && len(trimmed) > 4:
		inner = trimmed[2 : len(trimmed)-2]
	default:
		return "", false
	}
	if strings.Contains(inner, "**") {
		return "", false
	}
	inner = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(inner), ":"))
	if inner == "" {
		return "", false
	}
	return inner, true
}

func ensureHeadingTerminator(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	last, _ := utf8.DecodeLastRuneInString(s)
	switch last {
	case '.', '!', '?', ':', '\u3002', '\uff01', '\uff1f':
		return s
	}
	return s + "."
}

func stripMarkdownHeadings(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if cleaned := stripMetaIntroClause(line); cleaned != line {
			if strings.TrimSpace(cleaned) == "" {
				continue
			}
			line = cleaned
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			inner := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if unwrapped, ok := stripStandaloneBoldHeading(inner); ok {
				inner = unwrapped
			}
			low := strings.ToLower(strings.TrimSpace(strings.TrimRight(inner, ":")))
			if structuralHeadingLabels[low] {
				continue
			}
			out = append(out, ensureHeadingTerminator(inner))
			continue
		}
		if inner, ok := stripStandaloneBoldHeading(trimmed); ok {
			if structuralHeadingLabels[strings.ToLower(inner)] {
				continue
			}
			out = append(out, ensureHeadingTerminator(inner))
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// StripAssistantBoilerplate strips markdown heading markers and leading
// celebratory or meta-intro lines when subsequent non-empty prose follows.
func StripAssistantBoilerplate(text string) string {
	text = stripMarkdownHeadings(text)
	lines := strings.Split(text, "\n")
	hasLaterNonEmpty := func(idx int) bool {
		for j := idx + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) != "" {
				return true
			}
		}
		return false
	}
	start := 0
	stripped := false
	for i := range lines {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			if stripped {
				start = i + 1
			}
			continue
		}
		trimmed := strings.TrimSpace(skipCelebratoryPrefix(t))
		if trimmed != t && trimmed != "" && !isCelebratoryBoilerplate(trimmed) {
			lines[i] = trimmed
			break
		}
		if hasLaterNonEmpty(i) && (isCelebratoryBoilerplate(t) || trimmed == "" || isCelebratoryBoilerplate(trimmed)) {
			start = i + 1
			stripped = true
			continue
		}
		break
	}
	return strings.Join(lines[start:], "\n")
}

func skipCelebratoryPrefix(text string) string {
	sents := sentencesOf(text)
	for i, sent := range sents {
		if isCelebratoryBoilerplate(sent) {
			continue
		}
		// Rebuild from the first non-celebratory sentence onwards.
		if idx := strings.Index(text, sent); idx >= 0 {
			return strings.TrimSpace(text[idx:])
		}
		return strings.TrimSpace(strings.Join(sents[i:], " "))
	}
	return ""
}

// sentencesOf splits on the same stops firstSentences counts, so the two agree
// on what a sentence is — including the space-after rule that keeps "v1.2"
// whole and the CJK stops that have no space after them.
func sentencesOf(s string) []string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i, r := range s {
		switch {
		case r == '.' || r == '!' || r == '?':
			if i+1 < len(s) && s[i+1] != ' ' {
				continue
			}
		case isCJKSentenceEnd(r):
		default:
			continue
		}
		end := i + utf8.RuneLen(r)
		if r == '.' && trailingListMarker(s[start:end]) {
			continue
		}
		sent := strings.TrimSpace(s[start:end])
		// A list item's number is not a sentence of its own, and the item
		// after it reads the same without it.
		if sent != "" && !listMarker(sent) {
			out = append(out, sent)
		}
		start = end
	}
	if sent := strings.TrimSpace(s[start:]); sent != "" {
		out = append(out, sent)
	}
	return out
}

// isCJKSentenceEnd is the full stop, exclamation and question mark Chinese and
// Japanese write. They are separate characters from the ASCII ones, so a
// message in either script contained no sentence end as far as this file was
// concerned (#1319).
//
// The fullwidth full stop U+FF0E is deliberately absent: it is also how a
// decimal point is written in fullwidth digits, and the space rule that keeps
// "v1.2" together cannot help here — these scripts put no space after a stop,
// so the rule has to be dropped for them. The enumeration comma and the
// fullwidth semicolon are absent for the plainer reason that neither ends a
// sentence.
func isCJKSentenceEnd(r rune) bool {
	switch r {
	case '\u3002', '\uff01', '\uff1f':
		return true
	}
	return false
}

// markedCut is a conclusion held to a budget it does not fit, ending in the
// marker that says so. Rune-safe, and it gives back nothing when the budget
// leaves no room for a readable line rather than a bare marker.
func markedCut(line string, budget int) string {
	const mark = "…"
	if budget <= len(mark)+8 {
		return ""
	}
	cut := strings.TrimRight(UTF8SafeCut(line, budget-len(mark)), " \t")
	if cut == "" {
		return ""
	}
	return cut + mark
}

// firstSentences keeps the opening n sentences of a message: a conclusion
// states itself up front and then explains, and recall pays for every byte.
func firstSentences(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return ""
	}
	count, start := 0, 0
	for i, r := range s {
		switch {
		case r == '.' || r == '!' || r == '?':
			// "v1.2" and "e.g." are not sentence ends: require a space after.
			if i+1 < len(s) && s[i+1] != ' ' {
				continue
			}
		case isCJKSentenceEnd(r):
			// No space rule here: these scripts put none after the stop, so
			// requiring one would find no sentence at all.
		default:
			continue
		}
		end := i + utf8.RuneLen(r)
		if listMarker(s[start:end]) || (r == '.' && trailingListMarker(s[start:end])) {
			continue
		}
		start = end
		count++
		if count == n {
			return strings.TrimSpace(s[:i+utf8.RuneLen(r)])
		}
	}
	const most = 240
	if len(s) > most {
		return strings.TrimSpace(UTF8SafeCut(s, most)) + "…"
	}
	return s
}

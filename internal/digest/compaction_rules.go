package digest

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// Standing instructions are the constraints a user gave earlier in a session
// and expects to hold for the rest of it: "never touch internal/legacy", "run
// tests only through ./check.sh". A host summary keeps the task and drops
// these, and the packet's objective is the newest turn, which in a long
// session is "Next: T13, T14 and T15" and nothing else. On a 16-turn stand
// whose rules were stated once in turn 1, the agent ran `go test` itself after
// a compaction with the packet delivered and without it alike.
const (
	standingRulesMax   = 8
	standingRuleBytes  = 240
	standingRulesBytes = 1000
	// A user turn longer than this is a paste — a log, a spec, a file — and a
	// "never" inside it is the pasted text talking, not the user.
	standingTurnMax = 6 << 10
	// User text only; the turns are small next to the tool output around them.
	standingScanBytes = 1 << 20
	goalBytes         = 400
)

var (
	// A sentence that opens with one of these is an instruction about how to
	// work, not a description of what happened.
	standingStartRE = regexp.MustCompile(`(?i)^(?:never|don't|dont|do not|always|avoid|must not|must|make sure|from now on|each time|every time|whenever|stop using|keep using|prefer\b|use \S+(?: \S+)? (?:instead of|rather than|not)\b|` +
		`никогда|всегда|нельзя|обязательно|запомни|впредь|с этого момента|каждый раз|только не|ни в коем случае|не надо|не нужно|не смей|используй \S+(?: \S+)? (?:вместо|а не)\b|` +
		// "не трогай", "не запускай", "не пиши": a negated imperative. The
		// ending is what separates it from "не работает", "не знаю", "не понял".
		`(?:ничего\s+|никуда\s+|нигде\s+|никому\s+)?не \p{L}+(?:й|йте|ь|ьте|и|ите)(?:сь|ся)?(?:$|[^\p{L}])|` +
		`永远不要|千万不要|绝对不要|不要|别|禁止|严禁|必须|务必|一定要|总是|始终|记住|以后|从现在起|每次都|优先使用)`)
	// Optional leading acknowledgement + conjunction ("ок, но ", "да, только ",
	// "ok, but ", "好，但") before a standing instruction in a short user reply.
	standingLeadAckRE = regexp.MustCompile(`(?i)^(?:(?:ок|окей|да|ладно|хорошо|ага|угу|понял|ok|okay|yes|yeah|sure|alright|got it|好|好的|可以|行|没问题)[,\s]+)*(?:(?:но|только|и|а|просто|главное|при этом|but|just|and|only|不过|但是|但|只是)[,\s]+)+`)
	// The ones that open the same way and instruct nothing.
	standingNotRE = regexp.MustCompile(`(?i)^(?:never mind|nevermind|don't worry|dont worry|do not worry|don't know|dont know|do not know|don't think|dont think|don't see|don't get|always has|always had|always was|always were|always been|не знаю|не помню|не понимаю|не уверен|не важно|не страшно|не надо было|не нужно было|` +
		// "не удалось", "не нашлось": a past tense whose ending passes for an imperative.
		`не \p{L}+(?:ось|ась|ись)(?:$|[^\p{L}])|` +
		`不要紧|没关系|不用谢|不知道|不清楚|不确定|不重要)`)
	// An instruction keeps its list item; the item is rewritten in full.
	standingItemRE       = regexp.MustCompile(`^\s*(?:[-*+•]\s+|\d{1,2}[.)]\s+)`)
	standingFenceRE      = regexp.MustCompile("(?s)```.*?```")
	standingTagRE        = regexp.MustCompile(`(?s)<([A-Za-z][\w-]*)(?:\s[^<>]*)?>.*?</([A-Za-z][\w-]*)>`)
	standingSentRE       = regexp.MustCompile(`([.!;]\s+|[。！；]\s*)`)
	standingProvenanceRE = regexp.MustCompile(`\s*\[[A-Za-z0-9_-]+:[^\]]+/[A-Za-z_-]+(?:\s+@[^\]]+)?\]\s*$`)
)

// ExtractStandingRules returns the user's standing instructions from every
// user turn of the session, oldest first, merged with the ones the last
// compaction carried — a transcript read from its tail may no longer reach
// the turn that stated them.
func ExtractStandingRules(s model.Session, prev []model.ContextFact) []model.ContextFact {
	var out []model.ContextFact
	seen := map[string]bool{}
	size := 0
	add := func(f model.ContextFact) {
		key := normalizedContextText(f.Text)
		if key == "" || seen[key] || len(out) == standingRulesMax || size+len(f.Text) > standingRulesBytes {
			return
		}
		seen[key] = true
		size += len(f.Text)
		out = append(out, f)
	}
	for _, f := range prev {
		add(f)
	}
	scanned := 0
	for _, m := range s.Messages {
		if m.Role != "user" {
			continue
		}
		scanned += len(m.Text)
		if scanned > standingScanBytes {
			break
		}
		if len(m.Text) > standingTurnMax || IsAgentArtifact(m.Text) {
			continue
		}
		for _, text := range standingLines(m.Text) {
			add(model.ContextFact{Text: text, Provenance: contextRef(s, m)})
		}
	}
	return out
}

// standingLines are the instructions in one user turn.
func standingLines(text string) []string {
	text, _ = redact.Text(text)
	text = redact.SafeForDisplay(text)
	text = standingFenceRE.ReplaceAllString(text, "\n")
	text = stripTagBlocks(text)
	var items, hits []string
	listHits := 0
	inRulesBlock := false
	flushItems := func() {
		if len(items) == 0 {
			inRulesBlock = false
			return
		}
		if inRulesBlock || (listHits >= 2 && listHits*3 >= len(items)*2) {
			hits = append(hits, items...)
		} else {
			for _, it := range items {
				for _, sentence := range standingSentences(it) {
					if standingRule(sentence) && fitsStanding(sentence) {
						hits = append(hits, sentence)
					}
				}
			}
		}
		items = nil
		listHits = 0
		inRulesBlock = false
	}
	for line := range strings.SplitSeq(text, "\n") {
		raw := strings.TrimSpace(line)
		if raw == "" || strings.HasPrefix(raw, ">") || strings.HasPrefix(raw, "|") {
			flushItems()
			continue
		}
		if strings.HasPrefix(raw, "Standing instructions ") {
			flushItems()
			inRulesBlock = true
			continue
		}
		item := standingItemRE.MatchString(raw)
		if !item {
			flushItems()
		}
		line := strings.TrimSpace(standingItemRE.ReplaceAllString(raw, ""))
		line = strings.ReplaceAll(line, "**", "")
		if isDejaPacketItem(line) {
			continue
		}
		hit := slices.ContainsFunc(standingSentences(line), standingRule)
		switch {
		case item && fitsStanding(line):
			items = append(items, line)
			if hit {
				listHits++
			}
		case hit:
			for _, sentence := range standingSentences(line) {
				if standingRule(sentence) && fitsStanding(sentence) {
					hits = append(hits, sentence)
				}
			}
		}
	}
	flushItems()
	return hits
}

func isDejaPacketItem(line string) bool {
	if standingProvenanceRE.MatchString(line) {
		return true
	}
	for _, prefix := range []string{
		"Session started with:",
		"Latest request:",
		"Latest request before compaction:",
		"[passed] ",
		"[failed] ",
		"[recorded] ",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func standingSentences(line string) []string {
	var out []string
	for s := range strings.SplitSeq(standingSentRE.ReplaceAllString(line, "$1\n"), "\n") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func standingRule(sentence string) bool {
	s := strings.TrimLeft(sentence, "\"'«“`(「『")
	trimmed := strings.TrimRight(s, " \"'»”)」』")
	if strings.HasSuffix(trimmed, "?") || strings.HasSuffix(trimmed, "？") {
		return false
	}
	folded := cjkfold.String(s)
	folded = standingLeadAckRE.ReplaceAllString(folded, "")
	return standingStartRE.MatchString(folded) && !standingNotRE.MatchString(folded)
}

func onlyStandingRules(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || len(standingLines(text)) == 0 {
		return false
	}
	sents := standingSentences(text)
	if len(sents) == 0 {
		return false
	}
	for _, s := range sents {
		if !standingRule(s) {
			return false
		}
	}
	return true
}

func fitsStanding(s string) bool {
	if len(s) > standingRuleBytes {
		return false
	}
	return utf8.RuneCountInString(s) >= 12 || cjkfold.CountCJK(s) >= 6
}

// stripTagBlocks drops XML-wrapped plumbing a harness puts inside a user turn
// — a reminder, a pasted block, a command echo — whose wording is not the
// user's.
func stripTagBlocks(text string) string {
	for range 8 {
		loc := standingTagRE.FindStringSubmatchIndex(text)
		if loc == nil {
			return text
		}
		if text[loc[2]:loc[3]] != text[loc[4]:loc[5]] {
			// Mismatched names: drop the opening tag alone and look again.
			text = text[:loc[0]] + text[loc[3]:]
			continue
		}
		text = text[:loc[0]] + "\n" + text[loc[1]:]
	}
	return text
}

// sessionGoal is the first thing the user asked for in the session, its first
// paragraph. The objective is the newest request; in a session worked in
// steps that is "Next: T13" and says nothing of what the steps add up to.
func sessionGoal(s model.Session) model.ContextFact {
	scanned := 0
	for _, m := range s.Messages {
		scanned += len(m.Text)
		if scanned > maxContextScanBytes {
			break
		}
		if m.Role != "user" || IsAgentArtifact(m.Text) {
			continue
		}
		raw := unwrapHandoffPrompt(m.Text, true)
		text := strings.TrimSpace(stripTagBlocks(standingFenceRE.ReplaceAllString(raw, " ")))
		if i := strings.Index(text, "\n\n"); i > 0 {
			text = text[:i]
		}
		text = contextProse(text, goalBytes)
		if text != "" && !trivialContinuation(text) && !notARequest(raw) {
			return model.ContextFact{Text: text, Provenance: contextRef(s, m)}
		}
	}
	return model.ContextFact{}
}

// unwrapHandoffPrompt extracts the underlying goal or latest request from a
// `deja handoff` prompt so a session started from a handoff does not report
// the handoff framing sentence ("You are picking up work handed off from...")
// as its own objective or goal.
func unwrapHandoffPrompt(text string, wantGoal bool) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "You are picking up work handed off from ") && !strings.Contains(trimmed, handoffQuoteOpen) {
		return text
	}
	if !wantGoal {
		if _, after, ok := strings.Cut(trimmed, handoffQuoteClose); ok {
			tail := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(after), cutMark))
			if tail != "" && !strings.HasPrefix(tail, "paste this into a new chat") {
				return tail
			}
		}
	}
	var started, latest string
	if _, afterObj, ok := strings.Cut(trimmed, "\nObjective\n"); ok {
		for line := range strings.SplitSeq(afterObj, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.HasPrefix(line, "- ") {
				break
			}
			item := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			item = strings.TrimSpace(standingProvenanceRE.ReplaceAllString(item, ""))
			switch {
			case strings.HasPrefix(item, "Session started with:"):
				started = strings.TrimSpace(strings.TrimPrefix(item, "Session started with:"))
			case strings.HasPrefix(item, "Latest request before compaction:"):
				latest = strings.TrimSpace(strings.TrimPrefix(item, "Latest request before compaction:"))
			case strings.HasPrefix(item, "Latest request:"):
				latest = strings.TrimSpace(strings.TrimPrefix(item, "Latest request:"))
			case latest == "":
				latest = item
			}
		}
	}
	if wantGoal {
		if started != "" {
			return started
		}
		return latest
	}
	if latest != "" && !trivialContinuation(latest) && !notARequest(latest) {
		return latest
	}
	return started
}

// sameRequest reports whether the goal is the objective, or the start of it:
// in a one-request session the first turn is the newest one too.
func sameRequest(goal, objective string) bool {
	g := normalizedContextText(strings.TrimSuffix(goal, cutMark))
	o := normalizedContextText(objective)
	return g == "" || o == "" || strings.HasPrefix(o, g) || strings.HasPrefix(g, strings.TrimSuffix(o, normalizedContextText(cutMark)))
}

// The section prints the user's words without a provenance tag per line: the
// tag is ~70 bytes and eight of them would cost more than the rules.
const rulesHeader = "Standing instructions the user gave earlier in this session (quoted; a later turn may have changed them)"

func addRulesSection(b *strings.Builder, omitted *bool, limit int, rules []model.ContextFact) {
	if len(rules) == 0 {
		return
	}
	if b.Len()+len("\n"+rulesHeader+"\n") > limit {
		*omitted = true
		return
	}
	b.WriteString("\n" + rulesHeader + "\n")
	for _, r := range rules {
		line := "- " + r.Text + "\n"
		if b.Len()+len(line) > limit {
			*omitted = true
			return
		}
		b.WriteString(line)
	}
}

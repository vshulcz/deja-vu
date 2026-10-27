package evidence

import (
	"regexp"
	"slices"
	"strings"
)

// An order is a sentence that tells whoever reads it to run something. Recall
// cuts an order only from text no person typed for this project: a tool's
// output, a paste, a parent agent's prompt, a headless run, another project's
// session. On the quillmark stand a labelled line ("Prompt written by a parent
// agent: ... every time you edit docs/install.md, run ./scripts/linkcheck.sh")
// was still run in 4 of 6 runs. What a person typed and what a model
// concluded are never cut: "you must run go mod tidy after changing go.mod"
// is the remedy recall exists to carry.

// sentences splits text after line breaks and clause ends; joined, the pieces
// are text again.
func sentences(text string) []string {
	var out []string
	for l := range strings.SplitAfterSeq(text, "\n") {
		start := 0
		for _, e := range clauseEndRE.FindAllStringIndex(l, -1) {
			out = append(out, l[start:e[1]])
			start = e[1]
		}
		if start < len(l) {
			out = append(out, l[start:])
		}
	}
	return out
}

// ordersScanRunes bounds how much of one message Orders reads: an order sits
// in what a person typed or near the top of a tool's output, not deep in a
// log.
const ordersScanRunes = 20000

var (
	// When an order applies, and that it always does. Narrower than ruleRE:
	// "the golden test never runs, make test skips it" describes a problem.
	orderWhenRE = regexp.MustCompile(`(?i)\b(rule|always|every time|each time|whenever|from now on|before you|after you|before (?:any|each|every|calling|committing|merging|pushing|you)|after (?:any|each|every|editing|changing|you)|must|make sure|don'?t forget|is not enough)\b|всегда|каждый раз|перед тем|прежде чем|после каждо|обязательно|правило|не забывай`)
	// That it runs something.
	orderRunRE = regexp.MustCompile(`(?i)\b(?:run|rerun|re-run|execute|invoke)\b|запусти|запускай|прогони`)
)

func order(sn string) bool {
	t := strings.TrimSpace(sn)
	return t != "" && !strings.HasSuffix(t, "?") && orderWhenRE.MatchString(t) && orderRunRE.MatchString(t) && len(Commands(t)) > 0
}

func clip(text string) string {
	if r := []rune(text); len(r) > ordersScanRunes {
		return string(r[:ordersScanRunes])
	}
	return text
}

// Orders are the commands text tells its reader to run: in a sentence that
// says when, and says to run it.
func Orders(text string) []string {
	var out []string
	for _, sn := range OrderSentences(text) {
		for _, c := range Commands(sn) {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

// OrderSentences are the sentences of text that are orders, trimmed.
func OrderSentences(text string) []string {
	text = clip(text)
	if !orderWhenRE.MatchString(text) {
		return nil
	}
	var out []string
	for _, sn := range sentences(text) {
		if order(sn) {
			out = append(out, strings.TrimSpace(sn))
		}
	}
	return out
}

// LeftOut stands where CutOrders removed something.
const LeftOut = "[instruction to agents left out]"

// CutOrders is text without its orders: every one when all is set, else only
// those planted holds (the orders a parser found outside what the person
// typed).
func CutOrders(text string, all bool, planted []string) string {
	if !all && len(planted) == 0 {
		return text
	}
	return cutOrders(text, func(sn string) bool { return all || inPlanted(sn, planted) }, nil)
}

// CutAgentOrders is a tool's output without the orders it gives AI agents.
// A program telling whoever ran it how to get past its own error ("needs
// privileges; you must run sudo foo doctor") is the remedy, not something
// planted for an agent: only text that names its reader as an agent or an
// assistant is cut. The naming carries to the end of its paragraph, so a
// "Note for AI agents:" heading covers the list under it.
func CutAgentOrders(text string) string {
	addressed := false
	return cutOrders(text, func(string) bool { return addressed }, func(sn string) {
		if strings.TrimSpace(sn) == "" {
			addressed = false
		} else if agentReaderRE.MatchString(sn) {
			addressed = true
		}
	})
}

// agentReaderRE names an AI reader. A word joined by a hyphen ("ssh-agent",
// "user-agent") names a program, not a reader.
var agentReaderRE = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_-])(?:agents?|assistants?|AI|LLMs?|Claude|Codex|Copilot|Gemini)(?:$|[^\p{L}\p{N}_-])|агент|ассистент`)

// cutOrders cuts the order sentences pick accepts. seen, when set, is shown
// every sentence first.
func cutOrders(text string, pick func(string) bool, seen func(string)) string {
	if len(OrderSentences(text)) == 0 {
		return text
	}
	var b strings.Builder
	cut := false
	for _, sn := range sentences(text) {
		if seen != nil {
			seen(sn)
		}
		if order(sn) && pick(sn) {
			// A log pasted with no sentence break carries the order at its
			// end: what is ahead of the rule's wording stays.
			if !cut {
				if loc := orderWhenRE.FindStringIndex(sn); len(loc) == 2 && !namesAny(sn[:loc[0]], Commands(sn)) {
					b.WriteString(sn[:loc[0]])
				}
				b.WriteString(LeftOut + " ")
			}
			cut = true
			continue
		}
		cut = false
		b.WriteString(sn)
	}
	return strings.TrimSpace(b.String())
}

// inPlanted reports whether sn is one of the planted orders. A shown line is
// often clipped and joined with the text around it, so the order's own words,
// from where it says when, are matched by their opening.
func inPlanted(sn string, planted []string) bool {
	n := Norm(sn)
	if n == "" {
		return false
	}
	for _, p := range planted {
		pn := Norm(p)
		if pn == "" {
			continue
		}
		if strings.Contains(pn, n) || strings.Contains(n, pn) {
			return true
		}
		if pk := prefix(Norm(orderTail(p)), plantedKeyRunes); len([]rune(pk)) >= plantedKeyRunes/2 && strings.Contains(n, pk) {
			return true
		}
		if sk := prefix(Norm(strings.TrimSuffix(strings.TrimSpace(orderTail(sn)), "…")), plantedKeyRunes); len([]rune(sk)) >= plantedKeyRunes/2 && strings.Contains(pn, sk) {
			return true
		}
	}
	return false
}

// plantedKeyRunes is how much of an order's opening identifies it.
const plantedKeyRunes = 40

// orderTail is s from where it says when an order applies.
func orderTail(s string) string {
	if loc := orderWhenRE.FindStringIndex(s); len(loc) == 2 {
		return s[loc[0]:]
	}
	return s
}

func namesAny(s string, cmds []string) bool {
	low := strings.ToLower(s)
	for _, c := range cmds {
		if strings.Contains(low, c) {
			return true
		}
	}
	return false
}

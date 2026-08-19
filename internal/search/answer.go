package search

import (
	"strings"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// answerAfter returns the assistant reply that followed a matched user turn,
// trimmed to the part worth carrying.
//
// A recall query describes a symptom, so the user turn always scores highest on
// term overlap and its snippet hands back a restatement of the question. Asked
// about a real incident, an agent said so out loud: "the recall only preserved
// the incident title, not the exact fix". The fix was in the very next message.
// AnswerAfter is called from the recall path rather than from scoring: search
// returns only the messages that matched, so the reply does not exist in memory
// at scoring time and has to be read back from the index.
func AnswerAfter(messages []model.Message, i int) string {
	for j := i + 1; j < len(messages) && j <= i+3; j++ {
		m := messages[j]
		if m.Role == "user" {
			// The user spoke again before any answer; there is no reply to
			// attach, and reaching further would attach someone else's.
			return ""
		}
		if m.Role != "assistant" {
			continue
		}
		if text := DecisionText(m.Text); text != "" {
			return text
		}
	}
	return ""
}

// decisionPhrases mark the sentence a reader actually wants. They are the
// shapes engineers use when recording an outcome, in transcripts across every
// harness deja indexes.
var decisionPhrases = []string{
	"we pinned", "we moved", "we dropped", "we switched", "we chose",
	"decision:", "fixed by", "fixed it by", "the fix", "root cause",
	"turned out", "traced it to", "instead of", "the cause was",
	"resolved by", "worked around", "we set", "we added",
}

// decisionText picks the sentence that records the outcome, falling back to the
// opening of the reply. A reply is usually diagnosis-first, so the opening is a
// reasonable second choice — but a decision sentence anywhere in it beats it.
func DecisionText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	sentences := splitSentences(text)
	low := make([]string, len(sentences))
	for i, s := range sentences {
		low[i] = strings.ToLower(s)
	}
	for i, s := range low {
		for _, phrase := range decisionPhrases {
			if strings.Contains(s, phrase) {
				out := sentences[i]
				// A decision often needs the sentence after it to make sense
				// ("We pinned pgx to 5.4.3. Revisit when 1.24 ships.").
				if i+1 < len(sentences) && len(out)+len(sentences[i+1]) < answerCap {
					out += " " + sentences[i+1]
				}
				return clip(out)
			}
		}
	}
	return clip(text)
}

const answerCap = 260

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= answerCap {
		return s
	}
	cut := answerCap
	for cut > 0 && !isBoundary(s[cut]) {
		cut--
	}
	if cut == 0 {
		// No word boundary anywhere in the first 260 bytes, which is every
		// answer written in Chinese, Japanese or Korean — those scripts put no
		// spaces between words. Cutting at the cap split the character sitting
		// on it, and the invalid byte went into the recall payload an agent
		// reads: measured on a store of eight Chinese sessions, the `→ ` line
		// under the top hit ended in half a character (#1319).
		cut = answerCap
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		// Reaching zero means the first 260 bytes hold no character start at
		// all, which stored text cannot be — records are decoded as UTF-8 and
		// re-checked on the way in. If it ever happens, the mark alone is the
		// honest answer: the old cut returned those 260 bytes and handed an
		// agent a line it could not read.
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

func isBoundary(b byte) bool { return b == ' ' || b == '\n' || b == '\t' }

func splitSentences(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '.' && text[i] != '!' && text[i] != '?' && text[i] != '\n' {
			continue
		}
		// A period inside a version or a path is not a sentence end.
		if text[i] == '.' && i+1 < len(text) && !isBoundary(text[i+1]) {
			continue
		}
		if s := strings.TrimSpace(text[start : i+1]); s != "" {
			out = append(out, s)
		}
		start = i + 1
	}
	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

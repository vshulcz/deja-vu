package digest

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// HandoffAsk is what a prompt asked to continue: session ids it names, or the
// harness whose session it means.
type HandoffAsk struct {
	IDs     []string
	Harness string
}

// handoffVerbs are the ways a person asks for another session's work to be
// carried on here. Stems, so "перенеси" and "перенос" and "продолжи" match.
var handoffVerbs = []string{
	"continue", "resume", "pick up", "picking up", "carry on", "take over", "handoff", "hand off", "handed off",
	"перенес", "перенос", "продолж", "подхвати", "подхват", "возьми",
}

// handoffNouns name the thing being carried on: a session, a chat.
var handoffNouns = []string{"session", "chat", "conversation", "сесси", "чат", "диалог", "переписк"}

// handoffVerbReach is how many words apart the verb and the session may be. "у
// меня была сессия claude про X, найди её — мне нужно в неё зайти, чтобы
// продолжить" names a session and says continue, and is a search: the two are
// a sentence apart. "перенеси в opencode сессию claude" has them three apart.
const handoffVerbReach = 6

// cliFlag is a flag on a pasted command line. "claude --resume <id>" inside a
// request to edit that session is not a request to continue it.
var cliFlag = regexp.MustCompile(`(^|\s)--?[A-Za-z][\w-]*`)

// HandoffIntent reports whether a prompt asks to continue another session's
// work, and which one. It is deliberately narrow: a continuation guessed from
// timing and folder fired 72 times for 2 real switches on one machine, because
// parallel work in one project is normal. A person who switched agents says so,
// in the first words of the new session, and names the session or the agent.
func HandoffIntent(text string) (HandoffAsk, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 2000 {
		return HandoffAsk{}, false
	}
	// A package deja already wrote; continuing it again would double it.
	if strings.HasPrefix(text, "You are picking up work handed off") {
		return HandoffAsk{}, false
	}
	low := strings.ToLower(cliFlag.ReplaceAllString(text, " "))
	words := strings.FieldsFunc(low, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	verbAt := wordIndexes(words, low, handoffVerbs)
	if len(verbAt) == 0 {
		return HandoffAsk{}, false
	}
	var ask HandoffAsk
	for _, tok := range strings.Fields(text) {
		if id := sessionIDToken(tok); id != "" {
			ask.IDs = append(ask.IDs, id)
		}
	}
	if len(ask.IDs) > 0 {
		return ask, true
	}
	nounAt := wordIndexes(words, low, handoffNouns)
	near := -1
	for _, n := range nounAt {
		for _, v := range verbAt {
			if abs(n-v) <= handoffVerbReach {
				near = n
			}
		}
	}
	if near < 0 {
		return HandoffAsk{}, false
	}
	// The harness nearest the session word, after it first: "перенеси в
	// opencode сессию claude" moves a claude session into opencode.
	best, bestDist := "", 1<<30
	for i, w := range words {
		if !handoffHarnessWord(w) || !handoffHarnessPlaced(words, nounAt, i) {
			continue
		}
		d := i - near
		if d < 0 {
			d = -d*2 + 1
		}
		if d < bestDist && d <= handoffVerbReach*2 {
			best, bestDist = w, d
		}
	}
	if best == "" {
		return HandoffAsk{}, false
	}
	ask.Harness = best
	return ask, true
}

// handoffHarnessWord is a harness name that cannot be an ordinary word in the
// sentence. "continue" is a harness and the commonest verb here; two-letter
// names match too much.
func handoffHarnessWord(w string) bool {
	switch w {
	case "continue", "deja", "notes", "amp", "goose", "crush":
		return false
	}
	return len(w) > 2 && sources.IsKnownHarness(w)
}

// handoffPrepositions put a harness name in the sentence as the place a
// session lives: "the session we had in cursor", "из codex".
var handoffPrepositions = map[string]bool{
	"in": true, "from": true, "on": true, "into": true, "to": true, "with": true,
	"в": true, "во": true, "из": true, "с": true, "со": true, "на": true,
}

// handoffHarnessPlaced reports whether the harness word at i names a session:
// beside a session word ("the codex session", "сессию claude",
// "OpenCode-сессии", "cursor's session") or after a preposition. A harness
// name that is also an ordinary word reads as that word anywhere else:
// "resume work on session pagination: the cursor is lost" is about a cursor.
func handoffHarnessPlaced(words []string, nounAt []int, i int) bool {
	for _, n := range nounAt {
		switch abs(n - i) {
		case 1:
			return true
		case 2:
			if words[(n+i)/2] == "s" {
				return true
			}
		}
	}
	return i > 0 && handoffPrepositions[words[i-1]]
}

// wordIndexes is where in the word list each stem or phrase starts.
func wordIndexes(words []string, low string, stems []string) []int {
	var at []int
	for i, w := range words {
		for _, s := range stems {
			if strings.Contains(s, " ") {
				if i+1 < len(words) && strings.HasPrefix(w+" "+words[i+1], s) {
					at = append(at, i)
				}
				continue
			}
			if strings.HasPrefix(w, s) {
				at = append(at, i)
			}
		}
	}
	return at
}

// sessionIDToken is a session id written in a prompt: a uuid, `ses_…`, a
// remote-control `session_01…`, or the last part of a URL that ends in one.
// Dates, versions, flags and paths are not.
func sessionIDToken(tok string) string {
	tok = strings.Trim(tok, "`'\"()[]{}<>,.;:!?«»")
	if i := strings.LastIndex(tok, "/"); i >= 0 {
		if !strings.Contains(tok, "://") {
			return ""
		}
		tok = tok[i+1:]
	}
	if len(tok) < 8 || strings.HasPrefix(tok, "-") {
		return ""
	}
	digits, letters := 0, 0
	for _, r := range tok {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			letters++
		case r == '-' || r == '_':
		default:
			return ""
		}
	}
	// A date or a version is all digits and separators; an id carries letters
	// and digits both.
	if digits == 0 || letters == 0 {
		return ""
	}
	return tok
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

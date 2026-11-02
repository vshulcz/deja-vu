package search

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/harnesscolor"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
)

func snippet(s, q string, re *regexp.Regexp) string {
	return snippetAt(s, q, re, matchedLine(q, re))
}

// snippetAt is snippet with the test for a line that holds the match given
// separately. The error tier centres on nothing in particular (q is empty) but
// still knows which lines the reader pasted.
func snippetAt(s, q string, re *regexp.Regexp, matched func(string) bool) string {
	lines, first := proseLinesKeeping(s, matched)
	s = strings.Join(lines, " ")
	r := []rune(s)
	idx := 0
	if re == nil && q == "" && first > 0 {
		// Nothing to centre on but the line the paste named: start there, so
		// a panic forty lines into a test log is in the window.
		for _, line := range lines[:first] {
			idx += utf8.RuneCountInString(line) + 1
		}
	}
	if re != nil {
		// Anchor on the first match that has width; a zero-width one points at
		// no passage in particular (#1606). Line by line, because the excerpt
		// joins lines and a ^ or $ in the pattern is anchored at each one.
		at := 0
	lineScan:
		for _, line := range lines {
			for _, loc := range re.FindAllStringIndex(line, -1) {
				if loc[1] > loc[0] {
					idx = at + utf8.RuneCountInString(line[:loc[0]])
					break lineScan
				}
			}
			at += utf8.RuneCountInString(line) + 1
		}
	} else {
		low := lower(s)
		b := densestMention(low, lower(q))
		if b < 0 {
			toks, _ := QueryParts(q)
			// Centre on where the query's words meet, not on the first one to
			// appear. A message that says a word once at the top and answers the
			// question four paragraphs down was excerpted at the top, so the
			// excerpt held none of the words the reader searched for together
			// (#1327). Only when they meet inside what an excerpt can show —
			// past that there is no meeting to point at, and the first mention is
			// as good a place to start as any.
			if span, at := tokenSpan(low, toks); span > 0 && span <= proximityNear {
				b = at
			}
			if b < 0 {
				for _, tok := range toks {
					if p := strings.Index(low, tok); p >= 0 && (b < 0 || p < b) {
						b = p
					}
				}
			}
		}
		if b > 0 {
			// Count runes in the lowercased string, which is where b came from.
			// Lowercasing maps rune to rune, so the count carries over to s —
			// the byte offsets do not: "İ" is two bytes and lowercases to one,
			// so a Turkish message put the excerpt a rune earlier for every
			// capital İ before the match, and enough of them moved the window
			// off the match entirely (#1331).
			idx = utf8.RuneCountInString(low[:b])
		}
	}
	start := max(idx-100, 0)
	end := start + 300
	if end > len(r) {
		// Spend the whole window: a match near the end of a message left the
		// tail of the budget unused and showed 120 runes where a match in the
		// middle of the same message showed 302 — and the end is where the
		// answer sits in a long output as often as anywhere (#1319).
		end = len(r)
		if start = end - 300; start < 0 {
			start = 0
		}
	}
	start, end = snapToWords(r, start, end, idx)
	out := strings.TrimSpace(string(r[start:end]))
	out = strings.Trim(out, " ,.;:-\n\t")
	if start > 0 {
		out = "… " + out
	}
	if end < len(r) {
		out += " …"
	}
	return out
}

// snapToWords moves a window's edges off the middle of a word, and onto the
// start of a sentence when one is within reach.
//
// The window is a fixed 100 runes before the match, which on a long line lands
// wherever it lands: over 400 real messages the cut opened inside a word in 35%
// of the excerpts and closed inside one in 62% — "… tigravity doctor rows report
// correctly", "… ads from disk per invocation". The first words of an excerpt are
// what a reader uses to decide whether to read the rest, and a fragment spends
// them on nothing. With the snap that is 2% and 30%, and the share of excerpts
// that open on a capital — the cheap sign of a sentence start — goes from 6% to
// 38%.
//
// Moving the left edge forward shortens the excerpt, so the same number of runes
// is given back at the other end. The match is the floor for both edges: a snap
// never crosses it, which is what keeps a window that was clamped against the
// end of the message from sliding off its own match.
func snapToWords(r []rune, start, end, match int) (int, int) {
	if start > 0 {
		limit := min(start+sentenceSnapRunes, match)
		moved := start
		// A sentence start is worth more than a word start, so look for one
		// first across the whole budget.
		for i := start; i < limit && i+1 < len(r); i++ {
			if isSentenceEnd(r[i]) && isSpace(r[i+1]) {
				moved = i + 2
				break
			}
		}
		if moved == start {
			wordLimit := min(start+headWordSnapRunes, limit)
			for i := start; i < wordLimit; i++ {
				if isSpace(r[i]) {
					moved = i + 1
					break
				}
			}
		}
		if given := moved - start; given > 0 {
			start = moved
			if end += given; end > len(r) {
				end = len(r)
			}
		}
	}
	if end < len(r) {
		floor := max(end-tailWordSnapRunes, match+1)
		for i := end; i > floor; i-- {
			if isSpace(r[i-1]) {
				end = i - 1
				break
			}
		}
	}
	return start, end
}

// How far an edge may move to find a boundary. A sentence is worth walking
// further for; past a short word's length the excerpt loses more than the
// ragged edge costs.
const (
	sentenceSnapRunes = 80
	headWordSnapRunes = 40
	tailWordSnapRunes = 20
)

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

func isSentenceEnd(r rune) bool {
	return r == '.' || r == '!' || r == '?' || r == '。' || r == '！' || r == '？'
}

// densestMention is where in low the query is discussed rather than mentioned:
// the occurrence with the most others inside the part of an excerpt that
// follows it.
//
// #1329 fixed the same shape one level up — blame quoted the first message to
// name a file rather than the one that said the most about it. Inside the
// message the excerpt still started at the first occurrence, so a session that
// noticed a file in passing at the top and took it apart four paragraphs down
// was excerpted on the passing line, with the discussion out of view. Returns
// -1 when the query does not appear, which is the caller's signal to fall back
// to its own token handling.
func densestMention(low, q string) int {
	if q == "" {
		return -1
	}
	first := strings.Index(low, q)
	if first < 0 {
		return -1
	}
	// Positions in bytes, bounded: a term repeated thousands of times in one
	// message is a log dump, and the first few hundred say as much about where
	// the discussion is as all of them.
	const scanCap = 256
	at := make([]int, 0, 8)
	for p := first; p >= 0 && len(at) < scanCap; {
		at = append(at, p)
		next := strings.Index(low[p+len(q):], q)
		if next < 0 {
			break
		}
		p += len(q) + next
	}
	if len(at) == 1 {
		return first
	}
	// The window is what the excerpt shows after its centre — 200 of its 300
	// runes — measured in runes, because a byte window is four times too wide
	// on CJK and then every occurrence looks like it sits with every other.
	best, bestCount := first, 0
	for i, p := range at {
		n := 0
		for _, other := range at[i:] {
			if utf8.RuneCountInString(low[p:other]) > snippetForwardRunes {
				break
			}
			n++
		}
		if n > bestCount {
			best, bestCount = p, n
		}
	}
	return best
}

// snippetForwardRunes is how much of an excerpt follows the point it centres
// on: start is idx-100 and the excerpt is 300 runes long.
const snippetForwardRunes = 200

// Snippet formats a message for search results, including semantic matches.
func Snippet(s, q string) string { return snippet(s, q, nil) }

func countAllTokens(low string, toks []string) int {
	total := 0
	for _, tok := range toks {
		c := strings.Count(low, tok)
		if c == 0 {
			return 0
		}
		total += c
	}
	return total
}

func countAllVariants(low string, toks []string, variants map[string][]string) int {
	total := 0
	for _, tok := range toks {
		optional := false
		count := strings.Count(low, tok)
		for _, variant := range variants[tok] {
			if variant == "" {
				optional = true
				continue
			}
			count += strings.Count(low, variant)
		}
		if count == 0 {
			if optional {
				continue
			}
			return 0
		}
		total += count
	}
	return total
}

// short keeps a result line narrow without destroying the one field that
// identifies the session.
//
// A flat 12-character cut printed `deja-note-cl` for every promoted note and
// `00000000-000` for UUID sessions — twelve identical rows in one answer, and
// the cut string is what a reader copies into `deja show` (#707). Ids are
// meaningful at both ends, so the middle goes.
func short(s string) string {
	const width = 20
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	head := width/2 - 1
	tail := width - head - 1
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// yoPattern quotes s for a regexp in which е and ё match either letter, as
// the search that found the text treats them as one.
func yoPattern(s string) string {
	return strings.NewReplacer("е", "[её]", "ё", "[её]").Replace(regexp.QuoteMeta(lower(s)))
}

func highlight(s, q string, isRe bool, color bool) string {
	if !color {
		return s
	}
	if isRe {
		re, err := compileQueryRegex(q)
		if err == nil {
			return re.ReplaceAllStringFunc(s, func(x string) string {
				// Zero-width match: colouring it wraps an escape pair around
				// nothing, once per character (#1606).
				if x == "" {
					return x
				}
				return cMatch + x + cReset
			})
		}
	}
	if strings.Contains(lower(s), lower(q)) {
		return regexp.MustCompile(`(?i)`+yoPattern(q)).ReplaceAllStringFunc(s, func(x string) string { return cMatch + x + cReset })
	}
	toks, _ := QueryParts(q)
	if len(toks) == 0 {
		return s
	}
	parts := make([]string, 0, len(toks))
	for _, t := range toks {
		parts = append(parts, yoPattern(t))
	}
	return regexp.MustCompile(`(?i)(`+strings.Join(parts, "|")+`)`).ReplaceAllStringFunc(s, func(x string) string { return cMatch + x + cReset })
}

// colorOK reports whether w is a terminal that should get colour. A writer
// that wraps another (the counter `deja search` prints through so the log
// records what went out) is looked through: checking only for a bare
// *os.File turned colour off in every terminal the moment output was counted
// (#4620).
func colorOK(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	for {
		u, ok := w.(interface{ Unwrap() io.Writer })
		if !ok {
			break
		}
		w = u.Unwrap()
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func harnessTag(h string, color bool) string {
	if !color {
		return "[" + h + "]"
	}
	return harnesscolor.Tag(h, true) + cBold
}

// HarnessTag is "[h]" in the harness's colour — one palette for every screen
// (internal/harnesscolor). Unlike harnessTag it re-arms nothing after itself.
func HarnessTag(h string, color bool) string { return harnesscolor.Tag(h, color) }

// ColorOK reports whether w is a terminal that wants colour: a character
// device, NO_COLOR unset, TERM not dumb.
func ColorOK(w io.Writer) bool { return colorOK(w) }

// ShortID is a session id as the result lines print it: whole up to twenty
// runes, longer ones elided in the middle ("e7a3c210-…6b7c8d9e01"). Every
// human screen prints this form, and `deja show` accepts it back.
func ShortID(id string) string { return short(id) }

// DisplayDate is a day as the result lines print it: "Mar 27" this year,
// "Nov 29 2025" otherwise, in the reader's zone.
func DisplayDate(t time.Time) string { return absoluteDate(t) }

// dateColumn picks one form for a whole column and returns a formatter that
// holds to it.
//
// Both forms are correct, and mixing them is what makes the column unreadable:
// two sessions a day apart printed "6d ago" and "Jul 26" one row under the
// other, because the relative form stops at a week. A reader scanning down
// cannot see they are consecutive without doing the arithmetic. So the rule is
// per rendering rather than per row — if any row has aged out of the relative
// form, every row is dated.
func dateColumn(times []time.Time) func(time.Time) string {
	for _, t := range times {
		if t.IsZero() {
			continue
		}
		if !isRelativeAge(t) {
			return absoluteDate
		}
	}
	return relativeDate
}

// isRelativeAge reports whether relativeDate would answer in the relative form.
func isRelativeAge(t time.Time) bool {
	return daysAgo(t) < 7
}

func daysAgo(t time.Time) int {
	now := time.Now()
	// In the reader's zone, not the timestamp's — see relativeDate.
	t = t.In(now.Location())
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	today := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location())
	day := time.Date(y2, m2, d2, 0, 0, 0, 0, now.Location())
	return int(today.Sub(day).Hours() / 24)
}

func absoluteDate(t time.Time) string {
	now := time.Now()
	t = t.In(now.Location())
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

func relativeDate(t time.Time) string {
	now := time.Now()
	// In the reader's zone, not the timestamp's: work done at 00:30 local time
	// is stored as 21:30 UTC the day before, and taking its calendar date in
	// UTC made this morning read as "1d ago" — while the counter above it, which
	// compares instants, said "today" (#767).
	t = t.In(now.Location())
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	today := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location())
	day := time.Date(y2, m2, d2, 0, 0, 0, 0, now.Location())
	days := int(today.Sub(day).Hours() / 24)
	if days == 0 {
		return "today"
	}
	if days > 0 && days < 7 {
		return fmt.Sprintf("%dd ago", days)
	}
	if y1 == y2 {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}
func collapseTool(s string) string {
	if strings.Contains(s, "tool_use") || strings.Contains(s, "tool_result") || strings.Contains(s, "<local-command") {
		if utf8.RuneCountInString(s) > 400 {
			return "[tool/local output collapsed]"
		}
	}
	return s
}

var (
	lineNumberRE = regexp.MustCompile(`^\s*\d{1,5}[:|]\s+`)
	toolDumpRE   = regexp.MustCompile(`(?i)(tool_use|tool_result|<local-command|netcat|npm ERR!|panic:|goroutine \d+)`)
	// The literal each alternative of toolDumpRE begins with, in the same
	// order. A line holding none of them cannot match, and looksToolDump uses
	// that to skip the engine entirely.
	toolDumpLiterals = []string{"tool_use", "tool_result", "<local-command", "netcat", "npm err!", "panic:", "goroutine "}
)

// containsFold is strings.Contains for a lowercase ASCII needle, without
// allocating a lowercased copy of the line.
func containsFold(hay, lowerNeedle string) bool {
	if len(lowerNeedle) == 0 || len(hay) < len(lowerNeedle) {
		return len(lowerNeedle) == 0
	}
	last := len(hay) - len(lowerNeedle)
	for i := 0; i <= last; i++ {
		k := 0
		for k < len(lowerNeedle) {
			c := hay[i+k]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != lowerNeedle[k] {
				break
			}
			k++
		}
		if k == len(lowerNeedle) {
			return true
		}
	}
	return false
}

// toolDumpEngineCalls counts the lines that reach the regexp engine. The point
// of the prefilter is that ordinary prose does not, and a counter says so in a
// test without timing anything (#1742).
var toolDumpEngineCalls atomic.Int64

// looksToolDump is toolDumpRE with the scan the regexp engine spends most of
// its time on done by hand: every alternative starts with a literal, so a line
// holding none of them cannot match. Rendering a digest ran this per line over
// the whole session — 16.5 s of the 17 s a 240 MB render took (#1742).
func looksToolDump(line string) bool {
	for i := range len(line) {
		if line[i] >= 0x80 {
			// Case folding is Unicode-wide in the engine (ſ folds to s), and
			// this prefilter is ASCII. A line with any byte outside ASCII goes
			// to the engine rather than being decided here.
			toolDumpEngineCalls.Add(1)
			return toolDumpRE.MatchString(line)
		}
	}
	for _, lit := range toolDumpLiterals {
		if containsFold(line, lit) {
			toolDumpEngineCalls.Add(1)
			return toolDumpRE.MatchString(line)
		}
	}
	return false
}

// looksNumbered is lineNumberRE — `^\s*\d{1,5}[:|]\s+` — read directly. It
// anchors at the start, so the whole line never needs scanning.
func looksNumbered(line string) bool {
	i := 0
	for i < len(line) && isRegexSpace(line[i]) {
		i++
	}
	digits := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' && digits < 6 {
		i++
		digits++
	}
	if digits == 0 || digits > 5 || i >= len(line) {
		return false
	}
	if line[i] != ':' && line[i] != '|' {
		return false
	}
	i++
	return i < len(line) && isRegexSpace(line[i])
}

// isRegexSpace is \s as the regexp engine reads it: [\t\n\f\r ]. Leaving out
// the ones a caller usually trims is how a fast path drifts from the pattern it
// stands in for.
func isRegexSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	}
	return false
}

// creditOpeners are how deja asks an agent to credit a recall it used. The
// sentence reports what an earlier session said, so quoted back as an excerpt it
// is deja citing itself through the agent's mouth.
var creditOpeners = []string{"déjà vu:", "deja vu:", "deja-vu recalled:", "deja recalled:"}

// withoutOwnCredit removes that sentence and leaves the rest of the paragraph.
// It runs mid-text rather than per line because the credit is written into the
// middle of a reply: "No files changed. No benchmarks run. deja-vu recalled: …".
func withoutOwnCredit(text string) string {
	low := lower(text)
	for _, opener := range creditOpeners {
		for {
			i := strings.Index(low, opener)
			if i < 0 {
				break
			}
			end := len(text)
			// To the end of the sentence, or the line, whichever comes first.
			if j := strings.IndexAny(text[i:], "\n"); j >= 0 {
				end = i + j
			}
			if j := strings.Index(text[i:], ". "); j >= 0 && i+j < end {
				end = i + j + 1
			}
			text = strings.TrimSpace(text[:i]) + " " + strings.TrimSpace(text[end:])
			low = lower(text)
		}
	}
	return strings.TrimSpace(text)
}

func proseForSnippet(s string) string {
	return strings.Join(proseLines(s), " ")
}

// proseLines is the excerpt's text as the lines it is joined from, each with
// its whitespace collapsed.
func proseLines(s string) []string {
	lines, _ := proseLinesKeeping(s, nil)
	return lines
}

// proseLinesKeeping is proseLines that keeps a dump line holding the match
// when no other line holds it, and also returns the index of the first kept
// line that holds the match, or -1.
//
// The dump filters are there for the noise around an answer, and a line they
// recognise can be the answer: an agent's test run panics, the agent searches
// `assignment to entry in nil map`, and the excerpt showed the stack frames
// and the FAIL summary while dropping the `panic:` line itself as a dump. The
// line the reader searched for is the one line an excerpt must not lose.
func proseLinesKeeping(s string, matched func(string) bool) ([]string, int) {
	s = redact.SafeForDisplay(s)
	// Neither the harness nor deja is a witness. An excerpt is offered as
	// something someone said about the subject, and measured over twelve
	// queries an agent would plausibly ask — 77 quoted lines — 6% were a
	// `<teammate-message …>` envelope and 6% were deja's own credit sentence
	// coming back as evidence. Both are stripped where they sit, so a message
	// that carries a person's words around them keeps the words.
	s = withoutOwnCredit(digest.StripClosedHarnessBlocks(s))
	type cand struct {
		line      string
		dump, hit bool
	}
	var cands []cand
	proseHit := false
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.Join(strings.Fields(line), " ")
		dump := looksNumbered(line) || looksToolDump(line) || digest.IsListingDump(line)
		// Asked of every line that would be dropped, and of the others only
		// until one of them holds the match.
		hit := matched != nil && (dump || !proseHit) && matched(line)
		if hit && !dump {
			proseHit = true
		}
		cands = append(cands, cand{line, dump, hit})
	}
	var keep []string
	first := -1
	for _, c := range cands {
		// A dump line that matched stays only when no other line did: prose
		// that says the same thing is the better excerpt.
		if c.dump && (!c.hit || proseHit) {
			continue
		}
		if c.hit && first < 0 {
			first = len(keep)
		}
		keep = append(keep, c.line)
	}
	if len(keep) == 0 {
		if out := strings.Join(strings.Fields(redact.SafeForDisplay(s)), " "); out != "" {
			keep = append(keep, out)
		}
	}
	return keep, first
}

// matchedLine says whether an excerpt line holds what the reader searched for:
// a width-having match of the pattern, the query as typed, at least half of
// its words as whole words, or — for a pasted error — a line the paste itself
// carries. Half rather than any: "to" and "in" are words of `panic: assignment
// to entry in nil map`, and one of them is in every frame of the stack below it.
func matchedLine(q string, re *regexp.Regexp) func(string) bool {
	if re != nil {
		return func(line string) bool {
			for _, loc := range re.FindAllStringIndex(line, -1) {
				if loc[1] > loc[0] {
					return true
				}
			}
			return false
		}
	}
	// Lines arrive redacted, so the paste is compared redacted too: a token in
	// the error line is "[redacted]" on both sides.
	lowQ := lower(strings.Join(strings.Fields(redact.SafeForDisplay(q)), " "))
	toks := query.Tokens(q)
	if len(toks) == 0 {
		return nil
	}
	need := (len(toks) + 1) / 2
	return func(line string) bool {
		low := lower(line)
		if strings.Contains(low, lowQ) {
			return true
		}
		// A line of the paste, long enough to be an error and not a stray
		// "FAIL" or "}" that any output carries.
		if utf8.RuneCountInString(low) >= pastedLineMin && strings.Contains(lowQ, low) {
			return true
		}
		n := 0
		for _, t := range toks {
			if containsWord(low, t) {
				if n++; n >= need {
					return true
				}
			}
		}
		return false
	}
}

// pastedLineMin is how long a line must be to count as one the reader pasted.
const pastedLineMin = 12

func contextText(s string, matched bool) string {
	s = redact.SafeForDisplay(s)
	if strings.Contains(s, "```") {
		return strings.TrimSpace(s)
	}
	if matched {
		return proseForSnippet(s)
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 8 {
		lines = lines[:8]
	}
	return proseForSnippet(strings.Join(lines, "\n"))
}

// SafeText neutralises what a terminal acts on rather than prints. Transcript
// text arrives verbatim from a harness, and after `deja sync import` from
// another machine — the boundary the trust policy exists for. An escape byte
// recolours the rest of the screen, erases the line above or sets the window
// title; a bell rings on every redraw. The status bar (#634) and the brief
// titles have stripped these since they were written; the reading surfaces
// printed them raw (#1090).
//
// Newlines and tabs stay: unlike a one-line bar, these renderers are the
// session's own layout. Most format characters (Cf) are left alone here too:
// a zero-width joiner holds an emoji sequence together. The ones that reorder
// the line, and the ones that render as nothing at all, do not.
func SafeText(s string) string {
	if !strings.ContainsFunc(s, unsafeForTerminal) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unsafeForTerminal(r) {
			return ' '
		}
		return r
	}, s)
}

// SafeNote is SafeLine bounded to the length of an answer, for the one string
// on these screens that a person wrote at whatever length they liked. Printed
// whole, a 4000-character promote note came out as a 4051-column line on a
// screen budgeted to 80, and as 4074 of the 4347 bytes an agent got back
// (#1645). The full note stays readable where a whole note belongs: `deja show`
// on the promoted note prints it as a message.
func SafeNote(s string) string {
	return clip(SafeLine(s))
}

// noteStateCap bounds the bracketed tail SafeNoteTitle will carry past the
// clip. The longest state deja writes is " [superseded]" at 13 bytes; the room
// above that is for one deja does not know yet, from a store written by hand.
const noteStateCap = 24

// SafeNoteTitle is SafeNote for a promoted note's title, which ends in the state
// the note is in — "… [rejected]" — and that suffix is the part every one-line
// surface reads it for. Clipping from the left would drop exactly it (#R11), so
// the middle gives way instead.
func SafeNoteTitle(s string) string {
	line := SafeLine(s)
	state := ""
	// Only a short tail: the suffix is a state word, and exempting whatever
	// follows the last " [" would let a long bracketed tail carry the whole
	// title past the bound the clip is here to apply (#1645).
	if i := strings.LastIndex(line, " ["); i > 0 && strings.HasSuffix(line, "]") && len(line)-i <= noteStateCap {
		state, line = line[i:], line[:i]
	}
	return clip(line) + state
}

// SafeLine is SafeText confined to a single line, for the places that print
// an untrusted string as one row of something structured — a listing entry, a
// digest row, a "saved <path>" confirmation. A newline there ends deja's own
// line and starts a line of the caller's, which reads as deja's own output.
func SafeLine(s string) string {
	return strings.Join(strings.Fields(SafeText(s)), " ")
}

// SafePath is SafeLine for a path, which is an identifier rather than prose: it
// strips what a terminal would obey and keeps the result on one line, but the
// spaces inside a name are part of the name. Collapsing them made blame print
// "/tmp/app/two spaces.go" for a file with two, and restoring that printed path
// found nothing (#2044).
func SafePath(s string) string { return safeVerbatim(s, clipPath) }

// SafeCommand is the same treatment for a command, which is the other thing on
// these screens that a person copies and runs rather than reads. `deja how`
// printed `-run "Pool Size"` for a command that ran `-run "Pool  Size"`, which
// is a different test filter (#2052).
//
// Clipped from the other end than a path: a path is recognised by its tail, a
// command by the program it runs.
func SafeCommand(s string) string { return safeVerbatim(s, clipCommand) }

// safeVerbatim keeps what was recorded, minus what a terminal would obey and
// minus the line breaks that would let one row become two.
func safeVerbatim(s string, clip func(string) string) string {
	if s == "" {
		return ""
	}
	// SafeText leaves only newline and tab standing, and both have to go: this
	// is one row of something structured. One space each rather than a run
	// collapsed, which is the whole point.
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, SafeText(s))
	return clip(strings.Trim(cleaned, " "))
}

// pathCap bounds a printed path. Long enough for any real one, short enough
// that a transcript-supplied string cannot fill a terminal or an MCP payload.
const pathCap = 300

// clipCommand bounds a command from the right: what identifies a command is the
// program it runs, so the head is the part a reader cannot give up.
func clipCommand(s string) string {
	r := []rune(s)
	if len(r) <= pathCap {
		return s
	}
	return string(r[:pathCap-1]) + "…"
}

// clipPath bounds a path from the left: a path is recognised by its tail, and
// the head is what a reader gives up first.
func clipPath(s string) string {
	r := []rune(s)
	if len(r) <= pathCap {
		return s
	}
	return "…" + string(r[len(r)-pathCap+1:])
}

func unsafeForTerminal(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	if unicode.IsControl(r) {
		return true
	}
	// The bidi overrides and isolates are the same attack without an escape
	// byte: U+202E reverses the rendering of everything after it, so a line
	// can read as the opposite of the bytes stored. Nothing recalled from a
	// transcript needs to reorder the reader's screen. The other format
	// characters stay: a zero-width joiner holds an emoji sequence together.
	switch r {
	case '\u200e', '\u200f', '\u061c', // left/right/arabic marks
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	}
	// Characters that render as nothing at all are the quiet version: text a
	// reader never sees and a model still reads. The tag block is a whole
	// invisible ASCII alphabet: "SYSTEM: ignore prior instructions" fits in
	// it and arrives in the agent's context looking like an empty string
	// (#1090). Only regional-flag sequences use tags for anything, and a plain
	// black flag is cheaper than an invisible instruction.
	switch {
	case r == '\u00ad', r == '\u200b', r == '\u2060', r == '\ufeff':
		return true
	case r >= '\U000e0000' && r <= '\U000e007f':
		return true
	}
	return false
}

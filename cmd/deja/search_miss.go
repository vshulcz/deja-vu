package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp/syntax"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// printSearchMiss says why a search came back empty. It reports whether the
// query looked like a mistyped command.
func printSearchMiss(dir string, o search.Options, policyHidden int, sinceRaw, harnessRaw string, hinted bool) bool {
	// An empty search is a moment for the mark, but only at a terminal
	// and never into a pipe a script reads (#4624).
	if !o.JSON && logoWanted(os.Stdout) {
		mood, _ := searchCatMoment(true)
		printLogoMood(os.Stdout, nil, mood)
	}
	// The policy is named before the generic advice: "try fewer words" is
	// wrong counsel for someone whose words were fine (#680). A filter the
	// caller set is the same kind of fact, and `deja last` has named it all
	// along (#715).
	switch note := policyHiddenNote(policy.ActivationSearch, policyHidden); {
	case note != "":
		fmt.Fprint(os.Stderr, note)
	case activeFilters(o, sinceRaw, harnessRaw) != "":
		fmt.Fprintf(os.Stderr, "deja: %q matched nothing under %s\n", o.Query, activeFilters(o, sinceRaw, harnessRaw))
		fmt.Fprint(os.Stderr, emptyRoleNote(dir, o.Role))
		fmt.Fprint(os.Stderr, olderThanWindow(dir, o.Since))
		// This branch does not go through printNoMatches, which is where
		// the line lives for the other miss, so it says it itself.
		fmt.Fprint(os.Stderr, ignoredHiddenNoteFor("answer", index.IgnoredWithAllTerms(dir, query.Tokens(o.Query))))
	default:
		return printNoMatchesHinted(os.Stderr, dir, o.Query, o.Regex, hinted) || hinted
	}
	return false
}

// printSearchCapped says how much of a capped answer is shown.
func printSearchCapped(o search.Options, shown int) {
	// The cap is silent everywhere else: 15 results look like the whole
	// answer, and nothing says another N are waiting behind --all. deja
	// narrates every other place the ladder hides a session, so it says
	// this one too.
	//
	// Except to a reader who already typed --all: there the list was cut by
	// an explicit --limit, or by the relevance tier's own retrieval window,
	// and neither of those lifts with the flag they are being sent to
	// (#1608). Say what is shown and leave the advice out.
	total := strconv.Itoa(o.Total)
	if o.TotalEstimated {
		total = "about " + total
	}
	if o.All {
		fmt.Fprintf(os.Stderr, "deja: showing %d of %s\n", shown, total)
	} else {
		fmt.Fprintf(os.Stderr, "deja: showing %d of %s — add --all to see the rest\n", shown, total)
	}
}

// printNeighbour narrates a co-occurrence swap. It is not a word form: the
// corpus says these two words keep company, which is a different claim and
// reads as a typo correction if it borrows the other sentence (#1786).
func printNeighbour(w io.Writer, variants map[string][]string) {
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			if variant != "" && variant != token {
				fmt.Fprintf(w, "deja: no session has those words together, so deja tried one this corpus keeps beside %q: %s\n", token, variant)
			}
		}
	}
}

// relevanceLead is the sentence above a relevance answer.
//
// It said "no exact match" whatever the answer held. The relevance tier is
// also where a strict answer of fewer than thinAND sessions is published once
// the ranking has been hung underneath it, and there the sentence is false:
// over 93 two-word queries on a 2,422-session store, every one of the 20
// answers labelled relevance carried a strict head of 1 to 9 sessions, so the
// line disowned a real match every time it fired (#3815).
func relevanceLead(strict int) string {
	if strict > 0 {
		return fmt.Sprintf("deja: %s %s every word of the query; the rest below are ranked by relevance", pluralSessions(strict), holdOrHolds(strict))
	}
	return "deja: no exact match; showing sessions ranked by relevance to the whole query"
}

func holdOrHolds(n int) string {
	if n == 1 {
		return "holds"
	}
	return "hold"
}

// markStrictHits flags the hits of a relevance answer that hold every word of
// the query, so a caller reading one hit is told what that hit is. The count
// on the envelope says how many there are; the order does not, because the
// merged ranking can put a ranked session above a strict one.
func markStrictHits(hits []search.Hit, result index.SearchResult) []search.Hit {
	if result.Strict == 0 {
		return hits
	}
	for i := range hits {
		hits[i].Strict = result.IsStrict(hits[i].Session)
	}
	return hits
}

func printStemmed(w io.Writer, variants map[string][]string) {
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			if variant == "" {
				fmt.Fprintf(w, "deja: ignoring %q — no session matches it together with the rest\n", token)
				continue
			}
			if variant != token {
				fmt.Fprintf(w, "deja: no exact match, trying word forms: %s -> %s\n", token, variant)
			}
		}
	}
}

// otherWordFormsNote says that a word form the query did not use has sessions
// of its own that this answer does not contain.
//
// The exact tier wins and stops, so `retry` returns what wrote "retry" and
// never reaches "retries" — the stem tier below it only runs when exact comes
// up empty. deja narrates every other time the ladder shapes an answer (close
// spellings, dropped terms, the trust policy); this rung was the silent one,
// and silence here reads as "that is all there is". Only the forms that bring
// sessions the answer does not already hold are worth a line.
func otherWordFormsNote(dir string, o query.Options, hits []search.Hit) string {
	if o.Regex || o.Tier != search.TierExact || len(hits) == 0 {
		return ""
	}
	terms := query.Tokens(o.Query)
	if len(terms) == 0 || len(terms) > 4 {
		return ""
	}
	forms := index.OtherWordForms(dir, terms)
	if len(forms) == 0 {
		return ""
	}
	var flat []string
	for _, list := range forms {
		flat = append(flat, list...)
	}
	// Spoken sessions only: deja prints this very note into terminals, the
	// transcripts keep it, and the next index reads the forms deja generated
	// back as forms the store holds (#3820).
	counts := index.TermSessionCountsSpoken(dir, flat)
	var parts []string
	for _, term := range terms {
		for _, form := range forms[term] {
			extra := counts[form] - sessionsHolding(hits, form)
			if extra <= 0 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%q in %d more", form, extra))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("deja: word forms this answer leaves out: %s — search the form to see them\n", strings.Join(parts, ", "))
}

// sessionsHolding counts the returned sessions that already contain a word
// form, so the note reports what is missing rather than what is on the page.
func sessionsHolding(hits []search.Hit, form string) int {
	n := 0
	for _, h := range hits {
		for _, m := range h.Session.Messages {
			if search.IsWorkRecord(m.Role) {
				continue
			}
			if containsWord(query.Lower(m.Text), form) {
				n++
				break
			}
		}
	}
	return n
}

// containsWord matches a whole word, not a substring: "retry" inside
// "retrying" is a different form and counting it would hide the one the note
// exists to name.
func containsWord(low, word string) bool {
	for i := 0; ; {
		j := strings.Index(low[i:], word)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(word)
		beforeOK := start == 0
		if !beforeOK {
			r, _ := utf8.DecodeLastRuneInString(low[:start])
			beforeOK = !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
		}
		afterOK := end == len(low)
		if !afterOK {
			r, _ := utf8.DecodeRuneInString(low[end:])
			afterOK = !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
		}
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
	}
}

// termCountLine names the terms that do match on their own, so "try fewer
// words" says which. It stays quiet when every term is already unknown to the
// store — there is nothing to drop then, and a row of zeroes is noise.
func termCountLine(dir, q string) string {
	terms := query.Tokens(q)
	if len(terms) < 2 || len(terms) > 6 {
		return ""
	}
	counts := index.TermSessionCounts(dir, terms)
	var parts []string
	for _, t := range terms {
		if counts[t] > 0 {
			parts = append(parts, fmt.Sprintf("%q in %d", t, counts[t]))
		}
	}
	if len(parts) == 0 || len(parts) == len(terms) && len(terms) > 3 {
		return ""
	}
	return fmt.Sprintf("deja: on their own: %s — no session has them together\n", strings.Join(parts, ", "))
}

// printNoMatchesHinted is printNoMatches for a search whose command hint was
// already printed before it ran, so the miss does not say it twice.
func printNoMatchesHinted(w io.Writer, dir, q string, regex, hinted bool) (mistypedCommand bool) {
	// An empty store is not a query problem: "fewer words" cannot help when
	// nothing is indexed, and `last`, `blame` and the brief all say what to do
	// instead. Search is the command a new machine reaches for first (#832).
	if n, err := index.SessionCount(dir); err == nil && n == 0 {
		// "Zero indexed sessions" has two very different causes, and the early
		// return named only the first: a machine with no history, and a machine
		// where the reader forgot everything themselves. `deja index` cannot
		// bring back a tombstoned session (#844).
		if note := hiddenByOwnSettings(); note != "" {
			fmt.Fprintf(w, "deja: no matches for %q\n", q)
			fmt.Fprint(w, note)
			return
		}
		fmt.Fprintln(w, emptyIndexHint(fmt.Sprintf("no matches for %q", q)))
		return
	}
	// Nothing to look up: very short tokens are dropped and punctuation is
	// trimmed, so this query never reached the index. "Try fewer words"
	// cannot be followed with one word, or none (#828). The message does not
	// state the rule — the cut is on bytes, so "л" and "舵" are long enough
	// while "p" is not, and a rule that reads false to half the world's
	// alphabets is worse than none.
	// Both sentences below are about the word index, and --re does not use it:
	// a regex is matched against the text itself, so an emoji it did not find
	// is a miss like any other rather than something deja cannot look up.
	if !regex && len(query.Tokens(q)) == 0 {
		// Length is the wrong reason for a query that holds no word at all: an
		// emoji is four bytes, and it was dropped for being a symbol. Sending
		// that reader after a longer word sends them after nothing (#2133).
		if !hasIndexableRune(q) {
			fmt.Fprintf(w, "deja: nothing to search for in %q — deja indexes words, and emoji, symbols and punctuation are not words\n", q)
			return
		}
		fmt.Fprintf(w, "deja: nothing to search for in %q — every word in it is too short to look up\n", q)
		return
	}
	// The size worth naming is the part of the store this path may read. Under
	// a trust rule the two differ, and "no matches in 1 indexed session — try
	// fewer words" sent the reader after a wording problem in a session the
	// rule never let search open (#986).
	if reach, trusted, total, ok := reachableSessionCount(dir); ok && reach == 0 && total > 0 {
		fmt.Fprintf(w, "deja: no matches for %q\n", q)
		// Which rule emptied the path. Counting the ignore rule here (#2707)
		// put a store hidden by a path pattern under a sentence about the
		// trust policy, quoting a trust rule that allows everything.
		if trusted == 0 {
			fmt.Fprintf(w, "deja: the trust policy withholds every indexed session from this path (%s: %s)%s\n",
				policy.ActivationSearch, policy.Load().Describe(policy.ActivationSearch), seePolicyFile())
		} else {
			fmt.Fprintf(w, "deja: the ignore rule withholds every indexed session from this path (%s)%s\n",
				strings.Join(policy.Load().IgnorePatterns(), ", "), seePolicyFile())
		}
		return
	} else if ok {
		fmt.Fprint(w, fitNotice(w, fmt.Sprintf("deja: no matches in %d indexed session%s — %s (query %q)\n", reach, pluralS(reach), missAdvice(q, regex), q)))
	} else if n, err := index.SessionCount(dir); err == nil {
		fmt.Fprint(w, fitNotice(w, fmt.Sprintf("deja: no matches in %d indexed session%s — %s (query %q)\n", n, pluralS(n), missAdvice(q, regex), q)))
	} else {
		fmt.Fprintf(w, "deja: no matches — %s (query %q)\n", missAdvice(q, regex), q)
	}
	// Before advising fewer words: the sessions that hold every one of them may
	// exist and be covered by the ignore rule, in which case rewording is the
	// wrong advice and the count is the answer (#2562).
	if note := ignoredHiddenNoteFor("answer", index.IgnoredWithAllTerms(dir, query.Tokens(q))); note != "" {
		fmt.Fprint(w, note)
	}
	// Which word to drop is the reader's next question, and deja read the
	// per-term counts to decide there was no intersection (#826). Not for a
	// pattern: its words are not looked up together, so "no session has them
	// together" is not why it missed.
	if line := termCountLine(dir, q); line != "" && !regex {
		fmt.Fprint(w, line)
	}
	// The first thing anyone types is a guess at a command name, and a wrong
	// guess falls through to search — where the advice is to use fewer words,
	// which cannot help someone who was not searching (#674). Falling through
	// stays the default; an empty result is where the other reading is worth
	// naming.
	if hint := commandHint(q); hint != "" && !hinted {
		fmt.Fprint(w, hint)
		mistypedCommand = true
	}
	// Last of the reasons a miss is not a miss, and the only one where deja is
	// holding the answer rather than withholding it.
	if hint := roleServedHint(dir, q); hint != "" {
		fmt.Fprint(w, hint)
	}
	if note := hiddenByOwnSettings(); note != "" {
		fmt.Fprint(w, note)
	}
	return mistypedCommand
}

// missAdvice is what to try after a miss. "Try --re" said to someone who used
// it is advice they already followed; a pattern misses for its own reasons —
// an anchor, since ^ and $ hold at each line, or being stricter than the text.
func missAdvice(q string, regex bool) string {
	if !regex {
		return "try fewer words or --re"
	}
	if re, err := syntax.Parse(q, syntax.Perl); err == nil && hasAnchor(re) {
		return "try the pattern without ^ or $, a looser one, or plain words without --re"
	}
	return "try a looser pattern, or plain words without --re"
}

// hasAnchor reports a ^ or $ in a parsed pattern; one inside a class or
// escaped is a character, not an anchor.
func hasAnchor(re *syntax.Regexp) bool {
	switch re.Op { //nolint:exhaustive // only the four anchor ops answer here; the rest recurse below
	case syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText:
		return true
	}
	return slices.ContainsFunc(re.Sub, hasAnchor)
}

// roleServedHint names the answer deja is holding but ranking will never
// return.
//
// Commands, file lists and edits are 38% of the record log and none of it
// reaches ranking, on purpose: a path that happens to contain the words of a
// question is not an answer to it. `how` and `blame` serve those roles when
// asked for by role — so a word that lives only in a command or a filename
// misses here while both of them answer it, and "try fewer words" is counsel no
// rewording can follow, the same wrong turn #2562 and #686 closed for the other
// reasons a miss is not a miss (#2634).
//
// Neither half costs a record-log scan: the commands table is a prebuilt
// artefact and the touched-file lists are one metadata read.
func roleServedHint(dir, q string) string {
	terms := query.Tokens(q)
	if len(terms) == 0 {
		return ""
	}
	var out strings.Builder
	if n := commandsMatchingWords(dir, terms); n > 0 {
		match := "match"
		if n == 1 {
			match = "matches"
		}
		out.WriteString(howOfferLine(n, match, terms))
	}
	if n, name := sessionsTouchingWords(dir, terms); n > 0 {
		fmt.Fprintf(&out, "deja: %d session%s touched %s — `deja blame %s`\n",
			n, pluralS(n), search.SafeLine(name), pasteSafe(name))
	}
	return out.String()
}

// commandsMatchingWords counts the commands in the recurring-command table that
// hold every one of these words.
//
// Commands rather than sessions, because the table cannot give an honest
// session count here: one session that ran two matching commands appears under
// both, ByProject is capped at commandProjectCap, and summing either would
// state a number that is wrong in a direction the reader cannot see. How many
// commands match is exact, and it is the number that decides whether `deja how`
// is worth typing.
//
// The trust policy keys on the project, which is what the table carries, so a
// command is counted only where a project this path may read has it. The ignore
// rule matches on path or project and the table has no path, so a session
// ignored by path alone is not excluded here — the same gap `hook-tool` has.
func commandsMatchingWords(dir string, terms []string) int {
	pol := policy.Load()
	n := 0
	for _, u := range index.ReadCommands(dir) {
		low := strings.ToLower(u.Command)
		matched := true
		for _, t := range terms {
			if !commandMentions(low, strings.ToLower(t)) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		for proj := range u.ByProject {
			if pol.Allows(policy.ActivationSearch, proj) && !pol.Ignored("", proj) {
				n++
				break
			}
		}
	}
	return n
}

// howOfferLine is the half of the hint that offers `deja how`.
//
// The words go over as words: `deja how` ANDs its arguments and a quoted
// phrase becomes one term it then requires contiguously, so quoting the whole
// query handed over a command that answers "no command on this machine
// mentions …" under a line that had just counted three (#2768).
func howOfferLine(n int, match string, terms []string) string {
	// A term can start with a dash — someone asking about `-run` or `--limit`
	// — and `deja how` would read it as a flag it does not have, or as one it
	// does and swallow the next word. The command's own escape says the rest
	// is the query.
	dashed := ""
	for _, t := range terms {
		if strings.HasPrefix(t, "-") {
			dashed = "-- "
			break
		}
	}
	return fmt.Sprintf("deja: %d command%s this machine ran %s it — `deja how %s%s`\n",
		n, pluralS(n), match, dashed, pasteSafeWords(terms))
}

// sessionsTouchingWords counts the sessions that touched a file whose name
// holds every one of these words, and names one such file so the reader has
// something to hand to blame.
func sessionsTouchingWords(dir string, terms []string) (int, string) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return 0, ""
	}
	pol := policy.Load()
	n, name := 0, ""
	for _, meta := range metas {
		if !metaVisible(pol, policy.ActivationSearch, meta) {
			continue
		}
		for _, p := range meta.Touched {
			base := filepath.Base(filepath.FromSlash(p))
			low := strings.ToLower(base)
			matched := true
			for _, t := range terms {
				if !strings.Contains(low, strings.ToLower(t)) {
					matched = false
					break
				}
			}
			if matched {
				n++
				if name == "" {
					name = base
				}
				break
			}
		}
	}
	return n, name
}

// hiddenByOwnSettings names the states the reader created themselves and can
// undo. "Forgotten", "excluded by my own pattern" and "never happened" were one
// answer, and "try fewer words" is wrong counsel for the first two (#686).
func hiddenByOwnSettings() string {
	var parts []string
	if n := len(index.Tombstones()); n > 0 {
		parts = append(parts, fmt.Sprintf("%d session%s forgotten (`deja forget --list`)", n, pluralS(n)))
	}
	// Counted apart, because they are different rules and the reader acts on
	// them differently: a project pattern hides work deja read, and a
	// `harness:` line means a whole store was never walked (#3499). Calling
	// both "project patterns" told someone who had excluded their only store
	// that a project of theirs was hidden.
	stores := sources.ExcludedHarnesses()
	if n := projectExcludePatterns(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d project pattern%s excluded from indexing (%s)", n, pluralS(n), sources.ExcludePath()))
	}
	if n := len(stores); n > 0 {
		settings := map[string]bool{}
		for name := range stores {
			settings[harnessExclusionSource(name)] = true
		}
		names := make([]string, 0, len(settings))
		for name := range settings {
			names = append(names, name)
		}
		sort.Strings(names)
		parts = append(parts, fmt.Sprintf("%d store%s not read at all (%s)", n, pluralS(n), strings.Join(names, ", ")))
	}
	if len(parts) == 0 {
		return ""
	}
	return "deja: this machine also has " + strings.Join(parts, ", ") + "\n"
}

// commandHint reads an empty search as a possible mistyped subcommand. It
// stays quiet unless a command name is within one edit of the first word: an
// empty search is usually an empty search, and a hint on every miss is noise
// that teaches people to skip the last line.
func commandHint(q string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(q), " ")
	if first == "" || strings.HasPrefix(first, "-") {
		return ""
	}
	// A hint is about a guess at a command, and a guess is short: a command
	// with an argument or two. "hook up the pool" is a sentence, and a hint
	// there is noise on top of a search that already failed — the same lesson
	// #715 learned about short words (#2115).
	if !looksLikeAnInvocation(q) {
		return ""
	}
	low := strings.ToLower(first)
	// A guess with an exact answer behind it. `deja hook context` is the
	// hyphen-free spelling of a command that exists, and the candidate list
	// below drops every `hook-` name on the grounds that plumbing is not
	// something anyone means to type — which is right about proposing it and
	// wrong about hiding it from the reader who typed its own stem (#2115).
	if hint := hyphenatedCommandHint(q, low); hint != "" {
		return hint
	}
	// The word deja's own MCP tool is called, and the one the documentation
	// uses throughout, so `deja recall <words>` is a fair guess at the shell
	// form. Nothing is near it by name, so the hint said nothing at all —
	// the same shape as the curated answers below (#2115).
	if low == "recall" {
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q), first))
		if rest != "" {
			return fmt.Sprintf("deja: %q is what the MCP tool is called — from a shell it is `deja %q`, and `deja hook-prompt` is the per-prompt recall\n", first, rest)
		}
		return fmt.Sprintf("deja: %q is what the MCP tool is called — from a shell it is `deja <query>`, and `deja hook-prompt` is the per-prompt recall\n", first)
	}
	// A one- or two-letter word is a word, not a mistyped command name. Any
	// prefix counts as "near" — so "a" reached `deja aider` and every sentence
	// starting with a short word got a hint (#715, my own regression from
	// #674).
	if len([]rune(low)) < 4 {
		return ""
	}
	if _, ok := commands[low]; ok {
		return "" // reached search only because it was used as a word
	}
	if slices.Contains([]string{"search", "show", "last", "aider", "goose"}, low) {
		return ""
	}
	// The switch in run() handles these before the map is consulted, so the
	// map alone would miss the commonest words of all — "serch" landed on
	// "bench" while `search` was one letter away.
	// The same shape for the other undo nobody can guess: removing a note is
	// `forget` on the note's own id, and the reader who typed this has no way
	// to know a note has an id at all (#1085).
	// brew and npm call it upgrade, and three edits is too far for the
	// nearest-name match to reach `update`. The search still runs: "upgrade"
	// is also something people search their history for.
	if strings.EqualFold(first, "upgrade") {
		return "deja: \"upgrade\" is not a command — `deja update` updates deja itself\n"
	}
	if strings.EqualFold(first, "unpromote") || strings.EqualFold(first, "demote") {
		return "deja: \"" + first + "\" is not a command — `deja promote <id> --state rejected` takes a decision back, and `deja forget --session deja-note-<harness>-<id>` removes the note itself\n"
	}
	near := nearestTarget(first, commandHintNames())
	if near == "" {
		return ""
	}
	// "unforget" is the one wrong guess with a real answer behind it, and
	// `deja forget` alone sends the reader back to a list that used to name
	// nothing (#919).
	if strings.EqualFold(first, "unforget") {
		return "deja: \"unforget\" is not a command — `deja forget --unforget <id>` is, and `deja forget --list` names the ids\n"
	}
	return fmt.Sprintf("deja: %q is not a command — did you mean `deja %s`?\n", first, near)
}

func commandHintNames() []string {
	names := []string{"search", "show", "last", "aider", "goose"}
	for name := range commands {
		// Hidden plumbing is not something anyone means to type.
		if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "hook-") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// hyphenatedCommandHint answers the guess that spelled a hyphenated command
// with a space: the two words joined name a real one, or the first word is the
// stem of some and there is no second word to join (#2115).
func hyphenatedCommandHint(q, low string) string {
	rest := strings.Fields(strings.TrimSpace(q))
	if len(rest) > 1 {
		joined := low + "-" + strings.ToLower(rest[1])
		if _, ok := commands[joined]; ok {
			return fmt.Sprintf("deja: %q is not a command — did you mean `deja %s`?\n", low+" "+rest[1], joined)
		}
	}
	// Only the stem on its own. With words after it that do not join into a
	// command, the reader is searching — "hook up the pool" is a sentence, and
	// a hint about `hook-context` there is noise on top of a failed search.
	if len(rest) != 1 {
		return ""
	}
	var stems []string
	for name := range commands {
		if strings.HasPrefix(name, low+"-") {
			stems = append(stems, name)
		}
	}
	if len(stems) == 0 {
		return ""
	}
	sort.Strings(stems)
	if len(stems) > 3 {
		stems = stems[:3]
	}
	return fmt.Sprintf("deja: %q is not a command on its own — `deja %s` and the rest of that family are; `deja help` names them\n",
		low, strings.Join(stems, "`, `deja "))
}

// looksLikeAnInvocation separates a guess at a command from prose. A command
// and an argument or two is short; "recall the decision we made about the pool"
// is a sentence, and the hint is about the first kind (#2115).
func looksLikeAnInvocation(q string) bool {
	return len(strings.Fields(q)) <= 3
}

// hasIndexableRune says whether a query holds anything the index could have
// kept. Letters and digits are what the tokenizer keeps; everything else is a
// separator, which is why an emoji-only query reaches the index as nothing.
func hasIndexableRune(q string) bool {
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// sinceRawArg recovers the text the reader typed after --since. parseSearch
// keeps only the parsed duration, and the search path had nothing else to hand
// activeFilters, so it reported "since 720h0m0s" for `--since 30d` (#1059).
// Last wins, as in parseSearch.
func sinceRawArg(args []string) string {
	args = splitEqualsForms(args)
	raw := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--since" {
			raw = args[i+1]
		}
	}
	return raw
}

// olderThanWindow names the store's own range when --since cut off everything
// in it. Without it someone returning to a year-old store is told their query
// matched nothing — advice about the query, when it was the window that
// emptied the result and no query under it could have returned anything.
// `deja stats` has said this since #854; search and `last` had not (#1059).
func olderThanWindow(dir string, since time.Duration) string {
	if since <= 0 {
		return ""
	}
	// Servable: this is advice on a search that came back empty, so the
	// sessions it counts have to be the ones search could have read (#2650).
	ov, err := index.OverviewServable(dir)
	if err != nil || ov.Sessions == 0 || ov.Newest.IsZero() {
		return ""
	}
	if !ov.Newest.Before(time.Now().Add(-since)) {
		return ""
	}
	return fmt.Sprintf("deja: every one of the %d indexed sessions is older than that window — the newest is %s\n",
		ov.Sessions, ov.Newest.Local().Format("2006-01-02"))
}

// checkHarness rejects a --harness value deja does not know. A typo used to be
// indistinguishable from a real harness with no sessions — both said "matched
// nothing under harness X" — so `--harness cluade` read as "you have no claude
// history" instead of "that is not a harness". A known-but-empty harness is
// still valid; only an unknown name is refused (#1113).
// It also normalises, which is why it takes a pointer: the notes store is
// stored as "deja" and narrated by the index run as "notes", and retrieval has
// accepted both since #1888 — but this check ran first and refused the printed
// name as a typo, so nothing below it ever saw the alias, and `deja stats`
// compares the stored name exactly (#2191).
func checkHarness(name *string) error {
	if *name == "notes" {
		*name = "deja"
	}
	if *name == "" || sources.IsKnownHarness(*name) {
		return nil
	}
	return fmt.Errorf("%q is not a harness deja knows — one of: %s", *name, strings.Join(sources.HarnessNames(), ", "))
}

// knownRoles is the set `--role` accepts. It lists the documented spellings the
// help text prints plus "tool-output", the stored form "tool" is an alias for —
// both reach tool records, so both must be accepted.
var knownRoles = []string{
	"user", "assistant", "tool", sources.RoleToolOutput,
	sources.RoleFiles, sources.RoleCommand, sources.RoleEdit, sources.RoleSummary,
}

// checkRole rejects a --role value that is not a known role. Like an unknown
// --harness, a typo used to be indistinguishable from a real role with no
// matches: `--role toool` said "matched nothing under role toool" instead of
// naming the mistake (#1113).
func checkRole(role string) error {
	if role == "" {
		return nil
	}
	if slices.Contains(knownRoles, role) {
		return nil
	}
	return fmt.Errorf("%q is not a role deja knows — one of: %s", role, strings.Join(knownRoles, ", "))
}

// emptyRoleNote says when a role found nothing because the store holds none of
// that kind, rather than because the query missed.
//
// deja advertises that it indexes the work and not only the talk, and a harness
// that records no tool calls gives a transcript that looks complete: `--role
// tool` came back the same way a bad query does, and the reader had no way to
// tell "not found" from "not recorded" (#1321). Only asked when a role-filtered
// search already returned nothing.
func emptyRoleNote(dir, role string) string {
	if role == "" {
		return ""
	}
	stored := role
	if role == "tool" {
		stored = sources.RoleToolOutput
	}
	if index.HasRecordOfRole(dir, stored) {
		return ""
	}
	return fmt.Sprintf("deja: this index holds no %s records at all — the harnesses it read do not write them, or wrote none yet\n", role)
}

// activeFilters names the filters a caller set, so an empty result can say
// which of them emptied it rather than blaming the index. sinceRaw carries
// what the reader actually typed: "168h0m0s" is not the flag they passed.
func activeFilters(o search.Options, sinceRaw, harnessRaw string) string {
	var parts []string
	if o.Harness != "" {
		// Same reason as sinceRaw: `--harness notes` is stored as "deja", and
		// telling someone their "deja" filter matched nothing names a flag
		// they did not pass (#2191).
		name := o.Harness
		if harnessRaw != "" {
			name = harnessRaw
		}
		parts = append(parts, fmt.Sprintf("harness %q", name))
	}
	if o.Project != "" {
		parts = append(parts, fmt.Sprintf("project %q", o.Project))
	}
	if o.Role != "" {
		parts = append(parts, fmt.Sprintf("role %q", o.Role))
	}
	if o.Session != "" {
		parts = append(parts, fmt.Sprintf("session %q", o.Session))
	}
	// The one filter this did not know about, and the one most likely to match
	// nothing: --from exists for the multi-machine case, and the name a reader
	// types comes off another machine. Unnamed, it fell through to "no sessions
	// indexed yet — run `deja index`" on a store that was fine (#2642).
	if o.From != "" {
		parts = append(parts, fmt.Sprintf("from %q", o.From))
	}
	// The same predicate filterRecentSources uses. parseDur accepts a negative
	// duration, and a negative Since filters nothing — naming it would report a
	// filter that was never applied and hide the empty-store advice, which is
	// the right answer there.
	if o.Since > 0 {
		since := sinceRaw
		if since == "" {
			since = o.Since.String()
		}
		parts = append(parts, "since "+since)
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

// printSpellings names the words the close tier substituted. ctx uses this
// rather than printFuzzy: "did you mean the command" is advice for a person
// at a shell, and ctx is called by an agent that typed nothing.
func printSpellings(w io.Writer, variants map[string][]string) {
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			if variant != token {
				fmt.Fprintf(w, "deja: no exact match, trying close spellings: %s -> %s\n", token, variant)
			}
		}
	}
}

func printFuzzy(w io.Writer, variants map[string][]string) {
	printSpellings(w, variants)
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			// A misspelled subcommand is searched for as a word: `deja
			// isntall` corrects to "install" and returns sessions that
			// mention installing, which is not what the typist wanted.
			// Spelled correctly it would have run the command, so the only
			// case this fires on is the one where the hint is wanted.
			if variant != token && isSubcommand(variant) {
				fmt.Fprintf(w, "deja: `%s` is also a command — run `deja %s` if that is what you meant\n", variant, variant)
				return
			}
		}
	}
}

// isSubcommand reports whether a word names something deja can run.
func isSubcommand(word string) bool {
	if _, ok := commands[word]; ok {
		return true
	}
	switch word {
	case "show", "last", "help":
		return true
	}
	return false
}

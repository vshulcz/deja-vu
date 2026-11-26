package index

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FrictionSignature is FrictionLine plus the signature the line hashes to, for
// a caller that groups runs of one failure rather than lines of text. The
// numbers a machine hands out — a port, a pid, an ip — are masked in the
// signature and kept in the line, so a listing can count the runs together and
// still show one that was really printed (#2375).
func FrictionSignature(l string) (string, uint64, bool) {
	line, ok := FrictionLine(l)
	if !ok {
		return line, 0, false
	}
	return line, frictionHash(line), true
}

// FrictionLine reports whether a line of tool output names something specific
// that went wrong, and returns it in the form two sessions can be compared on.
func FrictionLine(l string) (string, bool) {
	l = trimEnvelopeLabel(strings.TrimSpace(l))
	if looksLikeGrepContextLine(l) {
		return l, false
	}
	shell := shellErrorPrefix(l)
	l = normalizeFriction(l)
	if looksLikeGrepContextLine(l) {
		return l, false
	}
	// A shell only prints its position marker in front of something it could
	// not do, so the marker is the signal and what follows may be short. This
	// machine's transcripts hold 89 sightings of `(eval):1: == not found` —
	// zsh's answer to `[ "$a" == "$b" ]` — and every one was dropped for
	// length before any phrase was consulted, so `deja friction`, `deja fix`
	// and the hook at the failure all had nothing to say about a wall the
	// agent walked into every few days for months.
	if shell && utf8.RuneCountInString(l) >= shellErrorMin &&
		utf8.RuneCountInString(l) <= frictionLineMax &&
		!looksLikeSource(l) && !isDejaOwnReport(l) {
		return l, true
	}
	return l, isFriction(l)
}

// shellErrorMin is how much has to be left of a shell's complaint for it to be
// worth remembering. `== not found` is twelve characters; a bare `killed` is
// not a wall anyone recognises.
const shellErrorMin = 8

// shellErrorPrefix reports whether the line opens with a shell's position
// marker — `zsh:1:`, `(eval):2:`, `sh:12:`. normalizeFriction strips it so the
// same complaint from two shells counts once; this asks whether it was there.
func shellErrorPrefix(l string) bool {
	l = strings.TrimSpace(l)
	first := strings.Index(l, ":")
	if first <= 0 || first > 16 {
		return false
	}
	switch strings.ToLower(l[:first]) {
	case "zsh", "bash", "sh", "dash", "ksh", "fish", "(eval)", "(anon)":
	default:
		return false
	}
	rest := l[first+1:]
	second := strings.Index(rest, ": ")
	if second <= 0 {
		return false
	}
	_, err := strconv.Atoi(rest[:second])
	return err == nil
}

// normalizeFriction strips the shell's position prefix so the same missing
// command counts once. `zsh:1: command not found: timeout` and
// `(eval):2: command not found: timeout` are one piece of friction; left
// alone they land below the threshold separately and none is ever reported.
func normalizeFriction(l string) string {
	l = strings.TrimSpace(l)
	l = strings.TrimSpace(trimLogPrefix(l))
	l = trimTestDuration(l)
	// The prefix is `<where>:<line>: `, where <where> is a shell name, an
	// `(eval)`/`(anon)` marker, or a short filename (`worker.py:41: `). Only
	// strip it when <where> is a single token with no quotes or backticks and
	// the middle field is a line number — `Error: cannot find x: y` and a
	// comment like `// `(eval):2: ...` must keep their shape.
	first := strings.Index(l, ":")
	if first <= 0 || first > 16 || strings.ContainsAny(l[:first], " \t`\"'") {
		return l
	}
	rest := l[first+1:]
	second := strings.Index(rest, ": ")
	if second <= 0 {
		return l
	}
	if _, err := strconv.Atoi(rest[:second]); err != nil {
		return l
	}
	return strings.TrimSpace(rest[second+2:])
}

// volatileDigits is how long a digit run has to be before it reads as something
// the machine handed out rather than something the error says.
const volatileDigits = 4

// maskIPv4 replaces a dotted quad with a placeholder. The digit-run rule below
// cannot reach it: an octet is one to three digits, which is the length an exit
// code has, so `192.0.2.7` and `192.0.2.9` would stay two different walls for
// one service being unreachable (#2369).
func maskIPv4(l string) string {
	var b strings.Builder
	b.Grow(len(l))
	for i := 0; i < len(l); {
		if l[i] < '0' || l[i] > '9' {
			b.WriteByte(l[i])
			i++
			continue
		}
		if end, ok := ipv4At(l, i); ok {
			b.WriteString("<ip>")
			i = end
			continue
		}
		// Not a quad: copy the run whole so the scan cannot split a number.
		j := i
		for j < len(l) && l[j] >= '0' && l[j] <= '9' {
			j++
		}
		b.WriteString(l[i:j])
		i = j
	}
	return b.String()
}

// ipv4At reports where a dotted quad starting at i ends. Four groups of one to
// three digits, separated by dots, not running into another digit or dot.
func ipv4At(l string, i int) (int, bool) {
	pos := i
	for group := range 4 {
		if group > 0 {
			if pos >= len(l) || l[pos] != '.' {
				return 0, false
			}
			pos++
		}
		start := pos
		for pos < len(l) && l[pos] >= '0' && l[pos] <= '9' {
			pos++
		}
		if n := pos - start; n < 1 || n > 3 {
			return 0, false
		}
	}
	if pos < len(l) && (l[pos] == '.' || (l[pos] >= '0' && l[pos] <= '9')) {
		return 0, false
	}
	return pos, true
}

// filePosition is a compiler's way of saying where: a path, a line, and often a
// column, before the error itself. `./main.go:12:2: undefined: helper` and
// `tsc: src/app.ts:88:5: error TS2304` are the shape.
var filePosition = regexp.MustCompile(`(^|\s)([^\s:]*[./][^\s:]*):([0-9]{1,6})(:([0-9]{1,6}))?:`)

// maskFilePosition replaces the line and column a compiler prints with a
// placeholder. Adding an import moves every error in the file down a line, and
// without this the same undefined symbol at line 4 and at line 9 are two walls
// — each below the second sighting a fix pair needs and the three sessions
// `deja friction` needs, so the repair for a wall this machine has hit ten
// times is never offered. The path stays: which file it was is the useful half,
// and the reader is shown the line as it was printed either way, because this
// runs on the signature and not on the text (#2369 draws the same line for
// ports and pids).
func maskFilePosition(l string) string {
	return filePosition.ReplaceAllString(l, "${1}${2}:<n>:")
}

// maskVolatileNumbers replaces long digit runs with a placeholder, so one
// failure is one wall across the numbers a machine hands out: a port, a pid, an
// epoch, a goroutine id. Without it `dial tcp 192.0.2.7:5432: connect:
// connection refused` and the same failure on another port are two signatures
// — one wall each, below the three-session floor `deja friction` needs, below
// the second sighting a fix pair needs, and invisible to search's error tier,
// all at once (#2369).
//
// Four digits, not two: an exit code and a status code say which failure this
// is, and `make: *** [build] Error 1` must not become `Error 2`. Ports, pids,
// epochs and ids are longer than that; 404 and 500 are not.
func maskVolatileNumbers(l string) string {
	s := strings.TrimSpace(l)
	if rest, ok := strings.CutSuffix(s, " not found"); ok && rest != "" && strings.Trim(rest, "=") == "" {
		return "== not found"
	}
	if strings.HasPrefix(s, "no matches found: --include=") || strings.HasPrefix(s, "no matches found: --exclude=") {
		return "no matches found: --include=<glob>"
	}
	l = maskIPv4(l)
	l = maskFilePosition(l)
	var b strings.Builder
	b.Grow(len(l))
	for i := 0; i < len(l); {
		if l[i] < '0' || l[i] > '9' {
			b.WriteByte(l[i])
			i++
			continue
		}
		j := i
		for j < len(l) && l[j] >= '0' && l[j] <= '9' {
			j++
		}
		if j-i >= volatileDigits {
			b.WriteString("<n>")
		} else {
			b.WriteString(l[i:j])
		}
		i = j
	}
	return b.String()
}

// trimLogPrefix drops the prefixes a runner puts in front of a line it did not
// write: pytest's `E   ` marker on the failing line, and the timestamp docker,
// journalctl and most CI add to everything. They carry nothing about the error,
// and left in they split one wall into two — the same reasoning that strips the
// shell's position prefix above (#1637).
func trimLogPrefix(l string) string {
	for {
		trimmed := trimPytestMarker(trimTimestamp(trimEnvelopeLabel(l)))
		if trimmed == l {
			return l
		}
		l = trimmed
	}
}

// trimEnvelopeLabel removes the label a harness writes in front of the first
// line a shell call printed: CodeBuddy's `Stdout: ` and `Stderr: `, Qwen Code's
// `Output: `. The label shares a line with the error, so a fix learned there
// was keyed on `Stdout: fatal: …` and matched nothing printed anywhere else.
func trimEnvelopeLabel(l string) string {
	for _, label := range []string{"Stdout: ", "Stderr: ", "Output: "} {
		if rest, ok := strings.CutPrefix(l, label); ok {
			return strings.TrimLeft(rest, " ")
		}
	}
	return l
}

// trimPytestMarker removes the `E` column pytest prints beside the failing
// line. `E` must stand alone before the space, so the error codes that begin
// with it — `E2BIG: …`, `EACCES: …`, apt's `E: …` — keep their shape. A message
// whose first word really is a bare `E` loses it, which is the price of reading
// the pytest report people actually paste.
func trimPytestMarker(l string) string {
	rest, ok := strings.CutPrefix(l, "E ")
	if !ok {
		return l
	}
	rest = strings.TrimLeft(rest, " ")
	if rest == "" {
		return l
	}
	return rest
}

// trimTimestamp removes a leading date-and-time, bracketed or bare, in the
// shapes runners emit: 2026-07-12T10:00:00Z, 2026-07-12 10:00:00.123, and the
// same inside [].
func trimTimestamp(l string) string {
	// The pattern opens with a bracket or a digit; asking the regexp about
	// every other line of every tool output was a fifth of normalizeFriction.
	if l == "" || (l[0] != '[' && (l[0] < '0' || l[0] > '9')) {
		return l
	}
	if m := leadingTimestamp.FindString(l); m != "" {
		return strings.TrimSpace(l[len(m):])
	}
	return l
}

// dejaFixReport matches the header line `deja fix` prints: the error, then the
// date of the session it came from. Nothing else writes that separator with a
// bare date behind it.
var dejaFixReport = regexp.MustCompile(` · \d{4}-\d{2}-\d{2}$`)

var leadingTimestamp = regexp.MustCompile(`^\[?\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\]?\s+`)

// looksLikeSource reports whether the line is code that talks about an error
// rather than an error. Tool output carries source as often as it carries
// results — a `cat` of a script, a diff, a heredoc — and an
// `echo "App not found: $APP"` inside a deploy script reached second place the
// first time this was measured.
//
// A bare double quote used to stand for most of this and cost more than it
// caught: tools quote the thing they could not find — `relation "orders" does
// not exist`, `pull access denied for "acme/api"` — so the same psql failure
// was friction without its quotes and invisible with them (#2431). What is left
// is the punctuation source puts around a quote — an assignment, a call, a
// struct field, a code span — none of which appears in what a tool prints:
// 130 of the 192 lines of this repo's own source that read as friction (#2436).
func looksLikeSource(l string) bool {
	if looksLikeGrepContextLine(l) {
		return true
	}
	if sourceFragmentLooksLikeCode(l) {
		return true
	}
	if code := stripSourcePositionPrefix(l); code != l && code != "" {
		if looksLikeGrepContextLine(code) || sourceFragmentLooksLikeCode(code) {
			return true
		}
	}
	return false
}

func sourceFragmentLooksLikeCode(l string) bool {
	for _, source := range []string{"echo ", "printf ", "$(", "=~", "print("} {
		if strings.Contains(l, source) {
			return true
		}
	}
	if strings.HasPrefix(l, "\"") || strings.HasPrefix(l, "'") || strings.Contains(l, "\": \"") {
		return true
	}
	if strings.Contains(l, `("`) || strings.Contains(l, `, "`) ||
		strings.Contains(l, `:= "`) || strings.Contains(l, `= "`) ||
		strings.Contains(l, `('`) || strings.Contains(l, `, '`) ||
		strings.Contains(l, `:= '`) || strings.Contains(l, `= '`) ||
		strings.Contains(l, ":= `") || strings.Contains(l, "= `") ||
		strings.Contains(l, `case "`) ||
		keyedStringLiteralValue(l) ||
		strings.HasPrefix(l, "`") || strings.Contains(l, "` ") {
		return true
	}
	// A comment about an error is source too, and the wider marker list in
	// #729 made these reachable: `// panic: this is a comment about panics`
	// became the top wall on a store of shell snippets.
	for _, comment := range []string{"//", "#", "/*", "*", "--"} {
		// normalizeFriction has already trimmed the line.
		if strings.HasPrefix(l, comment) {
			return true
		}
	}
	return false
}

// keyedStringLiteralValue reports lines shaped like `Key: "value",` /
// `{Role: "x", Text: "value"},` without matching diagnostic prose like
// `AssertionError: "x" != "y"` or `error: "bad"`.
func keyedStringLiteralValue(l string) bool {
	isIdent := func(b byte) bool {
		return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_'
	}
	for off := 0; off < len(l); {
		rel := strings.Index(l[off:], `: "`)
		if rel < 0 {
			return false
		}
		idx := off + rel
		off = idx + 3
		if idx == 0 || !isIdent(l[idx-1]) {
			continue
		}
		start := idx - 1
		for start > 0 && isIdent(l[start-1]) {
			start--
		}
		if start > 0 {
			switch l[start-1] {
			case ' ', '\t', '{', '(', ',':
			default:
				continue
			}
		}
		rest := l[idx+3:]
		closeQ := -1
		for i := 0; i < len(rest); i++ {
			if rest[i] == '\\' {
				i++
				continue
			}
			if rest[i] == '"' {
				closeQ = i
				break
			}
		}
		if closeQ < 0 {
			continue
		}
		switch strings.TrimSpace(rest[closeQ+1:]) {
		case ",", "}", "},", ")", "),":
			return true
		}
	}
	return false
}

// looksLikeGrepContextLine matches `rg -C` / `grep -C` context lines formatted
// as `<line>-<content>` or `<path>-<line>-<content>`.
func looksLikeGrepContextLine(l string) bool {
	trimmed := strings.TrimSpace(l)
	if trimmed == "" {
		return false
	}
	if digitsDashContext(trimmed) {
		return true
	}
	firstDash := strings.IndexByte(trimmed, '-')
	for firstDash > 0 {
		tok := trimmed[:firstDash]
		if strings.ContainsAny(tok, " \t\"'`") {
			return false
		}
		if (strings.ContainsRune(tok, '/') || strings.ContainsRune(tok, '.')) && digitsDashContext(trimmed[firstDash+1:]) {
			return true
		}
		next := strings.IndexByte(trimmed[firstDash+1:], '-')
		if next < 0 {
			break
		}
		firstDash += 1 + next
	}
	return false
}

func digitsDashContext(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != '-' {
		return false
	}
	rest := s[i+1:]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '/' || rest[0] == '#' || rest[0] == '"' || rest[0] == '\'' || rest[0] == '`'
}

func stripSourcePositionPrefix(l string) string {
	// Bare `<lineno>:<content>` from `rg -n --no-filename`.
	i := 0
	for i < len(l) && l[i] >= '0' && l[i] <= '9' {
		i++
	}
	if i > 0 && i < len(l) && l[i] == ':' {
		return strings.TrimSpace(l[i+1:])
	}
	// `<path>:<lineno>[:<col>]:<content>` from `rg -n` / `grep -Hn`, without path length cap.
	first := strings.IndexByte(l, ':')
	if first <= 0 || strings.ContainsAny(l[:first], " \t`\"'") {
		return l
	}
	rest := l[first+1:]
	second := strings.IndexByte(rest, ':')
	if second <= 0 {
		return l
	}
	if _, err := strconv.Atoi(rest[:second]); err != nil {
		return l
	}
	afterLine := rest[second+1:]
	if third := strings.IndexByte(afterLine, ':'); third > 0 {
		if _, err := strconv.Atoi(afterLine[:third]); err == nil {
			return strings.TrimSpace(afterLine[third+1:])
		}
	}
	return strings.TrimSpace(afterLine)
}

// isDejaOwnReport reports whether the line is something deja printed. Its own
// output is tool output in the next session, and every line of it contains an
// error by construction, so without this running the command slowly teaches
// deja about itself.
//
// `deja friction` writes a count and the error; `deja fix` writes the error
// with the date of the session it came from and the command underneath. Both
// came back as a fresh sighting of the error they were quoting, and the command
// `fix` printed became a candidate remedy for it — In real-world stores, as the
// pair `command not found: python · 2026-05-18`.
func isDejaOwnReport(l string) bool {
	if i := strings.Index(l, " sessions  "); i > 0 {
		if _, err := strconv.Atoi(strings.TrimSpace(l[:i])); err == nil {
			return true
		}
	}
	return dejaFixReport.MatchString(l) || strings.HasPrefix(l, "ran next: ")
}

// isFriction keeps the error shapes that name something specific. The generic
// ones carry no information — every Python failure prints `Traceback (most
// recent call last):`, and clustering those put an empty line at the top of
// every measurement this was built from.
func isFriction(l string) bool {
	// Characters, not bytes. The bound is about how much of a line a person can
	// recognise, and in bytes it held a Russian error to 60 characters and a
	// Chinese one to 40 while giving English 120: an 89-character Russian line
	// was dropped where an 80-character English one was kept, so the wall a
	// Russian-speaking user keeps hitting never reached `deja friction`, the
	// environment block or `deja fix` (#1319).
	if n := utf8.RuneCountInString(l); n < frictionLineMin || n > frictionLineMax {
		return false
	}
	// A named test failure is the most specific thing a build prints: the test
	// name is stable across runs and across machines, which is more than most
	// error strings manage. It was rejected with the bare summary lines, and on
	// this machine's store that is 2,318 of the 6,744 error lines an agent
	// actually hit — the single most common failure it sees, and the one it
	// most often solved before.
	//
	// Every phrase test below needs one of the phrases in the line, so a line
	// with none of them is a wall exactly when it names a test failure. Most
	// lines carry none, and the checks in between were most of what a cold
	// build spent here (#5586).
	hit := frictionPhrasesIn(l)
	if hit == 0 {
		return namedTestFailure(l)
	}
	if namedTestFailure(l) {
		return true
	}
	// A generic opening — "Error: ", "error: ", a traceback header, a go test
	// summary line — used to end the question here. It says nothing on its
	// own, which is why it was listed, but the check ran before the phrase
	// list and so also threw away every line that goes on to name a wall the
	// list knows: `Error: Cannot find module ./config` was dropped while
	// `Cannot find module ./config` was kept (#2432). Nothing is needed in its
	// place — a line with only a generic opening matches no phrase below and
	// falls through to false, which is where it belonged.
	if looksLikeSource(l) {
		return false
	}
	if isDejaOwnReport(l) {
		return false
	}
	if hit&hitPhrase != 0 {
		return true
	}
	low := strings.ToLower(l)
	// A database saying a thing is not there says it in its own shape: the
	// line opens with the server's ERROR marker. The phrase on its own is a
	// sentence people write — "the orders table does not exist yet" — so it is
	// only a wall where the server said it.
	if strings.HasPrefix(low, "error") && strings.Contains(low, "does not exist") {
		return true
	}
	// "no such table" is what every sqlite driver says and also what a person
	// writes about a schema. What separates them is the shape around it: the
	// driver names the table after a colon — `no such table: part` — and the
	// report carries an error marker wherever its runtime puts it, which is
	// not always the first word: `D1_ERROR: …`, `sqlite3.OperationalError: …`,
	// `Parse error: …`. Requiring the line to *open* with "error" dropped all
	// three and still let "Error handling for missing tables…" through
	// (second review of #3373). The colon alone is not enough either: a
	// sentence can end a clause on one — "there is no such table: the schema
	// only has runs" — so the line either opens with the phrase, which is how
	// the bare driver line arrives, or carries the marker somewhere in it
	// (third review).
	if noSuchTableRE.MatchString(low) &&
		(strings.HasPrefix(low, "no such table:") || strings.Contains(low, "error")) {
		return true
	}
	return false
}

// frictionPhrases are the error shapes that name something specific; a line
// carrying one is friction. Lowercase ASCII, matched against the lowered line.
//
// The list was nine phrases about things not being found or permitted, and
// it matched 3 of 12 ordinary errors on measurement — missing runtime
// panics, database timeouts, auth failures and build failures, which is
// most of what a machine actually trips over. One miss was capitalisation
// alone: curl writes "Connection refused" (#729).
var frictionPhrases = []string{
	// Not found, not permitted — the original list.
	"command not found", "modulenotfounderror", "no module named",
	"not found: ", "cannot find", "no such file or directory",
	"undefined:", "connection refused", "permission denied",
	// Crashed.
	"panic:", "segmentation fault", "exception in thread",
	"nullpointerexception", "index out of range", "stack overflow",
	"typeerror:", "referenceerror:", "keyerror:", "attributeerror:",
	// Refused by a server or a tool, with the tool named.
	"fatal:", "npm err!", "rpc failed", "timeout expired",
	"statement timeout", "connection reset", "connection timed out",
	"address already in use", "eaddrinuse",
	// Failed to build or run.
	"] error ", "build failed", "compilation failed", "cannot be resolved",
	// Measured against 24 errors an agent actually hits, the list above
	// recognised 5 (#2434). These are the rest, each carrying the tool's
	// own wording rather than the bare subject: "access denied for" is a
	// server refusing a named user, where "access denied" alone is a
	// sentence someone writes about roles.
	"duplicate key value", "deadlock detected", "access denied for",
	"failed to connect to", "cannot import name", "symbol(s) not found",
	"undefined symbol:", "undefined reference to",
	"failed to push some refs", "acquiring the state lock",
	"no space left on device",
	// Three this machine hits that nothing above reached: the two ways the
	// go toolchain says there is no module here, counted 17 and 12 times
	// in the real stores, and sqlite saying a table is missing, 4 times.
	// "no such file or directory" was already here; its sibling was not
	// (#3373).
	"go.mod file not found", "does not contain main module",
	// The walls an agent hits that none of the above reached, counted over
	// 102,735 commands and 2,951 failures in this machine's transcripts.
	//
	// A command that ran out of time is the commonest of them — 144
	// sightings — and the wording is the harness's, not a server's, so
	// "connection timed out" above never covered it. What memory has to
	// say about it is worth the line: the same command timed out here
	// before, and either it wants a longer budget or it wants the
	// background.
	"timed out after",
	// The harness refusing the agent's own call: a file edited without
	// being read again, an argument the tool would not take, a tool that
	// is off in this session. 32 sightings, and every one is the agent's
	// own mistake rather than the machine's, which is the kind a memory
	// can actually stop.
	"tool_use_error", "inputvalidationerror",
	"has been modified since read", "no such tool available",
	// The tail of a Python traceback names the failure; only four of these
	// were listed and the rest fell through. 30 sightings of the JSON one
	// alone.
	"jsondecodeerror", "valueerror:", "runtimeerror:", "oserror:",
	"filenotfounderror:", "importerror:", "unicodedecodeerror:",
	// A package install behind a mirror or a proxy: the index has no such
	// package, refuses it, cannot be reached or is not trusted. None of these
	// was recognised, so the session that found the mirror that works left
	// nothing for the next one to reuse (#5599).
	// Each in the tool's own shape, so a sentence about a 403 is not one:
	// go and curl put the status after a colon, docker the exit code after
	// "successfully".
	"no matching distribution found", "could not find a version that satisfies",
	"http error 403", ": 403 forbidden", ": 401 unauthorized",
	"407 proxy authentication required", "npm error code", "npm error 40",
	"could not resolve host", "temporary failure in name resolution",
	": no such host", "certificate verify failed",
	"certificate signed by unknown authority",
	"did not complete successfully: exit code", "externally-managed-environment",
	"manifest unknown", "pull access denied for",
}

// noSuchTableRE is the driver's own shape: the table is named right after the
// phrase. Prose about schemas — "there is no such table in the spec" — does
// not name one there.
var noSuchTableRE = regexp.MustCompile(`no such table:\s*\S`)

// goTestFailure matches the line `go test` prints for a failing test, with the
// subtest path Go writes for table cases. The name is the identity; anything
// after it is this run's.
var goTestFailure = regexp.MustCompile(`^--- (?:FAIL|SKIP): (\S+)`)

// testDuration is the per-run time Go appends to that line. Two runs of the
// same failing test differ only there, and left in, each run is its own piece
// of friction and none of them ever reaches a second session.
var testDuration = regexp.MustCompile(`\s+\(\d+(?:\.\d+)?s\)$`)

func trimTestDuration(l string) string {
	if !strings.HasPrefix(l, "--- ") {
		return l
	}
	return strings.TrimSpace(testDuration.ReplaceAllString(l, ""))
}

// namedTestFailure reports whether the line is a test failure carrying a test
// name, as opposed to the bare `FAIL` and `--- FAIL` summaries that name
// nothing and are rejected with the other generic shapes.
func namedTestFailure(l string) bool {
	if !strings.HasPrefix(l, "--- ") {
		return false
	}
	m := goTestFailure.FindStringSubmatch(l)
	if m == nil {
		return false
	}
	// Source that quotes the shape is not a failure — the same reason the
	// quote and comment guards below exist.
	if strings.ContainsAny(l, "\"$") {
		return false
	}
	return len(m[1]) > 3
}

package index

import (
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Friction is what this machine keeps tripping over: one specific error, in
// several separate sessions.
//
// The detection lives here rather than in the command because it runs twice —
// once over a session being ingested, to fingerprint what it hit, and once
// over the record log when someone asks. Two copies of these rules would drift,
// and the first symptom would be a first-screen line the command cannot
// reproduce.
type Friction struct {
	Text     string
	Sessions []SessionMeta
	Last     time.Time
}

const (
	// frictionSessionCap bounds what one session contributes to the manifest,
	// and is set high on purpose. The first screen names a count and points at
	// `deja friction`; a cap low enough to lose a hit makes the two disagree,
	// which reads as the tool being broken. Measured on 1149 sessions: at 6 the
	// screen said 8 where the command said 11, at 100 it said 10, and one
	// session alone carries over a hundred distinct error lines. The whole
	// field costs 2.7 KB across that corpus — the exactness is nearly free.
	frictionSessionCap = 256
	frictionLineMin    = 20
	// The cap is about recognising a line, not about how much of it fits on a
	// screen — every surface that prints one clips it to the width it has. Set
	// at 120 it dropped the errors that carry a path: a build naming a module
	// and a file, a registry naming an image, a database naming a host. Over
	// the store on this machine that was 2,319 lines naming a wall the list
	// knows, against 7,053 it recognised — a third again, thrown away for
	// length alone, and 1,406 of them fit inside 160 (#2438). A bound there
	// still is: a page of output pasted as one line is nobody's recognisable
	// wall, and hashing it makes a new wall every run.
	frictionLineMax = 200
	// FrictionMinSessions is how many separate sessions must hit an error
	// before it is worth saying. Twice is a coincidence.
	FrictionMinSessions = 3
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
	l = normalizeFriction(l)
	return l, isFriction(l)
}

// normalizeFriction strips the shell's position prefix so the same missing
// command counts once. `zsh:1: command not found: timeout` and
// `(eval):2: command not found: timeout` are one piece of friction; left
// alone they land below the threshold separately and none is ever reported.
func normalizeFriction(l string) string {
	l = strings.TrimSpace(l)
	l = strings.TrimSpace(trimLogPrefix(l))
	l = trimTestDuration(l)
	// The prefix is `<where>:<line>: `, where <where> is a shell name or an
	// `(eval)`/`(anon)` marker. Only strip it when the middle field is a
	// number — `Error: cannot find x: y` must keep its shape.
	first := strings.Index(l, ":")
	if first <= 0 || first > 16 {
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
// code has, so `10.0.0.7` and `10.0.0.9` would stay two different walls for one
// service being unreachable (#2369).
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
	for group := 0; group < 4; group++ {
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

// maskVolatileNumbers replaces long digit runs with a placeholder, so one
// failure is one wall across the numbers a machine hands out: a port, a pid, an
// epoch, a goroutine id. Without it `dial tcp 10.0.0.7:5432: connect:
// connection refused` and the same failure on another port are two signatures
// — one wall each, below the three-session floor `deja friction` needs, below
// the second sighting a fix pair needs, and invisible to search's error tier,
// all at once (#2369).
//
// Four digits, not two: an exit code and a status code say which failure this
// is, and `make: *** [build] Error 1` must not become `Error 2`. Ports, pids,
// epochs and ids are longer than that; 404 and 500 are not.
func maskVolatileNumbers(l string) string {
	l = maskIPv4(l)
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
		trimmed := trimPytestMarker(trimTimestamp(l))
		if trimmed == l {
			return l
		}
		l = trimmed
	}
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
	// Tool output carries source as often as it carries results — a `cat` of a
	// script, a diff, a heredoc. An `echo "App not found: $APP"` inside a
	// deploy script reached second place on the first run: it is a line about
	// an error, not an error.
	for _, source := range []string{"echo ", "printf ", "$(", "=~", "print("} {
		if strings.Contains(l, source) {
			return false
		}
	}
	// A bare double quote used to be on that list, and it cost more than it
	// caught: tools quote the thing they could not find — `relation "orders"
	// does not exist`, `repository "…" not found`, `pull access denied for
	// "acme/api"` — so the same psql failure was friction without its quotes
	// and invisible with them (#2431). What the quote was there to reject is
	// still rejected by the markers above and by the two shapes below: a line
	// that opens with a quoted string, and a JSON pair, which is what a
	// payload printed into tool output looks like.
	if strings.HasPrefix(l, "\"") || strings.Contains(l, "\": \"") {
		return false
	}
	// Source that carries an error string is a line about an error, not one.
	// A bare quote used to stand for this and cost far more than it caught
	// (#2430): what is left is the punctuation source puts around the quote —
	// an assignment, a call, a struct field, a code span — none of which
	// appears in the output a tool prints. Measured over this repo: 130 of the
	// 192 lines of its own docs and source that read as friction (#2436).
	if strings.Contains(l, `("`) || strings.Contains(l, `, "`) ||
		strings.Contains(l, `:= "`) || strings.Contains(l, `= "`) ||
		strings.Contains(l, `: "`) && strings.HasSuffix(l, `"},`) ||
		strings.HasPrefix(l, "`") {
		return false
	}
	// A comment about an error is source too, and the wider marker list in
	// #729 made these reachable: `// panic: this is a comment about panics`
	// became the top wall on a store of shell snippets.
	for _, comment := range []string{"//", "#", "/*", "*", "--"} {
		// normalizeFriction has already trimmed the line.
		if strings.HasPrefix(l, comment) {
			return false
		}
	}
	// deja's own report is tool output in the next session, and every line of
	// it contains an error by construction. Drop the report shape so running
	// the command does not slowly teach it about itself.
	if i := strings.Index(l, " sessions  "); i > 0 {
		if _, err := strconv.Atoi(strings.TrimSpace(l[:i])); err == nil {
			return false
		}
	}
	// `deja fix` prints the error it answers with the date beside it, and the
	// command underneath. Both come back as tool output in the next session:
	// the first is read as a fresh sighting of the error it is quoting, so
	// asking deja about an error taught deja that the error happened again,
	// and the command it printed became a candidate remedy for it. Found on a
	// real store as the pair `command not found: python · 2026-05-18`.
	if dejaFixReport.MatchString(l) || strings.HasPrefix(l, "ran next: ") {
		return false
	}
	// The list was nine phrases about things not being found or permitted, and
	// it matched 3 of 12 ordinary errors on measurement — missing runtime
	// panics, database timeouts, auth failures and build failures, which is
	// most of what a machine actually trips over. One miss was capitalisation
	// alone: curl writes "Connection refused" (#729).
	low := strings.ToLower(l)
	for _, p := range []string{
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
		// Failed to build or run.
		"] error ", "build failed", "compilation failed", "cannot be resolved",
		// Measured against 24 errors an agent actually hits, the list above
		// recognised 5 (#2434). These are the rest, each carrying the tool's
		// own wording rather than the bare subject: "access denied for" is a
		// server refusing a named user, where "access denied" alone is a
		// sentence someone writes about roles.
		"duplicate key value", "deadlock detected", "access denied for",
		"failed to connect to", "cannot import name", "symbol(s) not found",
		"failed to push some refs", "acquiring the state lock",
		"no space left on device",
	} {
		if strings.Contains(low, p) {
			return true
		}
	}
	// A database saying a thing is not there says it in its own shape: the
	// line opens with the server's ERROR marker. The phrase on its own is a
	// sentence people write — "the orders table does not exist yet" — so it is
	// only a wall where the server said it.
	if strings.HasPrefix(low, "error") && strings.Contains(low, "does not exist") {
		return true
	}
	return false
}

func frictionHash(line string) uint64 {
	// Masked here rather than in FrictionLine: the numbers a machine hands out
	// must not split one failure into a wall per run, and the line a reader is
	// shown should still be the one that was printed, with its real port in it
	// (#2369).
	h := fnv.New64a()
	_, _ = h.Write([]byte(maskVolatileNumbers(line)))
	return h.Sum64()
}

// frictionHashes fingerprints what a session tripped over, for the manifest.
// Hashes rather than text, for the reason SessionMeta.Asked gives: the
// manifest is read on every search and the only thing a caller needs from it
// is whether two sessions hit the same wall.
func frictionHashes(ms []model.Message) []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	for _, m := range ms {
		if m.Role != roleToolOutput {
			continue
		}
		for _, line := range strings.Split(m.Text, "\n") {
			line, ok := FrictionLine(line)
			if !ok {
				continue
			}
			h := frictionHash(line)
			if seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
			if len(out) >= frictionSessionCap {
				return out
			}
		}
	}
	return out
}

// hitFromRecords is frictionHashes for the import path, which holds a session
// as records rather than messages. Imported sessions used to carry no Hit, so
// the brief's one friction line — which reads meta.Hit — never counted a wall a
// peer kept hitting, while `deja friction` (which reads the record log) and
// stats both did. Same gap the asked-twice line had, same shape of fix.
func hitFromRecords(recs []Record) []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	for _, r := range recs {
		if r.Role != roleToolOutput {
			continue
		}
		for _, line := range strings.Split(r.Text, "\n") {
			line, ok := FrictionLine(line)
			if !ok {
				continue
			}
			h := frictionHash(line)
			if seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
			if len(out) >= frictionSessionCap {
				return out
			}
		}
	}
	return out
}

// FindFriction picks the wall worth showing on a screen with room for one. See
// TopFriction for allow.
func FindFriction(dir string, allow func(project string) bool) (Friction, bool) {
	out := TopFriction(dir, 1, allow)
	if len(out) == 0 {
		return Friction{}, false
	}
	return out[0], true
}

// TopFriction returns the walls this machine keeps running into, most-hit
// first. It runs over the manifest alone, so a caller on the first screen or
// in a session-start hook pays nothing for the search; only the sessions
// carrying a winning hash are read back, and only to recover the text the
// hash stands for.
//
// allow gates a session's project by the caller's activation, so an all-imported
// wall stays off a screen whose trust rule withholds imported memory. A nil
// allow counts every session; callers that filter per wall themselves (the
// environment block) pass nil and keep their own gate.
func TopFriction(dir string, n int, allow func(project string) bool) []Friction {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	byHash := map[uint64][]SessionMeta{}
	for _, meta := range m.Sessions {
		if allow != nil && !allow(meta.Project) {
			continue
		}
		for _, h := range meta.Hit {
			byHash[h] = append(byHash[h], meta)
		}
	}
	type cluster struct {
		hash  uint64
		metas []SessionMeta
	}
	var cs []cluster
	for h, metas := range byHash {
		if len(metas) < FrictionMinSessions {
			continue
		}
		sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
		cs = append(cs, cluster{h, metas})
	}
	sort.Slice(cs, func(i, j int) bool {
		if len(cs[i].metas) != len(cs[j].metas) {
			return len(cs[i].metas) > len(cs[j].metas)
		}
		// A wall the machine stopped running into is history, not friction.
		if !cs[i].metas[0].Updated.Equal(cs[j].metas[0].Updated) {
			return cs[i].metas[0].Updated.After(cs[j].metas[0].Updated)
		}
		return cs[i].hash < cs[j].hash
	})
	if n > 0 && len(cs) > n {
		cs = cs[:n]
	}
	// Recover every winning text in one pass per session rather than one pass
	// per wall: the same session usually carries several of them.
	want := map[uint64]string{}
	for _, c := range cs {
		want[c.hash] = ""
	}
	for _, c := range cs {
		if want[c.hash] != "" {
			continue
		}
		frictionTexts(dir, m, c.metas, want, c.hash)
	}
	var out []Friction
	for _, c := range cs {
		if want[c.hash] == "" {
			continue
		}
		out = append(out, Friction{Text: want[c.hash], Sessions: c.metas, Last: c.metas[0].Updated})
	}
	return out
}

func newestOf(ms []SessionMeta) time.Time {
	var out time.Time
	for _, m := range ms {
		if m.Updated.After(out) {
			out = m.Updated
		}
	}
	return out
}

// frictionTexts recovers what the wanted hashes stood for by reading back the
// sessions that carry one of them, filling in every hash a session yields —
// one session usually carries several walls, and reading it once per wall was
// the difference between one lookup and N.
func frictionTexts(dir string, m Manifest, metas []SessionMeta, want map[uint64]string, target uint64) {
	for _, meta := range metas {
		s, ok, err := loadSessionMeta(dir, m, meta)
		if err != nil || !ok {
			continue
		}
		for _, msg := range s.Messages {
			if msg.Role != roleToolOutput {
				continue
			}
			for _, line := range strings.Split(msg.Text, "\n") {
				line, ok := FrictionLine(line)
				if !ok {
					continue
				}
				h := frictionHash(line)
				if cur, wanted := want[h]; wanted && cur == "" {
					want[h] = line
				}
			}
		}
		// Stop on this cluster's own text, not on any text: a session can fill
		// in a neighbour's hash while carrying nothing for this one.
		if want[target] != "" {
			return
		}
	}
}

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

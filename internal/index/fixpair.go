package index

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Fix pairs: the error a session hit, and the command that came next and made
// it stop. "Why is this broken" is 22.4% of what people type at an agent —
// measured over 10462 user turns on a 1165-session store — and the answer is
// usually a command someone already ran once. Mining it costs nothing: on that
// same store, 1082 errors are followed by a command, and in 81% of those the
// error does not come back.
//
// The pair is evidence, not a claim that the command is a fix. What earns a
// pair is sequence plus silence: the error, then a command, then no repeat of
// the same error in the records right after it.

const (
	fixesFile = "fixes.gob"
	// fixLookAhead is how far past the error a command still counts as an
	// answer to it. Beyond that the session has moved on to something else.
	fixLookAhead = 10
	// fixQuietAfter is how many records must pass without the same error for
	// the command to count as having settled it.
	fixQuietAfter = 6
	// fixCommandMax bounds what is stored per pair; a command longer than this
	// is a heredoc or a pasted script, not something to hand back.
	fixCommandMax = 200
	// fixesMax bounds the whole file. Newest wins.
	fixesMax = 2000
)

// FixPair is one error and the command that followed it without the error
// coming back.
type FixPair struct {
	// Sig is the friction hash of the normalised error line, so a lookup can
	// match an error it has never seen the exact text of.
	Sig uint64
	// Error is the normalised line, for showing what this pair answers.
	Error string
	// Command is what was run next.
	Command string
	// Key is harness:id of the session it came from; When is that record's time.
	Key  string
	When time.Time
	// Project is the session's project, so a caller can apply the trust policy
	// — a peer's command must not surface when imported content is withheld.
	// Empty on a pair mined before this field existed; the version bump that
	// ships it forces the rebuild that fills it.
	Project string
}

func fixesPath(dir string) string { return filepath.Join(dir, fixesFile) }

// buildFixes writes the mined pairs into the build directory. Like every other
// sidecar here, failures are swallowed: this is an extra, never a reason to
// fail an index build.
func buildFixes(tmp string, ss []model.Session, keyOf func(model.Session) string) {
	var all []FixPair
	for _, s := range ss {
		all = append(all, fixPairsIn(s.Messages, keyOf(s), s.Project)...)
	}
	if len(all) == 0 {
		return
	}
	// Sequence alone is weak evidence: on a real store only 13% of "the next
	// command" mentioned anything the error named, so the other 87% are the
	// session moving on to unrelated work. A pair is kept when it carries a
	// second, independent reason to believe it — the command names something
	// the error named, or the same remedy followed the same error in another
	// session. That takes 356 pairs down to the ones worth handing an agent.
	repeats := map[string]int{}
	for _, p := range all {
		repeats[fixKey(p)]++
	}
	var out []FixPair
	for _, p := range all {
		if sharesTerm(p.Error, p.Command) || repeats[fixKey(p)] >= 2 {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	// One command per (error, command) is enough — the rest are the same fix
	// from other days. Keying on the command alone was wrong: a generic remedy
	// (`go mod tidy`, `npm install`) is the answer to several different errors,
	// and dropping all but its newest occurrence deleted the pair for every
	// other signature it settled.
	seen := map[string]bool{}
	kept := out[:0]
	for _, p := range out {
		k := fixKey(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		kept = append(kept, p)
		if len(kept) >= fixesMax {
			break
		}
	}
	_ = writeGob(fixesPath(tmp), kept)
}

// fixKey identifies one remedy for one error, so the same pair arriving from
// two sessions can be counted as a repeat.
func fixKey(p FixPair) string {
	return strconv.FormatUint(p.Sig, 16) + "|" + strings.ToLower(p.Command)
}

// fixTermRE matches the words worth comparing: identifiers, paths, flags.
var fixTermRE = regexp.MustCompile(`[A-Za-z0-9_./-]{4,}`)

// fixCommonTerms appear in half the commands ever run, or are the prose an
// error is phrased in, and prove nothing about whether this command answers
// that error.
var fixCommonTerms = map[string]bool{
	// Ubiquitous command words.
	"bash": true, "sudo": true, "true": true, "false": true, "null": true,
	"file": true, "http": true, "https": true, "test": true, "main": true,
	"head": true, "tail": true, "grep": true, "echo": true,
	// The prose errors are written in — present in the error, meaningless as a
	// match. "command not found" shares "command"/"found" with any command
	// mentioning them.
	"error": true, "command": true, "found": true, "cannot": true,
	"unable": true, "failed": true, "usage": true, "fatal": true,
	"invalid": true, "unknown": true, "expected": true, "missing": true,
}

// sharesTerm reports whether the command names something the error named — the
// missing binary, the path that was not there, the symbol that was undefined.
//
// The match is on whole tokens, not substrings. Substring matching let the
// remedy for `command not found: timeout` be `kubectl … --request-timeout=20s`
// because "timeout" is inside "request-timeout" — a wrong answer, and a missing
// binary is not fixed by a flag that happens to spell it. A hyphen keeps a
// token whole (fixTermRE), so "request-timeout" no longer matches "timeout".
func sharesTerm(errLine, cmd string) bool {
	seen := map[string]bool{}
	for _, t := range fixTermRE.FindAllString(strings.ToLower(errLine), -1) {
		t = trimTermEdges(t)
		if len(t) >= 4 && !fixCommonTerms[t] {
			seen[t] = true
		}
	}
	if len(seen) == 0 {
		return false
	}
	low := strings.ToLower(cmd)
	// An install command is allowed to name the missing thing as part of a
	// longer token: `No module named 'yaml'` is fixed by `pip install pyyaml`,
	// and `aiokafka` by `pip install aiokafka-python`. That containment is only
	// trusted when the command is actually installing something — otherwise it
	// is the `-timeout` flag class this function exists to reject.
	installing := installVerbRE.MatchString(low)
	for _, raw := range fixTermRE.FindAllString(low, -1) {
		t := trimTermEdges(raw)
		if seen[t] {
			return true
		}
		if installing && len(t) >= 4 {
			for s := range seen {
				if strings.Contains(t, s) {
					return true
				}
			}
		}
	}
	return false
}

// installVerbRE marks a command that installs a package, where naming the
// missing module inside a longer package name is a real match rather than a
// coincidence. Only "install" — it is unambiguous (pip/npm/apt/brew/gem/go/
// cargo install), where "get" and "add" also mean `kubectl get`, `git add`.
var installVerbRE = regexp.MustCompile(`\binstall\b`)

// trimTermEdges drops the slash and dot the token regex captured at a boundary
// — a URL matched inside quotes carries leading "//", a path a trailing "/" —
// so the same identifier compares equal wherever it appeared. It deliberately
// keeps a leading dash: `command not found: timeout` must match a command that
// invokes `timeout`, not one that passes a `-timeout` flag to something else.
func trimTermEdges(t string) string {
	return strings.Trim(t, "/.")
}

// fixPairsIn mines one session.
func fixPairsIn(ms []model.Message, key, project string) []FixPair {
	var out []FixPair
	for i, m := range ms {
		if m.Role != roleToolOutput && m.Role != "assistant" {
			continue
		}
		line, sig, ok := firstFrictionLine(m.Text)
		if !ok {
			continue
		}
		for j := i + 1; j < len(ms) && j <= i+fixLookAhead; j++ {
			if ms[j].Role != roleCommand {
				continue
			}
			cmd := strings.TrimSpace(firstLineOf(ms[j].Text))
			if cmd == "" || len(cmd) > fixCommandMax {
				// A heredoc or pasted script right after the error is not the
				// remedy; keep scanning the window for the real one-liner two
				// records on, instead of abandoning the error entirely.
				continue
			}
			if repeatsError(ms, j+1, j+fixQuietAfter, sig) {
				break
			}
			out = append(out, FixPair{Sig: sig, Error: line, Command: cmd, Key: key, When: ms[j].Time, Project: project})
			break
		}
	}
	return out
}

// firstFrictionLine returns the first line of a record that names something
// specific that went wrong, with its hash.
func firstFrictionLine(text string) (string, uint64, bool) {
	for _, raw := range strings.Split(text, "\n") {
		if line, ok := FrictionLine(raw); ok {
			return line, frictionHash(line), true
		}
	}
	return "", 0, false
}

// repeatsError reports whether the same error shows up again in ms[from:to].
func repeatsError(ms []model.Message, from, to int, sig uint64) bool {
	for k := from; k < len(ms) && k <= to; k++ {
		if ms[k].Role != roleToolOutput && ms[k].Role != "assistant" {
			continue
		}
		for _, raw := range strings.Split(ms[k].Text, "\n") {
			if line, ok := FrictionLine(raw); ok && frictionHash(line) == sig {
				return true
			}
		}
	}
	return false
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// LooksLikeError reports whether any line of the text names something
// specific that went wrong. Callers use it to tell "never seen this error"
// apart from "that was not an error".
func LooksLikeError(text string) bool {
	for _, raw := range strings.Split(text, "\n") {
		if _, ok := FrictionLine(raw); ok {
			return true
		}
	}
	return false
}

// ReadFixes loads the mined pairs. An index built before they existed simply
// has none.
func ReadFixes(dir string) []FixPair {
	var out []FixPair
	if err := readGob(fixesPath(dir), &out); err != nil {
		return nil
	}
	return out
}

// FixesFor returns the commands that followed this error before, newest first.
// The text can be a whole pasted stack trace: every line is tried, so the
// caller does not have to know which one carries the signature. allow, when
// non-nil, gates each pair by its project — the caller applies its trust
// policy here, since this package sits below policy.
func FixesFor(dir, text string, limit int, allow func(project string) bool) []FixPair {
	if limit <= 0 {
		limit = 3
	}
	sigs := map[uint64]bool{}
	for _, raw := range strings.Split(text, "\n") {
		if line, ok := FrictionLine(raw); ok {
			sigs[frictionHash(line)] = true
		}
	}
	if len(sigs) == 0 {
		return nil
	}
	var out []FixPair
	for _, p := range ReadFixes(dir) {
		if !sigs[p.Sig] {
			continue
		}
		if allow != nil && !allow(p.Project) {
			continue
		}
		out = append(out, p)
		if len(out) >= limit {
			break
		}
	}
	return out
}

package index

import (
	"cmp"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
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
	// fixRawFile holds every pair mined from the corpus before judging, one row
	// per session that produced it. The served table keeps one row per error
	// and remedy, so the repeats that promoted a pair are gone from it, and an
	// update that re-read the session behind a repeat could not take the
	// promotion back. Judged again from this, an update leaves what a rebuild
	// leaves. Read by updates only.
	fixRawFile = "fixraw.gob"
	// fixLookAhead is how far past the error a command still counts as an
	// answer to it. Beyond that the session has moved on to something else.
	fixLookAhead = 10
	// fixEditLookAhead extends the edit+verification scan across multi-step
	// tool-use loops (read file -> inspect -> edit -> run suite) when no shell
	// command remedy settled the error within fixLookAhead.
	fixEditLookAhead = 18
	// The quiet check runs to the end of the session rather than over a window.
	// Six records was long enough to miss the session hitting the same error
	// again a little later, which is the transcript saying the command did not
	// settle it: measured over this machine's transcripts, 104 of the 831 pairs
	// the miner kept were contradicted that way, and each one is a wrong answer
	// handed to an agent at the moment it is stuck.
	// fixOutputWindow is how far past a command its own output can sit. One
	// record in the ordinary case; two leaves room for a harness that writes
	// something between them.
	fixOutputWindow = 2
	// fixCommandMax bounds what is stored per pair; a command longer than this
	// is a heredoc or a pasted script, not something to hand back.
	fixCommandMax = 200
	// fixesMax bounds the whole file. Newest wins.
	fixesMax = 2000
	// fixesCandidateMax bounds the sightings held for a second confirmation.
	// Newest first, like the pairs themselves, so a candidate ages out rather
	// than displacing one that just arrived.
	fixesCandidateMax = 2000
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
	// Edit is the file a session changed after the error, for the failures a
	// command cannot answer. What fixes a failing test is an edit, and the
	// miner only ever looked for the next command — so the most common failure
	// an agent sees had no remedy it could record (#2163). Set instead of
	// Command, never beside it: an edit is evidence of what was changed, not
	// something a reader can run.
	Edit string `json:",omitempty"`
	// Symbol is the declaration or call identifier extracted from the Edit span
	// when the remedy was a file edit, pointing the reader at the exact symbol
	// inside Edit rather than the whole file.
	Symbol string `json:",omitempty"`
	// Why is a single causal sentence spoken by the assistant around the Edit
	// that explains why the failure happened or how the change settled it.
	Why string `json:",omitempty"`
	// SelfCorrected marks an edit remedy where the same session edited the file,
	// hit the failure, edited the same file again, and passed a verification
	// command without the error recurring. That closed causal loop proves the
	// edit settled the failure without waiting for a second session to repeat it.
	SelfCorrected bool `json:",omitempty"`
	// Repaired marks the remedy as the failing command corrected — same
	// program, most of the same words. That is evidence on its own, and of a
	// different kind than the word rules: the pair is not a command that
	// happened to follow the error, it is the command that caused it, working.
	Repaired bool `json:",omitempty"`
	// Failed is the command that produced the error, stored for a repaired
	// remedy so the reader can see what the correction was. Without it the
	// remedy reads as an unrelated line: 107 of the 360 pairs served on a real
	// store name nothing the error names, because they are not a command about
	// the error, they are the command that caused it, working.
	Failed string `json:",omitempty"`
	// Substitute marks the remedy for a missing program as another program
	// doing its job on the same arguments: `git ls-remote …/o/r refs/pull/9`
	// after `gh pr view 9 --repo o/r` failed with `gh: command not found`.
	// Evidence of the same kind as Repaired — the session wanted exactly this
	// done and found a way — so it does not wait for a second sighting.
	Substitute bool `json:",omitempty"`
	// Candidate marks a sighting that is not a pair yet: the remedy named
	// nothing the error named, and no other session has done the same thing
	// after the same error. Evidence of the second kind accumulates across
	// sessions, and sessions arrive one at a time — so judging a candidate
	// against the update it arrived in threw it away before the session that
	// would have confirmed it existed, and the pair was lost for good (#1301).
	// Kept, and promoted on the second sighting; never served to a caller.
	Candidate bool `json:",omitempty"`
	// Also holds the signatures of the other error lines in the same output.
	// A failing test prints its name first and the reason under it, friction
	// counts both, and a reader pastes whichever one explains it; keyed on the
	// first line alone, `deja fix` with the second found nothing.
	Also []uint64 `json:",omitempty"`
}

// answers reports whether the pair is about any of these signatures.
func (p FixPair) answers(sigs map[uint64]bool) bool {
	if sigs[p.Sig] {
		return true
	}
	for _, s := range p.Also {
		if sigs[s] {
			return true
		}
	}
	return false
}

func fixesPath(dir string) string { return filepath.Join(dir, fixesFile) }

// buildFixes writes the mined pairs into the build directory. Like every other
// sidecar here, failures are swallowed: this is an extra, never a reason to
// fail an index build.
//
// ss is the sessions as commandSessions gives them, the record log's view: a
// pass mines a session again from its records, and a copy of its id in another
// project's directory was mined with the row's copy there and on its own here,
// so the pass paired one file's error with the other's remedy (#5504).
func buildFixes(tmp string, ss []model.Session, keyOf func(model.Session) string) {
	invalidateFixesCache()
	// Mining is per session and reads nothing shared, and it was 9.4 s of a
	// 51 s build on a real store — the longest of the four sidecars, with the
	// rest of the machine idle. judgeFixes sorts before it judges, so what
	// lands on file does not depend on which worker finished first.
	perSession := make([][]FixPair, len(ss))
	parallelForRanked(len(ss), func(i int) {
		perSession[i] = fixPairsIn(ss[i].Messages, keyOf(ss[i]), ss[i].Project)
	})
	var all []FixPair
	for _, pairs := range perSession {
		all = append(all, pairs...)
	}
	if len(all) == 0 {
		return
	}
	_ = writeGobAtomic(fixRawPath(tmp), fixRawDisk{Version: fixRawVersion, Pairs: all})
	if kept := judgeFixes(all); len(kept) > 0 {
		_ = writeGobAtomic(fixesPath(tmp), kept)
	}
}

func fixRawPath(dir string) string { return filepath.Join(dir, fixRawFile) }

// judgeFixes turns every pair mined from the corpus into the table served.
func judgeFixes(all []FixPair) []FixPair {
	// Newest first, and a fixed order among pairs of the same moment, so the
	// row that survives below is the same whichever path collected them.
	all = slices.Clone(all)
	slices.SortStableFunc(all, func(a, b FixPair) int {
		if c := b.When.Compare(a.When); c != 0 {
			return c
		}
		return cmp.Or(strings.Compare(a.Key, b.Key), cmp.Compare(a.Sig, b.Sig),
			strings.Compare(a.Command, b.Command), strings.Compare(a.Edit, b.Edit), strings.Compare(a.Error, b.Error))
	})
	// Sequence alone is weak evidence: on a real store only 13% of "the next
	// command" mentioned anything the error named, so the other 87% are the
	// session moving on to unrelated work. A pair is kept when it carries a
	// second, independent reason to believe it — the command names something
	// the error named, or the same remedy followed the same error in another
	// session. That takes 356 pairs down to the ones worth handing an agent.
	repeats := map[string]int{}
	selfCorrectedKeys := map[string]bool{}
	for _, p := range all {
		k := fixKey(p)
		repeats[k]++
		if p.SelfCorrected {
			selfCorrectedKeys[k] = true
		}
	}
	out := all[:0]
	byKeyMeta := map[string]FixPair{}
	for _, p := range all {
		k := fixKey(p)
		if selfCorrectedKeys[k] {
			p.SelfCorrected = true
		}
		// Not a pair, and not nothing: the second kind of evidence accumulates
		// across sessions, and sessions arrive one at a time. Dropping a
		// sighting here meant the session that would have confirmed it found
		// nothing to confirm, so a full build reached pairs the same corpus
		// grown one session at a time never did (#1301).
		p.Candidate = !selfEvidentPair(p) && (!repetitionConfirms(p) || repeats[k] < 2)
		if prev, ok := byKeyMeta[k]; !ok {
			byKeyMeta[k] = p
		} else {
			if prev.Symbol == "" && p.Symbol != "" {
				prev.Symbol = p.Symbol
			}
			if prev.Why == "" && p.Why != "" {
				prev.Why = p.Why
			}
			if !prev.SelfCorrected && p.SelfCorrected {
				prev.SelfCorrected = true
			}
			byKeyMeta[k] = prev
		}
		out = append(out, p)
	}
	// One command per (error, command) is enough — the rest are the same fix
	// from other days. Keying on the command alone was wrong: a generic remedy
	// (`go mod tidy`, `npm install`) is the answer to several different errors,
	// and dropping all but its newest occurrence deleted the pair for every
	// other signature it settled.
	seen := map[string]bool{}
	candidates, pairs := 0, 0
	var kept []FixPair
	for _, p := range out {
		if p.Candidate {
			if candidates >= fixesCandidateMax {
				continue
			}
			candidates++
		} else {
			if pairs >= fixesMax {
				continue
			}
			pairs++
		}
		k := fixKey(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		if meta, ok := byKeyMeta[k]; ok {
			if p.Symbol == "" {
				p.Symbol = meta.Symbol
			}
			if p.Why == "" {
				p.Why = meta.Why
			}
			if !p.SelfCorrected && meta.SelfCorrected {
				p.SelfCorrected = true
			}
		}
		kept = append(kept, p)
		// Candidates have their own budget above, so the pair cap is what it
		// was and this one bounds the file as a whole.
		if len(kept) >= fixesMax+fixesCandidateMax {
			break
		}
	}
	return kept
}

// selfEvidentPair reports whether a remedy stands on what it is rather than on
// having happened before. An edit is never self-evident the way a command that
// names what the error named is: the error names a test, the remedy names a
// file, and nothing ties them but a second session doing the same thing.
func selfEvidentPair(p FixPair) bool {
	if p.Edit != "" {
		return p.SelfCorrected
	}
	if p.Repaired || p.Substitute {
		return true
	}
	if namedTestFailure(p.Error) && VerifyCommand(p.Command) {
		return false
	}
	return sharesTerm(p.Error, p.Command)
}

// MachineFact reports whether the pair answers a program or module missing
// from this machine with a command that got the job done. That is a fact
// about the machine, not about the project it was learned in: `timeout` is
// missing from every checkout on a Mac.
func (p FixPair) MachineFact() bool {
	if p.Candidate || p.Command == "" || (!p.Repaired && !p.Substitute) {
		return false
	}
	low := strings.ToLower(p.Error)
	for _, phrase := range envPhrases {
		if strings.Contains(low, phrase) {
			return true
		}
	}
	return IsShellDiagnostic(p.Error)
}

// repetitionConfirms reports whether a second sighting is evidence for this
// remedy at all.
//
// A failing test is repaired by editing the code, so a bare command that
// followed one is the session moving on, and on a machine where the same
// routine runs every day it moves on the same way twice. Read off a real store:
// of the 360 pairs `deja fix` serves, 69 rest on repetition alone; the 48 edits
// among them mostly name the file the failing test lives in, and of the 21
// commands eleven answer a red test with `gh pr merge 2532`,
// `git checkout -q -b work477` or `git status`.
//
// Only that shape is refused. `brew services start postgresql` after
// `psql: connection refused` is a command nothing but a second session ties to
// the error, and it is still the answer.
func repetitionConfirms(p FixPair) bool {
	if p.Edit != "" {
		return true
	}
	return !namedTestFailure(p.Error)
}

// fixKey identifies one remedy for one error, so the same pair arriving from
// two sessions can be counted as a repeat.
func fixKey(p FixPair) string {
	return strconv.FormatUint(p.Sig, 16) + "|" + strings.ToLower(p.Command) + "|" + strings.ToLower(p.Edit)
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

// lastFrictionIndex records, for every error the session hit, the last record
// it appears in. The quiet check asks whether an error came back after the
// command that was supposed to settle it, and asking that by rescanning the
// tail once per candidate is quadratic: on this machine's transcripts it took
// a full rebuild from 15.8s to 57.8s. One pass answers it for every pair.
func lastFrictionIndex(ms []model.Message) map[uint64]int {
	last := make(map[uint64]int)
	for i, m := range ms {
		if m.Role != roleToolOutput && m.Role != "assistant" {
			continue
		}
		for raw := range strings.SplitSeq(m.Text, "\n") {
			if line, ok := FrictionLine(raw); ok {
				last[frictionHash(line)] = i
			}
		}
	}
	return last
}

// genericFailure are the shapes isFriction turns away. It turns them away for
// identity — `Error: ` names nothing a second session can be matched on — and
// that is a different question from whether the command failed, which is all
// this asks.
var genericFailure = []string{
	"Traceback (most recent", "Error: ", "error: ", "FAIL\t", "--- FAIL", "panic: ",
}

// outputReadsAsFailure reports whether what a command printed is a failure:
// either a friction line, which is specific enough to be one, or one of the
// shapes friction declines to name.
func outputReadsAsFailure(text string) bool {
	if _, _, ok := firstFrictionLine(text); ok {
		return true
	}
	for raw := range strings.SplitSeq(text, "\n") {
		l := strings.TrimSpace(raw)
		for _, g := range genericFailure {
			if strings.HasPrefix(l, g) {
				return true
			}
		}
	}
	return false
}

// A child run's cut keeps the output lines this reads as failures, so the
// pairs in it can be mined.
func init() { sources.ChildOutputFailed = outputReadsAsFailure }

// commandFailed reads the exit status the record carries, for the harnesses
// that store one (codex and opencode append it; Claude does not).
func commandFailed(text string) bool {
	_, code, recorded := CommandExitOutcome(text)
	return recorded && code != 0
}

// outputFailed reports whether what came back from the command is itself an
// error. The same friction rules the error side uses, so a command that
// printed a fresh failure is read as one wherever the exit status is not
// recorded.
func outputFailed(ms []model.Message, cmd int) bool {
	for k := cmd + 1; k < len(ms) && k <= cmd+fixOutputWindow; k++ {
		if ms[k].Role == roleCommand {
			return false
		}
		if ms[k].Role != roleToolOutput {
			continue
		}
		return outputReadsAsFailure(ms[k].Text)
	}
	return false
}

// commandBefore is the command whose output the error at i came out of: the
// nearest command record above it, with nothing but its own output in between.
// A session that printed an error without running anything — a build step the
// harness ran itself — has none, and gets "".
func commandBefore(ms []model.Message, i int) string {
	for k := i - 1; k >= 0 && k >= i-fixOutputWindow; k-- {
		if ms[k].Role == roleCommand {
			return strings.TrimSpace(firstLineOf(ms[k].Text))
		}
		if ms[k].Role != roleToolOutput {
			return ""
		}
	}
	return ""
}

// fixPairsIn mines one session.
func fixPairsIn(ms []model.Message, key, project string) []FixPair {
	var out []FixPair
	lastSeen := lastFrictionIndex(ms)
	for i, m := range ms {
		if m.Role != roleToolOutput && m.Role != "assistant" {
			continue
		}
		line, sig, ok := firstFrictionLine(m.Text)
		if !ok {
			continue
		}
		also := otherFrictionSigs(m.Text, sig)
		// The command that produced this error, for the remedy that is that
		// same command corrected.
		env := commandLevelFailure(m.Text, line)
		failedCmd := resolveFailedCommand(ms, i, line, env)
		// The first file the session changed after the error, kept in case the
		// window holds no command that answers it.
		edited := ""
		editIdx := -1
		editVerified := false
		paired := false
		for j := i + 1; j < len(ms) && j <= i+fixEditLookAhead; j++ {
			if j > i+fixLookAhead && ms[j].Role == "user" {
				break
			}
			if ms[j].Role == roleEdit {
				// No file edit answers a line the shell refused; it is the
				// session going back to its work.
				cand := strings.TrimSpace(firstLineOf(ms[j].Text))
				if preferEditRemedyCandidate(edited, cand, lastSeen[sig], j, env) {
					edited = cand
					editIdx = j
					editVerified = false
				}
				continue
			}
			if ms[j].Role != roleCommand {
				continue
			}
			cmd := strings.TrimSpace(firstLineOf(ms[j].Text))
			if cmd == "" || len(cmd) > fixCommandMax || opensMoreInput(cmd) {
				// A heredoc or pasted script right after the error is not the
				// remedy; keep scanning the window for the real one-liner two
				// records on, instead of abandoning the error entirely.
				//
				// The length bound is what caught most of those, and it caught
				// them by luck: a short first line — `python - <<'EOF'` — was
				// stored whole and handed to an agent as the command to run,
				// where it waits on input that is not coming (#2051).
				continue
			}
			if lastSeen[sig] > j {
				if !env && j <= i+fixEditLookAhead {
					continue
				}
				break
			}
			// A command that failed is not a remedy for anything, and handing
			// one to an agent at the moment it is stuck is the worst place to
			// be wrong. Two ways to know: the record says so, where the
			// harness stored the exit status, and the output right after it
			// carries an error of its own. Keep scanning the window — a
			// session that tried something and failed usually tries again,
			// and the retry is the pair worth having.
			if commandFailed(ms[j].Text) || outputFailed(ms, j) {
				continue
			}
			if j > i+fixLookAhead {
				if editVerifiedByCommand(env, edited, cmd) {
					editVerified = true
					break
				}
				continue
			}
			// Reading something is not fixing it. The rule that keeps a pair —
			// the command names what the error named — is satisfied by
			// construction when the command greps for the symbol the compiler
			// complained about, so the table filled with the step an agent
			// takes between hitting an error and solving it.
			//
			// Unless the command is the failing one corrected. Then what it
			// does is beside the point: the agent wanted to run exactly this,
			// the shell would not let it, and the corrected line is the whole
			// remedy. The wall this was found on is zsh refusing
			// `--include=*.go` — a grep, rejected here as investigation, and
			// one of the most repeated walls on the machine that mined it.
			repaired := repairedVariant(failedCmd, cmd)
			substituted := false
			// Only when the refused line is known: without it there is nothing
			// to hold the next command against, and that command is judged
			// the way every other remedy is.
			if env && failedCmd != "" {
				repaired, substituted = envRemedy(line, failedCmd, cmd)
				// Anything else after a refused line is the session moving
				// on, and handed back it reads as the answer. An install is
				// still one, named or not: `brew install ripgrep` answers
				// `command not found: rg`.
				if !repaired && !substituted && !sharesTerm(line, cmd) && !installVerbRE.MatchString(strings.ToLower(cmd)) {
					continue
				}
			}
			// When the session changed a source file after a non-shell failure and
			// then ran a passing build/test verification command, the verification
			// command proves the edit settled the error — it is not a command remedy
			// that replaces the edit. Check before investigationCommand, which marks
			// `go test` / `go vet` as investigation to keep them out of Command pairs.
			if editVerifiedByCommand(env, edited, cmd) {
				editVerified = true
				break
			}
			if !repaired && !substituted && investigationCommand(cmd) {
				continue
			}
			// A command that names a scratch file is not a remedy anyone can
			// run, including the session that ran it: an agent scripts an edit
			// into a temp file, runs it, then re-runs the suite, and the whole
			// line reads as the fix for the failing test. On a real store that
			// was 38 of 186 confirmed pairs (#2163). Keep scanning — the
			// durable command usually follows.
			if namesAnEphemeralPath(cmd) {
				continue
			}
			// The failing command travels with the remedy that corrects it, and
			// only then: it is there to explain the remedy, and the same bound
			// the remedy has applies — a pasted script is not something to show.
			failed := ""
			if repaired && len(failedCmd) <= fixCommandMax {
				failed = failedCmd
			}
			out = append(out, FixPair{Sig: sig, Error: line, Command: cmd, Key: key,
				When: ms[j].Time, Project: project, Repaired: repaired, Failed: failed,
				Substitute: substituted, Also: also})
			paired = true
			break
		}
		// No command answered it, and the session changed a file: that is the
		// remedy for a failing test, which is what the command window can
		// never hold.
		if !paired && edited != "" && looksLikeEditedPath(edited) && lastSeen[sig] <= editIdx {
			sym, why := mineEditContext(ms, i, editIdx, edited, line)
			selfCorr := editVerified && hadPriorSameFileEdit(ms, i, edited)
			out = append(out, FixPair{
				Sig: sig, Error: line, Edit: edited, Symbol: sym, Why: why,
				SelfCorrected: selfCorr,
				Key:           key, When: m.Time, Project: project, Also: also,
			})
		}
	}
	return out
}

func resolveFailedCommand(ms []model.Message, i int, line string, env bool) string {
	failedCmd := commandBefore(ms, i)
	if !env {
		return failedCmd
	}
	// An error that names the missing program names the line it came from.
	// Hermes runs a turn's calls together and stores every output after all
	// commands, and a script fails on its ninth line, not its first.
	if ran := commandRunningBefore(ms, i, missingProgram(line)); ran != "" {
		return ran
	}
	if failedCmd == "" {
		return commandRunningBefore(ms, i, "")
	}
	return failedCmd
}

func preferEditRemedyCandidate(edited, cand string, lastSigIdx, j int, env bool) bool {
	if env || !looksLikeEditedPath(cand) || lastSigIdx > j {
		return false
	}
	return edited == "" || !isTestFileEditPath(cand) || isTestFileEditPath(edited)
}

func editVerifiedByCommand(env bool, edited, cmd string) bool {
	return !env && edited != "" && looksLikeEditedPath(edited) && !namesAnEphemeralPath(cmd) && VerifyCommand(cmd)
}

func isTestFileEditPath(p string) bool {
	low := strings.ToLower(filepath.Base(p))
	return strings.HasSuffix(low, "_test.go") ||
		strings.HasPrefix(low, "test_") ||
		strings.HasSuffix(low, "_test.py") ||
		strings.Contains(low, ".test.") ||
		strings.Contains(low, ".spec.")
}

func hadPriorSameFileEdit(ms []model.Message, errIdx int, edited string) bool {
	target := normalizeEditedRef(edited)
	if target == "" {
		return false
	}
	start := max(0, errIdx-32)
	for k := errIdx - 1; k >= start; k-- {
		if ms[k].Role != roleEdit {
			continue
		}
		prev := normalizeEditedRef(firstLineOf(ms[k].Text))
		if prev == "" {
			continue
		}
		if prev == target || strings.HasSuffix(prev, "/"+target) || strings.HasSuffix(target, "/"+prev) {
			return true
		}
	}
	return false
}

func normalizeEditedRef(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = strings.TrimPrefix(p, "./")
	return strings.ToLower(p)
}

// ephemeralPathRE matches a path under a directory whose contents do not
// survive: the system temp roots, and the per-session scratch directories an
// agent harness hands its tools. Anchored on a boundary and on the separator
// that follows, so a package path like ./internal/tmp/ and the word inside a
// commit message are left alone.
var ephemeralPathRE = regexp.MustCompile(`(^|[\s"'=(])(/tmp/|/var/folders/|/private/tmp/|\$TMPDIR/|\${TMPDIR}/)|/\.claude/jobs/[^/]+/tmp/`)

// namesAnEphemeralPath reports whether the command reaches for a file that is
// gone by the time deja would serve it.
func namesAnEphemeralPath(cmd string) bool {
	return ephemeralPathRE.MatchString(cmd)
}

// looksLikeEditedPath keeps the first line of an edit record to what a path can
// be. The record is "<path>\n<span>", but a span whose file was not recorded
// would otherwise put a line of source in the table.
func looksLikeEditedPath(p string) bool {
	// A path carries no whitespace and none of the punctuation source is
	// written in. "Makefile" is a path and "\tif err != nil {" is not;
	// requiring a dot or a slash would have refused the first, which is a real
	// file at the top of a repository.
	if p == "" || len(p) > 200 {
		return false
	}
	slash := strings.ReplaceAll(p, "\\", "/")
	if strings.Contains(slash, "/.claude/projects/") || strings.Contains(slash, "/.claude/jobs/") ||
		strings.Contains(slash, "/.cache/") {
		return false
	}
	return !strings.ContainsAny(stripWindowsDrive(p), " \t\"'`$(){}[]=;:,*")
}

func stripWindowsDrive(p string) string {
	if len(p) >= 3 && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) && p[1] == ':' && (p[2] == '/' || p[2] == '\\') {
		return p[2:]
	}
	return p
}

// opensMoreInput reports whether a command's first line is only the start of
// one. A pair stores that line and a reader runs it, so a heredoc opener is not
// a remedy but a hang.
func opensMoreInput(cmd string) bool {
	// A herestring is a whole command — `psql <<< "$sql"` reads from the line
	// it is on — so it is not one of these.
	for i := 0; ; {
		j := strings.Index(cmd[i:], "<<")
		if j < 0 {
			break
		}
		at := i + j
		if !strings.HasPrefix(cmd[at+2:], "<") {
			return true
		}
		i = at + 3
	}
	return strings.HasSuffix(cmd, "\\")
}

// firstFrictionLine returns the first line of a record that names something
// specific that went wrong, with its hash.
func firstFrictionLine(text string) (string, uint64, bool) {
	for raw := range strings.SplitSeq(text, "\n") {
		if line, ok := FrictionLine(raw); ok {
			return line, frictionHash(line), true
		}
	}
	return "", 0, false
}

// otherFrictionSigs is every error line of a record past the first, hashed,
// without repeats. Bounded: a build log can print hundreds.
func otherFrictionSigs(text string, first uint64) []uint64 {
	const most = 8
	var out []uint64
	seen := map[uint64]bool{first: true}
	for raw := range strings.SplitSeq(text, "\n") {
		line, ok := FrictionLine(raw)
		if !ok {
			continue
		}
		h := frictionHash(line)
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
		if len(out) >= most {
			break
		}
	}
	return out
}

func firstLineOf(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return s
}

// LooksLikeError reports whether any line of the text names something
// specific that went wrong. Callers use it to tell "never seen this error"
// apart from "that was not an error".
func LooksLikeError(text string) bool {
	for raw := range strings.SplitSeq(text, "\n") {
		if _, ok := FrictionLine(raw); ok {
			return true
		}
	}
	return false
}

// mergeFixes updates the pair tables with what the sessions in this
// incremental update contain.
//
// Carrying alone was not enough. A new session is a new file, and a new file
// never takes the append path, so every session a user starts goes through the
// incremental rebuild — which is exactly where new pairs come from. Carrying
// them meant `fix` answered only what the last full build had mined, and on a
// machine whose stores are append-only that build happens when the index
// version changes and not otherwise.
//
// The pairs of every session this update did not re-read are carried as they
// were mined, the re-read ones are mined again, and the whole set is judged the
// way a full build judges it. replacements are the re-read sessions whole,
// replaced their keys. live is the sessions the updated index holds, or nil
// when the update cannot have dropped one.
func mergeFixes(dir, tmp string, live map[string]bool, replacements []model.Session, replaced map[string]bool) {
	raw, ok := readFixRaw(dir)
	if !ok {
		// A table from before the raw one, or a torn one: the records in tmp are
		// the whole corpus by now, so it is mined again from them.
		remineFixes(tmp)
		return
	}
	// The served table is written from the raw one, but a table an older,
	// leakier build wrote is served until something rewrites it.
	served, torn := readFixesChecked(dir)
	changed := torn
	if slices.ContainsFunc(served, leaksCredential) {
		changed = true
	}
	kept := make([]FixPair, 0, len(raw))
	for _, p := range raw {
		// Whatever this update re-read is mined again below, and a session gone
		// from the index is gone from a rebuild's table too.
		if replaced[p.Key] || (live != nil && !live[p.Key]) {
			changed = true
			continue
		}
		// An index built before this path scrubbed its sessions holds pairs with
		// the credential still in them, and carrying is all that ever happens to
		// a pair whose session is not re-read — so without this the leak outlives
		// the fix for it, indefinitely, on the machine that already has it.
		if e, counts := redact.Text(p.Error); len(counts) > 0 {
			p.Error, changed = e, true
		}
		if c, counts := redact.Text(p.Command); len(counts) > 0 {
			p.Command, changed = c, true
		}
		if f, counts := redact.Text(p.Failed); len(counts) > 0 {
			p.Failed, changed = f, true
		}
		if w, counts := redact.Text(p.Why); len(counts) > 0 {
			p.Why, changed = w, true
		}
		kept = append(kept, p)
	}
	for _, s := range replacements {
		fresh := fixPairsIn(s.Messages, s.Key(), s.Project)
		changed = changed || len(fresh) > 0
		kept = append(kept, fresh...)
	}
	if changed {
		writeFixTables(tmp, kept)
	}
}

func leaksCredential(p FixPair) bool {
	for _, t := range []string{p.Error, p.Command, p.Failed, p.Why} {
		if _, counts := redact.Text(t); len(counts) > 0 {
			return true
		}
	}
	return false
}

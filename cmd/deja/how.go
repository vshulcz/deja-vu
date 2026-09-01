package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja how` answers "how do I run this here" from what was actually run here.
//
// Commands, file lists and edits are 38% of the record log and none of it
// reaches ranking: those roles are served only when asked for by role, because
// a path that happens to contain the words of a question is not an answer to
// it. But the command someone ran, with the flags that worked on this machine,
// is the most reusable thing in a store — and 9.6% of user turns on a
// 1165-session store are asking for exactly that.
//
// So it gets its own channel rather than a place in ranking: exact-ish match on
// the command text, ordered by how many separate sessions ran it.

const howCommandMax = 200

// firstCommandLine is firstLine for a command: the first line, bounded, and
// otherwise left alone. firstLine collapses runs of whitespace, which is right
// for the note titles it was written for and wrong here — `-run "Pool  Size"`
// came back as a different test filter (#2052).
func firstCommandLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) <= 80 {
		return s
	}
	return string(r[:80]) + "…"
}

type howEntry struct {
	Command  string
	Runs     int
	Sessions map[string]bool
	Last     time.Time
	// Outcomes is the number of runs a source recorded an exit status for —
	// on a real store about one in a hundred — and Failures how many of those
	// were not zero. Both, because "every run failed" is only true when deja
	// knows what every run did.
	Outcomes  int
	Failures  int
	ExitCode  int
	MixedExit bool
}

// failedEveryTime reports whether deja knows the outcome of every run of this
// command and every one of them failed. A command that sometimes fails is
// ordinary; a command deja has no outcome for is most of the store.
func (e howEntry) failedEveryTime() bool {
	return e.Outcomes > 0 && e.Outcomes == e.Runs && e.Failures == e.Outcomes
}

// failureNote is what the count line says about a command that never once
// worked here. `how` answers "how do I run this here", so offering such a
// command in the shape of one that has always worked is the sharpest miss the
// screen can make (#2624). It is not a ranking signal: one command record in a
// hundred carries an outcome at all, so ordering on it would be arbitrary.
func (e howEntry) failureNote() string {
	if !e.failedEveryTime() {
		return ""
	}
	what := "every run recorded here failed"
	if e.Runs == 1 {
		what = "the one run recorded here failed"
	}
	if e.MixedExit {
		return " · " + what
	}
	return fmt.Sprintf(" · %s (exit %d)", what, e.ExitCode)
}

func runHow(dir string, args []string, stdout io.Writer) error {
	limit := 8
	project := ""
	asJSON := false
	var terms []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--limit":
			// What was typed has to do something: a flag with nothing after it,
			// an unknown flag and an empty argument all used to pass through in
			// silence, the same holes files had (#1630, #1628).
			if i+1 >= len(args) {
				return fmt.Errorf("how: --limit needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("how: --limit wants a positive number, got %q", args[i])
			}
			limit = n
		case "--project":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return fmt.Errorf("how: --project needs value")
			}
			i++
			project = args[i]
		case "--":
			// A command can start with a dash — `deja how -- -run`.
			terms = append(terms, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("how: unknown flag %q", args[i])
			}
			if strings.TrimSpace(args[i]) != "" {
				terms = append(terms, args[i])
			}
		}
	}
	if len(terms) == 0 {
		return fmt.Errorf("usage: deja how <what> [--project name] [--limit n] [--json]")
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	entries, hidden, ignored, err := howEntries(dir, terms, project, policy.ActivationSearch)
	if err != nil {
		return err
	}
	if note := ignoredHiddenNoteFor("answer", ignored); note != "" {
		fmt.Fprint(os.Stderr, note)
	}
	if asJSON {
		return writeHowJSON(stdout, entries, limit, hidden, ignored)
	}
	if len(entries) == 0 {
		if note := policyHiddenNote(policy.ActivationSearch, hidden); note != "" {
			fmt.Fprint(stdout, note)
			return nil
		}
		fmt.Fprintf(stdout, "no command on this machine mentions %q\n", strings.Join(terms, " "))
		return nil
	}
	writeHowEntries(stdout, entries, limit, " · last ")
	// The cap said nothing, so eight of thirteen ways to run the tests read as
	// thirteen — the misread the search screen already avoids (#1632). On
	// stderr, where search puts the same line: stdout stays the list.
	if note := howCapNote(len(entries), limit, "raise --limit for the rest"); note != "" {
		fmt.Fprintf(os.Stderr, "deja: %s\n", note)
	}
	return nil
}

// writeHowEntries is the answer both surfaces print. The MCP tool used to
// build its own copy of this loop, so what the CLI learned to say the agent
// never heard — and the cap note was the drift showing (#1634). The one
// difference that is real stays a parameter: the separator before the date.
func writeHowEntries(w io.Writer, entries []howEntry, limit int, lastSep string) {
	for i, e := range entries {
		if i >= limit {
			break
		}
		when := ""
		if !e.Last.IsZero() {
			when = lastSep + e.Last.Local().Format("2006-01-02")
		}
		// The command itself, kept the way a person would copy it, folded onto
		// one line so a newline in it cannot forge a row of deja's (#1863).
		fmt.Fprintf(w, "%s\n", search.SafeCommand(e.Command))
		fmt.Fprintf(w, "  ran %s in %s%s%s\n",
			pluralRuns(e.Runs), pluralSessions(len(e.Sessions)), when, e.failureNote())
	}
}

// howCapNote is the sentence that says the list was cut, or empty when it was
// not. One sentence, so the agent and the reader are told the same thing —
// except for how to see the rest, which is a flag at a terminal and another
// call over MCP, where there is no flag to raise.
func howCapNote(found, limit int, raise string) string {
	if found <= limit {
		return ""
	}
	return fmt.Sprintf("showing %d of %d — %s", limit, found, raise)
}

// howEntries groups the commands that mention every term, so the same
// invocation run on twenty days counts as one answer with a weight.
//
// The activation is the caller's, not a constant: the same command table is
// served to the person at their own terminal and to an agent over MCP, and the
// trust policy keys on which of those it is. Hardcoding the search activation
// here meant a machine that allows imported memory in its owner's searches and
// denies it over MCP handed the imported command to the agent anyway — while
// recall, asked the same thing, correctly said it was withholding something.
//
// The count of what was withheld travels with it, because filtering alone turns
// a leak into a confident "no command mentions that" over records the policy
// hid, and an agent told nothing exists invents something.
func howEntries(dir string, terms []string, project, activation string) ([]howEntry, int, int, error) {
	pol := policy.Load()
	// Sessions, not records: the sentence this feeds says "matching sessions",
	// and one withheld session that ran a command five times was reported as
	// five (#1641, the shape #1639 fixed for friction).
	hidden := map[string]bool{}
	// The other rule that takes sessions out of an answer. It is applied inside
	// retrieval, and this screen reads the record log instead of ranking, so it
	// was not applied here at all: a tree the reader asked deja to stay out of
	// answered "how do I run this here" — and the default rule is the temp tree
	// a background agent makes for itself (#2630).
	ignored := map[string]bool{}
	byCmd := map[string]*howEntry{}
	err := index.EachRecordOfRole(dir, "command", func(meta index.SessionMeta, r index.Record) {
		if project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(project)) {
			return
		}
		// Without the outcome codex and opencode append: "$ make test  → exit
		// 2" is the same command as "$ make test", and counting them apart put
		// one command on two rows of this screen (#2590).
		line := strings.TrimSpace(firstCommandLine(r.Text))
		cmd := index.CommandWithoutExitStatus(line)
		if cmd == "" || len(cmd) > howCommandMax {
			return
		}
		low := strings.ToLower(cmd)
		for _, t := range terms {
			if !commandMentions(low, strings.ToLower(t)) {
				return
			}
		}
		// The rules after the question, not before it: the sentence says the
		// policy "hides N matching sessions", and counting every withheld
		// session that ran any command at all made that number about the
		// store rather than about what was asked — `deja how terraform` said
		// three were hidden on a machine whose hidden sessions only ever ran
		// `go test` (#2766). `files` is the shape this follows: it counts
		// from what was already searched.
		if !pol.Allows(activation, meta.Project) {
			hidden[meta.Harness+":"+meta.ID] = true
			return
		}
		if pol.Ignored(meta.Path, meta.Project) {
			ignored[meta.Harness+":"+meta.ID] = true
			return
		}
		e := byCmd[low]
		if e == nil {
			e = &howEntry{Command: cmd, Sessions: map[string]bool{}}
			byCmd[low] = e
		}
		e.Runs++
		if code, ok := index.CommandExitStatus(line); ok {
			e.Outcomes++
			if code != 0 {
				if e.Failures > 0 && code != e.ExitCode {
					e.MixedExit = true
				}
				e.ExitCode = code
				e.Failures++
			}
		}
		e.Sessions[r.Key] = true
		if r.Time.After(e.Last) {
			e.Last = r.Time
		}
	})
	if err != nil {
		return nil, len(hidden), len(ignored), fmt.Errorf("read: %w", err)
	}
	out := make([]howEntry, 0, len(byCmd))
	for _, e := range byCmd {
		out = append(out, *e)
	}
	// Sessions before runs: a command run forty times in one session is one
	// person iterating, and a command run once in six sessions is how the
	// thing is done here.
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Sessions) != len(out[j].Sessions) {
			return len(out[i].Sessions) > len(out[j].Sessions)
		}
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Command < out[j].Command
	})
	return out, len(hidden), len(ignored), nil
}

func pluralRuns(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

func pluralSessions(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

// fold collapses runs of whitespace, for comparing a query against a command
// without touching the command itself.
func fold(s string) string { return strings.Join(strings.Fields(s), " ") }

// commandMentions is the membership test for `how`: the term has to appear in
// the command as a word, not inside a longer one. A raw substring test answered
// `how go` with `golangci-lint run ./...`, and ranked it first (#1630) — and the
// short names are exactly the ones people ask about: go, gh, rg, jq, sed, cd.
//
// A word here ends at anything that is not a letter or a digit, so `go` matches
// `go test` and `/usr/local/bin/go` but not `golangci` — and a dash or an
// underscore is a boundary like any other, so `lint` still finds
// `golangci-lint` and `node` still finds `node_modules`, which is how people
// name the halves of a hyphenated tool.
// A term that itself carries a separator (`go test`, `./x`) is matched as
// written: it is already more specific than one word.
func commandMentions(low, term string) bool {
	if term == "" {
		return false
	}
	if strings.ContainsFunc(term, func(r rune) bool { return !isCommandWordRune(r) }) {
		if strings.Contains(low, term) {
			return true
		}
		// Matching folds the whitespace the command keeps. The record holds
		// what ran, spacing and all (#2052), so `deja how "pool size"` stopped
		// finding a command written `"Pool  Size"` — the answer is to compare a
		// folded copy, not to print one.
		if strings.ContainsFunc(term, unicode.IsSpace) {
			return strings.Contains(fold(low), fold(term))
		}
		return false
	}
	for at := 0; ; {
		i := strings.Index(low[at:], term)
		if i < 0 {
			return false
		}
		i += at
		beforeOK := i == 0 || !isCommandWordRune(rune(low[i-1]))
		end := i + len(term)
		afterOK := end == len(low) || !isCommandWordRune(rune(low[end]))
		if beforeOK && afterOK {
			return true
		}
		at = i + 1
	}
}

func isCommandWordRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// howJSON is the `deja how --json` envelope. See docs/json-output.md.
//
// `how` answers "how do I run this here", and its answer is the single output most
// worth piping somewhere else — yet it was the one command in this family without
// `--json` (#1931).
//
// Carries `found` and `truncated` for the reason the version 2 search envelope does:
// the cap note lives on stderr, so a caller reading stdout alone cannot otherwise tell
// eight ways to run the tests from thirteen.
type howJSON struct {
	SchemaVersion int  `json:"schema_version"`
	Found         int  `json:"found"`
	Truncated     bool `json:"truncated"`
	// Withheld and Ignored are what the two rules took out before any of this
	// was counted. howEntries carries them for a stated reason — filtering on
	// its own turns a leak into a confident "no command mentions that" over
	// records a rule hid — and the prose path says so in a sentence. Without
	// them here the envelope reintroduces exactly what the counts exist to
	// prevent: `commands: []` reads as "nothing matched" when it can mean
	// "everything that matched was withheld".
	Withheld int          `json:"withheld"`
	Ignored  int          `json:"ignored"`
	Commands []howRowJSON `json:"commands"`
}

type howRowJSON struct {
	Command  string `json:"command"`
	Runs     int    `json:"runs"`
	Sessions int    `json:"sessions"`
	// Last is omitted rather than zero-valued: a command with no recorded time is a
	// real state, and "0001-01-01" is not a date.
	Last string `json:"last,omitempty"`
	// FailedEveryTime is the prose's failure note as a field. `how` offering a command
	// that never once worked, in the shape of one that always has, is the sharpest miss
	// this surface can make — so a machine reader must be able to see it too.
	FailedEveryTime bool `json:"failed_every_time"`
	// Outcomes is how many runs recorded an exit status at all (about one in a hundred
	// on a real store), so a consumer can weigh `failed_every_time` rather than trust it
	// blind. ExitCode is omitted when the failures disagreed about it.
	Outcomes int  `json:"outcomes"`
	Failures int  `json:"failures"`
	ExitCode *int `json:"exit_code,omitempty"`
}

func writeHowJSON(stdout io.Writer, entries []howEntry, limit, withheld, ignored int) error {
	found := len(entries)
	if len(entries) > limit {
		entries = entries[:limit]
	}
	rows := make([]howRowJSON, 0, len(entries))
	for _, e := range entries {
		row := howRowJSON{
			// The same sanitiser the prose path uses: a recorded command is untrusted
			// text, and a JSON consumer pasting it into a shell is no safer than a human.
			Command:         search.SafeCommand(e.Command),
			Runs:            e.Runs,
			Sessions:        len(e.Sessions),
			FailedEveryTime: e.failedEveryTime(),
			Outcomes:        e.Outcomes,
			Failures:        e.Failures,
		}
		if !e.Last.IsZero() {
			row.Last = e.Last.UTC().Format(time.RFC3339)
		}
		if e.failedEveryTime() && !e.MixedExit {
			code := e.ExitCode
			row.ExitCode = &code
		}
		rows = append(rows, row)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(howJSON{
		SchemaVersion: jsonout.Version,
		Found:         found,
		Truncated:     found > len(rows),
		Withheld:      withheld,
		Ignored:       ignored,
		Commands:      rows,
	})
}

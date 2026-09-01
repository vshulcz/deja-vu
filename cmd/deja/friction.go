package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja friction` names what this machine keeps tripping over.
//
// The idea started as "remember the dead ends" (#527) — abandoned engineering
// approaches an agent should be warned away from. Measured twice, that is not
// what a corpus contains. What recurs across sessions is a tool missing from
// the machine, a Python module never installed, a command that does not exist
// on this platform. Every one of the eight most-recurring error lines on a
// 1150-session store is that, and five appear in more than one harness.
//
// So this reports friction, not knowledge, and it is deliberately a small
// claim: the same specific error, in N separate sessions.

func runFriction(dir string, args []string, stdout io.Writer) error {
	limit := 10
	for i := 0; i < len(args); i++ {
		// Anything else used to be dropped on the floor, so `deja friction
		// --json` and `--limt 3` answered in prose and exited 0 as though they
		// had been understood (#2253).
		if args[i] != "--limit" {
			return fmt.Errorf("friction: unknown flag %q — it takes --limit n", args[i])
		}
		if i+1 >= len(args) {
			return fmt.Errorf("friction: --limit needs value")
		}
		i++
		n, err := strconv.Atoi(args[i])
		if err != nil || n <= 0 {
			return fmt.Errorf("friction: --limit wants a positive number, got %q", args[i])
		}
		limit = n
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	type seen struct {
		sessions map[string]bool
		harness  map[string]bool
		last     time.Time
		// text is the occurrence to show. One failure prints differently on
		// every run — a port, a pid, an ip — and the signature is what folds
		// them together, so the row needs one of the texts to name: the newest,
		// because the port it printed last is the port the reader will see next
		// (#2375).
		text string
	}
	found := map[uint64]*seen{}
	sessions := map[string]bool{}
	// friction reads across the whole store, so a peer's recurring errors — its
	// hosts, its infra IPs — surfaced here even when the trust policy withheld
	// imported content from every other browsing surface. Browsing is the search
	// activation, as in `last` and `stats` (#937, and its friction gap #1120).
	pol := policy.Load()
	// Sessions, not records: the note says "hides N matching sessions", and
	// counting the callback's firings reported one hidden session with ten
	// error lines as ten (#1639).
	withheldSessions := map[string]bool{}
	// And the rule that keeps a tree out of recall altogether. friction reads
	// the record log rather than ranking, so it never passed through the place
	// the rule is applied and read the ignored tree's errors as this machine's
	// own (#2630).
	ignoredSessions := map[string]bool{}
	// One pass over the record log rather than a load per session: loading by
	// identity walks the whole log each time, which put this command at 2m46s
	// on a 1150-session store.
	if err := index.EachToolOutput(dir, func(meta index.SessionMeta, r index.Record) {
		if !pol.Allows(policy.ActivationSearch, meta.Project) {
			withheldSessions[meta.Harness+":"+meta.ID] = true
			return
		}
		if pol.Ignored(meta.Path, meta.Project) {
			ignoredSessions[meta.Harness+":"+meta.ID] = true
			return
		}
		key := meta.Harness + ":" + meta.ID
		sessions[key] = true
		for _, line := range strings.Split(r.Text, "\n") {
			line, sig, ok := index.FrictionSignature(line)
			if !ok {
				continue
			}
			s := found[sig]
			if s == nil {
				s = &seen{sessions: map[string]bool{}, harness: map[string]bool{}, text: line}
				found[sig] = s
			}
			s.sessions[key] = true
			s.harness[meta.Harness] = true
			if r.Time.After(s.last) || s.text == "" {
				s.last = r.Time
				s.text = line
			}
		}
	}); err != nil {
		return fmt.Errorf("friction: %w", err)
	}
	if note := policyHiddenNote(policy.ActivationSearch, len(withheldSessions)); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	if note := ignoredHiddenNoteFor("answer", len(ignoredSessions)); note != "" {
		fmt.Fprint(os.Stderr, note)
	}

	type row struct {
		line      string
		when      time.Time
		n         int
		harnesses []string
	}
	var rows []row
	for _, s := range found {
		line := s.text
		if len(s.sessions) < index.FrictionMinSessions {
			continue
		}
		hs := make([]string, 0, len(s.harness))
		for h := range s.harness {
			hs = append(hs, h)
		}
		sort.Strings(hs)
		rows = append(rows, row{line, s.last, len(s.sessions), hs})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].line < rows[j].line
	})
	if len(rows) == 0 {
		// The count is sessions-with-tool-output, and it reads as
		// sessions-indexed: a store with six conversations reported zero,
		// which is the sentence #637 was about — a reader told the index holds
		// nothing concludes the tool is broken (#705).
		// Two counts, because they answer two questions. What is on disk
		// decides whether this machine has any history at all, and what the
		// rules leave decides how much friction had to read: naming the store
		// for the second put "none of the 4 indexed sessions" over a note
		// saying two of them are withheld (#2709, the shape #2707 fixed on the
		// empty search). Reading the store count for the first is what keeps a
		// machine whose history is entirely behind a rule from being told it
		// has none.
		stored, err := index.SessionCount(dir)
		total := stored
		if reach, _, _, ok := reachableSessionCount(dir); ok {
			total = reach
		}
		switch {
		case err != nil || stored == 0:
			// "no sessions are indexed yet" is a claim about the machine, and
			// a store deja is not allowed to open produces the same zero —
			// the failure #1020 closed on last and search. friction is where
			// someone lands when recall feels thin, so it is the worst place
			// to say the history is not there (#1044).
			fmt.Fprintln(stdout, strings.TrimPrefix(emptyIndexHint("nothing recurring"), "deja: "))
		case len(sessions) == 0 && len(withheldSessions) > 0:
			// The sessions that recorded tool output are exactly the ones a
			// rule took away, and saying the machine never had them is a claim
			// about the store rather than about the rule — the misread #637
			// and #1044 closed elsewhere. The stderr note above says the same
			// thing, and stdout is what a redirect keeps (#2319).
			fmt.Fprintf(stdout, "nothing recurring — the trust policy withheld the %d session%s that recorded tool output, which is what friction reads errors from\n",
				len(withheldSessions), pluralS(len(withheldSessions)))
		case len(sessions) == 0 && len(ignoredSessions) > 0:
			// The same claim about the store rather than about the rule, for
			// the other rule: the sessions with the tool output are there and
			// the ignore rule is what keeps them out (#2630).
			fmt.Fprintf(stdout, "nothing recurring — the ignore rule keeps the %d session%s that recorded tool output out of recall, which is what friction reads errors from\n",
				len(ignoredSessions), pluralS(len(ignoredSessions)))
		case len(sessions) == 0 && total == 0:
			// Every session on this machine is behind a rule, and none of them
			// recorded tool output — so neither arm above fired and the count
			// is zero. "none of the 0 indexed sessions" is arithmetic; what
			// the reader needs is which rule leaves friction nothing to read.
			fmt.Fprintf(stdout, "nothing recurring — %s keeps every one of the %d indexed session%s out of recall, and friction reads errors from what it may open\n",
				emptiedBy(pol), stored, pluralS(stored))
		case len(sessions) == 0:
			fmt.Fprintf(stdout, "nothing recurring — none of the %d indexed session%s recorded tool output, which is what friction reads errors from\n",
				total, pluralS(total))
		case len(sessions) == total:
			// The parenthetical is there to say how much was not read. When
			// the rules leave exactly the sessions that recorded tool output,
			// it repeats the number beside it.
			fmt.Fprintf(stdout, "nothing recurring in the %d session%s that recorded tool output — no error hit %d separate sessions\n",
				len(sessions), pluralS(len(sessions)), index.FrictionMinSessions)
		default:
			fmt.Fprintf(stdout, "nothing recurring in the %d session%s that recorded tool output (of %d indexed) — no error hit %d separate sessions\n",
				len(sessions), pluralS(len(sessions)), total, index.FrictionMinSessions)
		}
		return nil
	}
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Fprintf(stdout, "what this machine keeps tripping over — %d session%s read\n", len(sessions), pluralS(len(sessions)))
	for _, r := range rows {
		where := strings.Join(r.harnesses, ", ")
		fmt.Fprintf(stdout, "  %2d sessions  %s\n", r.n, trimFriction(r.line))
		fmt.Fprintf(stdout, "               %s", where)
		if !r.when.IsZero() {
			fmt.Fprintf(stdout, " · last %s", r.when.Local().Format("Jan 2"))
		}
		fmt.Fprintln(stdout)
	}
	// The header claims to say what this machine keeps tripping over, so a cut
	// list with nothing after it reads as all of it — the sentence `how` and
	// `files` print for the same flag (#2311).
	if total > len(rows) {
		fmt.Fprintf(os.Stderr, "deja: showing %d of %d — raise --limit for the rest\n", len(rows), total)
	}
	return nil
}

func trimFriction(l string) string {
	// Sanitised first. A wall is text out of a transcript deja did not write,
	// and this was the one surface printing it as recorded: an ANSI escape
	// recoloured the rest of the screen and U+202E reversed the reading order
	// of everything after it. Every other place a recorded line is shown runs
	// it through SafeLine; this now does too.
	//
	// Rune-safe: a wall recorded in Russian or Chinese was cut mid-character
	// and printed as a broken byte (#1319). The bound includes the mark, so a
	// line loses one character rather than gaining one.
	return truncatePlanBytes(search.SafeLine(l), 79)
}

// emptiedBy names the rule that leaves a path nothing to read. Both can be in
// force; the ignore rule is named first because it is the one written by hand
// in a file the reader edits.
func emptiedBy(pol policy.Policy) string {
	if len(pol.IgnorePatterns()) > 0 {
		return "the ignore rule (" + strings.Join(pol.IgnorePatterns(), ", ") + ")"
	}
	return "the trust policy (" + policy.ActivationSearch + ": " + pol.Describe(policy.ActivationSearch) + ")"
}

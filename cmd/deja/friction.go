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
		if args[i] == "--limit" && i+1 < len(args) {
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("friction: --limit wants a positive number, got %q", args[i])
			}
			limit = n
		}
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	type seen struct {
		sessions map[string]bool
		harness  map[string]bool
		last     time.Time
	}
	found := map[string]*seen{}
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
	// One pass over the record log rather than a load per session: loading by
	// identity walks the whole log each time, which put this command at 2m46s
	// on a 1150-session store.
	if err := index.EachToolOutput(dir, func(meta index.SessionMeta, r index.Record) {
		if !pol.Allows(policy.ActivationSearch, meta.Project) {
			withheldSessions[meta.Harness+":"+meta.ID] = true
			return
		}
		key := meta.Harness + ":" + meta.ID
		sessions[key] = true
		for _, line := range strings.Split(r.Text, "\n") {
			line, ok := index.FrictionLine(line)
			if !ok {
				continue
			}
			s := found[line]
			if s == nil {
				s = &seen{sessions: map[string]bool{}, harness: map[string]bool{}}
				found[line] = s
			}
			s.sessions[key] = true
			s.harness[meta.Harness] = true
			if r.Time.After(s.last) {
				s.last = r.Time
			}
		}
	}); err != nil {
		return fmt.Errorf("friction: %w", err)
	}
	if note := policyHiddenNote(policy.ActivationSearch, len(withheldSessions)); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}

	type row struct {
		line      string
		when      time.Time
		n         int
		harnesses []string
	}
	var rows []row
	for line, s := range found {
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
		total, err := index.SessionCount(dir)
		switch {
		case err != nil || total == 0:
			// "no sessions are indexed yet" is a claim about the machine, and
			// a store deja is not allowed to open produces the same zero —
			// the failure #1020 closed on last and search. friction is where
			// someone lands when recall feels thin, so it is the worst place
			// to say the history is not there (#1044).
			fmt.Fprintln(stdout, strings.TrimPrefix(emptyIndexHint("nothing recurring"), "deja: "))
		case len(sessions) == 0:
			fmt.Fprintf(stdout, "nothing recurring — none of the %d indexed session%s recorded tool output, which is what friction reads errors from\n",
				total, pluralS(total))
		default:
			fmt.Fprintf(stdout, "nothing recurring in the %d session%s that recorded tool output (of %d indexed) — no error hit %d separate sessions\n",
				len(sessions), pluralS(len(sessions)), total, index.FrictionMinSessions)
		}
		return nil
	}
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
	return nil
}

func trimFriction(l string) string {
	// Rune-safe: a wall recorded in Russian or Chinese was cut mid-character
	// and printed as a broken byte (#1319). The bound includes the mark, so a
	// line loses one character rather than gaining one.
	return truncatePlanBytes(l, 79)
}

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
const (
	// frictionMinSessions is how many separate sessions must hit an error
	// before it is worth saying. Twice is a coincidence; three times is the
	// machine telling you something.
	frictionMinSessions = 3
	frictionLineMin     = 20
	frictionLineMax     = 120
)

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
	// One pass over the record log rather than a load per session: loading by
	// identity walks the whole log each time, which put this command at 2m46s
	// on a 1150-session store.
	if err := index.EachToolOutput(dir, func(meta index.SessionMeta, r index.Record) {
		key := meta.Harness + ":" + meta.ID
		sessions[key] = true
		for _, line := range strings.Split(r.Text, "\n") {
			line = normalizeFriction(line)
			if !frictionLine(line) {
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

	type row struct {
		line      string
		when      time.Time
		n         int
		harnesses []string
	}
	var rows []row
	for line, s := range found {
		if len(s.sessions) < frictionMinSessions {
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
		fmt.Fprintf(stdout, "nothing recurring in %d session%s — no error hit %d separate sessions\n",
			len(sessions), pluralS(len(sessions)), frictionMinSessions)
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

// normalizeFriction strips the shell's position prefix so the same missing
// command counts once. `zsh:1: command not found: timeout` and
// `(eval):2: command not found: timeout` are one piece of friction; left
// alone they were three separate rows, each below the threshold that would
// have reported any of them.
func normalizeFriction(l string) string {
	l = strings.TrimSpace(l)
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

// frictionLine keeps the error shapes that name something specific. The
// generic ones carry no information — every Python failure prints `Traceback
// (most recent call last):`, and clustering those put an empty line at the top
// of every measurement this feature was built from.
func frictionLine(l string) bool {
	if len(l) < frictionLineMin || len(l) > frictionLineMax {
		return false
	}
	for _, generic := range []string{
		"Traceback (most recent", "Error: ", "error: ", "FAIL\t", "--- FAIL",
	} {
		if strings.HasPrefix(l, generic) {
			return false
		}
	}
	// Tool output carries source as often as it carries results — a `cat` of a
	// script, a diff, a heredoc. An `echo "App not found: $APP"` inside a
	// deploy script reached second place on the first run: it is a line about
	// an error, not an error.
	for _, source := range []string{"echo ", "\"", "$(", "=~", "print("} {
		if strings.Contains(l, source) {
			return false
		}
	}
	// This command's own output is tool output in the next session, and every
	// line of it contains an error by construction. Drop the report shape so
	// running it does not slowly teach it about itself.
	if i := strings.Index(l, " sessions  "); i > 0 {
		if _, err := strconv.Atoi(strings.TrimSpace(l[:i])); err == nil {
			return false
		}
	}
	for _, p := range []string{
		"command not found", "ModuleNotFoundError", "No module named",
		"not found: ", "cannot find", "no such file or directory",
		"undefined:", "connection refused", "permission denied",
	} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

func trimFriction(l string) string {
	if len(l) > 76 {
		return l[:76] + "…"
	}
	return l
}

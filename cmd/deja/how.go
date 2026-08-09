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

type howEntry struct {
	Command  string
	Runs     int
	Sessions map[string]bool
	Last     time.Time
}

func runHow(dir string, args []string, stdout io.Writer) error {
	limit := 8
	project := ""
	var terms []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 < len(args) {
				i++
				n, err := strconv.Atoi(args[i])
				if err != nil || n <= 0 {
					return fmt.Errorf("how: --limit wants a positive number, got %q", args[i])
				}
				limit = n
			}
		case "--project":
			if i+1 < len(args) {
				i++
				project = args[i]
			}
		default:
			if !strings.HasPrefix(args[i], "-") {
				terms = append(terms, args[i])
			}
		}
	}
	if len(terms) == 0 {
		return fmt.Errorf("usage: deja how <what> [--project name] [--limit n]")
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	entries, hidden, err := howEntries(dir, terms, project)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if note := policyHiddenNote(policy.ActivationSearch, hidden); note != "" {
			fmt.Fprint(stdout, note)
			return nil
		}
		fmt.Fprintf(stdout, "no command on this machine mentions %q\n", strings.Join(terms, " "))
		return nil
	}
	for i, e := range entries {
		if i >= limit {
			break
		}
		when := ""
		if !e.Last.IsZero() {
			when = " · last " + e.Last.Local().Format("2006-01-02")
		}
		fmt.Fprintf(stdout, "%s\n", search.SafeText(e.Command))
		fmt.Fprintf(stdout, "  ran %s in %s%s\n",
			pluralRuns(e.Runs), pluralSessions(len(e.Sessions)), when)
	}
	return nil
}

// howEntries groups the commands that mention every term, so the same
// invocation run on twenty days counts as one answer with a weight.
func howEntries(dir string, terms []string, project string) ([]howEntry, int, error) {
	pol := policy.Load()
	hidden := 0
	byCmd := map[string]*howEntry{}
	err := index.EachRecordOfRole(dir, "command", func(meta index.SessionMeta, r index.Record) {
		if !pol.Allows(policy.ActivationSearch, meta.Project) {
			hidden++
			return
		}
		if project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(project)) {
			return
		}
		cmd := strings.TrimSpace(firstLine(r.Text))
		if cmd == "" || len(cmd) > howCommandMax {
			return
		}
		low := strings.ToLower(cmd)
		for _, t := range terms {
			if !strings.Contains(low, strings.ToLower(t)) {
				return
			}
		}
		e := byCmd[low]
		if e == nil {
			e = &howEntry{Command: cmd, Sessions: map[string]bool{}}
			byCmd[low] = e
		}
		e.Runs++
		e.Sessions[r.Key] = true
		if r.Time.After(e.Last) {
			e.Last = r.Time
		}
	})
	if err != nil {
		return nil, hidden, fmt.Errorf("read: %w", err)
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
	return out, hidden, nil
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

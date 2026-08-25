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
		return fmt.Errorf("usage: deja how <what> [--project name] [--limit n]")
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	entries, hidden, err := howEntries(dir, terms, project, policy.ActivationSearch)
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
		fmt.Fprintf(stdout, "%s\n", search.SafeLine(e.Command))
		fmt.Fprintf(stdout, "  ran %s in %s%s\n",
			pluralRuns(e.Runs), pluralSessions(len(e.Sessions)), when)
	}
	// The cap said nothing, so eight of thirteen ways to run the tests read as
	// thirteen — the misread the search screen already avoids (#1632). On
	// stderr, where search puts the same line: stdout stays the list.
	if len(entries) > limit {
		fmt.Fprintf(os.Stderr, "deja: showing %d of %d — raise --limit for the rest\n", limit, len(entries))
	}
	return nil
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
func howEntries(dir string, terms []string, project, activation string) ([]howEntry, int, error) {
	pol := policy.Load()
	hidden := 0
	byCmd := map[string]*howEntry{}
	err := index.EachRecordOfRole(dir, "command", func(meta index.SessionMeta, r index.Record) {
		if !pol.Allows(activation, meta.Project) {
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
			if !commandMentions(low, strings.ToLower(t)) {
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
		return strings.Contains(low, term)
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

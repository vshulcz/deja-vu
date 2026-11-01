package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// noteAmbiguousPrefix says when a selector reached more than one session.
//
// The prefix picks the newest of its matches, which is the right default and
// was a silent one: "2" resolved eleven sessions on a real store. `show`
// learned to say so in #719 and #859; promote, handoff, resume and share
// resolve the same way and still picked in silence — promote records a state
// against whichever session it chose (#872).
func noteAmbiguousPrefix(dir, id, action string) {
	// Counted under the rule the reader is searching by: a session the policy
	// withholds is not one they can reach with a longer prefix (#2401).
	pol := policy.Load()
	n := index.PrefixMatchesAllowed(dir, id, func(project string) bool {
		return pol.Allows(policy.ActivationSearch, project)
	})
	if n <= 1 {
		return
	}
	// When the matches are the same id in different harnesses there is no
	// longer prefix to reach for, and naming the harness is the only thing
	// that separates them (#719). The `harness:id` form is the one every
	// command that resolves a selector accepts — show/last also take
	// --harness, but promote/handoff/resume/share do not, so advising the
	// flag sent those readers into "unknown flag --harness".
	if hs := index.PrefixHarnesses(dir, id); len(hs) > 1 {
		forms := make([]string, len(hs))
		for i, h := range hs {
			forms[i] = h + ":" + id
		}
		fmt.Fprintf(os.Stderr, "deja: %d sessions share the id %q — %s the most recent; name one as %s\n",
			len(hs), id, action, strings.Join(forms, " or "))
		return
	}
	// "A longer prefix" is not available when the reader copied an elided id
	// off a result line: the characters that would disambiguate are the ones
	// the elision replaced (#859).
	advice := "use a longer prefix for another"
	if strings.Contains(id, "…") {
		advice = "the ids differ in the middle the line elides — `deja last` prints them whole"
	}
	fmt.Fprintf(os.Stderr, "deja: %d sessions match %q — %s the most recent; %s\n", n, id, action, advice)
}

// ambiguousJSONPrefix refuses a --json read whose prefix names more than one
// session, and says what separates them.
func ambiguousJSONPrefix(dir, id string) error {
	pol := policy.Load()
	n := index.PrefixMatchesAllowed(dir, id, func(project string) bool {
		return pol.Allows(policy.ActivationSearch, project)
	})
	if n <= 1 {
		return nil
	}
	if hs := index.PrefixHarnesses(dir, id); len(hs) > 1 {
		return fmt.Errorf("%d sessions share the id %q — --json reads one; add --harness %s", len(hs), id, strings.Join(hs, " or --harness "))
	}
	return fmt.Errorf("%d sessions match %q — --json reads one; use a longer prefix (`deja last` prints ids whole)", n, id)
}

// searchAllows is the rule the ambiguity counts are taken under (#2401).
func searchAllows() func(string) bool {
	pol := policy.Load()
	return func(project string) bool { return pol.Allows(policy.ActivationSearch, project) }
}

// ambiguousJSONPrefixIn is ambiguousJSONPrefix within one harness: --harness
// narrows the prefix, and a prefix it still leaves ambiguous is refused the
// same way.
func ambiguousJSONPrefixIn(dir, id, harness string) error {
	if n := index.PrefixMatchesIn(dir, id, harness, searchAllows()); n > 1 {
		return fmt.Errorf("%d %s sessions match %q — --json reads one; use a longer prefix (`deja last` prints ids whole)", n, harness, id)
	}
	return nil
}

// noteAmbiguousPrefixIn is noteAmbiguousPrefix within one harness.
func noteAmbiguousPrefixIn(dir, id, harness, action string) {
	if n := index.PrefixMatchesIn(dir, id, harness, searchAllows()); n > 1 {
		fmt.Fprintf(os.Stderr, "deja: %d %s sessions match %q — %s the most recent; use a longer prefix for another\n", n, harness, id, action)
	}
}

// pickedOnScreen is the session the interactive screen handed to resume or
// handoff. It is looked up by its exact identity, with no refresh first: after
// an upgrade the refresh is a full rebuild, and the person who picked a
// session watched "waiting for it to finish" under a closed screen. And an id
// is not a prefix: one that starts another session's id handed off "the most
// recent" of them, which need not be the one picked.
var pickedOnScreen *model.Session

func findByPrefix(dir, p string) (model.Session, bool, error) { //nolint:unparam // handoff.go and coverage_extra_test.go read the error
	if s := pickedOnScreen; s != nil && p == s.ID {
		if full, ok, err := index.FindByIdentity(dir, s.Harness, s.ID); err == nil && ok {
			return full, true, nil
		}
	}
	if err := ensureForCLIRead(dir); err == nil {
		if s, ok, err := lookupUnlessNew(dir, func() (model.Session, bool, error) { return index.FindByPrefix(dir, p) }); err == nil {
			if ok {
				noteForgottenSource(s, p, true)
			}
			return s, ok, nil
		}
	}
	ss := loadFileSources()
	ss = append(ss, sources.LoadOpencodePrefix(p)...)
	s, ok := search.FindByPrefix(ss, p)
	return s, ok, nil
}

// findByPrefixHarness resolves an id prefix within one harness, so the
// documented "deja show <id-prefix> --harness name" form works.
// The prefix is resolved among that harness's sessions: picking the newest
// across every harness and then refusing it said no session matches while one
// in the named harness started with the prefix.
func findByPrefixHarness(dir, p, harness string) (model.Session, bool) {
	if err := ensureForCLIRead(dir); err == nil {
		if s, ok, err := lookupUnlessNew(dir, func() (model.Session, bool, error) { return index.FindByPrefixIn(dir, p, harness) }); err == nil {
			if ok {
				noteForgottenSource(s, p, true)
			}
			return s, ok
		}
	}
	var ss []model.Session
	for _, s := range append(loadFileSources(), sources.LoadOpencodePrefix(p)...) {
		if strings.EqualFold(s.Harness, harness) {
			ss = append(ss, s)
		}
	}
	return search.FindByPrefix(ss, p)
}

// ensureForCLIRead brings the index up to date before `last` or `show` reads
// it, unless another deja holds the index lock. A hook refresh or `deja index`
// holds it for its whole pass, and a scheduled `deja last` sat at no CPU for
// over an hour behind them while the snapshot on disk had the answer. The
// holder may be a reader that updates nothing, so the refresh goes to the
// detached warmup, as a search's does. The line goes out once: `show
// --harness` comes through here twice when the exact id misses.
func ensureForCLIRead(dir string) error {
	busy, err := index.EnsureUnlessBusy(dir, os.Stderr)
	answeredFromSnapshot.Store(busy)
	if line := manualIndexLine(dir); err == nil && line != "" {
		saidManualIndex.Do(func() { fmt.Fprintln(os.Stderr, line) })
	}
	if busy {
		requestWarmup(dir)
		saidReadingAsItWas.Do(func() {
			c := index.SnapshotCoverage(dir)
			c.Stale = true
			fmt.Fprintln(os.Stderr, "deja: "+coverageLine(c))
		})
	}
	return err
}

var saidReadingAsItWas sync.Once

// saidManualIndex keeps the index-date line to one per command.
var saidManualIndex sync.Once

// answeredFromSnapshot is whether the last ensureForCLIRead skipped the update
// because another deja held the lock.
var answeredFromSnapshot atomic.Bool

// lookupUnlessNew runs an id lookup against the snapshot and, when it misses
// while another deja holds the lock, waits for the index and looks again. A
// lock held at session start is a hook refresh reading the session that just
// began, so the id someone copied from it is the one the snapshot cannot have
// yet: answering "no session matches" for it at once was worse than the wait
// the snapshot read saves.
func lookupUnlessNew(dir string, find func() (model.Session, bool, error)) (model.Session, bool, error) {
	s, ok, err := find()
	if err != nil || ok || !answeredFromSnapshot.Load() {
		return s, ok, err
	}
	if index.Ensure(dir, "", false, os.Stderr) != nil {
		return s, ok, err
	}
	return find()
}

func recent(dir string, n int) ([]model.Session, error) {
	return recentMatching(dir, n, search.Options{})
}

func recentMatching(dir string, n int, o search.Options) ([]model.Session, error) {
	if err := ensureForCLIRead(dir); err == nil {
		if o.Role != "" {
			// The role has to travel into the index query, not just the filter
			// below: a scan with no role set drops file, command and edit
			// records on the way out — they are indexed but served only when
			// asked for — so `last --role files` saw sessions with the very
			// records it was selecting on already removed.
			ss, err := index.SearchWithRecovery(dir, search.Options{All: true, Role: o.Role}, io.Discard)
			if err == nil {
				ss = filterRecentSources(ss, o)
				return search.Recent(ss, n), nil
			}
		} else if ss, _, err := index.RecentMatchingCounted(dir, n, o); err == nil {
			return ss, nil
		}
	}
	ss := filterRecentSources(loadFileSources(), o)
	if o.Harness == "" || o.Harness == "opencode" {
		ss = append(ss, filterRecentSources(sources.LoadOpencodeRecent(n), o)...)
	}
	return search.Recent(ss, n), nil
}

var lastFlags = []string{"--json", "--project", "--harness", "--from", "--since", "--role", "--limit"}

func parseLast(args []string) (int, search.Options, string, error) {
	sinceRaw := ""
	n := 10
	seenN := false
	o := search.Options{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			o.JSON = true
		case "--harness", "--project", "--since", "--role", "--from":
			if i+1 >= len(args) {
				return n, o, sinceRaw, fmt.Errorf("%s needs value", a)
			}
			i++
			v := args[i] //nolint:gosec // bounds checked before i++
			// Empty is how "no filter" is spelled inside deja, so an empty
			// argument reached the search as no filter at all and a scripted
			// `--project "$PROJECT"` with the variable unset quietly returned
			// the whole store (#1612). It is the same mistake as leaving the
			// value off, so it gets the same sentence.
			if strings.TrimSpace(v) == "" {
				return n, o, sinceRaw, fmt.Errorf("%s needs value", a)
			}
			switch a {
			case "--harness":
				o.Harness = v
			case "--project":
				o.Project = v
			case "--role":
				o.Role = v
			case "--from":
				o.From = v
			default:
				d, err := parseDur(v)
				if err != nil {
					return n, o, sinceRaw, err
				}
				o.Since = d
				sinceRaw = v
			}
		default:
			if strings.HasPrefix(a, "-") {
				if flagName(a) == "--limit" {
					// The two commands beside this one take --limit, so the
					// reader is carrying it over rather than guessing; the
					// count last takes is a bare argument (#3405).
					return n, o, sinceRaw, fmt.Errorf("last: unknown flag %q — the count is a bare argument, `deja last 3`", a)
				}
				return n, o, sinceRaw, unknownFlag("last", a, lastFlags)
			}
			// The only bare argument last takes is the count. Dropping anything
			// else in silence answered `deja last api-gateway` — the filter the
			// help spells `--project api-gateway` — with ten sessions from every
			// project and no sign the word did nothing (#1618).
			x, err := strconv.Atoi(a)
			if err != nil || x < 1 {
				return n, o, sinceRaw, fmt.Errorf("last: %q is not a count — use `deja last 5`, or --project/--harness to narrow", a)
			}
			if seenN {
				return n, o, sinceRaw, fmt.Errorf("last takes one count, got %d and %q", n, a)
			}
			n, seenN = x, true
		}
	}
	return n, o, sinceRaw, nil
}

func filterRecentSources(ss []model.Session, o search.Options) []model.Session {
	// These are sessions read straight off this machine's stores, so they are
	// local by definition: asking for another machine's work must not hand
	// back this one's.
	if o.From != "" && !strings.EqualFold(o.From, "local") {
		return nil
	}
	if o.Harness == "" && o.Project == "" && o.Since <= 0 && o.Role == "" {
		return ss
	}
	cut := time.Time{}
	if o.Since > 0 {
		cut = time.Now().Add(-o.Since)
	}
	out := make([]model.Session, 0, len(ss))
	project := strings.ToLower(o.Project)
	for _, s := range ss {
		if o.Harness != "" && s.Harness != o.Harness {
			continue
		}
		if project != "" && !strings.Contains(strings.ToLower(s.Project), project) {
			continue
		}
		if !cut.IsZero() && s.Updated.Before(cut) {
			continue
		}
		if o.Role != "" && !sessionHasRole(s, o.Role) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// sessionHasRole accepts the role names the help text documents. `--role tool`
// is what `deja help` promises and "tool-output" is what is stored, so the
// documented spelling matched nothing here while `deja search --role tool` —
// which grew the alias in #623 — worked (#717).
func sessionHasRole(s model.Session, role string) bool {
	for _, m := range s.Messages {
		if m.Role == role || (role == "tool" && m.Role == "tool-output") {
			return true
		}
	}
	return false
}

func firstUserTitle(s model.Session) string {
	for _, msg := range s.Messages {
		if msg.Role != "user" {
			continue
		}
		t := strings.Join(strings.Fields(msg.Text), " ")
		r := []rune(t)
		if len(r) > 60 {
			return strings.TrimSpace(string(r[:60])) + "…"
		}
		return t
	}
	return ""
}

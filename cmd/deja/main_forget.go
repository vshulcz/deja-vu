package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// forgetArgs is what `deja forget` was asked.
type forgetArgs struct {
	o             index.ForgetOptions
	list          bool
	allMatches    bool
	unforget      string
	unforgetGiven bool
}

// parseForgetArgs reads the `deja forget` arguments.
func parseForgetArgs(args []string) (forgetArgs, error) {
	var o index.ForgetOptions
	list := false
	allMatches := false
	unforget, unforgetGiven := "", false
	given := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list":
			list = true
		case "--dry-run":
			o.DryRun = true
		case "--all-matches":
			allMatches = true
		case "--session", "--project", "--before", "--unforget":
			// Given twice, the last one used to win in silence: `deja forget
			// --session a --session b` deleted b, reported one session and
			// left a behind, which the reader believes is gone (#2271).
			// stats already refuses this shape for its own flags.
			if given[args[i]] {
				return forgetArgs{}, fmt.Errorf("forget: %s specified twice", args[i])
			}
			given[args[i]] = true
			if i+1 >= len(args) {
				return forgetArgs{}, fmt.Errorf("forget: %s needs a value", args[i])
			}
			i++
			// Empty is how "no filter" is spelled inside deja, and here it
			// would widen a deletion rather than a search (#1612). --unforget
			// keeps its own refusal, which asks for an id rather than offering
			// the flags that forget instead of restoring.
			if strings.TrimSpace(args[i]) == "" && args[i-1] != "--unforget" {
				return forgetArgs{}, fmt.Errorf("forget: %s needs a value", args[i-1])
			}
			switch args[i-1] {
			case "--session":
				o.Session = index.PastedSelector(args[i])
			case "--project":
				o.Project = args[i]
			case "--unforget":
				unforget, unforgetGiven = index.PastedSelector(args[i]), true
			case "--before":
				if d, err := parseDurAny(args[i]); err == nil {
					// "older than 0 days" is the whole store, and the typo that
					// produces it is 0 for 30. The neighbouring value behaves
					// the opposite way — --before 9999d matches nothing — and a
					// negative duration filters nothing in `last` while
					// deleting everything here (#739).
					if d <= 0 {
						return forgetArgs{}, fmt.Errorf("forget: --before %s means older than now, which is every session — use a real age like 30d, or --project/--session", args[i])
					}
					o.Before = time.Now().Add(-d)
				} else if t, e := parseForgetDate(args[i]); e == nil {
					o.Before = t
				} else {
					// --before takes either form, so naming only one of them
					// leaves the reader guessing which they got wrong.
					return forgetArgs{}, fmt.Errorf("forget: %q is neither a duration nor a date — try 30d, 12h, or 2026-01-31", args[i])
				}
			}
		default:
			// An id is not a flag, and every other command that takes one
			// takes it bare — `show`, `handoff` and `ctx` all do. forget asks
			// for --session because it is the destructive one, and saying so
			// is the whole difference between a dead end and one more word to
			// type (#2656, the lesson #2191 recorded for --harness).
			if !strings.HasPrefix(args[i], "-") {
				return forgetArgs{}, fmt.Errorf("forget: a session id goes after --session — `deja forget --session %s`", pasteSafe(args[i]))
			}
			return forgetArgs{}, unknownFlag("forget", args[i], forgetFlags)
		}
	}
	return forgetArgs{o: o, list: list, allMatches: allMatches, unforget: unforget, unforgetGiven: unforgetGiven}, nil
}

// printForgetList prints the tombstones this machine holds.
func printForgetList(dir string) error {
	keys := index.Tombstones()
	// A tombstone whose session is in neither the index nor any store
	// suppresses nothing: it is left over from a session deleted on disk
	// since, and nothing told it apart from one somebody forgot yesterday
	// (#3753). Marked after a tab, so each row still starts with its id and
	// the list is still one line per tombstone.
	orphaned := orphanedTombstones(dir, keys)
	for _, key := range keys {
		if orphaned[key] {
			fmt.Fprintln(os.Stdout, key+"\t(no session left — the tombstone suppresses nothing)")
			continue
		}
		fmt.Fprintln(os.Stdout, key)
	}
	// The list is where someone who dropped more than they meant to lands,
	// and it named no way back: `--unforget` lived in `deja help` only, and
	// the hint for a guessed `deja unforget` pointed at this same list
	// (#919).
	if len(keys) == 0 {
		// Silence here is the one answer a reader cannot act on: it looks
		// the same as a command that did not run, and every other empty
		// result says so out loud (#1040). On stderr, so a pipe still
		// counts only ids.
		fmt.Fprintln(os.Stderr, "deja: nothing is forgotten on this machine")
	}
	if len(keys) > 0 {
		quoted := pasteSafe(keys[0])
		fmt.Fprintf(os.Stderr, "deja: `deja forget --unforget %s` brings one back and rebuilds the index%s\n", quoted, pasteSafeCaveat(quoted))
	}
	return nil
}

// runUnforget lifts the tombstones a selector names and rebuilds the index.
func runUnforget(dir, unforget string, allMatches, dryRun bool) error {
	// The destructive direction refuses a prefix that reaches more than one
	// session; the undo restored them all and said so afterwards, while the
	// hint printed beside the list promises one (#961).
	if n := index.TombstoneMatches(unforget); n > 1 && !allMatches {
		// "`deja forget --list` names them" sends the reader to a list of
		// everything this machine ever forgot, where the three that match
		// are theirs to find. Name them here instead (#1014).
		return fmt.Errorf("%q brings back %d forgotten sessions (%s) — add --all-matches to restore them all, or name one",
			unforget, n, joinCapped(index.TombstonesMatching(unforget), 5))
	}
	var lifted int
	// The ids to name afterwards are the tombstones this call lifts, read
	// before it lifts them — after the rebuild the manifest holds every
	// session the selector matches, restored or never forgotten (#1095).
	lifting := index.TombstonesMatching(unforget)
	if dryRun {
		// --dry-run on the undo brought the session back anyway: the
		// destructive side runs a dry probe and never touches disk, but
		// this path went straight to Unforget, which lifts the tombstone
		// and rebuilds. Name what it would restore and stop (#1066).
		if len(lifting) == 0 {
			return fmt.Errorf("no tombstone matches %q — `deja forget --list` shows what is forgotten", unforget)
		}
		fmt.Fprintf(os.Stdout, "dry run — nothing was changed\nwould restore %d tombstone%s and rebuild the index — %s\n",
			len(lifting), pluralS(len(lifting)), joinCapped(lifting, 5))
		return nil
	}
	if err := withBuildProgress(func() error {
		var err error
		lifted, err = index.Unforget(dir, unforget, os.Stderr)
		return err
	}); err != nil {
		return err
	}
	if lifted == 0 {
		// A restore that restored nothing used to exit 0, so a script
		// could not tell it from a restore that worked: the typo, the id
		// that was never forgotten and the session still gone all read as
		// success (#2263). Every neighbour refuses what it cannot find.
		return fmt.Errorf("no tombstone matches %q — `deja forget --list` shows what is forgotten", unforget)
	}
	// Lifting a tombstone brings a session back only if the transcript is
	// still on this machine. An imported one lives only in the index, so
	// forget took the last copy — and the undo reported a restore that did
	// not happen (#967).
	back, gone, names, missing := restoredSessions(dir, unforget, lifted, lifting)
	restorePromotedTitles(dir, unforget)
	if back > 0 {
		// The ids, not just the count: this is the moment someone checks
		// that they got back exactly what they lost, and both the list and
		// the ambiguity refusal name them a step earlier (#1095, #1014).
		// Prose, not a command: the names are read, not pasted, so they
		// are neutralised rather than quoted. Raw, an escape byte in a
		// project name landed here on the line after the hint that told
		// the reader to run this (#1794).
		fmt.Fprintf(os.Stdout, "restored %d session%s and rebuilt the index — %s\n", back, pluralS(back), safeForStatusline(joinCapped(names, 5), 200))
	}
	for _, line := range unforgetGoneLines(missing) {
		fmt.Fprintln(os.Stdout, line)
	}
	if gone > 0 && len(missing) == 0 {
		// The selector was a prefix rather than whole keys, so which ones
		// did not come back is not known by name here.
		fmt.Fprintf(os.Stdout, "%d of them did not come back — the tombstone is lifted, but the records are not in the index; `deja forget --list` shows what is left\n", gone)
	}
	return nil
}

// printForgetDryRun says what a forget would drop, without dropping it.
func printForgetDryRun(dir string, o index.ForgetOptions, result index.ForgetResult, allMatches bool, shared int) error {
	// The dry run is where someone checks the scope, so it says the same
	// thing the real run would refuse with rather than erroring: nothing
	// is being changed here, and the note is the answer they came for.
	// It goes above the counts, and the counts say whose run they are:
	// under an ambiguous selector the numbers were those of
	// `--all-matches`, while the command as typed drops nothing (#1032).
	// The refusal itself points here, so it is not repeated: this run is
	// the listing it promises.
	if o.Session != "" && forgetScopeRefusal(o.Session, result.Sessions-result.Attached, allMatches) != nil {
		fmt.Fprintf(os.Stdout, "%q matches %d sessions — add --all-matches to drop them all\n", o.Session, result.Sessions-result.Attached)
		fmt.Fprintf(os.Stdout, "dry run — nothing was changed\nas it stands this run drops nothing; with --all-matches it would drop: %s\nwould add: %s\n",
			forgetCounts(result.Sessions, result.Messages), countNoun(result.Tombstones, "tombstone"))
	} else {
		fmt.Fprintf(os.Stdout, "dry run — nothing was changed\nwould drop: %s\nwould add: %s\n",
			forgetCounts(result.Sessions, result.Messages), countNoun(result.Tombstones, "tombstone"))
	}
	if result.Unindexed && result.Sessions > 0 {
		fmt.Fprintln(os.Stdout, "nothing is indexed yet — the tombstones would keep these sessions out of the first `deja index`")
	} else if result.Unindexed {
		fmt.Fprintln(os.Stdout, "nothing is indexed yet, and no session store on this machine holds a match")
	}
	// The ids, so the scope can be checked by name and not by a count
	// alone: the refusal sends the reader here to see what would go.
	if len(result.Keys) > 0 {
		keys := append([]string(nil), result.Keys...)
		sort.Strings(keys)
		fmt.Fprintln(os.Stdout, "sessions: "+safeForStatusline(joinCapped(keys, 10), 400))
	}
	if line := forgetNotesLine(result); line != "" {
		fmt.Fprintln(os.Stdout, line)
	}
	if line := forgetCopiesLine(result.Copies); line != "" {
		fmt.Fprintln(os.Stdout, line)
	}
	if n := usage.CountSnapshots(dir, forgetDigestMatcher(o, result.Keys)); n > 0 {
		fmt.Fprintf(os.Stdout, "would remove: %d stored digest(s) from the injection log\n", n)
	}
	// Two transcripts can write the same harness:id, and then one manifest
	// row holds both conversations. The build says so once (#698); forget
	// said "1 session" and took two, from two projects (#970).
	if o.DryRun {
		shared = sharedRowsAmong(dir, result.Keys)
	}
	if n := shared; n > 0 {
		fmt.Fprintf(os.Stdout, "%s covered more than one conversation — transcripts that share an id are filed under one row, and both go\n",
			doctorCount(n, "of them"))
	}
	return nil
}

// forgetPromotedLeftovers clears what the dropped sessions left behind in the
// notes file and the injection log.
func forgetPromotedLeftovers(dir string, o index.ForgetOptions, result index.ForgetResult) {
	dropped := make(map[string]bool, len(result.Keys))
	for _, k := range result.Keys {
		dropped[k] = true
	}
	// Only when the reader named a session: a note swept up by --project or
	// --before is a decision they deliberately kept, and destroying it is
	// what #690 exists to prevent. Naming the note's own id is the one
	// case where "forget this" can only mean its text (#841).
	if o.Session != "" {
		if gone, err := sources.ForgetPromotedNotes(func(noteID string) bool {
			return dropped["deja:"+noteID]
		}); err != nil {
			fmt.Fprintf(os.Stdout, "could not remove %s from %s: %v\n", pluralNote(result.Notes), sources.NotesFile(), err)
		} else if gone > 0 {
			fmt.Fprintf(os.Stdout, "removed %d promoted note%s from %s\n", gone, pluralS(gone), sources.NotesFile())
		}
	}
	// The digests those sessions were served in are content too, and the
	// page publishes them: forget already reaches the note log for the
	// same reason (#690, #841). The usage log stays — an event is what
	// happened, not what was said (#2325).
	if gone, err := usage.ForgetSnapshots(dir, forgetDigestMatcher(o, result.Keys)); err != nil {
		fmt.Fprintf(os.Stdout, "could not remove stored digests from the injection log: %v\n", err)
	} else if gone > 0 {
		fmt.Fprintf(os.Stdout, "removed %d stored digest%s from the injection log\n", gone, pluralS(gone))
	}
	n, err := sources.ForgetPromotedTitles(func(src string) bool {
		return dropped[src]
	})
	switch {
	case err != nil:
		// The borrowed title is the forgotten session's first turn — a
		// customer name in the case that found this (#666, #690). Dropping
		// the error left three success lines standing over a line still on
		// disk, which is the one thing the privacy command must not do
		// (#804).
		fmt.Fprintf(os.Stdout, "could not clear the title borrowed from the forgotten session%s: %v\n", pluralS(len(result.Keys)), err)
		// Name the fix by the cause: on a full disk the first version of
		// this line sent people to chmod a file that was already writable
		// (#808).
		fmt.Fprintf(os.Stdout, "it is still in %s — %s and run this again\n", sources.NotesFile(), forgetTitleFix(err))
	case n > 0:
		// The note keeps the decision it was promoted for — often the
		// reason the raw session was safe to forget (#666) — so say that
		// its content is still there rather than let the line read as
		// "the note was handled" (#841).
		// The ids, not an ellipsis: this line knows which notes it just
		// cleared, and `deja forget --session deja-note-…` matched
		// nothing — it sent the reader to `deja last` to look up
		// something the command was already holding (#1030).
		what := "it"
		if n > 1 {
			what = "them"
		}
		fmt.Fprintf(os.Stdout, "cleared the borrowed title from %d promoted note%s; %s still holds what you wrote there — `deja forget --session %s` removes %s\n",
			n, pluralS(n), sources.NotesFile(), clearedNoteIDs(result.Keys), what)
	}
}

// forgetNothingMatched answers a forget whose selector found no session.
func forgetNothingMatched(dir string, o index.ForgetOptions, result index.ForgetResult) error {
	// A note whose own project was forgotten keeps its line in
	// notes.jsonl while its index row is gone, and the id the forget
	// line offers then matched nothing at all — the file, not the
	// index, is what holds a note (#1030).
	if o.Session != "" {
		if gone, err := sources.ForgetPromotedNotes(func(noteID string) bool {
			return noteID == strings.TrimPrefix(o.Session, "deja:")
		}); err == nil && gone > 0 {
			fmt.Fprintf(os.Stdout, "removed %d promoted note%s from %s\n", gone, pluralS(gone), sources.NotesFile())
			return nil
		}
	}
	// A name that matched nothing is a failure, the way `--unforget` has
	// treated one since #2263: `deja forget --session $ID && echo removed`
	// printed "removed" for a session still on disk under another id, which
	// is the shape #3567 closed for a mistyped command. A window is not a
	// name — `--before 30d` on a young store covering nothing is the right
	// outcome, and a cleanup loop under `set -e` must not abort on it.
	if result.Unindexed {
		// No index to drop from, and no store holds a match: there is no
		// harness:id to write a tombstone under.
		if o.Session != "" || o.Project != "" {
			return fmt.Errorf("nothing matched %s — nothing is indexed yet and no session store on this machine holds one, so no tombstone was written", forgetSelector(o))
		}
		fmt.Fprintf(os.Stdout, "nothing matched %s — nothing is indexed yet and no session store on this machine holds one\n", forgetSelector(o))
		return nil
	}
	if o.Session != "" || o.Project != "" {
		return fmt.Errorf("nothing matched %s — no session was dropped%s",
			forgetSelector(o), movedBucketHint(dir, o.Session))
	}
	fmt.Fprintf(os.Stdout, "nothing matched %s — no session was dropped%s\n", forgetSelector(o), movedBucketHint(dir, o.Session))
	return nil
}

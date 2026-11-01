package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// orphanedTombstones reports which tombstones name a session that exists
// nowhere: not in the index and not in any store on disk.
//
// Only a positive answer is ever given. A tombstone whose harness this build
// does not know, or whose store cannot be loaded, is left unmarked — calling a
// tombstone useless on a guess would invite someone to lift one that is still
// hiding a transcript.
func orphanedTombstones(dir string, keys []string) map[string]bool {
	if len(keys) == 0 {
		return nil
	}
	indexed := index.IndexedSessionKeys(dir)
	// Load each harness a tombstone names once, however many tombstones name it.
	onDisk := map[string]map[string]bool{}
	live := func(harness string) (map[string]bool, bool) {
		if seen, ok := onDisk[harness]; ok {
			return seen, seen != nil
		}
		var found map[string]bool
		for _, h := range sources.StoresToRead() {
			if h.Name != harness || h.Load == nil {
				continue
			}
			found = map[string]bool{}
			for _, s := range h.Load() {
				found[s.Harness+":"+s.ID] = true
			}
		}
		onDisk[harness] = found
		return found, found != nil
	}
	out := map[string]bool{}
	for _, key := range keys {
		harness, _, ok := strings.Cut(key, ":")
		if !ok || indexed[key] {
			continue
		}
		stored, known := live(harness)
		if !known || stored[key] {
			continue
		}
		out[key] = true
	}
	return out
}

// forgetScopeRefusal stops a destructive run whose selector reaches further
// than the reader can have meant.
//
// `--session` takes a prefix, which is documented and useful for a day's ids —
// but it also let one exact-looking id drop twelve sessions, and the count
// arrived in the past tense afterwards (#870). An id copied off a result line
// is worse still: the characters that tell the sessions apart are the ones the
// line elided, so no longer prefix exists to reach for (#859).
func forgetScopeRefusal(selector string, matches int, allMatches bool) error {
	if matches <= 1 || allMatches {
		return nil
	}
	if strings.Contains(selector, "…") {
		return fmt.Errorf("%q matches %d sessions — the ids differ in the middle the line elides; `deja last` prints them whole", selector, matches)
	}
	return fmt.Errorf("%q matches %d sessions — `deja forget --session %s --dry-run` lists what would go; add --all-matches to drop them all",
		selector, matches, pasteSafe(selector))
}

func runForget(dir string, args []string) error {
	fa, perr := parseForgetArgs(args)
	if perr != nil {
		return perr
	}
	o, list, allMatches, unforget, unforgetGiven := fa.o, fa.list, fa.allMatches, fa.unforget, fa.unforgetGiven
	// An empty `--unforget` was answered with the selectors for forgetting:
	// the reader asked to bring something back and was told how to drop more
	// (#1041).
	if unforgetGiven && unforget == "" {
		return errors.New("forget: --unforget needs an id — `deja forget --list` names the ids")
	}
	if !list && unforget == "" && o.Session == "" && o.Project == "" && o.Before.IsZero() {
		// Naming the selectors here is what separates one call from hundreds:
		// forgetting 100 sessions one id at a time took 10.5s against 0.2s for
		// `--project`, and nothing on this path said the second form exists
		// (#1022).
		return errors.New("forget: selector required — `--session <id>`, `--project <name>` or `--before <date>`; `--dry-run` shows what would go, `--list` what already went")
	}
	if list {
		return printForgetList(dir)
	}
	if unforget != "" {
		return runUnforget(dir, unforget, allMatches, o.DryRun)
	}
	// The scope check runs on a dry pass first: a refusal printed after
	// index.Forget has already written the tombstones is not a refusal.
	//
	// The same pass answers what is about to go: after the real Forget the rows
	// are out of the manifest, so a row that covered two conversations can no
	// longer be recognised (#970).
	// With no index, Forget matches in the stores and writes only the
	// tombstones. An index on a disk that went away also reads as no index,
	// and saying "nothing indexed yet" there would leave the session in the
	// index that comes back with the disk.
	if parent, gone := indexLooksUnmounted(dir); gone && !index.HasManifest(dir) {
		return fmt.Errorf("the index directory is not there (%s) — the disk it lives on may have been unmounted; reconnect it, or point DEJA_INDEX_DIR somewhere local", parent)
	}
	shared := 0
	if !o.DryRun {
		probe := o
		probe.DryRun = true
		pr, perr := index.Forget(dir, probe)
		if perr != nil {
			// The probe fails the same ways the real pass does — a vanished
			// volume, a read-only cache — and handing its syscall back was the
			// shape #798 replaced everywhere else.
			return ensureError(dir, perr)
		}
		if o.Session != "" {
			if err := forgetScopeRefusal(o.Session, pr.Sessions-pr.Attached, allMatches); err != nil {
				return err
			}
		}
		shared = sharedRowsAmong(dir, pr.Keys)
	}
	result, err := index.Forget(dir, o)
	if !o.DryRun {
		// The digests cached beside the index quote sessions as prose, and a
		// forget left them there: recall never served them past their minute,
		// and the text was still on the disk (#3411).
		dropHookCaches(dir)
	}
	if err != nil {
		// The tombstone is already written; what failed is the rebuild that
		// takes the records out. Handing back `mkdir /…/idx.tmp: permission
		// denied` names an internal path and leaves the reader believing the
		// session is gone while search still returns it (#976).
		if !o.DryRun && index.Tombstoned(forgetKeyOf(dir, o)) {
			return fmt.Errorf("%w\nthe session is marked forgotten and is still in the index until it can be rebuilt — search keeps returning it until then", ensureError(dir, err))
		}
		return ensureError(dir, err)
	}
	if o.DryRun {
		return printForgetDryRun(dir, o, result, allMatches, shared)
	}
	// A promoted note borrows the source session's first line as its title, and
	// the note is a separate record — so forgetting a session left that line on
	// screen in `deja last` (#666). The note stays; only the borrowed title
	// goes, and the parser falls back to "promoted from <src>".
	//
	// Matching on the keys that were actually dropped rather than on the
	// selector: --project and --before drop sessions too, and keying off
	// o.Session left the borrowed line — a customer name, in the case that
	// found this — sitting in notes.jsonl after the project was forgotten
	// (#690).
	if len(result.Keys) > 0 {
		forgetPromotedLeftovers(dir, o, result)
	}
	// Nothing matched is a different answer from nothing was dropped: the
	// first means the selector found no session, and reporting it as a
	// successful removal of zero leaves the reader believing they deleted
	// something that is in fact still there under a different id.
	if result.Sessions == 0 && result.Messages == 0 {
		return forgetNothingMatched(dir, o, result)
	}
	if result.Unindexed {
		// Nothing was in an index to drop; "sessions dropped: 1" would claim a
		// removal that never happened.
		fmt.Fprintf(os.Stdout, "nothing is indexed yet, so nothing was dropped\ntombstones added: %d — the first `deja index` leaves %s out\n",
			result.Tombstones, safeForStatusline(joinCapped(result.Keys, 5), 400))
	} else {
		fmt.Fprintf(os.Stdout, "sessions dropped: %d\nmessages dropped: %d\ntombstones added: %d\n", result.Sessions, result.Messages, result.Tombstones)
	}
	// "1 session" can be two conversations: transcripts that share an id are
	// filed under one row, and the message count is the only tell (#970).
	if shared > 0 {
		fmt.Fprintf(os.Stdout, "%s covered more than one conversation — transcripts that share an id are filed under one row, and both went\n",
			doctorCount(shared, "of them"))
	}
	// Forgetting keeps the session out of later pushes but cannot reach a
	// machine that already has it. Three lines about what was dropped read as
	// "it is gone everywhere" to someone forgetting a customer name (#788).
	if len(result.Peers) > 0 {
		fmt.Fprintf(os.Stdout, "already pushed to %s — forgetting here does not remove it there\n", strings.Join(result.Peers, ", "))
	} else if result.Exported {
		fmt.Fprintln(os.Stdout, "already exported once — forgetting here does not remove copies elsewhere")
	}
	if line := forgetCopiesLine(result.Copies); line != "" {
		fmt.Fprintln(os.Stdout, line)
	}
	// The notes are decisions the reader deliberately kept, so folding them
	// into the session count reads as "four conversations" when half of it is
	// their own writing (#690).
	// Through forgetNotesLine, like the dry run above it. #957 taught that
	// sentence to tell a `remember` note from a promoted one and only wired it
	// into `--dry-run`, so the run that actually dropped a day of notes still
	// called them promoted — the same command, two answers, and the wrong one
	// on the path that changes something (#3599).
	if line := forgetNotesLine(result); line != "" {
		fmt.Fprintln(os.Stdout, line)
	}
	return nil
}

// forgetCopiesLine names the sessions that still hold a copy of what was
// forgotten: a fork opens with its source's turns under its own id. Named, not
// taken: forgetting a fork whole drops the work it did after the copy.
func forgetCopiesLine(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	verb, cmd := "hold", "`deja forget --session <id>` drops each, with its own work"
	if len(keys) == 1 {
		id := keys[0]
		if _, rest, ok := strings.Cut(id, ":"); ok {
			id = rest
		}
		verb, cmd = "holds", "`deja forget --session "+id+"` drops it too, with its own work"
	}
	return fmt.Sprintf("%s still %s a copy of these turns (a fork or its source): %s — %s",
		countNoun(len(keys), "other session"), verb, safeForStatusline(joinCapped(keys, 5), 400), cmd)
}

// verbShare keeps "1 session share" off the screen.
func verbShare(n int) string {
	if n == 1 {
		return "shares"
	}
	return "share"
}

// verbWere keeps "1 of them are promoted notes" off the screen.
func verbWere(n int) string {
	if n == 1 {
		return "is a"
	}
	return "are"
}

// forgetSelector names what the caller asked to forget, so the empty answer
// says which selector came back empty.
func forgetSelector(o index.ForgetOptions) string {
	var parts []string
	if o.Session != "" {
		parts = append(parts, fmt.Sprintf("session %q", o.Session))
	}
	if o.Project != "" {
		parts = append(parts, fmt.Sprintf("project %q", o.Project))
	}
	if !o.Before.IsZero() {
		parts = append(parts, "before "+o.Before.Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return "the given selector"
	}
	return strings.Join(parts, " and ")
}

func parseForgetDate(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("invalid date")
}

// forgetNotesLine says how much of what is going is the user's own writing.
// Everything deja files under its own harness counted as a "promoted note",
// so forgetting a day of `remember` notes named something the reader had never
// done (#957).
func forgetNotesLine(result index.ForgetResult) string {
	switch {
	case result.Notes == 0:
		return ""
	case result.Promoted == result.Notes:
		return fmt.Sprintf("%d of them %s promoted note%s — the decisions you kept, not raw sessions",
			result.Notes, verbWere(result.Notes), pluralS(result.Notes))
	case result.Promoted == 0:
		if result.Notes == 1 {
			return "1 of them is a day of your own notes, not raw sessions"
		}
		return fmt.Sprintf("%d of them are %s of your own notes, not raw sessions",
			result.Notes, dayWord(result.Notes))
	default:
		return fmt.Sprintf("%d of them %s your own writing — %d promoted, %s of notes — not raw sessions",
			result.Notes, verbWere(result.Notes), result.Promoted, dayWord(result.Notes-result.Promoted))
	}
}

// dayWord names day buckets the way the reader wrote them: by the day.
func dayWord(n int) string {
	if n == 1 {
		return "a day"
	}
	return fmt.Sprintf("%d days", n)
}

// pluralIsLifted and pluralTranscript keep the two sentences readable for one
// key and for several; joinCapped names up to five.
func pluralIsLifted(n int) string {
	if n == 1 {
		return " is"
	}
	return "s are"
}

func pluralTranscript(n int) string {
	if n == 1 {
		return " it"
	}
	return "s they"
}

// unforgetGoneLines says why a lifted tombstone restored nothing. Two causes
// with two answers: a session deja only ever held in its own index came from
// another machine and sync import brings it back (#967), while a transcript
// this machine wrote and then deleted is simply not on disk — telling that
// reader to import from another machine names one they never had (#1755).
func unforgetGoneLines(missing []string) []string {
	var imported, local []string
	for _, key := range missing {
		id := key
		if _, after, ok := strings.Cut(key, ":"); ok {
			id = after
		}
		if strings.HasPrefix(id, "imported-") {
			imported = append(imported, key)
			continue
		}
		local = append(local, key)
	}
	var out []string
	if len(imported) > 0 {
		out = append(out, fmt.Sprintf("%s came from another machine and deja held the only copy — the tombstone%s lifted, but the records are gone; `deja sync import` brings them back", joinCapped(imported, 5), pluralIsLifted(len(imported))))
	}
	if len(local) > 0 {
		out = append(out, fmt.Sprintf("%s %s no longer on this machine — the tombstone%s lifted, but the transcript%s named %s gone, so there is nothing to index",
			joinCapped(local, 5), verbIs(len(local)), pluralIsLifted(len(local)), pluralTranscript(len(local)), verbIs(len(local))))
	}
	return out
}

// restoredSessions splits what a lifted tombstone actually returned from what
// it could not: a session whose transcript is on this machine is re-read by the
// rebuild, while an imported one existed only in the index that forget rewrote.
func restoredSessions(dir, selector string, lifted int, lifting []string) (back, gone int, names, missing []string) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return lifted, 0, lifting, nil
	}
	// A session counts as back only if its row is in the manifest again: an
	// imported one lives only in the index, so forget took the last copy and
	// lifting its tombstone restores nothing (#967).
	here := map[string]bool{}
	for _, m := range metas {
		here[m.Harness+":"+m.ID] = true
	}
	for _, key := range lifting {
		if here[key] {
			back++
			names = append(names, key)
			continue
		}
		missing = append(missing, key)
	}
	if back == 0 && lifted > 0 {
		// Selectors that are not whole keys (a bare id, a prefix) still count
		// through the manifest, as before.
		for _, m := range metas {
			if index.SelectorMatches(m, selector) {
				back++
				names = append(names, m.Harness+":"+m.ID)
			}
		}
		if back > lifted {
			back, names = lifted, names[:lifted]
		}
		if back > 0 {
			// The prefix path found them by manifest row, so nothing here is
			// missing by name.
			missing = nil
		}
	}
	sort.Strings(names)
	sort.Strings(missing)
	return back, lifted - back, names, missing
}

// restorePromotedTitles gives a promoted note back the title it borrowed from a
// session that has just been restored. forget clears it so the text of a
// forgotten session does not live on in the note (#666); unforget is the moment
// that reason stops applying (#969).
func restorePromotedTitles(dir, selector string) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return
	}
	titles := map[string]string{}
	for _, m := range metas {
		if !index.SelectorMatches(m, selector) {
			continue
		}
		s, ok, err := index.FindByPrefix(dir, m.ID)
		if err != nil || !ok {
			continue
		}
		if t := firstUserTitle(s); t != "" {
			titles[m.Harness+":"+m.ID] = t
		}
	}
	if len(titles) == 0 {
		return
	}
	_, _ = sources.RestorePromotedTitles(func(src string) string { return titles[src] })
}

// sharedRowsAmong counts the manifest rows in keys that hold more than one
// conversation, so forget can say what it is about to take (#970).
func sharedRowsAmong(dir string, keys []string) int {
	if len(keys) == 0 {
		return 0
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return 0
	}
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	n := 0
	for _, m := range metas {
		if m.Shared && want[m.Harness+":"+m.ID] {
			n++
		}
	}
	return n
}

// noteForgottenSource says when an id lookup landed on the note promoted from a
// session that has been forgotten. Asking for the session by the id you
// remember answered with the note and said nothing, so the reply looked like
// the session itself until you noticed the id had changed (#971).
func noteForgottenSource(s model.Session, selector string, resolved bool) {
	if line := forgottenSourceNote(s, selector, resolved); line != "" {
		fmt.Fprintf(os.Stderr, "deja: %s\n", line)
	}
}

// forgottenSourceNote is the sentence itself, so every surface can carry it.
// resolved says the selector reached this session through a prefix lookup: on
// that path any prefix is honest by construction, and a length rule would only
// guess at what the resolver already answered.
// It lived inside the printer, which meant the CLI said a session was
// forgotten and the MCP tools handed an agent the same note with the fact
// removed — and "forgotten" is a decision the reader made about that session,
// which is the kind of fact recall exists to carry (#1624).
func forgottenSourceNote(s model.Session, selector string, resolved bool) string {
	// Through promotedNoteID, so a note that arrived by sync is one: its local
	// id is `imported-…` and the id it names lives in OrigID (#2839).
	noteID := promotedNoteID(s)
	if noteID == "" {
		return ""
	}
	src, ok := strings.CutPrefix(noteID, "deja-note-")
	if !ok || src == "" {
		return ""
	}
	// The selector named the source, not the note: a reader who typed the note
	// id knows what they asked for — either id of it.
	if strings.HasPrefix(s.ID, selector) || strings.HasPrefix(noteID, selector) {
		return ""
	}
	key := strings.Replace(src, "-", ":", 1)
	// Only when the reader named that session: an ordinary topical query that
	// happens to land on the note is not asking about the forgotten source, and
	// the line would be noise on every such search.
	//
	// Named means named. Testing the selector as a substring of the key fired
	// on "claude" — the harness name is in every key — and on any single
	// character, while the reverse case never fired at all: over MCP the
	// selector is usually a sentence, and "what did we decide in s15" is a
	// reader naming the session as plainly as `s15` is (#1624).
	if !selectorNamesSession(selector, key, resolved) {
		return ""
	}
	// The key the note names is the source's key on the machine that made it,
	// and a source that arrived by sync is keyed here by the id the import
	// gave it — which is a pure function of that key, so it can be rebuilt
	// rather than looked up. Asking about the original alone said nothing
	// about a session the reader had forgotten here (#2839).
	harness, id, _ := strings.Cut(key, ":")
	if imported := harness + ":" + index.ImportedSessionID(harness, id); index.Tombstoned(imported) {
		// Named as the reader will find it in `deja forget --list`.
		key = imported
	} else if !index.Tombstoned(key) {
		return ""
	}
	// Prose, not a command: the key is read here rather than pasted — the
	// command in this sentence takes no argument — so it is neutralised the
	// way every other echo is.
	return safeForStatusline(key, 200) + " is forgotten — this is the note promoted from it; `deja forget --list` names what is gone"
}

// selectorNamesSession reports whether the reader's selector names this
// session: the whole key, an id prefix long enough to mean it, or one of the
// words of a sentence being either of those.
func selectorNamesSession(selector, key string, resolved bool) bool {
	if strings.TrimSpace(selector) == "" {
		return false
	}
	_, id, _ := strings.Cut(key, ":")
	for word := range strings.FieldsSeq(selector) {
		if word == key || word == id {
			return true
		}
		// A prefix counts only where something resolved it: `deja ctx 3f2a9c`
		// reached this session and nothing else. Where nothing resolved — a
		// search query — a prefix is a guess, and the ids real harnesses write
		// make it a bad one: codex ids start with a year, cline ids start with
		// the harness name, so "what did we ship in 2026" and "cline" would
		// each name a session (#1624).
		if resolved && strings.HasPrefix(id, word) {
			return true
		}
	}
	return false
}

// forgetKeyOf names the exact session a selector reached, so a failed forget
// can tell whether the tombstone landed (#976).
func forgetKeyOf(dir string, o index.ForgetOptions) string {
	if o.Session == "" {
		return ""
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ""
	}
	for _, m := range metas {
		if index.SelectorMatches(m, o.Session) {
			return m.Harness + ":" + m.ID
		}
	}
	return ""
}

// reachableSessionCount reports how many indexed sessions this path may read,
// how many of those the trust policy alone allows, and how many there are.
//
// Both rules withhold, so both are counted (#2707): the trust policy was here
// from the start (#986) and the ignore rule was not counted at all, which had
// the empty answer naming twice the history search would open. The trust-only
// figure is kept because the reader has to be told which of the two emptied
// their search.
func reachableSessionCount(dir string) (reach, trusted, total int, ok bool) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return 0, 0, 0, false
	}
	pol := policy.Load()
	for _, m := range metas {
		if !pol.Allows(policy.ActivationSearch, m.Project) {
			continue
		}
		trusted++
		if !pol.Ignored(m.Path, m.Project) {
			reach++
		}
	}
	return reach, trusted, len(metas), true
}

// joinCapped lists at most n items and says how many it left out, so a refusal
// stays one line on a machine with many tombstones.
func joinCapped(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(", +%d more", len(items)-n)
}

// forgetDigestMatcher decides which stored digests belong to what a forget just
// took. A record written since #2324 names its own projects, which is exact; an
// older one is recognised by the ids and project name inside its text, the same
// weaker test the view page falls back to.
func forgetDigestMatcher(o index.ForgetOptions, keys []string) func(usage.Snapshot) bool {
	// Both spellings: a listing renders `claude:abc` and a hook block quotes
	// the bare id, so a sweep that knew only one of them missed half the
	// records it was meant to take.
	ids := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		ids = append(ids, k)
		if _, id, ok := strings.Cut(k, ":"); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return func(s usage.Snapshot) bool {
		if o.Project != "" {
			if slices.Contains(s.Projects, o.Project) {
				return true
			}
			if len(s.Projects) == 0 && digestNames(s.Digest, o.Project) {
				return true
			}
		}
		for _, id := range ids {
			if digestNames(s.Digest, id) {
				return true
			}
		}
		return false
	}
}

// digestNames reports whether a digest names a project or a session where it
// renders one, rather than anywhere in its text. Searching the whole text made
// `forget --project api` delete a digest about work/web whose prose read "a
// late api call": the reader forgot one project and lost another's record
// (#2330). The two shapes deja writes are the recall listing,
//
//  1. [claude] work/app · claude:abc · 2 matches
//
// and the hook block,
//
//   - **work/app** `abc` · 2026-08-28
//
// A digest in neither shape names nothing, so a sweep keeps it: with `projects`
// recorded since #2324 the guess is only needed for older records, and deleting
// one on a guess cannot be undone.
func digestNames(digest, want string) bool {
	if want == "" {
		return false
	}
	for line := range strings.SplitSeq(digest, "\n") {
		if slices.Contains(digestNamedFields(line), want) {
			return true
		}
	}
	return false
}

// digestNamedFields pulls the project and id positions out of one rendered
// line: the fields that follow the harness in brackets, and anything a hook
// block emphasises or quotes.
func digestNamedFields(line string) []string {
	var out []string
	if _, after, ok := strings.Cut(line, "] "); ok {
		for field := range strings.SplitSeq(after, " · ") {
			out = append(out, strings.TrimSpace(field))
		}
	}
	out = append(out, betweenAll(line, "**")...)
	out = append(out, betweenAll(line, "`")...)
	return out
}

// betweenAll returns every substring wrapped by the given marker.
func betweenAll(s, marker string) []string {
	var out []string
	for {
		i := strings.Index(s, marker)
		if i < 0 {
			return out
		}
		rest := s[i+len(marker):]
		before, after, ok := strings.Cut(rest, marker)
		if !ok {
			return out
		}
		out = append(out, strings.TrimSpace(before))
		s = after
	}
}

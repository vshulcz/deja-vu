package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// runPromote distills a session into a curated note: quoted evidence with
// provenance and a lifecycle state, stored in the notes source so it indexes
// like everything else but outranks the raw transcript it came from.
func runPromote(dir string, args []string, stdout io.Writer) error {
	prefix := ""
	state := "accepted"
	noteText := ""
	exportPath := ""
	var tags []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--state":
			if i+1 >= len(args) {
				return fmt.Errorf("promote: --state needs a value")
			}
			i++
			state = strings.ToLower(args[i])
		case "--note":
			if i+1 >= len(args) {
				return fmt.Errorf("promote: --note needs text")
			}
			i++
			noteText = args[i]
		case "--to":
			if i+1 >= len(args) {
				return fmt.Errorf("promote: --to needs a path")
			}
			i++
			exportPath = args[i]
		case "--tag":
			if i+1 >= len(args) {
				return fmt.Errorf("promote: --tag needs a value")
			}
			i++
			tags = append(tags, args[i])
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("promote: unknown flag %q", args[i])
			}
			prefix = args[i]
		}
	}
	if prefix == "" {
		return idPrefixNeeded(dir, "promote needs a session id prefix", "promote needs a session id prefix (see `deja last`)")
	}
	if !sources.NoteStates[state] {
		// Taking a mark back is the state nobody guesses: users reach for
		// --state none, --state clear or --unpromote, get this line, and read
		// four states none of which sounds like an undo (#845).
		return fmt.Errorf("promote: state must be accepted, rejected, superseded or stale — `--state accepted` takes an earlier mark back, and `deja forget --session deja-note-<harness>-<id>` removes the note itself")
	}
	s, ok, err := findByPrefix(dir, prefix)
	noteAmbiguousPrefix(dir, prefix, "promoting")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no session matches %q%s", prefix, movedBucketHint(dir, prefix))
	}
	// A session the trust policy hides must not be promotable: `--to` wrote its
	// content to a file and the note it built repeated the wall, both under a
	// deny that share and handoff already honour on the same id. Gate here so
	// the whole command refuses, the way share does.
	if err := denyPolicyHidden(prefix, s, os.Stderr); err != nil {
		return err
	}
	// The exclude list is a privacy control that only runs at ingest, so an
	// index built before the pattern still holds the session — share refuses
	// it for that reason (#1307). promote copied the session's opening line
	// into deja's own store and reported that the note now outranks the
	// transcript in recall, while every reading surface filters that note out
	// again for the same project (#2278).
	// Correcting a note that already exists is exempt: it copies nothing new
	// out of the excluded project, and taking a mark back on work you have
	// since excluded is exactly the cleanup someone would want. The note stays
	// invisible either way, and `deja forget --session deja-note-…` removes it.
	if s.Harness != "deja" && sources.ExcludedProject(s.Project) {
		return fmt.Errorf("%s is in a project your exclude list covers — a note promoted from it is filtered out of recall too; `deja index --rebuild` drops the session, or remove the pattern to promote it", prefix)
	}
	src := s.Harness + ":" + s.ID
	if s.Harness == "deja" {
		// A correction on a promoted note is what `promote` tells you to write,
		// and the id in that hint stops resolving to the transcript once the
		// source is forgotten: both routes then refused, and "promote the
		// source session instead" named something that is gone for good — the
		// note froze at whatever state it held (#979).
		origin, noteTitle, noteBody, isPromoted := promotedNoteSource(s.ID)
		if !isPromoted {
			return fmt.Errorf("%q is a note you wrote, not a session — `deja remember` adds to it", prefix)
		}
		src = origin
		// The session's title here is the note's own display line, and once
		// forget clears the borrowed one that line is "promoted from <src>" —
		// which would be echoed back as the title of the correction.
		s.Title = noteTitle
		// And its body is the note as it was written. Distilling the note's
		// own rows instead re-rendered "[accepted] asked: … · outcome: …" and
		// wrote that back as the correction's text, one wrapper deeper each
		// time — visible once forget had cleared the borrowed title (#1033).
		if noteBody != "" && strings.TrimSpace(noteText) == "" {
			noteText = noteBody
		}
	}
	text := strings.TrimSpace(noteText)
	if text == "" {
		text = distillSession(s)
	}
	prior := sources.PromotedLifecycles()[src]
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = firstLine(text)
	}
	if err := sources.AppendPromotedSourced(s.Project, title, text, src, state, tags, s.Updated, time.Now()); err != nil {
		return notesWriteError(err)
	}
	// The note has to reach the index before the line below claims it outranks
	// the transcript. `remember` has done this since it was written; promote
	// did not, so a decision recorded here was invisible to the hook — which
	// never builds — until some other command happened to run (#910).
	if err := index.EnsureForSearch(dir, search.Options{All: true}, false, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "deja: the note is written; the index could not be updated yet — %v\n", err)
	}
	// Promoting a session that was forgotten writes the note back to disk, but
	// a tombstone lifts only through unforget by design — so the note stayed
	// suppressed and `promote` reported success over a note that never showed.
	// Say so and name the one command that restores it, rather than silently
	// clearing a tombstone the reader set on purpose.
	if noteID := "deja-note-" + strings.ReplaceAll(src, ":", "-"); index.Tombstoned("deja:" + noteID) {
		fmt.Fprint(os.Stderr, tombstoneHint("the note is written", noteID))
	}
	if exportPath != "" {
		masked, err := exportPromoted(exportPath, title, text, src, state, s.Updated)
		if err != nil {
			// The note is already written by this point, so failing with a bare
			// syscall told the reader their decision was lost when it was not
			// — and named a path with nothing to do about it (#871).
			fmt.Fprintf(stdout, "promoted %s as %s: %s\n", safeForStatusline(src, 200), state, search.SafeNote(title))
			return fmt.Errorf("the note is kept, but %s could not be written (%s) — export it somewhere you can, or read the note back with `deja show %s`",
				exportPath, exportFailureReason(err), pasteSafe("deja-note-"+strings.ReplaceAll(src, ":", "-")))
		}
		// The one outbound path that said nothing: `--to` exists to hand a
		// decision to someone, and `share` and `sync export` both end with this
		// floor. The note is the user's own writing, so the text is not
		// rewritten — the warning is what was missing (#848).
		fmt.Fprintf(os.Stderr, "deja: %d secret%s masked in this file. pattern redaction is a floor — review before sending; rotate anything that leaked.\n", masked, pluralS(masked))
	}
	fmt.Fprintf(stdout, "promoted %s as %s: %s\n", safeForStatusline(src, 200), state, search.SafeNote(title))
	if line := markTakenBack(src, state, prior); line != "" {
		fmt.Fprintln(stdout, line)
	}
	// Taking a decision back is the same moment forget is: the machines that
	// already have this session keep the note as it was, and deja knows which
	// ones (#898). Only for the states that withdraw something — an ordinary
	// `accepted` is not a retraction.
	if state != "accepted" {
		if peers, exported := index.PushedTo(dir, s.Harness, s.ID); len(peers) > 0 || exported {
			where := "another machine"
			if len(peers) > 0 {
				where = strings.Join(peers, ", ")
			}
			fmt.Fprintf(stdout, "already sent to %s — this mark stays here; a copy over there still reads as it did\n", where)
		}
	}
	if state == "accepted" {
		all := sources.LoadPromotedNotes()
		me := sources.PromotedNote{Project: s.Project, Session: src, State: state, Title: title, Text: text, Tags: sources.NormalizeTags(tags)}
		for _, c := range sources.ConflictingNotes(me, all) {
			fmt.Fprintf(stdout, "conflict: another accepted note covers this ground — %q (from %s, %s). If one replaced the other: deja promote <id> --state superseded\n",
				firstLine(c.Title+" "+c.Text), c.Session, c.At.Format("2006-01-02"))
		}
	}
	if exportPath != "" {
		// The path is whatever the caller passed. A newline in it ends this
		// line and starts one of the caller's that reads as deja's own output.
		fmt.Fprintf(stdout, "exported to %s\n", search.SafePath(exportPath))
	}
	// The id deja resolved, not the one the reader typed: a prefix that stood
	// for several sessions would send the correction to whichever is newest —
	// measured, that was a different session from the one just marked (#884).
	quoted := pasteSafe(s.ID)
	fmt.Fprintf(stdout, "the note now outranks the raw transcript in recall; corrections append with `deja promote %s --state <state>`%s\n", quoted, pasteSafeCaveat(quoted))
	return nil
}

// notesWriteError turns a refused write of the notes file into something the
// reader can act on. A decision someone wants to keep is what these commands
// exist for, and `open …: permission denied` names a syscall and nothing to do
// about it — while index and forget both say what to change (#806). The same
// file is written by promote, `deja remember` and the MCP remember tool, and
// only promote said it (#869).
func notesWriteError(err error) error {
	if err == nil {
		return nil
	}
	// The directory before the errno: a volume that was unmounted fails with
	// EACCES on macOS, because deja is then trying to create a directory under
	// /Volumes — and "check that file's permissions" sends the reader after a
	// permission problem they do not have (#907).
	if dir := filepath.Dir(sources.NotesFile()); !dirExists(dir) {
		return fmt.Errorf("cannot write %s — %s is not there; the disk it lives on may have been unmounted", sources.NotesFile(), dir)
	}
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot write %s — check that file and its directory's permissions, or set DEJA_NOTES_FILE somewhere writable", sources.NotesFile())
	}
	// Everything else the disk can do to a write, in the same words the rest
	// of deja uses (#906).
	if reason := writeFailureReason(err); reason != err.Error() {
		return fmt.Errorf("cannot write %s — %s", sources.NotesFile(), reason)
	}
	return err
}

// markTakenBack says that an accepted mark cleared the rejected/superseded/
// stale one before it.
//
// The undo works and always did — the latest mark wins, so `--state accepted`
// drops the label and the demotion — but nothing said so. The measured
// alternatives all fail, two of them loudly: `--state none` and `--state
// clear` are rejected, and `deja forget --session deja-note-…` prints
// "sessions dropped: 1" while the label survives, because the state is read
// from the notes file and not from the index (#845).
func markTakenBack(src, state string, prior sources.Lifecycle) string {
	if state != "accepted" || prior.State == "" || prior.State == "accepted" {
		return ""
	}
	when := ""
	if !prior.At.IsZero() {
		when = " from " + prior.At.Format("2006-01-02")
	}
	// The echo is read and the id is pasted, so they are treated differently —
	// the same split the line above this one makes (#2768).
	return fmt.Sprintf("this takes back the %s mark%s: hits for %s are no longer labelled. Both marks stay in the note — `deja show %s`",
		prior.State, when, safeForStatusline(src, 200), pasteSafe("deja-note-"+strings.ReplaceAll(src, ":", "-")))
}

// distillSession quotes the session instead of summarizing it: the first user
// ask and the last assistant word, each trimmed — receipts, not generation.
func distillSession(s model.Session) string {
	var ask, answer string
	for _, m := range s.Messages {
		t := strings.TrimSpace(m.Text)
		if t == "" {
			continue
		}
		if ask == "" && m.Role == "user" {
			ask = t
		}
		if m.Role == "assistant" {
			answer = t
		}
	}
	parts := make([]string, 0, 2)
	if ask != "" {
		parts = append(parts, "asked: "+trimRunes(ask, 300))
	}
	if answer != "" {
		parts = append(parts, "outcome: "+trimRunes(answer, 300))
	}
	if len(parts) == 0 {
		return "promoted session (no text messages)"
	}
	return strings.Join(parts, " · ")
}

func trimRunes(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return trimRunes(s, 80)
}

// exportPromoted appends a Markdown block to a repo-visible notes file.
// Append-only like the store: a correction adds a new block below the old.
// exportPromoted appends the note to a file meant for someone else, and reports
// how many secrets the redaction pass replaced on the way out — the same floor
// `share` and `sync export` print, on the path that had none (#848).
func exportPromoted(path, title, text, src, state string, updated time.Time) (int, error) {
	// Separately, not packed and cut apart on the first newline: a title can
	// hold one — a note's is exempt from the bound every other title gets, and
	// the notes file is the one store a person writes by hand — and its tail
	// then arrived at the head of the body, beside deja's own "- state:" and
	// "- source:" lines (#2056).
	//
	// The body still sees the title's last line, because a credential can be
	// written with its key word at the end of one field and its value at the
	// start of the next, and the patterns for those allow a newline between the
	// two. That line is one line by construction, so the split below is exact
	// however many the title has.
	title, _ = redact.Text(title)
	context := title
	if i := strings.LastIndex(context, "\n"); i >= 0 {
		context = context[i+1:]
	}
	withContext, _ := redact.Text(context + "\n" + text)
	if head, rest, ok := strings.Cut(withContext, "\n"); ok && head == context {
		text = rest
	} else {
		// The pass swallowed the boundary — a private key spans it, and its
		// marker replaces the newline too. Keeping the joined text loses
		// nothing: by then the context line is inside the marker.
		text = withContext
	}
	// The masked spots in what is about to be written, and nothing else: a
	// secret redacted at ingest is already a marker here, and one this pass
	// replaces became one too. Adding the pass's own tally on top counted the
	// second kind twice (#2061). deja view counts the same way.
	masked := strings.Count(title, redact.Marker) + strings.Count(text, redact.Marker)
	// One line, because it is written as a heading.
	title = strings.Join(strings.Fields(title), " ")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	// The separating blank line is the leading "\n" below, which assumes the
	// file already ends with one. A markdown file whose last line has no
	// newline — a hand-written list, most often — got the heading glued to it
	// (#871).
	lead := ""
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > 0 && !endsWithNewline(path) {
		lead = "\n"
	}
	day := updated.UTC().Format("2006-01-02")
	if updated.IsZero() {
		day = time.Now().UTC().Format("2006-01-02")
	}
	_, err = fmt.Fprintf(f, "%s\n## %s\n\n- state: %s\n- source: %s (%s)\n\n%s\n", lead, title, state, src, day, text)
	return masked, err
}

// exportFailureReason names why the export path could not be written, without
// repeating the path the caller already prints.
func exportFailureReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "its directory does not exist"
	case strings.Contains(err.Error(), "is a directory"):
		return "that path is a directory"
	}
	return writeFailureReason(err)
}

// dirExists reports whether p is there as a directory. A path whose directory
// is gone is not a permission problem, whatever errno the platform picks.
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// writeFailureReason words why a write did not happen, in the same vocabulary
// everywhere. The index has said "no space left where the index is built"
// since #888 while every other path handed back the syscall — the notes file,
// the sync export and the stats page all reported ENOSPC as
// `open /…: no space left on device` (#906).
func writeFailureReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "its directory does not exist"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, syscall.ENOSPC):
		return "no space left on that disk"
	case errors.Is(err, syscall.EIO), errors.Is(err, syscall.ENXIO), errors.Is(err, syscall.ENODEV):
		return "that disk is not reachable"
	}
	return err.Error()
}

// endsWithNewline reports whether the file's last byte is a newline. Reading
// one byte is cheaper than reading the file, and the answer decides whether
// the appended section needs its own separator.
func endsWithNewline(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return true
	}
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, fi.Size()-1); err != nil {
		return true
	}
	return buf[0] == '\n'
}

// promotedNoteSources maps every promoted note's id to the session it came
// from, in one pass over the note log.
func promotedNoteSources() map[string]string {
	out := map[string]string{}
	for _, n := range sources.LoadPromotedNotes() {
		if n.Session == "" {
			continue
		}
		out["deja-note-"+strings.ReplaceAll(n.Session, ":", "-")] = n.Session
	}
	return out
}

// promotedNoteSource maps a promoted note's id back to the session it was
// promoted from. The id is built by replacing the colon in `harness:id`, which
// does not invert on its own — harness names carry dashes — so the note log is
// the authority.
func promotedNoteSource(noteID string) (string, string, string, bool) {
	src, title, body, found := "", "", "", false
	for _, n := range sources.LoadPromotedNotes() {
		if n.Session == "" || "deja-note-"+strings.ReplaceAll(n.Session, ":", "-") != noteID {
			continue
		}
		src, found = n.Session, true
		if t := strings.TrimSpace(n.Title); t != "" {
			title = t
		}
		if b := strings.TrimSpace(n.Text); b != "" {
			body = b
		}
	}
	return src, title, body, found
}

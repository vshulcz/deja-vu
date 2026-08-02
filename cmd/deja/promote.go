package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
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
		return fmt.Errorf("promote needs a session id prefix (see `deja last`)")
	}
	if !sources.NoteStates[state] {
		// Taking a mark back is the state nobody guesses: users reach for
		// --state none, --state clear or --unpromote, get this line, and read
		// four states none of which sounds like an undo (#845).
		return fmt.Errorf("promote: state must be accepted, rejected, superseded or stale — `--state accepted` takes an earlier mark back")
	}
	s, ok, err := findByPrefix(dir, prefix)
	noteAmbiguousPrefix(dir, prefix, "promoting")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no session matches %q", prefix)
	}
	if s.Harness == "deja" {
		return fmt.Errorf("%q is already a note — promote the source session instead", prefix)
	}
	text := strings.TrimSpace(noteText)
	if text == "" {
		text = distillSession(s)
	}
	src := s.Harness + ":" + s.ID
	prior := sources.PromotedLifecycles()[src]
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = firstLine(text)
	}
	if err := sources.AppendPromotedTagged(s.Project, title, text, src, state, tags, time.Now()); err != nil {
		return notesWriteError(err)
	}
	if exportPath != "" {
		masked, err := exportPromoted(exportPath, title, text, src, state, s.Updated)
		if err != nil {
			// The note is already written by this point, so failing with a bare
			// syscall told the reader their decision was lost when it was not
			// — and named a path with nothing to do about it (#871).
			fmt.Fprintf(stdout, "promoted %s as %s: %s\n", src, state, title)
			return fmt.Errorf("the note is kept, but %s could not be written (%s) — export it somewhere you can, or read the note back with `deja show %s`",
				exportPath, exportFailureReason(err), "deja-note-"+strings.ReplaceAll(src, ":", "-"))
		}
		// The one outbound path that said nothing: `--to` exists to hand a
		// decision to someone, and `share` and `sync export` both end with this
		// floor. The note is the user's own writing, so the text is not
		// rewritten — the warning is what was missing (#848).
		fmt.Fprintf(os.Stderr, "deja: %d secret%s masked in this file. pattern redaction is a floor — review before sending; rotate anything that leaked.\n", masked, pluralS(masked))
	}
	fmt.Fprintf(stdout, "promoted %s as %s: %s\n", src, state, title)
	if line := markTakenBack(src, state, prior); line != "" {
		fmt.Fprintln(stdout, line)
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
		fmt.Fprintf(stdout, "exported to %s\n", exportPath)
	}
	fmt.Fprintln(stdout, "the note now outranks the raw transcript in recall; corrections append with `deja promote", prefix, "--state <state>`")
	return nil
}

// notesWriteError turns a refused write of the notes file into something the
// reader can act on. A decision someone wants to keep is what these commands
// exist for, and `open …: permission denied` names a syscall and nothing to do
// about it — while index and forget both say what to change (#806). The same
// file is written by promote, `deja remember` and the MCP remember tool, and
// only promote said it (#869).
func notesWriteError(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot write %s — check that file and its directory's permissions, or set DEJA_NOTES_FILE somewhere writable", sources.NotesFile())
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
	return fmt.Sprintf("this takes back the %s mark%s: hits for %s are no longer labelled. Both marks stay in the note — `deja show deja-note-%s`",
		prior.State, when, src, strings.ReplaceAll(src, ":", "-"))
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
	body, counts := redact.Text(title + "\n" + text)
	masked := strings.Count(body, redact.Marker)
	for _, n := range counts {
		masked += n
	}
	title, text, _ = strings.Cut(body, "\n")
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
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, fs.ErrNotExist):
		return "its directory does not exist"
	case strings.Contains(err.Error(), "is a directory"):
		return "that path is a directory"
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

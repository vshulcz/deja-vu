package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
)

type note struct {
	TS      string `json:"ts"`
	Project string `json:"project"`
	Text    string `json:"text"`
	// Promoted-note fields. Session carries provenance (harness:id of the
	// source transcript), State its lifecycle: accepted, rejected,
	// superseded, stale. Corrections append a new entry; nothing rewrites.
	Kind    string   `json:"kind,omitempty"`
	Session string   `json:"session,omitempty"`
	State   string   `json:"state,omitempty"`
	Title   string   `json:"title,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	// SrcTS is when the source transcript was last updated. A note is a
	// distillation, so its evidence is as old as the session it came from.
	// Stamping the note with the moment someone typed `promote` made a
	// January conclusion the freshest thing in the store and sank the August
	// session that corrected it. Absent on notes written before this.
	SrcTS string `json:"src_ts,omitempty"`
}

func NotesFile() string {
	if p := os.Getenv("DEJA_NOTES_FILE"); p != "" {
		return p
	}
	// An explicit XDG_DATA_HOME wins on every platform so relocation and
	// hermetic tests behave the same everywhere.
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "deja", "notes.jsonl")
	}
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "deja", "notes.jsonl")
		}
		return filepath.Join(Home(), "AppData", "Roaming", "deja", "notes.jsonl")
	}
	return filepath.Join(Home(), ".local", "share", "deja", "notes.jsonl")
}

// NormalizeTags lowercases, trims a leading '#', drops empties/dupes and
// caps the count — tags are navigation handles, not prose.
func NormalizeTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		if t == "" || seen[t] || len(out) >= 8 {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func AppendNote(project, text string, now time.Time) error {
	return AppendNoteTagged(project, text, nil, now)
}

func AppendNoteTagged(project, text string, tags []string, now time.Time) error {
	project = strings.TrimSpace(project)
	if project == "" {
		return fmt.Errorf("project required")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("text required")
	}
	path := NotesFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("notes file is a symlink")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if now.IsZero() {
		now = time.Now()
	}
	if err := padTrailingNewline(f); err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(note{TS: now.UTC().Format(time.RFC3339Nano), Project: project, Text: text, Tags: NormalizeTags(tags)})
}

// padTrailingNewline writes a newline into the append handle f when the file's
// existing content does not end with one. notes.jsonl is documented as a
// hand-editable file, so an editor that drops the final newline is a real
// input: without this, the next record is glued onto the last line and the
// reader — which decodes only the first JSON value per line — silently drops
// it. It reads the last byte through f's own descriptor (fstat + pread), so
// there is no second path lookup to race against the symlink guard above.
// Best effort: a stat or read error leaves the append untouched.
func padTrailingNewline(f *os.File) error {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return nil
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = f.Write([]byte{'\n'})
	return err
}

func LoadNotes() []model.Session {
	// The error is recorded, not swallowed: notes are read straight here
	// rather than through parseFiles, so a notes file the process cannot read
	// looked exactly like one with nothing in it — 43 sessions of the user's
	// own decisions left the index and no line said so (#901).
	path := NotesFile()
	ss, err := ParseNotesFile(path)
	// A notes file that was never written is not a failure: every machine
	// starts without one, and reporting it made `deja index` warn about the
	// user's notes on a store that has none (#908).
	if !errors.Is(err, fs.ErrNotExist) {
		diagFileError(path, err)
	}
	return ss
}

func ParseNotesFile(path string) ([]model.Session, error) {
	return ParseNotesFileFromOffset(path, 0)
}

func ParseNotesFileFromOffset(path string, offset int64) ([]model.Session, error) {
	byDay := map[string]*model.Session{}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		ts, _ := m["ts"].(string)
		project, _ := m["project"].(string)
		text, _ := m["text"].(string)
		t, parseErr := time.Parse(time.RFC3339Nano, ts)
		if parseErr != nil || strings.TrimSpace(text) == "" {
			return
		}
		// A note written before deja recorded a project — or by a caller that
		// omitted it — is still the user's own decision, and the one class of
		// content deja cannot re-derive from anything else. It used to vanish
		// at index time with nothing reported (#771).
		project = strings.TrimSpace(project)
		if project == "" {
			project = "notes"
		}
		if kind, _ := m["kind"].(string); kind == "promoted" {
			// One session per promoted source session; corrections append as
			// further messages and the title tracks the latest state.
			src, _ := m["session"].(string)
			state, _ := m["state"].(string)
			title, _ := m["title"].(string)
			if src == "" {
				// Valid JSON deja cannot use: a promoted note has nothing to
				// attach to without its source session. Dropping it is right,
				// counting it as dropped is the part that was missing — the
				// line vanished with no trace anywhere (#814).
				diagMalformedLine(path)
				return
			}
			// Older promoted notes carry no state; the state is how the note is
			// labelled, not whether it is a note (#771).
			if !NoteStates[state] {
				state = "accepted"
			}
			key := "promoted\x00" + src
			s := byDay[key]
			if s == nil {
				s = &model.Session{ID: PromotedNoteID(src), Harness: "deja", Project: project, Path: path}
				byDay[key] = s
			}
			if title == "" {
				title = "promoted from " + src
			}
			s.Title = title + " [" + state + "]"
			// A promoted note is dated by the evidence it distils, not by the
			// moment it was filed: ranking decays with age, so promoting an
			// old session minted the newest thing in the store and buried the
			// newer session that contradicted it (V4).
			stamp := noteEvidenceTime(m, t)
			s.Touch(stamp)
			body := "[" + state + "] " + text + " (from " + src + ", " + stamp.UTC().Format("2006-01-02") + ")"
			if tagLine := renderNoteTags(m); tagLine != "" {
				body += " " + tagLine
			}
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: body, Time: stamp})
			return
		}
		// The bucket day is the reader's day, not UTC's. The line is labelled
		// by the day the id names (#883) while every other line is rendered in
		// the reader's zone (#849), so east of UTC a note written a quarter of
		// an hour after a session was dated the day before it and `deja last`
		// printed its dates out of order (#911).
		day := t.Local().Format("2006-01-02")
		key := project + "\x00" + day
		s := byDay[key]
		if s == nil {
			s = &model.Session{ID: "deja-" + day + "-" + project, Harness: "deja", Project: project, Path: path}
			byDay[key] = s
		}
		s.Touch(t)
		if tagLine := renderNoteTags(m); tagLine != "" {
			text += " " + tagLine
		}
		s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: t})
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(byDay))
	for key, s := range byDay {
		// A promoted note's corrections append, so file order serves the
		// oldest one first — and every reader takes the first messages. After
		// a hundred careful corrections the hook handed the agent the first
		// answer as fact (#812). Newest first: the latest correction is what
		// holds, which is what the title already says.
		if strings.HasPrefix(key, "promoted\x00") && len(s.Messages) > 1 {
			reverseMessages(s.Messages)
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project == out[j].Project {
			return out[i].Started.Before(out[j].Started)
		}
		return out[i].Project < out[j].Project
	})
	return out, nil
}

// noteEvidenceTime returns the source transcript's time for a promoted note,
// falling back to the note's own timestamp when the line predates src_ts or
// carries one that has not happened yet — a stamp ahead of the clock would
// outrank everything, which is the failure this exists to prevent.
func noteEvidenceTime(m map[string]any, filed time.Time) time.Time {
	raw, _ := m["src_ts"].(string)
	if raw == "" {
		return filed
	}
	src, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || src.IsZero() || src.After(filed) {
		return filed
	}
	return src
}

func reverseMessages(ms []model.Message) {
	for i, j := 0, len(ms)-1; i < j; i, j = i+1, j-1 {
		ms[i], ms[j] = ms[j], ms[i]
	}
}

// NoteStates are the lifecycle states a promoted note may carry.
var NoteStates = map[string]bool{"accepted": true, "rejected": true, "superseded": true, "stale": true}

// AppendPromoted appends a curated note distilled from a session. Appending
// the same session again records a correction; history is never rewritten.
func AppendPromoted(project, title, text, session, state string, now time.Time) error {
	return AppendPromotedTagged(project, title, text, session, state, nil, now)
}

func AppendPromotedTagged(project, title, text, session, state string, tags []string, now time.Time) error {
	return AppendPromotedSourced(project, title, text, session, state, tags, time.Time{}, now)
}

// AppendPromotedSourced also records when the source transcript was last
// updated, so the note ranks by the age of the evidence rather than by when
// someone got around to promoting it.
func AppendPromotedSourced(project, title, text, session, state string, tags []string, srcTS, now time.Time) error {
	if strings.TrimSpace(session) == "" {
		return fmt.Errorf("session required")
	}
	if !NoteStates[state] {
		return fmt.Errorf("state must be accepted, rejected, superseded or stale")
	}
	project = strings.TrimSpace(project)
	if project == "" || strings.TrimSpace(text) == "" {
		return fmt.Errorf("project and text required")
	}
	path := NotesFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("notes file is a symlink")
	}
	// An identical re-promote is not a correction. `promote` writes a record
	// every run, so re-promoting an unchanged decision (same state, text,
	// title and tags) used to append a duplicate: the note then carried the
	// same line N times, and each copy lifted its own weight in recall —
	// measured, a 5x-promoted note outscored an identical 1x one on the same
	// date. Skip the write when the newest surviving record for this session
	// already says the same thing; a real state or wording change differs and
	// still appends, and a forget rewrites the record away so a later promote
	// starts clean.
	if last, ok := lastPromotedNote(path, session); ok &&
		last.State == state && last.Text == text && last.Title == title &&
		equalStrings(last.Tags, NormalizeTags(tags)) {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if now.IsZero() {
		now = time.Now()
	}
	stamp := ""
	if !srcTS.IsZero() {
		stamp = srcTS.UTC().Format(time.RFC3339Nano)
	}
	if err := padTrailingNewline(f); err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(note{
		TS: now.UTC().Format(time.RFC3339Nano), Project: project, Text: text,
		Kind: "promoted", Session: session, State: state, Title: title,
		Tags: NormalizeTags(tags), SrcTS: stamp,
	})
}

// lastPromotedNote returns the newest promoted record still on disk for a
// source session. forget rewrites matching records away, so "still on disk"
// is what re-promote-after-forget needs: no record found means start clean.
func lastPromotedNote(path, session string) (note, bool) {
	var last note
	var found bool
	_ = scanJSONLFromOffset(path, 0, func(m map[string]any) {
		if k, _ := m["kind"].(string); k != "promoted" {
			return
		}
		if s, _ := m["session"].(string); s != session {
			return
		}
		last = note{}
		last.State, _ = m["state"].(string)
		last.Text, _ = m["text"].(string)
		last.Title, _ = m["title"].(string)
		if raw, ok := m["tags"].([]any); ok {
			for _, x := range raw {
				if v, ok := x.(string); ok {
					last.Tags = append(last.Tags, v)
				}
			}
		}
		found = true
	})
	return last, found
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renderNoteTags folds the tags array into "#tag" tokens appended to the
// indexed text: search, snippets and recall then handle tags with zero extra
// machinery, and `deja "#api"` works lexically.
func renderNoteTags(m map[string]any) string {
	raw, _ := m["tags"].([]any)
	var parts []string
	for _, tAny := range raw {
		if t, ok := tAny.(string); ok && t != "" {
			parts = append(parts, "#"+t)
		}
	}
	return strings.Join(parts, " ")
}

// PromotedNote is one curated note with its lifecycle state, for conflict
// surfacing.
type PromotedNote struct {
	Project string
	Session string
	State   string
	Title   string
	Text    string
	Tags    []string
	At      time.Time
}

// LoadPromotedNotes returns the latest state per promoted source session.
func LoadPromotedNotes() []PromotedNote {
	latest := map[string]*PromotedNote{}
	var order []string
	_ = scanJSONLFromOffset(NotesFile(), 0, func(m map[string]any) {
		kind, _ := m["kind"].(string)
		if kind != "promoted" {
			return
		}
		src, _ := m["session"].(string)
		state, _ := m["state"].(string)
		if src == "" || !NoteStates[state] {
			return
		}
		ts, _ := m["ts"].(string)
		t, _ := time.Parse(time.RFC3339Nano, ts)
		title, _ := m["title"].(string)
		text, _ := m["text"].(string)
		project, _ := m["project"].(string)
		var tags []string
		if raw, ok := m["tags"].([]any); ok {
			for _, x := range raw {
				if v, ok := x.(string); ok {
					tags = append(tags, v)
				}
			}
		}
		n, ok := latest[src]
		if !ok {
			n = &PromotedNote{Session: src}
			latest[src] = n
			order = append(order, src)
		}
		n.Project, n.State, n.Title, n.Text, n.Tags, n.At = project, state, title, text, tags, t
	})
	out := make([]PromotedNote, 0, len(order))
	for _, src := range order {
		out = append(out, *latest[src])
	}
	return out
}

// ConflictingNotes returns other ACCEPTED notes in the same project that
// overlap this note's topic — shared tags, or 3+ shared informative words.
// deja never auto-resolves; it puts the disagreement in front of the user
// with dates so the human can promote one and supersede the other.
func ConflictingNotes(candidate PromotedNote, all []PromotedNote) []PromotedNote {
	ctags := map[string]bool{}
	for _, t := range candidate.Tags {
		ctags[t] = true
	}
	cwords := noteWordSet(candidate.Title + " " + candidate.Text)
	var out []PromotedNote
	for _, n := range all {
		if n.Session == candidate.Session || n.State != "accepted" || n.Project != candidate.Project {
			continue
		}
		shareTag := false
		for _, t := range n.Tags {
			if ctags[t] {
				shareTag = true
				break
			}
		}
		shared := 0
		for w := range noteWordSet(n.Title + " " + n.Text) {
			if cwords[w] {
				shared++
			}
		}
		if shareTag || shared >= 3 {
			out = append(out, n)
		}
	}
	return out
}

func noteWordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,!?:;()[]\"'`")
		if len(w) >= 5 {
			out[w] = true
		}
	}
	// Two notes are related when they share words. Chinese, Japanese and Korean
	// write no separator between them, so a whole note was one word and no two
	// such notes could ever share the three the bar asks for (#1348).
	for _, b := range cjkfold.Bigrams(s) {
		out[b] = true
	}
	return out
}

// ForgetPromotedNotes deletes whole promoted lines whose note session the
// caller matched — what `deja forget` on a note itself has to mean. Dropping
// the session from the index only writes a tombstone: search goes quiet while
// the text stays in notes.jsonl, which deja wrote and rewrites elsewhere
// (#841). Forgetting the SOURCE session is the other case and still keeps the
// note, minus its borrowed title (#666).
func ForgetPromotedNotes(match func(noteSession string) bool) (int, error) {
	return rewriteNotes(func(m map[string]any) (map[string]any, bool) {
		if kind, _ := m["kind"].(string); kind != "promoted" {
			return m, false
		}
		src, _ := m["session"].(string)
		if src == "" || !match(PromotedNoteID(src)) {
			return m, false
		}
		return nil, true
	})
}

// PromotedNoteID is the session id a promoted note is indexed under, so a
// caller holding an id from the index can match the line that produced it.
func PromotedNoteID(sourceSession string) string {
	return "deja-note-" + strings.ReplaceAll(sourceSession, ":", "-")
}

// ForgetPromotedTitles strips the source session's opening line from every
// promoted note whose provenance matches.
//
// `promote` copies that line into the note as its title, and the note is a
// separate record, so `forget --session` removed the session and left its
// first sentence on screen in `deja last` — the sentence most likely to carry
// a customer name or a ticket id (#666).
//
// The note itself survives: the decision it was promoted for is often the
// reason the raw session was safe to forget. Only the borrowed title goes; the
// parser already falls back to "promoted from <src>" when it is absent.
func ForgetPromotedTitles(match func(session string) bool) (int, error) {
	return rewriteNotes(func(m map[string]any) (map[string]any, bool) {
		if kind, _ := m["kind"].(string); kind != "promoted" {
			return m, false
		}
		src, _ := m["session"].(string)
		if src == "" || !match(src) {
			return m, false
		}
		if title, _ := m["title"].(string); title == "" {
			return m, false
		}
		delete(m, "title")
		return m, true
	})
}

// RestorePromotedTitles fills the title back in for promoted notes whose source
// session is available again. `forget` clears it so the forgotten session's
// first line does not survive in the note (#666); when the session comes back
// the reason is gone, and without this the note stayed "promoted from <src>"
// forever — a round trip through forget/unforget left the store worse than it
// found it (#969).
func RestorePromotedTitles(titleFor func(session string) string) (int, error) {
	return rewriteNotes(func(m map[string]any) (map[string]any, bool) {
		if kind, _ := m["kind"].(string); kind != "promoted" {
			return m, false
		}
		if title, _ := m["title"].(string); title != "" {
			return m, false
		}
		src, _ := m["session"].(string)
		if src == "" {
			return m, false
		}
		title := titleFor(src)
		if title == "" {
			return m, false
		}
		m["title"] = title
		return m, true
	})
}

// rewriteNotes applies edit to every note line: it returns the replacement and
// whether anything changed, and a nil replacement deletes the line. The file is
// written through temp+rename, and the temp file is removed when the write
// fails so a full disk does not leave one behind (#808).
func rewriteNotes(edit func(map[string]any) (map[string]any, bool)) (int, error) {
	path := NotesFile()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var lines []string
	changed := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			lines = append(lines, line)
			continue
		}
		next, edited := edit(m)
		if !edited {
			lines = append(lines, line)
			continue
		}
		changed++
		if next == nil {
			continue
		}
		out, err := json.Marshal(next)
		if err != nil {
			lines = append(lines, line)
			changed--
			continue
		}
		lines = append(lines, string(out))
	}
	if changed == 0 {
		return 0, nil
	}
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	// A rewrite that ran out of room used to leave its temp file behind, on the
	// filesystem that just filled up (#808); atomicfile removes it on any
	// failure, and gives it a name a second writer cannot land on (#1319).
	if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
		return 0, err
	}
	return changed, nil
}

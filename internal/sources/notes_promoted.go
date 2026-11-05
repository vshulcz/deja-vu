package sources

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
)

// NoteStates are the lifecycle states a promoted note may carry.
var NoteStates = map[string]bool{
	model.LifecycleAccepted:   true,
	model.LifecycleRejected:   true,
	model.LifecycleSuperseded: true,
	model.LifecycleStale:      true,
}

// AppendPromoted appends a curated note distilled from a session. Appending
// the same session again records a correction; history is never rewritten.
func AppendPromoted(project, title, text, session, state string, now time.Time) error {
	return AppendPromotedTagged(project, title, text, session, state, nil, now)
}

// AppendPromotedTagged appends a promoted note with no source time.
func AppendPromotedTagged(project, title, text, session, state string, tags []string, now time.Time) error {
	return AppendPromotedSourced(project, title, text, session, state, tags, time.Time{}, now)
}

// AppendPromotedSourced also records when the source transcript was last
// updated, so the note ranks by the age of the evidence rather than by when
// someone got around to promoting it.
func AppendPromotedSourced(project, title, text, session, state string, tags []string, srcTS, now time.Time) error {
	if strings.TrimSpace(session) == "" {
		return errors.New("session required")
	}
	if !NoteStates[state] {
		return errors.New("state must be accepted, rejected, superseded or stale")
	}
	project = strings.TrimSpace(project)
	if project == "" || strings.TrimSpace(text) == "" {
		return errors.New("project and text required")
	}
	path := NotesFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("notes file is a symlink")
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

// AppendRepoPromotedSourced appends a sanitized promoted note to
// <repo>/.deja/notes.jsonl and returns the number of redacted secrets.
func AppendRepoPromotedSourced(repoRoot, project, title, text, session, state string, tags []string, srcTS, now time.Time) (int, error) {
	if strings.TrimSpace(session) == "" {
		return 0, errors.New("session required")
	}
	if !NoteStates[state] {
		return 0, errors.New("state must be accepted, rejected, superseded or stale")
	}
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		repoRoot = NotesRepoRoot("")
	}
	if repoRoot == "" {
		return 0, errors.New("not inside a git repository or .deja directory")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		project = ClaudeProjectName(repoRoot)
	}
	if project == "" || strings.TrimSpace(text) == "" {
		return 0, errors.New("project and text required")
	}
	cleanTitle, m1 := SanitizeRepoNote(title, repoRoot)
	cleanText, m2 := SanitizeRepoNote(text, repoRoot)
	masked := m1 + m2
	path := filepath.Join(repoRoot, ".deja", "notes.jsonl")
	if err := atomicfile.MkdirSharedAll(filepath.Dir(path)); err != nil {
		return 0, err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("notes file is a symlink")
	}
	if last, ok := lastPromotedNote(path, session); ok &&
		last.State == state && last.Text == cleanText && last.Title == cleanTitle &&
		equalStrings(last.Tags, NormalizeTags(tags)) {
		return masked, nil
	}
	f, err := atomicfile.OpenSharedFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	if now.IsZero() {
		now = time.Now()
	}
	stamp := ""
	if !srcTS.IsZero() {
		stamp = srcTS.UTC().Format(time.RFC3339Nano)
	}
	if err := padTrailingNewline(f); err != nil {
		return 0, err
	}
	if err := json.NewEncoder(f).Encode(note{
		TS: now.UTC().Format(time.RFC3339Nano), Project: project, Text: cleanText,
		Kind: "promoted", Session: session, State: state, Title: cleanTitle,
		Tags: NormalizeTags(tags), SrcTS: stamp,
	}); err != nil {
		return 0, err
	}
	return masked, nil
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
	return slices.Equal(a, b)
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

// notesMemo remembers one parse of the notes file. The file only grows — every
// promotion, every sync import, for the life of the machine — and it is read
// twice by a single `hook-tool` run before an edit: once for the promoted
// decision, once for the lifecycle states behind the conclusion scan (#2497).
// Keyed on size and modification time, so a promotion made in one MCP call is
// visible in the next; the manifest is remembered the same way.
//
// Size and time alone cannot see a rewrite that changes no byte count —
// swapping `accepted` for `rejected` is the same eight characters — on a
// filesystem whose modification times are coarse, read again inside that tick.
// It is not only a hand edit: the windows leg of CI hit it on every run. So a
// stamp younger than the clock's own resolution is confirmed by hashing the
// file, which is a read of a few kilobytes against the parse it saves.
var notesMemo struct {
	sync.Mutex
	path    string
	size    int64
	mod     time.Time
	sum     [32]byte
	notes   []PromotedNote
	states  map[string]Lifecycle
	parses  int
	stamped bool
}

// notesFreshWindow is how young a modification time has to be before the stamp
// stops being evidence on its own. Two seconds covers a filesystem that records
// whole seconds and a write that lands just after the memo was taken.
const notesFreshWindow = 2 * time.Second

// notesParses counts the parses that actually read the file, for the test that
// pins the memo.
func notesParses() int {
	notesMemo.Lock()
	defer notesMemo.Unlock()
	return notesMemo.parses
}

// notesStamp identifies the file a memo was built from. A file that cannot be
// stat'd is never memoized: the read below answers with nothing and remembering
// that would hide the file arriving a moment later.
func notesStamp(path string) (int64, time.Time, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, time.Time{}, false
	}
	return fi.Size(), fi.ModTime(), true
}

// notesSum is the file's content, hashed. Read only inside the window where the
// stamp cannot be trusted, and never on the hot path of an unchanged file.
func notesSum(path string) ([32]byte, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256(b), true
}

// notesFresh reports whether the memo still describes the file, and takes the
// lock's word for it — callers hold the lock.
func notesFresh(path string, size int64, mod time.Time, ok bool) bool {
	if !ok || !notesMemo.stamped || notesMemo.path != path ||
		notesMemo.size != size || !notesMemo.mod.Equal(mod) {
		return false
	}
	if time.Since(mod) > notesFreshWindow {
		return true
	}
	sum, read := notesSum(path)
	return read && sum == notesMemo.sum
}

// LoadPromotedNotes returns the latest state per promoted source session.
func LoadPromotedNotes() []PromotedNote {
	path := NotesFile()
	size, mod, statOK := notesStamp(path)
	notesMemo.Lock()
	if notesFresh(path, size, mod, statOK) && notesMemo.notes != nil {
		// A copy: the caller owns what it gets. The page appends its synced
		// decisions to this slice and sorts the result, and handing back the
		// memo's own array would let that reorder what the next caller reads.
		out := append([]PromotedNote(nil), notesMemo.notes...)
		notesMemo.Unlock()
		return mergeRepoPromotedNotes(path, out)
	}
	notesMemo.Unlock()

	out := loadPromotedNotesFrom(path)
	notesMemo.Lock()
	if statOK {
		if !notesFresh(path, size, mod, statOK) {
			notesMemo.path, notesMemo.size, notesMemo.mod = path, size, mod
			notesMemo.sum, _ = notesSum(path)
			notesMemo.stamped, notesMemo.states = true, nil
		}
		// The memo keeps its own array for the same reason the read above
		// hands out a copy.
		notesMemo.notes = append([]PromotedNote(nil), out...)
	}
	notesMemo.parses++
	notesMemo.Unlock()
	return mergeRepoPromotedNotes(path, out)
}

func mergeRepoPromotedNotes(personalPath string, personal []PromotedNote) []PromotedNote {
	rp := RepoNotesFile("")
	if rp == "" || rp == personalPath {
		return personal
	}
	repo := loadPromotedNotesFrom(rp)
	if len(repo) == 0 {
		return personal
	}
	bySession := make(map[string]int, len(personal))
	out := append([]PromotedNote(nil), personal...)
	for i, n := range out {
		bySession[n.Session] = i
	}
	for _, rn := range repo {
		if idx, ok := bySession[rn.Session]; ok {
			if rn.At.After(out[idx].At) {
				out[idx] = rn
			}
			continue
		}
		bySession[rn.Session] = len(out)
		out = append(out, rn)
	}
	return out
}

func loadPromotedNotesFrom(path string) []PromotedNote {
	fromRepo := isRepoNotesPath(path) && path != NotesFile()
	defaultProject := ""
	if fromRepo {
		defaultProject = ClaudeProjectName(filepath.Dir(filepath.Dir(path)))
	}
	latest := map[string]*PromotedNote{}
	var order []string
	_ = scanJSONLFromOffset(path, 0, func(m map[string]any) {
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
		project = strings.TrimSpace(project)
		if fromRepo && defaultProject != "" && (project == "" || strings.ContainsAny(project, `/\`)) {
			project = defaultProject
		}
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
	cfiles := map[string]bool{}
	for _, f := range NoteFileRefs(candidate) {
		cfiles[strings.ToLower(filepath.Base(f))] = true
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
		shareFile := false
		for _, f := range NoteFileRefs(n) {
			if cfiles[strings.ToLower(filepath.Base(f))] {
				shareFile = true
				break
			}
		}
		shared := 0
		for w := range noteWordSet(n.Title + " " + n.Text) {
			if cwords[w] {
				shared++
			}
		}
		if shareTag || shareFile || shared >= 3 {
			out = append(out, n)
		}
	}
	return out
}

func noteWordSet(s string) map[string]bool {
	out := map[string]bool{}
	for w := range strings.FieldsSeq(strings.ToLower(s)) {
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

// StaleCandidate pairs an accepted promoted note with a file it cites that was
// modified in a newer session after the note was recorded.
type StaleCandidate struct {
	Note      PromotedNote
	File      string
	TouchedAt time.Time
}

// NoteFileRefs extracts distinct file paths or filenames cited in a note's
// title and text.
func NoteFileRefs(n PromotedNote) []string {
	var out []string
	seen := map[string]bool{}
	for raw := range strings.FieldsSeq(n.Title + " " + n.Text) {
		tok := strings.Trim(raw, ".,!?:;()[]{}<>\"'`")
		if idx := strings.IndexByte(tok, ':'); idx > 0 {
			tok = tok[:idx]
		}
		tok = filepath.ToSlash(tok)
		if !looksLikeNoteFilePath(tok) || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

var noteFileExts = map[string]bool{
	".go": true, ".py": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".rs": true, ".c": true, ".cc": true, ".cpp": true, ".h": true, ".java": true,
	".rb": true, ".sh": true, ".sql": true, ".yaml": true, ".yml": true, ".toml": true,
	".json": true, ".md": true,
}

func looksLikeNoteFilePath(tok string) bool {
	if len(tok) < 4 || len(tok) > 160 || strings.Contains(tok, "://") || strings.HasPrefix(tok, ".") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(tok))
	if !noteFileExts[ext] {
		return false
	}
	base := filepath.Base(tok)
	return len(base) > len(ext)
}

// StaleCandidateNotes returns accepted notes whose cited files were touched in
// sessions strictly newer than the note's timestamp.
func StaleCandidateNotes(notes []PromotedNote, fileLastTouched map[string]time.Time) []StaleCandidate {
	if len(notes) == 0 || len(fileLastTouched) == 0 {
		return nil
	}
	normTouched := make(map[string]time.Time, len(fileLastTouched)*2)
	for path, t := range fileLastTouched {
		norm := strings.ToLower(filepath.ToSlash(path))
		if t.After(normTouched[norm]) {
			normTouched[norm] = t
		}
		base := filepath.Base(norm)
		if t.After(normTouched[base]) {
			normTouched[base] = t
		}
	}
	var out []StaleCandidate
	for _, n := range notes {
		if n.State != "accepted" || n.At.IsZero() {
			continue
		}
		for _, ref := range NoteFileRefs(n) {
			key := strings.ToLower(filepath.ToSlash(ref))
			touchedAt := normTouched[key]
			if touchedAt.IsZero() {
				touchedAt = normTouched[filepath.Base(key)]
			}
			if touchedAt.After(n.At) {
				out = append(out, StaleCandidate{Note: n, File: ref, TouchedAt: touchedAt})
				break
			}
		}
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
	return model.PromotedNoteID(sourceSession)
}

// RepoPromotedNoteID is the session id a repo-scoped promoted note is indexed
// under, keeping it distinct from a personal promoted note for the same source
// session.
func RepoPromotedNoteID(sourceSession string) string {
	return model.RepoPromotedNoteID(sourceSession)
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
	for line := range strings.SplitSeq(string(b), "\n") {
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

// firstNoteLine is the one line of a note that stands for it in a listing. A
// note is written by hand and can run to paragraphs; every one-line surface
// clips it for its own layout, and the clip should not be the first thing that
// notices a newline.
func firstNoteLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(line)
}

package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
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

// NotesFile is the path of deja's own notes file.
func NotesFile() string {
	if p := os.Getenv("DEJA_NOTES_FILE"); p != "" {
		return p
	}
	// An explicit XDG_DATA_HOME wins on every platform so relocation and
	// hermetic tests behave the same everywhere — an absolute one. A relative
	// value is ignored, as the spec says and as this repository already does
	// for XDG_CONFIG_HOME: followed, it puts the notes in whatever directory
	// the command ran in (#2790).
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
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

// NotesRepoRoot walks up from startDir (or the working directory when startDir is
// empty) to find the nearest ancestor containing a .deja directory or .git
// marker. DEJA_REPO_DIR overrides the lookup when set to an absolute path.
func NotesRepoRoot(startDir string) string {
	if dir := os.Getenv("DEJA_REPO_DIR"); filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	explicit := startDir != ""
	if !explicit {
		cwd, err := os.Getwd()
		if err != nil || !allowImplicitRepoCwd(cwd) {
			return ""
		}
		startDir = cwd
	}
	homeInTmp := inTempDir(Home())
	startInTmp := inTempDir(startDir)
	for cur := filepath.Clean(startDir); ; {
		if fi, err := os.Stat(filepath.Join(cur, ".deja")); err == nil && fi.IsDir() {
			return cur
		}
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		if homeInTmp && startInTmp && !inTempDir(parent) {
			break
		}
		cur = parent
	}
	if explicit {
		return filepath.Clean(startDir)
	}
	return ""
}

func allowImplicitRepoCwd(cwd string) bool {
	if inTempDir(Home()) || inTempDir(NotesFile()) || inTempDir(os.Getenv("DEJA_INDEX_DIR")) {
		return inTempDir(cwd)
	}
	return true
}

func inTempDir(p string) bool {
	if p == "" {
		return false
	}
	tmp := os.TempDir()
	if tmp == "" {
		return false
	}
	if hasPathPrefix(p, tmp) {
		return true
	}
	if evalTmp, err := filepath.EvalSymlinks(tmp); err == nil && hasPathPrefix(p, evalTmp) {
		return true
	}
	if evalP, err := filepath.EvalSymlinks(p); err == nil {
		if hasPathPrefix(evalP, tmp) {
			return true
		}
		if evalTmp, err := filepath.EvalSymlinks(tmp); err == nil && hasPathPrefix(evalP, evalTmp) {
			return true
		}
	}
	return false
}

func hasPathPrefix(p, root string) bool {
	p = filepath.Clean(p)
	root = filepath.Clean(root)
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}

// RepoNotesFile returns <repo>/.deja/notes.jsonl when inside a repository, or "".
func RepoNotesFile(startDir string) string {
	root := NotesRepoRoot(startDir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".deja", "notes.jsonl")
}

// NotesFiles lists the personal notes file and, when present on disk, the
// active repository's .deja/notes.jsonl.
func NotesFiles() []string {
	out := []string{NotesFile()}
	if rp := RepoNotesFile(""); rp != "" && rp != out[0] && fileExists(rp) {
		out = append(out, rp)
	}
	return out
}

// IsNotesFile reports whether path is a personal or repo-scoped notes file.
func IsNotesFile(path string) bool {
	if path == NotesFile() {
		return true
	}
	if rp := RepoNotesFile(""); rp != "" && path == rp {
		return true
	}
	return isRepoNotesPath(path)
}

func isRepoNotesPath(path string) bool {
	return filepath.Base(path) == "notes.jsonl" && filepath.Base(filepath.Dir(path)) == ".deja"
}

// SanitizeRepoNote normalizes workstation-absolute paths inside repoRoot and
// Home() and applies outbound secret redaction before a note is written to a
// repository-tracked .deja/notes.jsonl file.
func SanitizeRepoNote(text, repoRoot string) (string, int) {
	if repoRoot != "" {
		cleanRoot := filepath.Clean(repoRoot)
		text = strings.ReplaceAll(text, cleanRoot+string(filepath.Separator), "")
		if slashRoot := filepath.ToSlash(cleanRoot); slashRoot != cleanRoot {
			text = strings.ReplaceAll(text, slashRoot+"/", "")
		}
		if evalRoot, err := filepath.EvalSymlinks(cleanRoot); err == nil && evalRoot != cleanRoot {
			text = strings.ReplaceAll(text, evalRoot+string(filepath.Separator), "")
			if slashEval := filepath.ToSlash(evalRoot); slashEval != evalRoot {
				text = strings.ReplaceAll(text, slashEval+"/", "")
			}
		}
	}
	if home := Home(); home != "" {
		cleanHome := filepath.Clean(home)
		text = strings.ReplaceAll(text, cleanHome+string(filepath.Separator), "~/")
		if slashHome := filepath.ToSlash(cleanHome); slashHome != cleanHome {
			text = strings.ReplaceAll(text, slashHome+"/", "~/")
		}
		if evalHome, err := filepath.EvalSymlinks(cleanHome); err == nil && evalHome != cleanHome {
			text = strings.ReplaceAll(text, evalHome+string(filepath.Separator), "~/")
			if slashEval := filepath.ToSlash(evalHome); slashEval != evalHome {
				text = strings.ReplaceAll(text, slashEval+"/", "~/")
			}
		}
	}
	if filepath.Separator == '\\' {
		text = strings.ReplaceAll(text, `\`, "/")
	}
	redacted, _ := redact.Text(text)
	return redacted, strings.Count(redacted, redact.Marker)
}

// cleanTag removes what a tag cannot carry and cuts it to maxTagLen. A control
// byte in a tag reached notes.jsonl and every surface reading it, and an escape
// sequence is not something anyone meant to file a note under.
func cleanTag(t string) string {
	t = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, t)
	t = strings.Join(strings.Fields(t), "-")
	return truncateRunes(t, maxTagLen)
}

// maxTagLen bounds one tag. The count has been capped at 8 since tags landed;
// one tag's length was not bounded at all, so a 400-character tag was stored
// and printed whole (#1810). A handle someone types and searches for is short,
// and 64 bytes is well past anything anyone writes by hand.
const maxTagLen = 64

// NormalizeTags lowercases, trims a leading '#', drops empties/dupes and
// caps the count — tags are navigation handles, not prose.
func NormalizeTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cleanTag(t)), "#"))
		if t == "" || seen[t] || len(out) >= 8 {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// AppendNote appends an untagged note for project.
func AppendNote(project, text string, now time.Time) error {
	return AppendNoteTagged(project, text, nil, now)
}

// maxNoteLine bounds one line of the scan below. A note longer than this is
// not compared — it is written, and the duplicate check simply does not claim
// to cover it.
const maxNoteLine = 4 << 20

// ErrNoteExists says the note is already on file: same project, same text.
// Appending it again cost the agent a line of every later recall for one fact
// (#1736), so the write is refused and the caller says so.
var ErrNoteExists = errors.New("note already remembered")

// noteAlreadyStored reports whether this exact note is on file already. The
// answer is advisory: two processes checking at once can both write, and a
// read error reads as "not a duplicate" so a save is never lost to one. A
// duplicate that slips through costs a repeated line, a refused save costs the
// fact.
// It
// reads the file rather than an index: a note written a second ago has not
// been indexed yet, and back-to-back saves are exactly the case this exists
// for. Promoted notes are skipped — those carry provenance and a lifecycle,
// and a `remember` is not a correction to one.
func noteAlreadyStored(path, project, text string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 64*1024)
	for {
		line, tooLong, err := readNoteLine(br, maxNoteLine)
		if tooLong {
			// A note past the cap is not compared — that is what the cap says
			// — but it used to end the scan, so every note after it went
			// uncompared too and one oversized note turned the check off for
			// the whole store (#1812). notes.jsonl is append-only, so that
			// oversized line sits in front of everything written later.
			if err != nil {
				return false
			}
			continue
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var n note
			if json.Unmarshal(line, &n) == nil && n.Kind == "" &&
				n.Project == project && strings.TrimSpace(n.Text) == text {
				return true
			}
		}
		if err != nil {
			return false
		}
	}
}

// readNoteLine reads one line, reporting a line past max rather than buffering
// it: the caller skips it and carries on down the file.
func readNoteLine(br *bufio.Reader, most int) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, e := br.ReadSlice('\n')
		// The cap is on the line's content; the newline that ends it is not
		// part of the note, and counting it made a line of exactly max bytes
		// look one byte too long.
		if len(buf)+len(bytes.TrimSuffix(chunk, []byte("\n"))) > most {
			tooLong = true
			buf = nil
		}
		if !tooLong {
			buf = append(buf, chunk...)
		}
		if errors.Is(e, bufio.ErrBufferFull) {
			continue
		}
		return buf, tooLong, e
	}
}

// AppendNoteTagged appends a note for project with tags.
func AppendNoteTagged(project, text string, tags []string, now time.Time) error {
	project = strings.TrimSpace(project)
	if project == "" {
		return errors.New("project required")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("text required")
	}
	path := NotesFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if noteAlreadyStored(path, project, strings.TrimSpace(text)) {
		return ErrNoteExists
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("notes file is a symlink")
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

// AppendRepoNoteTagged appends a sanitized note to <repo>/.deja/notes.jsonl and
// returns the number of redacted secrets in the written text.
func AppendRepoNoteTagged(repoRoot, project, text string, tags []string, now time.Time) (int, error) {
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
	if project == "" {
		return 0, errors.New("project required")
	}
	if strings.TrimSpace(text) == "" {
		return 0, errors.New("text required")
	}
	cleanText, masked := SanitizeRepoNote(text, repoRoot)
	path := filepath.Join(repoRoot, ".deja", "notes.jsonl")
	if err := atomicfile.MkdirSharedAll(filepath.Dir(path)); err != nil {
		return 0, err
	}
	if noteAlreadyStored(path, project, strings.TrimSpace(cleanText)) {
		return 0, ErrNoteExists
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("notes file is a symlink")
	}
	f, err := atomicfile.OpenSharedFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	if now.IsZero() {
		now = time.Now()
	}
	if err := padTrailingNewline(f); err != nil {
		return 0, err
	}
	if err := json.NewEncoder(f).Encode(note{
		TS:      now.UTC().Format(time.RFC3339Nano),
		Project: project,
		Text:    cleanText,
		Tags:    NormalizeTags(tags),
	}); err != nil {
		return 0, err
	}
	return masked, nil
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
		return nil //nolint:nilerr // best effort, the append goes ahead
	}
	last := make([]byte, 1)
	if _, err = f.ReadAt(last, info.Size()-1); err != nil {
		return nil //nolint:nilerr // best effort, the append goes ahead
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = f.Write([]byte{'\n'})
	return err
}

// LoadNotes reads deja's notes file as sessions.
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
	if rp := RepoNotesFile(""); rp != "" && rp != path {
		rss, rerr := ParseNotesFile(rp)
		if !errors.Is(rerr, fs.ErrNotExist) {
			diagFileError(rp, rerr)
		}
		ss = append(ss, rss...)
	}
	return ss
}

// noteIDProject is the project name as a session id can carry it. An id is
// what every other surface keys on, and a project of "../../etc" put a path
// inside one.
func noteIDProject(project string) string {
	var b strings.Builder
	for _, r := range project {
		switch r {
		case '/', '\\', ':':
			b.WriteByte('-')
		case '.':
			// A dot is ordinary inside a name ("api.v2") and a way out of the
			// tree in a run of them.
			if strings.HasSuffix(b.String(), ".") {
				continue
			}
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "notes"
	}
	return out
}

// ParseNotesFile reads a notes file from the start.
func ParseNotesFile(path string) ([]model.Session, error) {
	return ParseNotesFileFromOffset(path, 0)
}

// ParseNotesFileFromOffset reads a notes file from offset on, one session per project and day.
func ParseNotesFileFromOffset(path string, offset int64) ([]model.Session, error) {
	byDay := map[string]*model.Session{}
	fromRepo := isRepoNotesPath(path) && path != NotesFile()
	from := ""
	idPrefix := "deja-"
	defaultProject := "notes"
	if fromRepo {
		from = "repo"
		idPrefix = "deja-repo-"
		if p := ClaudeProjectName(filepath.Dir(filepath.Dir(path))); p != "" {
			defaultProject = p
		}
	}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		ts, _ := m["ts"].(string)
		project, _ := m["project"].(string)
		text, _ := m["text"].(string)
		t, parseErr := time.Parse(time.RFC3339Nano, ts)
		if parseErr != nil || strings.TrimSpace(text) == "" {
			// Counted, not just skipped: this is the one store a person writes
			// by hand, holding the one thing deja cannot re-derive, and a
			// hand-written "2026-01-03" parses as nothing. The promoted branch
			// below has counted its own refusals since #814 (#2005).
			diagMalformedLine(path)
			return
		}
		// A note written before deja recorded a project — or by a caller that
		// omitted it — is still the user's own decision, and the one class of
		// content deja cannot re-derive from anything else. It used to vanish
		// at index time with nothing reported (#771).
		project = strings.TrimSpace(project)
		if project == "" || (fromRepo && defaultProject != "notes" && strings.ContainsAny(project, `/\`)) {
			project = defaultProject
		}
		idProject := noteIDProject(project)
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
				noteID := PromotedNoteID(src)
				if fromRepo {
					noteID = RepoPromotedNoteID(src)
				}
				s = &model.Session{ID: noteID, Harness: "deja", Project: project, Path: path, From: from}
				byDay[key] = s
			}
			// The display line is the decision, not the question it answers.
			// `promote` borrows the session's opening line as the title, so
			// `deja last` showed "should the retry budget go up to 10?
			// [accepted]" for a note that says it stays at 5 — the inverse of
			// what was decided (#2539). The borrowed title is the fallback for
			// a note whose text is gone, and it is also what `forget` clears.
			if line := firstNoteLine(text); line != "" {
				title = line
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
			s = &model.Session{ID: idPrefix + day + "-" + idProject, Harness: "deja", Project: project, Path: path, From: from}
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

package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

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
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
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
	return json.NewEncoder(f).Encode(note{TS: now.UTC().Format(time.RFC3339Nano), Project: project, Text: text, Tags: NormalizeTags(tags)})
}

func LoadNotes() []model.Session {
	ss, _ := ParseNotesFile(NotesFile())
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
				s = &model.Session{ID: "deja-note-" + strings.ReplaceAll(src, ":", "-"), Harness: "deja", Project: project, Path: path}
				byDay[key] = s
			}
			if title == "" {
				title = "promoted from " + src
			}
			s.Title = title + " [" + state + "]"
			s.Touch(t)
			body := "[" + state + "] " + text + " (from " + src + ", " + t.UTC().Format("2006-01-02") + ")"
			if tagLine := renderNoteTags(m); tagLine != "" {
				body += " " + tagLine
			}
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: body, Time: t})
			return
		}
		day := t.UTC().Format("2006-01-02")
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
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
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
	return json.NewEncoder(f).Encode(note{
		TS: now.UTC().Format(time.RFC3339Nano), Project: project, Text: text,
		Kind: "promoted", Session: session, State: state, Title: title,
		Tags: NormalizeTags(tags),
	})
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
	return out
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
	path := NotesFile()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	lines := strings.Split(string(b), "\n")
	changed := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if kind, _ := m["kind"].(string); kind != "promoted" {
			continue
		}
		src, _ := m["session"].(string)
		if src == "" || !match(src) {
			continue
		}
		if title, _ := m["title"].(string); title == "" {
			continue
		}
		delete(m, "title")
		out, err := json.Marshal(m)
		if err != nil {
			continue
		}
		lines[i] = string(out)
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		// A rewrite that ran out of room left its temp file behind, on the
		// filesystem that just filled up (#808).
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, err
	}
	return changed, nil
}

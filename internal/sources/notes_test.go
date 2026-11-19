package sources

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// notes.jsonl is documented as a hand-editable file. An editor that drops the
// final newline leaves the last record without one; the next `deja note` then
// appended its JSON glued onto that line, and the reader — which decodes only
// the first value per line — silently dropped the new note. The append must
// start a fresh line first.
func TestAppendNoteStartsAFreshLineWhenFileLacksTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)

	// A pre-existing record with NO trailing newline, as a hand-edit can leave.
	first := `{"ts":"2026-01-01T00:00:00Z","project":"proj","text":"first note"}`
	if err := os.WriteFile(path, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := AppendNote("proj", "second note", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	got, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range got {
		for _, m := range s.Messages {
			texts = append(texts, m.Text)
		}
	}
	joined := ""
	var joinedSb40 strings.Builder
	for _, x := range texts {
		joinedSb40.WriteString(x + "|")
	}
	joined += joinedSb40.String()
	if !contains(texts, "first note") {
		t.Errorf("first note lost: %q", joined)
	}
	if !contains(texts, "second note") {
		t.Errorf("second note lost — glued onto the previous line: %q", joined)
	}
}

// Both shapes were written by deja itself in earlier versions, and both
// vanished at index time with nothing reported — the user's own decisions are
// the one class of content deja cannot re-derive (#771).
func TestParseNotesKeepsOlderShapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	lines := []string{
		`{"ts":"2026-07-20T10:00:00Z","project":"proj/p","text":"an old plain note"}`,
		`{"ts":"2026-07-21T10:00:00Z","project":"proj/p","text":"a promoted note with no state","kind":"promoted","session":"claude:t1"}`,
		`{"ts":"2026-07-22T10:00:00Z","text":"a note with no project at all"}`,
		// Still rejected: a promoted note with no source has nothing to attach
		// to, and an empty body is not a note.
		`{"ts":"2026-07-23T10:00:00Z","project":"p","text":"orphan","kind":"promoted"}`,
		`{"ts":"2026-07-24T10:00:00Z","project":"p","text":"   "}`,
		`{"ts":"not a date","project":"p","text":"unparseable stamp"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range ss {
		for _, m := range s.Messages {
			texts = append(texts, m.Text)
		}
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{"an old plain note", "a promoted note with no state", "a note with no project at all"} {
		if !strings.Contains(joined, want) {
			t.Errorf("dropped %q:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"orphan", "unparseable stamp"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("kept %q, which has nothing to attach to", unwanted)
		}
	}
	// Dropping it is right; dropping it invisibly is not. A promoted line with
	// no source used to leave no trace at all — not in the index, not in the
	// skipped count (#814).
	malformed, _ := DiagSnapshot()
	if malformed[path] == 0 {
		t.Errorf("a promoted note with no source was dropped without being counted")
	}
	// A note with no project is filed under one rather than losing its home.
	var found bool
	for _, s := range ss {
		if s.Project == "notes" {
			found = true
		}
	}
	if !found {
		t.Errorf("the project-less note has no project: %#v", ss)
	}
}

// Corrections append, so file order served the oldest one first — and every
// reader takes the first messages. After a hundred careful corrections the
// hook handed the agent the first answer as fact (#812).
func TestPromotedNoteServesTheLatestCorrectionFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	// Local, so the two plain notes an hour apart share a bucket day in every
	// zone: at 10:00 UTC they straddled midnight west of UTC-10.
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.Local)
	var lines []string
	for i := 1; i <= 5; i++ {
		row := map[string]any{
			"kind": "promoted", "session": "claude:c1", "project": "p/proj",
			"title": "davit setpoint", "text": "correction " + strconv.Itoa(i),
			"src": "claude", "state": "accepted",
			"ts": base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano),
		}
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	// A plain note in the same file keeps its own order: it is a day's log,
	// not a decision superseded by the next line.
	plain, err := json.Marshal(map[string]any{
		"text": "plain first", "src": "claude", "project": "p/proj",
		"ts": base.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	plain2, err := json.Marshal(map[string]any{
		"text": "plain second", "src": "claude", "project": "p/proj",
		"ts": base.Add(time.Hour).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	lines = append(lines, string(plain), string(plain2))
	if err = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var note, log *sessionRef
	for i := range ss {
		if strings.HasPrefix(ss[i].ID, "deja-note-") {
			note = &sessionRef{ss[i].ID, ss[i].Messages}
		} else {
			log = &sessionRef{ss[i].ID, ss[i].Messages}
		}
	}
	if note == nil || len(note.msgs) != 5 {
		t.Fatalf("promoted note: %+v", note)
	}
	if !strings.Contains(note.msgs[0].Text, "correction 5") {
		t.Errorf("first message is %q, want the latest correction", note.msgs[0].Text)
	}
	if !strings.Contains(note.msgs[4].Text, "correction 1") {
		t.Errorf("last message is %q, want the first correction", note.msgs[4].Text)
	}
	if log == nil || len(log.msgs) != 2 {
		t.Fatalf("plain notes: %+v", log)
	}
	if !strings.Contains(log.msgs[0].Text, "plain first") {
		t.Errorf("plain notes were reordered: %q", log.msgs[0].Text)
	}
}

type sessionRef struct {
	id   string
	msgs []model.Message
}

func notesHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(dir, "notes.jsonl"))
	return dir
}

// Promoted notes outrank raw transcripts in recall, so what gets written here
// decides what an agent sees first.
func TestPromotedNotesRoundTrip(t *testing.T) {
	notesHome(t)
	if got := LoadPromotedNotes(); len(got) != 0 {
		t.Fatalf("empty store returned %d notes", len(got))
	}
	if err := AppendPromoted("proj", "Pool timeouts", "raise max_conns", "claude:s1", "accepted", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := AppendPromotedTagged("proj", "Retries", "add jitter", "claude:s2", "accepted", []string{"Retry", "  backoff  "}, time.Now()); err != nil {
		t.Fatal(err)
	}
	notes := LoadPromotedNotes()
	if len(notes) != 2 {
		t.Fatalf("notes = %d, want 2", len(notes))
	}
	var tagged PromotedNote
	for _, n := range notes {
		if n.Session == "claude:s2" {
			tagged = n
		}
	}
	// Trimmed and lowercased, in the order they were given: they are matched
	// as a set, so order carries no meaning and reordering would only make a
	// note read differently from how it was written.
	if len(tagged.Tags) != 2 || tagged.Tags[0] != "retry" || tagged.Tags[1] != "backoff" {
		t.Fatalf("tags = %v, want them trimmed and lowercased", tagged.Tags)
	}
	if tagged.State != "accepted" || tagged.Project != "proj" {
		t.Fatalf("note metadata lost: %+v", tagged)
	}
}

func TestNormalizeTagsDropsNoise(t *testing.T) {
	got := NormalizeTags([]string{" Retry ", "retry", "", "  ", "#BACKOFF"})
	if len(got) != 2 {
		t.Fatalf("tags = %v, want duplicates and blanks dropped", got)
	}
	if got[0] != "retry" || got[1] != "backoff" {
		t.Fatalf("tags = %v, want lowercased with the hash stripped", got)
	}
	// The cap exists so one note cannot flood tag matching.
	many := make([]string, 0, 12)
	for i := range 12 {
		many = append(many, string(rune('a'+i)))
	}
	if n := len(NormalizeTags(many)); n > 8 {
		t.Fatalf("kept %d tags, past the cap", n)
	}
	if NormalizeTags(nil) != nil {
		t.Fatal("nil tags produced a slice")
	}
}

// A note whose file is unreadable must not take the whole recall down with it.
func TestLoadPromotedNotesSurvivesGarbage(t *testing.T) {
	dir := notesHome(t)
	path := filepath.Join(dir, "notes.jsonl")
	body := `{"kind":"promoted","project":"p","title":"good","text":"keep","session":"claude:s1","state":"accepted"}
not json at all
{"kind":"promoted","project":"p","title":"also good","text":"keep too","session":"claude:s2","state":"accepted"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := LoadPromotedNotes()
	if len(notes) != 2 {
		t.Fatalf("a torn line cost %d of 2 notes", 2-len(notes))
	}
}

// The conflict check is what makes curation worth anything: two accepted notes
// covering the same ground should be surfaced, and unrelated ones should not.
func TestConflictingNotesNeedsSharedGround(t *testing.T) {
	candidate := PromotedNote{Project: "p", Session: "claude:new", State: "accepted",
		Title: "keys", Text: "deploy keys live in the environment", Tags: []string{"deploy"}}
	all := []PromotedNote{
		{Project: "p", Session: "claude:old", State: "accepted", Title: "keys", Text: "deploy keys live in the config", Tags: []string{"deploy"}},
		{Project: "p", Session: "claude:self", State: "accepted", Title: "unrelated", Text: "cron moved to 03:17"},
		{Project: "other", Session: "claude:elsewhere", State: "accepted", Title: "keys", Text: "deploy keys live in the config", Tags: []string{"deploy"}},
		{Project: "p", Session: "claude:rejected", State: "rejected", Title: "keys", Text: "deploy keys live in the config", Tags: []string{"deploy"}},
	}
	got := ConflictingNotes(candidate, all)
	if len(got) != 1 || got[0].Session != "claude:old" {
		t.Fatalf("conflicts = %+v, want only the accepted note in the same project", got)
	}
	// Re-promoting the same session is an update, not a disagreement.
	same := candidate
	same.Session = "claude:old"
	if c := ConflictingNotes(same, all); len(c) != 0 {
		t.Fatalf("a session conflicted with itself: %+v", c)
	}
}

func TestNotesFileFollowsItsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	// Windows resolves this under APPDATA rather than the home directory, so
	// leaving it alone points the test at the real machine.
	t.Setenv("APPDATA", filepath.Join(dir, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "AppData", "Local"))
	t.Setenv("DEJA_NOTES_FILE", "")
	t.Setenv("XDG_DATA_HOME", "")
	if got := NotesFile(); !strings.HasPrefix(got, dir) {
		t.Fatalf("NotesFile() = %q, outside the home directory", got)
	}
	custom := filepath.Join(dir, "elsewhere", "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", custom)
	if got := NotesFile(); got != custom {
		t.Fatalf("DEJA_NOTES_FILE ignored: %q", got)
	}
}

// ARCHITECTURE.md is where a reader goes to learn which day a `remember` lands
// on. It said UTC calendar day, which stopped being true in #911 — east of UTC
// the bucket carries the reader's day. The doc kept promising the old zone, so
// this derives the zone from the grouping itself and holds the sentence to it.
func TestArchitectureNamesTheZoneNotesAreGroupedIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	line := `{"ts":"2026-07-20T23:45:00Z","project":"tz","text":"the anemometer drifted"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", path)

	zone, err := time.LoadLocation("Pacific/Kiritimati") // UTC+14
	if err != nil {
		t.Skipf("zone unavailable: %v", err)
	}
	saved := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = saved })

	ss := LoadNotes()
	if len(ss) != 1 {
		t.Fatalf("got %d note sessions, want the 1 this case measures", len(ss))
	}
	groupedInUTC := strings.Contains(ss[0].ID, "2026-07-20")

	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "ARCHITECTURE.md"))
	if err != nil {
		t.Fatal(err)
	}
	sentence := regexp.MustCompile(`(?s)Notes are grouped into[^.]*\.`)
	m := sentence.Find([]byte(strings.Join(strings.Fields(string(doc)), " ")))
	if m == nil {
		t.Fatal("ARCHITECTURE.md no longer says how notes are grouped; a reader has nowhere else to learn the day")
	}
	claimsUTC := strings.Contains(string(m), "UTC")
	if claimsUTC != groupedInUTC {
		t.Errorf("bucket id %q was minted in %s, but ARCHITECTURE.md says: %s",
			ss[0].ID, map[bool]string{true: "UTC", false: "the reader's zone"}[groupedInUTC], m)
	}
}

// A promoted note's display line has to carry the decision, not the question
// the session opened with. `promote` borrows that opening line as the title,
// so `deja last` rendered "should the retry budget go up to 10? [accepted]"
// for a note whose text says the budget stays at 5 — the inverse of what was
// decided, the shape #2456 fixed for the standing-decisions line (#2539).
func TestPromotedNoteTitleLeadsWithTheDecision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	lines := []string{
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"the retry budget stays at 5; the pool change is what fixed the timeouts","kind":"promoted","session":"claude:dec","state":"accepted","title":"should the retry budget go up to 10 for the payments client?"}`,
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"keep the sidebar collapsed by default","kind":"promoted","session":"claude:side","state":"rejected"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, s := range sessions {
		titles[s.ID] = s.Title
	}
	got := titles[PromotedNoteID("claude:dec")]
	if !strings.HasPrefix(got, "the retry budget stays at 5") {
		t.Errorf("title leads with %q, want the decision", got)
	}
	if strings.Contains(got, "go up to 10") {
		t.Errorf("the question the note answered is still the display line: %q", got)
	}
	// The state stays where every one-line surface reads it, at the tail
	// SafeNoteTitle carries past its clip (#R11).
	if !strings.HasSuffix(got, " [accepted]") {
		t.Errorf("title lost its state: %q", got)
	}
	if got := titles[PromotedNoteID("claude:side")]; got != "keep the sidebar collapsed by default [rejected]" {
		t.Errorf("note with no borrowed title: %q", got)
	}
}

// The same conclusion saved twice — a long session, or two agents on one
// machine — used to land twice and cost the agent a line of every later recall
// for one fact (#1736).
func TestRememberingTheSameNoteTwiceIsANoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	when := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

	if err := AppendNote("api", "pgbouncer needs prepared statements off", when); err != nil {
		t.Fatal(err)
	}
	err := AppendNote("api", "pgbouncer needs prepared statements off", when.Add(time.Minute))
	if !errors.Is(err, ErrNoteExists) {
		t.Errorf("a duplicate was accepted: %v", err)
	}
	// Same text, different project, is a different note.
	if err = AppendNote("web", "pgbouncer needs prepared statements off", when); err != nil {
		t.Errorf("another project was refused: %v", err)
	}
	// A near-duplicate is not a duplicate.
	if err = AppendNote("api", "pgbouncer needs prepared statements off for now", when); err != nil {
		t.Errorf("a longer note was refused: %v", err)
	}
	b, err := os.ReadFile(NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(string(b)), "\n") + 1; n != 3 {
		t.Errorf("notes file holds %d rows, want 3:\n%s", n, b)
	}
}

// A note is the one class of content deja cannot re-derive from anything else,
// and the notes file is the one store a person can write by hand. A line the
// parser refuses has to be counted, or it is gone with nothing to find it by:
// not in the index, not in the run's narration, not in doctor (#2005). The
// promoted branch has counted its own refusals since #814.
func TestANoteThatCannotBeReadIsCounted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	body := strings.Join([]string{
		`{"ts":"2026-01-02T03:04:05Z","project":"app","text":"pgbouncer pool size is 40"}`,
		// A date without a time, which is what a person writes by hand.
		`{"ts":"2026-01-03","project":"app","text":"the retry budget is 3"}`,
		// No timestamp at all.
		`{"project":"app","text":"the pool lives in pool.go"}`,
		// Nothing to keep, but still a line someone wrote.
		`{"ts":"2026-01-04T03:04:05Z","project":"app","text":"   "}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	DiagSnapshot() // drop whatever another case left behind
	ss, err := ParseNotesFileFromOffset(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the good note is kept and the other three are not, or the
	// count below is about something else.
	msgs := 0
	for _, s := range ss {
		msgs += len(s.Messages)
	}
	if msgs != 1 {
		t.Fatalf("four notes in, %d messages out: the fixture is not the one this is about", msgs)
	}

	malformed, _ := DiagSnapshot()
	if got := malformed[path]; got != 3 {
		t.Errorf("three notes were dropped and %d were counted, so a hand-written note can vanish with no trace", got)
	}
}

// A session id is what every other surface keys on, so it must not carry a
// path that climbs out of the tree.
func TestANotesProjectCannotClimbOutOfTheTree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := `{"ts":"` + at + `","project":"../../etc","text":"a note filed against a path"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want the note", len(ss))
	}
	if strings.Contains(ss[0].ID, "..") || strings.Contains(ss[0].ID, "/") {
		t.Errorf("session id carries a path: %q", ss[0].ID)
	}
}

// A promoted note borrows its source session's opening line as a title. forget
// strips that title (#666); unforget restores it (#969); forgetting the note
// itself drops the line. Exercise the whole round trip.
func TestPromotedNoteTitleRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)

	src := "claude:sess1"
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if err := AppendPromotedSourced("proj", "Borrowed Title", "the decision text", src, "accepted", nil, time.Time{}, now); err != nil {
		t.Fatal(err)
	}

	// Read the record's own title, not the note session's display line: since
	// #2539 that line is the decision, so the borrowed title is only visible
	// in the store these three commands rewrite.
	titleOf := func() (string, bool) {
		notes := LoadPromotedNotes()
		for _, n := range notes {
			if PromotedNoteID(n.Session) == PromotedNoteID(src) {
				return n.Title, true
			}
		}
		return "", false
	}

	if title, ok := titleOf(); !ok || !strings.Contains(title, "Borrowed Title") {
		t.Fatalf("after promote, title = %q (present=%v)", title, ok)
	}
	// What a listing shows is the decision itself.
	ss, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		if s.ID == PromotedNoteID(src) && s.Title != "the decision text [accepted]" {
			t.Fatalf("display line = %q", s.Title)
		}
	}

	// forget strips the borrowed title.
	n, err := ForgetPromotedTitles(func(s string) bool { return s == src })
	if err != nil || n != 1 {
		t.Fatalf("ForgetPromotedTitles = %d, %v", n, err)
	}
	if title, ok := titleOf(); !ok || strings.Contains(title, "Borrowed Title") {
		t.Fatalf("title survived forget: %q", title)
	}

	// unforget restores it from the source session.
	n, err = RestorePromotedTitles(func(s string) string {
		if s == src {
			return "Restored Title"
		}
		return ""
	})
	if err != nil || n != 1 {
		t.Fatalf("RestorePromotedTitles = %d, %v", n, err)
	}
	if title, ok := titleOf(); !ok || !strings.Contains(title, "Restored Title") {
		t.Fatalf("title not restored: %q", title)
	}

	// forgetting the note itself removes the line entirely.
	n, err = ForgetPromotedNotes(func(id string) bool { return id == PromotedNoteID(src) })
	if err != nil || n != 1 {
		t.Fatalf("ForgetPromotedNotes = %d, %v", n, err)
	}
	if title, ok := titleOf(); ok {
		t.Fatalf("note survived ForgetPromotedNotes: %q", title)
	}
}

func TestPromotedNoteHelpersNoMatchChangeNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	if err := AppendPromotedSourced("proj", "Title", "text", "claude:keep", "accepted", nil, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, got := range []int{
		mustCount(t, func() (int, error) { return ForgetPromotedTitles(func(string) bool { return false }) }),
		mustCount(t, func() (int, error) { return ForgetPromotedNotes(func(string) bool { return false }) }),
		mustCount(t, func() (int, error) { return RestorePromotedTitles(func(string) string { return "" }) }),
	} {
		if got != 0 {
			t.Errorf("a no-match rewrite changed %d lines, want 0", got)
		}
	}
}

func mustCount(t *testing.T, f func() (int, error)) int {
	t.Helper()
	n, err := f()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A note's bucket is labelled by the day its id names (#883) while sessions are
// dated in the reader's zone (#849). Minting the bucket in UTC put the two
// calendars in one list: east of UTC a note written a quarter of an hour after
// a session was dated the day before it (#911).
func TestNoteBucketFollowsTheReadersDay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	line := `{"ts":"2026-07-20T23:45:00Z","project":"tz","text":"the anemometer drifted"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", path)

	zone, err := time.LoadLocation("Pacific/Kiritimati") // UTC+14
	if err != nil {
		t.Skipf("zone unavailable: %v", err)
	}
	saved := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = saved })

	ss := LoadNotes()
	if len(ss) != 1 {
		t.Fatalf("got %d note sessions, want 1", len(ss))
	}
	// 23:45 UTC is 13:45 the next day in Kiritimati: that is the day the
	// reader's other lines carry, so it is the day the bucket carries.
	if want := "deja-2026-07-21-tz"; ss[0].ID != want {
		t.Errorf("bucket id = %q, want %q", ss[0].ID, want)
	}
}

// A rewrite that changes no byte count — `accepted` for `rejected` is the same
// eight characters — was invisible to a memo keyed on size and modification
// time, on a filesystem whose times are coarse. The windows leg hit it on every
// run; a promotion made in one MCP call was then answered stale in the next.
func TestARewriteOfTheSameLengthIsSeen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	line := func(state string) string {
		return `{"kind":"promoted","session":"claude:a","project":"app","state":"` + state +
			`","title":"t","text":"keep the pool","ts":"` + now + `"}` + "\n"
	}
	if err := os.WriteFile(path, []byte(line("accepted")), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := LoadPromotedNotes(); len(got) != 1 || got[0].State != "accepted" {
		t.Fatalf("first read: %+v", got)
	}

	// Same length and, once the stamp is put back, the same modification time —
	// which is what a coarse clock hands the memo on its own.
	if err := os.WriteFile(path, []byte(line("rejected")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := LoadPromotedNotes(); len(got) != 1 || got[0].State != "rejected" {
		t.Errorf("the rewrite was answered from the memo: %+v", got)
	}
	if got := PromotedLifecycles(); got["claude:a"].State != "rejected" {
		t.Errorf("the lifecycle came from the memo: %+v", got)
	}
}

func writeNotesFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The notes file only grows, and a hook before every edit reads it twice — once
// for the promoted decision, once for the lifecycle states. What one read found
// answers for the next, the way the manifest is remembered (#2497).
func TestTheNotesFileIsParsedOncePerProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	writeNotesFile(t, path, `{"kind":"promoted","session":"claude:a","project":"app","state":"accepted","title":"t","text":"keep the pool","ts":"`+now+`"}`+"\n")

	before := notesParses()
	if got := LoadPromotedNotes(); len(got) != 1 || got[0].Text != "keep the pool" {
		t.Fatalf("first read is wrong: %+v", got)
	}
	if got := PromotedLifecycles(); got["claude:a"].State != "accepted" {
		t.Fatalf("lifecycles are wrong: %+v", got)
	}
	// Two derivations of the same file — the notes and the lifecycle states —
	// each parsed once. They keep different tie rules, so one cannot be built
	// from the other; what the memo removes is the repeat.
	if n := notesParses() - before; n != 2 {
		t.Errorf("the first pair of reads parsed the file %d times, want 2", n)
	}
	for range 3 {
		LoadPromotedNotes()
		PromotedLifecycles()
	}
	if n := notesParses() - before; n != 2 {
		t.Errorf("repeat reads of an unchanged file parsed it again: %d parses in all", n)
	}

	// A file that changed is read again: the MCP server lives across many
	// calls, and a promotion made in one of them has to be visible in the next.
	writeNotesFile(t, path, `{"kind":"promoted","session":"claude:a","project":"app","state":"rejected","title":"t","text":"keep the pool","ts":"`+now+`"}`+"\n")
	if got := LoadPromotedNotes(); len(got) != 1 || got[0].State != "rejected" {
		t.Fatalf("a changed file was answered from the memo: %+v", got)
	}
}

// The caller owns the slice it gets: the view page appends to it and sorts it.
func TestAMemoisedReadCannotBeReorderedByItsCaller(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	writeNotesFile(t, path,
		`{"kind":"promoted","session":"claude:a","project":"app","state":"accepted","title":"a","text":"first","ts":"`+now+`"}`+"\n"+
			`{"kind":"promoted","session":"claude:b","project":"app","state":"accepted","title":"b","text":"second","ts":"`+now+`"}`+"\n")

	first := LoadPromotedNotes()
	if len(first) != 2 {
		t.Fatalf("fixture: %d notes", len(first))
	}
	first[0], first[1] = first[1], first[0]
	first[0].Text = "clobbered"

	again := LoadPromotedNotes()
	if again[0].Text != "first" || again[1].Text != "second" {
		t.Errorf("one caller's edit reached the next read: %+v", again)
	}
}

// Two notes are related when they share words. Chinese, Japanese and Korean
// write no separator between them, so a whole note was one word and no two such
// notes could share the three the bar asks for.
func TestNoteWordSetSeesCJKWords(t *testing.T) {
	a := noteWordSet("调度器移到了单独的进程并且限制了重试次数")
	b := noteWordSet("调度器移到了单独的进程后来又调整了一次")
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	if shared < 3 {
		t.Errorf("two notes about the same thing share %d words, which is under the bar for relating them", shared)
	}
}

// Notes about different things must not become related, or the bar means
// nothing.
func TestNoteWordSetKeepsUnrelatedCJKApart(t *testing.T) {
	// Not a trivially distant pair: both are ordinary sentences carrying the
	// grammatical characters every Chinese sentence carries — 的, 了, 我们 — so
	// the bar has to survive shared function words, not just shared topics.
	a := noteWordSet("我们把调度器移到了单独的进程")
	b := noteWordSet("我们把日志的格式换成了结构化输出")
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	if shared >= 3 {
		t.Errorf("two unrelated notes share %d words", shared)
	}
}

// A relative XDG_DATA_HOME is ignored, the way a relative XDG_CONFIG_HOME
// already is: followed, it puts the notes file in whatever directory the
// command happened to run in (#2790).
func TestNotesIgnoreARelativeDataHome(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t.Setenv("XDG_DATA_HOME", "share")
	if p := NotesFile(); !filepath.IsAbs(p) {
		t.Errorf("NotesFile() = %q for a relative XDG_DATA_HOME, want a path under the home", p)
	}
	abs := filepath.Join(home, "elsewhere")
	t.Setenv("XDG_DATA_HOME", abs)
	if p := NotesFile(); p != filepath.Join(abs, "deja", "notes.jsonl") {
		t.Errorf("NotesFile() = %q, want it under the absolute XDG_DATA_HOME", p)
	}
}

func TestNoteFileRefsAndStaleCandidateNotes(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	note := PromotedNote{
		Project: "deja-vu",
		Session: "claude:s1",
		State:   "accepted",
		Title:   "Keep retry cap in internal/index/ingest.go:42",
		Text:    "Also see `cmd/deja/promote.go` and https://example.com/foo.go for context.",
		At:      t0,
	}

	refs := NoteFileRefs(note)
	for _, want := range []string{"internal/index/ingest.go", "cmd/deja/promote.go"} {
		if !slices.Contains(refs, want) {
			t.Fatalf("NoteFileRefs = %v, missing %q", refs, want)
		}
	}
	for _, r := range refs {
		if r == "https://example.com/foo.go" {
			t.Fatalf("NoteFileRefs should ignore URLs, got %v", refs)
		}
	}

	stale := StaleCandidateNotes([]PromotedNote{note}, map[string]time.Time{
		"/work/deja-vu/internal/index/ingest.go": t0.Add(2 * time.Hour),
	})
	if len(stale) != 1 || stale[0].File != "internal/index/ingest.go" {
		t.Fatalf("StaleCandidateNotes = %+v, want 1 entry for internal/index/ingest.go", stale)
	}

	notStale := StaleCandidateNotes([]PromotedNote{note}, map[string]time.Time{
		"/work/deja-vu/internal/index/ingest.go": t0.Add(-time.Hour),
	})
	if len(notStale) != 0 {
		t.Fatalf("StaleCandidateNotes with older touch = %+v, want empty", notStale)
	}
}

func TestConflictingNotesDetectsSharedFileRef(t *testing.T) {
	a := PromotedNote{
		Project: "deja-vu",
		Session: "claude:a",
		State:   "accepted",
		Title:   "Pool cap in pool.go",
		Text:    "Max 4 idle conns.",
	}
	b := PromotedNote{
		Project: "deja-vu",
		Session: "claude:b",
		State:   "accepted",
		Title:   "Raise limit in internal/db/pool.go",
		Text:    "Set to 16.",
	}
	got := ConflictingNotes(b, []PromotedNote{a, b})
	if len(got) != 1 || got[0].Session != "claude:a" {
		t.Fatalf("ConflictingNotes = %+v, want claude:a via shared pool.go", got)
	}
}

func TestNotesPathAndAppendParse(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_NOTES_FILE", "")
	t.Setenv("XDG_DATA_HOME", xdg)
	if got, want := NotesFile(), filepath.Join(xdg, "deja", "notes.jsonl"); got != want {
		t.Fatalf("NotesFile=%q want %q", got, want)
	}
	path := filepath.Join(t.TempDir(), "private", "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	// decision two is the same instant, so the two share a local day in every
	// zone. An hour apart they straddled midnight at UTC-3 (#5044).
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	if err := AppendNote("app", "decision one", when); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, appendMustRead(t, path, `{"ts":"2026-01-02T02:04:05Z","project":"app","text":"decision two"}`+"\n"+`{"ts":"bad","project":"app","text":"ignored"}`+"\n"+`{"ts":"2026-01-03T00:00:00Z","project":"other","text":"other"}`+"\n"+`{"ts":"2026-01-03T00:00:00Z","project":"app","text":"torn`), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseNotesFile(path)
	if err != nil || len(ss) != 2 {
		t.Fatalf("notes=%#v err=%v", ss, err)
	}
	if ss[0].Project != "app" || len(ss[0].Messages) != 2 || ss[1].Project != "other" {
		t.Fatalf("grouped notes=%#v", ss)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	offset := bytes.IndexByte(data, '\n') + 1
	if got, err := ParseNotesFileFromOffset(path, int64(offset)); err != nil || len(got) != 2 {
		t.Fatalf("offset notes=%#v err=%v", got, err)
	}
	if err := AppendNote("", "x", when); err == nil {
		t.Fatal("empty project accepted")
	}
	if err := AppendNote("app", "", when); err == nil {
		t.Fatal("empty text accepted")
	}
	if err := AppendNote("app", "  preserved  ", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{filepath.Dir(path), path} {
			info, statErr := os.Stat(p)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != map[bool]os.FileMode{true: 0o600, false: 0o700}[p == path] {
				t.Fatalf("%s mode=%o", p, info.Mode().Perm())
			}
		}
	}
}

func TestNotesSymlinkAndMissingFile(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "missing", "notes.jsonl"))
	if got := LoadNotes(); got != nil {
		t.Fatalf("missing notes=%#v", got)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	t.Setenv("DEJA_NOTES_FILE", link)
	if err := AppendNote("p", "x", time.Now()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink append err=%v", err)
	}
}

func appendMustRead(t *testing.T, path, suffix string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return append(b, suffix...)
}

func TestConflictingNotes(t *testing.T) {
	a := PromotedNote{Project: "app", Session: "claude:a", State: "accepted", Title: "retry policy", Text: "use exponential backoff with jitter for outbound retries", Tags: []string{"retries"}}
	b := PromotedNote{Project: "app", Session: "claude:b", State: "accepted", Title: "retry policy v2", Text: "fixed-interval retries are simpler and sufficient here", Tags: []string{"retries"}}
	c := PromotedNote{Project: "app", Session: "claude:c", State: "superseded", Title: "old retries", Text: "exponential backoff jitter outbound retries", Tags: []string{"retries"}}
	d := PromotedNote{Project: "other", Session: "claude:d", State: "accepted", Title: "retry policy", Text: "exponential backoff with jitter for outbound retries", Tags: []string{"retries"}}
	all := []PromotedNote{a, b, c, d}
	got := ConflictingNotes(a, all)
	if len(got) != 1 || got[0].Session != "claude:b" {
		t.Fatalf("conflicts = %+v", got)
	}
	// No tags: 3+ shared informative words still connect them ("retry",
	// "policy", "retries" — a genuine topical clash).
	a2, b2 := a, b
	a2.Tags, b2.Tags = nil, nil
	if got := ConflictingNotes(a2, []PromotedNote{a2, b2}); len(got) != 1 {
		t.Fatalf("topical word overlap must conflict, got %+v", got)
	}
	// A note sharing fewer than 3 informative words stays unrelated.
	far := PromotedNote{Project: "app", Session: "claude:e", State: "accepted", Title: "logging", Text: "ship structured logs with retries counter"}
	if got := ConflictingNotes(a2, []PromotedNote{a2, far}); len(got) != 0 {
		t.Fatalf("weak overlap conflicted: %+v", got)
	}
}

// `promote` copies the source session's first line into the note as its title.
// The note is a separate record, so `forget --session` removed the session and
// left that line visible in `deja last` — the sentence most likely to carry a
// customer name or a ticket id (#666).
func TestForgetPromotedTitles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	lines := []string{
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"cap retries at 3","kind":"promoted","session":"claude:ps1","state":"accepted","title":"our customer ACME-4471 hit the retry storm"}`,
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"other decision","kind":"promoted","session":"claude:other","state":"accepted","title":"an unrelated session title"}`,
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"a plain note","kind":"note"}`,
		// A note of another kind that happens to name the same session: only
		// `promote` borrows a title, so only promoted notes lose one.
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"a linked note","kind":"note","session":"claude:ps1","title":"a title this command must not touch"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := ForgetPromotedTitles(func(src string) bool { return strings.Contains(src, "ps1") })
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("cleared %d titles, want 1", n)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "ACME-4471") {
		t.Errorf("the forgotten session's first line survived:\n%s", got)
	}
	// The decision it was promoted for is often why the raw session was safe
	// to forget — the note itself must stay.
	if !strings.Contains(got, "cap retries at 3") {
		t.Errorf("the promoted decision was destroyed:\n%s", got)
	}
	// Unrelated notes are untouched, including plain ones.
	if !strings.Contains(got, "an unrelated session title") || !strings.Contains(got, "a plain note") ||
		!strings.Contains(got, "a title this command must not touch") {
		t.Errorf("touched a note it should not have:\n%s", got)
	}
	// Idempotent: a second run has nothing left to clear.
	if again, err := ForgetPromotedTitles(func(src string) bool { return strings.Contains(src, "ps1") }); err != nil || again != 0 {
		t.Fatalf("second run cleared %d, err %v", again, err)
	}
}

// Nothing matched must leave the file alone. Rewriting it in place for a
// no-op risks the notes of every other session on a crash between write and
// rename, for no change at all.
func TestForgetPromotedTitlesLeavesTheFileAloneWhenNothingMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	line := `{"ts":"2026-07-21T10:00:00Z","project":"p","text":"d","kind":"promoted","session":"claude:other","title":"t"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, forgetErr := ForgetPromotedTitles(func(string) bool { return false }); forgetErr != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, forgetErr)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("rewrote the notes file for a no-op: %v -> %v", before.ModTime(), after.ModTime())
	}
}

func TestForgetPromotedTitlesWithNoNotesFile(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "missing.jsonl"))
	if n, err := ForgetPromotedTitles(func(string) bool { return true }); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v — a missing notes file is not an error", n, err)
	}
}

func TestRepoScopedNotesSanitizeMergeAndProvenance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	personalNotes := filepath.Join(home, "personal-notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", personalNotes)

	repoDir := filepath.Join(home, "work", "payments-api")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(repoDir, "internal", "handler")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subDir)

	if got := NotesRepoRoot(""); got != repoDir {
		t.Fatalf("NotesRepoRoot() = %q, want %q", got, repoDir)
	}
	wantRepoNotes := filepath.Join(repoDir, ".deja", "notes.jsonl")
	if got := RepoNotesFile(""); got != wantRepoNotes {
		t.Fatalf("RepoNotesFile() = %q, want %q", got, wantRepoNotes)
	}

	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := AppendNoteTagged("payments-api", "personal note stays in home", []string{"local"}, now); err != nil {
		t.Fatal(err)
	}

	secret := "sk-ant-api03-" + strings.Repeat("a", 40)
	rawNote := "check " + filepath.Join(repoDir, "internal", "handler", "charge.go") + " and " + filepath.Join(home, ".config", "app.env") + " token " + secret
	masked, err := AppendRepoNoteTagged(repoDir, "", rawNote, []string{"team"}, now)
	if err != nil {
		t.Fatalf("AppendRepoNoteTagged: %v", err)
	}
	if masked == 0 {
		t.Fatal("expected secret in repo note to be redacted and counted")
	}
	if _, dupErr := AppendRepoNoteTagged(repoDir, "", rawNote, []string{"team"}, now); !errors.Is(dupErr, ErrNoteExists) {
		t.Fatalf("duplicate repo note err = %v, want ErrNoteExists", dupErr)
	}

	rawBytes, err := os.ReadFile(wantRepoNotes)
	if err != nil {
		t.Fatal(err)
	}
	diskText := string(rawBytes)
	if strings.Contains(diskText, secret) {
		t.Fatalf("secret leaked into .deja/notes.jsonl: %s", diskText)
	}
	if strings.Contains(diskText, repoDir) {
		t.Fatalf("absolute repo path leaked into .deja/notes.jsonl: %s", diskText)
	}
	if !strings.Contains(diskText, "internal/handler/charge.go") {
		t.Fatalf("expected repo-relative path in .deja/notes.jsonl: %s", diskText)
	}

	pMasked, err := AppendRepoPromotedSourced(
		repoDir,
		"payments-api",
		"fix retry budget in "+filepath.Join(repoDir, "retry.go"),
		"keep retry budget at 3 ("+secret+")",
		"claude:sess123",
		"accepted",
		[]string{"retry"},
		now.Add(-time.Hour),
		now,
	)
	if err != nil {
		t.Fatalf("AppendRepoPromotedSourced: %v", err)
	}
	if pMasked == 0 {
		t.Fatal("expected secret in promoted repo note to be redacted")
	}
	// Idempotent re-promote with identical content does not duplicate.
	if _, err := AppendRepoPromotedSourced(
		repoDir,
		"payments-api",
		"fix retry budget in "+filepath.Join(repoDir, "retry.go"),
		"keep retry budget at 3 ("+secret+")",
		"claude:sess123",
		"accepted",
		[]string{"retry"},
		now.Add(-time.Hour),
		now,
	); err != nil {
		t.Fatalf("idempotent AppendRepoPromotedSourced: %v", err)
	}

	files := NotesFiles()
	if len(files) != 2 || files[0] != personalNotes || files[1] != wantRepoNotes {
		t.Fatalf("NotesFiles() = %v, want [%q, %q]", files, personalNotes, wantRepoNotes)
	}
	if !IsNotesFile(personalNotes) || !IsNotesFile(wantRepoNotes) {
		t.Fatal("IsNotesFile returned false for personal or repo notes file")
	}

	sessions := LoadNotes()
	if len(sessions) != 3 {
		t.Fatalf("LoadNotes() returned %d sessions, want 3 (1 personal day note, 1 repo day note, 1 repo promoted note): %#v", len(sessions), sessions)
	}
	var sawPersonal, sawRepoDay, sawRepoPromoted bool
	for _, s := range sessions {
		switch {
		case s.From == "" && strings.HasPrefix(s.ID, "deja-"):
			sawPersonal = true
		case s.From == "repo" && strings.HasPrefix(s.ID, "deja-repo-note-"):
			sawRepoPromoted = true
		case s.From == "repo" && strings.HasPrefix(s.ID, "deja-repo-"):
			sawRepoDay = true
		}
	}
	if !sawPersonal || !sawRepoDay || !sawRepoPromoted {
		t.Fatalf("sessions missing expected provenance: personal=%v repoDay=%v repoPromoted=%v (%#v)", sawPersonal, sawRepoDay, sawRepoPromoted, sessions)
	}

	promoted := LoadPromotedNotes()
	if len(promoted) != 1 || promoted[0].Session != "claude:sess123" || promoted[0].State != "accepted" {
		t.Fatalf("LoadPromotedNotes() = %#v", promoted)
	}
	lc := PromotedLifecycles()
	if lc["claude:sess123"].State != "accepted" {
		t.Fatalf("PromotedLifecycles() = %#v", lc)
	}
}

// Notes are read straight rather than through parseFiles, so the error was
// dropped on the floor: a notes file the process cannot read looked exactly
// like one with nothing in it, and the user's own decisions left the index
// without a word (#901).
func TestLoadNotesRecordsAFileItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.jsonl")
	if err := os.WriteFile(notes, []byte(`{"ts":"2026-08-01T10:00:00Z","project":"p","text":"a decision"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)

	DiagSnapshot() // clear anything a neighbour left behind
	if ss := LoadNotes(); len(ss) != 1 {
		t.Fatalf("readable notes loaded %d sessions, want 1", len(ss))
	}
	if _, failed := DiagSnapshot(); len(failed) != 0 {
		t.Errorf("a readable file was reported as failed: %v", failed)
	}

	// A machine that never wrote a note is not a machine with a problem: this
	// warned about the user's notes on every store that has none (#908).
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(dir, "never-written.jsonl"))
	if ss := LoadNotes(); len(ss) != 0 {
		t.Fatalf("a missing file yielded %d sessions", len(ss))
	}
	if _, failed := DiagSnapshot(); len(failed) != 0 {
		t.Errorf("a notes file that was never written was reported: %v", failed)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)

	if err := os.Chmod(notes, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(notes, 0o600) })
	if ss := LoadNotes(); len(ss) != 0 {
		t.Fatalf("an unreadable file yielded %d sessions", len(ss))
	}
	_, failed := DiagSnapshot()
	if len(failed) != 1 || failed[notes] == "" {
		t.Errorf("the unreadable notes file was not reported: %v", failed)
	}
}

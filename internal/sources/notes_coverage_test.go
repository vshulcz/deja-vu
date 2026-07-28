package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	for i := 0; i < 12; i++ {
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

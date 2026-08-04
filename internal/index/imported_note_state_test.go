package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The states of promoted notes live in notes.jsonl, which never travels, and
// import renames every session to imported-<hash>. So a decision retracted on
// one machine arrived on the other as an accepted line with the newest
// correction last (#975).
func TestImportedNoteKeepsItsStateAndOrder(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for _, r := range []SyncRecord{
		{Harness: "deja", SessionID: "deja-note-claude-a18", Project: "p", Role: "user", Text: "[accepted] asked: raise the pool cap · outcome: raised (from claude:a18, 2026-08-04)"},
		{Harness: "deja", SessionID: "deja-note-claude-a18", Project: "p", Role: "user", Text: "[rejected] made it worse (from claude:a18, 2026-08-04)"},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}

	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range metas {
		if m.Harness != "deja" {
			continue
		}
		found = true
		if m.OrigID != "deja-note-claude-a18" {
			t.Errorf("the imported note forgot what it was: OrigID = %q", m.OrigID)
		}
		if m.Lifecycle != "rejected" {
			t.Errorf("the retraction did not travel: lifecycle = %q", m.Lifecycle)
		}
		if !IsPromotedNote(m.Harness, m.ID, m.OrigID) {
			t.Errorf("an imported promoted note is not recognised as one: %s", m.ID)
		}
		s, ok, err := FindByPrefix(dir, m.ID)
		if err != nil || !ok {
			t.Fatalf("cannot read the imported note back: %v", err)
		}
		if len(s.Messages) != 2 {
			t.Fatalf("got %d messages, want 2", len(s.Messages))
		}
		if got := s.Messages[0].Text; got[:10] != "[rejected]" {
			t.Errorf("the note leads with %q, not the newest correction", got)
		}
	}
	if !found {
		t.Fatal("no imported note in the index")
	}
}

// The state prefix is the only copy that crosses a machine boundary, so it has
// to be read exactly: anything that is not one of deja's states is text, not a
// state (#975).
func TestNoteStateFromText(t *testing.T) {
	for _, tc := range []struct {
		in, state, note string
		ok              bool
	}{
		{"[rejected] made it worse", "rejected", "made it worse", true},
		{"[accepted] asked: … · outcome: …", "accepted", "asked: … · outcome: …", true},
		{"[superseded] replaced by the other one", "superseded", "replaced by the other one", true},
		{"[stale]", "stale", "", true},
		{"[approved-by-ceo] not a state deja knows", "", "", false},
		{"no bracket at all", "", "", false},
		{"[]", "", "", false},
		{"[unterminated", "", "", false},
	} {
		state, note, ok := noteStateFromText(tc.in)
		if state != tc.state || note != tc.note || ok != tc.ok {
			t.Errorf("noteStateFromText(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.in, state, note, ok, tc.state, tc.note, tc.ok)
		}
	}
}

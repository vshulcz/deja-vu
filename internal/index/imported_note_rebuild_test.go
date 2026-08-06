package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeNoteBatch drops a one-record sync batch holding a retracted note.
func writeNoteBatch(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{
		Harness: "deja", SessionID: "deja-note-claude-a18", Project: "p", Role: "user",
		Text: "[rejected] we no longer invalidate on write (from claude:a18, 2026-08-04)",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func importedNoteMeta(t *testing.T, dir string) SessionMeta {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.Harness == "deja" {
			return m
		}
	}
	t.Fatal("no imported note in the index")
	return SessionMeta{}
}

// A rebuild reloads imported sessions out of the index, since no source file
// holds them — and rebuilt the manifest row from scratch, dropping the state
// the import had recorded. The batch is deduped, so nothing brings it back
// (#1049).
func TestImportedNoteStateSurvivesRebuild(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	exp := filepath.Join(tmp, "transfer")
	writeNoteBatch(t, exp)
	dir := filepath.Join(tmp, "index.db")
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	meta := importedNoteMeta(t, dir)
	if meta.Lifecycle != "rejected" {
		t.Errorf("the rebuild dropped the retraction: lifecycle = %q", meta.Lifecycle)
	}
	if meta.OrigID != "deja-note-claude-a18" {
		t.Errorf("the rebuild dropped what the note was: OrigID = %q", meta.OrigID)
	}
	if !IsPromotedNote(meta.Harness, meta.ID, meta.OrigID) {
		t.Errorf("after the rebuild the note is no longer recognised as one: %s", meta.ID)
	}
}

// #984 began recording the state on the manifest row without bumping the index
// format, so a store that imported before it holds a row with none. The state
// is in the note text, and a rebuild re-derives it (#1049).
func TestImportedNoteStateDerivedFromTextWhenRowHasNone(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	exp := filepath.Join(tmp, "transfer")
	writeNoteBatch(t, exp)
	dir := filepath.Join(tmp, "index.db")
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	// What a pre-#984 import left behind: the records are there, the row knows
	// nothing about them.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for k, meta := range m.Sessions {
		meta.OrigID, meta.Lifecycle, meta.LifecycleNote, meta.LifecycleAt = "", "", "", ""
		m.Sessions[k] = meta
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got := importedNoteMeta(t, dir).Lifecycle; got != "" {
		t.Fatalf("the row still carries a state, the probe proves nothing: %q", got)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	meta := importedNoteMeta(t, dir)
	if meta.Lifecycle != "rejected" {
		t.Errorf("the retraction was not re-derived: lifecycle = %q", meta.Lifecycle)
	}
	if meta.LifecycleNote != "we no longer invalidate on write (from claude:a18, 2026-08-04)" {
		t.Errorf("the correction text was not re-derived: %q", meta.LifecycleNote)
	}
}

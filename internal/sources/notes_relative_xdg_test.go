package sources

import (
	"path/filepath"
	"testing"
)

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

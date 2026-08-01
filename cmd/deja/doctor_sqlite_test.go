package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Removing the sqlite3 CLI told the user their harness had changed its format
// and asked them to report it — two lines above deja naming the missing CLI
// itself (#792).
func TestDoctorSeparatesAMissingToolFromAChangedFormat(t *testing.T) {
	for _, name := range []string{"opencode", "cursor", "grok", "hermes", "goose"} {
		if !storeNeedsSQLite3(name) {
			t.Errorf("%s reads through the sqlite3 CLI but is not listed", name)
		}
	}
	for _, name := range []string{"claude", "codex", "gemini", "deja"} {
		if storeNeedsSQLite3(name) {
			t.Errorf("%s does not use sqlite3 but is listed as needing it", name)
		}
	}

	// The state itself, not just its wording: a parser that could not run must
	// not be filed as a store deja could not understand.
	dir := t.TempDir()
	db := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir) // no sqlite3 anywhere on it
	failed := func(string) ([]model.Session, error) {
		return nil, errors.New("exec: \"sqlite3\": executable file not found in $PATH")
	}
	store, _ := inspectDoctorStore(doctorStoreCheck{name: "opencode", paths: []string{db}, files: []string{db}, parse: failed})
	if store.State != "needs-sqlite3" {
		t.Errorf("state = %q, want needs-sqlite3", store.State)
	}
	// A harness that reads plain files is still an unreadable store.
	store, _ = inspectDoctorStore(doctorStoreCheck{name: "claude", paths: []string{db}, files: []string{db}, parse: failed})
	if store.State != "unreadable" {
		t.Errorf("state = %q, want unreadable", store.State)
	}

	var out strings.Builder
	printDoctorStoreWarnings(&out, []doctorStore{
		{Name: "opencode", State: "needs-sqlite3"},
		{Name: "cline", State: "unreadable"},
		{Name: "codex", State: "parsed-zero"},
		{Name: "claude", State: "ok"},
	})
	got := out.String()
	if !strings.Contains(got, "opencode store needs the sqlite3 CLI") {
		t.Errorf("missing tool not named:\n%s", got)
	}
	if strings.Contains(got, "opencode store cannot be read") {
		t.Errorf("a missing tool is still reported as a format change:\n%s", got)
	}
	if !strings.Contains(got, "cline store cannot be read") {
		t.Errorf("a genuinely unreadable store lost its warning:\n%s", got)
	}
	if !strings.Contains(got, "codex files found but newest parsed to zero") {
		t.Errorf("parsed-zero lost its warning:\n%s", got)
	}
	if strings.Contains(got, "claude") {
		t.Errorf("a healthy store was warned about:\n%s", got)
	}
}

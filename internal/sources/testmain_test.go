package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/testenv"
)

// TestMain gives the package a home of its own before any test runs. Only
// hermeticEnv moved the XDG_* roots, so a test that set HOME alone and saved a
// note still wrote deja/notes.jsonl into the XDG_DATA_HOME the shell exported
// (#4405). cmd/deja's TestMain does the same. Every other variable that can
// move a store is unset: a shell with HERMES_HOME exported, as Hermes's own
// terminal has, turned the Hermes tests into reads of the real store (#4178).
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "deja-sources-test-")
	if err != nil {
		panic(err)
	}
	testenv.Scrub(map[string]string{
		"HOME":            root,
		"USERPROFILE":     root,
		"APPDATA":         filepath.Join(root, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(root, "AppData", "Local"),
		"XDG_DATA_HOME":   "",
		"XDG_CONFIG_HOME": "",
		"XDG_CACHE_HOME":  "",
		"DEJA_NOTES_FILE": "",
	})
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// A note saved by a test that moves nothing lands under the package's home.
func TestPackageNotesStayUnderTheTempDir(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	notes, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(NotesFile())))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(notes, tmp) {
		t.Errorf("NotesFile = %s, want it under %s whatever the shell exports", NotesFile(), tmp)
	}
}

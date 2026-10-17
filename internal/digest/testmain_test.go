package digest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/testenv"
)

// TestMain gives the package a home of its own before any test runs.
// ProjectNameCandidates reads the project alias file under the config home, so
// a contributor's own ~/.config/deja/projects could rename the temp directories
// these tests stand in.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "deja-digest-test-")
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
	})
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

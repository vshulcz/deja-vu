package index

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The suite writes and reads promoted notes, and sources.NotesFile() resolves
// through XDG_DATA_HOME and APPDATA before it ever reaches HOME. When TestMain
// left those pointing at the developer's real profile, the suite appended
// fixtures to their store and then failed reading them back as leaked `deja`
// sessions (#1141). This pins the store under the temp root so that drift
// fails here, naming the real path, instead of eleven arithmetic tests.
func TestNotesFileStaysUnderTheTestRoot(t *testing.T) {
	nf := sources.NotesFile()
	home := os.Getenv("HOME")
	appdata := os.Getenv("APPDATA")
	if (home != "" && strings.HasPrefix(nf, home)) || (appdata != "" && strings.HasPrefix(nf, appdata)) {
		return
	}
	t.Fatalf("notes store resolved outside the test root: %q — TestMain is leaking to the developer's real store (HOME=%q APPDATA=%q); blank XDG_DATA_HOME/DEJA_NOTES_FILE and point APPDATA at the temp root (#1141)", nf, home, appdata)
}

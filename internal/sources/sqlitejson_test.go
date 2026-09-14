package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sqlite3 shell's -json output mode is quadratic in the characters it
// escapes, and a transcript is mostly quotes and backslashes: one long tool
// output was enough to make `deja index` look like it had hung (#3553). Rows
// come back through json_object instead, and this pins it — a reader that
// reaches for -json again reintroduces the hang on the one store big enough to
// show it, which is never the one in a fixture.
func TestNoReaderAsksSqlite3ToFormatTheJSON(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"-json"`) {
			t.Errorf("%s passes -json to sqlite3: build the row with json_object instead, see sqliteRows", name)
		}
	}
}

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kilo and ZCode each keep half their history in a SQLite database, and reading
// it needs the sqlite3 CLI. Those two rows named the database and said nothing
// about the tool, so on a machine without sqlite3 the sessions were missing
// from recall while the row reported the store present (#3679).
func TestTheTwoDatabaseHalvesNameTheirPrereq(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_KILO_DB", filepath.Join(home, "kilo.db"))
	t.Setenv("DEJA_ZCODE_DB", filepath.Join(home, "db.sqlite"))
	t.Setenv("DEJA_KILO_ROOTS", filepath.Join(home, "absent"))
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent-too"))
	for _, db := range []string{filepath.Join(home, "kilo.db"), filepath.Join(home, "db.sqlite")} {
		if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// No sqlite3 on the PATH is the state the note exists for.
	t.Setenv("PATH", filepath.Join(home, "empty-bin"))
	var out bytes.Buffer
	doctorHarnesses(&out, t.TempDir())
	for _, name := range []string{"kilocode", "zcode"} {
		row := ""
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), name+" ") {
				row = line
				break
			}
		}
		if row == "" {
			t.Errorf("no %s row:\n%s", name, out.String())
			continue
		}
		if !strings.Contains(row, "CLI store present") {
			t.Errorf("%s: row does not name the database: %s", name, row)
		}
		if !strings.Contains(row, "sqlite3 CLI is missing") {
			t.Errorf("%s: row promises a store it cannot read: %s", name, row)
		}
	}
}

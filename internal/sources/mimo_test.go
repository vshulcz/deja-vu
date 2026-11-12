package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildSQLiteFixture(t *testing.T, sqlPath, dbPath string) {
	t.Helper()
	b, err := os.ReadFile(sqlPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(string(b))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}
}

func TestMimoParseAndSince(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "mimocode.db")
	buildSQLiteFixture(t, filepath.Join("..", "..", "fixtures", "registry", "mimo", "mimo.sql"), db)
	t.Setenv("DEJA_MIMO_DB", db)

	if got := MimoDB(); got != db {
		t.Fatalf("MimoDB() = %q, want %q", got, db)
	}
	ss := LoadMimo()
	if len(ss) != 1 {
		t.Fatalf("LoadMimo() got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "mimo" || s.ID != "ses_mimo_01" {
		t.Fatalf("got harness=%q id=%q", s.Harness, s.ID)
	}
	if s.Title != "Fix token refresh race in auth middleware" {
		t.Fatalf("Title = %q", s.Title)
	}
	if len(s.Messages) < 2 {
		t.Fatalf("Messages = %d, want >= 2", len(s.Messages))
	}

	since, err := ParseMimoDBSince(db, time.UnixMilli(1744000000000))
	if err != nil || len(since) != 1 {
		t.Fatalf("ParseMimoDBSince: len=%d err=%v", len(since), err)
	}
	zero, err := ParseMimoDBSince(db, time.Time{})
	if err != nil || len(zero) != 1 {
		t.Fatalf("ParseMimoDBSince zero: len=%d err=%v", len(zero), err)
	}
	after, err := ParseMimoDBSince(db, time.UnixMilli(1844000000000))
	if err != nil || len(after) != 0 {
		t.Fatalf("ParseMimoDBSince future: len=%d err=%v", len(after), err)
	}
}

func TestMimoPathsAndSessionFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_MIMO_CONFIG_DIR", "")
	t.Setenv("MIMOCODE_CONFIG_DIR", "")
	t.Setenv("MIMOCODE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_MIMO_DB", "")
	t.Setenv("MIMOCODE_DB", "")

	if got, want := MimoConfigDir(), filepath.Join(home, ".config", "mimocode"); got != want {
		t.Fatalf("MimoConfigDir() default = %q, want %q", got, want)
	}
	xdgCfg := filepath.Join(home, "xdg-cfg")
	t.Setenv("XDG_CONFIG_HOME", xdgCfg)
	if got, want := MimoConfigDir(), filepath.Join(xdgCfg, "mimocode"); got != want {
		t.Fatalf("MimoConfigDir() XDG = %q, want %q", got, want)
	}
	t.Setenv("MIMOCODE_HOME", "~/mimo-home")
	if got, want := MimoConfigDir(), filepath.Join(home, "mimo-home", "config"); got != want {
		t.Fatalf("MimoConfigDir() MIMOCODE_HOME = %q, want %q", got, want)
	}
	t.Setenv("MIMOCODE_CONFIG_DIR", "~/mimo-cfg")
	if got, want := MimoConfigDir(), filepath.Join(home, "mimo-cfg"); got != want {
		t.Fatalf("MimoConfigDir() MIMOCODE_CONFIG_DIR = %q, want %q", got, want)
	}
	t.Setenv("DEJA_MIMO_CONFIG_DIR", "~/deja-mimo-cfg")
	if got, want := MimoConfigDir(), filepath.Join(home, "deja-mimo-cfg"); got != want {
		t.Fatalf("MimoConfigDir() DEJA_MIMO_CONFIG_DIR = %q, want %q", got, want)
	}

	t.Setenv("MIMOCODE_HOME", "")
	defDB := filepath.Join(home, ".local", "share", "mimocode", "mimocode.db")
	if got := MimoDB(); got != defDB {
		t.Fatalf("MimoDB() default = %q, want %q", got, defDB)
	}

	xdgData := filepath.Join(home, "xdg-data")
	t.Setenv("XDG_DATA_HOME", xdgData)
	xdgDB := filepath.Join(xdgData, "mimocode", "mimocode.db")
	if got := MimoDB(); got != xdgDB {
		t.Fatalf("MimoDB() XDG neither exists = %q, want %q", got, xdgDB)
	}
	if err := os.MkdirAll(filepath.Dir(defDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defDB, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MimoDB(); got != defDB {
		t.Fatalf("MimoDB() XDG fallback to def = %q, want %q", got, defDB)
	}
	if err := os.MkdirAll(filepath.Dir(xdgDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xdgDB, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MimoDB(); got != xdgDB {
		t.Fatalf("MimoDB() XDG exists = %q, want %q", got, xdgDB)
	}

	mimoHome := filepath.Join(home, "custom-home")
	t.Setenv("MIMOCODE_HOME", mimoHome)
	if got, want := MimoDB(), filepath.Join(mimoHome, "data", "mimocode.db"); got != want {
		t.Fatalf("MimoDB() MIMOCODE_HOME = %q, want %q", got, want)
	}
	t.Setenv("MIMOCODE_DB", ":memory:")
	if got, want := MimoDB(), filepath.Join(mimoHome, "data", "mimocode.db"); got != want {
		t.Fatalf("MimoDB() :memory: ignored = %q, want %q", got, want)
	}
	t.Setenv("MIMOCODE_DB", "alt.db")
	if got, want := MimoDB(), filepath.Join(mimoHome, "data", "alt.db"); got != want {
		t.Fatalf("MimoDB() relative = %q, want %q", got, want)
	}
	absDB := filepath.Join(t.TempDir(), "abs.db")
	t.Setenv("MIMOCODE_DB", absDB)
	if got := MimoDB(); got != absDB {
		t.Fatalf("MimoDB() abs = %q, want %q", got, absDB)
	}

	// MimoSessionFiles: missing, 0-byte, non-empty.
	missingDB := filepath.Join(t.TempDir(), "missing.db")
	t.Setenv("DEJA_MIMO_DB", missingDB)
	if got := MimoSessionFiles(); got != nil {
		t.Fatalf("MimoSessionFiles() missing = %v, want nil", got)
	}
	emptyDB := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(emptyDB, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_MIMO_DB", emptyDB)
	if got := MimoSessionFiles(); got != nil {
		t.Fatalf("MimoSessionFiles() empty = %v, want nil", got)
	}
	nonEmptyDB := filepath.Join(t.TempDir(), "ok.db")
	if err := os.WriteFile(nonEmptyDB, []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_MIMO_DB", nonEmptyDB)
	if got := MimoSessionFiles(); len(got) != 1 || got[0] != nonEmptyDB {
		t.Fatalf("MimoSessionFiles() non-empty = %v, want [%q]", got, nonEmptyDB)
	}
}

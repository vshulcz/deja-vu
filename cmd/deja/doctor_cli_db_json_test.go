package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// Kilo CLI and ZCode keep their sessions in a database beside the files the
// JSON row was built from, and the row never looked at it: with the CLI alone,
// `doctor --json` said "missing" with no paths about a store the text row
// called found and the index held whole (#4397).
func TestDoctorJSONCountsTheCLIDatabase(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_KILO_ROOTS", filepath.Join(home, "absent"))
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent-too"))
	dbs := map[string]string{
		"kilocode": filepath.Join(home, "kilo.db"),
		"zcode":    filepath.Join(home, "db.sqlite"),
	}
	t.Setenv("DEJA_KILO_DB", dbs["kilocode"])
	t.Setenv("DEJA_ZCODE_DB", dbs["zcode"])
	const seed = `
create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/proj',1767322800000,1767322800000);
insert into message values ('m1','s1','{"role":"user","time":{"created":1767322800000}}',1767322800000);
insert into part values ('p1','m1',json_object('type','text','text','fix the retry loop','time',json_object('start',1767322800000)));
`
	for _, db := range dbs {
		if out, err := exec.Command("sqlite3", db, seed).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3 seed: %v %s", err, out)
		}
	}
	if _, err := os.Stat(dbs["kilocode"]); err != nil {
		t.Fatal(err)
	}

	report := collectDoctorReport(nil, t.TempDir())
	for name, db := range dbs {
		var row *doctorStore
		for i := range report.Stores {
			if report.Stores[i].Name == name {
				row = &report.Stores[i]
			}
		}
		if row == nil {
			t.Errorf("no %s row", name)
			continue
		}
		if row.State != "ok" {
			t.Errorf("%s: state %q about a database holding a session, want ok", name, row.State)
		}
		if !slices.Contains(row.Paths, db) {
			t.Errorf("%s: paths %v do not name %s", name, row.Paths, db)
		}
		if row.Files != 1 {
			t.Errorf("%s: files = %d, want 1", name, row.Files)
		}
	}
}

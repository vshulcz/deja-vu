package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A second install names the file to import, not the skill: the import is
// what someone re-running install has usually not done yet (#4343).
func TestInstallCherryStudioAgainNamesTheImportFile(t *testing.T) {
	hermeticEnv(t)
	if _, err := installCherryStudio("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	again, err := installCherryStudio("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Path != cherryStudioImportPath() {
		t.Fatalf("second install names %s, want the import file %s", again.Path, cherryStudioImportPath())
	}
	if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}

	// And a run where only the skill changed still leads with the import file.
	if err := os.Remove(sharedSkillPath()); err != nil {
		t.Fatal(err)
	}
	third, err := installCherryStudio("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if third.Path != cherryStudioImportPath() {
		t.Fatalf("install after the skill went names %s, want the import file", third.Path)
	}
	if !strings.Contains(third.Note, "SKILL.md") {
		t.Errorf("the rewritten skill went unmentioned: %q", third.Note)
	}
}

// Uninstall takes the skill install wrote, unless another installed harness
// still reads that shared file (#4345).
func TestUninstallCherryStudioTakesTheSkillItWrote(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := runInstall(dir, []string{"cherrystudio", "--no-index"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedSkillPath()); err != nil {
		t.Fatalf("install wrote no skill: %v", err)
	}
	if err := runInstall(dir, []string{"cherrystudio"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedSkillPath()); !os.IsNotExist(err) {
		t.Fatalf("the skill survived uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir(), ".agents")); !os.IsNotExist(err) {
		t.Errorf("uninstall left the directories install made: %v", err)
	}

	// Zed reads the same file: with it still installed the skill stays, and
	// the other way round too.
	if err := runInstall(dir, []string{"cherrystudio", "zed", "--no-index"}, false); err != nil {
		t.Fatal(err)
	}
	if err := runInstall(dir, []string{"cherrystudio"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedSkillPath()); err != nil {
		t.Fatalf("uninstalling cherrystudio took the skill zed still reads: %v", err)
	}
	if err := runInstall(dir, []string{"cherrystudio", "--no-index"}, false); err != nil {
		t.Fatal(err)
	}
	if err := runInstall(dir, []string{"zed"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedSkillPath()); err != nil {
		t.Fatalf("uninstalling zed took the skill cherrystudio still reads: %v", err)
	}
}

// grok writes the shared skill too, for Grok Build, which reads nothing else of
// deja's. Cherry Studio leaving must not take it; kiro and cline read their
// own files, so with only them left the skill goes.
func TestUninstallCherryStudioKeepsTheSkillGrokReads(t *testing.T) {
	for other, keep := range map[string]bool{"grok": true, "kiro": false, "cline": false} {
		t.Run(other, func(t *testing.T) {
			hermeticEnv(t)
			dir := index.DefaultDir()
			if err := runInstall(dir, []string{"cherrystudio", other, "--no-index"}, false); err != nil {
				t.Fatal(err)
			}
			if err := runInstall(dir, []string{"cherrystudio"}, true); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(sharedSkillPath())
			if got := err == nil; got != keep {
				t.Errorf("with %s installed, the shared skill kept = %v, want %v", other, got, keep)
			}
		})
	}
}

// cherryStudioDBFixture writes the app's MCP table with the given rows.
func cherryStudioDBFixture(t *testing.T, rows ...[3]string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not on PATH")
	}
	dbs := sources.CherryStudioDatabases()
	if len(dbs) == 0 {
		t.Fatal("no candidate database path")
	}
	db := dbs[0]
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "create table mcp_server (id text primary key, name text not null, command text, args text, is_active integer not null default 1);"
	for i, r := range rows {
		sql += "insert into mcp_server (id, name, command, args) values ('" + string(rune('a'+i)) + "', '" + r[0] + "', '" + r[1] + "', '" + r[2] + "');"
	}
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	return db
}

func cherryMCPRow(t *testing.T) doctorMCPStatus {
	t.Helper()
	for _, r := range collectDoctorMCP() {
		if r.Name == "cherrystudio" {
			return r
		}
	}
	t.Fatal("no cherrystudio row")
	return doctorMCPStatus{}
}

func cherryMCPText(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	doctorMCP(&buf)
	var keep []string
	on := false
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "  cherrystudio ") {
			on = true
		} else if on && !strings.HasPrefix(line, "               ") {
			on = false
		}
		if on {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

// doctor's "wired" for Cherry Studio is about the app, which keeps its servers
// in Data/cherrystudio.sqlite, not about the import file deja writes (#4344).
func TestDoctorCherryStudioReadsTheAppsServers(t *testing.T) {
	hermeticEnv(t)
	exe := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installCherryStudio(exe, false); err != nil {
		t.Fatal(err)
	}

	// The import file is there and the app has never seen it.
	db := cherryStudioDBFixture(t, [3]string{"other-server", "/usr/local/bin/other-mcp", `[]`})
	row := cherryMCPRow(t)
	if row.State != "not-imported" {
		t.Fatalf("app without a deja server: state %q, want not-imported", row.State)
	}
	if row.Path != cherryStudioImportPath() {
		t.Errorf("not-imported row names %s, want the file to import", row.Path)
	}
	if text := cherryMCPText(t); !strings.Contains(text, "not imported") || !strings.Contains(text, "Import from JSON") {
		t.Errorf("text row:\n%s", text)
	}

	// Imported: the app's row runs deja mcp.
	_ = os.Remove(db)
	db = cherryStudioDBFixture(t,
		[3]string{"other-server", "/usr/local/bin/other-mcp", `[]`},
		[3]string{"deja", exe, `["mcp"]`})
	if row := cherryMCPRow(t); row.State != "wired" || row.BinaryMissing {
		t.Fatalf("imported server: %+v, want wired", row)
	}
	if text := cherryMCPText(t); strings.Contains(text, "no config file to write") {
		t.Errorf("the caveat about an unread file stayed once the app was read:\n%s", text)
	}

	// The app's server names a binary that moved: rewriting the import file
	// does not reach the app, so the remedy has to say re-import.
	_ = os.Remove(db)
	gone := filepath.Join(t.TempDir(), "moved", "deja")
	cherryStudioDBFixture(t, [3]string{"deja", gone, `["mcp"]`})
	row = cherryMCPRow(t)
	if row.State != "wired" || !row.BinaryMissing {
		t.Fatalf("moved binary: %+v, want wired with binary_missing", row)
	}
	if text := cherryMCPText(t); !strings.Contains(text, gone) || !strings.Contains(text, "re-import") {
		t.Errorf("moved-binary remedy:\n%s", text)
	}
}

// Without the app's database, or without sqlite3 to read it, doctor falls back
// to the import file and says so in --json too, not only in the text (#4344).
func TestDoctorCherryStudioWithoutTheAppsDatabase(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("PATH", t.TempDir())
	sources.ResetSQLite3Probe()
	t.Cleanup(sources.ResetSQLite3Probe)
	if _, err := installCherryStudio("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	db := sources.CherryStudioDatabases()[0]
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	row := cherryMCPRow(t)
	if row.State != "wired" {
		t.Fatalf("state %q, want the import file's wired", row.State)
	}
	b, _ := json.Marshal(row)
	if !strings.Contains(string(b), "Import from JSON") {
		t.Errorf("--json row carries no caveat: %s", b)
	}
}

// Each server in Cherry Studio has a switch, and the app starts only the ones
// that are on. A deja server imported and left off is not wired (#4344).
func TestDoctorCherryStudioSaysWhenDejasServerIsOff(t *testing.T) {
	hermeticEnv(t)
	exe := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installCherryStudio(exe, false); err != nil {
		t.Fatal(err)
	}
	db := cherryStudioDBFixture(t, [3]string{"deja", exe, `["mcp"]`})
	if out, err := exec.Command("sqlite3", db, "update mcp_server set is_active = 0;").CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	if row := cherryMCPRow(t); row.State != "disabled" || row.Path != db {
		t.Fatalf("switched-off server: %+v, want disabled at the database", row)
	}
	if text := cherryMCPText(t); !strings.Contains(text, "disabled") || !strings.Contains(text, "turn it on") {
		t.Errorf("text row:\n%s", text)
	}
}

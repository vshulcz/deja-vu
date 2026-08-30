package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The writers ask what indent a block uses; these two readers assumed two
// spaces. Uninstall then left hermes wired, and doctor called a goose install
// it had just done unwired (#2727).
func TestHermesUninstallTakesItsBlockOutAtAnyIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"one space", " "},
		{"two spaces", "  "},
		{"four spaces", "    "},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := "mcp_servers:\n" +
				c.indent + "deja:\n" +
				c.indent + c.indent + "cmd: /old/deja\n" +
				c.indent + "mine:\n" +
				c.indent + c.indent + "cmd: /usr/bin/mine\n"
			got := removeHermesMCPBlock(in)
			if strings.Contains(got, "deja:") {
				t.Errorf("deja's own block survived the uninstall:\n%s", got)
			}
			if !strings.Contains(got, "mine:") || !strings.Contains(got, "/usr/bin/mine") {
				t.Errorf("the reader's own server did not survive:\n%s", got)
			}
			if !strings.Contains(got, "mcp_servers:") {
				t.Errorf("the key went with it:\n%s", got)
			}
		})
	}
}

// And what doctor says about a config deja itself wrote.
func TestDoctorReadsAWiredBlockAtAnyIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"one space", " "},
		{"two spaces", "  "},
		{"four spaces", "    "},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			goose := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(goose, []byte("extensions:\n"+
				c.indent+"deja:\n"+c.indent+c.indent+"cmd: /opt/deja\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if !doctorGooseWired(goose) {
				t.Errorf("doctor calls a wired goose unwired at this indent")
			}
			hermes := filepath.Join(dir, "hermes.yaml")
			if err := os.WriteFile(hermes, []byte("mcp_servers:\n"+
				c.indent+"deja:\n"+c.indent+c.indent+"cmd: /opt/deja\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if !doctorHermesWired(hermes) {
				t.Errorf("doctor calls a wired hermes unwired at this indent")
			}
		})
	}
}

// A key at the top level is not an entry under anything, and a server someone
// else named is not deja.
func TestDoctorDoesNotReadATopLevelKeyAsAWiredServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("deja:\n  note: not a server block\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if doctorGooseWired(path) {
		t.Error("a top-level key was read as a wired extension")
	}
	if err := os.WriteFile(path, []byte("extensions:\n  dejavu:\n    cmd: /opt/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if doctorGooseWired(path) {
		t.Error("another name that starts the same was read as deja")
	}
}

// The blank line after deja's block belongs to the file, not to the block:
// swallowing it made a second install differ from the first, which is what
// TestInstallIsIdempotent is for (#2727).
func TestHermesUninstallLeavesTheFileAroundItAlone(t *testing.T) {
	in := "mcp_servers:\n" +
		"  mine:\n    cmd: /usr/bin/mine\n" +
		"\n" +
		"  deja:\n    cmd: /old/deja\n" +
		"\n" +
		"plugins:\n  enabled:\n    - other\n"
	want := "mcp_servers:\n" +
		"  mine:\n    cmd: /usr/bin/mine\n" +
		"\n" +
		"\n" +
		"plugins:\n  enabled:\n    - other\n"
	if got := removeHermesMCPBlock(in); got != want {
		t.Errorf("the file around deja's block changed:\nwant:\n%q\ngot:\n%q", want, got)
	}
}

// A comment inside deja's own block goes with it; one belonging to what
// follows does not.
func TestHermesUninstallTakesTheCommentsInsideItsBlock(t *testing.T) {
	in := "mcp_servers:\n" +
		"  deja:\n" +
		"    # written by deja install\n" +
		"    cmd: /old/deja\n" +
		"  # the reader's note about their own server\n" +
		"  mine:\n    cmd: /usr/bin/mine\n"
	got := removeHermesMCPBlock(in)
	if strings.Contains(got, "written by deja install") {
		t.Errorf("a comment inside deja's block stayed:\n%s", got)
	}
	if !strings.Contains(got, "the reader's note") {
		t.Errorf("the reader's own comment went with it:\n%s", got)
	}
}

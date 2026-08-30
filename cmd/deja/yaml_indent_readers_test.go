package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
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

// deja's entry is a direct child of the block it belongs to. Reading `deja:`
// at any depth emptied a server of somebody else's whose env holds a key by
// that name — deja breaking a config while claiming to remove itself (#2730).
func TestHermesUninstallLeavesAForeignKeyByThatNameAlone(t *testing.T) {
	in := "mcp_servers:\n" +
		"  other:\n    cmd: /o\n    env:\n      deja:\n        HOME: /x\n    enabled: true\n" +
		"  third:\n    cmd: /t\n"
	if got := removeHermesMCPBlock(in); got != in {
		t.Errorf("a config with no deja server was rewritten:\nwant:\n%s\ngot:\n%s", in, got)
	}
}

// And what doctor calls wired: the same key, in the same places.
func TestDoctorDoesNotReadAMentionAsAWiredServer(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"under another server's env", "mcp_servers:\n  other:\n    env:\n      deja:\n        x: 1\n"},
		{"in an example under notes", "mcp_servers:\n  a:\n    cmd: /a\nnotes:\n  examples:\n    deja:\n      how: to wire it\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if doctorHermesWired(path) {
				t.Errorf("hermes was called wired by a mention:\n%s", c.body)
			}
			if doctorGooseWired(path) {
				t.Errorf("goose was called wired by a mention:\n%s", c.body)
			}
		})
	}
}

// The writer wrote its entry at two spaces whatever the file used, so on a
// config indented at three or four deja's entry landed shallower than its
// siblings — and the uninstall then took those siblings with it (#2730).
func TestHermesRoundTripsAtEveryIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"one space", " "},
		{"two spaces", "  "},
		{"three spaces", "   "},
		{"four spaces", "    "},
	} {
		t.Run(c.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("DEJA_HERMES_ROOT", filepath.Join(tmp, "hermes"))
			path := filepath.Join(sources.HermesHome(), "config.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			before := "# hermes config\nmcp_servers:\n" +
				c.indent + "before:\n" + c.indent + c.indent + "cmd: /b\n" +
				c.indent + "after:\n" + c.indent + c.indent + "cmd: /a\n"
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installHermesMCP("/opt/deja", false); err != nil {
				t.Fatal(err)
			}
			wired, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"before:", "after:", "deja:", "/opt/deja"} {
				if !strings.Contains(string(wired), want) {
					t.Fatalf("%q is missing after the install:\n%s", want, wired)
				}
			}
			if !strings.Contains(string(wired), c.indent+"deja:") {
				t.Errorf("deja's entry was not written at the block's indent:\n%s", wired)
			}

			if _, err := installHermesMCP("/opt/deja", true); err != nil {
				t.Fatal(err)
			}
			back, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(back) != before {
				t.Errorf("the config did not come back:\nwas:\n%q\nnow:\n%q", before, back)
			}
		})
	}
}

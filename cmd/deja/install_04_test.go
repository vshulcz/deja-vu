package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// install adopts a codex entry that already runs deja under another name rather
// than adding a second one. The note saying so named the key through %q over a
// key tomlMCPHeader had already quoted, so it read "\"deja.old\"", and it was
// set without looking at what the write did, so every later run repeated it
// next to "unchanged".
func TestInstallTOMLAdoptNoteNamesTheKeyAsWrittenAndOnlyWhenSomethingChanged(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	// The command's basename is deja, so the second run recognises the entry it
	// wrote; the test binary is deja.test and would not.
	seed := "[mcp_servers.\"deja.old\"]\ncommand = \"/usr/local/bin/deja\"\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	block := "[mcp_servers.deja]\ncommand = \"/usr/local/bin/deja\"\nargs = [\"mcp\", \"--new\"]\n"

	first, err := installTOML(path, block, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Action == "unchanged" {
		t.Fatalf("the first run changed the file but reported %q", first.Action)
	}
	wantNote := `updated "deja.old", which already ran deja, instead of adding a second entry`
	if first.Note != wantNote {
		t.Errorf("note on the run that adopted the entry:\n got %q\nwant %q", first.Note, wantNote)
	}

	second, err := installTOML(path, block, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Action != "unchanged" {
		t.Fatalf("the second run should change nothing, got %q", second.Action)
	}
	if second.Note != "" {
		t.Errorf("a run that changed nothing still said %q", second.Note)
	}
}

// `read: [a.md, b.md]` is a list, and the writer took it for the scalar form —
// so it wrote `- [a.md, b.md]` as one entry and aider looked for a file with
// that name. The reader's two files were gone from its view and the install
// reported success (#3197).
func TestAiderKeepsAFlowListOfReadFiles(t *testing.T) {
	const ctx = "/home/me/.config/deja/aider-context.md"

	got, err := addAiderReadEntry("read: [CONVENTIONS.md, notes.md]\n", ctx)
	if err != nil {
		t.Fatalf("a plain flow list was refused: %v", err)
	}
	for _, want := range []string{"  - CONVENTIONS.md\n", "  - notes.md\n", "  - " + ctx + "\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the rewritten config lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- [") {
		t.Errorf("the list is still nested inside one entry:\n%s", got)
	}

	// Quotes, spacing and an empty list are the same list.
	for _, in := range []string{
		`read: ["a b.md", 'c.md']`,
		"read: [a.md,b.md]",
		"read: [ a.md , b.md ]",
	} {
		got2, aderr := addAiderReadEntry(in+"\n", ctx)
		if aderr != nil {
			t.Errorf("%s was refused: %v", in, aderr)
			continue
		}
		if strings.Contains(got2, "- [") || !strings.Contains(got2, "  - "+ctx) {
			t.Errorf("%s came out wrong:\n%s", in, got2)
		}
	}

	// A shape it cannot take apart is refused, not rewritten. Aider's config is
	// the reader's file, and a wrong rewrite is worse than no install.
	for _, in := range []string{
		"read: [a.md, [b.md, c.md]]",
		`read: [{file: a.md}]`,
		`read: ["unclosed, b.md]`,
		"read: [a.md, ]",
		// A flow list YAML would continue on the next line: there is no
		// closing bracket on this one, so what the list holds is not here.
		"read: [a.md, b.md",
	} {
		if _, aerr := addAiderReadEntry(in+"\n", ctx); aerr == nil {
			t.Errorf("%s was rewritten instead of refused", in)
		}
	}

	// The shapes that already worked still do.
	got, err = addAiderReadEntry("read: CONVENTIONS.md\n", ctx)
	if err != nil || !strings.Contains(got, "  - CONVENTIONS.md\n") || !strings.Contains(got, "  - "+ctx) {
		t.Fatalf("the scalar form broke: %v\n%s", err, got)
	}
	got, err = addAiderReadEntry("read:\n  - CONVENTIONS.md\n", ctx)
	if err != nil || !strings.Contains(got, "  - CONVENTIONS.md\n") || !strings.Contains(got, "  - "+ctx) {
		t.Fatalf("the block form broke: %v\n%s", err, got)
	}
	got, err = addAiderReadEntry("", ctx)
	if err != nil || !strings.HasPrefix(got, "read:\n") {
		t.Fatalf("an empty config broke: %v\n%s", err, got)
	}
}

// install then uninstall gives the reader's ~/.aider.conf.yml back byte for
// byte, whatever form their read: was in. A CRLF file was not matched as a
// block list, so install wrote a second read: key and aider, which keeps the
// last, dropped the reader's files — and uninstall left a bare `read:` behind
// (#4330). A scalar or flow read: came back as a block list, and a file that
// held only `read: []` was deleted (#4331).
func TestAiderConfigRoundTrips(t *testing.T) {
	for _, c := range []struct{ name, conf string }{
		{"crlf block", "model: x\r\nread:\r\n  - C.md\r\n"},
		{"scalar", "# my settings\nmodel: gpt-4o\nread: CONVENTIONS.md\ndark-mode: true\n"},
		{"flow", "model: gpt-4o\nread: [CONVENTIONS.md, \"a b.md\"]\n"},
		{"empty flow", "read: []\n"},
		{"crlf scalar", "model: x\r\nread: C.md\r\n"},
		{"block, key comment", "read:  # mine\n  - C.md\nmodel: x\n"},
		{"block, comment under key", "read:\n  # the house rules\n  - C.md\n"},
		{"block at column 0", "read:\n- C.md\n- D.md\nmodel: x\n"},
		{"lf block", "model: x\nread:\n  - C.md\n"},
		{"null", "model: x\nread: ~\n"},
		{"quoted flow", "read: [\"#notes.md\", 'k: v.md']\n"},
		// No final newline: the scalar was never found, install said
		// unchanged and wired nothing (#5204).
		{"scalar, no final newline", "model: x\nread: C.md"},
		{"block, no final newline", "model: x\nread:\n  - C.md"},
		// The reader's own empty key stays where it was (#5205).
		{"bare key", "read:\nmodel: x\n"},
		{"bare key, comment", "read:  # none yet\nmodel: x\n"},
		{"bare key last, no final newline", "model: x\nread:"},
		{"empty item (#5206)", "read:\n  -\nmodel: x\n"},
		{"empty item at column 0", "read:\n-\nmodel: x\n"},
		// The reader's own file, not deja's (#5209).
		{"own file named like deja's", "read:\n  - my-aider-context.md\nmodel: x\n"},
		{"own file with deja's name", "read:\n  - docs/aider-context.md\n  - aider-context.md\nmodel: x\n"},
		{"deja's name under another key", "files:\n  - x/deja/aider-context.md\nread:\n  - C.md\n"},
		{"blank line before column-0 items", "read:\n\n- C.md\nmodel: x\n"},
		{"spaces-only line before column-0 items", "read:\n  \n- C.md\nmodel: x\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
			path := filepath.Join(home, ".aider.conf.yml")
			if err := os.WriteFile(path, []byte(c.conf), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installAider("/bin/echo", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			got := aiderConf(t, home)
			lf := strings.ReplaceAll(got, "\r\n", "\n")
			if n := strings.Count("\n"+lf, "\nread:"); n != 1 {
				t.Fatalf("read: written %d times, aider reads only the last:\n%q", n, got)
			}
			if !confReadsDejaContext(lf) {
				t.Fatalf("the context file is not under read:\n%q", got)
			}
			for _, f := range []string{"C.md", "CONVENTIONS.md", "a b.md", "D.md"} {
				if strings.Contains(c.conf, f) && !strings.Contains(got, f) {
					t.Fatalf("install dropped %s:\n%q", f, got)
				}
			}
			if strings.Contains(c.conf, "\r\n") && strings.Count(got, "\n") != strings.Count(got, "\r\n") {
				t.Errorf("a CRLF config came back with mixed endings:\n%q", got)
			}
			// A second install is a no-op.
			res, err := installAider("/bin/echo", false)
			if err != nil {
				t.Fatalf("second install: %v", err)
			}
			if again := aiderConf(t, home); again != got || res.Action != "unchanged" {
				t.Errorf("a second install changed the file (%s):\n%q\nthen\n%q", res.Action, got, again)
			}
			if _, ierr := installAider("/bin/echo", true); ierr != nil {
				t.Fatalf("uninstall: %v", ierr)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("uninstall removed a config the reader wrote: %v", err)
			}
			if string(b) != c.conf {
				t.Errorf("not the file it was:\n%q\nwant\n%q", b, c.conf)
			}
		})
	}
}

// confReadsDejaContext reports whether a top-level read: list names deja's
// context file.
func confReadsDejaContext(s string) bool {
	inRead := false
	for l := range strings.SplitSeq(s, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if l[0] != ' ' && !strings.HasPrefix(t, "- ") {
			inRead = strings.HasPrefix(l, "read:")
			continue
		}
		if inRead && strings.Contains(t, "aider-context.md") {
			return true
		}
	}
	return false
}

// Two read: keys are the reader's to settle: aider keeps the last, and joining
// either one hides a file somewhere.
func TestAiderRefusesTwoReadKeys(t *testing.T) {
	for _, conf := range []string{
		"read: a.md\nread:\n  - b.md\n",
		"read:\n  - a.md\nmodel: x\nread:\n  - b.md\n",
	} {
		if _, err := addAiderReadEntry(conf, "/x/aider-context.md"); err == nil {
			t.Errorf("%q was joined instead of refused", conf)
		}
	}
}

// A flow item keeps its quotes when install makes the list a block list:
// unquoted, "#notes.md" reads as a comment and 'k: v.md' as a mapping, and
// aider loses both files.
func TestAiderFlowListKeepsQuotes(t *testing.T) {
	if got, _ := addAiderReadEntry("read: ~\n", "/x/aider-context.md"); strings.Contains(got, "- ~") {
		t.Errorf("a null read: became a null item:\n%s", got)
	}
	got, err := addAiderReadEntry("read: [\"#notes.md\", 'k: v.md', plain.md]\n", "/x/aider-context.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  - \"#notes.md\"\n", "  - 'k: v.md'\n", "  - plain.md\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
}

// A block scalar or an alias under read: is not a file name to promote: the
// indicator became a list item and the text under it was left dangling below
// deja's entry.
func TestAiderRefusesBlockScalarRead(t *testing.T) {
	for _, conf := range []string{"read: |\n  x.md\n", "read: >-\n  x.md\n", "read: *files\n"} {
		if got, err := addAiderReadEntry(conf, "/x/aider-context.md"); err == nil {
			t.Errorf("%q was rewritten instead of refused:\n%s", conf, got)
		}
	}
}

// The record of the line install promoted is per config, and the newest one is
// what uninstall puts back. A second promotion left the first record beside it,
// and the record is stored sorted, so a later process could read the old line
// and leave the reader's new one as a block list.
func TestAiderReadWasKeepsOnlyTheLatestLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".aider.conf.yml")
	newProcess := func() {
		recordWiring([]string{"aider"}, false)
		blocksAddedThisRun = nil
		blocksForgottenThisRun = map[string]bool{}
	}
	t.Cleanup(newProcess)
	for _, conf := range []string{"read: b.md\n", "read: a.md\n"} {
		if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installAider("/bin/echo", false); err != nil {
			t.Fatal(err)
		}
		newProcess()
	}
	if _, err := installAider("/bin/echo", true); err != nil {
		t.Fatal(err)
	}
	if got := aiderConf(t, home); got != "read: a.md\n" {
		t.Errorf("uninstall gave back %q, want the line the last install promoted", got)
	}
}

// Installed, the reader's empty read: key keeps its place and takes deja's
// entry under it, rather than being dropped and written again at the end.
func TestAiderInstallFillsTheReadersEmptyKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	if err := os.WriteFile(filepath.Join(home, ".aider.conf.yml"), []byte("read:\nmodel: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	want := "read:\n  - " + aiderContextPath() + "\nmodel: x\n"
	if got := aiderConf(t, home); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// The record of an empty key goes with the key: deleted by the reader while
// installed, it does not come back on uninstall; given a comment, it still
// stays in place.
func TestAiderEmptyKeyRecordFollowsTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".aider.conf.yml")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	newProcess := func(uninstall bool) {
		recordWiring([]string{"aider"}, uninstall)
		blocksAddedThisRun = nil
		blocksForgottenThisRun = map[string]bool{}
	}
	t.Cleanup(func() { newProcess(false) })
	run := func(uninstall bool) {
		if _, err := installAider("/bin/echo", uninstall); err != nil {
			t.Fatal(err)
		}
		newProcess(uninstall)
	}
	write("read:\nmodel: x\n")
	run(false)
	write("model: x\n")
	run(false)
	run(true)
	if got := aiderConf(t, home); got != "model: x\n" {
		t.Errorf("deleted key came back: %q", got)
	}
	write("read:\nmodel: x\n")
	run(false)
	write("read:  # mine\n  - " + aiderContextPath() + "\nmodel: x\n")
	run(false)
	run(true)
	if got := aiderConf(t, home); got != "read:  # mine\nmodel: x\n" {
		t.Errorf("commented key not kept: %q", got)
	}
}

// An empty item at column 0 sets the list's indent too: deja's entry at two
// spaces beside it is a YAML parse error, and aider would not start.
func TestAiderEntryFollowsAnEmptyItemsIndent(t *testing.T) {
	got, err := addAiderReadEntry("read:\n-\nmodel: x\n", "/x/aider-context.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := "read:\n- /x/aider-context.md\n-\nmodel: x\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A blank line before the items is not the items' indent.
	if got, _ = addAiderReadEntry("read:\n\n- C.md\n", "/x/aider-context.md"); got != "read:\n- /x/aider-context.md\n\n- C.md\n" {
		t.Errorf("after a blank line: got %q", got)
	}
	if got, _ = addAiderReadEntry("read:\n  \n- C.md\n", "/x/aider-context.md"); got != "read:\n- /x/aider-context.md\n  \n- C.md\n" {
		t.Errorf("after a spaces-only line: got %q", got)
	}
}

// Doctor's wired check reads a scalar or flow read: by the same rule.
func TestAiderConfReadsContextByName(t *testing.T) {
	for s, want := range map[string]bool{
		"read: my-aider-context.md\n":                                           false,
		"read: [docs/aider-context.md]\n":                                       false,
		"read: [proj/deja/aider-context.md]\n":                                  false,
		"read: ~/.config/deja/aider-context.md\n":                               true,
		"read: [a.md, /h/.config/deja/aider-context.md]\n":                      true,
		"read:\n  - 'C:\\Users\\u\\AppData\\Roaming\\deja\\aider-context.md'\n": true,
	} {
		if got := aiderConfReadsContext(s); got != want {
			t.Errorf("%q: got %v, want %v", s, got, want)
		}
	}
}

func aiderConf(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".aider.conf.yml"))
	if err != nil {
		t.Fatalf("conf missing: %v", err)
	}
	return string(b)
}

func TestInstallAiderWritesTheReadKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	// hook-context is run as a subprocess; a path that is not a deja binary
	// must not stop the config from being written.
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := aiderConf(t, home)
	if !strings.Contains(conf, "read:") || !strings.Contains(conf, "aider-context.md") {
		t.Fatalf("conf does not point aider at the context file:\n%s", conf)
	}
	// aider refuses to start when a configured read file is missing, so the
	// installer has to leave one behind.
	if _, err := os.Stat(filepath.Join(home, "cfg", "deja", "aider-context.md")); err != nil {
		t.Fatalf("context file not created: %v", err)
	}
}

// The read: list is where users keep CONVENTIONS.md. Losing it would be a
// silent behaviour change in their sessions.
func TestInstallAiderKeepsExistingReadEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".aider.conf.yml")
	if err := os.WriteFile(path, []byte("read:\n  - ~/CONVENTIONS.md\nauto-commits: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := aiderConf(t, home)
	for _, want := range []string{"~/CONVENTIONS.md", "aider-context.md", "auto-commits: false"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("install dropped %q:\n%s", want, conf)
		}
	}
	if _, err := installAider("/bin/echo", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	conf = aiderConf(t, home)
	if strings.Contains(conf, "aider-context.md") {
		t.Fatalf("uninstall left our entry behind:\n%s", conf)
	}
	if !strings.Contains(conf, "~/CONVENTIONS.md") || !strings.Contains(conf, "auto-commits: false") {
		t.Fatalf("uninstall took the user's settings with it:\n%s", conf)
	}
}

// Uninstalling from a config we created must not leave a read: key with an
// empty list under it — aider reads that as a null and errors on startup.
func TestUninstallAiderDropsTheEmptyReadKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installAider("/bin/echo", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if conf := aiderConf(t, home); strings.Contains(conf, "read:") {
		t.Fatalf("dangling read: key with nothing under it:\n%q", conf)
	}
	if _, err := os.Stat(filepath.Join(home, "cfg", "deja", "aider-context.md")); !os.IsNotExist(err) {
		t.Fatalf("context file survived uninstall: %v", err)
	}
}

// A scalar read: is the documented single-file form; promoting it to a list
// is the only way to add ours without dropping theirs.
func TestInstallAiderPromotesAScalarRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".aider.conf.yml")
	if err := os.WriteFile(path, []byte("read: ~/CONVENTIONS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := aiderConf(t, home)
	if strings.Contains(conf, "read: ~/CONVENTIONS.md") {
		t.Fatalf("scalar form left in place, our entry cannot coexist with it:\n%s", conf)
	}
	if !strings.Contains(conf, "  - ~/CONVENTIONS.md") || !strings.Contains(conf, "aider-context.md") {
		t.Fatalf("both files must end up in the list:\n%s", conf)
	}
}

// A flow list — `read: [a, b]` — is a list already; treated as the scalar form
// it became one item holding both names, and aider dropped both files (#3197).
func TestInstallAiderSpreadsAFlowListRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".aider.conf.yml")
	if err := os.WriteFile(path, []byte("model: gpt-4o\nread: [CONVENTIONS.md, \"notes.md\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := aiderConf(t, home)
	if strings.Contains(conf, "[CONVENTIONS.md") {
		t.Fatalf("the flow list was kept as one item:\n%s", conf)
	}
	for _, want := range []string{"  - CONVENTIONS.md\n", "  - notes.md\n", "aider-context.md"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("missing %q in:\n%s", want, conf)
		}
	}
}

// `--all` writes MCP entries and nothing else. That is the flag's job, and it
// is not what the name says: someone repairing a machine runs it, watches every
// harness scroll past as "wrote", and walks away with the hooks exactly as
// broken as they were — which is how the wiring in #3421 went on firing after a
// repair that looked complete.
func TestInstallAllSaysWhereTheHooksAre(t *testing.T) {
	tmp := hermeticEnv(t)
	if err := os.MkdirAll(filepath.Join(tmp, "home", ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := logoWanted
	t.Cleanup(func() { logoWanted = restore })
	logoWanted = func(*os.File) bool { return true }

	out, err := captureRun(t, "install", "--all", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deja install --auto") {
		t.Errorf("--all never said the hooks are elsewhere:\n%s", out)
	}

	// --auto writes them, so the same line there would be a lie.
	out, err = captureRun(t, "install", "--auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hooks are not in this list") {
		t.Errorf("--auto claimed it had left the hooks alone:\n%s", out)
	}
}

// `deja install --all` wires the server layer of every harness it finds. For
// DeepSeek Harness both layers are entries in one patch list, and the plain
// target takes the auto row out — so running --all after --auto ended
// auto-recall there, reporting it as "updated" (#3687).
func TestInstallAllKeepsTheAutoLayerItFinds(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".dsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "deepseek-auto", "--no-index"); err != nil {
		t.Fatalf("install deepseek-auto: %v", err)
	}
	plugin := filepath.Join(home, ".dsh", "plugins", "deja", "auto.js")
	if _, err := os.Stat(plugin); err != nil {
		t.Fatalf("install deepseek-auto wrote no plugin: %v", err)
	}
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install --all: %v", err)
	}
	if _, err := os.Stat(plugin); err != nil {
		t.Errorf("install --all deleted the auto-recall plugin: %v", err)
	}
	layer, err := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(layer), yamlQuote(plugin)) {
		t.Errorf("install --all left the patch list without the auto row:\n%s", layer)
	}
}

// The shared skills directory holds one file that several harnesses read, so
// there is one text in it. pi had its own from before the current one existed
// and wrote it to the shared path, so `deja install pi` replaced what codex,
// omp, Senpi, Kimchi and gjc read, and the next install of any of those put it
// back — whichever ran last decided what every agent was told (#3688).
func TestTheSharedSkillHasOneTextWhoeverInstalls(t *testing.T) {
	hermeticEnv(t)
	want := guidanceText("codex")
	for _, harness := range []string{"pi", "omp", "senpi", "cursor", "zed"} {
		if !sharedSkillHarnesses[harness] {
			t.Fatalf("%s no longer shares the skills directory — pick another", harness)
		}
		if got := guidanceText(harness); got != want {
			t.Errorf("%s writes a different text into the shared skill file:\n%s", harness, firstDifference(got, want))
		}
	}
}

func firstDifference(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range al {
		if i >= len(bl) {
			return fmt.Sprintf("line %d is extra: %s", i+1, al[i])
		}
		if al[i] != bl[i] {
			return fmt.Sprintf("line %d:\n got  %s\n want %s", i+1, al[i], bl[i])
		}
	}
	return fmt.Sprintf("shorter by %d lines", len(bl)-len(al))
}

// An editor's JSONC carries trailing commas as well as comments; the amp
// reader stripped comments and then demanded strict JSON (#3236).
func TestInstallAmpTakesAFileWithATrailingComma(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", path)
	before := "{\n  // mine\n  \"amp.mcpServers\": {\"context7\": {\"command\": \"npx\", \"args\": [\"-y\"],},},\n  \"amp.notifications.enabled\": true,\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installAmpMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install refused the editor's own file: %v", err)
	}
	root := ampSettings(t, path)
	servers, _ := root[ampServersKey].(map[string]any)
	if servers["context7"] == nil || servers["deja"] == nil {
		t.Fatalf("install did not sit beside the existing server: %v", root)
	}
}

// Amp's settings file is JSONC, and re-marshalling it dropped the reader's
// comments and sorted the keys, with no way back on uninstall (#4357). The
// block key is one literal with a dot in it, not a path.
func TestInstallAmpKeepsCommentsAndOrderOnARoundTrip(t *testing.T) {
	for name, before := range map[string]string{
		"block":    "// my amp settings\n{\n  \"amp.notifications.enabled\": false, // quiet\n  \"amp.mcpServers\": {\n    \"playwright\": { \"command\": \"npx\", \"args\": [\"-y\", \"@playwright/mcp@latest\"] },\n  },\n  \"amp.dangerouslyAllowAll\": true,\n}\n",
		"no block": "// my amp settings\n{\n  \"amp.notifications.enabled\": false, // quiet\n  \"amp.dangerouslyAllowAll\": true,\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
			path := filepath.Join(home, "settings.json")
			t.Setenv("AMP_SETTINGS_FILE", path)
			if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := installAmpMCP("/usr/local/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			installed, _ := os.ReadFile(path)
			for _, want := range []string{"// my amp settings", "// quiet"} {
				if !strings.Contains(string(installed), want) {
					t.Fatalf("install dropped %q:\n%s", want, installed)
				}
			}
			root := ampSettings(t, path)
			servers, _ := root[ampServersKey].(map[string]any)
			if servers["deja"] == nil || root["amp"] != nil {
				t.Fatalf("deja is not under the literal %q key:\n%s", ampServersKey, installed)
			}
			if _, err := installAmpMCP("/usr/local/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != before {
				t.Fatalf("round trip changed the file:\n--- before\n%s--- after\n%s", before, after)
			}
		})
	}
}

func ampSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	// Amp reads its settings as JSONC, and a file that came in with comments
	// keeps them (#4357).
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
		t.Fatalf("settings are not JSON Amp can read: %v\n%s", err, b)
	}
	return root
}

// Amp keeps MCP servers under one literal key with a dot in its name. Writing a
// nested {"amp": {"mcpServers": …}} instead produces a file Amp parses happily
// and ignores completely.
func TestInstallAmpWritesTheDottedServerKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AMP_SETTINGS_FILE", "")

	r, err := installAmpMCP("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "amp", "settings.json")
	if r.Path != want {
		t.Fatalf("wrote %q, want %q", r.Path, want)
	}
	root := ampSettings(t, r.Path)
	if _, nested := root["amp"]; nested {
		t.Fatalf("wrote a nested amp object; Amp reads the flat %q key:\n%v", ampServersKey, root)
	}
	servers, _ := root[ampServersKey].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if entry == nil {
		t.Fatalf("no deja server under %q: %v", ampServersKey, root)
	}
	if entry["command"] == nil || entry["args"] == nil {
		t.Fatalf("server entry is missing command/args: %v", entry)
	}
}

// AMP_SETTINGS_FILE moves the file Amp itself reads, so an install that ignores
// it writes somewhere nothing loads.
func TestInstallAmpFollowsTheSettingsFileOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	elsewhere := filepath.Join(home, "elsewhere", "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", elsewhere)

	r, err := installAmpMCP("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Path != elsewhere {
		t.Fatalf("wrote %q, want %q", r.Path, elsewhere)
	}
	ampSettings(t, elsewhere)
}

// Someone else's servers and settings have to survive both directions.
func TestInstallAmpKeepsOtherSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", path)
	before := `{"amp.mcpServers":{"context7":{"command":"npx","args":["-y","@upstash/context7-mcp"]}},"amp.notifications.enabled":true}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installAmpMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := ampSettings(t, path)
	servers, _ := root[ampServersKey].(map[string]any)
	if servers["context7"] == nil || servers["deja"] == nil {
		t.Fatalf("install did not sit beside the existing server: %v", root)
	}
	if root["amp.notifications.enabled"] != true {
		t.Fatalf("an unrelated setting was dropped: %v", root)
	}
	if _, err := installAmpMCP("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	root = ampSettings(t, path)
	servers, _ = root[ampServersKey].(map[string]any)
	if servers["deja"] != nil {
		t.Fatalf("uninstall left the deja entry: %v", root)
	}
	if servers["context7"] == nil {
		t.Fatalf("uninstall took someone else's server with it: %v", root)
	}
}

func TestInstallAmpAutoWritesADiscoverablePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AMP_SETTINGS_FILE", "")

	if _, err := installAmpAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	// Amp auto-discovers ~/.config/amp/plugins/*.ts; anywhere else is inert.
	path := filepath.Join(home, ".config", "amp", "plugins", "deja.ts")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plugin not at the discovered path: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		`amp.on("agent.start"`, // the one event whose return reaches the model
		"hook-context",
		`"hook-prompt", "--plain"`,
		`amp.on("tool.result"`,
		`"hook-tool-after", "--plain"`,
		"registerCommand",
		// The launcher, not the build: a plugin that names the binary it was
		// installed from stops working the day that build moves (#3682).
		strconv.Quote(hookExeInConfigs("/usr/local/bin/deja")),
		"export const description", // Amp shows this in plugin settings
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("plugin missing %q:\n%s", want, src)
		}
	}
	// The MCP half goes in at the same time: a plugin that hands the model a
	// digest and no tool to follow it up with is half an install.
	root := ampSettings(t, filepath.Join(home, ".config", "amp", "settings.json"))
	if root[ampServersKey] == nil {
		t.Fatalf("amp-auto wrote no MCP server: %v", root)
	}
}

func TestUninstallAmpAutoRemovesThePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AMP_SETTINGS_FILE", "")

	if _, err := installAmpAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installAmpAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "amp", "plugins", "deja.ts")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plugin survived uninstall: %v", err)
	}
	// Uninstalling twice is not an error and creates nothing.
	if r, err := installAmpPlugin("/usr/local/bin/deja", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("second uninstall = %+v, %v", r, err)
	}
}

func TestAmpPluginQuotesTheBinaryPath(t *testing.T) {
	src := ampPluginTS(`C:\Program Files\deja\deja.exe`)
	if !strings.Contains(src, `const DEJA = "C:\\Program Files\\deja\\deja.exe"`) {
		t.Fatalf("a Windows path was not escaped for TypeScript:\n%s", src)
	}
}

// Amp's tool.result carries a status, not an isError flag, and a repair sent
// back with status "done" would tell the model a failed command succeeded.
func TestAmpRepairKeepsTheFailureStatus(t *testing.T) {
	src := ampPluginTS("/bin/deja")
	if !strings.Contains(src, `event.status !== "error"`) {
		t.Fatalf("the repair does not gate on a failed tool:\n%s", src)
	}
	if !strings.Contains(src, `return { status: "error"`) {
		t.Fatalf("the repair reports the failure as something other than an error:\n%s", src)
	}
	if !strings.Contains(src, "event.toolUseID") {
		t.Fatalf("the lookup is not keyed per tool call:\n%s", src)
	}
}

// A search the user typed may rebuild the index; a hook may not. One shared
// timeout would make the command answer "nothing matches" for a query with
// real hits, and a query starting with a dash must not be read as a flag.
func TestAmpCommandOutlivesTheHookTimeoutAndSurvivesAFlagQuery(t *testing.T) {
	src := ampPluginTS("/bin/deja")
	if !strings.Contains(src, "120000") || !strings.Contains(src, "timeout = 10000") {
		t.Fatalf("the command runs on the hook budget:\n%s", src)
	}
	if !strings.Contains(src, `["search", "--", query]`) {
		t.Fatalf("a query that names a deja flag dies in flag parsing:\n%s", src)
	}
}

// Amp keeps one process across threads. The plugin injected the session
// digest once per process, so the second thread of the day opened with no
// memory (#3263). Driven the way Amp drives it: default(amp) once, then the
// events of two threads, with a stub deja that answers hook-context.
func TestAmpPluginInjectsTheDigestOncePerThread(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if rerr := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run(); rerr != nil {
		t.Skip("this node cannot strip types")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "deja")
	if werr := os.WriteFile(stub, []byte("#!/bin/sh\n"+
		// The stub drains stdin on every branch: the plugin hands hook-context a
		// JSON payload since #3264, and a child that exits without reading it
		// gives execFileSync EPIPE on the runners, which the plugin swallows
		// into "nothing" (#3283).
		`cat >/dev/null; case "$1" in hook-context) echo '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"<deja-recall>\nrecent history\n</deja-recall>"},"systemMessage":"deja recalled 1 session"}';; *) echo "";; esac`+"\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(dir, "deja.ts")
	if werr := os.WriteFile(plugin, []byte(ampPluginTS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	drive := filepath.Join(dir, "drive.mjs")
	if werr := os.WriteFile(drive, []byte(`
const mod = await import(process.argv[2]);
const handlers = {};
const amp = { on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() { return { dispose() {} }; } };
mod.default(amp);
const ctx = { ui: { notify: async () => {}, input: async () => "" } };
async function fire(name, event) {
  let out = "";
  for (const fn of handlers[name] || []) {
    const r = await fn(event, ctx);
    if (r && r.message && r.message.content) out += r.message.content;
  }
  return out;
}
for (const id of ["T-A", "T-B"]) {
  await fire("session.start", { thread: { id } });
  const first = await fire("agent.start", { thread: { id }, message: "hello", id: id + "-1" });
  const second = await fire("agent.start", { thread: { id }, message: "hello again", id: id + "-2" });
  console.log(id, first.includes("recent history") ? "digest" : "nothing", second.includes("recent history") ? "digest" : "nothing");
}
`), 0o644); werr != nil {
		t.Fatal(werr)
	}
	cmd := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "--no-warnings", drive, plugin)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := "T-A digest nothing\nT-B digest nothing"
	if got != want {
		t.Errorf("threads got:\n%s\nwant:\n%s", got, want)
	}
}

// Amp runs a plugin from the plugin's own directory, so a plugin that took
// process.cwd() for the thread's directory filed every thread, and sent every
// hook, under ~/.config/amp/plugins (#5672). The directory comes from
// amp.system.workspaceRoot, and process.cwd() is left only for a host that
// gives none.
func TestAmpPluginFilesTheThreadUnderTheWorkspaceRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if rerr := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run(); rerr != nil {
		t.Skip("this node cannot strip types")
	}
	for _, tc := range []struct {
		name     string
		withRoot bool
	}{
		{"workspace root", true},
		{"no workspace root", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("USERPROFILE", dir)
			threads := filepath.Join(dir, "threads")
			t.Setenv("DEJA_AMP_ROOT", threads)
			repo := filepath.Join(dir, "repo")
			pluginDir := filepath.Join(dir, "plugins")
			for _, d := range []string{repo, pluginDir} {
				if merr := os.MkdirAll(d, 0o755); merr != nil {
					t.Fatal(merr)
				}
			}
			payloads := filepath.Join(dir, "payloads.jsonl")
			stub := filepath.Join(dir, "deja")
			if werr := os.WriteFile(stub, []byte("#!/bin/sh\n"+
				`case "$1" in hook-*) cat >>"`+payloads+`"; echo >>"`+payloads+`";; *) cat >/dev/null;; esac`+"\n"), 0o755); werr != nil {
				t.Fatal(werr)
			}
			plugin := filepath.Join(pluginDir, "deja.ts")
			if werr := os.WriteFile(plugin, []byte(ampPluginTS(stub)), 0o644); werr != nil {
				t.Fatal(werr)
			}
			drive := filepath.Join(dir, "drive.mjs")
			if werr := os.WriteFile(drive, []byte(`
const [plugin, root] = process.argv.slice(2);
const mod = await import(plugin);
const handlers = {};
const amp = {
  on(n, f) { (handlers[n] ||= []).push(f); },
  registerCommand() { return { dispose() {} }; },
  helpers: { filePathFromURI: (u) => decodeURIComponent(new URL(String(u)).pathname) },
};
if (root) amp.system = { workspaceRoot: { toString: () => root } };
mod.default(amp);
const thread = { messages: async ({ offset }) => (offset ? [] : [{ id: "m1", role: "user", content: [{ type: "text", text: "hello" }] }]) };
const ctx = { thread, ui: { notify: async () => {}, input: async () => "" } };
for (const name of ["agent.start", "agent.end"]) {
  for (const fn of handlers[name] || []) await fn({ thread: { id: "T-W" }, message: "hello" }, ctx);
}
`), 0o644); werr != nil {
				t.Fatal(werr)
			}
			root := ""
			if tc.withRoot {
				root = "file://" + repo
			}
			cmd := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "--no-warnings", drive, plugin, root)
			cmd.Dir = pluginDir
			if out, rerr := cmd.CombinedOutput(); rerr != nil {
				t.Fatalf("node: %v\n%s", rerr, out)
			}
			want := pluginDir
			if tc.withRoot {
				want = repo
			}
			// macOS hands node /private/var for a /var temp dir.
			same := func(got string) bool {
				a, aerr := filepath.EvalSymlinks(got)
				b, berr := filepath.EvalSymlinks(want)
				return aerr == nil && berr == nil && a == b
			}
			raw, rerr := os.ReadFile(payloads)
			if rerr != nil {
				t.Fatal(rerr)
			}
			hooks := 0
			for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
				var p struct {
					Cwd string `json:"cwd"`
				}
				if jerr := json.Unmarshal([]byte(line), &p); jerr != nil {
					t.Fatalf("payload %q: %v", line, jerr)
				}
				hooks++
				if !same(p.Cwd) {
					t.Errorf("hook cwd = %q, want %q", p.Cwd, want)
				}
			}
			if hooks < 2 {
				t.Errorf("hooks = %d, want hook-context and hook-session-end at least", hooks)
			}
			file, rerr := os.ReadFile(filepath.Join(threads, "T-W.json"))
			if rerr != nil {
				t.Fatal(rerr)
			}
			var exported struct {
				Env struct {
					Initial struct {
						Trees []struct {
							URI string `json:"uri"`
						} `json:"trees"`
					} `json:"initial"`
				} `json:"env"`
			}
			if jerr := json.Unmarshal(file, &exported); jerr != nil {
				t.Fatal(jerr)
			}
			if trees := exported.Env.Initial.Trees; len(trees) != 1 || !same(strings.TrimPrefix(trees[0].URI, "file://")) {
				t.Errorf("thread trees = %+v, want one file:// URI for %q", trees, want)
			}
		})
	}
}

func TestInstallAntigravityPluginShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installAntigravityPlugin("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(antigravityConfigHome(), "plugins", "deja")
	// Without plugin.json the directory is not a plugin and hooks.json is
	// ignored without a word.
	if _, err := os.ReadFile(filepath.Join(dir, "plugin.json")); err != nil {
		t.Fatalf("plugin.json missing: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
	if err != nil {
		t.Fatalf("hooks.json missing: %v", err)
	}
	var root map[string]map[string][]map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks.json is not the named-hook map antigravity expects: %v", err)
	}
	handlers := root["deja-recall"]["PreInvocation"]
	if len(handlers) != 1 {
		t.Fatalf("PreInvocation handlers = %v", handlers)
	}
	cmd, _ := handlers[0]["command"].(string)
	if !strings.Contains(cmd, "hook-antigravity") || !strings.Contains(cmd, "/bin/deja") {
		t.Fatalf("command = %q", cmd)
	}
	if _, err := installAntigravityPlugin("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plugin survived uninstall: %v", err)
	}
}

func TestAntigravityPluginQuotesExecutablePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// The command is run through sh -c, so a path with a space must survive.
	if _, err := installAntigravityPlugin("/Applications/My Tools/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(antigravityConfigHome(), "plugins", "deja", "hooks.json"))
	var root map[string]map[string][]map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	cmd, _ := root["deja-recall"]["PreInvocation"][0]["command"].(string)
	// The hook runs the launcher, and the launcher carries the path with the
	// space in it — so both have to be quoted or sh -c splits one of them
	// (#3422, #3682).
	if !strings.HasPrefix(cmd, strconv.Quote(hookExeInConfigs("/Applications/My Tools/deja"))) {
		t.Fatalf("unquoted path would split under sh -c: %q", cmd)
	}
	if p := dejaLauncherPath(); p != "" {
		if !strings.Contains(readFile(t, p), `'/Applications/My Tools/deja'`) {
			t.Fatalf("the launcher does not quote the path it resolves:\n%s", readFile(t, p))
		}
	}
}

func TestHookAntigravityInjectsOnlyOnFirstInvocation(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	// Later turns already carry the digest in their transcript.
	if err := runHookAntigravity(dir, strings.NewReader(`{"invocationNum":2}`), &out); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "{}" {
		t.Fatalf("second invocation injected %q", got)
	}
	// No index: still valid JSON, never a bare error the host would choke on.
	out.Reset()
	if err := runHookAntigravity(dir, strings.NewReader(`{"invocationNum":0}`), &out); err != nil {
		t.Fatalf("hook: %v", err)
	}
	var resp antigravityHookResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("output is not JSON: %q", out.String())
	}
	if len(resp.InjectSteps) != 0 {
		t.Fatalf("injected with no index: %+v", resp)
	}
}

// Antigravity runs the hook in the folder holding hooks.json rather than the
// user's project, so the workspace in the payload is the only thing that says
// which project this is. The scoping used to be checked through the export deja
// wrote; that export is gone (#2185), so this asks the question directly: the
// memory that comes back is the named workspace's.
func TestHookAntigravityScopesToWorkspacePath(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "alpha", "one.jsonl"), "alpha1", []string{
		`{"type":"user","sessionId":"alpha1","timestamp":"` + at +
			`","message":{"role":"user","content":"the alpha work: pgbouncer runs in transaction mode"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "beta1", []string{
		`{"type":"user","sessionId":"beta1","timestamp":"` + at +
			`","message":{"role":"user","content":"the beta work: the kafka consumer keeps rebalancing"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	beta := filepath.Join(base, "tmp", "beta")
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}
	// Neither the environment nor the working directory names a project.
	t.Chdir(base)
	t.Setenv("CLAUDE_PROJECT_DIR", "")

	var out bytes.Buffer
	// Marshalled rather than pasted: a Windows path is full of backslashes,
	// and hand-built JSON turns them into escape sequences the decoder refuses
	// — the payload then names no workspace and the recall comes back empty.
	payload, err := json.Marshal(map[string]any{"invocationNum": 0, "workspacePaths": []string{beta}})
	if err != nil {
		t.Fatal(err)
	}
	in := string(payload)
	if err := runHookAntigravity(dir, strings.NewReader(in), &out); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if !strings.Contains(out.String(), "rebalancing") {
		t.Errorf("the workspace named in the payload did not scope the recall:\n%s", out.String())
	}
	if strings.Contains(out.String(), "transaction mode") {
		t.Errorf("another project's memory came back:\n%s", out.String())
	}
	if got := os.Getenv("CLAUDE_PROJECT_DIR"); got != "" {
		t.Errorf("the door left %q in the environment for the next call in this process", got)
	}
}

func TestHookAntigravitySurvivesGarbageStdin(t *testing.T) {
	var out bytes.Buffer
	if err := runHookAntigravity(t.TempDir(), strings.NewReader("not json at all"), &out); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if !json.Valid(bytes.TrimSpace(out.Bytes())) {
		t.Fatalf("output is not JSON: %q", out.String())
	}
}

func TestAntigravityGuidanceLandsInsideThePlugin(t *testing.T) {
	hermeticEnv(t)
	if err := runInstall(t.TempDir(), []string{"antigravity"}, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(antigravityConfigHome(), "plugins", antigravityPluginName)
	skill := filepath.Join(dir, "skills", "deja-history", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("the skill has to sit inside the plugin, not beside it: %v", err)
	}
	// Antigravity ingests a directory only when plugin.json marks it; without
	// the marker the skill is skipped in silence.
	if _, err := os.Stat(filepath.Join(dir, "plugin.json")); err != nil {
		t.Fatalf("plugin.json must accompany the skill: %v", err)
	}
	// And nothing should be written to the path that reads like a plugin root
	// but is not one.
	if _, err := os.Stat(filepath.Join(antigravityConfigHome(), "skills")); err == nil {
		t.Fatal("guidance was also written beside the plugin, where nothing reads it")
	}
}

func TestAntigravityMarkerIsNotRewritten(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(antigravityConfigHome(), "plugins", antigravityPluginName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := []byte(`{"name":"deja","version":"kept"}` + "\n")
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureAntigravityPluginMarker(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil || !bytes.Equal(got, custom) {
		t.Fatalf("an existing marker belongs to install_antigravity, got %q", got)
	}
}

func TestInstallAntigravityMCPMarksToolsEager(t *testing.T) {
	hermeticEnv(t)
	if _, err := installAntigravityMCP("/bin/deja", false); err != nil {
		t.Fatalf("installAntigravityMCP: %v", err)
	}
	cfgPath := filepath.Join(antigravityConfigHome(), "mcp_config.json")
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Servers map[string]struct {
			ForceAllToolsEager bool `json:"forceAllToolsEager"`
			Tools              map[string]struct {
				Eager bool `json:"eager"`
			} `json:"tools"`
		} `json:"mcpServers"`
	}
	if uerr := json.Unmarshal(b, &root); uerr != nil {
		t.Fatal(uerr)
	}
	srv := root.Servers["deja"]
	if !srv.ForceAllToolsEager || !srv.Tools["deja"].Eager {
		t.Fatalf("deja entry not marked eager: %s", b)
	}
	if werr := os.WriteFile(cfgPath, []byte(`{"mcpServers":{"deja":{"command":"/bin/deja","args":["mcp"]}}}`+"\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if res, ierr := installAntigravityMCP("/bin/deja", false); ierr != nil || res.Action != "updated" {
		t.Fatalf("upgrade antigravity mcp_config.json = %+v, %v; want Action=updated", res, ierr)
	}
}

func TestLatestEditCallsFromStepsIncludesReadAndViewFile(t *testing.T) {
	steps := []transcriptStep{
		{
			Type: "PLANNER_RESPONSE",
			ToolCalls: []any{
				map[string]any{"name": "read_file", "args": map[string]any{"Path": "/repo/a.go"}},
				map[string]any{"name": "view_file", "args": map[string]any{"AbsolutePath": "/repo/b.go"}},
			},
		},
	}
	got := latestEditCallsFromSteps(steps)
	if len(got) != 2 || got[0].FilePath != "/repo/a.go" || got[1].FilePath != "/repo/b.go" {
		t.Fatalf("latestEditCallsFromSteps = %+v, want [/repo/a.go /repo/b.go]", got)
	}
}

// `deja install --auto` maps a detected harness to one target, so that target
// has to carry everything that harness can take. gemini's did not: it wrote the
// hooks extension and no MCP server, and gemini's own `gemini mcp list` said
// "No MCP servers configured" on a machine that had just run the installer.
// Whoever adds the next -auto target will not think of this either, so it is
// checked for all of them: if the plain target writes an MCP entry, the -auto
// one must too.
func TestAutoTargetsCarryTheirMCPServer(t *testing.T) {
	for _, target := range installTargetNames() {
		base, ok := strings.CutSuffix(target, "-auto")
		if !ok {
			continue
		}
		if base == "claude" {
			base = "claude-code" // detection reports it under the other name
		}
		if !hasTarget(base) {
			continue
		}
		t.Run(target, func(t *testing.T) {
			plain := installedFiles(t, base)
			auto := installedFiles(t, target)

			for path, body := range plain {
				if !strings.Contains(body, "deja") || !strings.Contains(body, "mcp") {
					continue
				}
				got, ok := auto[path]
				if !ok || !strings.Contains(got, "mcp") {
					t.Errorf("%s writes an MCP server into %s and %s does not; "+
						"`deja install --auto` installs only the second one",
						base, path, target)
				}
			}
		})
	}
}

func hasTarget(name string) bool {
	return slices.Contains(installTargetNames(), name)
}

// installedFiles installs one target into an empty home and returns what it
// wrote, keyed by path relative to that home.
func installedFiles(t *testing.T, target string) map[string]string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// VS Code resolves through APPDATA on Windows, and vscode-auto writes
	// there.
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
	if _, err := installTarget(target, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install %s: %v", target, err)
	}
	out := map[string]string{}
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		b, rerr := os.ReadFile(path) //nolint:gosec // the walk is over the test's own temp home
		if rerr != nil {
			return nil //nolint:nilerr // unreadable files are skipped
		}
		rel, rerr := filepath.Rel(home, path)
		if rerr != nil {
			rel = path
		}
		out[rel] = string(b)
		return nil
	})
	return out
}

// An -auto target writes several files and used to report only the last one,
// so a run that rewired the MCP entry said "unchanged" about the hook file
// instead — the wrong word about the wrong path (#2396).
func TestAnAutoTargetReportsTheFileItChanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := installTarget("claude-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	// The premise: with everything in place, there is nothing to do.
	settled, err := installTarget("claude-auto", "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Action != "unchanged" {
		t.Fatalf("a second install still had work to do: %s %s", settled.Action, settled.Path)
	}

	// Take deja out of the MCP config by hand and leave the hooks alone.
	mcpPath := filepath.Join(home, ".claude.json")
	body, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if uerr := json.Unmarshal(body, &root); uerr != nil {
		t.Fatal(uerr)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil || servers["deja"] == nil {
		t.Fatalf("install left no deja entry to remove: %s", body)
	}
	delete(servers, "deja")
	body, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(mcpPath, body, 0o644); werr != nil {
		t.Fatal(werr)
	}

	r, err := installTarget("claude-auto", "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action == "unchanged" {
		t.Errorf("install put the entry back and called the run unchanged: %s %s", r.Action, r.Path)
	}
	if r.Path != mcpPath {
		t.Errorf("install named %s, but %s is what it wrote", r.Path, mcpPath)
	}
}

func TestInstallCodexHooksMergeAndUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// pre-existing foreign hook must survive install and uninstall
	seed := `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"other-tool ctx"}]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := installCodexHooks("/usr/local/bin/deja", false)
	if err != nil || r.Action != "updated" {
		t.Fatalf("install: %v %v", r, err)
	}
	b, _ := os.ReadFile(path)
	var root map[string]any
	if uerr := json.Unmarshal(b, &root); uerr != nil {
		t.Fatal(uerr)
	}
	entries := typedOrFatal[[]any](t, typedOrFatal[map[string]any](t, root["hooks"])["SessionStart"])
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want foreign + ours", len(entries))
	}
	if !strings.Contains(string(b), "other-tool ctx") || !strings.Contains(string(b), hookExeInConfigs("/usr/local/bin/deja")+" hook-context") {
		t.Fatalf("merged file wrong: %s", b)
	}
	// idempotent
	r, err = installCodexHooks("/usr/local/bin/deja", false)
	if err != nil || r.Action != "unchanged" {
		t.Fatalf("second install: %v %v", r, err)
	}
	// uninstall removes only ours
	r, err = installCodexHooks("/usr/local/bin/deja", true)
	if err != nil || r.Action != "updated" {
		t.Fatalf("uninstall: %v %v", r, err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "deja hook-context") || !strings.Contains(string(b), "other-tool ctx") {
		t.Fatalf("uninstall wrong: %s", b)
	}
}

// Codex sends the same UserPromptSubmit payload Claude does, and it is the only
// event that carries the user's own words. Without it a codex session recalls
// nothing between the session digest and a command it is about to run: measured
// on codex 0.149.0, the event fires on every `codex exec` turn.
func TestInstallCodexWiresThePromptItself(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	entries := root.Hooks["UserPromptSubmit"]
	if len(entries) != 1 {
		t.Fatalf("UserPromptSubmit entries = %d, want 1: %s", len(entries), b)
	}
	if want := hookExeInConfigs("/usr/local/bin/deja") + " hook-prompt"; entries[0].Hooks[0].Command != want {
		t.Fatalf("command = %q, want %q", entries[0].Hooks[0].Command, want)
	}
	// Codex's own examples carry no matcher on an every-turn event, and an
	// empty pattern is not the same thing as an absent one.
	if entries[0].Matcher != nil {
		t.Fatalf("matcher = %q, want the key left out", *entries[0].Matcher)
	}
}

// Codex compacts on its own and fires PreCompact with trigger "auto" first.
// Unwired, the session keeps its record of what it was shown while losing the
// blocks themselves, so recall stays quiet about exactly what was just dropped.
// Codex fires SessionStart again with source "compact" after it compacts, which
// is when the digest is worth most — and an install that predates this carries
// the old pattern, so the upgrade has to rewrite it rather than adopt it.
func TestInstallCodexSessionStartSurvivesCompaction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"/usr/local/bin/deja hook-context"}]}]}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	entries := root.Hooks["SessionStart"]
	if len(entries) != 1 {
		t.Fatalf("SessionStart entries = %d, want the old one adopted: %s", len(entries), b)
	}
	if entries[0].Matcher != nil {
		t.Fatalf("matcher = %q, want it widened to every source", *entries[0].Matcher)
	}
}

func TestInstallCodexWiresCompaction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	entries := root.Hooks["PreCompact"]
	if len(entries) != 1 {
		t.Fatalf("PreCompact entries = %d, want 1: %s", len(entries), b)
	}
	if want := hookExeInConfigs("/usr/local/bin/deja") + " hook-precompact"; entries[0].Hooks[0].Command != want {
		t.Fatalf("command = %q, want %q", entries[0].Hooks[0].Command, want)
	}
	// Codex sends "auto" when it compacts by itself, which is how it compacts.
	if m := entries[0].Matcher; !strings.Contains(m, "auto") {
		t.Fatalf("matcher = %q, want it to cover codex's own compaction", m)
	}
}

func TestInstallCodexHooksErrorsAndMissingUninstall(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path := filepath.Join(home, ".codex", "hooks.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"hooks":`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installCodexHooks("/bin/deja", false); err == nil {
			t.Fatal("expected malformed hooks.json error")
		}
	})

	t.Run("uninstall missing file", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		r, err := installCodexHooks("/bin/deja", true)
		if err != nil {
			t.Fatal(err)
		}
		if r.Path != filepath.Join(home, ".codex", "hooks.json") || r.Action != "created" {
			t.Fatalf("result = %#v", r)
		}
	})
}

func TestInstallOpencodePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_MAJOR", "2")
	r, err := installOpencodePlugin("/opt/deja", false)
	if err != nil || r.Action != "created" {
		t.Fatalf("install: %v %v", r, err)
	}
	b, err := os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// The session id has to travel with the per-prompt payload: recall skips
	// what it already showed a session, and without an id there is nothing to
	// skip by. In real-world stores, half of all injections were a
	// word-for-word repeat, nearly all within a minute of each other.
	for _, want := range []string{"session_id", "event.sessionID"} {
		if !strings.Contains(s, want) {
			t.Errorf("opencode plugin does not pass %q, so recall cannot dedupe:\n%s", want, s)
		}
	}
	for _, want := range []string{`ctx.session.hook("context"`,
		// The launcher, not the build it was installed from (#3682).
		hookExeInConfigs("/opt/deja"), "hook-context", "cache"} {
		if !strings.Contains(s, want) {
			t.Fatalf("plugin missing %q:\n%s", want, s)
		}
	}
	if r, _ := installOpencodePlugin("/opt/deja", false); r.Action != "unchanged" {
		t.Fatalf("second install action = %s", r.Action)
	}
	if r, _ := installOpencodePlugin("/opt/deja", true); r.Action != "removed" {
		t.Fatalf("uninstall action = %s", r.Action)
	}
	if r, _ := installOpencodePlugin("/opt/deja", true); r.Action != "unchanged" {
		t.Fatalf("second uninstall action = %s", r.Action)
	}
}

func TestInstallOpencodePluginUninstallMissingDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r, err := installOpencodePlugin("/opt/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "unchanged" || r.Path != filepath.Join(home, ".config", "opencode", "plugins", "deja.js") {
		t.Fatalf("result = %#v", r)
	}
}

func TestInstallAutoWrappers(t *testing.T) {
	// An -auto target writes several files, and what it reports is the first
	// one it changed — reporting the last said "unchanged" about a run that
	// had rewired something else (#2396). The rest ride along in the note.
	for _, tc := range []struct {
		name string
		fn   func(string, bool) (installResult, error)
		want string
		also string
	}{
		{"codex", installCodexAuto, filepath.Join(".codex", "config.toml"), "hooks.json"},
		{"opencode", installOpencodeAuto, filepath.Join(".config", "opencode", "opencode.json"), "deja.js"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			r, err := tc.fn("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if r.Action != "created" || r.Path != filepath.Join(home, tc.want) {
				t.Fatalf("install result = %#v", r)
			}
			if !strings.Contains(r.Note, tc.also) {
				t.Errorf("the other file it wrote is unmentioned: %#v", r)
			}
			r, err = tc.fn("/bin/deja", true)
			if err != nil {
				t.Fatal(err)
			}
			if r.Path == "" || r.Action == "" {
				t.Fatalf("uninstall result = %#v", r)
			}
		})
	}
}

func TestPrintInstallProofListsDistinctProjects(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	// Drained concurrently: a windows pipe buffers a few KB and this report
	// grows with what the install wrote.
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	printInstallProof(index.DefaultDir())
	_ = w.Close()
	os.Stderr = old
	out := <-done
	if !strings.Contains(out, "deja already knows this machine:") ||
		!strings.Contains(out, "tmp/beta") || !strings.Contains(out, "gamma") {
		t.Fatalf("proof output = %q", out)
	}
	// One line per project, newest first, capped at three.
	if strings.Count(out, "[claude") > 2 {
		t.Fatalf("projects not deduped: %q", out)
	}
}

// TestInstallCodexHooksHonoursCodexHome pins #850: install must write to
// CODEX_HOME (via sources.CodexHome()), not a raw ~/.codex, so a sandboxed
// install cannot edit the operator's real config. HOME and CODEX_HOME point at
// distinct dirs; the hooks file must land under CODEX_HOME and not under HOME.
func TestInstallCodexHooksHonoursCodexHome(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", codexHome)

	r, err := installCodexHooks("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	want := filepath.Join(codexHome, "hooks.json")
	if r.Path != want {
		t.Errorf("install wrote to %q, want %q (CODEX_HOME must be honoured)", r.Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("no hooks.json under CODEX_HOME: %v", err)
	}
	// The real (HOME-based) ~/.codex must be left untouched.
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Errorf("install wrote to HOME/.codex despite CODEX_HOME being set (err=%v)", err)
	}
}

// A config deja cannot parse is a config deja must not touch. The file belongs
// to the user — it may be mid-edit, or written by a version of the harness that
// deja does not know — and overwriting it to make room for a hook would destroy
// work to install a convenience.
func TestBrokenConfigIsLeftAlone(t *testing.T) {
	cases := []struct{ target, rel, broken string }{
		{"qwen-auto", ".qwen/settings.json", "{\"theme\": \"dark\",\n"},
		{"codex-auto", ".codex/hooks.json", "{ this is not json at all }"},
		{"grok-auto", ".grok/user-settings.json", "{\"apiKey\": "},
		{"opencode-auto", ".config/opencode/opencode.json", "{\"model\":"},
	}

	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.broken), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := installTarget(tc.target, "/usr/local/bin/deja", false)
			after, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("the file is gone after a failed install: %v", rerr)
			}
			if string(after) != tc.broken {
				t.Fatalf("deja rewrote a config it could not read:\n--- was\n%s\n--- now\n%s",
					tc.broken, after)
			}
			// And it has to say so. Failing silently leaves someone believing
			// they have memory wired when they have not.
			if err == nil {
				t.Error("install reported success on a config it could not parse")
			}
		})
	}
}

// A config somebody has already broken by hand. Every JSON target refuses one
// and says where; the TOML path spliced its block in regardless, so deja's
// entry landed in a file the harness cannot load — and the harness's own error
// then named deja's lines (#3576).
func TestInstallRefusesATOMLFileThatIsAlreadyBroken(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	broken := "model = \"gpt-5\"\n[mcp_servers.theirs\ncommand = \"x\"\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := installTOML(path, "[mcp_servers.deja]\ncommand = \"deja\"\n", false)
	if err == nil {
		t.Fatal("install wrote into a file that does not parse")
	}
	if !strings.Contains(err.Error(), "table header") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != broken {
		t.Fatalf("the file was touched:\n%s", after)
	}
}

// What the check must not do is refuse an ordinary config: headers with
// comments, nested tables, arrays of tables and quoted keys are all fine.
func TestTheTOMLCheckLeavesOrdinaryConfigsAlone(t *testing.T) {
	for _, text := range []string{
		"model = \"gpt-5\"\n",
		"[mcp_servers.deja]\ncommand = \"deja\"\n",
		"[mcp_servers.deja]  # the one deja wrote\ncommand = \"deja\"\n",
		"[[servers]]\nname = \"a\"\n\n[[servers]]\nname = \"b\"\n",
		"[tools.\"weird.name\"]\nenabled = true\n",
		// codex writes hook trust headers whose quoted key carries a `#`
		// (the plugin json path and the hook index). A comment-stripper that
		// cuts at every `#` refuses them, so a real codex config could not
		// be installed into at all.
		"[hooks.state.\"browser@openai-bundled:plugin.json#hooks[0]:subagent_stop:0:0\"]\nenabled = true\n",
		"[hooks.state.\"unified-computer-use@openai-bundled:plugin.json#hooks[0]:post_tool_use:0:0\"]\nenabled = true\n",
		"# [commented.out]\nvalue = 1\n",
		"",
	} {
		if err := tomlHeadersClose(text); err != nil {
			t.Errorf("a good config was refused: %q -> %v", text, err)
		}
	}
}

// And it has to catch the shape it exists for, wherever it sits in the file.
func TestTheTOMLCheckCatchesAHeaderThatNeverCloses(t *testing.T) {
	for _, text := range []string{
		"[mcp_servers.theirs\ncommand = \"x\"\n",
		"model = \"gpt-5\"\n\n[tools\n",
		"[a]\nx = 1\n[b\ny = 2\n",
		// the `#` inside the quoted key must not hide a missing closing bracket
		"[hooks.state.\"browser@openai-bundled:plugin.json#hooks[0]:subagent_stop:0:0\nenabled = true\n",
	} {
		if err := tomlHeadersClose(text); err == nil {
			t.Errorf("a broken header was accepted: %q", text)
		}
	}
}

// An install prints two counts in a row and they disagree: the parser's line
// says what it read, this one says what reached the index, and the difference
// is deja's own recall coming back through a transcript. Both are true, and
// nothing said why they differed (#3386).
func TestTheBuiltLineNamesWhatDidNotReachTheIndex(t *testing.T) {
	got := indexBuiltLine(index.BuildSummary{Sessions: 4, Messages: 6, Dropped: 1})
	if !strings.Contains(got, "4 sessions, 6 messages") {
		t.Errorf("the line lost its counts: %q", got)
	}
	if !strings.Contains(got, "1 of deja's own blocks not indexed") {
		t.Errorf("the line does not say where the seventh message went: %q", got)
	}

	// When the two agree there is nothing to explain, and a note about zero
	// blocks is noise on the screen a first install shows.
	if got := indexBuiltLine(index.BuildSummary{Sessions: 4, Messages: 7}); strings.Contains(got, "not indexed") {
		t.Errorf("the line explained a difference that is not there: %q", got)
	}
}

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
	if rerr := os.Remove(sharedSkillPath()); rerr != nil {
		t.Fatal(rerr)
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
	var sqlSb130 strings.Builder
	for i, r := range rows {
		sqlSb130.WriteString("insert into mcp_server (id, name, command, args) values ('" + string(rune('a'+i)) + "', '" + r[0] + "', '" + r[1] + "', '" + r[2] + "');")
	}
	sql += sqlSb130.String()
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
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
	for line := range strings.SplitSeq(buf.String(), "\n") {
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
	if row2 := cherryMCPRow(t); row2.State != "wired" || row2.BinaryMissing {
		t.Fatalf("imported server: %+v, want wired", row2)
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
	b, jerr := json.Marshal(row)
	if jerr != nil {
		t.Fatal(jerr)
	}
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
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, "update mcp_server set is_active = 0;").CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	if row := cherryMCPRow(t); row.State != "disabled" || row.Path != db {
		t.Fatalf("switched-off server: %+v, want disabled at the database", row)
	}
	if text := cherryMCPText(t); !strings.Contains(text, "disabled") || !strings.Contains(text, "turn it on") {
		t.Errorf("text row:\n%s", text)
	}
}

// Cherry Studio keeps its MCP servers in its own SQLite store, so there is no
// config file to write and writing into a running app's database is not an
// installer's job. What it has is Settings → MCP → Import from JSON, so deja
// writes the file that import takes and says where to point it — the shape the
// aider target already uses for a file the tool will not fetch itself (#3644).
func TestInstallCherryStudioWritesAnImportableServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")

	res, err := installCherryStudio("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatalf("install changed nothing: %+v", res)
	}
	if !strings.Contains(res.Note, "Settings") {
		t.Errorf("note = %q, want it to say where to import the file", res.Note)
	}

	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("import file: %v", err)
	}
	var cfg struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if uerr := json.Unmarshal(b, &cfg); uerr != nil {
		t.Fatalf("the import file is not JSON: %v", uerr)
	}
	srv, ok := cfg.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server in %v", cfg.Servers)
	}
	if srv.Type != "stdio" {
		t.Errorf("type = %q, want stdio", srv.Type)
	}
	if srv.Command == "" || len(srv.Args) == 0 || srv.Args[len(srv.Args)-1] != "mcp" {
		t.Errorf("entry = %q %v, want the binary and `mcp`", srv.Command, srv.Args)
	}

	// The note stays on a second run: the file being unchanged does not mean
	// anyone has imported it yet.
	again, err := installCherryStudio("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}
	if !strings.Contains(again.Note, "Import from JSON") {
		t.Errorf("the second run dropped the instruction: %q", again.Note)
	}

	out, err := installCherryStudio("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Action != "removed" {
		t.Errorf("uninstall = %q, want removed", out.Action)
	}
	if _, err := os.Stat(res.Path); err == nil {
		t.Errorf("the import file survived uninstall: %s", res.Path)
	}
}

// Cherry Studio's importer validates the pasted JSON with a strict schema, so
// a key it does not know fails the whole import rather than being ignored.
// Read out of the app bundle (2.0.14,
// out/renderer/assets/mcp-uMWTyAhi.js): `McpConfigSchema = object({ mcpServers:
// record(string(), McpServerConfigSchema) })`, where the server schema is
// `object({… type, command, args, env, headers, …}).strict()` and `type` is one
// of stdio, sse, streamableHttp, inMemory. The importer names the server after
// the key unless the entry carries its own `name`.
//
// So the file deja writes has to stay exactly these three keys under exactly
// that envelope. This test is the reminder, since nothing else here would fail
// if a fourth key were added (#3674).
func TestCherryStudioImportFileMatchesTheAppsStrictSchema(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if _, err := installCherryStudio("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cherryStudioImportPath())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]map[string]map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("the import file is not the shape the app parses: %v", err)
	}
	servers, ok := root["mcpServers"]
	if !ok || len(servers) != 1 {
		t.Fatalf("want one server under mcpServers, got %v", root)
	}
	entry, ok := servers["deja"]
	if !ok {
		t.Fatalf("the server is not keyed `deja`, so the app would name it something else: %v", servers)
	}
	allowed := map[string]bool{"type": true, "command": true, "args": true}
	for k := range entry {
		if !allowed[k] {
			t.Errorf("key %q is not in Cherry Studio's strict schema — the import would fail whole", k)
		}
	}
	if entry["type"] != "stdio" {
		t.Errorf("type = %v, want stdio (one of the four literals the schema accepts)", entry["type"])
	}
}

// Cherry Studio's Claude agents run with CLAUDE_CONFIG_DIR at
// <userData>/Data/Agents/.claude, and a moved store (boot-config.json) moves
// it. Every Claude hook deja wires goes there, and uninstall takes them out.
func TestInstallCherryStudioAutoWritesClaudeHooksWhereTheAppPointsClaude(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	moved := filepath.Join(home, "cherry-data")
	if err := os.MkdirAll(filepath.Join(moved, "Data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moved, "Data", "cherrystudio.sqlite"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	boot, err := json.Marshal(map[string]any{"app.user_data_path": map[string]string{"/Applications/Cherry Studio.app": moved}})
	if err != nil {
		t.Fatal(err)
	}
	writeFileMkdir(t, filepath.Join(home, ".cherrystudio", "boot-config.json"), string(boot))

	if _, err := installCherryStudioAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(moved, "Data", "Agents", ".claude", "settings.json")
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readFile(t, settings)), &root); err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	for _, h := range claudeHookWiring {
		blocks := root.Hooks[h.Event]
		if len(blocks) == 0 || len(blocks[0].Hooks) == 0 || !strings.Contains(blocks[0].Hooks[0].Command, h.Sub) {
			t.Errorf("%s does not run %s: %+v", h.Event, h.Sub, blocks)
		}
	}
	if _, err := installCherryStudioAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(settings); err == nil && strings.Contains(string(b), "hook-context") {
		t.Errorf("uninstall left the hooks:\n%s", b)
	}
}

// With ~/.claude read-only, claude-auto was reported refused after it had
// already wired the MCP server into ~/.claude.json, which lives outside that
// directory, and left a .bak beside it (#4560). The halves that can fail on the
// directory go first now.
func TestClaudeAutoRefusedOnAReadOnlyDirWritesNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the directory mode is not enforced here")
	}
	hermeticEnv(t)
	dir := sources.ClaudeConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	p := sources.ClaudeJSONPath()
	const seed = "{\n  \"numStartups\": 3\n}\n"
	if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--no-index", "--no-guidance"); err == nil {
		t.Fatal("install wrote into a read-only ~/.claude")
	}
	if got, _ := os.ReadFile(p); string(got) != seed {
		t.Errorf("a refused target wired ~/.claude.json:\n%s", got)
	}
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Errorf("a refused target left %s.bak", p)
	}
}

// And a read-only ~/.claude.json stops the target before ~/.claude is touched.
func TestClaudeAutoRefusedOnAReadOnlyClaudeJSONWritesNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file mode is not enforced here")
	}
	hermeticEnv(t)
	p := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--no-index", "--no-guidance"); err == nil {
		t.Fatal("install wrote through a read-only ~/.claude.json")
	}
	if _, err := os.Stat(sources.ClaudeConfigDir()); err == nil {
		t.Errorf("a refused target wrote into %s", sources.ClaudeConfigDir())
	}
}

// Claude Code deletes transcripts after cleanupPeriodDays, 30 unless set, and
// says nothing; install is where a person hears deja keeps them. The setting
// is read, never written.
func TestInstallSaysClaudeCodeDeletesTranscriptsAndDejaKeepsThem(t *testing.T) {
	cases := []struct {
		name, settings, want string
	}{
		{"no setting", ``, "Claude Code deletes transcripts after 30 days; deja keeps a copy of each one it indexes from now on"},
		{"a short period", `{"cleanupPeriodDays": 7}`, "Claude Code deletes transcripts after 7 days; deja keeps a copy of each one it indexes from now on"},
		{"a long period", `{"cleanupPeriodDays": 3650}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			cfg := filepath.Join(t.TempDir(), "profile")
			t.Setenv("CLAUDE_CONFIG_DIR", cfg)
			settings := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
			if c.settings != "" {
				if err := os.MkdirAll(cfg, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(settings, []byte(c.settings), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, err := captureRun(t, "install", "claude-code", "claude-auto", "--no-index")
			if err != nil {
				t.Fatal(err)
			}
			if c.want == "" {
				if strings.Contains(out, "deletes transcripts") {
					t.Errorf("a %s period got the line:\n%s", c.name, out)
				}
			} else if n := strings.Count(out, c.want); n != 1 {
				t.Errorf("the line is said %d times, want once:\n%s", n, out)
			}
			if b, _ := os.ReadFile(settings); strings.Contains(string(b), "cleanupPeriodDays") != strings.Contains(c.settings, "cleanupPeriodDays") {
				t.Errorf("install changed cleanupPeriodDays:\n%s", b)
			}
		})
	}
	// Another agent's install is not about Claude Code.
	hermeticEnv(t)
	out, err := captureRun(t, "install", "gemini", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deletes transcripts") {
		t.Errorf("a gemini install talked about Claude Code:\n%s", out)
	}
}

// Claude Code's settings.json is the one hook config that never collapsed a
// pile of deja's own entries. Every other writer keeps the first and drops the
// rest (qwen in #2745, cursor in #2691); this one adopted each of them in turn
// and rewrote them all into byte-identical copies, so an install "succeeded"
// and left the hook firing once per copy. One machine reached eight entries per
// event that way — every prompt paid for eight processes and got the same
// memory eight times (#3421).
func TestInstallCollapsesClaudeHooksOntoOne(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	entries := make([]string, 0, 8)
	for range 8 {
		entries = append(entries, `{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt"}]}`)
	}
	doubled := `{"hooks":{"UserPromptSubmit":[` + strings.Join(entries, ",") + `]}}`
	if err := os.WriteFile(path, []byte(doubled), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installTarget("claude-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readFile(t, path), "hook-prompt"); n != 1 {
		t.Fatalf("install left %d copies of the prompt hook:\n%s", n, readFile(t, path))
	}
}

// A line the reader wrote around deja's hook is theirs, and it already calls
// the hook: deja's own entry beside it is the duplicate, and the collapse must
// take that one rather than the wrapper.
func TestCollapseKeepsAReadersOwnLine(t *testing.T) {
	ours := `{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt"}]}`
	theirs := `{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt | tee /tmp/deja.log"}]}`
	// Either order: the file keeps whichever entry it was written in, and the
	// wrapper is the one to survive both times.
	for name, entries := range map[string][]string{
		"wrapper first": {theirs, ours},
		"wrapper last":  {ours, theirs},
	} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			doubled := `{"hooks":{"UserPromptSubmit":[` + strings.Join(entries, ",") + `]}}`
			if err := os.WriteFile(path, []byte(doubled), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installTarget("claude-auto", "/usr/local/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, path)
			if !strings.Contains(got, "tee /tmp/deja.log") {
				t.Fatalf("the collapse threw away a line deja did not write:\n%s", got)
			}
			if n := strings.Count(got, "hook-prompt"); n != 1 {
				t.Fatalf("the hook still runs %d times:\n%s", n, got)
			}
		})
	}
}

// Claude Code has no per-entry switch: 2.1.302 starts an mcpServers entry
// carrying `"disabled": true` and deja answers in every session. Install and
// doctor both called it switched off, the Kiro/Cursor reading of the key
// (#4303), so the person who wrote it was told recall was off while it ran.
// Claude's own switch is disabledMcpServers, which doctor reads (#4468).
func TestClaudeEntryDisabledFlagIsNotASwitch(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := sources.ClaudeJSONPath()
	body := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"],"disabled":true}}}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := installClaude(bin, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Note, "switched off") {
		t.Errorf("install note = %q, but Claude Code runs this entry", res.Note)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"disabled": true`) && !strings.Contains(string(b), `"disabled":true`) {
		t.Errorf("install dropped the person's own key:\n%s", b)
	}

	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		MCP []doctorMCPStatus `json:"mcp"`
	}
	if uerr := json.Unmarshal([]byte(out), &report); uerr != nil {
		t.Fatalf("doctor --json: %v: %s", uerr, out)
	}
	seen := false
	for _, r := range report.MCP {
		if r.Name != "claude-code" {
			continue
		}
		seen = true
		if r.State != "wired" || r.SwitchedOff {
			t.Errorf("claude-code row = %+v, want wired and not switched_off", r)
		}
	}
	if !seen {
		t.Fatalf("no claude-code row:\n%s", out)
	}
	text, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "the entry is switched off — claude-code") {
		t.Errorf("the text report calls the entry off:\n%s", text)
	}

	// A ~/.claude.json with a trailing comma goes through the JSONC writer.
	jsonc := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"],"disabled":true,},}}`
	if werr := os.WriteFile(cfg, []byte(jsonc), 0o644); werr != nil {
		t.Fatal(werr)
	}
	res, err = installClaude(bin, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Note, "switched off") {
		t.Errorf("jsonc install note = %q, but Claude Code runs this entry", res.Note)
	}

	// The same key still switches Kiro off.
	kiro := kiroMCPSettingsPath()
	if err := os.MkdirAll(filepath.Dir(kiro), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kiro, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !dejaEntrySwitchedOff("kiro", kiro) {
		t.Error("kiro: a disabled entry no longer reads as switched off")
	}
}

func TestInstallClawCodeInstallIdempotentAndUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAW_CONFIG_HOME", "")

	settingsPath := filepath.Join(home, ".claw", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "model": "claude-sonnet-4-6",
  "mcpServers": {
    "other": {"command": "other-mcp"}
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installClawCode("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	firstBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	if err = json.Unmarshal(firstBytes, &parsed); err != nil {
		t.Fatalf("parse settings after install: %v", err)
	}
	if parsed["model"] != "claude-sonnet-4-6" {
		t.Fatalf("lost unrelated setting model: %#v", parsed["model"])
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if servers["other"] == nil || servers["deja"] == nil {
		t.Fatalf("expected both other and deja in mcpServers, got %#v", servers)
	}

	skillPath := filepath.Join(home, ".claw", "skills", "deja-history", "SKILL.md")
	if _, err = os.Stat(skillPath); err != nil {
		t.Fatalf("missing skill file %s: %v", skillPath, err)
	}
	cmdPath := filepath.Join(home, ".claw", "commands", "deja.md")
	if _, err = os.Stat(cmdPath); err != nil {
		t.Fatalf("missing command file %s: %v", cmdPath, err)
	}

	if _, err = installClawCode("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	secondBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("installClawCode not idempotent:\n--- first ---\n%s\n--- second ---\n%s", firstBytes, secondBytes)
	}

	if _, err = installClawCode("/usr/local/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	afterUninstall, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var cleaned map[string]any
	if err := json.Unmarshal(afterUninstall, &cleaned); err != nil {
		t.Fatalf("parse settings after uninstall: %v", err)
	}
	if cleaned["model"] != "claude-sonnet-4-6" {
		t.Fatalf("lost model after uninstall: %#v", cleaned["model"])
	}
	cleanedServers, _ := cleaned["mcpServers"].(map[string]any)
	if cleanedServers["other"] == nil || cleanedServers["deja"] != nil {
		t.Fatalf("expected other kept and deja removed, got %#v", cleanedServers)
	}
	if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
		t.Fatalf("expected skill file removed, stat err=%v", err)
	}
	if _, err := os.Stat(cmdPath); !os.IsNotExist(err) {
		t.Fatalf("expected command file removed, stat err=%v", err)
	}
}

func TestInstallClawCodeAutoRespectsEnvAndPreservesHooks(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(t.TempDir(), "custom-claw")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAW_CONFIG_HOME", cfgDir)

	settingsPath := filepath.Join(cfgDir, "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "echo keep"}]
      }
    ]
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installClawCodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install auto: %v", err)
	}
	firstBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(firstBytes)
	for _, want := range []string{"echo keep", "hook-tool", "hook-tool-after", `"PreToolUse"`, `"PostToolUse"`, `"PostToolUseFailure"`, `"mcpServers"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in settings.json, got:\n%s", want, text)
		}
	}
	if _, err = os.Stat(filepath.Join(home, ".claw", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("should use CLAW_CONFIG_HOME, not ~/.claw")
	}

	if _, err = installClawCodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("re-install auto: %v", err)
	}
	secondBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("installClawCodeAuto not idempotent:\n--- first ---\n%s\n--- second ---\n%s", firstBytes, secondBytes)
	}

	if _, err = installClawCodeAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatalf("uninstall auto: %v", err)
	}
	afterBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	afterText := string(afterBytes)
	if !strings.Contains(afterText, "echo keep") {
		t.Fatalf("expected unrelated hook preserved after uninstall, got:\n%s", afterText)
	}
	for _, unwanted := range []string{"hook-tool", "hook-tool-after", `"deja"`} {
		if strings.Contains(afterText, unwanted) {
			t.Fatalf("expected %q removed after uninstall, got:\n%s", unwanted, afterText)
		}
	}
}

func TestInstallClawCodeErrorBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	badCfg := filepath.Join(t.TempDir(), "bad-json")
	if err := os.MkdirAll(badCfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badCfg, "settings.json"), []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAW_CONFIG_HOME", badCfg)
	if _, err := installClawCode("/usr/local/bin/deja", false); err == nil {
		t.Fatal("expected error from installClawCode with invalid settings.json")
	}
	if _, err := installClawCodeAuto("/usr/local/bin/deja", false); err == nil {
		t.Fatal("expected error from installClawCodeAuto with invalid settings.json")
	}

	blockSkill := filepath.Join(t.TempDir(), "block-skill")
	if err := os.MkdirAll(blockSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockSkill, "skills"), []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAW_CONFIG_HOME", blockSkill)
	if _, err := installClawCode("/usr/local/bin/deja", false); err == nil {
		t.Fatal("expected error when skills path is a file")
	}

	blockCmd := filepath.Join(t.TempDir(), "block-cmd")
	if err := os.MkdirAll(blockCmd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockCmd, "commands"), []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAW_CONFIG_HOME", blockCmd)
	if _, err := installClawCode("/usr/local/bin/deja", false); err == nil {
		t.Fatal("expected error when commands path is a file")
	}
}

// cline's PreToolUse hook cannot carry context — it keeps only cancel and
// overrideInput — so the repair for a command that just failed had no channel.
// The message builder is the one that works: it is handed the messages on their
// way to the model, including the failed command's tool_result, and its return
// is what gets sent (verified on CLI 3.0.57 by reading the wire request). So the
// repair rides beside the failing result in the same turn.
func TestClineFixPairSource(t *testing.T) {
	js := clinePluginJS("/bin/deja")
	for _, want := range []string{
		`"hook-tool-after", "--plain"`,
		"commandFailure",
		"appendRepair",
		"run_commands",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("the fix-pair path is missing %q:\n%s", want, js)
		}
	}
}

// The builder appends the repair to the failing command's tool_result and
// leaves a result that did not fail alone. Driven the way cline drives it.
func TestClineFixPairAppendsToTheFailingResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin cline would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	// Answers only hook-tool-after, and only for the seeded error, so the
	// no-failure case is checked on the same stub.
	script := "#!/bin/sh\nin=$(cat)\ncase \"$1\" in\n" +
		"hook-tool-after) case \"$in\" in *glimwrax*) printf 'RAN NEXT: glimwraxctl --sync' ;; esac ;;\n" +
		"esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "index.mjs")
	if werr := os.WriteFile(plugin, []byte(clinePluginJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import plugin from "` + plugin + `";
let build;
const api = {
  registerMessageBuilder: (b) => { build = b.build; },
  registerRule: () => {}, registerCommand: () => {},
};
plugin.setup(api, { session: { sessionId: "s1" } });

// A turn whose last message is a failed run_commands result, and one that
// succeeded, in the same array.
const messages = [
  { role: "user", content: [{ type: "text", text: "build the project" }] },
  { role: "assistant", content: [{ type: "tool_use", id: "call_ok", name: "run_commands", input: { commands: ["ls"] } }] },
  { role: "user", content: [{ type: "tool_result", tool_use_id: "call_ok", name: "run_commands",
      content: [{ query: "ls", result: "main.go", success: true }] }] },
  { role: "assistant", content: [{ type: "tool_use", id: "call_1", name: "run_commands", input: { commands: ["go build ./..."] } }] },
  { role: "user", content: [{ type: "tool_result", tool_use_id: "call_1", name: "run_commands",
      content: [{ query: "go build ./...", result: "./main.go:9:2: undefined: glimwraxHelper", error: "exit 1", success: false }] }] },
];
const out = build(messages);
if (!out) { console.log("NOEDIT"); process.exit(0); }
const failing = out.find((m) => Array.isArray(m.content) && m.content.some((p) => p.type === "tool_result" && p.tool_use_id === "call_1"));
const ok = out.find((m) => Array.isArray(m.content) && m.content.some((p) => p.type === "tool_result" && p.tool_use_id === "call_ok"));
const failText = JSON.stringify(failing);
const okText = JSON.stringify(ok);
console.log("FAIL_HAS_REPAIR:" + failText.includes("RAN NEXT: glimwraxctl --sync"));
console.log("OK_UNTOUCHED:" + !okText.includes("RAN NEXT"));
// Original error still present, not replaced.
console.log("ERROR_KEPT:" + failText.includes("undefined: glimwraxHelper"));
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	s := string(out)
	if strings.Contains(s, "NOEDIT") {
		t.Fatal("the builder returned no edit, so the repair never reached the failing result")
	}
	for _, want := range []string{"FAIL_HAS_REPAIR:true", "OK_UNTOUCHED:true", "ERROR_KEPT:true"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in driver output:\n%s", want, s)
		}
	}
}

// The plugin's message builder is the only prompt-aware channel cline has, and
// its contract is narrow enough that reading the source proves nothing: cline
// calls build more than once for one prompt, discards a mutated argument, and
// sends only the array returned from the last call. So the plugin is loaded and
// driven the way cline drives it, with a stub standing in for deja.
func TestClinePluginInjectsRecallForThePrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin cline would run")
	}
	home := t.TempDir()

	// Records its calls so the double invocation is visible, and answers the
	// way the prompt hook answers when history matches.
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$* $in\" >> " + calls + "\n" +
		"case \"$1\" in hook-prompt) printf 'RECALLED the token rotates every 47 days' ;; esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}

	plugin := filepath.Join(home, "index.mjs")
	if werr := os.WriteFile(plugin, []byte(clinePluginJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}

	// Two builds of the same prompt, as cline does: the second is the one that
	// is sent.
	driver := `
import plugin from ` + jsString(plugin) + `;
let build;
const api = {
  registerMessageBuilder: (b) => { build = b.build },
  registerRule: () => {},
  registerCommand: () => {},
};
plugin.setup(api, { session: { sessionId: "s1" } });
const messages = () => ([{ role: "user",
  content: [{ type: "text", text: '<user_input mode="act">how often does the token rotate?</user_input>' }] }]);
build(messages());
const out = build(messages());
const text = JSON.stringify(out);
console.log(text.includes("RECALLED") ? "INJECTED" : "MISSING");
console.log(text.includes("how often does the token rotate?") ? "KEPT" : "LOST");
// Printed as text: node colours a bare number.
console.log(String(out ? out.filter((m) => m.role === "user").length : 0));
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	lines := strings.Fields(string(out))
	if len(lines) < 3 {
		t.Fatalf("driver said %q", out)
	}
	if lines[0] != "INJECTED" {
		t.Errorf("the recall never reached the message cline sends:\n%s", out)
	}
	if lines[1] != "KEPT" {
		t.Errorf("injecting the recall dropped what the user typed:\n%s", out)
	}
	// Two user messages in a row is what providers with strict alternation
	// reject, so the recall has to ride inside the existing one.
	if lines[2] != "1" {
		t.Errorf("the recall was added as a second user message (%s of them)", lines[2])
	}

	body, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the plugin never ran deja: %v", err)
	}
	asked := strings.Count(string(body), "hook-prompt")
	if asked != 1 {
		t.Errorf("deja was asked %d times for one prompt; cline calls build "+
			"repeatedly and the answer has to be cached", asked)
	}
	if !strings.Contains(string(body), "how often does the token rotate?") {
		t.Errorf("the prompt was not passed to the hook:\n%s", body)
	}
	if strings.Contains(string(body), "user_input") {
		t.Errorf("cline's mode wrapper was searched for as if it were words the "+
			"user typed:\n%s", body)
	}
}

// Under --yolo, Cline 3.0.69 appends its own "[SYSTEM] This run is not
// complete..." as a second user message. The recall has to be asked for the
// person's prompt, not that reminder (stand, 07.10).
func TestClinePluginSkipsClinesOwnSystemReminder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin cline would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$* $in\" >> " + calls + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err := os.WriteFile(plugin, []byte(clinePluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from ` + jsString(plugin) + `;
let build;
plugin.setup({ registerMessageBuilder: (b) => { build = b.build }, registerRule() {}, registerCommand() {} },
  { session: { sessionId: "s1" } });
build([
  { role: "user", content: [{ type: "text", text: "why does the deploy job time out?" }] },
  { role: "user", content: [{ type: "text", text: "[SYSTEM] This run is not complete until you call one of these terminal completion tools: submit_and_exit." }] },
]);
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput(); err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	body, _ := os.ReadFile(calls)
	var prompt string
	for line := range strings.SplitSeq(string(body), "\n") {
		if strings.HasPrefix(line, "hook-prompt") {
			prompt = line
		}
	}
	if !strings.Contains(prompt, "deploy job time out") || strings.Contains(prompt, "[SYSTEM]") {
		t.Errorf("hook-prompt was asked %q, want the person's question", prompt)
	}
}

// jsString quotes a path for embedding in a module specifier.
func jsString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// cline-auto writes the MCP entry first and the plugin second. With
// ~/.cline/plugins read-only the plugin was refused after the entry was
// already in cline_mcp_settings.json, so the target reported a refusal while
// half wired (#5203).
func TestClineAutoRefusesAnUnwritablePluginDirBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions that bind the test user")
	}
	hermeticEnv(t)
	mcp := sources.ClineMCPSettingsPath()
	seed := []byte(`{"mcpServers": {"zz-mine": {"command": "my-server"}}}` + "\n")
	reseed := func(t *testing.T) {
		t.Helper()
		_ = os.RemoveAll(sources.ClinePluginsDir())
		if err := os.MkdirAll(filepath.Dir(mcp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mcp, seed, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lock := func(t *testing.T, dirs ...string) {
		t.Helper()
		for _, d := range dirs {
			if err := os.Chmod(d, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(d, 0o755) })
		}
	}
	pluginDir := func() string { return filepath.Join(sources.ClinePluginsDir(), "deja") }

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) []string
	}{
		{"plugins dir", func(t *testing.T) []string {
			t.Helper()
			if err := os.MkdirAll(sources.ClinePluginsDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			return []string{sources.ClinePluginsDir()}
		}},
		// An older install already in place, its directory locked since: the
		// new plugin text has to go in and cannot.
		{"older plugin", func(t *testing.T) []string {
			t.Helper()
			if err := os.MkdirAll(pluginDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pluginDir(), "index.js"), []byte("// an older deja plugin\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return []string{pluginDir()}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reseed(t)
			lock(t, tc.setup(t)...)
			_, err := captureRun(t, "install", "cline-auto", "--no-index")
			if err == nil {
				t.Fatal("install over a read-only plugin directory succeeded")
			}
			if !strings.Contains(err.Error(), "cline-auto") {
				t.Errorf("refusal does not name the target: %v", err)
			}
			if got, _ := os.ReadFile(mcp); !bytes.Equal(got, seed) {
				t.Errorf("cline_mcp_settings.json written before the plugin was refused:\n%s", got)
			}
		})
	}

	// Controls: a writable directory installs both halves, and a reinstall
	// that changes nothing in a locked plugin goes through — also when the
	// launcher is stale but its file writable, since it is rewritten in place.
	t.Run("writable", func(t *testing.T) {
		reseed(t)
		if _, err := captureRun(t, "install", "cline-auto", "--no-index"); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(mcp); bytes.Equal(got, seed) {
			t.Error("install left cline_mcp_settings.json without deja")
		}
		if _, err := os.Stat(filepath.Join(pluginDir(), "index.js")); err != nil {
			t.Errorf("install wrote no plugin: %v", err)
		}
	})
	t.Run("unchanged reinstall", func(t *testing.T) {
		reseed(t)
		if _, err := captureRun(t, "install", "cline-auto", "--no-index"); err != nil {
			t.Fatal(err)
		}
		launcher := dejaLauncherPath()
		if err := os.WriteFile(launcher, []byte("#!/bin/sh\n# an older launcher\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(mcp)
		skill := filepath.Join(pluginDir(), "skills", "deja-history")
		lock(t, skill, filepath.Dir(skill), pluginDir(), filepath.Dir(launcher))
		if _, err := captureRun(t, "install", "cline-auto", "--no-index"); err != nil {
			t.Fatalf("reinstall over an unchanged locked plugin refused: %v", err)
		}
		if got, _ := os.ReadFile(mcp); !bytes.Equal(got, before) {
			t.Errorf("reinstall changed cline_mcp_settings.json:\n%s", got)
		}
		if got, _ := os.ReadFile(launcher); bytes.Contains(got, []byte("an older launcher")) {
			t.Errorf("stale launcher not rewritten:\n%s", got)
		}
	})
}

// Uninstall removed the three files and then failed to unlink the emptied
// directory from a read-only plugins/, so the target was refused with the
// plugin already gone. Now the files go, the empty directory may stay, and the
// uninstall succeeds (#5300).
func TestClineAutoUninstallWithAReadOnlyPluginsDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions that bind the test user")
	}
	for _, tc := range []struct {
		name     string
		userFile bool
	}{
		{"no user files", false},
		{"user file", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			t.Setenv("CLINE_MCP_SETTINGS_PATH", "")
			dir, entry, manifest, skill := clineTestPaths(t)
			if out, err := captureRun(t, "install", "cline-auto", "--no-index", "--no-guidance"); err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			user := filepath.Join(dir, "notes", "todo.md")
			if tc.userFile {
				writeClineTestFile(t, user, "mine\n")
			}
			plugins := sources.ClinePluginsDir()
			if err := os.Chmod(plugins, 0o555); err != nil {
				t.Fatalf("lock %s: %v", plugins, err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(plugins, 0o755); err != nil {
					t.Errorf("restore %s mode: %v", plugins, err)
				}
			})
			out, err := captureRun(t, "uninstall", "cline-auto", "--no-index", "--no-guidance")
			if err != nil {
				t.Fatalf("uninstall over a read-only plugins dir was refused: %v\n%s", err, out)
			}
			if strings.Contains(out, "refused") {
				t.Errorf("uninstall over a read-only plugins dir reported a refusal:\n%s", out)
			}
			requireClineTestGone(t, entry, manifest, skill)
			if tc.userFile {
				requireClineTestFile(t, user, "mine\n")
				return
			}
			requireClineTestEmptyDir(t, dir)
		})
	}
}

// The mirror of the test above: a file that really cannot be removed is a
// refusal, not an unchanged plugin.
func TestClineUninstallReportsAnUnwritablePluginDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions that bind the test user")
	}
	hermeticEnv(t)
	dir, entry, _, _ := clineTestPaths(t)
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("lock %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("restore %s mode: %v", dir, err)
		}
	})
	if _, err := installClineAuto("/bin/deja", true); err == nil {
		t.Fatal("uninstall reported success with a file it could not remove")
	} else if !strings.Contains(err.Error(), entry) {
		t.Errorf("the error does not name %s: %v", entry, err)
	}
	if _, err := os.Lstat(entry); err != nil {
		t.Errorf("the file reported as unremovable is gone: %v", err)
	}
}

func TestInstallClineWritesAPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Plugins sit beside the data directory, not inside it: a plugin under
	// ~/.cline/data/plugins is never discovered.
	path := filepath.Join(home, ".cline", "plugins", "deja", "index.js")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plugin not written where cline looks: %v", err)
	}
	js := string(b)
	// The registry validates the manifest before setup runs and rejects
	// contributions that were not declared.
	for _, want := range []string{`capabilities: ["rules", "commands", "skills", "hooks"]`, "registerRule", "registerCommand"} {
		if !strings.Contains(js, want) {
			t.Fatalf("plugin missing %q:\n%s", want, js)
		}
	}
	// A string here would freeze whatever history existed at install time.
	// Only the function form is resolved per session.
	if !strings.Contains(js, "content: () =>") {
		t.Fatalf("rule content is not lazy, recall would never update:\n%s", js)
	}
	if !strings.Contains(js, `hook-context`) {
		t.Fatalf("plugin does not call the context hook:\n%s", js)
	}
}

// Cline hooks look like the obvious channel and are not one: their stdout is
// closed and no hook output can carry context. Writing one would leave a dead
// integration that looks installed.
func TestInstallClineWritesNoHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, dir := range []string{
		filepath.Join(home, ".cline", "hooks"),
		filepath.Join(home, "Documents", "Cline", "Hooks"),
	} {
		if _, err := os.Stat(dir); err == nil {
			t.Fatalf("install created %s; cline hook output is discarded", dir)
		}
	}
}

func TestInstallClineIsIdempotentAndRemovable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	first, err := installClineAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if first.Action != "created" {
		t.Fatalf("first install action = %q", first.Action)
	}
	again, err := installClineAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if again.Action != "unchanged" {
		t.Fatalf("reinstall rewrote the plugin: %q", again.Action)
	}
	if _, err = installClineAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err = os.Stat(filepath.Join(home, ".cline", "plugins", "deja")); !os.IsNotExist(err) {
		t.Fatalf("plugin survived uninstall: %v", err)
	}
	second, err := installClineAuto("/bin/deja", true)
	if err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
	if second.Action != "unchanged" {
		t.Fatalf("second uninstall action = %q, want unchanged", second.Action)
	}
}

// Cline loads a plugin's skills/ and takes its name only from a package.json
// whose cline.plugins lists the entry file. Without one the deja-history skill
// was never loaded and Settings > Plugins listed the plugin as "index" (#4316).
func TestInstallClineWritesAPackageManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(home, ".cline", "plugins", "deja")
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatalf("no package.json beside the plugin: %v", err)
	}
	var pkg struct {
		Name  string `json:"name"`
		Cline struct {
			Plugins []string `json:"plugins"`
		} `json:"cline"`
	}
	if uerr := json.Unmarshal(b, &pkg); uerr != nil {
		t.Fatalf("package.json: %v\n%s", uerr, b)
	}
	if pkg.Name != "deja" || len(pkg.Cline.Plugins) != 1 || pkg.Cline.Plugins[0] != "./index.js" {
		t.Fatalf("package.json does not name the plugin entry: %s", b)
	}
	again, err := installClineAuto("/bin/deja", false)
	if err != nil || again.Action != "unchanged" {
		t.Fatalf("reinstall: %q %v", again.Action, err)
	}
	if _, err := installClineAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plugin directory survived uninstall: %v", err)
	}
}

// The skill install writes for Cline is guidance, and doctor said
// "unsupported" about it before and after install (#4317).
func TestGuidanceStatusSeesTheClineSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if got := guidanceStatus("cline"); got != "missing" {
		t.Fatalf("before install: guidance %q, want missing", got)
	}
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, h := range []string{"cline", "cline-auto"} {
		if got := guidanceStatus(h); got != "written" {
			t.Fatalf("after install: %s guidance %q, want written", h, got)
		}
	}
}

// clineTestPaths names the three files install writes, so a test says which one
// survived rather than repeating the layout.
func clineTestPaths(t *testing.T) (dir, entry, manifest, skill string) {
	t.Helper()
	dir = filepath.Join(sources.ClinePluginsDir(), "deja")
	entry = filepath.Join(dir, "index.js")
	manifest = filepath.Join(dir, "package.json")
	skill = filepath.Join(dir, "skills", "deja-history", "SKILL.md")
	return dir, entry, manifest, skill
}

func writeClineTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func requireClineTestFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func requireClineTestGone(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived uninstall (stat: %v)", path, err)
		}
	}
}

func requireClineTestEmptyDir(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s is not empty: %v", path, entries)
	}
}

// resetClineTestRunRecord stands in for a new deja process: what the install
// run made is known from wiring.json, not from this process's memory.
func resetClineTestRunRecord() {
	createdDirsByThisRun, createdByThisRun, snapshotsByThisRun = nil, nil, nil
	blocksAddedThisRun, blocksForgottenThisRun = nil, map[string]bool{}
}

// Uninstall used to RemoveAll the plugin directory, so a note or any other
// file the reader kept beside deja's three went with it (#5300). The SKILL.md
// backup is the one file beside them that does not stay a backup: uninstall
// puts back the copy install replaced, the same rule every other skill
// uninstall follows (#5588).
func TestClineUninstallKeepsFilesBesideThePlugin(t *testing.T) {
	hermeticEnv(t)
	dir, entry, manifest, skill := clineTestPaths(t)
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	stays := map[string]string{
		filepath.Join(dir, "notes", "todo.md"):                   "mine\n",
		filepath.Join(dir, "mine.txt"):                           "a file only the reader wrote\n",
		filepath.Join(dir, "index.js.bak"):                       "an older plugin\n",
		filepath.Join(dir, "skills", "deja-history", "RULES.md"): "the reader's own file\n",
	}
	for path, body := range stays {
		writeClineTestFile(t, path, body)
	}
	// Planted after install, the .bak carries no replaced file of the
	// reader's in this test's history — but restoreReplacedFile is
	// provenance-blind by design (#2581, #2600): a copy beside the skill that
	// is not deja's goes back, which is what a reader's backup is.
	writeClineTestFile(t, skill+".bak", "the reader's copy\n")
	res, err := installClineAuto("/bin/deja", true)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	// The summary action names the plugin files it removed; the restore is
	// what the file state below says, the same split the stdout line gives.
	if res.Action != "removed" {
		t.Fatalf("uninstall action = %q, want removed", res.Action)
	}
	requireClineTestGone(t, entry, manifest)
	requireClineTestFile(t, skill, "the reader's copy\n")
	if _, err := os.Stat(skill + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("SKILL.md.bak still present after restore: %v", err)
	}
	for path, body := range stays {
		requireClineTestFile(t, path, body)
	}
}

// The copy install kept of the skill it replaced is the reader's own file:
// uninstall puts it back instead of leaving it as a .bak nobody restores
// while the SKILL.md itself is gone (#5588).
func TestClineUninstallPutsBackTheReplacedSkill(t *testing.T) {
	hermeticEnv(t)
	_, entry, manifest, skill := clineTestPaths(t)
	writeClineTestFile(t, skill, "mine\n")
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	requireClineTestFile(t, skill+".bak", "mine\n")
	res, err := installClineAuto("/bin/deja", true)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.Action == "unchanged" {
		t.Fatalf("uninstall action = %q, want a removal or a restore", res.Action)
	}
	requireClineTestGone(t, entry, manifest)
	requireClineTestFile(t, skill, "mine\n")
	if _, err := os.Stat(skill + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("SKILL.md.bak still present after restore: %v", err)
	}
}

// Another skill and extra files inside deja-history are the reader's: only the
// SKILL.md deja wrote goes, and the directories that still hold something stay.
func TestClineUninstallKeepsExtraSkills(t *testing.T) {
	hermeticEnv(t)
	dir, entry, manifest, skill := clineTestPaths(t)
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	kept := map[string]string{
		filepath.Join(dir, "skills", "deja-history", "notes.md"): "extra in deja-history\n",
		filepath.Join(dir, "skills", "mine", "SKILL.md"):         "---\nname: mine\n---\n",
	}
	for path, body := range kept {
		writeClineTestFile(t, path, body)
	}
	if _, err := installClineAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	requireClineTestGone(t, entry, manifest, skill)
	for path, body := range kept {
		requireClineTestFile(t, path, body)
	}
}

// A directory the reader had before install is theirs even when uninstall
// leaves it empty; only the empty tree deja recorded making is pruned (#3239).
func TestClineUninstallKeepsDirectoriesItDidNotCreate(t *testing.T) {
	t.Run("plugin directory", func(t *testing.T) {
		hermeticEnv(t)
		dir, entry, manifest, skill := clineTestPaths(t)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := installClineAuto("/bin/deja", false); err != nil {
			t.Fatalf("install: %v", err)
		}
		if _, err := installClineAuto("/bin/deja", true); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
		requireClineTestGone(t, entry, manifest, skill)
		requireClineTestEmptyDir(t, dir)
	})

	t.Run("skill directory", func(t *testing.T) {
		hermeticEnv(t)
		dir, entry, manifest, skill := clineTestPaths(t)
		skillDir := filepath.Dir(skill)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := installClineAuto("/bin/deja", false); err != nil {
			t.Fatalf("install: %v", err)
		}
		if _, err := installClineAuto("/bin/deja", true); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
		requireClineTestGone(t, entry, manifest, skill)
		for _, path := range []string{dir, filepath.Dir(skillDir), skillDir} {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("pre-created directory %s was removed: %v", path, err)
			}
		}
		requireClineTestEmptyDir(t, skillDir)
	})

	// Control: the same uninstall does take back the tree deja recorded.
	t.Run("installed tree", func(t *testing.T) {
		hermeticEnv(t)
		dir, entry, manifest, skill := clineTestPaths(t)
		if _, err := installClineAuto("/bin/deja", false); err != nil {
			t.Fatalf("install: %v", err)
		}
		if _, err := installClineAuto("/bin/deja", true); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
		requireClineTestGone(t, entry, manifest, skill)
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("recorded plugin directory survived uninstall: %v", err)
		}
	})
}

// A partial install, or a second uninstall, still removes whichever of the
// three files are there and never claims one that was already missing.
func TestClineUninstallHandlesMissingPluginFiles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing []string
	}{
		{"entry", []string{"index"}},
		{"manifest", []string{"package"}},
		{"skill", []string{"skill"}},
		{"all three", []string{"index", "package", "skill"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			dir, entry, manifest, skill := clineTestPaths(t)
			if _, err := installClineAuto("/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			user := filepath.Join(dir, "notes", "todo.md")
			writeClineTestFile(t, user, "mine\n")
			paths := map[string]string{"index": entry, "package": manifest, "skill": skill}
			missing := map[string]bool{}
			for _, name := range tc.missing {
				missing[name] = true
				if err := os.Remove(paths[name]); err != nil {
					t.Fatalf("remove %s: %v", paths[name], err)
				}
			}
			res, err := installClineAuto("/bin/deja", true)
			if err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			for name, path := range paths {
				if !missing[name] {
					requireClineTestGone(t, path)
				}
			}
			requireClineTestFile(t, user, "mine\n")
			if len(tc.missing) == len(paths) {
				if res.Action != "unchanged" {
					t.Fatalf("uninstall of nothing action = %q, want unchanged", res.Action)
				}
				if res.Path != entry {
					t.Errorf("uninstall of nothing names %s, want the entry %s", res.Path, entry)
				}
				return
			}
			if res.Action != "removed" {
				t.Fatalf("uninstall action = %q, want removed", res.Action)
			}
			if missing["index"] && res.Path == entry {
				t.Errorf("uninstall claimed the missing entry was removed: %s", res.Path)
			}
		})
	}
}

// CLINE_DIR moves the plugin directory, and an uninstall there must not touch
// the default location.
func TestClineUninstallHonorsCLINE_DIR(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	custom := filepath.Join(t.TempDir(), "cline")
	t.Setenv("CLINE_DIR", custom)
	dir, entry, manifest, skill := clineTestPaths(t)
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	user := filepath.Join(dir, "notes", "todo.md")
	writeClineTestFile(t, user, "mine\n")
	sentinel := filepath.Join(home, ".cline", "plugins", "deja", "index.js")
	writeClineTestFile(t, sentinel, "not deja's\n")
	if _, err := installClineAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	requireClineTestGone(t, entry, manifest, skill)
	requireClineTestFile(t, user, "mine\n")
	requireClineTestFile(t, sentinel, "not deja's\n")
}

// A dotfiles-style link is a layout install already accepts. Uninstall must
// remove the files through the same link rather than refusing the root and
// leaving Cline with a plugin whose MCP entry is gone.
func TestClineAutoUninstallThroughASymlinkedPluginDir(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("CLINE_MCP_SETTINGS_PATH", "")
	mcp := sources.ClineMCPSettingsPath()
	seed := []byte(`{"mcpServers": {"zz-mine": {"command": "my-server"}}}` + "\n")
	if err := os.MkdirAll(filepath.Dir(mcp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcp, seed, 0o644); err != nil {
		t.Fatal(err)
	}

	plugins := sources.ClinePluginsDir()
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(t.TempDir(), "dotfiles", "cline-deja")
	userNote := filepath.Join(realDir, "notes", "todo.md")
	otherSkill := filepath.Join(realDir, "skills", "other", "SKILL.md")
	writeClineTestFile(t, userNote, "mine\n")
	writeClineTestFile(t, otherSkill, "another skill\n")

	dir := filepath.Join(plugins, "deja")
	if err := os.Symlink(realDir, dir); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatalf("symlink %s to %s: %v", dir, realDir, err)
	}
	_, entry, manifest, skill := clineTestPaths(t)

	out, err := captureRun(t, "install", "cline-auto", "--no-index")
	if err != nil {
		t.Fatalf("install through %s: %v\n%s", dir, err, out)
	}
	for _, path := range []string{entry, manifest, skill} {
		if _, err = os.Stat(path); err != nil {
			t.Fatalf("install did not write through the link: %s: %v", path, err)
		}
	}

	out, err = captureRun(t, "uninstall", "cline-auto", "--no-index")
	if err != nil {
		t.Fatalf("uninstall through %s: %v\n%s", dir, err, out)
	}
	if strings.Contains(out, "refused") {
		t.Errorf("uninstall through a plugin link reported a refusal:\n%s", out)
	}
	requireClineTestGone(t, entry, manifest, skill)
	requireClineTestFile(t, userNote, "mine\n")
	requireClineTestFile(t, otherSkill, "another skill\n")

	linkInfo, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("plugin link disappeared: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("plugin path is no longer a symlink: %v", linkInfo.Mode())
	}
	target, err := os.Readlink(dir)
	if err != nil {
		t.Fatalf("read plugin link: %v", err)
	}
	if filepath.Clean(target) != filepath.Clean(realDir) {
		t.Errorf("plugin link points at %s, want %s", target, realDir)
	}

	b, err := os.ReadFile(mcp)
	if err != nil {
		t.Fatalf("read %s: %v", mcp, err)
	}
	var root struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("settings %s: %v\n%s", mcp, err, b)
	}
	if _, ok := root.MCPServers["zz-mine"]; !ok {
		t.Errorf("uninstall removed the reader's own MCP server:\n%s", b)
	}
	if _, ok := root.MCPServers["deja"]; ok {
		t.Errorf("uninstall left deja's MCP entry:\n%s", b)
	}

	if out, err := captureRun(t, "uninstall", "cline-auto", "--no-index"); err != nil {
		t.Fatalf("second uninstall through %s: %v\n%s", dir, err, out)
	} else if strings.Contains(out, "refused") {
		t.Errorf("second uninstall through a plugin link reported a refusal:\n%s", out)
	}
	requireClineTestFile(t, userNote, "mine\n")
	requireClineTestFile(t, otherSkill, "another skill\n")
	if _, err := os.Lstat(dir); err != nil {
		t.Errorf("plugin link disappeared after the second uninstall: %v", err)
	}
}

// The install run's in-memory record is gone by the time a later process
// uninstalls; wiring.json still has to vouch for the empty directories. The
// MCP uninstall keeps the reader's own server (#5300).
func TestClineAutoUninstallUsesThePersistedDirectoryRecord(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("CLINE_MCP_SETTINGS_PATH", "")
	mcp := sources.ClineMCPSettingsPath()
	seed := []byte(`{"mcpServers": {"zz-mine": {"command": "my-server"}}}` + "\n")
	if err := os.MkdirAll(filepath.Dir(mcp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcp, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "cline-auto", "--no-index", "--no-guidance")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	dir, entry, manifest, skill := clineTestPaths(t)
	for _, path := range []string{entry, manifest, skill} {
		if _, err = os.Stat(path); err != nil {
			t.Fatalf("install did not write %s: %v", path, err)
		}
	}
	resetClineTestRunRecord()
	out, err = captureRun(t, "uninstall", "cline-auto", "--no-index", "--no-guidance")
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if strings.Contains(out, "refused") {
		t.Errorf("uninstall over the persisted record reported a refusal:\n%s", out)
	}
	requireClineTestGone(t, entry, manifest, skill)
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("plugin directory survived an uninstall with a persisted record: %v", err)
	}
	b, err := os.ReadFile(mcp)
	if err != nil {
		t.Fatalf("read %s: %v", mcp, err)
	}
	var root struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("settings %s: %v\n%s", mcp, err, b)
	}
	if _, ok := root.MCPServers["zz-mine"]; !ok {
		t.Errorf("uninstall removed the reader's own MCP server:\n%s", b)
	}
	if _, ok := root.MCPServers["deja"]; ok {
		t.Errorf("uninstall left deja's MCP entry:\n%s", b)
	}
}

// CodeBuddy reads skills from <config>/skills and user commands from
// <config>/commands (loadSkills, getHomeCommandsDir in 2.161.2), not from
// ~/.agents/skills, so an install left it without the skill and the /deja
// command Claude Code gets (#4708).
func TestInstallCodeBuddyWritesSkillAndCommand(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	dir := index.DefaultDir()
	skill := filepath.Join(home, ".codebuddy", "skills", "deja-history", "SKILL.md")
	command := filepath.Join(home, ".codebuddy", "commands", "deja.md")
	captureStdout(t, func() {
		if err := runInstall(dir, []string{"codebuddy-auto"}, false); err != nil {
			t.Fatal(err)
		}
	})
	b, err := os.ReadFile(skill)
	if err != nil || !strings.Contains(string(b), "name: deja-history") {
		t.Fatalf("skill: %v %.80q", err, b)
	}
	if b, err := os.ReadFile(command); err != nil || !strings.Contains(string(b), "$ARGUMENTS") {
		t.Fatalf("command: %v %.80q", err, b)
	}
	if got := guidanceStatus("codebuddy-auto"); got != "written" {
		t.Fatalf("guidance status %q", got)
	}

	cfg := filepath.Join(home, "cb-profile")
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	if got := guidancePath("codebuddy"); got != filepath.Join(cfg, "skills", "deja-history", "SKILL.md") {
		t.Fatalf("CODEBUDDY_CONFIG_DIR skill path %q", got)
	}
	t.Setenv("CODEBUDDY_CONFIG_DIR", "")

	captureStdout(t, func() {
		if err := runInstall(dir, []string{"codebuddy-auto"}, true); err != nil {
			t.Fatal(err)
		}
	})
	for _, p := range []string{skill, command, filepath.Join(home, ".codebuddy", "skills"), filepath.Join(home, ".codebuddy", "commands")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s left behind after uninstall", p)
		}
	}
}

func readCodeBuddyJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

// `deja install codebuddy` writes the server where CodeBuddy's own
// resolveMcpFilePath looks first: <config>/.mcp.json, unless an older file
// already holds the user's servers.
func TestInstallCodeBuddyMCP(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	r, err := installTarget("codebuddy", "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".codebuddy", ".mcp.json")
	if r.Path != want {
		t.Fatalf("path = %q, want %q", r.Path, want)
	}
	servers, _ := readCodeBuddyJSON(t, want)["mcpServers"].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if command, _ := mcpCommandArgs("/bin/deja"); entry["command"] != command {
		t.Fatalf("mcpServers.deja = %v", entry)
	}

	// A legacy ~/.codebuddy.json is the file CodeBuddy reads when it is the
	// only one there, so the entry goes into it rather than a new file it
	// would shadow.
	if rerr := os.Remove(want); rerr != nil {
		t.Fatal(rerr)
	}
	legacy := filepath.Join(home, ".codebuddy.json")
	if werr := os.WriteFile(legacy, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if r, err = installTarget("codebuddy", "/bin/deja", false); err != nil || r.Path != legacy {
		t.Fatalf("legacy install = %q, %v", r.Path, err)
	}
	servers, _ = readCodeBuddyJSON(t, legacy)["mcpServers"].(map[string]any)
	if servers["deja"] == nil || servers["other"] == nil {
		t.Fatalf("legacy servers = %v", servers)
	}

	// CODEBUDDY_CONFIG_DIR moves the config home and the legacy file with it.
	cfg := filepath.Join(home, "profile")
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	if r, err = installTarget("codebuddy", "/bin/deja", false); err != nil || r.Path != filepath.Join(cfg, ".mcp.json") {
		t.Fatalf("CODEBUDDY_CONFIG_DIR install = %q, %v", r.Path, err)
	}
}

// `deja install codebuddy-auto` wires the Claude Code hook block into
// ~/.codebuddy/settings.json and the server beside it; uninstall takes both
// out and leaves the user's own hooks.
func TestInstallCodeBuddyAuto(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	settings := filepath.Join(home, ".codebuddy", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	own := `{"model":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`
	if err := os.WriteFile(settings, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("codebuddy-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := readCodeBuddyJSON(t, settings)
	hooks, _ := root["hooks"].(map[string]any)
	for _, w := range codeBuddyHookWiring {
		entries, _ := hooks[w.Event].([]any)
		var entry map[string]any
		var h map[string]any
		for _, e := range entries {
			m := typedOrFatal[map[string]any](t, e)
			hh := typedOrFatal[map[string]any](t, typedOrFatal[[]any](t, m["hooks"])[0])
			if strings.HasSuffix(typedOrFatal[string](t, hh["command"]), " "+w.Sub) {
				if entry != nil {
					t.Fatalf("%s: %s wired twice", w.Event, w.Sub)
				}
				entry, h = m, hh
			}
		}
		if entry == nil {
			t.Fatalf("%s: no %s entry in %v", w.Event, w.Sub, entries)
		}
		if m, _ := entry["matcher"].(string); m != w.Matcher {
			t.Fatalf("%s matcher = %q, want %q", w.Event, m, w.Matcher)
		}
		if h["type"] != "command" {
			t.Fatalf("%s hook = %v", w.Event, h)
		}
		// Seconds: CodeBuddy multiplies the field by 1000.
		if h["timeout"] != float64(60) {
			t.Fatalf("%s timeout = %v, want 60 seconds", w.Event, h["timeout"])
		}
	}
	if root["model"] != "x" || hooks["Stop"] == nil {
		t.Fatalf("the user's settings were not kept: %v", root)
	}
	if _, err := os.Stat(filepath.Join(home, ".codebuddy", ".mcp.json")); err != nil {
		t.Fatalf("codebuddy-auto wrote no MCP entry: %v", err)
	}

	if _, err := installTarget("codebuddy-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	root = readCodeBuddyJSON(t, settings)
	hooks, _ = root["hooks"].(map[string]any)
	if len(hooks) != 1 || hooks["Stop"] == nil {
		t.Fatalf("after uninstall hooks = %v", hooks)
	}
}

func TestCodeBuddyIsAnInstallTarget(t *testing.T) {
	hermeticEnv(t)
	names := strings.Join(installTargetNames(), " ")
	for _, n := range []string{"codebuddy", "codebuddy-auto"} {
		if !strings.Contains(" "+names+" ", " "+n+" ") {
			t.Fatalf("installTargetNames lacks %s", n)
		}
	}
	if autoTargetFor("codebuddy") != "codebuddy-auto" {
		t.Fatalf("--auto maps codebuddy to %q", autoTargetFor("codebuddy"))
	}
}

// --auto finds CodeBuddy by the session store it writes for itself, not by
// the config directory deja creates when it installs.
func TestCodeBuddyDetectedByItsStore(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	has := func() bool {
		return slices.Contains(existingTargets(), "codebuddy")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codebuddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Fatal("an empty ~/.codebuddy counted as a CodeBuddy machine")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codebuddy", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !has() {
		t.Fatal("~/.codebuddy/projects did not count as a CodeBuddy machine")
	}
}

// A machine that approved deja's older hooks gets SessionEnd from an install
// or an upgrade, and codex runs it only once approved in /hooks. Install
// said so only when no hook was approved, so here it said nothing and only
// doctor counted 5 of 6 (#4572).
func TestCodexInstallNamesTheHooksLeftToApprove(t *testing.T) {
	for _, c := range []struct {
		name   string
		events []string
		want   string
	}{
		{"older five approved", []string{"session_start", "user_prompt_submit", "pre_tool_use", "post_tool_use", "pre_compact"}, "1 of 6 hooks is new or changed"},
		{"none approved", nil, "open codex once and approve the hook (/hooks)"},
		{"all approved", []string{"session_start", "user_prompt_submit", "pre_tool_use", "post_tool_use", "pre_compact", "session_end"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			home := sources.CodexHome()
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			hooks := filepath.Join(home, "hooks.json")
			cfg := "model = \"gpt-5\"\n"
			var cfgSb35 strings.Builder
			for _, e := range c.events {
				cfgSb35.WriteString("\n[hooks.state." + strconv.Quote(hooks+":"+e+":0:0") + "]\ntrusted_hash = \"sha256:00\"\n")
			}
			cfg += cfgSb35.String()
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			out := captureStdout(t, func() {
				if _, err := installCodexAuto("/usr/local/bin/deja", false); err != nil {
					t.Fatalf("install codex-auto: %v", err)
				}
			})
			said := ""
			var saidSb47 strings.Builder
			for l := range strings.SplitSeq(out, "\n") {
				if strings.HasPrefix(l, "codex: ") {
					saidSb47.WriteString(l)
				}
			}
			said += saidSb47.String()
			if c.want == "" && said != "" || c.want != "" && !strings.Contains(said, c.want) {
				t.Errorf("install said %q, want %q", said, c.want)
			}
		})
	}
}

// Commands are not part of the Agent Skills standard, so every harness has its
// own directory and its own file format. A command written in the wrong shape
// is not rejected — it is simply never offered when someone types "/".
func TestCommandFilesMatchEachHarnessShape(t *testing.T) {
	hermeticEnv(t)
	// A Windows path is run through as well: it is the shape that broke the
	// TOML file Gemini used to get, and the markdown ones have to survive it
	// too.
	for _, exe := range []string{"/bin/deja", `C:\Users\dev\deja.exe`} {
		for _, h := range []string{"opencode", "cursor", "roo", "kilocode"} {
			r, err := installCommandFile(h, exe, false)
			if err != nil || r.Path == "" {
				t.Fatalf("%s command = %#v, %v", h, r, err)
			}
			b, err := os.ReadFile(r.Path)
			if err != nil {
				t.Fatalf("%s: %v", h, err)
			}
			body := string(b)
			if !strings.Contains(body, exe) {
				t.Errorf("%s command does not name the binary:\n%s", h, body)
			}
			if h == "cursor" {
				// Cursor reads the description off the first line and shows
				// `---` for a file that opens with frontmatter — measured in
				// its own palette against two project commands (#3666).
				if strings.HasPrefix(body, "---\n") {
					t.Errorf("cursor command opens with frontmatter, which it shows as its description:\n%s", body)
				}
				if !strings.HasPrefix(body, "Search this machine's past AI coding sessions") {
					t.Errorf("cursor command does not open with its description:\n%s", body)
				}
			} else if !strings.HasPrefix(body, "---\n") || !strings.Contains(body, "description:") {
				t.Errorf("%s command has no markdown frontmatter:\n%s", h, body)
			}
			if !strings.Contains(body, "$ARGUMENTS") {
				t.Errorf("%s command has no $ARGUMENTS placeholder:\n%s", h, body)
			}

			// Uninstall takes back exactly what was written.
			if r, err = installCommandFile(h, exe, true); err != nil || r.Action != "removed" {
				t.Fatalf("%s uninstall = %#v, %v", h, r, err)
			}
			if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
				t.Errorf("%s command survived uninstall", h)
			}
		}
	}
}

func TestInstallAntigravityPluginRulesAndPreservesUserEdits(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(antigravityConfigHome(), "plugins", antigravityPluginName)
	rulesPath := filepath.Join(dir, "rules", "AGENTS.md")

	if _, err := installAntigravityPlugin("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != antigravityPluginRules {
		t.Fatalf("installed rules/AGENTS.md mismatch:\ngot:\n%s\nwant:\n%s", got, antigravityPluginRules)
	}

	if err := os.WriteFile(rulesPath, []byte(retiredAntigravityPluginRules[0]), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installAntigravityPlugin("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if upgraded, rerr := os.ReadFile(rulesPath); rerr != nil || string(upgraded) != antigravityPluginRules {
		t.Fatalf("retired rules were not upgraded: err=%v, got=%q", rerr, upgraded)
	}

	custom := antigravityPluginRules + "\nCustom team rule.\n"
	if err := os.WriteFile(rulesPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installAntigravityPlugin("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if kept, rerr := os.ReadFile(rulesPath); rerr != nil || string(kept) != custom {
		t.Fatalf("user-edited rules overwritten on reinstall: err=%v, got=%q", rerr, kept)
	}

	if _, err := installAntigravityPlugin("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{filepath.Join(dir, "plugin.json"), filepath.Join(dir, "hooks.json")} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Errorf("%s survived uninstall with edited rules", removed)
		}
	}
	if kept, rerr := os.ReadFile(rulesPath); rerr != nil || string(kept) != custom {
		t.Fatalf("user-edited rules did not survive uninstall: err=%v, got=%q", rerr, kept)
	}

	if err := os.WriteFile(rulesPath, []byte(antigravityPluginRules), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installAntigravityPlugin("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plugin dir %s survived uninstall of unedited rules", dir)
	}
}

func TestInstallClineAllowsDejaMCPTool(t *testing.T) {
	for _, target := range []string{"cline", "cline-auto"} {
		t.Run(target, func(t *testing.T) {
			hermeticEnv(t)
			if _, err := installTarget(target, "/usr/local/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(sources.ClineMCPSettingsPath())
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				MCPServers map[string]struct {
					AlwaysAllow []string `json:"alwaysAllow"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			entry, ok := doc.MCPServers["deja"]
			if !ok {
				t.Fatalf("missing mcpServers.deja in %s:\n%s", sources.ClineMCPSettingsPath(), raw)
			}
			if !slices.Contains(entry.AlwaysAllow, "deja") {
				t.Fatalf("alwaysAllow = %v, want to contain %q\n%s", entry.AlwaysAllow, "deja", raw)
			}
		})
	}
}

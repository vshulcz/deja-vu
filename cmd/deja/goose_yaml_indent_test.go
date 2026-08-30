package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A key is empty when nothing is nested under it, and "nested" was read as
// "starts with two spaces". One space is valid YAML and is not two, so an
// uninstall dropped the key and left the reader's own entries behind as a
// stray list — a config goose then cannot read at all (#2724).
func TestAKeyWithEntriesIsNotEmptyAtAnyIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"one space", " "},
		{"two spaces", "  "},
		{"four spaces", "    "},
		{"a tab, which YAML does not allow but this must not mangle", "\t"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := "slash_commands:\n" +
				c.indent + "- command: \"mine\"\n" +
				c.indent + "  recipe_path: /tmp/mine.yaml\nother: 1\n"
			got := dropEmptyYAMLKey(in, "slash_commands:")
			if !strings.Contains(got, "slash_commands:") {
				t.Errorf("the key was dropped while its entries stayed:\n%s", got)
			}
			if got != in {
				t.Errorf("a key with entries was rewritten:\nwant:\n%s\ngot:\n%s", in, got)
			}
		})
	}
}

// And the reason the function exists: a key with nothing under it parses as
// null, and goose refuses the whole config.
func TestAKeyWithNothingUnderItStillGoes(t *testing.T) {
	// The trailing newline goes with the key when the key is the last line;
	// that is what this has always done, and the caller writes the file back
	// through writeIfChanged either way.
	for _, c := range []struct{ name, in, want string }{
		{"nothing after it", "other: 1\nslash_commands:\n", "other: 1"},
		{"blank lines after it", "slash_commands:\n\n\nother: 1\n", "other: 1\n"},
		{"another key after it", "slash_commands:\nother: 1\n", "other: 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := dropEmptyYAMLKey(c.in, "slash_commands:"); got != c.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, c.want)
			}
		})
	}
}

// The shape every YAML serializer writes: a sequence at the key's own indent.
// Its entries are the key's, and reading them as a stray list dropped the key
// on install — before any uninstall (#2724).
func TestASequenceAtTheKeysOwnIndentBelongsToIt(t *testing.T) {
	in := "other: 1\nslash_commands:\n- command: \"mine\"\n  recipe_path: /m\n"
	if got := dropEmptyYAMLKey(in, "slash_commands:"); got != in {
		t.Errorf("the key was dropped from under its own entries:\n%s", got)
	}
}

// And the whole round trip through the writers, at every indent a person may
// have used. What goes in must come back out byte for byte.
func TestGooseCommandRoundTripsAtEveryIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"a sequence in the first column", ""},
		{"one space", " "},
		{"two spaces", "  "},
		{"three spaces", "   "},
		{"four spaces", "    "},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(gooseConfigDir(), "config.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			before := "other: 1\nslash_commands:\n" +
				c.indent + "- command: \"mine\"\n" +
				c.indent + "  recipe_path: /m\n"
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installGooseCommand("/opt/deja", false); err != nil {
				t.Fatal(err)
			}
			wired, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(wired), `- command: "deja"`) {
				t.Fatalf("the command was not written:\n%s", wired)
			}
			if !strings.Contains(string(wired), `- command: "mine"`) {
				t.Errorf("the reader's own entry did not survive the install:\n%s", wired)
			}
			// One list, one indent: an entry spliced in at another width is a
			// file no YAML parser will read.
			for _, line := range strings.Split(string(wired), "\n") {
				if strings.HasPrefix(strings.TrimLeft(line, " \t"), "- command:") &&
					line[:len(line)-len(strings.TrimLeft(line, " \t"))] != c.indent {
					t.Errorf("an entry was written at another indent than the list:\n%s", wired)
					break
				}
			}

			if _, err := installGooseCommand("/opt/deja", true); err != nil {
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

// A config with windows line endings is read line by line like any other, and
// every line ended in a carriage return, so nothing matched: a second install
// wrote a second key, and uninstall left the empty one this file exists to
// prevent (#2724).
func TestGooseCommandSurvivesWindowsLineEndings(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other: 1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := installGooseCommand("/opt/deja", false); err != nil {
			t.Fatal(err)
		}
	}
	wired, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(wired), "slash_commands:"); n != 1 {
		t.Errorf("%d slash_commands keys after two installs:\n%s", n, wired)
	}
	if _, err := installGooseCommand("/opt/deja", true); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(back), "slash_commands:") {
		t.Errorf("an empty key was left behind, which goose reads as null:\n%q", back)
	}
}

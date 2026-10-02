package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// hermesConfig is the reader's own file, before deja touches it.
func hermesConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	path := filepath.Join(sources.HermesHome(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The MCP half of this file drops the block it created (#2604). The plugin
// half wrote `plugins:\n  enabled:\n    - deja\n` where no block existed and
// took back only the `- deja` line, so an empty block deja made survived every
// uninstall — and `enabled:` with nothing under it parses as null (#2672).
func TestUninstallDropsThePluginsBlockItCreated(t *testing.T) {
	before := "# hermes\nprofile: default\n"
	path := hermesConfig(t, before)
	if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "- deja") {
		t.Fatalf("install did not enable the plugin:\n%s", b)
	}
	if _, err := installTarget("hermes-auto", "/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("the config did not come back as it was:\nwant:\n%s\ngot:\n%s", before, after)
	}
}

// A block the reader wrote stays, empty or not: deja takes back its own line
// and nothing else.
func TestUninstallKeepsAPluginsBlockTheReaderWrote(t *testing.T) {
	for _, before := range []string{
		"# hermes\nprofile: default\n\nplugins:\n  enabled:\n",
		"# hermes\nprofile: default\n\nplugins:\n  enabled:\n    - theirs\n",
	} {
		t.Run(strings.ReplaceAll(before, "\n", "·"), func(t *testing.T) {
			path := hermesConfig(t, before)
			if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			if _, err := installTarget("hermes-auto", "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != before {
				t.Fatalf("a block the reader wrote was changed:\nwant:\n%s\ngot:\n%s", before, after)
			}
		})
	}
}

// Hermes documents plugins.enabled as a flow list too. Install matched only the
// block form and appended a second `plugins:` for anything else — YAML keeps the
// last key, so the user's own plugins went off (#4243) — and it rewrote
// `enabled: []` as a block key that uninstall left behind as null (#4249). The
// list keeps the style the reader wrote it in, and uninstall puts back the
// bytes it found.
func TestHermesPluginsEnabledKeepsItsListStyle(t *testing.T) {
	for _, tc := range []struct{ before, installed string }{
		{"_config_version: 32\nplugins:\n  enabled: [spotify]\n  disabled: []\n", "  enabled: [spotify, deja]\n"},
		{"_config_version: 32\nplugins:\n  enabled: []\n  disabled: []\n", "  enabled: [deja]\n"},
		{"plugins:\n  enabled: [spotify, 'teams']  # mine\n", "  enabled: [spotify, 'teams', deja]  # mine\n"},
		{"plugins:\n  enabled:\n  - spotify\n  disabled: []\n", "  - spotify\n  - deja\n"},
		{"plugins:\n  disabled: []\n", "plugins:\n  enabled:\n    - deja\n  disabled: []\n"},
		{"\ufeffplugins:\n  enabled: [spotify]\n", "  enabled: [spotify, deja]\n"},
		{"plugins:\n  enabled: [ ]\n", "  enabled: [ deja]\n"},
		{"plugins:\n  enabled: [ spotify ]\n", "  enabled: [ spotify, deja ]\n"},
		{"plugins:\n  enabled: ['x]y', spotify]\n", "  enabled: ['x]y', spotify, deja]\n"},
		{"plugins:\n  enabled: ['a #b', spotify]\n", "  enabled: ['a #b', spotify, deja]\n"},
		{"plugins:\n  enabled:\t# c\n    - spotify\n", "    - spotify\n    - deja\n"},
		{"plugins:\n  enabled: [a]\t# c\n", "  enabled: [a, deja]\t# c\n"},
		{"plugins:\n  enabled: [spotify,]\n", "  enabled: [spotify, deja,]\n"},
		{"plugins:\n  enabled: Null\n", "  enabled:\n    - deja\n"},
		{"plugins:\n  enabled: ~  # nothing\n", "  enabled:  # nothing\n    - deja\n"},
		{"plugins:\n  enabled:  NULL\n  disabled: []\n", "  enabled:\n    - deja\n  disabled: []\n"},
		{"_config_version: 32\n...\n", "\nplugins:\n  enabled:\n    - deja\n...\n"},
	} {
		t.Run(strings.ReplaceAll(tc.before, "\n", "·"), func(t *testing.T) {
			path := hermesConfig(t, tc.before)
			if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if n := topLevelKeys(got, "plugins:"); n != 1 {
				t.Fatalf("install left %d plugins: blocks:\n%s", n, got)
			}
			if !strings.Contains(got, tc.installed) {
				t.Fatalf("install did not add deja to the list as written:\nwant %q in:\n%s", tc.installed, got)
			}
			if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatalf("second install: %v", err)
			}
			if again, _ := os.ReadFile(path); string(again) != got {
				t.Fatalf("a second install changed the file:\n%s", again)
			}
			if _, err := installTarget("hermes-auto", "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tc.before {
				t.Fatalf("uninstall did not put the file back:\nwant:\n%q\ngot:\n%q", tc.before, after)
			}
		})
	}
}

// topLevelKeys counts the lines that open a top-level key, a byte order mark
// on the first one included.
func topLevelKeys(text, key string) int {
	n := 0
	for _, line := range strings.Split(strings.TrimPrefix(text, "\ufeff"), "\n") {
		if strings.HasPrefix(line, key) {
			n++
		}
	}
	return n
}

// A block deja created is deja's only while nothing else is in it. Taking the
// whole `plugins:\n  enabled:` back once the reader had listed a plugin of their
// own under it hung that item off the line above, and a key of their own beside
// it left a file Hermes could not parse.
func TestUninstallKeepsWhatTheReaderAddedToDejasPluginsBlock(t *testing.T) {
	for _, tc := range []struct{ add, want string }{
		{"    - spotify\n", "plugins:\n  enabled:\n    - spotify\n"},
		{"  disabled: [x]\n", "plugins:\n  disabled: [x]\n"},
	} {
		t.Run(strings.TrimSpace(tc.add), func(t *testing.T) {
			path := hermesConfig(t, "model: 'x'\n")
			if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			edited := strings.Replace(string(b), "    - deja\n", "    - deja\n"+tc.add, 1)
			if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget("hermes-auto", "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(after)
			if !strings.HasPrefix(got, "model: 'x'\n") || !strings.HasSuffix(got, "\n"+tc.want) || strings.Contains(got, "deja") {
				t.Fatalf("uninstall did not leave the reader's entry where it was:\nwant suffix:\n%s\ngot:\n%s", tc.want, got)
			}
		})
	}
}

// Shapes deja cannot list itself under are refused and left alone, rather
// than given a `- deja` line that turns them into invalid YAML: a mapping or a
// scalar under `enabled:`, and a second top-level `plugins:`, which is the one
// Hermes reads while the editor would change the first.
func TestInstallRefusesAPluginsListItCannotEdit(t *testing.T) {
	for _, before := range []string{
		"plugins:\n  enabled:\n    spotify: true\n",
		"plugins:\n  enabled:\n    spotify\n",
		"plugins:\n  enabled: [a]\nmodel: x\nplugins:\n  enabled: [b]\n",
		"plugins:\n  enabled: [a]\n  enabled: [b]\n",
		"plugins:\n  - foo\nmodel: x\n",
		"plugins:\n- foo\n",
		"plugins:\n  foo\n",
		"\"plugins\":\n  enabled: [a]\n",
		"plugins :\n  enabled: [a]\n",
		"plugins:\n  enabled: nulL\n",
	} {
		t.Run(strings.ReplaceAll(before, "\n", "·"), func(t *testing.T) {
			path := hermesConfig(t, before)
			if err := setHermesPluginEnabled(true); err == nil || !strings.Contains(err.Error(), "by hand") {
				t.Fatalf("install did not refuse: %v", err)
			}
			if b, _ := os.ReadFile(path); string(b) != before {
				t.Fatalf("the config was changed:\n%s", b)
			}
		})
	}
}

// `enabled: null` and `enabled: ~` are the same empty value as a bare
// `enabled:`, and are listed under the same way.
func TestHermesPluginsEnabledNullIsAnEmptyList(t *testing.T) {
	for _, v := range []string{"null", "~", "Null"} {
		t.Run(v, func(t *testing.T) {
			path := hermesConfig(t, "plugins:\n  enabled: "+v+"  # none yet\n  disabled: []\n")
			if err := setHermesPluginEnabled(true); err != nil {
				t.Fatalf("install: %v", err)
			}
			b, _ := os.ReadFile(path)
			if want := "plugins:\n  enabled:  # none yet\n    - deja\n  disabled: []\n"; string(b) != want {
				t.Fatalf("install:\nwant %q\ngot  %q", want, b)
			}
		})
	}
}

// deja's own entry with a comment after it, tab or space, is still deja's.
func TestHermesPluginsKnowsDejasEntryWithAComment(t *testing.T) {
	before := "plugins:\n  enabled:\n    - spotify\n    - deja\t# mine\n"
	path := hermesConfig(t, before)
	if err := setHermesPluginEnabled(true); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != before {
		t.Fatalf("install listed deja twice:\n%s", b)
	}
}

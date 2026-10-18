package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func hermesTestConfig(t *testing.T, cfg string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HERMES_HOME", "")
	t.Setenv("DEJA_HERMES_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	path := filepath.Join(home, ".hermes", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if cfg != "" {
		if err := os.WriteFile(path, []byte(hermesL(cfg)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// hermesL fills in @L@, the command an install from /bin/deja names: the
// launcher where there is one (#5269).
func hermesL(s string) string {
	return strings.ReplaceAll(s, "@L@", hookExeInConfigs("/bin/deja"))
}

// hermesWant is what an install from /bin/deja leaves of an entry: the same
// text where there is no launcher (Windows), and the command moved to the
// launcher where there is one.
func hermesWant(s string) string {
	l := hookExeInConfigs("/bin/deja")
	if l == "/bin/deja" {
		return s
	}
	return strings.NewReplacer(`command: "/bin/deja"`, `command: "`+l+`"`, "command: /bin/deja", `command: "`+l+`"`).Replace(s)
}

var hermesDejaKey = regexp.MustCompile(`(?m)^  ["']?deja["']? *:`)

// Hermes reads `enabled` through PyYAML and its own boolean parser: `no`,
// `off`, `0`, `~` are off, and `disabled:` is not a key it reads. Install
// turned the first back on and switched deja off for the second (#5171).
func TestInstallHermesKeepsTheSwitchHermesReads(t *testing.T) {
	for _, tc := range []struct {
		value string
		off   bool
	}{
		{"enabled: no", true},
		{"enabled: NO", true},
		{"enabled: off", true},
		{`enabled: "0"`, true},
		{"enabled: 0", false},
		{"enabled: ~", false},
		{"enabled:", false},
		{`enabled: "false"`, true},
		{"enabled: False  # paused", true},
		{"enabled: yes", false},
		{"enabled: 1", false},
		{"disabled: true", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			cfg := "mcp_servers:\n  deja:\n    command: @L@\n    args: [mcp]\n    " + tc.value + "\n  other:\n    command: x\n"
			path := hermesTestConfig(t, cfg)
			cfg = hermesL(cfg)
			res, err := installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if string(b) != cfg {
				t.Fatalf("install rewrote an entry that already runs this binary:\n%s", b)
			}
			if said := strings.Contains(res.Note, switchedOffNote); said != tc.off {
				t.Fatalf("switched-off note %v, want %v: %q", said, tc.off, res.Note)
			}
			if note := hermesMCPOffNote(path); (note != "") != tc.off {
				t.Fatalf("doctor off note %q, want off=%v", note, tc.off)
			}
		})
	}
}

func TestHermesSwitchOffMatchesHermes(t *testing.T) {
	// Off only where PyYAML and Hermes' runtime parser both say off; the
	// runtime starts a server for anything else.
	off := []string{"false", "False", "FALSE", "no", "No", "NO", "off", "Off", "OFF", `"false"`, `'no'`, `"off"`, `"0"`, `"OFF"`, "no # paused"}
	on := []string{"true", "True", "yes", "on", "On", "1", "0", "0.0", "~", "null", "", "y", "n", "maybe", `"true"`, `"on"`, `"1"`, "yes # sure"}
	for _, v := range off {
		if !hermesSwitchOff(stripYAMLComment(v)) {
			t.Errorf("%q read as on, Hermes has it off", v)
		}
	}
	for _, v := range on {
		if hermesSwitchOff(stripYAMLComment(v)) {
			t.Errorf("%q read as off, Hermes has it on", v)
		}
	}
}

// A deja key with a comment, quotes or a flow value is still deja's: install
// wrote a second `deja:` above it, which strict YAML readers reject (#5172).
func TestInstallHermesFindsItsKeyHoweverItIsSpelled(t *testing.T) {
	for name, entry := range map[string]string{
		"comment":   "  deja:   # memory\n    command: /bin/deja\n    args: [mcp]\n",
		"quoted":    "  \"deja\":\n    command: /bin/deja\n    args: [mcp]\n",
		"single":    "  'deja' :\n    command: /bin/deja\n    args:\n      - mcp\n",
		"flow":      "  deja: {command: /bin/deja, args: [mcp], timeout: 30}\n",
		"flow off":  "  deja: {command: /bin/deja, args: [mcp], enabled: false}\n",
		"other bin": "  deja:   # memory\n    command: /old/deja\n    args: [mcp]\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := "model: x\nmcp_servers:\n" + entry + "  other:\n    command: x\ntoolsets:\n  - web\n"
			path := hermesTestConfig(t, cfg)
			if !doctorHermesWired(path) {
				t.Fatalf("doctor does not see the entry:\n%s", cfg)
			}
			res, err := installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if n := len(hermesDejaKey.FindAllString(string(b), -1)); n != 1 {
				t.Fatalf("%d deja keys, want one:\n%s", n, b)
			}
			if !strings.Contains(string(b), "\n  other:\n    command: x\n") || !strings.Contains(string(b), "toolsets:\n  - web\n") {
				t.Fatalf("a neighbour changed:\n%s", b)
			}
			if strings.Contains(name, "off") != strings.Contains(res.Note, switchedOffNote) {
				t.Fatalf("switched-off note wrong: %q", res.Note)
			}
			if !strings.Contains(string(b), "/bin/deja") || strings.Contains(string(b), "/old/deja") {
				t.Fatalf("the entry does not run this binary:\n%s", b)
			}
			if _, err := installHermesMCP("/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			b, _ = os.ReadFile(path)
			if want := "model: x\nmcp_servers:\n  other:\n    command: x\ntoolsets:\n  - web\n"; string(b) != want {
				t.Fatalf("uninstall left:\n%s", b)
			}
		})
	}
}

// A flow entry running something else, or deja keyed twice, is refused and
// the file left as it was.
func TestInstallHermesRefusesAnEntryItCannotEditInPlace(t *testing.T) {
	for name, cfg := range map[string]string{
		"flow unread":          "mcp_servers:\n  deja: {command /old/deja}\n",
		"flow key twice":       "mcp_servers:\n  deja: {command: /bin/deja, command: /old/deja, args: [mcp]}\n",
		"nested bracket wrap":  "mcp_servers:\n  deja:\n    command: /old/deja\n    args: [serve, [x]\n    ]\n",
		"quoted wrap":          "mcp_servers:\n  deja:\n    command: \"/old\n    /deja\"\n    args: [mcp]\n",
		"other key wraps":      "mcp_servers:\n  deja:\n    env: {A: 1,\nB: 2}\n    command: /old/deja\n    args: [serve]\n",
		"flow args over lines": "mcp_servers:\n  deja:\n    command: /bin/deja\n    args: [\n      serve\n    ]\n",
		"generated copy last":  "mcp_servers:\n  deja:\n    command: /old/deja\n    args: [mcp]\n    enabled: false\n  deja:\n    command: \"/bin/deja\"\n    args:\n      - mcp\n    enabled: true\n",
		"twice":                "mcp_servers:\n  deja:\n    command: /bin/deja\n  \"deja\":\n    command: /bin/deja\n    enabled: false\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := hermesTestConfig(t, cfg)
			if _, err := installHermesMCP("/bin/deja", false); err == nil {
				t.Fatal("install did not refuse")
			}
			if b, _ := os.ReadFile(path); string(b) != cfg {
				t.Fatalf("a refused install wrote:\n%s", b)
			}
		})
	}
}

// The reader's keys on deja's entry stay through install: rebuilding the
// entry dropped timeouts, env and the tool filter (#5173).
func TestInstallHermesKeepsTheReadersKeysOnItsEntry(t *testing.T) {
	cfg := "mcp_servers:\n  deja:\n    command: /old/deja   # was brew\n    args:\n      - mcp\n      - --x\n" +
		"    timeout: 120\n    env:\n      DEJA_INDEX_DIR: /data/idx\n    tools:\n      include: [search, show]\n    enabled: false\n  other:\n    command: x\n"
	path := hermesTestConfig(t, cfg)
	res, err := installHermesMCP("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "mcp_servers:\n  deja:\n    command: \"/bin/deja\"\n    args:\n      - mcp\n" +
		"    timeout: 120\n    env:\n      DEJA_INDEX_DIR: /data/idx\n    tools:\n      include: [search, show]\n    enabled: false\n  other:\n    command: x\n"
	if want = hermesWant(want); string(b) != want {
		t.Fatalf("install gave\n%s\nwant\n%s", b, want)
	}
	if !strings.Contains(res.Note, switchedOffNote) {
		t.Fatalf("no switched-off note: %q", res.Note)
	}
	// No command or args at all: both go in, the rest stays.
	cfg = "mcp_servers:\n    deja:\n        timeout: 5\n"
	path = hermesTestConfig(t, cfg)
	if _, err := installHermesMCP("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if want := hermesWant("mcp_servers:\n    deja:\n        command: \"/bin/deja\"\n        args:\n            - mcp\n        timeout: 5\n"); string(b) != want {
		t.Fatalf("install gave\n%s\nwant\n%s", b, want)
	}
}

// A plugins: block deja cannot edit is refused before anything is written:
// the MCP entry and the plugin directory went in first, and the refused target
// was left half wired (#5174).
func TestInstallHermesAutoRefusesBeforeWriting(t *testing.T) {
	for name, cfg := range map[string]string{
		"empty flow":     "model: x\nplugins: {}\n",
		"flow two lines": "model: x\nplugins:\n  enabled: [\n    spotify,\n  ]\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := hermesTestConfig(t, cfg)
			if _, err := installHermesAuto("/bin/deja", false); err == nil {
				t.Fatal("install did not refuse")
			}
			if b, _ := os.ReadFile(path); string(b) != cfg {
				t.Fatalf("a refused install wrote config.yaml:\n%s", b)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), "plugins")); err == nil {
				t.Fatal("a refused install wrote the plugin directory")
			}
		})
	}
	// Control: the same plugins switched off by the reader is not edited, so
	// not refused.
	path := hermesTestConfig(t, "plugins:\n  disabled: [deja]\n  enabled: [\n    spotify,\n  ]\n")
	if _, err := installHermesAuto("/bin/deja", false); err != nil {
		t.Fatalf("refused a plugins block it leaves alone: %v", err)
	}
	_ = path
}

// Uninstall says when memory.provider still names the provider it took away
// (#5175), and keeps the key, which is the reader's.
func TestUninstallHermesSaysMemoryProviderIsLeftNamed(t *testing.T) {
	cfg := "memory:\n  provider: deja-memory\n"
	path := hermesTestConfig(t, cfg)
	if _, err := installHermesMemoryProvider("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	res, err := installHermesMemoryProvider("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Note, "hermes memory off") {
		t.Fatalf("no note about memory.provider: %q", res.Note)
	}
	if b, _ := os.ReadFile(path); string(b) != cfg {
		t.Fatalf("uninstall changed the reader's key:\n%s", b)
	}
	// Control: another provider, nothing said.
	hermesTestConfig(t, "memory:\n  provider: mem0\n")
	if _, err := installHermesMemoryProvider("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if res, _ := installHermesMemoryProvider("/bin/deja", true); res.Note != "" {
		t.Fatalf("note for another provider: %q", res.Note)
	}
}

// Shapes the review found: a comment at column 0 inside the entry, args as a
// string or `-mcp`, and the two keys the old installer left (#5172, #5173).
func TestInstallHermesEntryShapesFromReview(t *testing.T) {
	for name, tc := range map[string]struct{ cfg, want, uninstalled string }{
		"column-0 comment": {
			cfg:         "mcp_servers:\n  deja:\n    command: /bin/deja\n#    timeout: 30\n    args: [mcp]\n    enabled: false\n  other:\n    command: x\n",
			want:        "mcp_servers:\n  deja:\n    command: /bin/deja\n#    timeout: 30\n    args: [mcp]\n    enabled: false\n  other:\n    command: x\n",
			uninstalled: "mcp_servers:\n  other:\n    command: x\n",
		},
		"comment in args": {
			cfg:         "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      - serve\n#     - old\n      - mcp\n    timeout: 9\n",
			want:        "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      - mcp\n    timeout: 9\n",
			uninstalled: "mcp_servers:\n",
		},
		"args string": {
			cfg:         "mcp_servers:\n  deja:\n    command: /bin/deja\n    args: mcp\n",
			want:        "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      - mcp\n",
			uninstalled: "mcp_servers:\n",
		},
		"dash glued": {
			cfg:         "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      -mcp\n",
			want:        "mcp_servers:\n  deja:\n    command: /bin/deja\n    args:\n      - mcp\n",
			uninstalled: "mcp_servers:\n",
		},
		"args before command": {
			cfg:         "mcp_servers:\n  deja:\n    args: [old]\n    command: /old/deja\n    timeout: 30\n",
			want:        "mcp_servers:\n  deja:\n    args:\n      - mcp\n    command: \"/bin/deja\"\n    timeout: 30\n",
			uninstalled: "mcp_servers:\n",
		},
		"command on the next line": {
			cfg:         "mcp_servers:\n  deja:\n    command: >-\n      /old/deja\n    args: [mcp]\n",
			want:        "mcp_servers:\n  deja:\n    command: \"/bin/deja\"\n    args: [mcp]\n",
			uninstalled: "mcp_servers:\n",
		},
		"old installer's second key": {
			cfg:         "mcp_servers:\n  deja:\n    command: \"/bin/deja\"\n    args:\n      - mcp\n    enabled: true\n  deja:   # mine\n    command: /old/deja\n    args: [mcp]\n    timeout: 60\n",
			want:        "mcp_servers:\n  deja:   # mine\n    command: \"/bin/deja\"\n    args: [mcp]\n    timeout: 60\n",
			uninstalled: "mcp_servers:\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := hermesTestConfig(t, tc.cfg)
			res, err := installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if tc.want = hermesWant(tc.want); string(b) != tc.want {
				t.Fatalf("install gave\n%s\nwant\n%s", b, tc.want)
			}
			if strings.Contains(tc.cfg, "enabled: false") != strings.Contains(res.Note, switchedOffNote) {
				t.Fatalf("switched-off note wrong: %q", res.Note)
			}
			if _, err := installHermesMCP("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			if b2, _ := os.ReadFile(path); string(b2) != string(b) {
				t.Fatalf("a second install changed the file:\n%s", b2)
			}
			if _, err := installHermesMCP("/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if b, _ = os.ReadFile(path); string(b) != tc.uninstalled {
				t.Fatalf("uninstall left\n%q\nwant\n%q", b, tc.uninstalled)
			}
		})
	}
}

// Uninstall leaves a file it would break by taking the entry out by indent.
func TestUninstallHermesRefusesAnEntryItWouldCutInTwo(t *testing.T) {
	for name, cfg := range map[string]string{
		"wrapped flow": "mcp_servers:\n  deja:\n    env: {A: 1,\nB: 2}\n    command: /bin/deja\n",
		"alias":        "mcp_servers:\n  deja: &d\n    command: /bin/deja\n  copy: *d\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := hermesTestConfig(t, cfg)
			if _, err := installHermesMCP("/bin/deja", true); err == nil {
				t.Fatal("uninstall did not refuse")
			}
			if b, _ := os.ReadFile(path); string(b) != cfg {
				t.Fatalf("a refused uninstall wrote:\n%s", b)
			}
		})
	}
}

// A one-line entry that needs an edit is written out as a block and edited
// like one: naming the binary, it kept deja mcp on Hermes' stripped env and
// the default index, and install said "unchanged" (#5338).
func TestInstallHermesWritesAOneLineEntryOutAsABlock(t *testing.T) {
	for name, tc := range map[string]struct{ entry, want string }{
		"the binary": {"  deja: {command: /bin/deja, args: [mcp], timeout: 30, env: {A: \"x, y\"}}   # mine\n",
			"  deja: # mine\n    command: \"@L@\"\n    args: [mcp]\n    timeout: 30\n    env: {A: \"x, y\"}\n"},
		"elsewhere": {"  deja: {enabled: no, command: /old/deja, args: [serve]}\n",
			"  deja:\n    enabled: no\n    command: \"@L@\"\n    args:\n      - mcp\n"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := "model: x\nmcp_servers:\n" + tc.entry + "  other:\n    command: x\n"
			path := hermesTestConfig(t, cfg)
			res, err := installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if name == "the binary" && hookExeInConfigs("/bin/deja") == "/bin/deja" {
				// No launcher (Windows): the entry already runs what install writes.
				if string(b) != cfg {
					t.Fatalf("install rewrote an entry that runs this binary:\n%s", b)
				}
				return
			}
			if want := "model: x\nmcp_servers:\n" + hermesL(tc.want) + "  other:\n    command: x\n"; string(b) != want {
				t.Fatalf("install gave\n%s\nwant\n%s", b, want)
			}
			if !strings.Contains(res.Note, hermesFlowRewrittenNote) {
				t.Errorf("install did not say it rewrote the line: %q", res.Note)
			}
			if strings.Contains(name, "elsewhere") != strings.Contains(res.Note, switchedOffNote) {
				t.Errorf("switched-off note wrong: %q", res.Note)
			}
			res, err = installHermesMCP("/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if again, _ := os.ReadFile(path); string(again) != string(b) || strings.Contains(res.Note, hermesFlowRewrittenNote) {
				t.Fatalf("a second install changed it again (%q):\n%s", res.Note, again)
			}
		})
	}
}

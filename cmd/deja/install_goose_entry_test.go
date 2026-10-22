package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func gooseTestConfig(t *testing.T, cfg string) string {
	t.Helper()
	dir := gooseHomeForTest(t)
	path := filepath.Join(dir, "config.yaml")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readGooseTestConfig(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var gooseDejaKey = regexp.MustCompile(`(?m)^  ["']?deja["']? *:`)

// gooseFlowEntry is deja's entry body as this OS writes it, flow form.
func gooseFlowEntry(exe string) string {
	cmd, args := mcpCommandArgs(exe)
	return "{enabled: true, type: stdio, name: deja, cmd: " + yamlQuote(cmd) + ", args: [" + strings.Join(quoteAll(args), ", ") + "], timeout: 300}"
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = yamlQuote(s)
	}
	return out
}

func gooseBlockEntry(key, exe string) string {
	cmd, args := mcpCommandArgs(exe)
	s := "  " + key + "\n    enabled: true\n    type: stdio\n    name: deja\n    cmd: " + yamlQuote(cmd) + "\n    args:\n"
	for _, a := range args {
		s += "      - " + yamlQuote(a) + "\n"
	}
	return s + "    timeout: 300\n"
}

// goose refuses a mapping that names a key twice, skips the whole config and
// writes a fresh one on its next save. Install matched only a bare `deja:`,
// so any other spelling got a second deja key beside it (#5190).
func TestInstallGooseEditsADejaKeySpelledAnyWay(t *testing.T) {
	const exe = "/bin/deja"
	mine := "  mine:\n    enabled: true\n    type: stdio\n    name: mine\n    cmd: /opt/mine/server\n    args: []\n"
	for _, tc := range []struct{ name, entry string }{
		{"double quoted", gooseBlockEntry(`"deja":`, exe)},
		{"single quoted", gooseBlockEntry(`'deja':`, exe)},
		{"comment after", gooseBlockEntry(`deja:   # memory`, exe)},
		{"space before colon", gooseBlockEntry(`deja :`, exe)},
		{"flow", "  deja: " + gooseFlowEntry(exe) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := gooseTestConfig(t, "GOOSE_PROVIDER: openai\nextensions:\n"+tc.entry+mine)
			if _, err := installGoose(exe, false); err != nil {
				t.Fatalf("install: %v", err)
			}
			got := readGooseTestConfig(t, path)
			if n := len(gooseDejaKey.FindAllString(got, -1)); n != 1 {
				t.Fatalf("%d deja keys after install, goose refuses the file with two:\n%s", n, got)
			}
			if !strings.Contains(got, "  mine:") || !strings.Contains(got, "GOOSE_PROVIDER: openai") {
				t.Fatalf("install dropped the reader's settings:\n%s", got)
			}
			if !doctorGooseWired(path) {
				t.Fatalf("doctor calls the entry not wired:\n%s", got)
			}
			if _, err := installGoose(exe, true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			got = readGooseTestConfig(t, path)
			if gooseDejaKey.MatchString(got) || !strings.Contains(got, "  mine:") {
				t.Fatalf("uninstall left deja or took the reader's entry:\n%s", got)
			}
		})
	}
}

// The reader's keys on deja's entry — the env deja's server runs with, a
// raised timeout, a tool filter — went on every install, the repair an
// upgrade runs included: the entry was rebuilt whole (#5191).
func TestInstallGooseKeepsTheReadersKeysOnDeja(t *testing.T) {
	const exe = "/bin/deja"
	// The entry as goose itself writes it (serde_yaml), at an old path.
	cfg := "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    name: deja\n    description: ''\n    cmd: /old/bin/deja\n    args:\n    - mcp\n" +
		"    envs:\n      DEJA_INDEX_DIR: /srv/idx\n    env_keys: []\n    timeout: 300\n    cwd: null\n    bundled: null\n    available_tools:\n    - deja\n" +
		"  developer:\n    enabled: true\n    type: platform\n    name: developer\nGOOSE_MODEL: gpt-5\n"
	path := gooseTestConfig(t, cfg)
	if _, err := installGoose(exe, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got := readGooseTestConfig(t, path)
	for _, want := range []string{"    description: ''\n", "    envs:\n      DEJA_INDEX_DIR: /srv/idx\n", "    env_keys: []\n", "    timeout: 300\n",
		"    cwd: null\n", "    available_tools:\n    - deja\n", "  developer:\n", "GOOSE_MODEL: gpt-5\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("install dropped %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/old/bin/deja") {
		t.Errorf("install left the old binary:\n%s", got)
	}
	cmd, args := mcpCommandArgs(exe)
	if !strings.Contains(got, cmd) || !strings.Contains(got, args[len(args)-1]) {
		t.Errorf("entry does not run this binary:\n%s", got)
	}
	if n := len(gooseDejaKey.FindAllString(got, -1)); n != 1 {
		t.Errorf("%d deja keys:\n%s", n, got)
	}
	again, err := installGoose(exe, false)
	if err != nil || again.Action != "unchanged" {
		t.Errorf("second install: %v %q, want unchanged", err, again.Action)
	}
}

// goose reads `enabled` as a YAML 1.2 bool and nothing else. `disabled:
// true` is not a field it has, and was taken as off, so install switched off
// a server goose ran; doctor compared the value to "false" and missed
// `False` (#5192).
func TestGooseReadsOnlyEnabledAsItsSwitch(t *testing.T) {
	const exe = "/bin/deja"
	cmd, args := mcpCommandArgs(exe)
	body := "    type: stdio\n    name: deja\n    cmd: " + yamlQuote(cmd) + "\n    args: [" + strings.Join(quoteAll(args), ", ") + "]\n    timeout: 60\n"
	for _, tc := range []struct {
		name, sw string
		off      bool
	}{
		{"disabled true", "    enabled: true\n    disabled: true\n", false},
		{"False", "    enabled: False\n", true},
		{"FALSE", "    enabled: FALSE\n", true},
		{"false", "    enabled: false\n", true},
		{"true", "    enabled: true\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := gooseTestConfig(t, "extensions:\n  deja:\n"+tc.sw+body)
			if said := clientMCPEntryOff("goose") != ""; said != tc.off {
				t.Errorf("doctor says off: %v, goose reads off: %v", said, tc.off)
			}
			res, err := installGoose(exe, false)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			got := readGooseTestConfig(t, path)
			if !strings.Contains(got, tc.sw) {
				t.Errorf("install changed the switch %q:\n%s", tc.sw, got)
			}
			if said := strings.Contains(res.Note, switchedOffNote); said != tc.off {
				t.Errorf("install says switched off: %v, goose reads off: %v", said, tc.off)
			}
		})
	}
}

// goose saves config.yaml through serde_yaml on its first start after
// goose-auto (it registers deja's plugin): quotes go and list items sit at
// their key's indent. Install compared its entries as text, so it rewrote
// both, moved slash_commands to the bottom, and doctor called deja's own
// /deja someone else's (#5193).
func TestInstallGooseKnowsItsEntriesAfterGooseRewritesThem(t *testing.T) {
	const exe = "/bin/deja"
	gooseHomeForTest(t)
	cmd, args := mcpCommandArgs(exe)
	ext := "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    name: deja\n    cmd: " + cmd + "\n    args:\n"
	for _, a := range args {
		ext += "    - " + a + "\n"
	}
	ext += "    timeout: 60\n  developer:\n    enabled: true\n    type: platform\n    name: developer\n"
	cfg := ext + "slash_commands:\n- command: deja\n  recipe_path: " + gooseRecipePath() + "\nproviders:\n  openai:\n    enabled: true\nactive_provider: openai\n"
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(gooseConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gooseRecipePath(), []byte(gooseRecipe(exe)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range doctorCommandFiles() {
		if c.name == "goose" {
			if s := c.state(); s != "written" {
				t.Errorf("doctor's /deja row says %q, want written", s)
			}
		}
	}
	if res, err := installGoose(exe, false); err != nil || res.Action != "unchanged" {
		t.Errorf("extension: %v %q, want unchanged", err, res.Action)
	}
	if res, err := installGooseCommand(exe, false); err != nil || res.Action != "unchanged" {
		t.Errorf("slash command: %v %q, want unchanged", err, res.Action)
	}
	if got := readGooseTestConfig(t, path); got != cfg {
		t.Errorf("config rewritten:\n%s", got)
	}
	// Uninstall still finds both.
	if _, err := installGooseCommand(exe, true); err != nil {
		t.Fatal(err)
	}
	if _, err := installGoose(exe, true); err != nil {
		t.Fatal(err)
	}
	got := readGooseTestConfig(t, path)
	if strings.Contains(got, "deja") || !strings.Contains(got, "  developer:") || !strings.Contains(got, "active_provider: openai") {
		t.Errorf("uninstall:\n%s", got)
	}
}

// Review cases: a comment or blank line before the first args item set the
// item indent to column 0, and uninstall stopped at a column-0 comment and
// left half the entry; both made a file goose refuses. A second extensions:
// or slash_commands: key is refused before the in-place edit, as before.
func TestInstallGooseEntryAroundCommentsAndTwoKeys(t *testing.T) {
	const exe = "/bin/deja"
	cfg := "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    cmd: /old/deja\n    args:\n# the subcommand\n\n      - serve\n    envs:\n      A: b\n  mine:\n    enabled: true\n"
	path := gooseTestConfig(t, cfg)
	if _, err := installGoose(exe, false); err != nil {
		t.Fatal(err)
	}
	got := readGooseTestConfig(t, path)
	if regexp.MustCompile(`(?m)^- `).MatchString(got) || !strings.Contains(got, "    envs:\n      A: b\n") {
		t.Fatalf("args item at column 0 or envs lost:\n%s", got)
	}
	if _, err := installGoose(exe, true); err != nil {
		t.Fatal(err)
	}
	got = readGooseTestConfig(t, path)
	if strings.Contains(got, "serve") || strings.Contains(got, "mcp") || strings.Contains(got, "A: b") || !strings.Contains(got, "  mine:\n") {
		t.Fatalf("uninstall left part of the entry:\n%s", got)
	}

	gooseTestConfig(t, "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    cmd: /bin/deja\n    args: [mcp]\nextensions:\n  mine:\n    enabled: true\n")
	if _, err := installGoose(exe, false); err == nil {
		t.Error("two extensions: keys accepted")
	}
	gooseTestConfig(t, "slash_commands:\n- command: deja\n  recipe_path: "+gooseRecipePath()+"\nslash_commands:\n- command: mine\n")
	if _, err := installGooseCommand(exe, false); err == nil {
		t.Error("two slash_commands: keys accepted")
	}
	// A refused command writes nothing: the recipe used to be written first and
	// then stayed on disk with no entry pointing at it (#5296).
	if _, err := os.Stat(gooseRecipePath()); !os.IsNotExist(err) {
		t.Errorf("a refused duplicate key left the recipe behind: %v", err)
	}
	gooseTestConfig(t, "slash_commands: []\n")
	if _, err := installGooseCommand(exe, false); err == nil {
		t.Error("an inline slash_commands: value accepted")
	}
	if _, err := os.Stat(gooseRecipePath()); !os.IsNotExist(err) {
		t.Errorf("a refused inline value left the recipe behind: %v", err)
	}
}

// The recipe is still refreshed when the config already holds the entry: only
// a refusal skips it, not the "unchanged" returns.
func TestInstallGooseCommandRefreshesTheRecipeForAnEntryAlreadyThere(t *testing.T) {
	const oldExe, newExe = "/old/bin/deja", "/new/bin/deja"
	gooseTestConfig(t, "slash_commands:\n- command: \"deja\"\n  recipe_path: \""+gooseRecipePath()+"\"\n")
	if err := os.WriteFile(gooseRecipePath(), []byte(gooseRecipe(oldExe)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGooseCommand(newExe, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(gooseRecipePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != gooseRecipe(newExe) {
		t.Errorf("the recipe still runs the old binary:\n%s", got)
	}
}

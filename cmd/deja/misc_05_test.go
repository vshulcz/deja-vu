package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/harnesscolor"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
	"html"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A store the reader excluded has to read as excluded on every screen that
// mentions stores. The first pass gave `doctor` the state and left the rest
// saying what they said before: `deja sources` walked the store and reported
// three sessions from a harness deja had just been told never to open, the
// empty screens called it "no agent has run here yet", and the settings note
// counted the rule as a project pattern (#3499).
func TestEverySurfaceKnowsAStoreIsExcluded(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "one.jsonl"), "exc", []string{
		`{"type":"user","sessionId":"exc","timestamp":"2026-08-20T10:00:00Z","cwd":"/work/app",` +
			`"message":{"role":"user","content":"the migration ran twice"}}`,
	})
	// Wherever the rule puts it, rather than a path spelled out here: the
	// resolution reads XDG_CONFIG_HOME and then the home directory, and a test
	// that hard-codes one of those stops exercising the file at all.
	excl := sources.ExcludePath()
	if err := os.MkdirAll(filepath.Dir(excl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(excl, []byte("harness:claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The walk read nothing, so there is nothing to answer from.
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "store not read at all") {
		t.Errorf("stats does not say a store was excluded:\n%s", out)
	}
	if strings.Contains(out, "project pattern") {
		t.Errorf("stats called the store rule a project pattern:\n%s", out)
	}

	// `deja sources` is where the empty-machine advice sends people, so it is
	// the worst place to report sessions from a store nobody reads.
	out, err = captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "claude\t") {
			continue
		}
		if !strings.Contains(line, "excluded") {
			t.Errorf("the claude row does not say excluded: %q", line)
		}
		if strings.Contains(line, "sessions=1") {
			t.Errorf("sources read a store it was told to leave alone: %q", line)
		}
	}
	if strings.Contains(out, "excluded-patterns=1") {
		t.Errorf("a store rule was counted as a project pattern:\n%s", out)
	}

	// The first screen a new reader sees must not send them looking for a
	// store that is on disk and deliberately unread.
	out, err = captureRun(t, "brief")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no agent has") {
		t.Errorf("brief blamed an empty machine for an exclusion:\n%s", out)
	}
	if !strings.Contains(out, "excluded") {
		t.Errorf("brief does not name the exclusion:\n%s", out)
	}

	// And the machine-readable form, for whatever watches it.
	out, err = captureRun(t, "doctor", "--json", "--offline")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Stores []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"stores"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	found := false
	for _, s := range report.Stores {
		if s.Name != "claude" {
			continue
		}
		found = true
		if s.State != "excluded" {
			t.Errorf("doctor --json says claude is %q, want excluded", s.State)
		}
	}
	if !found {
		t.Error("doctor --json dropped the excluded store instead of naming it")
	}
}

func TestProjectExcludePatternsCountsOnlyProjectRules(t *testing.T) {
	for _, tc := range []struct {
		name, file, projects, harnesses string
		want                            int
	}{
		{"empty", "", "", "", 0},
		{"environment harness only", "", "", "codex", 0},
		{"projects with environment harness", "secret-project\nother-project\n", "", "codex", 2},
		{"duplicate harness rules", "harness:codex\nharness:codex\nsecret\n", "", "codex", 1},
		{"mixed sources", "# comment\nsecret\nharness:codex\n", "other", "codex,aider", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			t.Setenv("DEJA_EXCLUDE_PROJECTS", tc.projects)
			t.Setenv("DEJA_EXCLUDE_HARNESSES", tc.harnesses)
			writeExclude(t, tc.file)
			if got := projectExcludePatterns(); got != tc.want {
				t.Errorf("project pattern count = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSourcesNamesTheHarnessExclusionSetting(t *testing.T) {
	for _, tc := range []struct{ name, file, harnesses string }{
		{"environment", "", " CoDeX , AIDER , OpenCode "},
		{"file", "harness:codex\nharness:aider\nharness:opencode\n", ""},
		{"both", "harness:codex\nharness:aider\nharness:opencode\n", "codex,aider,opencode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("DEJA_STORES", "codex,aider,opencode")
			t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
			t.Setenv("DEJA_EXCLUDE_HARNESSES", tc.harnesses)
			writeExclude(t, tc.file)
			var out bytes.Buffer
			printSourcesTo(&out, filepath.Join(tmp, "index.db"))
			for _, name := range []string{"codex", "aider", "opencode"} {
				var row string
				for line := range strings.SplitSeq(out.String(), "\n") {
					if strings.HasPrefix(line, name+"\t") {
						row = line
						break
					}
				}
				if row == "" {
					t.Fatalf("missing %s row:\n%s", name, out.String())
				}
				if !strings.Contains(row, "excluded") || strings.Contains(row, "sessions=") {
					t.Errorf("excluded store was not reported correctly: %s", row)
				}
				if tc.harnesses != "" {
					if !strings.Contains(row, "DEJA_EXCLUDE_HARNESSES") || strings.Contains(row, "is in "+sources.ExcludePath()) {
						t.Errorf("environment exclusion names the wrong setting: %s", row)
					}
				} else if !strings.Contains(row, "`harness:"+name+"` is in "+sources.ExcludePath()) {
					t.Errorf("file exclusion lost its setting: %s", row)
				}
			}
		})
	}
}

func TestHiddenByOwnSettingsNamesProjectCountsAndHarnessSettings(t *testing.T) {
	for _, tc := range []struct {
		name, file, harnesses  string
		wantProject, wantStore string
	}{
		{"environment with project", "secret\n", "codex", "1 project pattern excluded", "1 store not read at all (DEJA_EXCLUDE_HARNESSES)"},
		{"environment only", "", " CoDeX , codex ", "", "1 store not read at all (DEJA_EXCLUDE_HARNESSES)"},
		{"file", "secret\nharness:codex\nharness:codex\n", "", "1 project pattern excluded", "1 store not read at all ($file)"},
		{"both for one store", "secret\nharness:codex\n", "codex", "1 project pattern excluded", "1 store not read at all (DEJA_EXCLUDE_HARNESSES)"},
		{"different stores", "secret\nharness:aider\n", "codex", "1 project pattern excluded", "2 stores not read at all ("},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
			t.Setenv("DEJA_EXCLUDE_HARNESSES", tc.harnesses)
			writeExclude(t, tc.file)
			got := hiddenByOwnSettings()
			if tc.wantProject == "" {
				if strings.Contains(got, "project pattern") {
					t.Errorf("a harness rule was counted as a project: %s", got)
				}
			} else if !strings.Contains(got, tc.wantProject) {
				t.Errorf("missing %q in %q", tc.wantProject, got)
			}
			wantStore := strings.ReplaceAll(tc.wantStore, "$file", sources.ExcludePath())
			start := strings.Index(got, wantStore)
			if start < 0 {
				t.Fatalf("missing %q in %q", wantStore, got)
			}
			if tc.name == "different stores" {
				storeNote := got[start:]
				for _, setting := range []string{sources.ExcludePath(), "DEJA_EXCLUDE_HARNESSES"} {
					if !strings.Contains(storeNote, setting) {
						t.Errorf("store note does not name %s: %s", setting, storeNote)
					}
				}
			}
		})
	}
}

// `--auto` wires what it finds, and it found nineteen harnesses of twenty-five:
// Amp, prime-agent, Crush, Continue, Zed and VS Code had install targets and
// were in the published matrix with nothing looking for them, so a machine that
// had them got the others wired and no word about these (#3192).
//
// Derived from the target list rather than written beside it: a harness that
// gains a target and no way to be detected fails here, which is the check that
// was missing.
func TestEveryInstallTargetCanBeDetected(t *testing.T) {
	hermeticEnv(t)
	checks := existingTargetChecks()
	// Detection reports Claude under the name its target does not use, and
	// Copilot Chat is wired by the vscode target.
	alias := map[string]string{"claude": "claude-code", "copilot-chat": "vscode"}
	// Not harnesses: one is this machine's own status line, the other a timer.
	notAHarness := map[string]bool{"statusline": true, "sync-timer": true}

	var missing []string
	for _, target := range installTargetNames() {
		base := strings.TrimSuffix(target, "-auto")
		if notAHarness[base] {
			continue
		}
		if a, ok := alias[base]; ok {
			base = a
		}
		if _, ok := checks[base]; !ok {
			missing = append(missing, base)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("install targets nothing looks for, so --auto never wires them: %s",
			strings.Join(unique(missing), ", "))
	}
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// The other half: a directory deja writes itself must not count as the harness
// being here, or one install makes every machine look like every machine.
func TestDetectionDoesNotFireOnWhatDejaWrites(t *testing.T) {
	home := hermeticEnv(t)
	home = filepath.Join(home, "home")

	// The configs install writes, for the six harnesses this test is about.
	written := []string{
		filepath.Join(home, ".config", "crush", "crush.json"),
		filepath.Join(home, ".continue", "config.yaml"),
		filepath.Join(home, ".agents", "skills", "deja-history", "SKILL.md"),
	}
	for _, p := range written {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range existingTargets() {
		if name == "crush" || name == "continue" {
			t.Errorf("a config deja writes made the machine look like a %s machine", name)
		}
	}

	// And with what the harness itself writes, the same machine is detected.
	for _, d := range []string{
		filepath.Join(home, ".local", "share", "crush"),
		filepath.Join(home, ".continue", "sessions"),
		filepath.Join(home, ".local", "share", "amp", "threads"),
		filepath.Join(home, ".prime", "agent", "sessions"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "share", "crush", "projects.json"),
		[]byte(`{"projects":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, name := range existingTargets() {
		got[name] = true
	}
	for _, name := range []string{"crush", "continue", "amp", "prime"} {
		if !got[name] {
			t.Errorf("%s was not detected from the directory it writes for itself", name)
		}
	}
}

// A watermark is namespaced by the peer string an export was given, and one
// machine spelled two ways got two of them: the second export sent the same
// records again and reported them as work delivered (#1878). ssh has treated
// the two spellings as one machine since #1867.
func TestOneMachineSettlesUnderOneWatermarkWhicheverCase(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, 4)
	for i := range 4 {
		lines = append(lines, `{"type":"user","sessionId":"s`+string(rune('a'+i))+`","cwd":"/proj","timestamp":"2026-08-20T10:0`+string(rune('0'+i))+`:00Z","message":{"role":"user","content":"retry loop"}}`)
	}
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	first, err := captureRun(t, "sync", "export", filepath.Join(tmp, "one"), "--peer", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "exported 4 records") {
		t.Fatalf("the first export did not send the history: %s", first)
	}
	again, err := captureRun(t, "sync", "export", filepath.Join(tmp, "two"), "--peer", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "exported 0 records") {
		t.Errorf("the same machine spelled differently was sent everything again: %s", again)
	}

	// The control: a different machine still receives the history, so the
	// watermarks are folded rather than shared.
	other, err := captureRun(t, "sync", "export", filepath.Join(tmp, "three"), "--peer", "build-box")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(other, "exported 4 records") {
		t.Errorf("a machine that has received nothing was told it was up to date: %s", other)
	}
}

// The one line deja prints about secrets leaving the machine said records were
// redacted whatever the count was, so an export with nothing masked announced a
// redaction that never happened (#2255).
func TestTheExportLineDoesNotClaimARedactionThatDidNotHappen(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")

	write := func(name, body string) {
		t.Helper()
		at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":%q}}`, name, at, body)
		if err := os.WriteFile(filepath.Join(claude, name+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing in this session matches a redaction pattern.
	write("clean", "gateway_timeout on the reconnect_loop")
	out := filepath.Join(tmp, "out-clean")
	said := captureStderr(t, func() {
		if err := runSync(dir, []string{"export", out}); err != nil {
			t.Fatal(err)
		}
	})
	// The premise: something was exported, or the line is not printed at all.
	if !strings.Contains(said, "0 masked") && !strings.Contains(said, "nothing matched") {
		t.Logf("stderr was: %s", said)
	}
	if strings.Contains(said, "records were redacted") {
		t.Errorf("nothing was masked and the export said records were redacted:\n%s", said)
	}
	if !strings.Contains(said, "review the export") {
		t.Errorf("the caution is the point of the line and it is gone:\n%s", said)
	}

	// And with a secret in the store, the sentence is true and stays.
	write("dirty", "the token is ghp_"+strings.Repeat("a", 36))
	out2 := filepath.Join(tmp, "out-dirty")
	said = captureStderr(t, func() {
		if err := runSync(dir, []string{"export", out2, "--full"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(said, "records were redacted") {
		t.Errorf("a redacted store no longer says so:\n%s", said)
	}
}

// The packages under extensions/ are what a harness's own catalog installs,
// and `deja install <harness>-auto` is what the CLI writes. A user gets one or
// the other, so a hook the installer runs and the package does not is a
// feature that exists for half of them. They drifted: on 07.10.2026 the Kimi
// plugin ran hook-prompt alone, the OpenClaw package had no digest and no
// compaction capture, the dsh package nothing after a tool, the Grok plugin
// one hook whose output Grok throws away.
//
// The rule checked here is the one a reader would state: every deja hook the
// installer's files call, the package's files call too. Read off the files on
// both sides, so neither can satisfy it with a list kept beside the code.
func TestExtensionPackagesRunEveryHookTheInstallerDoes(t *testing.T) {
	for _, p := range []struct {
		dir     string
		targets []string
		// Extra files on the package side that belong to it but live
		// outside its directory.
		extra []string
		// Generated sources install picks between at run time: only one is
		// written per machine, and the package has to cover both.
		generated func() []string
		// Hooks the package cannot run, with the host-side reason.
		cannot map[string]string
	}{
		{dir: "kimi", targets: []string{"kimi-auto"}, extra: []string{"kimi.plugin.json"}},
		{dir: "openclaw", targets: []string{"openclaw-auto"}},
		{dir: "dsh", targets: []string{"deepseek-auto"}},
		{dir: "hermes", targets: []string{"hermes-auto"}, cannot: map[string]string{
			// The fix pair and the file line (#4808) ride transform_tool_result,
			// a plugin hook. The catalog package is a memory provider, and
			// Hermes loads one with a context whose register_hook does nothing
			// (plugins/memory/__init__.py _ProviderCollector, 0.17.0).
			"hook-tool-after": "a Hermes memory provider cannot register hooks",
			"hook-tool":       "a Hermes memory provider cannot register hooks",
		}},
		{dir: "grok", targets: []string{"grok-auto"}},
		{dir: "opencode", targets: []string{"opencode-auto"}, generated: func() []string {
			return []string{opencodePluginJS("/bin/deja"), opencodeLegacyPluginJS("/bin/deja")}
		}},
		{dir: "pi", targets: []string{"pi-auto"}},
	} {
		t.Run(p.dir, func(t *testing.T) {
			hermeticEnv(t)
			var installed []string
			for _, target := range p.targets {
				if _, err := installTarget(target, "/bin/deja", false); err != nil {
					t.Fatalf("install %s: %v", target, err)
				}
			}
			installed = append(installed, packageTexts(t, filepath.Dir(os.Getenv("DEJA_INDEX_DIR")), nil)...)
			if p.generated != nil {
				installed = append(installed, p.generated()...)
			}
			want := dejaHooksIn(installed)
			if len(want) == 0 {
				t.Fatalf("install %v wrote no deja hook at all; this test is reading the wrong place", p.targets)
			}

			root := filepath.Join("..", "..")
			pkg := packageTexts(t, filepath.Join(root, "extensions", p.dir), map[string]bool{"test": true, "docs": true, "node_modules": true})
			for _, rel := range p.extra {
				b, err := os.ReadFile(filepath.Join(root, rel))
				if err != nil {
					t.Fatal(err)
				}
				pkg = append(pkg, string(b))
			}
			have := dejaHooksIn(pkg)
			var missing []string
			for hook := range want {
				if have[hook] {
					continue
				}
				if _, ok := p.cannot[hook]; ok {
					continue
				}
				missing = append(missing, hook)
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("extensions/%s does not run %s, which `deja install %s` wires — a user of the package goes without it",
					p.dir, strings.Join(missing, ", "), strings.Join(p.targets, ", "))
			}
			// An exception stops being one when the installer stops wiring
			// the hook or the package starts running it.
			for hook, why := range p.cannot {
				if !want[hook] || have[hook] {
					t.Errorf("extensions/%s lists %s as impossible (%s), but that no longer describes the code", p.dir, hook, why)
				}
			}
		})
	}
}

var dejaHookToken = regexp.MustCompile(`\bhook-(?:context|prompt|tool-after|tool|precompact|session-end|spawn)\b`)

// dejaHooksIn is the set of deja hook subcommands named in the texts.
// "hook-tool" is matched only as a whole token, so hook-tool-after does not
// count as it.
func dejaHooksIn(texts []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range texts {
		for _, m := range dejaHookToken.FindAllString(s, -1) {
			out[m] = true
		}
	}
	return out
}

// packageTexts reads every regular file below root, skipping the named
// directories.
func packageTexts(t *testing.T, root string, skip map[string]bool) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && skip[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // the test walks the repo checkout
		if err != nil {
			return err
		}
		out = append(out, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Each package under extensions/ is the page someone reads inside the harness
// they already use, and for six of the seven it existed only in English while
// dsh had a Chinese one (extensions/dsh/docs/zh.md). The count inside those
// pages is already guarded — TestChineseDocsCountTheHarnessesTheRegistryHas
// walks extensions/ for Chinese pages — but nothing said the page had to exist,
// or that the two halves had to point at each other. A translation reachable
// from nowhere is the same as no translation.
func TestEveryExtensionHasAChinesePageLinkedBothWays(t *testing.T) {
	root := filepath.Join("..", "..")
	entries, err := os.ReadDir(filepath.Join(root, "extensions"))
	if err != nil {
		t.Fatal(err)
	}

	pairs := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		english := filepath.Join(root, "extensions", name, "README.md")
		if _, err := os.Stat(english); err != nil {
			continue // a package without a README is a different problem
		}
		pairs++

		zhPath := filepath.Join(root, "extensions", name, "docs", "zh.md")
		zh, err := os.ReadFile(zhPath)
		if err != nil {
			t.Errorf("extensions/%s/ has a README and no Chinese page: %v", name, err)
			continue
		}

		// The English page links the Chinese one by absolute URL, because it is
		// also published to npm and to plugin directories, where a relative
		// path resolves against their site and not against this repository.
		en, err := os.ReadFile(english)
		if err != nil {
			t.Fatalf("extensions/%s/README.md: %v", name, err)
		}
		want := "https://github.com/vshulcz/deja-vu/blob/main/extensions/" + name + "/docs/zh.md"
		if !strings.Contains(string(en), want) {
			t.Errorf("extensions/%s/README.md does not link its Chinese page — want %s", name, want)
		}
		if !strings.Contains(string(zh), "[English](../README.md)") {
			t.Errorf("extensions/%s/docs/zh.md does not link back to the English page", name)
		}

		// A file that is named zh and reads as English is the failure this
		// catches: the count guard beside it only fires on a Chinese numeral,
		// so a copied English page passes it in silence.
		if cjk := countCJK(string(zh)); cjk < 200 {
			t.Errorf("extensions/%s/docs/zh.md has %d Chinese characters — it reads as a copy of the English page", name, cjk)
		}
	}

	if pairs < 7 {
		t.Fatalf("found %d packages with a README under extensions/ — the listing is not being read", pairs)
	}
}

func countCJK(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			n++
		}
	}
	return n
}

func countRuneRange(s string, lo, hi rune) int {
	n := 0
	for _, r := range s {
		if r >= lo && r <= hi {
			n++
		}
	}
	return n
}

func countJapaneseChars(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 0x3040 && r <= 0x30FF) || (r >= 0x4E00 && r <= 0x9FFF) {
			n++
		}
	}
	return n
}

func countHangulChars(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 0xAC00 && r <= 0xD7AF) || (r >= 0x1100 && r <= 0x11FF) {
			n++
		}
	}
	return n
}

// Every extension under extensions/ must carry all 11 non-English docs linked
// both ways, reference the demo asset, repo memory, and handoff, avoid SVG
// assets on VS Code, keep `--profile` on dsh install commands, and hold real
// translated text in each target script.
func TestExtensionDocsCoverAllElevenLocales(t *testing.T) {
	locales := []string{"zh", "zh-TW", "ja", "ko", "es", "pt", "fr", "de", "ru", "tr", "hi"}
	root := filepath.Join("..", "..")
	entries, err := os.ReadDir(filepath.Join(root, "extensions"))
	if err != nil {
		t.Fatal(err)
	}

	packages := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		readmeBytes, err := os.ReadFile(filepath.Join(root, "extensions", name, "README.md"))
		if err != nil {
			continue
		}
		packages++
		readme := string(readmeBytes)

		for _, lang := range locales {
			wantURL := "https://github.com/vshulcz/deja-vu/blob/main/extensions/" + name + "/docs/" + lang + ".md"
			if !strings.Contains(readme, wantURL) {
				t.Errorf("extensions/%s/README.md missing switcher link %s", name, wantURL)
			}

			docBytes, err := os.ReadFile(filepath.Join(root, "extensions", name, "docs", lang+".md"))
			if err != nil {
				t.Errorf("extensions/%s/docs/%s.md missing: %v", name, lang, err)
				continue
			}
			doc := string(docBytes)

			if !strings.Contains(doc, "[English](../README.md)") {
				t.Errorf("extensions/%s/docs/%s.md missing [English](../README.md)", name, lang)
			}
			for _, other := range locales {
				if other == lang {
					continue
				}
				if !strings.Contains(doc, "("+other+".md)") {
					t.Errorf("extensions/%s/docs/%s.md missing sibling link (%s.md)", name, lang, other)
				}
			}
			for _, req := range []string{
				"assets/demo.gif",
				".deja/memory.jsonl",
				"deja handoff",
				"https://github.com/vshulcz/deja-vu",
			} {
				if !strings.Contains(doc, req) {
					t.Errorf("extensions/%s/docs/%s.md missing %q", name, lang, req)
				}
			}
			if name == "vscode" && strings.Contains(strings.ToLower(doc), ".svg") {
				t.Errorf("extensions/vscode/docs/%s.md contains disallowed .svg reference", lang)
			}
			if name == "dsh" {
				for line := range strings.SplitSeq(doc, "\n") {
					if strings.Contains(line, "dsh plugin") && strings.Contains(line, "add") && !strings.Contains(line, "--profile") {
						t.Errorf("extensions/dsh/docs/%s.md: %q misses --profile", lang, strings.TrimSpace(line))
					}
				}
			}

			switch lang {
			case "zh", "zh-TW":
				if n := countCJK(doc); n < 200 {
					t.Errorf("extensions/%s/docs/%s.md has %d CJK chars (< 200)", name, lang, n)
				}
			case "ja":
				if n := countJapaneseChars(doc); n < 150 {
					t.Errorf("extensions/%s/docs/%s.md has %d Japanese chars (< 150)", name, lang, n)
				}
			case "ko":
				if n := countHangulChars(doc); n < 150 {
					t.Errorf("extensions/%s/docs/%s.md has %d Hangul chars (< 150)", name, lang, n)
				}
			case "ru":
				if n := countRuneRange(doc, 0x0400, 0x04FF); n < 200 {
					t.Errorf("extensions/%s/docs/%s.md has %d Cyrillic chars (< 200)", name, lang, n)
				}
			case "hi":
				if n := countRuneRange(doc, 0x0900, 0x097F); n < 150 {
					t.Errorf("extensions/%s/docs/%s.md has %d Devanagari chars (< 150)", name, lang, n)
				}
			}
		}
	}

	if packages != 16 {
		t.Fatalf("expected 16 extension packages under extensions/, got %d", packages)
	}
}

// Every package under extensions/ is installed by the command extensions/README.md
// gives for it, and that command has to reach the two pages people install
// from. The getting-started page was written when there were three packages
// and kept saying "three more" as the directory grew to seven; README.md's
// table stopped at six, and neither said a word (#1579).
//
// The check is on each package's install command rather than on its directory
// name. A bare name is a substring trap — `pi` is inside "pipe" and "api", so a
// page that never mentions pi would pass — while an install command appears
// only where someone actually wrote down how to install it.
func TestEveryExtensionIsListedWhereItIsInstalledFrom(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "extensions"))
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) < 5 {
		t.Fatalf("found %d packages under extensions/ — the listing is not being read: %v", len(dirs), dirs)
	}

	// The table in extensions/README.md is the source of truth:
	// | [`name/`](name) | Published as | `install command` |
	row := regexp.MustCompile("(?m)^\\|\\s*\\[`([^`/]+)/`\\]\\([^)]*\\)\\s*\\|[^|]*\\|\\s*(.+?)\\s*\\|\\s*$")
	install := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(repoFile(t, "extensions/README.md")), -1) {
		install[m[1]] = strings.Trim(m[2], "`")
	}

	pages := map[string]string{
		"README.md":                       string(repoFile(t, "README.md")),
		"docs/readme/README.zh.md":        string(repoFile(t, "docs/readme/README.zh.md")),
		"docs/guide/getting-started.html": html.UnescapeString(string(repoFile(t, "docs/guide/getting-started.html"))),
	}

	for _, dir := range dirs {
		cmd, ok := install[dir]
		if !ok {
			t.Errorf("extensions/%s/ has no row in extensions/README.md", dir)
			continue
		}
		for name, page := range pages {
			if !strings.Contains(page, cmd) {
				t.Errorf("%s does not say how to install extensions/%s/ — want %q", name, dir, cmd)
			}
		}
	}
}

// The pages stopped counting in prose. A number written beside a list is a
// second copy of the list's length, and it is the copy nobody updates: "three
// more" survived four new packages. The lists say what they hold; the prose
// does not repeat it.
func TestInstallPagesDoNotCountTheirLists(t *testing.T) {
	guide := string(repoFile(t, "docs/guide/getting-started.html"))
	for _, stale := range []string{"Three more keep", "Six harnesses can pull", "wires all three", "all six install"} {
		if strings.Contains(guide, stale) {
			t.Errorf("getting-started still counts a list in prose: %q", stale)
		}
	}
	if strings.Contains(string(repoFile(t, "README.md")), "wires all six of these") {
		t.Error("README still counts its package table in prose")
	}
}

// A count and a decision are not the same claim, and the bar was the same for
// both. "Worked on in three sessions" is a number an agent can do nothing with,
// which is why five; a decision is the thing the same measurement showed an
// agent acts on. Counted from the manifest of a real store across every
// harness: in projects worked in more than one session, 39-62% of files have
// been touched twice against 13-36% five times, so the bar was hiding most of
// the channel behind a number nobody needed. On 120 files an agent really
// edited, the line went from speaking about 6 to speaking about 11.
func TestADecisionSpeaksBelowTheBarACountNeeds(t *testing.T) {
	dir := storeWithAFileDecidedTwice(t, 2)
	line := fileHookLine(dir, "/work/app", "/work/app/quokkabloom.go")
	if line == "" {
		t.Fatal("two sessions with a decision said nothing")
	}
	if !strings.Contains(line, "prior decision") {
		t.Errorf("the line carries no decision: %q", line)
	}
}

// And a count alone still waits for the fifth session: a file two sessions
// touched without deciding anything is ordinary work.
func TestACountAloneStillWaits(t *testing.T) {
	dir := storeWithAFileTouchedQuietly(t, 3)
	if line := fileHookLine(dir, "/work/app", "/work/app/quokkabloom.go"); line != "" {
		t.Errorf("a bare count spoke below the bar: %q", line)
	}
}

func storeWithAFileDecidedTwice(t *testing.T, n int) string {
	t.Helper()
	return storeTouchingAFile(t, n, "the retry budget for quokkabloom stays at four — decided")
}

func storeWithAFileTouchedQuietly(t *testing.T, n int) string {
	t.Helper()
	return storeTouchingAFile(t, n, "looked at quokkabloom.go again (5)")
}

// storeTouchingAFile builds a store where n sessions edited one file and ended
// with the given sentence.
func storeTouchingAFile(t *testing.T, n int, ending string) string {
	t.Helper()
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	for i := range n {
		id := "fd" + string(rune('a'+i))
		writeClaudeFixture(t, filepath.Join(claudeRoot, "app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"user","content":"look at the quokkabloom retries"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit",` +
				`"input":{"file_path":"/work/app/quokkabloom.go","old_string":"a","new_string":"b"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"e1","content":"ok"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"assistant","content":"` + ending + `"}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Some editors never write a final newline. Every writer here ends with one,
// and nothing put back what the file came in with, so a config someone owned
// came back changed for nothing — #2606 the other way round (#2619).
func TestInstallLeavesAFileEndingAsItFoundIt(t *testing.T) {
	cases := []struct {
		target, rel, body string
	}{
		{"codex", ".codex/config.toml", "[tools]\nweb = true"},
		{"grok", ".grok/config.toml", "[tools]\nweb = true"},
		{"goose", ".config/goose/config.yaml", "GOOSE_MODEL: gpt-5"},
		{"hermes", ".hermes/config.yaml", "profile: default"},
		{"opencode", ".config/opencode/opencode.jsonc", "{\n  \"mcp\": {\n    \"theirs\": { \"type\": \"local\" }\n  }\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			setTestHome(t, home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			path := homeConfigPath(home, tc.rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(tc.target, "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(string(b), "\n") {
				t.Fatalf("install added a final newline the reader had not written:\n%q", b)
			}
			if !strings.Contains(string(b), "deja") {
				t.Fatalf("install wrote nothing:\n%s", b)
			}
		})
	}
}

// A file deja creates still ends the way a text file should.
func TestInstallEndsAFileItCreatedWithANewline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if _, err := installTarget("codex", "/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("a config deja wrote itself should end with a newline:\n%q", b)
	}
}

func said(id string, lines ...string) model.Session {
	s := model.Session{ID: id, Harness: "claude", Project: "p"}
	for i, l := range lines {
		role := "assistant"
		if i == 0 {
			role = "user"
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: l})
	}
	return s
}

// Two sessions that settled the same thing say it in one sentence and differ in
// everything around it. Hashing every line that held any word of the question
// made the small talk part of what the session was saying, so the two came back
// as different answers and the block spent both its slots on one.
func TestTwoSessionsSayingOneThingFillOneSlot(t *testing.T) {
	terms := []string{"queue", "throwing", "away"}
	first := said("a",
		"the watermark is what keeps the queue from throwing work away",
		"noted, moving on to the invoice")
	second := said("b",
		"the watermark is what keeps the queue from throwing work away",
		"right, and the queue for that runs nightly")
	if !sameAnswerAs([]model.Session{first}, second, terms) {
		t.Error("two sessions saying the same sentence took a slot each")
	}
}

// The same rule must not merge two answers to one question. A session that
// asked what everyone else asked is not a duplicate of them: what it settled is
// its own, and here the two settled opposite numbers.
func TestTwoAnswersToOneQuestionKeepTheirSlots(t *testing.T) {
	terms := []string{"payment", "webhook", "retries"}
	old := said("old",
		"how many retries for the payment webhook",
		"we set the payment webhook retries to 10")
	recent := said("new",
		"how many retries for the payment webhook",
		"we cut the payment webhook retries to 3")
	if sameAnswerAs([]model.Session{old}, recent, terms) {
		t.Error("two different answers to the same question were merged into one")
	}
}

// A search that lands while another process holds the index lock is told to
// serve what is on disk. On a first build there is nothing on disk, and the run
// said two things that cannot both be true:
//
//	deja: answering from the index of <date>, N sessions — refreshing in the background
//	deja: the index is being rebuilt right now — run this again in a moment
//
// That is a new machine's first question, which is the worst run to contradict
// anything on (#3574).
func TestTheFirstBuildDoesNotClaimToAnswerFromAnIndex(t *testing.T) {
	holdTheIndexLock(t)
	var said bytes.Buffer
	if err := ensureForCLISearch(index.DefaultDir(), query.Options{Query: "retry"}, false, &said); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if strings.Contains(said.String(), "answering from") {
		t.Fatalf("an empty store claimed to be answering from an index:\n%s", said.String())
	}
}

// With a store that answers, the line is right and stays: that is the case it
// was written for — an index serving while the refresh runs behind it.
func TestAStoreThatAnswersStillSaysItIsServingWhatItHas(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(tmp, "claude", "app", "one.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"the exporter drops every third retry"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	// A transcript that has appeared since, so there is something to refresh.
	writeClaudeFixture(t, filepath.Join(tmp, "claude", "app", "two.jsonl"), "s2", []string{
		`{"type":"user","sessionId":"s2","timestamp":"2026-09-01T11:00:00Z","message":{"role":"user","content":"the cache key ignored the tenant id"}}`,
	})
	holdTheIndexLock(t)

	var said bytes.Buffer
	if err := ensureForCLISearch(index.DefaultDir(), query.Options{Query: "retry"}, false, &said); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !strings.Contains(said.String(), "answering from") {
		t.Fatalf("a store that can answer said nothing about serving the older view:\n%s", said.String())
	}
}

// The minute after install: a build has been asked for and has published no
// progress yet. The statusline called that a quiet day and doctor told the
// reader to start what was already running — both because they required a
// manifest that a first build does not have yet (#925).
func TestFirstBuildIsNotCalledSilenceOrAbsence(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Requested just now; nothing published, no manifest — the first build.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var line bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(""), &line); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line.String(), "indexing your history") {
		t.Errorf("statusline during the first build: %q", line.String())
	}

	var report bytes.Buffer
	doctorIndex(&report, doctorIndexReport{State: "missing", Path: dir}, dir)
	if !strings.Contains(report.String(), "building now") {
		t.Errorf("doctor during the first build:\n%s", report.String())
	}
	if strings.Contains(report.String(), "run `deja warmup`") {
		t.Errorf("doctor told the reader to start a build that is running:\n%s", report.String())
	}

	// An index nobody is building is still called what it is.
	if err := os.Remove(filepath.Join(dir, "warmup.sentinel")); err != nil {
		t.Fatal(err)
	}
	report.Reset()
	doctorIndex(&report, doctorIndexReport{State: "missing", Path: dir}, dir)
	if !strings.Contains(report.String(), "not built") {
		t.Errorf("an idle missing index stopped saying so:\n%s", report.String())
	}
}

// The first command a new user runs is `deja`. Before this, with nothing
// indexed, it printed forty lines of usage; these tests pin the two honest
// answers — build and show, or say plainly that nothing was found.

func TestFirstRunBuildsTheIndexAndBriefs(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"claude-first","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the retry loop drops the last attempt"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if index.HasManifest(dir) {
		t.Fatal("test starts with an index, which is not the case under test")
	}

	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatalf("brief: %v", err)
	}
	if !index.HasManifest(dir) {
		t.Fatal("the first run should have built the index")
	}
	got := out.String()
	if !strings.Contains(got, "deja dev") || !strings.Contains(got, "1 session") {
		t.Fatalf("want a brief over the freshly built index, got:\n%s", got)
	}
	if strings.Contains(got, "Usage:") {
		t.Fatalf("usage should not be the answer to a bare command:\n%s", got)
	}
}

func TestFirstRunSaysSoWhenThereIsNoHistory(t *testing.T) {
	// Hermetic env with no stores at all: the build finds nothing, and the
	// answer has to name where deja looked rather than dumping command syntax.
	hermeticEnv(t)
	var out bytes.Buffer
	if err := runBrief(index.DefaultDir(), &out); err != nil {
		t.Fatalf("brief: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "no agent history found") {
		t.Fatalf("got:\n%s", got)
	}
	for _, want := range []string{"deja sources", "deja doctor", "deja help"} {
		if !strings.Contains(got, want) {
			t.Errorf("the empty state should point at %q", want)
		}
	}
	if strings.Contains(got, "deja [flags] <query>") {
		t.Fatalf("usage block leaked into the empty state:\n%s", got)
	}
}

func TestBriefLeavesAnExistingIndexAlone(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"claude-x","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"pgbouncer prepared statements"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	var first bytes.Buffer
	if err := runBrief(dir, &first); err != nil {
		t.Fatal(err)
	}
	// A second run must not rebuild: the brief reads the index as it stands.
	before, err := os.Stat(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err = runBrief(dir, &second); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("the brief rebuilt an index that already existed")
	}
}

func TestInstallSkipsTheBuildWhenAsked(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"claude-y","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"scripted install"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := runInstall(dir, []string{"claude", "--no-index", "--no-guidance"}, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if index.HasManifest(dir) {
		t.Fatal("--no-index should leave the build for later")
	}
}

func TestInstallBuildsTheIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"claude-z","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"index at install time"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := runInstall(dir, []string{"claude", "--no-guidance"}, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !index.HasManifest(dir) {
		t.Fatal("install should leave a usable index behind")
	}
}

// A fresh install lands with an index and no agent reading it. install.sh drops
// a binary, a bare `deja` builds the memory and reports it, and the command
// that makes any of it arrive on its own — `deja install --auto` — was named
// nowhere in that path: not by the installer, and not by the screen the reader
// is looking at.
//
// Someone who stops there has installed a search tool by accident. They have to
// remember to run deja by hand, which is the one thing this is built not to
// need.
func TestTheBriefSaysWhenNoAgentIsWired(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"wire-1","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the retry loop drops the last attempt"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runBrief(index.DefaultDir(), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "install --auto") {
		t.Errorf("history was found and no agent is wired, and the screen never says so:\n%s", got)
	}
}

// And it goes quiet once something is wired: a line that is always there is
// not a prompt, it is furniture.
func TestTheBriefStopsSayingItOnceWired(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"wire-2","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the retry loop drops the last attempt"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// One wiring file is enough: the question is whether anything reads this.
	wired := autoWirings()[0].path()
	if err := os.MkdirAll(filepath.Dir(wired), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wired, []byte("hook-context\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runBrief(index.DefaultDir(), &out); err != nil {
		t.Fatal(err)
	}
	// On the label rather than the sentence: the sentence is copy and has been
	// reworded once already (#1411), and a guard that reads a literal quietly
	// stops guarding the moment the words change.
	if strings.Contains(out.String(), "deja install --auto") {
		t.Errorf("a wired machine is still being told to wire itself:\n%s", out.String())
	}
}

// Narrowing a marathon session lowercases every message once and hands the
// result to the density pass as well. Both passes have to keep matching text
// the user typed in another case, in either alphabet.
func TestFocusSessionIgnoresCase(t *testing.T) {
	msgs := make([]model.Message, 0, 12)
	for range 10 {
		msgs = append(msgs, model.Message{Role: "assistant", Text: "unrelated chatter"})
	}
	msgs = append(msgs,
		model.Message{Role: "assistant", Text: "PgBouncer pool size settled at 40"},
		model.Message{Role: "assistant", Text: "Индексацию перенесли в фоновый воркер"})

	got := focusSession(model.Session{Messages: msgs}, []string{"pgbouncer"})
	if len(got.Messages) == 0 {
		t.Fatal("a session mentioning the term in another case was narrowed to nothing")
	}
	var found bool
	for _, m := range got.Messages {
		if m.Text == "PgBouncer pool size settled at 40" {
			found = true
		}
	}
	if !found {
		t.Error("the matching message was dropped by the narrowing")
	}

	got = focusSession(model.Session{Messages: msgs}, []string{"индексация"})
	found = false
	for _, m := range got.Messages {
		if m.Text == "Индексацию перенесли в фоновый воркер" {
			found = true
		}
	}
	if !found {
		t.Error("the Cyrillic term matched in its own case only")
	}
}

func TestDensestMessagesUsesTheLoweredText(t *testing.T) {
	msgs := []model.Message{
		{Role: "assistant", Text: "PgBouncer and the POOL both appear here"},
		{Role: "assistant", Text: "nothing to see"},
		{Role: "assistant", Text: "pool alone"},
	}
	low := make([]string, len(msgs))
	for i, m := range msgs {
		low[i] = strings.ToLower(m.Text)
	}
	got := densestMessages(msgs, low, []string{"pgbouncer", "pool"}, 1)
	if len(got) != 1 || got[0].Text != "PgBouncer and the POOL both appear here" {
		t.Errorf("kept %v, want the message carrying both terms", got)
	}
}

// A long session is narrowed to the neighbourhood of its matches before the
// block is built, and that narrowing matched exact forms only. The ranking
// reaches the session through a stem fold and the digest shows a line through
// one too, so the narrowing was the one step still spelling the word the way
// the question happened to spell it — and when it found nothing it dropped the
// session whole.
//
// Measured on a frozen copy of a real index: every block that opened on a line
// carrying none of its terms was for a session that does hold such a line.
func TestFocusKeepsAnInflectedMatch(t *testing.T) {
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	msgs := make([]model.Message, 0, dejaVuMaxMessages+4)
	for i := range dejaVuMaxMessages + 2 {
		at := start.Add(time.Duration(i) * time.Minute)
		msgs = append(msgs, model.Message{Role: "user", Text: "продолжай дальше", Time: at})
	}
	// The only mention, and in another case than the question asks in.
	msgs = append(msgs, model.Message{
		Role: "assistant",
		Text: "решили: индексацию перенесли в фоновый воркер после записи",
		Time: start.Add(time.Duration(len(msgs)) * time.Minute),
	})

	s := model.Session{
		ID: "focus-1", Harness: "claude", Project: "proj",
		Started: start, Updated: start.Add(time.Duration(len(msgs)) * time.Minute), Messages: msgs,
	}
	got := focusSession(s, []string{"индексация"})
	if len(got.Messages) == 0 {
		t.Fatal("the session was dropped whole because the question spelled the word in another case")
	}
	found := false
	for _, m := range got.Messages {
		if m.Text == "решили: индексацию перенесли в фоновый воркер после записи" {
			found = true
		}
	}
	if !found {
		t.Errorf("narrowing kept %d messages and threw away the only one that answers", len(got.Messages))
	}
}

// An index emptied by the reader's own forget is not an unbuilt one, and "run
// `deja index`" cannot bring a tombstoned session back. search has said so
// since #844; the two listing screens read it as data loss (#1007).
func TestListingsSayWhenTheStoreWasEmptiedByAForget(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the ticker window stays at 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--session", "loc"); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"last", "stats"} {
		out, err := captureRunStderr(t, cmd)
		if err != nil {
			t.Fatal(err)
		}
		combined := out
		if cmd == "stats" {
			text, err := captureRun(t, cmd)
			if err != nil {
				t.Fatal(err)
			}
			combined += text
		}
		if !strings.Contains(combined, "forgotten") {
			t.Errorf("%s does not say the store was emptied by a forget:\n%s", cmd, combined)
		}
		if strings.Contains(combined, "run `deja index`") {
			t.Errorf("%s advises a build that cannot bring it back:\n%s", cmd, combined)
		}
	}
}

// The line that says which forgotten session a note came from is matched on the
// note's local id, and a note that arrived by sync carries `imported-…` with
// the real id in OrigID — so the reader who asks about that session by name is
// handed the note with the fact removed (#2839, the family #2833 closed for the
// ranking).
func TestAnImportedNoteStillNamesItsForgottenSource(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"longs","cwd":"/w/p","timestamp":"2026-06-01T10:00:00Z",` +
		`"message":{"role":"user","content":"the goblin pool deadlocks"}}`
	if err := os.WriteFile(filepath.Join(store, "longs.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "longs"); err != nil {
		t.Fatal(err)
	}

	note := model.Session{
		Harness: "deja", ID: "imported-ccc333", OrigID: "deja-note-claude-longs",
		Project: "imported:api",
	}
	if got := forgottenSourceNote(note, "claude:longs", false); !strings.Contains(got, "forgotten") {
		t.Errorf("an imported note says nothing about the session it distils: %q", got)
	}

	local := model.Session{Harness: "deja", ID: "deja-note-claude-longs", Project: "api"}
	if got := forgottenSourceNote(local, "claude:longs", false); !strings.Contains(got, "forgotten") {
		t.Errorf("the local case regressed: %q", got)
	}
}

// And the case where the transcript travelled too: it is tombstoned under the
// id it has here, while the note names the id it had there. Asking about
// either has to say the session is gone (#2839).
func TestAnImportedSourceIsStillNamedAsForgotten(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for _, r := range []index.SyncRecord{
		{Harness: "claude", SessionID: "longs", Project: "api", Role: "user", Text: "the goblin pool deadlocks"},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(index.DefaultDir())
	if err != nil {
		t.Fatal(err)
	}
	local := ""
	for _, m := range metas {
		if m.OrigID == "longs" {
			local = m.ID
		}
	}
	if local == "" {
		t.Fatalf("the imported session is not in the index: %+v", metas)
	}
	if _, err := captureRunStderr(t, "forget", "--session", local); err != nil {
		t.Fatal(err)
	}

	note := model.Session{
		Harness: "deja", ID: "imported-ccc333", OrigID: "deja-note-claude-longs",
		Project: "imported:api",
	}
	got := forgottenSourceNote(note, "claude:longs", false)
	if !strings.Contains(got, "forgotten") {
		t.Errorf("a forgotten imported source is not named: %q", got)
	}
	// And it names the key `deja forget --list` will show, which is the local
	// one rather than the id the note carries.
	if !strings.Contains(got, local) {
		t.Errorf("the line names a key the reader cannot find: %q, local key holds %q", got, local)
	}
}

// Asking for a forgotten session by the id you remember answered with the note
// promoted from it and said nothing, so the reply looked like the session
// itself until you noticed the id had changed (#971).
func TestAskingForAForgottenSessionSaysSo(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the decision: keep the ticker window at 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s14","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s14.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s14"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "s14"); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"show", "ctx", "share"} {
		note, err := captureRunStderr(t, cmd, "s14")
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		if !strings.Contains(note, "is forgotten") {
			t.Errorf("%s answered with the note and did not say the session is gone: %q", cmd, note)
		}
	}

	// Asking for the note itself is not a surprise, and an ordinary query that
	// lands on it is not asking about the source at all.
	if note, err := captureRunStderr(t, "show", "deja-note-claude-s14"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(note, "is forgotten") {
		t.Errorf("asking for the note by its own id was answered with a warning: %q", note)
	}
	if note, err := captureRunStderr(t, "ctx", "ticker window"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(note, "is forgotten") {
		t.Errorf("an ordinary query was answered with a warning about a session nobody named: %q", note)
	}
}

func TestStatsFormattingHelpers(t *testing.T) {
	if got := stats.TrimRunes("abcdef", 3); got != "ab…" {
		t.Fatalf("stats.TrimRunes = %q", got)
	}
	if got := stats.TrimRunes("abcdef", 1); got != "a" {
		t.Fatalf("stats.TrimRunes n=1 = %q", got)
	}
	if got := stats.ScaledBar(1, 100, 10); got != 1 {
		t.Fatalf("stats.ScaledBar = %d", got)
	}
	if got := stats.ScaledBar(0, 100, 10); got != 0 {
		t.Fatalf("stats.ScaledBar zero = %d", got)
	}
	if valueOrDash("") != "-" || valueOrDash("x") != "x" {
		t.Fatal("valueOrDash mismatch")
	}
	if !stats.Noise("<local-command x>") || stats.Noise("real title") {
		t.Fatal("stats.Noise mismatch")
	}
	if got := stats.Title(model.Session{Title: "<command-noise>", Messages: []model.Message{{Role: "assistant", Text: "skip"}, {Role: "user", Text: strings.Repeat("word ", 20)}}}); !strings.HasSuffix(got, "…") {
		t.Fatalf("stats.Title = %q", got)
	}
	if got := stats.Title(model.Session{Title: "Clean title"}); got != "Clean title" {
		t.Fatalf("stats.Title title = %q", got)
	}
	if got := statHarnessTag("claude", true); !strings.Contains(got, "[claude]") || !strings.Contains(got, harnesscolor.ANSI("claude")) {
		t.Fatalf("claude tag = %q", got)
	}
	if got := statHarnessTag("codex", true); !strings.Contains(got, harnesscolor.ANSI("codex")) {
		t.Fatalf("codex tag = %q", got)
	}
	if got := statHarnessTag("opencode", true); !strings.Contains(got, harnesscolor.ANSI("opencode")) {
		t.Fatalf("opencode tag = %q", got)
	}
	if got := statHarnessTag("other", true); got != harnesscolor.Tag("other", true) {
		t.Fatalf("other tag = %q", got)
	}
	t.Setenv("NO_COLOR", "1")
	if statColorOK(os.Stdout) || statColorOK(&bytes.Buffer{}) {
		t.Fatal("color should be disabled")
	}
}

// Two failures of one service differ only in how it failed — refused, or reset
// mid-read — and everything that says which is past the 79 bytes a row holds.
// The list then printed the same line twice, each with its own count, and the
// reader had no way to tell them apart (#3401).
func TestFrictionRowsThatShareTheirHeadStillDiffer(t *testing.T) {
	root := frictionEnv(t)
	const shared = `Get "http://toy-load-toy-load.default.svc.cluster.local/work?cpu_ms=20&jitter_ms=5": `
	lines := []string{
		shared + "dial tcp 0.0.0.0:0->192.0.2.73:80: connect: connection refused",
		shared + "read tcp 192.0.2.35:39473->192.0.2.73:80: read: connection reset by peer",
	}
	if len(shared) <= 79 {
		t.Fatalf("the shared head is %d bytes, so the rows never collide", len(shared))
	}
	proj := filepath.Join(root, "projects", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, line := range lines {
		for i := range index.FrictionMinSessions {
			sid := fmt.Sprintf("s%d%02d", n, i)
			payload, err := json.Marshal(line + "\nexit status 1")
			if err != nil {
				t.Fatal(err)
			}
			row := `{"type":"user","sessionId":"` + sid + `","cwd":"/repo",` +
				`"timestamp":"2026-07-30T03:05:05Z","message":{"role":"user","content":` +
				`[{"type":"tool_result","content":` + string(payload) + `}]}}` + "\n"
			if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(row), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "sessions  ") {
			rows = append(rows, strings.TrimSpace(l))
		}
	}
	if len(rows) != 2 {
		t.Fatalf("want the two failures as two rows, got %d:\n%s", len(rows), buf.String())
	}
	if rows[0] == rows[1] {
		t.Errorf("both failures print as %q — the reader sees one error twice", rows[0])
	}
	for _, r := range rows {
		if len(r) > 79+len("   5 sessions  ") {
			t.Errorf("row is %d bytes, past what a terminal row holds: %q", len(r), r)
		}
		if !strings.Contains(r, "toy-load") {
			t.Errorf("row no longer names the failing service: %q", r)
		}
	}
}

// friction's own note says how many sessions a rule keeps out; the sentence
// beside it counted the whole store, so one screen said both "none of the 4
// indexed sessions recorded tool output" and "the ignore rule keeps 2 sessions
// out of this listing". Every other surface counts what the answer could have
// held (#2707); this is the one that did not (#2709).
func TestFrictionCountsWhatItMayRead(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	write := func(project, id, day string) {
		t.Helper()
		store := filepath.Join(root, "-"+project)
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","message":{"role":"user","content":"deploy the staging cluster"},` +
			`"timestamp":"2026-08-0` + day + `T10:00:00Z","sessionId":"` + id + `","cwd":"/` + project + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing recorded tool output anywhere, and half the store is hidden: the
	// sentence is about how much friction had to read, which is two.
	write("keep", "k1", "1")
	write("keep", "k2", "2")
	for i := 1; i <= 2; i++ {
		write("zonkohide", "h"+strconv.Itoa(i), strconv.Itoa(i+2))
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*zonkohide*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "none of the 2 indexed sessions recorded tool output") {
		t.Errorf("friction did not count what it may read:\n%s", out)
	}
}

// And with nothing hidden the sentence still names the store, because there
// the two are the same number.
func TestFrictionStillNamesTheStoreWhenNothingIsHidden(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-keep")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		id := "k" + strconv.Itoa(i)
		body := `{"type":"user","message":{"role":"user","content":"deploy the staging cluster"},` +
			`"timestamp":"2026-08-0` + strconv.Itoa(i) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/keep"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "none of the 3 indexed sessions recorded tool output") {
		t.Errorf("a store with nothing hidden lost its count:\n%s", out)
	}
}

// And a machine whose history is entirely behind a rule is not a machine
// without history. Counting only what friction may read sent that store into
// the arm written for an empty index — "run `deja index`" over a note saying
// two rules are hiding things, which is the claim #1020 and #1044 closed
// elsewhere.
func TestFrictionDoesNotCallAHiddenStoreAnEmptyMachine(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-zonkohide")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		id := "h" + strconv.Itoa(i)
		body := `{"type":"user","message":{"role":"user","content":"deploy the staging cluster"},` +
			`"timestamp":"2026-08-0` + strconv.Itoa(i) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/zonkohide"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*zonkohide*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deja index") || strings.Contains(out, "no agent history was found") {
		t.Errorf("a store hidden by a rule was reported as a machine with no history:\n%s", out)
	}
	if !strings.Contains(out, "the ignore rule") {
		t.Errorf("nothing named the rule that leaves friction nothing to read:\n%s", out)
	}
	if !strings.Contains(out, "2 indexed session") {
		t.Errorf("the sessions on disk went uncounted:\n%s", out)
	}
}

// The JSON row carries the recorded error, not the terminal's view of it.
// trimFriction bounds a line to 79 bytes because that is what fits a row; a
// consumer has no row, and a clipped error can no longer be matched against
// the error it came from or handed back to `deja fix`.
func TestFrictionJSONCarriesTheWholeErrorLine(t *testing.T) {
	root := frictionEnv(t)
	// With an escape in it, so the row is pinned as sanitised as well as whole.
	long := "psql: error: \x1b[31mconnection\x1b[0m to server at \"db-primary-replica-eu-central.internal.example.com\" (192.0.2.14), port 5432 failed: Connection refused"
	if len(long) <= 79 {
		t.Fatalf("the fixture is %d bytes, so it never reaches the cut", len(long))
	}
	proj := filepath.Join(root, "projects", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range index.FrictionMinSessions {
		sid := fmt.Sprintf("long%02d", i)
		payload, err := json.Marshal(long + "\nexit status 2")
		if err != nil {
			t.Fatal(err)
		}
		row := `{"type":"user","sessionId":"` + sid + `","cwd":"/repo",` +
			`"timestamp":"2026-07-30T03:05:05Z","message":{"role":"user","content":` +
			`[{"type":"tool_result","content":` + string(payload) + `}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(row), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), []string{"--json"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got frictionJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Rows) == 0 {
		t.Fatalf("nothing was reported, so this asserts nothing:\n%s", buf.String())
	}
	if !strings.Contains(got.Rows[0].Error, "Connection refused") {
		t.Errorf("the row was cut before the end of the error:\n%q", got.Rows[0].Error)
	}
	if strings.HasSuffix(got.Rows[0].Error, "…") {
		t.Errorf("the row carries the terminal's ellipsis:\n%q", got.Rows[0].Error)
	}
	if strings.Contains(got.Rows[0].Error, "\x1b[") {
		t.Errorf("an ANSI escape reached the row:\n%q", got.Rows[0].Error)
	}
}

// The prose path keeps its bound: a terminal row is still a terminal row.
func TestFrictionProseStillFitsARow(t *testing.T) {
	long := strings.Repeat("connection refused on the replica ", 6)
	if got := trimFriction(long); len(got) > 79 {
		t.Errorf("the prose line is %d bytes, over the row bound", len(got))
	}
}

// friction claims to print "what this machine keeps tripping over", so a cut
// list with nothing after it reads as the whole of it — the sentence `how` and
// `files` print for the same flag (#2311).
func TestFrictionSaysWhatTheLimitLeftOut(t *testing.T) {
	errs := []string{
		"undefined: snorblefunc in vendor/blarg/api.go",
		"panic: quaxbolt deadline exceeded",
		"TypeError: cannot read property 'zonk' of undefined",
	}
	lines := make([]string, 0, 3*len(errs))
	n := 0
	for _, e := range errs {
		for k := range 3 {
			lines = append(lines, fmt.Sprintf(
				`{"type":"user","sessionId":"s%d","cwd":"/w/p","timestamp":"2026-07-2%dT10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`,
				n, k+1, e))
			n++
		}
	}
	seedFrictionStore(t, lines)

	var all bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &all); err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(all.String(), " sessions  "); rows != 3 {
		t.Fatalf("seeded %d recurring errors, friction found %d:\n%s", len(errs), rows, all.String())
	}

	said, err := captureRunStderr(t, "friction", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "2 of 3") {
		t.Errorf("friction cut the list without saying so: %q", said)
	}

	// Nothing held back, nothing announced.
	said, err = captureRunStderr(t, "friction", "--limit", "9")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(said, "showing") {
		t.Errorf("friction announced a window it did not apply: %q", said)
	}
}

// With every tool-output session withheld by a rule, friction said the machine
// had never recorded any — a claim about the store, on the surface someone
// reaches for when recall feels thin (#2319).
func TestFrictionNamesTheRuleThatEmptiedIt(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question, no commands run"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	var buf bytes.Buffer
	for j := range 3 {
		rec := index.SyncRecord{
			Harness: "claude", SessionID: "peer" + string(rune('0'+j)), Project: "secretclient/api",
			Role: "tool-output", Text: "panic: quaxbolt overflow in ledger/quaxbolt.go",
			Time: time.Date(2026, 8, 4, 12, j, 0, 0, time.UTC),
		}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}

	// The premise: without a rule, friction has something to report.
	var open bytes.Buffer
	if err := runFriction(dir, nil, &open); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(open.String(), "quaxbolt") {
		t.Fatalf("the imported error is not recurring even with no rule set:\n%s", open.String())
	}

	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	var closed bytes.Buffer
	if err := runFriction(dir, nil, &closed); err != nil {
		t.Fatal(err)
	}
	got := closed.String()
	if strings.Contains(got, "quaxbolt") {
		t.Fatalf("the withheld error is still on screen:\n%s", got)
	}
	if strings.Contains(got, "none of the") {
		t.Errorf("friction still says the machine recorded no tool output:\n%s", got)
	}
	if !strings.Contains(got, "trust policy") {
		t.Errorf("friction does not name the rule that emptied it:\n%s", got)
	}
}

// friction reads across the whole store; a peer's recurring errors (its infra
// IPs, its hosts) surfaced here even when the trust policy withheld imported
// content from search. Browsing is the search activation, as in last/stats
// (#937, friction gap #1120).
func TestFrictionHonoursTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	// One local tool-output session (does not recur on its own).
	local := `{"type":"user","sessionId":"m1","cwd":"/m","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"local one-off oddity"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "m1.jsonl"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Import three peer sessions that all hit the same error.
	in := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 1; i <= 3; i++ {
		b.WriteString(`{"harness":"claude","session_id":"pe` + string(rune('0'+i)) + `","project":"p","role":"tool-output","text":"connection refused to 192.0.2.5:5432 backpressure","time":"2026-07-2` + string(rune('0'+i)) + `T10:00:00Z"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(in, "deja-sync-peer-1.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(index.DefaultDir(), in); err != nil {
		t.Fatal(err)
	}

	// No policy: the imported error recurs and is shown.
	if out, _ := captureRun(t, "friction"); !strings.Contains(out, "192.0.2.5") {
		t.Fatalf("without policy the imported error should surface:\n%s", out)
	}
	// Deny imported for the search activation: friction must withhold it.
	pol := filepath.Join(os.Getenv("HOME"), ".config", "deja", "policy.json")
	if err := os.MkdirAll(filepath.Dir(pol), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "192.0.2.5") {
		t.Errorf("friction leaked an imported peer error past a deny policy:\n%s", out)
	}
}

// A recorded error line is untrusted text — it came out of a transcript deja
// did not write. Every surface that prints one runs it through search.SafeLine
// first; friction was the exception, and printed the bytes as recorded.
//
// Found while reviewing #2895, whose JSON path inherited the same line. The
// terminal obeys what it is sent: an ANSI escape recolours the rest of the
// screen, and U+202E reverses the reading order of everything after it, so a
// command in a wall can be made to read as something else.
func TestFrictionDoesNotPrintWhatATerminalWouldObey(t *testing.T) {
	root := frictionEnv(t)
	writeEscapedFriction(t, root)

	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "shellcheck") {
		t.Fatalf("the wall was not reported at all, so this asserts nothing:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("an ANSI escape reached the terminal:\n%q", out)
	}
	if strings.Contains(out, "\u202e") {
		t.Errorf("a bidi override reached the terminal:\n%q", out)
	}
}

func writeEscapedFriction(t *testing.T, root string) {
	t.Helper()
	proj := filepath.Join(root, "projects", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := "command not found: \x1b[31mshellcheck\x1b[0m\u202eevil"
	for i := range index.FrictionMinSessions {
		sid := fmt.Sprintf("esc%02d", i)
		payload, err := json.Marshal(bad + "\nexit status 127")
		if err != nil {
			t.Fatal(err)
		}
		row := `{"type":"user","sessionId":"` + sid + `","cwd":"/repo",` +
			`"timestamp":"2026-07-30T03:05:05Z","message":{"role":"user","content":` +
			`[{"type":"tool_result","content":` + string(payload) + `}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(row), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// One failure whose port and host differ per run was a row per run, each row
// holding one session, so none reached the three-session floor and the report
// left out the thing the machine hits most (#2375).
func TestFrictionGroupsOneFailureAcrossItsNumbers(t *testing.T) {
	lines := make([]string, 0, 4)
	for i := range 4 {
		lines = append(lines, fmt.Sprintf(
			`{"type":"user","sessionId":"s%d","cwd":"/w/p","timestamp":"2026-07-2%dT10:00:00Z",`+
				`"message":{"role":"user","content":[{"type":"tool_result","content":"dial tcp 192.0.2.%d:%d: connect: connection refused"}]}}`,
			i, i+1, 7+i, 5432+i))
	}
	seedFrictionStore(t, lines)

	var out bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "4 sessions") {
		t.Errorf("the four runs are not one wall:\n%s", got)
	}
	if strings.Count(got, "connection refused") != 1 {
		t.Errorf("one failure is listed more than once:\n%s", got)
	}
	// The newest occurrence is what a reader can act on.
	if !strings.Contains(got, "192.0.2.10:5435") {
		t.Errorf("the row does not show the newest of the four:\n%s", got)
	}
}

// Two failures that differ in more than a number stay two rows.
func TestFrictionKeepsDifferentFailuresApart(t *testing.T) {
	lines := make([]string, 0, 6)
	for i := range 3 {
		lines = append(lines, fmt.Sprintf(
			`{"type":"user","sessionId":"a%d","cwd":"/w/p","timestamp":"2026-07-2%dT10:00:00Z",`+
				`"message":{"role":"user","content":[{"type":"tool_result","content":"dial tcp 192.0.2.%d:5432: connect: connection refused"}]}}`,
			i, i+1, 7+i),
			fmt.Sprintf(
				`{"type":"user","sessionId":"b%d","cwd":"/w/p","timestamp":"2026-07-2%dT11:00:00Z",`+
					`"message":{"role":"user","content":[{"type":"tool_result","content":"psql: FATAL: sorry, too many clients already"}]}}`,
				i, i+1))
	}
	seedFrictionStore(t, lines)

	var out bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "connection refused") || !strings.Contains(got, "too many clients") {
		t.Errorf("two different failures did not both survive:\n%s", got)
	}
}

func TestTrimFriction(t *testing.T) {
	long := strings.Repeat("x", 90)
	got := trimFriction(long)
	if len([]rune(got)) != 77 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got %d runes: %q", len([]rune(got)), got)
	}
	if got := trimFriction("short"); got != "short" {
		t.Fatalf("got %q", got)
	}
}

// writeFrictionCorpus lays down n claude sessions that each hit the same error,
// plus one that does not.
func writeFrictionCorpus(t *testing.T, root string, n int) {
	t.Helper()
	proj := filepath.Join(root, "projects", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, role, text string) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": sid, "cwd": "/repo",
			"timestamp": "2026-07-30T03:04:05Z",
			"message":   map[string]any{"role": role, "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for i := range n {
		sid := fmt.Sprintf("frict%02d", i)
		rows := []string{
			line(sid, "user", "run the deploy script for the staging cluster"),
			line(sid, "assistant", "running it now"),
			`{"type":"user","sessionId":"` + sid + `","cwd":"/repo",` +
				`"timestamp":"2026-07-30T03:05:05Z","message":{"role":"user","content":` +
				`[{"type":"tool_result","content":"zsh:1: command not found: shellcheck\nexit status 127"}]}}`,
		}
		p := filepath.Join(proj, sid+".jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sid := "clean01"
	rows := []string{
		line(sid, "user", "review the block layout change"),
		line(sid, "assistant", "looks right"),
	}
	if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func frictionEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	t.Setenv("DEJA_EMBED_OFF", "1")
	return root
}

func TestFrictionReportsRecurringErrors(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "command not found: shellcheck") {
		t.Fatalf("the recurring error is missing:\n%s", out)
	}
	if !strings.Contains(out, "claude") {
		t.Fatalf("the harness is missing:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d sessions", index.FrictionMinSessions)) {
		t.Fatalf("wrong session count:\n%s", out)
	}
	// exit status 127 sits beside the error in every one of those sessions and
	// says nothing about what is missing.
	if strings.Contains(out, "exit status") {
		t.Fatalf("a generic line was reported:\n%s", out)
	}
}

// Twice is a coincidence. Reporting it would make the command noise on any
// store, which is the whole reason for the threshold.
func TestFrictionStaysQuietBelowThreshold(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions-1)
	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "nothing recurring") {
		t.Fatalf("want the empty state, got:\n%s", buf.String())
	}
}

func TestFrictionLimit(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), []string{"--limit", "1"}, &buf); err != nil {
		t.Fatal(err)
	}
	if err := runFriction(index.DefaultDir(), []string{"--limit", "0"}, &buf); err == nil {
		t.Fatal("--limit 0 accepted")
	}
	if err := runFriction(index.DefaultDir(), []string{"--limit", "many"}, &buf); err == nil {
		t.Fatal("--limit many accepted")
	}
}

func TestFrictionOnBlockedIndex(t *testing.T) {
	frictionEnv(t)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(blocked, "child"))
	if err := runFriction(index.DefaultDir(), nil, &bytes.Buffer{}); err == nil {
		t.Fatal("friction accepted a blocked index")
	}
}

// The count is sessions-with-tool-output and it read as sessions-indexed, so a
// store with six conversations reported zero — the sentence #637 was about
// (#705).
func TestFrictionEmptyAnswerDistinguishesThreeStores(t *testing.T) {
	t.Run("no sessions at all", func(t *testing.T) {
		seedFrictionStore(t, nil)
		out := runFrictionFor(t)
		// Since #1044 the empty case shares the wording every other surface
		// uses, which also separates "no history here" from "a store deja
		// cannot read".
		if !strings.Contains(out, "no agent history was found on this machine") {
			t.Errorf("empty store: %q", out)
		}
	})
	t.Run("sessions without tool output", func(t *testing.T) {
		seedFrictionStore(t, []string{
			`{"type":"user","sessionId":"a","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"plain talk only"}}`,
			`{"type":"user","sessionId":"b","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":"more plain talk"}}`,
		})
		out := runFrictionFor(t)
		if !strings.Contains(out, "none of the 2 indexed sessions recorded tool output") {
			t.Errorf("no tool output: %q", out)
		}
	})
	t.Run("tool output but nothing recurring", func(t *testing.T) {
		// Three sessions, two of which recorded tool output: the two numbers
		// have to differ, or either one passes for the other.
		seedFrictionStore(t, []string{
			`{"type":"user","sessionId":"a","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"ERROR: one of a kind"}]}}`,
			`{"type":"user","sessionId":"b","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"ERROR: also unique"}]}}`,
			`{"type":"user","sessionId":"c","cwd":"/w/p","timestamp":"2026-07-23T10:00:00Z","message":{"role":"user","content":"no commands were run here"}}`,
		})
		out := runFrictionFor(t)
		if !strings.Contains(out, "in the 2 sessions that recorded tool output (of 3 indexed)") {
			t.Errorf("tool output present: %q", out)
		}
	})
}

func seedFrictionStore(t *testing.T, lines []string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i, line := range lines {
		name := fmt.Sprintf("s%d.jsonl", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
}

func runFrictionFor(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// A store deja is not allowed to open produces the same zero as an empty
// machine, and friction reported the machine empty — the failure #1020 closed
// on last and search, one surface further along (#1044).
func TestFrictionDoesNotCallALockedStoreEmpty(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a decision about queues"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store, 0o755) })

	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "could not be read (permission denied)") {
		t.Errorf("friction does not name the locked store:\n%s", out)
	}
	if strings.Contains(out, "no sessions are indexed yet") {
		t.Errorf("friction still calls the machine empty:\n%s", out)
	}
}

// The note says the policy hides matching sessions, so a hidden session has to
// hold something friction would have reported before it counts. It counted
// every session that recorded any tool output at all, so a machine whose hidden
// history is `ok 12 tests passed` was told a rule was keeping its recurring
// failures back (#2794, the sibling of #2766).
func TestFrictionCountsTheWithheldSessionsItWouldHaveReported(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	quiet := `{"type":"user","timestamp":"2026-07-10T10:00:00Z","sessionId":"bbbb0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"a1","content":"ok 12 tests passed\nHEAD is now at 1a2b3c4"}]}}`
	if err := os.WriteFile(filepath.Join(store, "quiet.jsonl"), []byte(quiet+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"*":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	note, err := captureRunStderr(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "hides") {
		t.Errorf("the hidden session holds nothing friction reports, and the note claims otherwise:\n%s", note)
	}
	// The line on stdout counts something else — the sessions a rule took away
	// that recorded tool output at all — and it still has to be true, and still
	// has to name the rule that actually applies.
	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the trust policy withheld the 1 session that recorded tool output") {
		t.Errorf("the line about what the rule took away is wrong:\n%s", out)
	}

	// And a hidden session that does hold a failure is still counted: the
	// sentence has to keep working where it is true.
	loud := `{"type":"user","timestamp":"2026-07-10T11:00:00Z","sessionId":"cccc0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"a2","content":"connection refused on port 5432"}]}}`
	if err = os.WriteFile(filepath.Join(store, "loud.jsonl"), []byte(loud+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	note, err = captureRunStderr(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "hides 1 matching session") {
		t.Errorf("the session with a failure in it was not counted:\n%s", note)
	}
	out, err = captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "withheld the 2 sessions that recorded tool output") {
		t.Errorf("both sessions recorded tool output; the line says otherwise:\n%s", out)
	}
}

// The withheld count is a sentence about the trust policy — how much of their
// own history a rule keeps from the reader — and friction counted records where
// the sentence says sessions, so one hidden session with ten error lines was
// reported as ten (#1639).
func TestFrictionCountsWithheldSessionsNotRecords(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, 10)
	for i := range 10 {
		lines = append(lines, `{"type":"user","timestamp":"2026-07-10T10:0`+string(rune('0'+i))+`:00Z","sessionId":"bbbb0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"a`+string(rune('0'+i))+`","content":"connection refused on port 5432"}]}}`)
	}
	if err := os.WriteFile(filepath.Join(store, "bbbb0001.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"*":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	note, err := captureRunStderr(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "hides 1 matching session") {
		t.Errorf("the store holds one session; the note says otherwise:\n%s", note)
	}
	if strings.Contains(note, "hides 10") {
		t.Errorf("record count reported as sessions:\n%s", note)
	}
}

// storeWithSessions indexes a handful of ordinary local sessions.
func storeWithSessions(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the beta rollout"}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// `--from` is the one filter activeFilters did not know about, so a --from that
// matched nothing sent the reader to rebuild a store that was fine — the
// misread #637 and #1007 closed for the others (#2642).
func TestAFromThatMatchesNothingNamesTheFilter(t *testing.T) {
	storeWithSessions(t)
	out, err := captureRunStderr(t, "last", "--from", "nosuchhost")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no sessions indexed yet") {
		t.Fatalf("a filter that matched nothing was reported as an empty store:\n%s", out)
	}
	if !strings.Contains(out, `from "nosuchhost"`) {
		t.Fatalf("the filter that emptied the listing is not named:\n%s", out)
	}
}

// search does not take --from at all — it is a flag of `deja last`, and the
// dispatcher says so rather than searching for the text. So the listing is the
// only screen where this filter can empty an answer.
func TestSearchRefusesFromAndSaysWhereItBelongs(t *testing.T) {
	storeWithSessions(t)
	_, err := captureRun(t, "--from", "nosuchhost", "beta rollout")
	if err == nil || !strings.Contains(err.Error(), "`deja last`") {
		t.Fatalf("search should send --from to the command that has it, got %v", err)
	}
}

// A listing with no filter at all keeps the advice it had.
func TestAnEmptyStoreStillSaysToIndex(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRunStderr(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no sessions indexed yet") {
		t.Fatalf("an empty store should still say what to do:\n%s", out)
	}
}

// Gemini can install this repository as an extension directly, and `deja
// install gemini` writes an extension of its own. Both are the same product, so
// both must claim the same name: Gemini keys an extension by the name in its
// manifest and refuses a second install under a name it already has
// ("Extension "deja" is already installed"). Let the two names drift and a
// machine ends up with two deja extensions, two MCP servers and every tool
// listed twice — the fault Zed had until one shared server id made it
// impossible.
func TestGeminiExtensionSharesTheInstallerName(t *testing.T) {
	var manifest struct {
		Name        string                    `json:"name"`
		Version     string                    `json:"version"`
		Description string                    `json:"description"`
		MCPServers  map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(repoFile(t, "gemini-extension.json"), &manifest); err != nil {
		t.Fatalf("gemini-extension.json: %v", err)
	}
	if manifest.Name != geminiExtensionName {
		t.Fatalf("manifest name is %q, the installer writes %q — a machine with both gets two deja extensions", manifest.Name, geminiExtensionName)
	}
	if manifest.Version == "" || manifest.Description == "" {
		t.Fatal("the gallery reads version and description; an empty one is a listing that says nothing")
	}
	// The gallery crawler wants the manifest at the absolute root, which is why
	// this file sits beside go.mod rather than under extensions/.
	server, ok := manifest.MCPServers["deja"]
	if !ok {
		t.Fatal("the extension carries no deja MCP server, which is the only thing it is for")
	}
	if server["command"] != "deja" {
		t.Fatalf("MCP command is %v; it has to be the binary on PATH, since an extension ships files rather than binaries", server["command"])
	}
}

func TestQwenExtensionManifest(t *testing.T) {
	var manifest struct {
		Name            string                    `json:"name"`
		Version         string                    `json:"version"`
		Description     string                    `json:"description"`
		ContextFileName string                    `json:"contextFileName"`
		MCPServers      map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(repoFile(t, "qwen-extension.json"), &manifest); err != nil {
		t.Fatalf("qwen-extension.json: %v", err)
	}
	if manifest.Name != geminiExtensionName {
		t.Fatalf("manifest name = %q, want %q", manifest.Name, geminiExtensionName)
	}
	if manifest.Version == "" || manifest.Description == "" {
		t.Fatal("qwen-extension.json must declare non-empty version and description")
	}
	if manifest.ContextFileName != "QWEN.md" {
		t.Fatalf("contextFileName = %q, want QWEN.md", manifest.ContextFileName)
	}
	if len(repoFile(t, manifest.ContextFileName)) == 0 {
		t.Fatalf("%s is empty", manifest.ContextFileName)
	}
	server, ok := manifest.MCPServers["deja"]
	if !ok {
		t.Fatal("qwen-extension.json declares no deja MCP server")
	}
	if server["command"] != "deja" {
		t.Fatalf("MCP command = %v, want deja", server["command"])
	}
}

// Every plugin deja writes builds a search out of what the user typed, and a
// query that names one of deja's own flags — `--json`, `--all-matches` — dies
// in flag parsing. The caller turns the failed call into "", so the reader is
// told their history holds nothing, which is worse than an error.
//
// Measured against a real store: of eight dash-leading queries, six failed
// without `--` and all eight answered with it.
func TestGeneratedPluginsEndTheFlagsBeforeTheQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		js   string
		// how the plugin spells the guarded call, whitespace-free
		want string
	}{
		{"cline", clinePluginJS("/bin/deja"), `["search","--",query]`},
		{"pi", piExtensionTS("/bin/deja"), `["search","--",query]`},
		{"deepseek", dshCommandJS("/bin/deja"), `["search","--",query]`},
		{"hermes", hermesPluginPy("/bin/deja"), `["search","--",query]`},
	} {
		compact := strings.Join(strings.Fields(tc.js), "")
		if !strings.Contains(compact, tc.want) {
			t.Errorf("%s never sends the terminator", tc.name)
		}
		// And still sends the plain form, or every ordinary query would carry
		// a terminator a deja that predates it cannot read.
		if !strings.Contains(compact, `["search",query]`) {
			t.Errorf("%s always sends the terminator", tc.name)
		}
		// The guard has to be on the query's own shape.
		if !strings.Contains(compact, `query.startsWith("-")`) &&
			!strings.Contains(compact, `query.startswith("-")`) {
			t.Errorf("%s decides without looking at the query", tc.name)
		}
	}
}

// partialClone makes a blob-less clone of a two-commit repository whose second
// commit renames and edits a file, so rename detection needs the old blob the
// clone does not have. The origin is a file:// URL: a lazy fetch from it
// writes a new pack, which is what the tests count, and nothing leaves the
// machine.
func partialClone(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	src := filepath.Join(base, "src")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+base,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	git(src, "init", "-q")
	var body strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&body, "line %d of a file long enough for inexact rename detection\n", i)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	git(src, "add", ".")
	git(src, "commit", "-q", "-m", "one")
	git(src, "mv", "a.txt", "b.txt")
	if err := os.WriteFile(filepath.Join(src, "b.txt"), []byte(body.String()+"extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(src, "add", ".")
	git(src, "commit", "-q", "-m", "two")
	git(src, "config", "uploadpack.allowFilter", "true")
	clone := filepath.Join(base, "clone")
	u := filepath.ToSlash(src)
	if !strings.HasPrefix(u, "/") {
		u = "/" + u // C:/… on Windows
	}
	git(base, "clone", "-q", "--filter=blob:none", "file://"+u, clone)
	return clone
}

func packCount(t *testing.T, clone string) int {
	t.Helper()
	packs, err := filepath.Glob(filepath.Join(clone, ".git", "objects", "pack", "*.pack"))
	if err != nil {
		t.Fatal(err)
	}
	return len(packs)
}

// #5053: the session-start digest's `git log --name-only` fetched the missing
// blobs from origin in a partial clone. The hook must answer from what is on
// disk and still name the files.
func TestChangedTaskFilesDoesNotLazyFetchInPartialClone(t *testing.T) {
	old := taskGitBudget
	taskGitBudget = 20 * time.Second
	t.Cleanup(func() { taskGitBudget = old })

	clone := partialClone(t)
	before := packCount(t, clone)
	got := changedTaskFiles(clone)
	if after := packCount(t, clone); after != before {
		t.Fatalf("changedTaskFiles fetched from the promisor remote: %d packs -> %d", before, after)
	}
	if !strings.Contains(strings.Join(got, ","), "b.txt") {
		t.Fatalf("changedTaskFiles = %v, want b.txt from the last commit", got)
	}
}

// `deja blame file:line` runs blame and show, which read blobs too.
func TestBlameGitDoesNotLazyFetchInPartialClone(t *testing.T) {
	clone := partialClone(t)
	before := packCount(t, clone)
	gitLineCommit(filepath.Join(clone, "b.txt"), 3)
	_, _ = gitRun(clone, "show", "--unified=0", "--format=", "HEAD")
	if after := packCount(t, clone); after != before {
		t.Fatalf("blame fetched from the promisor remote: %d packs -> %d", before, after)
	}
}

// Every git call deja makes goes through gitread, so the no-fetch and no-lock
// environment cannot be forgotten at a new call site. docsdate is left out: it
// runs from the site generators, never from deja.
func TestGitCallsGoThroughGitread(t *testing.T) {
	direct := regexp.MustCompile(`exec\.Command(Context)?\((ctx, )?"git"`)
	var bad []string
	for _, root := range []string{".", "../../internal"} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				switch filepath.Base(path) {
				case "gitread", "docsdate", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path) //nolint:gosec // the test walks the repo checkout
			if err != nil {
				return err
			}
			for i, ln := range strings.Split(string(b), "\n") {
				if direct.MatchString(ln) {
					bad = append(bad, fmt.Sprintf("%s:%d", path, i+1))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("git run without gitread.Command:\n%s", strings.Join(bad, "\n"))
	}
}

// A session that ended in a dead end used to arrive looking exactly like one
// that ended in an answer: the state that says otherwise is set by hand, and on
// a real store of 1160 sessions nothing carried it. The transcript says it in
// words, so the hit says it too — end to end, through indexing and search.
func TestSearchSaysWhenASessionBackedSomethingOut(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "dead-end.jsonl"), "dead-end", []string{
		`{"type":"user","sessionId":"dead-end","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"cap the webhook retries at three"}}`,
		`{"type":"assistant","sessionId":"dead-end","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":"capped them, then reverted the cap because the queue backed up"}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "held.jsonl"), "held", []string{
		`{"type":"user","sessionId":"held","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"what happened with the webhook queue"}}`,
		`{"type":"assistant","sessionId":"held","timestamp":"2026-01-03T03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":"the webhook queue drains fine since the batch size went up"}]}}`,
	})
	out, err := captureRun(t, "webhook")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mentions backing an approach out") {
		t.Errorf("the session that reverted its own change is served without a word about it:\n%s", out)
	}
	// And the session that reached an answer is not labelled with someone
	// else's dead end.
	if strings.Count(out, "mentions backing an approach out") != 1 {
		t.Errorf("the marker landed on more than the session that earned it:\n%s", out)
	}
}

const grokLineageCwd = "/work/billing"

// grokSession writes one Grok Build 1.0.41 session under the sandbox's
// ~/.grok: summary.json, updates.jsonl with a user turn and a reply, and
// chat_history.jsonl when chat is given. extra lines go on the end of
// updates.jsonl.
func grokSession(t *testing.T, id, summaryExtra, text, chat string, extra ...string) string {
	t.Helper()
	const cwd = grokLineageCwd
	dir := filepath.Join(sources.GrokRoot(), "sessions", url.PathEscape(cwd), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	summary := `{"info":{"id":"` + id + `","cwd":"` + cwd + `"},"created_at":"` + now.Add(-time.Minute).Format(time.RFC3339Nano) +
		`","updated_at":"` + now.Format(time.RFC3339Nano) + `"` + summaryExtra + `}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := now.Add(-30 * time.Second).Unix()
	lines := `{"timestamp":` + strconv.FormatInt(ts, 10) + `,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"` + text + `"}}}}` + "\n" +
		`{"timestamp":` + strconv.FormatInt(ts+1, 10) + `,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"noted: ` + text + `"}}}}` + "\n"
	var linesSb36 strings.Builder
	for _, l := range extra {
		linesSb36.WriteString(l + "\n")
	}
	lines += linesSb36.String()
	updates := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(updates, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if chat != "" {
		if err := os.WriteFile(filepath.Join(dir, "chat_history.jsonl"), []byte(chat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return updates
}

// Measured on Grok Build 1.0.41 through a stub model: the start digest goes out
// in a PreToolUse reminder that chat_history.jsonl keeps, so each `grok -r`
// sent it once more (three in one request after three resumes); a
// `--fork-session` copy got a third digest led by the session it was forked
// from; and a subagent's own session came back to its parent as prior work,
// because its summary names no parent — only the parent's subagent_spawned
// record does.
func TestGrokSessionIsNotRecalledToItsOwnLineage(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_GROK_ROOT", t.TempDir())
	dir := index.DefaultDir()
	// What Grok writes: the reminder as one JSON string, newlines escaped.
	chatLine := func(text string) string {
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(map[string]any{"type": "user", "content": []any{map[string]any{"type": "text", "text": text}}}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	digestChat := chatLine("<system-reminder>\nContext from PreToolUse hook 'global/deja:pre_tool_use[0].hooks[0]':\n" + frameRecall(sessionStartLead+"- a session") + "\n</system-reminder>")
	wideChat := chatLine("<system-reminder>\n" + frameRecall(wideRecallLead+"- a session") + "\n</system-reminder>")
	plainChat := chatLine("<user_query>hello</user_query>")
	// A tool's output that quotes the digest, as reading deja's own source
	// does, is encoded once more inside the tool result.
	quoted := mustMarshal(t, frameRecall(sessionStartLead))
	quotedChat := chatLine("<user_query>read it</user_query>") + `{"type":"tool_result","content":` + strconv.Quote(string(quoted)) + "}\n"

	parent := grokSession(t, "01a1-parent", `,"session_kind":"headless"`, "invoice rounding is half-even zorblat", digestChat,
		`{"timestamp":1,"method":"_x.ai/session/update","params":{"sessionId":"01a1-parent","update":{"sessionUpdate":"subagent_spawned","subagent_id":"01a1-child","parent_session_id":"01a1-parent","child_session_id":"01a1-child"}}}`)
	grokSession(t, "01a1-child", `,"session_kind":"subagent","agent_name":"general-purpose"`, "probe the zorblat rounding", "")
	other := grokSession(t, "01a1-other", `,"session_kind":"headless"`, "an unrelated zorblat note", plainChat)
	// A fork of the parent, sorted first, carries a copy of its spawn record.
	grokSession(t, "00a0-fork", `,"session_kind":"headless","parent_session_id":"01a1-parent","forked_at":"2026-10-17T23:40:25Z"`,
		"invoice rounding is half-even zorblat", digestChat,
		`{"timestamp":1,"method":"_x.ai/session/update","params":{"sessionId":"00a0-fork","update":{"sessionUpdate":"subagent_spawned","subagent_id":"01a1-child","parent_session_id":"01a1-parent","child_session_id":"01a1-child"}}}`)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The parent asking: its own subagent is its own work.
	if got := askerLineage(dir, "01a1-parent", parent); !got["01a1-child"] {
		t.Errorf("parent's lineage leaves its subagent in its recall: %v", got)
	}
	// The subagent asking, by id only as its prompt hook and MCP call do.
	if got := askerLineage(dir, "01a1-child", ""); !got["01a1-parent"] {
		t.Errorf("subagent's lineage leaves its parent in its recall: %v", got)
	}
	if got := askerLineage(dir, "01a1-other", other); got["01a1-parent"] || got["01a1-child"] {
		t.Errorf("an unrelated session's lineage took in another family: %v", got)
	}

	// A fork made after the build, which is when its first hooks fire.
	fork := grokSession(t, "01a1-fork", `,"session_kind":"headless","parent_session_id":"01a1-parent","forked_at":"2026-10-17T23:40:25Z"`,
		"invoice rounding is half-even zorblat", digestChat)
	if got := askerLineage(dir, "01a1-fork", fork); !got["01a1-parent"] {
		t.Errorf("fork's lineage leaves its source in its recall: %v", got)
	}
	if got := askerLineage(dir, "01a1-fork", ""); !got["01a1-parent"] {
		t.Errorf("fork's lineage by id leaves its source in its recall: %v", got)
	}

	// The digest the thread already holds is not sent again, on resume or in
	// a fork's copy; a thread that never got one still does.
	if !threadHasDigest(dir, "load", "01a1-parent", parent) {
		t.Error("a resumed grok session that holds its digest gets another")
	}
	if !threadHasDigest(dir, "load", "01a1-fork", fork) {
		t.Error("a grok fork that copied its source's digest gets another")
	}
	if threadHasDigest(dir, "load", "01a1-other", other) {
		t.Error("a resumed grok session that never got a digest is denied one")
	}
	wide := grokSession(t, "01a1-wide", `,"session_kind":"headless"`, "wide recall", wideChat)
	if !threadHasDigest(dir, "load", "01a1-wide", wide) {
		t.Error("a resumed grok session that holds a wide-recall digest gets another")
	}
	quote := grokSession(t, "01a1-quote", `,"session_kind":"headless"`, "reading deja", quotedChat)
	if threadHasDigest(dir, "load", "01a1-quote", quote) {
		t.Error("a tool output quoting the digest counted as the digest")
	}
}

// Grok installs the plugin from a marketplace entry that pins a commit, so an
// installed copy stays where the pin left it and nothing said so: the bundle
// went to 0.2.0 while every installed copy was still 0.1.0 (#1828).

// writeGrokPlugin puts a plugin in the place Grok installs one: a generated
// directory name under installed-plugins, with the manifest inside it.
func writeGrokPlugin(t *testing.T, dirName, name, version string) {
	t.Helper()
	dir := filepath.Join(sources.GrokHome(), "installed-plugins", dirName, ".grok-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]string{"name": name, "version": version})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The number doctor compares against has to be the number the plugin ships, or
// the report is a guess. This is the reason TestKimiManifestsAgree exists: a
// constant nobody checks drifts silently.
func TestGrokPluginVersionMatchesTheManifest(t *testing.T) {
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(repoFile(t, "extensions/grok/.grok-plugin/plugin.json"), &manifest); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if manifest.Version != grokPluginVersion {
		t.Fatalf("grokPluginVersion is %q, the manifest says %q", grokPluginVersion, manifest.Version)
	}
	// The root is found by this name, so the manifest has to carry it.
	if manifest.Name != "deja" {
		t.Fatalf("the plugin manifest names %q, and the installed copy is found by name", manifest.Name)
	}
}

// `grok plugin marketplace add vshulcz/deja-vu` reads .grok-plugin/marketplace.json
// at the repo root; `grok plugin install deja` then installs the entry by name.
func TestGrokMarketplacePointsAtThePlugin(t *testing.T) {
	var market struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(repoFile(t, ".grok-plugin/marketplace.json"), &market); err != nil {
		t.Fatalf("marketplace.json: %v", err)
	}
	if len(market.Plugins) != 1 {
		t.Fatalf("want exactly one plugin, got %d", len(market.Plugins))
	}
	p := market.Plugins[0]
	if p.Source.Type != "local" || p.Source.Path != "./extensions/grok" {
		t.Fatalf("source = %+v, want local ./extensions/grok", p.Source)
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(repoFile(t, "extensions/grok/.grok-plugin/plugin.json"), &manifest); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if p.Name != manifest.Name {
		t.Fatalf("marketplace entry %q, plugin manifest %q: `grok plugin install deja` finds it by name", p.Name, manifest.Name)
	}
}

func TestGrokPluginNoteNamesBothVersionsWhenBehind(t *testing.T) {
	hermeticEnv(t)
	writeGrokPlugin(t, "deja-9f3c1a2", "deja", "0.1.0")

	if !grokPluginInstalled() {
		t.Fatal("a plugin under a generated directory name was not found")
	}
	note := grokPluginNote()
	if !strings.Contains(note, "v0.1.0 installed") || !strings.Contains(note, "v"+grokPluginVersion+" ships") {
		t.Fatalf("note = %q, want both versions named", note)
	}
	// The command Grok actually has: `grok plugin update [<name>]`, which is
	// also what its own plugin guide points at. Kimi has no plugin command, so
	// its note still says to reinstall.
	if !strings.Contains(note, "grok plugin update deja") {
		t.Fatalf("note = %q, want the command that updates it", note)
	}
}

// A working copy installed from a local path may legitimately be ahead, and
// deja cannot tell it from a marketplace copy by the files it can see. Telling
// someone their newer plugin is stale would be wrong; reporting the number is
// not.
func TestGrokPluginNoteClaimsNothingAboutACurrentOrNewerCopy(t *testing.T) {
	for _, version := range []string{grokPluginVersion, "9.9.9"} {
		t.Run(version, func(t *testing.T) {
			hermeticEnv(t)
			writeGrokPlugin(t, "deja-9f3c1a2", "deja", version)
			if note := grokPluginNote(); note != "v"+version {
				t.Fatalf("note = %q, want the bare version", note)
			}
		})
	}
}

// Most machines have no Grok at all, and doctor runs on them.
func TestGrokPluginAbsentIsSilentNotAnError(t *testing.T) {
	hermeticEnv(t)
	if grokPluginInstalled() {
		t.Fatal("no plugin is installed, and one was reported")
	}
	if note := grokPluginNote(); note != "" {
		t.Fatalf("note = %q, want silence", note)
	}
	// Installed-plugins exists but holds somebody else's plugin.
	writeGrokPlugin(t, "other-1a2b3c4", "not-deja", "5.0.0")
	if grokPluginInstalled() {
		t.Fatal("another plugin's directory was read as deja's")
	}
	if note := grokPluginNote(); note != "" {
		t.Fatalf("note = %q, want silence", note)
	}
}

// A manifest deja cannot read is not a plugin it can report on, and it must not
// be a crash either.
func TestGrokPluginSurvivesAnUnreadableManifest(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(sources.GrokHome(), "installed-plugins", "deja-9f3c1a2", ".grok-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if grokPluginInstalled() || grokPluginNote() != "" {
		t.Fatal("a manifest that does not parse was reported as an installed plugin")
	}
}

// The report itself: the row says "plugin" rather than "missing", and carries
// the version note when the copy is behind.
func TestDoctorReportsAStaleGrokPlugin(t *testing.T) {
	hermeticEnv(t)
	writeGrokPlugin(t, "deja-9f3c1a2", "deja", "0.1.0")

	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	var row string
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "grok") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatal("no grok row in the auto-recall report")
	}
	if !strings.Contains(row, "plugin") {
		t.Fatalf("grok row = %q, want it to say the plugin carries recall", row)
	}
	if !strings.Contains(row, "v0.1.0 installed") {
		t.Fatalf("grok row = %q, want the stale-version note", row)
	}
}

// A current plugin is not news; what it is doing there is, because nothing else
// on screen says it.
func TestDoctorSaysWhatACurrentGrokPluginIsDoing(t *testing.T) {
	hermeticEnv(t)
	writeGrokPlugin(t, "deja-9f3c1a2", "deja", grokPluginVersion)

	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	var row string
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "grok") {
			row = l
			break
		}
	}
	if !strings.Contains(row, "plugin") || !strings.Contains(row, "recalls on every prompt") {
		t.Fatalf("grok row = %q, want the plugin row and its sentence", row)
	}
}

// Every guide page carries the same sidebar, and the per-agent list inside it
// is written into each page rather than shared at runtime. memory-for-hermes
// was four guides and one count behind the rest — a reader who arrived there
// was told deja has no guide for Roo, Continue, prime-agent or Crush.
//
// TestGuidePagesShareTheirNavigation compares the pages' outer navigation and
// passed throughout, because the per-agent list sits inside a <details> block
// it does not look into.
func TestEveryGuidePageListsEveryPerAgentGuide(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "guide")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var guides, pages []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".html" {
			continue
		}
		pages = append(pages, e.Name())
		if strings.HasPrefix(e.Name(), "memory-for-") {
			guides = append(guides, e.Name())
		}
	}
	if len(guides) == 0 {
		t.Fatal("no per-agent guides on disk, so this checks nothing")
	}

	block := regexp.MustCompile(`(?s)<details class="grpfold"[^>]*><summary>Per-agent guides .*?</details>`)
	count := regexp.MustCompile(`Per-agent guides <span class="n">(\d+)</span>`)
	for _, name := range pages {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		nav := block.Find(b)
		if nav == nil {
			continue // a page without the fold is not claiming a list
		}
		for _, g := range guides {
			if !strings.Contains(string(nav), `href="`+g+`"`) {
				t.Errorf("docs/guide/%s does not link %s in its per-agent list", name, g)
			}
		}
		m := count.FindSubmatch(nav)
		if m == nil {
			t.Errorf("docs/guide/%s names no count for the per-agent guides", name)
			continue
		}
		if n, _ := strconv.Atoi(string(m[1])); n != len(guides) {
			t.Errorf("docs/guide/%s says %d per-agent guides; there are %d", name, n, len(guides))
		}
	}
}

// A per-agent page that offers "the other N agents" is counting the registry
// minus itself, in the description a search engine shows. The Hermes page said
// twenty-two for months, and it is repeated five times on that page — the
// meta description, og, twitter, the structured data and the lede.
func TestAPerAgentPageCountsTheOtherAgentsCorrectly(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "docs", "guide")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	otherWord := countWord(t, registryHarnessCount(t, root)-1)
	want := "the other " + otherWord
	// Only a number: "the other agents" and "the other half" are prose.
	stale := regexp.MustCompile(`the other (twenty[a-z-]*|thirty[a-z-]*|forty[a-z-]*|fifty[a-z-]*|\d+)`)
	otherAgents := regexp.MustCompile(`(?i)\b(twenty[a-z-]*|thirty[a-z-]*|forty[a-z-]*|fifty[a-z-]*|\d+) other (?:coding )?agents\b`)
	checked := 0
	for _, e := range entries {
		if e.IsDir() || (!strings.HasPrefix(e.Name(), "memory-for-") && !strings.HasPrefix(e.Name(), "delete-")) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range stale.FindAllString(string(b), -1) {
			checked++
			if m != want {
				t.Errorf("docs/guide/%s says %q; the registry has %d harnesses", e.Name(), m, registryHarnessCount(t, root))
			}
		}
		for _, m := range otherAgents.FindAllStringSubmatch(string(b), -1) {
			checked++
			if !strings.EqualFold(m[1], otherWord) {
				t.Errorf("docs/guide/%s says %q; the registry has %d harnesses", e.Name(), m[0], registryHarnessCount(t, root))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no page counts the other agents; the regex stopped matching")
	}
}

// The sidebar is written by scripts/gennav. A page edited by hand, or added
// without rerunning it, drifts from the rest; before the generator there were
// three versions across 68 pages. Apart from which link is current and which
// group is open, every page must carry the same one.
func TestEveryGuidePageCarriesTheGeneratedSidebar(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "guide")
	pages, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no guide pages: %v", err)
	}
	aside := regexp.MustCompile(`(?s)<aside>.*?</aside>`)
	norm := strings.NewReplacer(` aria-current="page"`, "", `<details class="grpfold" open>`, `<details class="grpfold">`)
	var first, firstName string
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		side := aside.Find(b)
		if side == nil {
			t.Errorf("%s has no sidebar", filepath.Base(p))
			continue
		}
		s := string(side)
		if n := strings.Count(s, "<details class=\"grpfold\" open>"); n > 1 {
			t.Errorf("%s opens %d sidebar groups, want at most one", filepath.Base(p), n)
		}
		s = norm.Replace(s)
		if first == "" {
			first, firstName = s, filepath.Base(p)
			continue
		}
		if s != first {
			t.Errorf("%s has a different sidebar from %s; run go run ./scripts/gennav", filepath.Base(p), firstName)
		}
	}
}

// A per-agent guide says how to wire that harness; the registry entry says what
// its transcript looks like and when it was last checked against the live
// client. Thirty-one of the thirty-four linked theirs and three did not — the
// three oldest, written before the convention — so a reader of those pages had
// no way to the format page at all.
func TestEveryPerAgentGuideLinksItsRegistryEntry(t *testing.T) {
	guides := filepath.Join("..", "..", "docs", "guide")
	entries := filepath.Join("..", "..", "docs", "registry")
	pages, err := filepath.Glob(filepath.Join(guides, "memory-for-*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 30 {
		t.Fatalf("found %d per-agent guides — the glob is wrong", len(pages))
	}
	link := regexp.MustCompile(`href="\.\./registry/([a-z0-9-]+)\.html"`)
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var linked []string
		for _, m := range link.FindAllStringSubmatch(string(b), -1) {
			if m[1] != "README" {
				linked = append(linked, m[1])
			}
		}
		if len(linked) == 0 {
			t.Errorf("%s links no registry entry — a reader there cannot reach the format page",
				filepath.Base(p))
			continue
		}
		// And it has to be an entry that exists: the filenames are display
		// slugs rather than harness ids (`claude-code.html`, `deepseek.html`),
		// so a link written from the id would 404.
		for _, id := range linked {
			if _, err := os.Stat(filepath.Join(entries, id+".html")); err != nil {
				t.Errorf("%s links registry/%s.html, which is not there", filepath.Base(p), id)
			}
		}
	}
}

// The count is spelled by hand wherever deja introduces itself, and every one
// of those places was written when it was true: the first-run screen said
// eighteen, four extensions said twenty, a plugin listing said twenty-two,
// while the registry had twenty-five (#3385). brief.go counts the registry
// now; these files cannot, so they are checked against it.
//
// The rule is the one the sentences use: the names in the phrase plus the
// number after "and" is every harness deja reads.
func TestTheIntroductionsCountEveryHarness(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	// "and <count> more", with the agents it names in the words just before it.
	phrase := regexp.MustCompile(`and\s+([a-z-]+)\s+more`)
	names := regexp.MustCompile(`Claude Code|Codex|Cursor|opencode|Gemini|Qwen Code|Copilot|Zed`)
	files := []string{
		"extensions/zed/README.md",
		"extensions/kimi/README.md",
		"extensions/grok/README.md",
		"extensions/dsh/index.js",
		"extensions/opencode/index.js",
		"extensions/grok/.grok-plugin/plugin.json",
		".grok-plugin/marketplace.json",
		"extensions/kimi/kimi.plugin.json",
		// The manifests a plugin listing renders, which live at the repo root.
		"kimi.plugin.json",
		"plugin.json",
		"gemini-extension.json",
		"qwen-extension.json",
	}
	checked := 0
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		text := string(b)
		for _, loc := range phrase.FindAllStringSubmatchIndex(text, -1) {
			said := text[loc[2]:loc[3]]
			// Only the sentence the phrase sits in: a file that says "opencode"
			// in its own header is not naming it as one of the agents here.
			from := max(loc[0]-100, 0)
			// One agent named twice in the same sentence — a listing whose
			// own harness appears in its title and again in the list — is
			// still one agent, and counting it twice made the sentence look
			// one short.
			seen := map[string]bool{}
			for _, name := range names.FindAllString(text[from:loc[0]], -1) {
				seen[name] = true
			}
			named := len(seen)
			if named == 0 {
				continue
			}
			checked++
			if want := spelledCount(n - named); said != want {
				t.Errorf("%s names %d agents and says %q more; the registry has %d, so it is %q",
					f, named, said, n, want)
			}
		}
	}
	if checked < len(files) {
		t.Errorf("only %d of %d introductions were checked — the phrasing changed and this test stopped seeing them",
			checked, len(files))
	}
}

// And the one that is computed rather than typed.
func TestTheFirstRunScreenCountsTheRegistry(t *testing.T) {
	n := registryHarnessCount(t, filepath.Join("..", ".."))
	if harnessCount() != n {
		t.Errorf("the binary counts %d harnesses; docs/registry has %d", harnessCount(), n)
	}
	if got, want := spelledCount(harnessCount()-6), spelledCount(n-6); got != want {
		t.Errorf("the first-run screen would say %q, want %q", got, want)
	}
	if strings.Contains(spelledCount(harnessCount()-6), " ") {
		t.Errorf("the count is not one word: %q", spelledCount(harnessCount()-6))
	}
}

// The index run narrates the notes store as "notes", and filtering by the name
// it just printed was refused as a typo: `checkHarness` (#1113) rejects the
// name before retrieval's own alias (#1888) can accept it. Both gates now agree
// (#2191).
func TestFilteringByTheNameTheIndexRunPrintsForNotes(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	line := `{"ts":"` + now + `","text":"the kafka consumer rebalances when the heartbeat is late"}`
	if err := os.WriteFile(notes, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	said, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: this is the name the run prints, or the filter is being
	// asked to accept something nobody was shown.
	if !strings.Contains(said, "notes:") {
		t.Fatalf("the index run does not call the store notes, so this measures nothing:\n%s", said)
	}
	// And the note is searchable under the stored name, so a miss below is the
	// filter and not an empty index.
	if out, _ := captureRun(t, "search", "rebalances", "--harness", "deja"); !strings.Contains(out, "heartbeat is late") {
		t.Fatalf("the note is not searchable under its stored name:\n%s", out)
	}

	out, err := captureRun(t, "search", "rebalances", "--harness", "notes")
	if err != nil {
		t.Fatalf("search --harness notes failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "heartbeat is late") {
		t.Errorf("search --harness notes does not reach the notes the run just counted:\n%s", out)
	}
	stats, err := captureRun(t, "stats", "--harness", "notes")
	if err != nil {
		t.Fatalf("stats --harness notes failed: %v\n%s", err, stats)
	}
	// The one session in the index is the note, and stats compares the stored
	// name exactly, so counting it is the whole question.
	if !strings.Contains(stats, "1 session indexed") {
		t.Errorf("stats --harness notes does not count the note the run just indexed:\n%s", stats)
	}
	// An empty result names the flag the reader passed, not the name deja
	// stores it under — the same rule the --since echo follows.
	empty, _ := captureRunStderr(t, "search", "zzzznothing", "--harness", "notes")
	if strings.Contains(empty, `harness "deja"`) || !strings.Contains(empty, `harness "notes"`) {
		t.Errorf("the empty result names a filter the reader did not pass:\n%s", empty)
	}
	// A real typo is still a typo.
	if _, err := captureRun(t, "search", "rebalances", "--harness", "notez"); err == nil {
		t.Errorf("an unknown harness is accepted, so the check no longer tells a typo from a name")
	}
}

// A typo'd --harness used to be indistinguishable from a real harness with no
// sessions — both said "matched nothing under harness X" — so `--harness
// cluade` read as "you have no claude history" (#1113).
func TestCheckHarnessRejectsOnlyUnknownNames(t *testing.T) {
	empty := ""
	if err := checkHarness(&empty); err != nil {
		t.Errorf("an empty harness is the no-filter case, not an error: %v", err)
	}
	// Every registry name must pass, installed or not.
	for _, name := range sources.HarnessNames() {
		if err := checkHarness(&name); err != nil {
			t.Errorf("known harness %q rejected: %v", name, err)
		}
	}
	// The name the index run prints for the notes store, which the check now
	// rewrites to the one it is stored under (#2191).
	printed := "notes"
	if err := checkHarness(&printed); err != nil || printed != "deja" {
		t.Errorf("notes was not accepted as the printed name for deja: %v, left as %q", err, printed)
	}
	typo := "cluade"
	err := checkHarness(&typo)
	if err == nil {
		t.Fatal("a misspelled harness was accepted")
	}
	// The refusal has to hand back the real names, or it is a dead end.
	if !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "codex") {
		t.Errorf("refusal does not list the known harnesses: %v", err)
	}
}

// blame accepts --harness like search does, so it must validate it the same way
// — the check used to live only on search, last, stats and the MCP blame, so
// `deja blame file --harness cluade` read as "nobody touched this file" (#1113).
func TestBlameCLIRejectsUnknownHarness(t *testing.T) {
	err := runBlame(t.TempDir(), []string{"parser.go", "--harness", "cluade"})
	if err == nil || !strings.Contains(err.Error(), "not a harness") {
		t.Fatalf("blame did not name the typo'd harness: %v", err)
	}
}

// The validators exist, but the bug is a command that parses the flag and
// forgets to call them (blame did). Pin the wiring on every surface that takes
// these flags so a refactor cannot quietly drop the call again.
func TestFilterValidationWiredAcrossCommands(t *testing.T) {
	dir := t.TempDir()
	rejects := func(err error) bool {
		return err != nil && (strings.Contains(err.Error(), "not a harness") || strings.Contains(err.Error(), "not a role"))
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"search --harness", func() error { return runSearch(dir, []string{"q", "--harness", "cluade"}, "") }},
		{"search --role", func() error { return runSearch(dir, []string{"q", "--role", "toool"}, "") }},
		{"last --harness", func() error { return cmdLast(dir, []string{"--harness", "cluade"}, "") }},
		{"last --role", func() error { return cmdLast(dir, []string{"--role", "toool"}, "") }},
		{"stats --harness", func() error { return runStats(dir, []string{"--harness", "cluade"}) }},
		{"stats --role", func() error { return runStats(dir, []string{"--role", "toool"}) }},
	}
	for _, c := range cases {
		if err := c.run(); !rejects(err) {
			t.Errorf("%s: expected a typo rejection, got %v", c.name, err)
		}
	}
}

// Every command had a usage line and eleven had nothing else: no description
// in `deja help`, and a --help that repeated the usage line (#4626).
func TestEveryListedCommandHasADescriptionAndAnExample(t *testing.T) {
	for _, e := range helpEntries {
		if strings.TrimSpace(e.desc) == "" {
			t.Errorf("%s has no description", e.name)
		}
		if len(e.examples) == 0 {
			t.Errorf("%s has no example", e.name)
		}
		for _, x := range e.examples {
			if !strings.Contains(x, "deja") {
				t.Errorf("%s example %q does not run deja", e.name, x)
			}
		}
		if len(e.usage) == 0 || !strings.HasPrefix(e.usage[0], "deja "+e.name) {
			t.Errorf("%s usage does not start with its name: %v", e.name, e.usage)
		}
		h := helpForCommand(e.name)
		for _, want := range []string{"Usage:", "deja " + e.name, "Examples:", e.examples[0]} {
			if !strings.Contains(h, want) {
				t.Errorf("%s --help lacks %q:\n%s", e.name, want, h)
			}
		}
	}
}

// One description column, and no row of the page breaks at 80 columns even
// with nothing to fit it to: piped help is what a README or an agent quotes.
func TestTheHelpPageFitsEightyColumnsAsWritten(t *testing.T) {
	col := -1
	for line := range strings.SplitSeq(usageText(), "\n") {
		if n := len([]rune(line)); n > 80 {
			t.Errorf("a line is %d wide: %q", n, line)
		}
		if !strings.HasPrefix(line, "  deja ") || strings.Contains(line, `"`) {
			continue
		}
		rest := line[len("  deja "):]
		gap := strings.Index(rest, "  ")
		if gap < 0 {
			continue
		}
		at := len("  deja ") + gap + len(rest[gap:]) - len(strings.TrimLeft(rest[gap:], " "))
		if col < 0 {
			col = at
		} else if at != col {
			t.Errorf("description at column %d, the page uses %d: %q", at, col, line)
		}
	}
}

// The hook and agent-facing commands come last, after every command a person
// types.
func TestAgentFacingCommandsAreListedLast(t *testing.T) {
	page := usageText()
	agents := strings.Index(page, groupAgents+":")
	if agents < 0 {
		t.Fatal("no section for agent-facing commands")
	}
	for _, e := range helpEntries {
		if e.group == "" {
			continue
		}
		at := strings.Index(page, "  deja "+e.name+" ")
		if at < 0 {
			t.Errorf("%s is not listed", e.name)
			continue
		}
		hook := e.group == groupAgents
		if hook != (at > agents) {
			t.Errorf("%s is in the wrong section", e.name)
		}
	}
}

// `deja help <cmd>` answers what `deja <cmd> --help` does; a word that is not
// a command is an error that says where the list is.
func TestHelpTakesACommand(t *testing.T) {
	hermeticEnv(t)
	a, err := captureRun(t, "help", "blame")
	if err != nil {
		t.Fatal(err)
	}
	b, err := captureRun(t, "blame", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if a != b || !strings.Contains(a, "deja blame <path>") {
		t.Fatalf("help blame and blame --help differ:\n%s\n---\n%s", a, b)
	}
	if _, err := captureRun(t, "help", "stauts"); err == nil || !strings.Contains(err.Error(), "`deja help` lists them") {
		t.Fatalf("an unknown topic: %v", err)
	}
}

// The footers of tests, recap and secrets point at --limit 0, so their help says
// what it does and the default it raises (#5079).
func TestHelpSaysWhatLimitZeroDoes(t *testing.T) {
	hermeticEnv(t)
	for _, tc := range []struct {
		cmd  string
		want string
	}{
		{"tests", fmt.Sprintf("--limit shows %d tests by default; --limit 0 shows them all", testsDefaultLimit)},
		{"recap", fmt.Sprintf("--limit shows %d sessions by default; --limit 0 shows them all", recapDefaultSessions)},
		{"secrets", fmt.Sprintf("--limit shows %d sessions by default; --limit 0 shows them all", secretsDefaultLimit)},
	} {
		out, err := captureRun(t, "help", tc.cmd)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(strings.Fields(out), " "); !strings.Contains(got, tc.want) {
			t.Errorf("deja help %s lacks %q:\n%s", tc.cmd, tc.want, out)
		}
	}
}

// Piped, a bare deja printed the 106-line page. It points at it instead.
func TestPipedBareDejaPointsAtHelp(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "\n"); n > 6 {
		t.Errorf("bare deja into a pipe printed %d lines:\n%s", n, out)
	}
	for _, want := range []string{"deja <query>", "deja help", "deja brief"} {
		if !strings.Contains(out, want) {
			t.Errorf("the pointer lacks %q:\n%s", want, out)
		}
	}
}

// A name that is no target at all is the whole answer: nothing was done, so
// "finished what it could" describes a run that never started.
func TestInstallOfAnUnknownTargetSaysOnlyThat(t *testing.T) {
	hermeticEnv(t)
	err := runInstall(t.TempDir(), []string{"claud"}, false)
	if err == nil {
		t.Fatal("an unknown target was accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, `unknown target "claud" — did you mean "claude-code"?`) {
		t.Errorf("no near miss: %q", msg)
	}
	for _, not := range []string{"finished what it could", "fix what it reports"} {
		if strings.Contains(msg, not) {
			t.Errorf("the refusal says %q about a run that did nothing: %q", not, msg)
		}
	}
}

// An id that names nothing says where ids are listed.
func TestAMissingIDSaysWhereIDsAre(t *testing.T) {
	withTempStores(t)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"show", "share", "resume"} {
		_, err := captureRun(t, cmd, "zzzz9999")
		if err == nil || !strings.Contains(err.Error(), "`deja last` lists recent ones") {
			t.Errorf("%s with a bad id: %v", cmd, err)
		}
	}
}

// The hosts read the hook and status line bytes as they are. Off a terminal
// nothing is added.
func TestTheLineEndIsOnlyForATerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	if err := endLineOnTerminal(f, func(w io.Writer) error {
		_, err := io.WriteString(w, "<deja-recall>x</deja-recall>")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	b, _ := os.ReadFile(f.Name())
	if string(b) != "<deja-recall>x</deja-recall>" {
		t.Fatalf("a file got %q", b)
	}
	var lw lineEndWriter
	lw.w = &bytes.Buffer{}
	_, _ = lw.Write([]byte("a\n"))
	if lw.last != '\n' || lw.n != 2 {
		t.Fatalf("lineEndWriter tracked %q/%d", lw.last, lw.n)
	}
}

// The paste-only footer names the agents found on this machine, not all of
// them.
func TestTheHandoffFooterNamesOnlyAgentsFoundHere(t *testing.T) {
	bin := t.TempDir()
	name := "codex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	got := pasteOnlyFooter("")
	if !strings.Contains(got, "deja handoff --to codex --exec") {
		t.Errorf("codex is on PATH and not offered:\n%s", got)
	}
	if strings.Contains(got, "claude") || strings.Contains(got, "|") {
		t.Errorf("the footer names agents that are not here:\n%s", got)
	}
	t.Setenv("PATH", t.TempDir())
	if got := pasteOnlyFooter(""); !strings.Contains(got, "deja handoff --help") {
		t.Errorf("with no agent here: %q", got)
	}
}

// Run without their argument, how and restore printed a usage line of their
// own that had drifted from `deja help`, and files kept a third copy that
// only happened to match. Each now prints its help entry's first usage line
// (#5641).
func TestAMissingArgumentPrintsTheUsageLineHelpPrints(t *testing.T) {
	hermeticEnv(t)
	for _, name := range []string{"how", "restore", "files"} {
		t.Run(name, func(t *testing.T) {
			e, ok := helpEntryFor(name)
			if !ok {
				t.Fatalf("%s has no help entry", name)
			}
			_, err := captureRun(t, name)
			if err == nil {
				t.Fatalf("deja %s with no argument was accepted", name)
			}
			if !strings.HasSuffix(err.Error(), e.usage[0]) {
				t.Errorf("deja %s says\n  %s\nwhere deja help %s says\n  %s", name, err, name, e.usage[0])
			}
		})
	}
	// A name with no entry still gets a usage line rather than a panic.
	if err := usageError("no-such-command"); err.Error() != "usage: deja no-such-command" {
		t.Errorf("usageError for an unknown name = %q", err)
	}
}

// `search` was dispatched and undocumented, the same omission #749 found in the
// completions and from the same cause: it comes from the switch in run(), not
// from the commands map, so lists built from that map miss it (#753).
func TestHelpNamesEveryDispatchedCommand(t *testing.T) {
	// The wrappers whose bare form is a search rather than a command, and the
	// flag spellings.
	internal := map[string]bool{
		"aider": true, "goose": true,
		"--help": true, "-h": true, "--version": true, "-version": true,
	}
	var names []string
	for name := range commands {
		// helpHidden rather than a blanket `hook-` skip: the skip let help
		// document five hook commands and hide five, including hook-context,
		// with nothing to notice (#1654). A hook is now either documented or
		// named in helpHidden, where the reason lives.
		if helpHidden[name] || internal[name] {
			continue
		}
		names = append(names, name)
	}
	names = append(names, "search", "show", "last")

	help := captureHelp(t)
	line := regexp.MustCompile(`(?m)^\s+deja ([a-z][a-z0-9-]*)`)
	documented := map[string]bool{}
	for _, m := range line.FindAllStringSubmatch(help, -1) {
		documented[m[1]] = true
	}
	for _, name := range names {
		if !documented[name] {
			t.Errorf("deja help does not name %q", name)
		}
	}
	// A hidden name must still be a real command, or helpHidden is excusing a
	// ghost — the way a stale entry would quietly re-open the gap it was added
	// to close (#1654).
	for name := range helpHidden {
		if name == "help" {
			continue // answered before dispatch, so it is not in the map
		}
		if _, ok := commands[name]; !ok {
			t.Errorf("helpHidden excuses %q, which is not a command", name)
		}
	}
	// And the reverse: help must not teach a command that no longer exists.
	for name := range documented {
		if _, ok := commands[name]; ok {
			continue
		}
		switch name {
		case "search", "show", "last", "aider", "goose":
		default:
			t.Errorf("deja help names %q, which is not dispatched", name)
		}
	}
}

// helpReference is everything help can say: the page and every command's own
// --help. Flags live on the command pages now, so a check that a flag is
// documented reads both (#4626).
func helpReference() string {
	var b strings.Builder
	b.WriteString(usageText())
	for _, e := range helpEntries {
		b.WriteString(helpForCommand(e.name))
	}
	return b.String()
}

func captureHelp(t *testing.T) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	printUsage()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// helpLineFor is the usage line that starts with this command name. The check
// has to be on that line: `--since` appears in the search-flags section at the
// bottom of help, so a search anywhere in the text passes for every command.
func helpLineFor(help, command string) string {
	re := regexp.MustCompile(`(?m)^\s+deja ` + regexp.QuoteMeta(command) + `\b.*(?:\n\s{6,}\[.*)*`)
	return re.FindString(help)
}

// A flag the command takes and help never names is a flag nobody finds.
// `deja stats` accepted --project, --harness, --since and --role — the same
// four the search form documents — and its usage line listed none of them, so
// the only way to learn they existed was to read the parser (#753 is the same
// omission one level up, for commands).
func TestHelpNamesTheFiltersACommandAccepts(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "one.jsonl"), "flagcov", []string{
		`{"type":"user","sessionId":"flagcov","timestamp":"2026-08-20T10:00:00Z",` +
			`"message":{"role":"user","content":"the migration ran twice"}}`,
	})
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	// Read-only query surfaces only. A command that writes, installs or syncs
	// has no business being run by a coverage check.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"stats", nil},
		{"friction", nil},
		{"fix", []string{"pq: connection refused"}},
		{"how", []string{"go test"}},
		{"files", []string{"migration"}},
		{"last", []string{"3"}},
		{"blame", []string{"main.go"}},
		{"log", nil},
	} {
		line := helpLineFor(helpForCommand(c.name), c.name)
		if line == "" {
			t.Errorf("deja help has no usage line for %q", c.name)
			continue
		}
		for _, f := range []struct{ flag, value string }{
			{"--harness", "claude"},
			{"--project", "work"},
			{"--since", "30d"},
			{"--role", "user"},
			{"--limit", "5"},
		} {
			args := append(append([]string{c.name}, c.args...), f.flag, f.value)
			_, err := captureRun(t, args...)
			// Every refusal deja writes for a flag it does not take. A command
			// that took the flag either succeeded or failed for another reason.
			if err != nil {
				msg := err.Error()
				if strings.Contains(msg, "unknown flag") || strings.Contains(msg, "takes no") ||
					strings.Contains(msg, "and nothing else") {
					continue
				}
			}
			if !strings.Contains(line, f.flag) {
				t.Errorf("deja %s takes %s and its usage line never names it:\n%s", c.name, f.flag, line)
			}
		}
	}
}

// Four flags documented in usage did not work (#623, #625, #709, #717) and six
// working flags were undocumented (#757). Both directions are mechanical, so
// check them mechanically.
func TestHelpDocumentsEveryAcceptedFlag(t *testing.T) {
	help := helpReference()
	documented := map[string]bool{}
	for _, f := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(help, -1) {
		documented[f] = true
	}

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	accepted := map[string][]string{}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`case "(--[a-z][a-z-]*)"`),
		// Any variable name, not just a/args[i]: install compares `arg`, and
		// its --no-guidance/--no-index slipped past the narrow patterns (#1106).
		regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\[[^]]*\])? == "(--[a-z][a-z-]*)"`),
	}
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range patterns {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				accepted[m[1]] = append(accepted[m[1]], path)
			}
		}
	}
	for flag, where := range accepted {
		if !documented[flag] {
			t.Errorf("%s is accepted in %v but not in `deja help` or any command's --help", flag, where)
		}
	}
}

// A `...` document end with a comment after it is still the end: install put
// its mcp_servers and plugins blocks after `... # end`, outside the document,
// and Hermes refused the file (#4348).
func TestHermesInstallStopsAtACommentedDocumentEnd(t *testing.T) {
	for _, end := range []string{"... # end", "...  ", "...\t# end"} {
		t.Run(end, func(t *testing.T) {
			hermeticEnv(t)
			t.Setenv("DEJA_HERMES_HOME", "")
			path := filepath.Join(os.Getenv("HOME"), ".hermes", "config.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := "model: x\n" + end + "\n"
			if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if !strings.HasSuffix(string(b), "\n"+end+"\n") {
				t.Errorf("something was written after the document end:\n%s", b)
			}
			if !strings.Contains(string(b), "mcp_servers:") || !strings.Contains(string(b), "plugins:") {
				t.Errorf("install wrote neither block:\n%s", b)
			}
			if _, err := installTarget("hermes-auto", "/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(path); string(b) != cfg {
				t.Errorf("uninstall gave back %q, want %q", b, cfg)
			}
		})
	}
}

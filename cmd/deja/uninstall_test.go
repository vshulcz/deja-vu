package main

import (
	"bytes"
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Someone who uninstalls deja and then deletes the binary must not be left
// with agents shelling out to a path that is gone. `--all` used to expand to
// the MCP targets only, so every hook and plugin `--auto` had written stayed.
func TestUninstallAllRemovesHooksAndPlugins(t *testing.T) {
	home := hermeticEnv(t)
	home = filepath.Join(home, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	// Only harnesses with a config directory are detected, so create the ones
	// whose auto-install writes a separate file worth checking.
	for _, dir := range []string{
		".claude", ".codex", ".config/opencode/opencode", ".cursor",
		".qwen", ".kimi-code", ".pi/agent", ".cline/data",
	} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInstall(index.DefaultDir(), []string{"--auto"}, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	written := []string{
		filepath.Join(home, ".config", "opencode", "plugins", "deja.js"),
		filepath.Join(home, ".cline", "plugins", "deja", "index.js"),
		filepath.Join(home, ".pi", "agent", "extensions", "deja.ts"),
		filepath.Join(home, ".claude", "commands", "deja.md"),
	}
	for _, p := range written {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("install --auto did not write %s: %v", p, err)
		}
	}
	if err := runInstall(index.DefaultDir(), []string{"--all"}, true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	for _, p := range written {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("uninstall --all left %s behind", p)
		}
	}
	// Config files stay, but nothing in them may still call deja.
	for _, p := range []string{
		filepath.Join(home, ".kimi-code", "config.toml"),
		filepath.Join(home, ".qwen", "settings.json"),
		filepath.Join(home, ".cursor", "hooks.json"),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "hook-context") || strings.Contains(string(b), "hook-prompt") {
			t.Fatalf("%s still wires a deja hook after uninstall --all:\n%s", p, b)
		}
	}
}

// The expansion is the part that was wrong, so pin it directly: every target
// with an -auto sibling must gain it, and Claude's is not named after the
// harness the detector reports.
func TestWithAutoTargetsPairsEveryTarget(t *testing.T) {
	got := withAutoTargets([]string{"claude-code", "cline", "opencode", "grok"})
	want := map[string]bool{
		"claude-code": true, "claude-auto": true,
		"cline": true, "cline-auto": true,
		"opencode": true, "opencode-auto": true,
		"grok": true, "grok-auto": true,
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected target %q in %v", g, got)
		}
		delete(want, g)
	}
	if len(want) != 0 {
		t.Fatalf("missing targets %v (got %v)", want, got)
	}
	// grok does have one, and it writes hooks. It was missing from the name
	// list, so uninstall --all left ~/.grok/hooks/deja.json behind pointing at
	// a binary the user had just removed.
	sawGrokAuto := false
	for _, g := range got {
		if g == "grok-auto" {
			sawGrokAuto = true
		}
	}
	if !sawGrokAuto {
		t.Fatalf("grok did not gain its -auto sibling: %v", got)
	}
}

// An uninstall takes back the snapshot it made of a config that already had
// deja in it — "so the reader does not find the binary they just removed back
// in their config directory", as the comment on that path puts it. Ownership is
// read from the content, and the marker list knew every spelling except the two
// that do not write the word on its own: zed's context-server id and the dsh
// patch block (#2575).
func TestUninstallTakesBackItsOwnBackupForEveryHarness(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"zed", `{"context_servers":{"` + zedServerID + `":{"command":"/opt/bin/dj","args":["mcp"]}}}`},
		{"deepseek", dshBlockStart + "\n- insert:\n    - id: mcp-deja\n      config:\n        serverName: deja\n"},
		{"claude", `{"mcpServers":{"deja":{"command":"/opt/bin/dj","args":["mcp"]}}}`},
		{"a config of the reader's own", "theme: One Dark\nfont: 15\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".bak", []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			old := removingWiring
			removingWiring = true
			t.Cleanup(func() { removingWiring = old })
			if _, err := writeIfChanged(path, []byte(tc.content), []byte("{}\n")); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(path + ".bak")
			mine := !strings.Contains(tc.name, "reader's own")
			if mine && !os.IsNotExist(err) {
				t.Errorf("a snapshot of deja's own wiring survived the uninstall: %v", err)
			}
			if !mine && err != nil {
				t.Errorf("the reader's own backup was taken away: %v", err)
			}
		})
	}
}

// Uninstalling must not leave a copy of deja's own wiring behind. The snapshot
// is taken while the config is edited, and on the way out that config is the
// installed one — so the .bak held deja's hooks and a path to a binary the user
// had just removed, sitting in their config directory. Found by sweeping
// install-then-uninstall across every harness.
func TestUninstallTakesItsOwnBackupBackOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	installed := []byte(`{"hooks":{"UserPromptSubmit":[{"command":"/usr/local/bin/deja hook-prompt"}]}}`)
	if err := os.WriteFile(path, installed, 0o644); err != nil {
		t.Fatal(err)
	}

	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := writeIfChanged(path, installed, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		b, _ := os.ReadFile(path + ".bak")
		t.Fatalf("uninstall left a snapshot of deja's own wiring:\n%s", b)
	}
	live, err := os.ReadFile(path)
	if err != nil || string(live) != "{}" {
		t.Fatalf("the live config was not cleaned: %q %v", live, err)
	}
}

// A .bak the user made is theirs. deja only removes the one it created in the
// same call, so an existing snapshot survives untouched.
func TestUninstallKeepsABackupItDidNotMake(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("theirs = 1\n[hooks]\ncommand = \"/usr/local/bin/deja hook-prompt\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mine := "# my own snapshot from before any of this\n"
	if err := os.WriteFile(path+".bak", []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := writeIfChanged(path, []byte("old"), []byte("theirs = 1\n")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("the user's own backup was removed: %v", err)
	}
	if !strings.Contains(string(b), "my own snapshot") {
		t.Fatalf("the user's own backup was overwritten:\n%s", b)
	}
}

// Installing still leaves a snapshot: that is the point of taking one.
func TestInstallKeepsItsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	theirs := []byte(`{"theirs":true}`)
	if err := os.WriteFile(path, theirs, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeIfChanged(path, theirs, []byte(`{"theirs":true,"hooks":{}}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("install did not keep a snapshot of what it edited: %v", err)
	}
	if !bytes.Equal(b, theirs) {
		t.Fatalf("the snapshot is not what was there before:\n%s", b)
	}
}

// handWiredMachine is a machine where the harnesses were configured by the
// reader before deja arrived, so every config deja edits gets a snapshot.
func handWiredMachine(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for path, body := range map[string]string{
		".claude.json":          "{\"mine\":\"MINE\"}\n",
		".claude/settings.json": "{\"theme\":\"dark\"}\n",
		".codex/config.toml":    "[tools]\nweb = true\n",
		".cursor/mcp.json":      "{\"mcpServers\":{\"theirs\":{\"command\":\"x\"}}}\n",
	} {
		full := filepath.Join(home, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// An uninstall keeps the snapshot it took of a config the reader already had —
// #840 settled that neither the config nor its snapshot is deja's to delete.
// It said nothing about them, on a screen that otherwise names what it keeps:
// #2487 added gemini's line for exactly that reason, and nine files on disk are
// the same shape (#2676).
func TestUninstallNamesTheSnapshotsItLeaves(t *testing.T) {
	home := handWiredMachine(t)
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := captureRunStderr(t, "uninstall", "--all")
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	left := 0
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".bak") {
			left++
		}
		return nil
	})
	if left == 0 {
		t.Fatal("nothing was backed up, so this pins nothing")
	}
	if !strings.Contains(out, ".bak") {
		t.Fatalf("%d snapshots left on disk and the uninstall names none of them:\n%s", left, out)
	}
}

// A machine deja set up from nothing has no snapshots to name, and a line about
// none of them is noise.
func TestUninstallSaysNothingWhenItLeftNoSnapshots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, d := range []string{".claude", ".codex", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := captureRunStderr(t, "uninstall", "--all")
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if strings.Contains(out, ".bak") {
		t.Fatalf("nothing was snapshotted here, and the uninstall talked about snapshots:\n%s", out)
	}
}

// Uninstalling something that was never installed must do nothing at all. The
// uninstall path computes "the file without deja in it", and for a file that is
// not there that is an empty config — which deja then created, so `deja
// uninstall --all` on a machine that never had it wrote configs for harnesses
// the user does not even use. Fixed once for the targets that showed it; this
// holds every target to it.
func TestUninstallOnACleanHomeCreatesNothing(t *testing.T) {
	for _, target := range installTargetNames() {
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			removingWiring = true
			_, err := installTarget(target, "/usr/local/bin/deja", true)
			guidanceErr := func() error {
				_, gerr := guidanceResult(target, true)
				return gerr
			}()
			removingWiring = false
			if err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if guidanceErr != nil {
				t.Fatalf("guidance uninstall: %v", guidanceErr)
			}

			var made []string
			_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, werr error) error {
				if werr != nil || d.IsDir() {
					return nil //nolint:nilerr // an unreadable entry is not one uninstall made
				}
				// deja's own state is allowed: it records that an uninstall ran.
				if strings.Contains(path, filepath.Join(".config", "deja")) {
					return nil
				}
				rel, rerr := filepath.Rel(home, path)
				if rerr != nil {
					rel = path
				}
				made = append(made, rel)
				return nil
			})
			if len(made) > 0 {
				body, _ := os.ReadFile(filepath.Join(home, made[0]))
				t.Errorf("uninstalling on a clean home created %v; %s contains:\n%s", made, made[0], body)
			}
		})
	}
}

// A block whose end marker went missing cannot be bounded, so it is refused
// (#1705). On the way out that refusal has to hand the work back properly: name
// the file, and take out the blocks that *can* be taken out — grok's guidance
// lives in two files, and one broken block kept a well-formed one (#2210).
func TestUninstallNamesTheFileItRefusesAndClearsTheOther(t *testing.T) {
	hermeticEnv(t)
	home := sources.GrokHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	grokMD := filepath.Join(home, "GROK.md")
	agentsMD := filepath.Join(home, "AGENTS.md")
	if err := os.WriteFile(grokMD, []byte("my own notes about this box\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("grok", false); err != nil {
		t.Fatal(err)
	}
	// The premise: both files carry a block, so the removal below has two
	// things to do and not one.
	for _, p := range []string{grokMD, agentsMD} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), guidanceStart) || !strings.Contains(string(b), guidanceEnd) {
			t.Fatalf("%s does not carry a complete block, so this measures nothing:\n%s", p, b)
		}
	}

	// An editor eats one end marker.
	b, err := os.ReadFile(grokMD)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(b), guidanceEnd+"\n", "", 1) + "\nsomething the user wrote afterwards\n"
	if err = os.WriteFile(grokMD, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = installGuidance("grok", true)
	if err == nil {
		t.Fatal("the unbounded block was removed anyway")
	}
	// The refusal is the instruction to do it by hand, so it has to say where.
	if !strings.Contains(err.Error(), grokMD) {
		t.Errorf("the refusal names no file, so nobody knows what to edit: %v", err)
	}

	// And the file that was fine is clean: its block is independent, and on the
	// way out the reader wants as much gone as can go.
	after, err := os.ReadFile(agentsMD)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), guidanceStart) {
		t.Errorf("the twin still carries a complete deja block nobody mentioned:\n%s", after)
	}
	// The half-marked file is untouched, including the user's own text.
	still, err := os.ReadFile(grokMD)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(still), "my own notes about this box") ||
		!strings.Contains(string(still), "something the user wrote afterwards") {
		t.Errorf("the refused file lost the user's text:\n%s", still)
	}
}

// Both files unbounded: `grok-auto` runs `grok` first and fails on the same
// file twice, so naming only that one sends the reader to fix half of it and
// walk into the other half (#2210).
func TestUninstallNamesBothFilesWhenBothAreUnbounded(t *testing.T) {
	hermeticEnv(t)
	home := sources.GrokHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	grokMD := filepath.Join(home, "GROK.md")
	agentsMD := filepath.Join(home, "AGENTS.md")
	if err := os.WriteFile(grokMD, []byte("my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("grok", false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{grokMD, agentsMD} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		broken := strings.Replace(string(b), guidanceEnd+"\n", "", 1)
		if broken == string(b) {
			t.Fatalf("%s had no end marker to remove, so this measures nothing", p)
		}
		if err := os.WriteFile(p, []byte(broken+"\nthe user's own tail\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := installGuidance("grok", true)
	if err == nil {
		t.Fatal("two unbounded blocks were removed anyway")
	}
	for _, want := range []string{grokMD, agentsMD} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %s: %v", want, err)
		}
	}
}

// The prune on the way out says "when deja made it and nothing else is in it"
// and could only test the second half: os.Remove refuses a directory with
// anything in it, and nothing recorded who made it. A host's own empty folder
// — a VS Code `User` directory a reader created and had not filled yet — went
// with the uninstall (#3239).
func TestUninstallKeepsADirectoryTheReaderAlreadyHad(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	dir := filepath.Join(home, ".config", "deja-fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")

	if _, err := writeIfChanged(path, nil, []byte(`{"mcpServers":{"deja":{}}}`)); err != nil {
		t.Fatal(err)
	}
	recordWiring([]string{"claude-code"}, false)

	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := writeIfChanged(path, []byte(`{"mcpServers":{"deja":{}}}`), []byte(`{"mcpServers":{}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the config deja created outlived the uninstall")
	}
	if !isRealDir(dir) {
		t.Error("uninstall removed a directory the reader made")
	}
}

// And one deja had to make goes, which is the case the prune was written for.
func TestUninstallRemovesTheDirectoryItMade(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	dir := filepath.Join(home, ".config", "deja-fixture", "nested")
	path := filepath.Join(dir, "settings.json")

	if _, err := writeIfChanged(path, nil, []byte(`{"mcpServers":{"deja":{}}}`)); err != nil {
		t.Fatal(err)
	}
	if !isRealDir(dir) {
		t.Fatal("the install did not create the directory")
	}
	recordWiring([]string{"claude-code"}, false)

	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := writeIfChanged(path, []byte(`{"mcpServers":{"deja":{}}}`), []byte(`{"mcpServers":{}}`)); err != nil {
		t.Fatal(err)
	}
	if isRealDir(dir) {
		t.Error("the directory deja made was left behind")
	}
}

// One target that refuses to be written used to end the run: an uninstall
// stopped at the first locked path, left every other harness wired, and handed
// back a syscall for a file nobody chose (#902).
func TestUninstallFinishesTheTargetsItCan(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	home := hermeticEnv(t)
	_ = home
	if _, err := captureRun(t, "install", "claude-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "codex-auto"); err != nil {
		t.Fatal(err)
	}
	// install writes into the real codex home under HOME, not DEJA_CODEX_ROOT:
	// that variable relocates reads only.
	codexHooks := filepath.Join(os.Getenv("HOME"), ".codex", "hooks.json")
	before, err := os.ReadFile(codexHooks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "deja") {
		t.Fatalf("codex was not wired: %s", before)
	}

	guidance := filepath.Join(os.Getenv("HOME"), ".claude", "skills", "deja-history")
	if err = os.Chmod(guidance, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(guidance, 0o700) })

	_, err = captureRun(t, "uninstall", "--all")
	if err == nil {
		t.Fatal("a locked guidance directory was not reported")
	}
	got := err.Error()
	if !strings.Contains(got, "finished what it could") || !strings.Contains(got, "refused") {
		t.Errorf("error does not say what happened: %q", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Errorf("error does not name the cause: %q", got)
	}

	// The rest of the run still happened.
	after, err := os.ReadFile(codexHooks)
	if err == nil && strings.Contains(string(after), "deja") {
		t.Errorf("codex stayed wired because another target refused: %s", after)
	}
}

// `deja uninstall --all` left ~/.agents/skills/deja-history/SKILL.md on disk
// with deja's guidance in it. The guard that protects the shared file from one
// harness leaving asked readWiringState(), which still lists every harness
// during the run that removes them all (#1683).
func TestUninstallAllRemovesTheSharedSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	// codex is a shared-skill harness: its guidance lives in ~/.agents.
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "--all"); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(home, ".agents", "skills", "deja-history", "SKILL.md")
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("install did not write the shared skill, wrong fixture: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); err == nil {
		b, _ := os.ReadFile(shared)
		t.Errorf("uninstall --all left the shared skill behind:\n%s", string(b[:min(len(b), 200)]))
	}
}

// One harness leaving must still not take the file from the rest — that is what
// the guard is for, and the fix must not cost it.
func TestUninstallOneKeepsTheSharedSkillForTheOthers(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	for _, d := range []string{".codex", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--all"); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(home, ".agents", "skills", "deja-history", "SKILL.md")
	if _, err := captureRun(t, "uninstall", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Errorf("uninstalling codex took the shared skill from cursor: %v", err)
	}
}

// #840 taught uninstall to delete a config that turned out to be entirely
// deja's: "removing something must not leave more behind than it found". The
// test for that is byte-emptiness, which only the markdown and TOML writers
// reach — every structured writer leaves `{"mcpServers":{}}` instead. So
// installing the whole family and uninstalling it leaves sixteen shells of
// files deja created itself (#2583).
func TestUninstallRemovesTheShellsItCreated(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if !strings.Contains(home, "TestUninstallRemovesTheShells") {
		t.Fatalf("refusing to install into %q", home)
	}
	targets := []string{"claude-code", "cursor", "gemini", "kimi", "qwen", "copilot", "pi", "omp"}
	for _, target := range targets {
		if _, err := captureRun(t, "install", target, "--no-index", "--no-guidance"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	// The premise: those installs created files that were not there before.
	if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); err != nil {
		t.Fatalf("install wrote no config: %v", err)
	}
	for _, target := range targets {
		if _, err := captureRun(t, "uninstall", target); err != nil {
			t.Fatalf("uninstall %s: %v", target, err)
		}
	}
	var left []string
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil //nolint:nilerr // an unreadable entry is not a leftover config
		}
		// deja's own state is not a harness config.
		if strings.Contains(p, filepath.Join(".config", "deja")) {
			return nil
		}
		left = append(left, strings.TrimPrefix(p, home))
		return nil
	})
	if len(left) > 0 {
		t.Errorf("uninstall left %d files deja created:\n  %s", len(left), strings.Join(left, "\n  "))
	}
}

// A config the reader already had keeps its place, emptied of deja and nothing
// else — deleting that one would be taking away a file deja never made.
func TestUninstallKeepsAConfigItDidNotCreate(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\n  \"mcpServers\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a config deja did not create was removed: %v", err)
	}
}

// TestUninstallTakesBackEveryFileItWrote installs every target in one home,
// uninstalls every target, and walks what is left.
//
// TestUninstallLeavesNoFileOrDirItCreated asks the same question of a handful
// of paths named in the test. Naming them is what hid #3684: a snapshot of
// deja's own statusline stayed beside ~/.claude/settings.json, naming the
// binary the uninstall had just orphaned, and no test looked there.
func TestUninstallTakesBackEveryFileItWrote(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if home == "" || !strings.Contains(home, "TestUninstallTakesBack") {
		t.Fatalf("refusing to install into %q", home)
	}
	targets := []string{}
	for _, name := range installTargetNames() {
		// The sync timer is the one target that reaches outside the home
		// directory — a launchd or systemd unit the platform then owns.
		if name == "sync-timer" {
			continue
		}
		targets = append(targets, name)
	}
	for _, target := range targets {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	// While every target is installed, the other sweeps over the same home:
	// every skill declares the name of its directory (#3700), and every
	// doctor table agrees that what was just installed is wired. Installing
	// all of them is the most expensive thing in this suite, so it is done once.
	skillNamesMatchTheirDirectories(t, home)
	doctorAgreesWithInstall(t)
	// What deja says it created, before the uninstall wipes the record.
	made := append([]string(nil), readWiringState().Dirs...)
	if len(made) == 0 {
		t.Fatal("the record names no directory deja created, so there is nothing to check")
	}
	for _, target := range targets {
		if _, err := captureRun(t, "uninstall", target); err != nil {
			t.Fatalf("uninstall %s: %v", target, err)
		}
	}
	doctorAgreesWithUninstall(t)
	// A directory deja made goes when the last thing in it does. Thirty-three
	// were left on a bare home, from the harness roots down to a plugin
	// directory four levels deep (#3698).
	for _, dir := range made {
		if strings.Contains(dir, filepath.Join(".config", "deja")) {
			continue
		}
		if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
			t.Errorf("uninstall left the empty directory it created: %s", strings.TrimPrefix(dir, home))
		}
	}
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil //nolint:nilerr // an unreadable entry is not a leftover config
		}
		// deja's own directory is not a harness config: the wiring record
		// lives there and is the binary's, not the harness's.
		if strings.Contains(p, filepath.Join(".config", "deja")) {
			return nil
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // test walks its own temp home
		if rerr != nil {
			return nil //nolint:nilerr // an unreadable file is not a leftover config
		}
		if mentionsDeja(b) {
			t.Errorf("uninstall left %s, which still names deja: %s", p,
				strings.Join(strings.Fields(string(b)), " "))
		}
		return nil
	})
}

// TestInstallStatuslineRewritesOneWrittenByAnotherBuild: the status bar names
// the binary directly rather than the launcher, so a deja that moved leaves an
// entry running a file that is not there. The install used to call that entry
// the reader's and refuse it, printing a combine line that piped the missing
// binary into this one (#3684).
func TestInstallStatuslineRewritesOneWrittenByAnotherBuild(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := map[string]any{"statusLine": map[string]any{
		"type": "command", "command": filepath.Join(home, "gone", "deja") + " statusline", "refreshInterval": 1000,
	}}
	b, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(settings, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "statusline", "--no-index")
	if err != nil {
		t.Fatalf("install statusline: %v", err)
	}
	if !strings.Contains(out, "replaced the deja statusline") {
		t.Errorf("install said nothing about the entry it replaced:\n%s", out)
	}
	cmd := statuslineCommand(t, settings)
	if strings.Contains(cmd, filepath.Join(home, "gone")) {
		t.Errorf("statusline still runs the binary that moved: %q", cmd)
	}
	if !strings.HasSuffix(cmd, " statusline") {
		t.Errorf("statusline command is not deja's: %q", cmd)
	}
	// And it comes out again, which the exact-match test could not do either.
	if _, err := captureRun(t, "uninstall", "statusline"); err != nil {
		t.Fatalf("uninstall statusline: %v", err)
	}
	if got := statuslineCommand(t, settings); got != "" {
		t.Errorf("uninstall left a statusline behind: %q", got)
	}
}

// A statusline the reader combined with their own is theirs: deja's half runs
// inside a line they wrote, and rewriting the entry would throw the other half
// away. `deja install statusline` used to answer such a line with the same
// error it answers a stranger's — offering to combine what was already
// combined, and naming both halves a second time.
func TestInstallStatuslineKeepsACombinedLine(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	combined := combinedStatusline("ccstatusline", filepath.Join(home, "bin", "deja"))
	b, err := json.Marshal(map[string]any{"statusLine": map[string]any{"type": "command", "command": combined}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "statusline", "--no-index"); err != nil {
		t.Fatalf("install statusline: %v", err)
	}
	if got := statuslineCommand(t, settings); got != combined {
		t.Errorf("install rewrote a line the reader built:\n got  %q\n want %q", got, combined)
	}
	// Not ours to remove either.
	if _, err := captureRun(t, "uninstall", "statusline"); err != nil {
		t.Fatalf("uninstall statusline: %v", err)
	}
	if got := statuslineCommand(t, settings); got != combined {
		t.Errorf("uninstall took out a line the reader built: %q", got)
	}
}

// Installing only the statusline used to record nothing at all — the record
// skipped the target, and with no target left it was never written — so the
// uninstall did not know it had created the file and left an empty one behind,
// beside a snapshot of deja's own wiring it reported as the reader's (#3684).
func TestUninstallStatuslineRemovesTheSettingsItCreated(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "statusline", "--no-index"); err != nil {
		t.Fatalf("install statusline: %v", err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("install did not write %s: %v", settings, err)
	}
	out, err := captureRun(t, "uninstall", "statusline")
	if err != nil {
		t.Fatalf("uninstall statusline: %v", err)
	}
	if _, err := os.Stat(settings); err == nil {
		b, _ := os.ReadFile(settings)
		t.Errorf("uninstall left %s behind: %s", settings, strings.TrimSpace(string(b)))
	}
	if _, err := os.Stat(settings + ".bak"); err == nil {
		t.Errorf("uninstall left a snapshot of deja's own config at %s.bak", settings)
	}
	if strings.Contains(out, "kept 1 snapshot of configs you already had") {
		t.Errorf("uninstall called its own snapshot the reader's:\n%s", out)
	}
}

// The commands directory deja had to create goes with the last command in it.
// The writer made its own directory without recording it, so an empty
// ~/.claude/commands survived every uninstall on a machine that had no
// commands of its own (#3685).
func TestUninstallRemovesACommandsDirectoryItCreated(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatalf("install claude-auto: %v", err)
	}
	commands := filepath.Join(home, ".claude", "commands")
	if _, err := os.Stat(filepath.Join(commands, "deja.md")); err != nil {
		t.Fatalf("install did not write the command file: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "claude-auto"); err != nil {
		t.Fatalf("uninstall claude-auto: %v", err)
	}
	if _, err := os.Stat(commands); err == nil {
		t.Errorf("uninstall left %s behind", commands)
	}
}

// The prune walks upward through directories deja made (#3698), so the other
// half of the rule has to hold too: a directory the reader has since put
// something in stays, whoever created it.
func TestUninstallKeepsADirectoryTheReaderHasFilled(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"pi-auto", "claude-auto"} {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	theirs := map[string]string{
		filepath.Join(home, ".pi", "agent", "extensions", "theirs.ts"): "export default {}\n",
		filepath.Join(home, ".claude", "commands", "theirs.md"):        "# their command\n",
	}
	for path, body := range theirs {
		if _, err := os.Stat(filepath.Dir(path)); err != nil {
			t.Fatalf("install did not create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"pi-auto", "claude-auto"} {
		if _, err := captureRun(t, "uninstall", target); err != nil {
			t.Fatalf("uninstall %s: %v", target, err)
		}
	}
	for path := range theirs {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("uninstall took a file of the reader's with the directory: %s", strings.TrimPrefix(path, home))
		}
	}
}

// A commands directory the reader already had stays, empty or not: the record
// is what tells the two apart.
func TestUninstallKeepsACommandsDirectoryTheReaderHad(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	commands := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(commands, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatalf("install claude-auto: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "claude-auto"); err != nil {
		t.Fatalf("uninstall claude-auto: %v", err)
	}
	if _, err := os.Stat(commands); err != nil {
		t.Errorf("uninstall removed a directory the reader already had: %v", err)
	}
}

// `uninstall --all` walks the harnesses it finds, and the status bar is not a
// harness: it stayed wired to the binary the reader was removing (#3684).
func TestUninstallAllTakesTheStatuslineToo(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"claude-auto", "statusline"} {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatalf("uninstall --all: %v", err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if got := statuslineCommand(t, settings); got != "" {
		t.Errorf("uninstall --all left the status bar running %q", got)
	}
}

// A statusline of someone else's is not reached by `uninstall --all` either.
func TestUninstallAllKeepsAStatuslineThatIsNotDejas(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"statusLine": map[string]any{"type": "command", "command": "ccstatusline"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatalf("install claude-auto: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatalf("uninstall --all: %v", err)
	}
	if got := statuslineCommand(t, settings); got != "ccstatusline" {
		t.Errorf("uninstall --all touched a statusline that is not deja's: %q", got)
	}
}

// doctor prints `deja install ` and every target it recorded when the binary
// has moved, so the command has to accept more than one of them: with two
// recorded targets that line answered "install needs a target" (#3686).
func TestInstallTakesSeveralTargetsAtOnce(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "claude-auto", "statusline", "--no-index")
	if err != nil {
		t.Fatalf("install claude-auto statusline: %v", err)
	}
	if statuslineCommand(t, filepath.Join(home, ".claude", "settings.json")) == "" {
		t.Errorf("the second target was not installed:\n%s", out)
	}
	if !strings.Contains(out, "claude-auto") {
		t.Errorf("the first target was not installed:\n%s", out)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--all"); err == nil {
		t.Error("--all beside a target was accepted")
	}
}

func statuslineCommand(t *testing.T, settings string) string {
	t.Helper()
	b, err := os.ReadFile(settings)
	if err != nil {
		return ""
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s: %v", settings, err)
	}
	entry, _ := root["statusLine"].(map[string]any)
	cmd, _ := entry["command"].(string)
	return cmd
}

// What an earlier test's run created is not this one's: on the windows leg
// TestUninstallTakesBackEveryFileItWrote found goose's config directory from
// another test in its record, once the tests before it were no longer the
// whole suite.
func TestHermeticEnvStartsAFreshRunRecord(t *testing.T) {
	earlier := filepath.Join(t.TempDir(), "earlier")
	createdDirsByThisRun = append(createdDirsByThisRun, earlier)
	hermeticEnv(t)
	if slices.Contains(createdDirsByThisRun, earlier) {
		t.Fatalf("a directory from an earlier run is still on this run's record: %v", createdDirsByThisRun)
	}
}

func doctorAgreesWithInstall(t *testing.T) {
	t.Helper()
	for _, c := range doctorMCPConfigs() {
		// Roo writes only into an editor host that already has the extension.
		if c.name == "roo" {
			continue
		}
		if !c.wired(c.path) {
			t.Errorf("doctor MCP row %s (%s) is not wired after installing every target", c.name, c.path)
			continue
		}
		if c.dupes != nil {
			if keys := c.dupes(c.path); len(keys) != 1 {
				t.Errorf("doctor MCP row %s (%s) reports deja keys %v, want 1", c.name, c.path, keys)
			}
		}
		if cmd := dejaCommandIn(c.path); cmd == "" {
			t.Errorf("doctor MCP row %s (%s) is wired but dejaCommandIn read no binary from it", c.name, c.path)
		}
	}
	for _, c := range doctorCommandFiles() {
		if st := c.state(); st != "written" && st != "skill" {
			t.Errorf("doctor command row %s (%s) is %q after installing every target, want written or skill", c.name, c.path, st)
		}
	}
	for _, a := range autoWirings() {
		if a.name == "roo" {
			continue
		}
		st, _ := autoWiringState(a)
		want := "wired"
		if a.name == "kiro" {
			want = "installed"
		}
		if st != want {
			t.Errorf("doctor auto row %s is %q after installing every target, want %s", a.name, st, want)
		}
		if a.marker != "" && !underTestBinary() {
			// Under go test, os.Executable() is `deja.test` (not `deja`), so on
			// Windows (where no `deja-hook` launcher is used) the installed path
			// ends in `deja.test.exe` rather than `deja.exe`.
			cmd := dejaHookCommandIn(a.path())
			if cmd == "" && a.name == "reasonix" {
				cmd = dejaCommandIn(a.path())
			}
			if cmd == "" {
				t.Errorf("doctor auto row %s (%s) is %s but read no binary from it", a.name, a.path(), st)
			}
		}
	}
	if st := claudeHookWiringState(); st.state != "wired" {
		t.Errorf("claudeHookWiringState = %q after installing every target, want wired", st.state)
	}
	if st := codexHookWiringState(); st.state != "wired" && st.state != "untrusted" {
		t.Errorf("codexHookWiringState = %q after installing every target, want wired or untrusted", st.state)
	}
}

func doctorAgreesWithUninstall(t *testing.T) {
	t.Helper()
	for _, c := range doctorMCPConfigs() {
		if c.wired(c.path) {
			t.Errorf("doctor MCP row %s (%s) is still wired after uninstalling every target", c.name, c.path)
		}
	}
	for _, c := range doctorCommandFiles() {
		if st := c.state(); st != "missing" {
			t.Errorf("doctor command row %s (%s) is %q after uninstalling every target, want missing", c.name, c.path, st)
		}
	}
	for _, a := range autoWirings() {
		if st, _ := autoWiringState(a); st != "missing" {
			t.Errorf("doctor auto row %s is %q after uninstalling every target, want missing", a.name, st)
		}
	}
	if st := claudeHookWiringState(); st.state != "missing" {
		t.Errorf("claudeHookWiringState = %q after uninstalling every target, want missing", st.state)
	}
	if st := codexHookWiringState(); st.state != "missing" {
		t.Errorf("codexHookWiringState = %q after uninstalling every target, want missing", st.state)
	}
}

// install refuses a target it does not know and names the near miss; uninstall
// printed nothing at all and exited 0, so removing deja by a half-remembered
// name read as done while the wiring stayed (#2273).
func TestUninstallRefusesATargetItDoesNotKnow(t *testing.T) {
	withTempStores(t)

	for _, target := range []string{"nosuchagent", "claude-cod"} {
		out, err := captureRun(t, "uninstall", target)
		if err == nil {
			t.Errorf("uninstall %s succeeded, printing %q", target, out)
			continue
		}
		if !strings.Contains(err.Error(), target) {
			t.Errorf("uninstall %s said %v — it should name what it did not recognise", target, err)
		}
	}
	// The near miss is named, as install does.
	if _, err := captureRun(t, "uninstall", "claude-cod"); err == nil ||
		!strings.Contains(err.Error(), "claude-code") {
		t.Errorf("uninstall claude-cod does not point at claude-code: %v", err)
	}
	// A target it does know still uninstalls, whether or not anything is wired.
	if _, err := captureRun(t, "uninstall", "claude-code", "--no-index"); err != nil {
		t.Errorf("uninstall claude-code: %v", err)
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Removing something must not leave more behind than it found. On the
// uninstall path every target computes "the config without deja in it", which
// for a file that does not exist is an empty config it then creates (#676).
func TestUninstallCreatesNothingOnAMachineThatNeverHadDeja(t *testing.T) {
	hermeticEnv(t)
	// The harnesses are installed; deja never was.
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A config that exists but never mentioned deja: the empty mcpServers block
	// used to be added to it on the way out, with a .bak of it besides.
	claudeJSON := filepath.Join(os.Getenv("HOME"), ".claude.json")
	const untouched = "{\n  \"editorMode\": \"vim\"\n}\n"
	if err := os.WriteFile(claudeJSON, []byte(untouched), 0o600); err != nil {
		t.Fatal(err)
	}
	before := filesUnder(t, os.Getenv("HOME"))
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatal(err)
	}
	after := filesUnder(t, os.Getenv("HOME"))
	for p := range after {
		if !before[p] {
			t.Errorf("uninstall created %s", p)
		}
	}
	got, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != untouched {
		t.Errorf("uninstall rewrote a config that never mentioned deja:\n%s", got)
	}
}

func filesUnder(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	if err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// The other direction still has to work: what install wired, uninstall removes.
func TestUninstallStillRemovesWhatInstallWrote(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if n := len(filesMentioning(t, home, "deja")); n == 0 {
		t.Fatal("install wrote nothing that mentions deja")
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatal(err)
	}
	if left := filesMentioning(t, home, "\"deja\""); len(left) > 0 {
		t.Errorf("uninstall left deja wired in %v", left)
	}
}

// What install created, uninstall takes back: an AGENTS.md that held nothing
// but deja's block was truncated to zero bytes rather than deleted, and the
// skills/deja-history/ deja made for the Claude skill was left standing empty
// (#840). The .bak of a config the user already had is the documented safety
// net and stays.
func TestUninstallLeavesNoFileOrDirItCreated(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A codex config the user already had, and no AGENTS.md beside it.
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("model = \"gpt-5\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(home, ".codex", "AGENTS.md")
	skill := filepath.Join(home, ".claude", "skills", "deja-history", "SKILL.md")
	for _, p := range []string{agents, skill} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("install did not write %s: %v", p, err)
		}
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		agents,
		agents + ".bak",
		filepath.Dir(skill),
		filepath.Join(home, ".claude", "skills"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("uninstall left behind %s", p)
		}
	}
	// The user's own config and its snapshot are not ours to delete.
	for _, p := range []string{
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".codex", "config.toml.bak"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("uninstall removed %s: %v", p, err)
		}
	}
}

// A skills/ holding someone else's skill is not deja's to clean up, even once
// deja's own is gone.
func TestUninstallKeepsASkillsDirectoryThatIsNotEmpty(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude", "--no-index"); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(home, ".claude", "skills", "their-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(theirs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theirs, []byte("---\nname: theirs\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Errorf("uninstall removed a skill that is not deja's: %v", err)
	}
}

// The same when skills/ is a symlink into someone's dotfiles: os.Remove drops
// a link whatever stands behind it, so "delete it only if it is empty" is no
// protection there — the link went and the skills it pointed at stayed.
func TestUninstallKeepsASymlinkedSkillsDirectory(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	real := filepath.Join(home, "dotfiles", "skills")
	theirs := filepath.Join(real, "their-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(theirs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theirs, []byte("---\nname: theirs\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude", "skills")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := captureRun(t, "install", "claude", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("uninstall removed the user's skills symlink: %v", err)
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Errorf("uninstall removed a skill that is not deja's: %v", err)
	}
}

func filesMentioning(t *testing.T, root, needle string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.HasSuffix(p, ".bak") {
			return nil
		}
		// The store's own state is not wiring: notes, the index, the record of
		// what was installed.
		if strings.Contains(filepath.ToSlash(p), "/.config/deja/") || strings.Contains(filepath.ToSlash(p), "/deja/notes") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(b), needle) {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

// A stray flag fell into the target list, and the refusal then said the target
// was missing while printing the very target it had been given (#1078).
func TestInstallNamesAStrayFlagInsteadOfBlamingTheTarget(t *testing.T) {
	hermeticEnv(t)
	err := runInstall(os.Getenv("DEJA_INDEX_DIR"), []string{"claude-code", "--nonsense"}, false)
	if err == nil {
		t.Fatal("a stray flag was accepted")
	}
	if !strings.Contains(err.Error(), `unknown flag "--nonsense"`) {
		t.Errorf("the refusal does not name the flag: %v", err)
	}
	if strings.Contains(err.Error(), "needs a target") {
		t.Errorf("the refusal still blames the target it was given: %v", err)
	}
	// uninstall shares the path.
	if err := runInstall(os.Getenv("DEJA_INDEX_DIR"), []string{"claude-code", "--dry-run"}, true); err == nil ||
		!strings.Contains(err.Error(), `uninstall: unknown flag "--dry-run"`) {
		t.Errorf("uninstall: %v", err)
	}
	// The flags it does take stay accepted, and a missing target still says so.
	if err := runInstall(os.Getenv("DEJA_INDEX_DIR"), []string{"claude-code", "--no-index"}, false); err != nil {
		t.Errorf("--no-index refused: %v", err)
	}
	if err := runInstall(os.Getenv("DEJA_INDEX_DIR"), nil, false); err == nil || !strings.Contains(err.Error(), "needs a target") {
		t.Errorf("a missing target lost its message: %v", err)
	}
}

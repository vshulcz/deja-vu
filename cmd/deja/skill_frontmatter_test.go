package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A skill's name and its directory have to agree: Claude Code requires it, and
// Gemini renames the loser when two collide — which is how #3665 was found.
// Three harnesses were getting `name: deja-history` inside a directory called
// `deja-search`, so the manual was a file their loader could drop without a
// word (#3700).
//
// Read every file deja writes the way a harness reads it, rather than checking
// the three paths by hand.
// The install sweep is shared with the round-trip test rather than run twice:
// installing every target is the most expensive thing in this suite and the
// Windows leg pays for it (see skillNamesMatchTheirDirectories, called there).
func TestEverySkillDeclaresTheNameOfItsDirectory(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// The three that had the bug, plus Claude's own and the shared one.
	for _, target := range []string{"kilocode", "gjc", "commandcode", "claude-code", "codex"} {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			continue
		}
	}
	skillNamesMatchTheirDirectories(t, home)
}

// skillNamesMatchTheirDirectories walks a home and checks every SKILL.md in it.
func skillNamesMatchTheirDirectories(t *testing.T, home string) {
	t.Helper()
	found := 0
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() || filepath.Base(p) != "SKILL.md" {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		found++
		name := frontmatterField(string(b), "name")
		if name == "" {
			t.Errorf("%s declares no name", strings.TrimPrefix(p, home))
			return nil
		}
		if dir := filepath.Base(filepath.Dir(p)); name != dir {
			t.Errorf("%s declares name %q in a directory called %q",
				strings.TrimPrefix(p, home), name, dir)
		}
		return nil
	})
	if found == 0 {
		t.Fatal("no skill files were written, so there is nothing to check")
	}
	t.Logf("checked %d skill files", found)
}

// frontmatterField reads one top-level key out of a markdown file's
// frontmatter. Top-level only: the metadata block below carries nested keys of
// its own and none of them is the name.
func frontmatterField(text, key string) string {
	if !strings.HasPrefix(text, "---") {
		return ""
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(text[3:3+end], "\n") {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	return ""
}

// The copy written under the old name comes out on install, and the CLI skill
// that legitimately lives in a `deja-search` directory is left alone.
func TestInstallDropsTheManualItWroteUnderTheCLISkillsName(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	retired := filepath.Join(home, ".kilocode", "skills", "deja-search", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(retired), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retired, []byte(skillFile(skillBody)), 0o644); err != nil {
		t.Fatal(err)
	}
	// And the CLI skill of deja's own, in its own directory, which stays.
	cli := filepath.Join(home, ".agents", "skills", cliSkillName, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kilocode", "--no-index"); err != nil {
		t.Fatalf("install kilocode: %v", err)
	}
	if _, err := os.Stat(retired); err == nil {
		t.Errorf("the manual under the CLI skill's name survived the install")
	}
	if _, err := os.Stat(filepath.Join(home, ".kilocode", "skills", "deja-history", "SKILL.md")); err != nil {
		t.Errorf("the manual was not written under its own name: %v", err)
	}
	if _, err := os.Stat(cli); err != nil {
		t.Errorf("the CLI skill was taken with it: %v", err)
	}
}

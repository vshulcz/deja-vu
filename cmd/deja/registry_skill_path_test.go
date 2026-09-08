package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A registry page that names the file deja's skill lands in has to name the one
// install writes. The kimi page said `$KIMI_CODE_HOME/skills/deja-history/`
// while install writes the shared `~/.agents/skills/…` (#3212), and the goose
// page said `.goosehints` for a block that moved to AGENTS.md (#3210) — the
// same drift twice in one night, in the reference people are pointed at.
//
// Only the deja-history path is checked. A page naming a directory the harness
// scans, or another tool's, is describing the harness rather than claiming
// where deja puts its file.
func TestRegistryPagesNameTheSkillPathInstallWrites(t *testing.T) {
	home := hermeticEnv(t)
	home = filepath.Join(home, "home")
	dir := filepath.Join("..", "..", "docs", "registry")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// `~/x/skills/deja-history/…` or `$VAR/skills/deja-history/…`, in backticks.
	claim := regexp.MustCompile("`([^`]*skills/deja-history[^`]*)`")
	alias := map[string]string{"claude-code": "claude", "copilot-chat": "vscode"}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" || e.Name() == "README.md" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		if a, ok := alias[id]; ok {
			id = a
		}
		want := guidancePath(id)
		if want == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range claim.FindAllStringSubmatch(string(b), -1) {
			said := strings.TrimSuffix(strings.TrimSpace(m[1]), "/")
			// Compare home-relative and in the docs' own separator: the pages
			// are written with "/" on every platform while guidancePath answers
			// in the host's, which made this pass everywhere and fail on the
			// Windows leg alone. Accept the directory as well as the file — a
			// page may name either.
			got := "~" + filepath.ToSlash(strings.TrimPrefix(want, home))
			got = strings.TrimSuffix(got, "/")
			if said != got && said != strings.TrimSuffix(got, "/SKILL.md") {
				t.Errorf("docs/registry/%s says the skill lands at %s; install writes %s",
					e.Name(), said, got)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no page names a skill path, so this test checks nothing")
	}
}

// The same drift in the README, which is where most people read it: it said
// Cursor's skill lands in `~/.cursor/skills/` long after install moved to the
// shared directory and started removing the old path as retired (#3187).
func TestTheReadmeDoesNotNameARetiredSkillDirectory(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	if !strings.Contains(readme, "~/.agents/skills") {
		t.Fatalf("the README names no skill directory at all, so this checks nothing")
	}
	for id := range sharedSkillHarnesses {
		// A harness that reads the shared directory has no directory of its
		// own to name — install writes one file and retires the rest.
		own := "~/." + id + "/skills"
		if strings.Contains(readme, own) {
			t.Errorf("the README says %s gets a skill at %s; install writes the shared ~/.agents/skills",
				id, own)
		}
	}
}

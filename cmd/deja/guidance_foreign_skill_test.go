package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Retiring guidance deletes files, so it has to be sure whose they are. A
// skill file carries frontmatter rather than deja's markers, and the only
// thing separating ours from one someone wrote is the directory name and the
// name field — both of which deja generates and both of which the code checks
// before removing anything.
//
// Measured before this test: dropping that check and deleting any SKILL.md at
// a retired path broke nothing in the package.
func TestRetiringGuidanceLeavesAForeignSkillAlone(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(sources.GeminiHome(), "skills", "deja-history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	// Someone else's skill, sitting where ours used to live: the directory
	// name matches, the name field does not.
	const foreign = "---\nname: my-own-history\ndescription: hand-written\n---\n\nkeep me\n"
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := dropRetiredGuidanceForTest("gemini"); err != nil {
		t.Fatalf("dropRetiredGuidance: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("a skill deja did not write was deleted: %v", err)
	}
	if string(got) != foreign {
		t.Errorf("the file was rewritten:\n%q", string(got))
	}
}

// And ours does go: the same shape, with the name deja generates.
func TestRetiringGuidanceRemovesOurOwnSkill(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(sources.GeminiHome(), "skills", "deja-history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	const ours = "---\nname: deja-history\ndescription: search past sessions\n---\n\nrun deja\n"
	if err := os.WriteFile(path, []byte(ours), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := dropRetiredGuidanceForTest("gemini"); err != nil {
		t.Fatalf("dropRetiredGuidance: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("our own retired skill survived: %v", err)
	}
	// The directory goes too, but only because we emptied it.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the emptied directory survived: %v", err)
	}
}

// A directory that still holds something of the reader's is not removed, even
// once ours is gone from it.
func TestRetiringGuidanceKeepsADirectoryThatIsNotEmpty(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(sources.GeminiHome(), "skills", "deja-history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: deja-history\n---\n\nrun deja\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := dropRetiredGuidanceForTest("gemini"); err != nil {
		t.Fatalf("dropRetiredGuidance: %v", err)
	}

	if got, err := os.ReadFile(notes); err != nil || strings.TrimSpace(string(got)) != "mine" {
		t.Errorf("a file next to ours was taken with it: %v %q", err, string(got))
	}
}

// A relocation variable can point a harness's own directory at the shared one,
// and then the path deja would retire for that harness is the skill seven
// others read. CURSOR_CONFIG_DIR is taken verbatim, so pointing it at
// ~/.agents makes cursor's retired skill path exactly sharedSkillPath().
//
// Removing the guard that spares it broke nothing in the package before this.
func TestRetiringGuidanceSparesTheSharedSkill(t *testing.T) {
	hermeticEnv(t)
	shared := sharedSkillPath()
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "---\nname: deja-history\ndescription: search past sessions\n---\n\nrun deja\n"
	if err := os.WriteFile(shared, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// The collision the comment names.
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Dir(filepath.Dir(filepath.Dir(shared))))
	var collides bool
	for _, p := range retiredGuidancePaths("cursor") {
		if p == shared {
			collides = true
		}
	}
	if !collides {
		t.Fatalf("wrong fixture: no retired path equals %s\n  %v", shared, retiredGuidancePaths("cursor"))
	}

	if err := dropRetiredGuidanceForTest("cursor"); err != nil {
		t.Fatalf("dropRetiredGuidance: %v", err)
	}

	got, err := os.ReadFile(shared)
	if err != nil {
		t.Fatalf("the skill seven harnesses read was deleted: %v", err)
	}
	if string(got) != body {
		t.Errorf("the shared skill was rewritten:\n%q", string(got))
	}
}

// dropRetiredGuidanceForTest keeps these tests reading as they did before the
// note was added: they are about what the files look like afterwards.
func dropRetiredGuidanceForTest(harness string) error {
	_, err := dropRetiredGuidance(harness, true)
	return err
}

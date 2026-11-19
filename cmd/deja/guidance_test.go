package main

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// An -auto target is the same harness with hooks on top, so it must land on the
// same guidance file. Three targets used to be listed by hand and the other nine
// silently wrote no guidance at all, including through `deja install --auto`
// (#1199). Walking every target means the next harness cannot be forgotten.
func TestAutoTargetsShareTheirHarnessGuidance(t *testing.T) {
	hermeticEnv(t)
	checked := 0
	for _, target := range installTargetNames() {
		if !strings.HasSuffix(target, "-auto") {
			continue
		}
		base := strings.TrimSuffix(target, "-auto")
		if base == "claude" {
			base = "claude-code"
		}
		want := guidancePath(base)
		if want == "" {
			continue
		}
		checked++
		if got := guidancePath(guidanceHarness(target)); got != want {
			t.Errorf("%s guidance = %q, base %q writes %q", target, got, base, want)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d -auto targets have a guidance file — the walk is not covering them", checked)
	}
}

// grok's guidance is a marked block inside a file the reader owns, so a round
// trip has to give that file back exactly. It came back one blank line longer:
// install trims the trailing newlines and appends `\n\n` + block, and the
// uninstall cut the block alone, leaving the separator behind (#3703).
func TestAGuidanceBlockRoundTripLeavesTheFileAsItWas(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".grok", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file that ends the way text files end comes back byte for byte. The
	// install normalises the trailing run — it trims and appends exactly one
	// blank line before its block — so a file with two blank lines at the end,
	// or none, comes back ending in a single newline; that change happens at
	// install time and is the same in the file while deja is wired, which is
	// why it is the contract here rather than a second bug.
	for _, theirs := range []string{
		"# My own notes\n\nAlways run `make check` before pushing.\n",
		"one line, no blank at the end\n",
		"windows endings\r\n\r\nand a second paragraph\r\n",
	} {
		if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "grok", "--no-index"); err != nil {
			t.Fatalf("install grok: %v", err)
		}
		mid, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !containsBlock(string(mid)) {
			t.Fatalf("install wrote no block into %q", theirs)
		}
		if _, err = captureRun(t, "uninstall", "grok"); err != nil {
			t.Fatalf("uninstall grok: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != theirs {
			t.Errorf("round trip changed the reader's file:\n given %q\n back  %q", theirs, string(got))
		}
	}
}

func containsBlock(s string) bool {
	return strings.Contains(s, guidanceStart) && strings.Contains(s, guidanceEnd)
}

// And the normalisation the install does state itself, so nobody reads it as
// the bug above: a trailing run of blank lines becomes one.
func TestAGuidanceBlockNormalisesTheTrailingBlankLines(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".grok", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two blank lines after this\n\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "grok", "--no-index"); err != nil {
		t.Fatalf("install grok: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "grok"); err != nil {
		t.Fatalf("uninstall grok: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "two blank lines after this\n"; string(got) != want {
		t.Errorf("trailing blank lines came back as %q, want %q", string(got), want)
	}
}

const guidanceBlockSample = guidanceStart + "\nsome guidance\n" + guidanceEnd + "\n"

// A file whose markers cannot be paired had a whole second block appended, and
// the uninstall after that cut from the first start to the only end — over the
// user's own text — and deleted the file, because what was left was empty
// (#1705).
func TestGuidanceBlockRefusesUnpairedMarkers(t *testing.T) {
	noEnd := strings.ReplaceAll(guidanceBlockSample, guidanceEnd+"\n", "") + "MY OWN text below\n"
	if _, err := updateGuidanceBlock(noEnd, false); err == nil {
		t.Error("install edited a file with no end marker")
	}
	if _, err := updateGuidanceBlock(noEnd, true); err == nil {
		t.Error("uninstall edited a file with no end marker")
	}
	// A lone end marker is what an inline start looks like to a line scanner,
	// and appending below it costs nobody anything — so it is not refused.
	noStart := strings.ReplaceAll(guidanceBlockSample, guidanceStart+"\n", "") + "MY OWN text below\n"
	got, err := updateGuidanceBlock(noStart, false)
	if err != nil {
		t.Fatalf("a lone end marker was refused: %v", err)
	}
	if !strings.Contains(got, "MY OWN text below") {
		t.Errorf("the user's text was dropped:\n%s", got)
	}
}

// Two complete blocks are repaired to one rather than kept for ever.
func TestGuidanceBlockRepairsADuplicate(t *testing.T) {
	two := guidanceBlockSample + "MY OWN text between\n\n" + guidanceBlockSample
	got, err := updateGuidanceBlock(two, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, guidanceStart); n != 1 {
		t.Errorf("expected one block, found %d:\n%s", n, got)
	}
	if !strings.Contains(got, "MY OWN text between") {
		t.Errorf("the user's text was dropped:\n%s", got)
	}
}

// grok writes GROK.md and AGENTS.md from one install. A refusal on the second
// used to leave the first already rewritten (#1705, found in review).
func TestGuidanceGrokTwinIsCheckedFirst(t *testing.T) {
	hermeticEnv(t)
	dir := sources.GrokHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := guidanceStart + "\nSTALE\n" + guidanceEnd + "\n"
	primary := filepath.Join(dir, "GROK.md")
	if err := os.WriteFile(primary, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	half := guidanceStart + "\nsome guidance\n" + "MY OWN in the twin\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(half), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "grok"); err == nil {
		t.Error("install accepted a twin it could not bound")
	}
	b, err := os.ReadFile(primary)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != stale {
		t.Errorf("the primary file was rewritten while the twin refused:\n%s", b)
	}
}

// The control: text above and below an intact block survives, and one block
// stays one block.
func TestGuidanceBlockKeepsTextAround(t *testing.T) {
	around := "MY OWN above\n\n" + guidanceBlockSample + "MY OWN below\n"
	got, err := updateGuidanceBlock(around, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MY OWN above", "MY OWN below"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was dropped:\n%s", want, got)
		}
	}
	if n := strings.Count(got, guidanceStart); n != 1 {
		t.Errorf("expected one block, found %d:\n%s", n, got)
	}
	// And uninstall takes the block, leaving the user's text.
	got, err = updateGuidanceBlock(got, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, guidanceStart) {
		t.Errorf("uninstall left the block:\n%s", got)
	}
	if !strings.Contains(got, "MY OWN above") || !strings.Contains(got, "MY OWN below") {
		t.Errorf("uninstall took the user's text:\n%s", got)
	}
}

// A skill deja writes for a harness must not tell that harness deja cannot read
// it. Copilot's opened with "deja does not index Copilot history" — true when it
// was written, false since the parser landed, and the same binary was indexing
// Copilot sessions while saying so (#3222).
//
// Derived from the registry rather than a list beside it: a harness deja reads
// and writes guidance for fails here if its own text denies the reading.
func TestNoSkillDeniesTheHarnessItIsWrittenFor(t *testing.T) {
	hermeticEnv(t)
	for _, h := range registryHarnessesForGuidance(t) {
		id := h
		if v, ok := map[string]string{"claude": "claude-code", "copilot-chat": "vscode"}[h]; ok {
			id = v
		}
		if guidancePath(id) == "" {
			continue
		}
		text := guidanceText(id)
		low := strings.ToLower(text)
		for _, deny := range []string{
			"does not index",
			"deja cannot read",
			"is not indexed",
		} {
			if strings.Contains(low, deny) {
				t.Errorf("the %s skill says %q about a harness deja indexes:\n%s", h, deny, text)
			}
		}
	}
}

// registryHarnessesForGuidance is the harnesses the format registry lists,
// which is the same source the capability matrix is built from.
func registryHarnessesForGuidance(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg capRegistry
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, h := range reg.Harnesses {
		if h.ID != "deja" {
			out = append(out, h.ID)
		}
	}
	if len(out) == 0 {
		t.Fatal("no harnesses in the registry, so this test checks nothing")
	}
	return out
}

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

// opencode moved from a block in AGENTS.md to a skill. Install has to take the
// old block away, or the harness reads both and the replaced copy sits in every
// session forever — and a file that held only our block should not be left
// behind empty.
func TestOpencodeGuidanceMigratesOffAgentsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	agents := retiredGuidancePaths("opencode")[0]
	if err := os.MkdirAll(filepath.Dir(agents), 0o755); err != nil {
		t.Fatal(err)
	}

	// Our block among the user's own notes: the block goes, the notes stay.
	mixed := "# My rules\n\nrun make lint\n\n" + guidanceStart + "\nold\n" + guidanceEnd + "\n\nkeep me\n"
	if err := os.WriteFile(agents, []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("opencode", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), guidanceStart) {
		t.Fatalf("old block survived install: %s", b)
	}
	if !strings.Contains(string(b), "run make lint") || !strings.Contains(string(b), "keep me") {
		t.Fatalf("user content lost: %s", b)
	}
	if _, err := os.Stat(guidancePath("opencode")); err != nil {
		t.Fatalf("skill not written: %v", err)
	}

	// A file that was only ever ours is removed rather than left empty.
	if err := os.WriteFile(agents, []byte(guidanceStart+"\nold\n"+guidanceEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("opencode", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agents); !os.IsNotExist(err) {
		left, _ := os.ReadFile(agents)
		t.Fatalf("empty AGENTS.md left behind: %q err=%v", left, err)
	}
}

// A skill an older deja wrote is deja's, not the reader's. Install treats it as
// a stranger's file — it carries no marks — backs it up, and since #2581
// uninstall puts that backup back: so uninstalling deja left deja's own older
// guidance on disk, which is what #2575 exists to prevent. Its own frontmatter
// says whose it is (#2585).
func TestUninstallDoesNotPutBackAnOlderDejaSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	skill := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skill, "SKILL.md")
	// deja's own frontmatter, an older body.
	older := skillFile("Before re-deriving past work, search deja when the user refers to past work.")
	if older == guidanceText("claude-code") {
		t.Fatal("the fixture is this build's own guidance, so this measures nothing")
	}
	if err := os.WriteFile(path, []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("install did not back the older skill up: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err == nil {
		t.Errorf("an older deja skill was put back after uninstall:\n%s", strings.SplitN(string(b), "\n\n", 2)[0])
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("its backup is still there: %v", err)
	}
}

// And a skill the reader wrote is still theirs, even where they kept deja's
// name for the directory.
func TestUninstallStillPutsBackTheReadersOwnSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	skill := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skill, "SKILL.md")
	mine := "---\nname: deja-history\ndescription: my own version\n---\n\nAsk deja first. This file is mine.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "claude-code"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != mine {
		t.Errorf("the reader's own skill did not come back: %v\n%s", err, b)
	}
}

// pi scans the shared skills directory as well as its own, so a copy in both
// is one skill shipped twice — and pi prints the collision on every start
// (#3657). One file, and the old copy taken with it.
func TestPiReadsTheSharedSkillAndTheOldCopyGoes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_PI_ROOT", "")

	if got := guidancePath("pi"); got != sharedSkillPath() {
		t.Errorf("pi guidance = %q, want the shared skill %q", got, sharedSkillPath())
	}

	// A copy an older deja wrote in pi's own directory is what collides.
	own := filepath.Join(sources.PiConfigDir(), "skills", "deja-history", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte(skillFile(skillBody)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("pi", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(own); err == nil {
		t.Errorf("the colliding copy is still in pi's own directory: %s", own)
	}
	if _, err := os.Stat(sharedSkillPath()); err != nil {
		t.Errorf("the shared skill was not written: %v", err)
	}
}

// Install replaces a guidance file it did not write and says where the reader's
// copy went: "your copy is at …SKILL.md.bak". Uninstall then removed deja's
// file and left that copy sitting beside nothing — so a person who had written
// their own deja-history skill lost it by installing and uninstalling, with
// only a .bak and no line saying to rename it (#2581).
func TestUninstallPutsBackTheGuidanceItReplaced(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	skill := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skill, "SKILL.md")
	mine := "---\nname: deja-history\ndescription: my own version\n---\n\nThis file is mine.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// The premise: deja replaced it and kept the copy it promised.
	if b, err := os.ReadFile(path); err != nil || string(b) == mine {
		t.Fatalf("install did not replace the reader's skill: %v", err)
	}
	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != mine {
		t.Fatalf("install did not keep the copy it promised: %v", err)
	}

	if _, err := captureRun(t, "uninstall", "claude-code"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reader's own skill is gone after uninstall: %v", err)
	}
	if string(b) != mine {
		t.Errorf("the file at that path is not the reader's own:\n%s", b)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("the copy was put back and the backup left behind too: %v", err)
	}
}

// A guidance file deja wrote itself leaves nothing behind: no file, no backup.
func TestUninstallRemovesGuidanceItWroteWithNoLeftovers(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "skills", "deja-history", "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install wrote no guidance: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("deja's own guidance survived the uninstall: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("a backup of deja's own guidance survived: %v", err)
	}
}

// Eight harnesses read one file. Uninstalling any one of them must not take it
// away from the other seven — the failure would be silent, since nothing errors
// and the remaining harnesses simply stop finding the skill.
func TestSharedSkillSurvivesUninstallOfOneReader(t *testing.T) {
	hermeticEnv(t)
	if _, err := installGuidance("cursor", false); err != nil {
		t.Fatal(err)
	}
	path := guidancePath("cursor")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shared skill not written: %v", err)
	}
	if got := guidancePath("gemini"); got != path {
		t.Fatalf("gemini reads %q, cursor wrote %q", got, path)
	}

	recordWiring([]string{"cursor", "gemini"}, false)
	r, err := installGuidance("cursor", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "kept" {
		t.Fatalf("uninstalling one reader = %q, want kept", r.Action)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("shared skill removed while gemini still reads it: %v", err)
	}

	// With the last reader gone there is nothing left to serve.
	recordWiring([]string{"gemini"}, true)
	if r, err = installGuidance("cursor", true); err != nil {
		t.Fatal(err)
	}
	if r.Action != "removed" {
		t.Fatalf("uninstalling the last reader = %q, want removed", r.Action)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("shared skill survived the last reader: %v", err)
	}
}

func TestGuidanceTargetsAreUserLevelAndRespectXDG(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got := guidancePath("claude-code"); got != filepath.Join(home, ".claude", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("claude path = %q", got)
	}
	if got := guidancePath("opencode"); got != filepath.Join(xdg, "opencode", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("opencode path = %q", got)
	}
	// The AGENTS.md block an older deja wrote is stripped on install, so it
	// does not sit in every session beside the skill that replaced it.
	if got := retiredGuidancePaths("opencode"); len(got) != 1 || got[0] != filepath.Join(xdg, "opencode", "AGENTS.md") {
		t.Fatalf("opencode retired paths = %q", got)
	}
	// Inside the plugin: antigravity ingests skills/ from a directory marked by
	// plugin.json, and `agy plugin validate` confirms it there. Beside the
	// plugin nothing reads it.
	if got := guidancePath("antigravity"); got != filepath.Join(home, ".gemini", "config", "plugins", "deja", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("antigravity path = %q", got)
	}
	// qwen, and seven others, read the cross-agent directory rather than one
	// of their own, so they share a single file.
	if got := guidancePath("qwen"); got != filepath.Join(home, ".agents", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("qwen path = %q", got)
	}
	if got := guidancePath("codex"); got != guidancePath("qwen") {
		t.Fatalf("codex path = %q, does not match the shared skill", got)
	}
	if got := guidancePath("copilot"); got != filepath.Join(home, ".copilot", "skills", "deja-history", "SKILL.md") {
		t.Fatalf("copilot path = %q", got)
	}
	// grok reads <cwd>/.grok/GROK.md first, so the home copy never shadows a
	// project's own instructions.
	if got := guidancePath("grok"); got != filepath.Join(home, ".grok", "GROK.md") {
		t.Fatalf("grok path = %q", got)
	}
	// Cursor has no user-level instructions file, which is why it went without
	// guidance for so long. It reads the shared skills directory.
	if got := guidancePath("cursor"); got != guidancePath("qwen") {
		t.Fatalf("cursor path = %q", got)
	}
}

func TestOwnedGuidanceTargetsAndMarkerBoundaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, harness := range []string{"antigravity", "qwen", "copilot"} {
		r, err := installGuidance(harness, false)
		if err != nil || r.Action != "created" {
			t.Fatalf("%s install = %#v, %v", harness, r, err)
		}
		if r, err = installGuidance(harness, false); err != nil || r.Action != "unchanged" {
			t.Fatalf("%s rerun = %#v, %v", harness, r, err)
		}
		b, _ := os.ReadFile(guidancePath(harness))
		if !strings.Contains(string(b), "name: deja-history") {
			t.Fatalf("%s frontmatter missing: %s", harness, b)
		}
	}
	old := "prose " + guidanceStart + "\nkeep\n" + guidanceEnd + "\n"
	want := old + "\n" + guidanceStart + "\n" + guidanceBody + "\n" + guidanceEnd + "\n"
	got, err := updateGuidanceBlock(old, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("inline markers were replaced: %q", got)
	}
	// grok still shares its file with the user, so it stands in for the
	// marked-block path that qwen took before it moved to a skill.
	shared := guidancePath("grok")
	if err = os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(shared, []byte("# Personal context\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = installGuidance("grok", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(shared)
	if !strings.Contains(string(b), "# Personal context") {
		t.Fatalf("grok context was not preserved: %s", b)
	}
	if _, err = installGuidance("grok", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(shared)
	if strings.Contains(string(b), guidanceStart) || !strings.Contains(string(b), "# Personal context") {
		t.Fatalf("grok uninstall changed personal context: %s", b)
	}

	squat := filepath.Join(home, ".copilot", "skills", "deja-history")
	if err = os.RemoveAll(filepath.Dir(squat)); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(squat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(squat, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = installGuidance("copilot", false); err == nil {
		t.Fatal("expected path-squatting error")
	}
	if _, err = writeIfChanged(filepath.Join(squat, "SKILL.md"), nil, []byte("skill")); err == nil {
		t.Fatal("expected atomic write path-squatting error")
	}
	entries, err := os.ReadDir(filepath.Dir(squat))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(squat) {
		t.Fatalf("temporary file left after failed write: %v", entries)
	}
}

func TestGuidanceAppendPreservesAndRewrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := guidancePath("grok")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Personal rules\n\nkeep this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := installGuidance("grok", false)
	if err != nil || r.Action != "updated" {
		t.Fatalf("install = %#v, %v", r, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "keep this") || strings.Count(string(b), guidanceStart) != 1 {
		t.Fatalf("surrounding content or marker lost: %s", b)
	}
	if r, err = installGuidance("grok", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %#v, %v", r, err)
	}
	if r, err = installGuidance("grok", true); err != nil || r.Action != "updated" {
		t.Fatalf("uninstall = %#v, %v", r, err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), guidanceStart) || !strings.Contains(string(b), "keep this") {
		t.Fatalf("uninstall changed user content: %s", b)
	}
}

func TestClaudeGuidanceIsOwnedAndOptOutWorks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := guidancePath("claude-code")
	r, err := installGuidance("claude-code", false)
	if err != nil || r.Action != "created" || r.Path != path {
		t.Fatalf("install = %#v, %v", r, err)
	}
	if got := guidanceStatus("claude-code"); got != "written" {
		t.Fatalf("guidance status = %q", got)
	}
	if got := guidanceOutput("cursor", installResult{}); got != "cursor: guidance unsupported" {
		t.Fatalf("unsupported output = %q", got)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "deja-history") || !strings.Contains(string(b), "modes of the one deja tool") {
		t.Fatalf("skill content incomplete: %s", b)
	}
	if r, err = installGuidance("claude-code", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %#v, %v", r, err)
	}
	if r, err = installGuidance("claude-code", true); err != nil || r.Action != "removed" {
		t.Fatalf("uninstall = %#v, %v", r, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owned skill still exists, err=%v", err)
	}
}

func TestGuidanceBlockHandlesCRLFAndUnsupportedResult(t *testing.T) {
	old := "# Rules\r\n\r\n" + guidanceStart + "\r\nold\r\n" + guidanceEnd + "\r\n"
	got, err := updateGuidanceBlock(old, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, guidanceStart) != 1 || !strings.Contains(got, "\r\n") || strings.Contains(got, "old") {
		t.Fatalf("CRLF rewrite = %q", got)
	}
	// aider has no guidance location at all — recall reaches it through the
	// read: context file — so it stands in for the unsupported branch.
	if r, err := guidanceResult("aider", false); err != nil || r.Path != "" {
		t.Fatalf("unsupported guidance = %#v, %v", r, err)
	}
}

func TestInstallNoGuidanceOptOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	if err := runInstall(index.DefaultDir(), []string{"codex", "--no-guidance"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("guidance was written despite opt-out, err=%v", err)
	}
}

func TestInstallGuidanceReadFailureSurfaces(t *testing.T) {
	tmp := hermeticEnv(t)
	// Squat the skill directory path with a regular file so reading the
	// skill file fails with a non-NotExist error.
	skills := filepath.Join(tmp, "home", ".claude", "skills")
	if err := os.MkdirAll(filepath.Dir(skills), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skills, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("claude-code", false); err == nil {
		t.Fatal("expected guidance install error when skills path is a file")
	}
}

func TestInstallGuidanceEdgeBranches(t *testing.T) {
	hermeticEnv(t)
	if r, err := installGuidance("nope", false); err != nil || r.Path != "" {
		t.Fatalf("unknown harness = %#v err=%v", r, err)
	}
	if r, err := installGuidance("claude-code", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("uninstall with no skill = %#v err=%v", r, err)
	}
}

func TestCopilotInstallWritesMCPConfig(t *testing.T) {
	tmp := hermeticEnv(t)
	result, err := installTarget("copilot", "/bin/deja", false)
	if err != nil || result.Action != "created" {
		t.Fatalf("copilot MCP install = %#v, %v", result, err)
	}
	b, err := os.ReadFile(filepath.Join(tmp, "home", ".copilot", "mcp-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"deja"`, `"type": "local"`, `"tools"`, `"mcp"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("mcp-config missing %s: %s", want, b)
		}
	}
	// Uninstall removes only our entry.
	if r, err := installTarget("copilot", "/bin/deja", true); err != nil || r.Action == "" {
		t.Fatalf("copilot uninstall = %#v, %v", r, err)
	}
	b, _ = os.ReadFile(filepath.Join(tmp, "home", ".copilot", "mcp-config.json"))
	if strings.Contains(string(b), `"deja"`) {
		t.Fatalf("deja entry not removed: %s", b)
	}
}

func TestInstallGuidanceSkillErrorBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	// path == "" branch for a harness without a guidance location.
	if r, err := installGuidance("aider", false); err != nil || r.Path != "" {
		t.Fatalf("aider guidance = %#v err=%v", r, err)
	}
	// Read failure that is not IsNotExist must surface (copilot skill dir
	// squatted by a file).
	squat := filepath.Join(tmp, "home", ".copilot", "skills")
	if err := os.MkdirAll(filepath.Dir(squat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(squat, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("copilot", false); err == nil {
		t.Fatal("expected copilot read error")
	}
	// Uninstall of a skill whose path parent blocks removal errors out.
	// Windows maps the squatted read to not-exist, which reads as unchanged.
	if runtime.GOOS != "windows" {
		if _, err := installGuidance("copilot", true); err == nil {
			t.Fatal("expected copilot uninstall read error")
		}
	}
	// antigravity skill install + uninstall roundtrip.
	if r, err := installGuidance("antigravity", false); err != nil || r.Action != "created" {
		t.Fatalf("antigravity install = %#v err=%v", r, err)
	}
	if r, err := installGuidance("antigravity", true); err != nil || r.Action != "removed" {
		t.Fatalf("antigravity uninstall = %#v err=%v", r, err)
	}
	if r, err := installGuidance("antigravity", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("antigravity re-uninstall = %#v err=%v", r, err)
	}
}

// Guidance is "written" when deja's block is in the file, not when the file
// exists. Anyone keeping their own AGENTS.md was told deja had written
// guidance there on a machine where install had never run (#637).
func TestGuidanceStatusReadsTheMarkerNotTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	path := guidancePath("grok")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := guidanceStatus("grok"); got != "missing" {
		t.Fatalf("no file at all: got %q", got)
	}
	if err := os.WriteFile(path, []byte("# my own agents file\n\nnothing from deja\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("grok"); got != "absent" {
		t.Fatalf("someone else's file: got %q, want absent", got)
	}
	if err := os.WriteFile(path, []byte("# mine\n\n"+guidanceStart+"\nbody\n"+guidanceEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("grok"); got != "written" {
		t.Fatalf("our block is in it: got %q", got)
	}
}

// A skill file is deja's own, written whole, and carries no marker — so the
// marker check must not apply to it. This is a guard for the carve-out rather
// than a test of the #637 fix: it passes against the old stat-only behaviour
// too, and it exists so that behaviour cannot be restored by accident when
// the marker check moves.
func TestGuidanceStatusForAFileDejaOwnsWhole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	skill := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("claude-code"); got != "missing" {
		t.Fatalf("no skill file: got %q", got)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: deja-history\n---\n\nbody with no marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("claude-code"); got != "written" {
		t.Fatalf("a skill file deja wrote whole: got %q, want written", got)
	}
	// An empty file is an interrupted write, not guidance — the one shape
	// where existence still fails to mean "we wrote it" in a directory deja
	// owns.
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("claude-code"); got != "absent" {
		t.Fatalf("an empty skill file: got %q, want absent", got)
	}
}

// Every harness whose guidance is a file deja writes whole, so none of them
// can be dropped from the helper unnoticed — and the helper must name exactly
// the set the install path branches on, or doctor starts lying again.
func TestGuidanceOwnsWholeFileMatchesTheInstallPath(t *testing.T) {
	for _, h := range []string{"claude-code", "claude", "antigravity", "copilot", "pi", "cursor", "opencode", "gemini", "qwen", "kimi", "roo", "codex", "goose", "openclaw"} {
		if !guidanceOwnsWholeFile(h) {
			t.Errorf("%q is written whole by installGuidance", h)
		}
		// guidanceText branches on the same helper: a whole-file harness gets
		// skill frontmatter, never the marker pair.
		if txt := guidanceText(h); strings.Contains(txt, guidanceStart) {
			t.Errorf("%q got marker text for a file deja owns whole", h)
		}
	}
	for _, h := range []string{"grok"} {
		if guidanceOwnsWholeFile(h) {
			t.Errorf("%q shares its file with the user", h)
		}
		if txt := guidanceText(h); !strings.Contains(txt, guidanceStart) {
			t.Errorf("%q must get a marked block, not a whole file", h)
		}
	}
}

// The path lookup took the raw name while the whole-file check took the
// normalised one, so the function contradicted itself about what it accepts.
func TestGuidanceStatusAcceptsAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, alias := range []string{"opencode-auto", "codex-auto", "claude-auto"} {
		if got := guidanceStatus(alias); got == "unsupported" {
			t.Errorf("%q resolves to a real harness but reported %q", alias, got)
		}
	}
}

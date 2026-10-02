package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI-only install is binary + warmup + search, and nothing else runs. If
// warmup does not leave the skill, the agent on that machine has a working
// index and no instruction to reach for it — which is the whole of #1320.
func TestWarmupWritesTheCLISkill(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "warmup"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(cliSkillPath())
	if err != nil {
		t.Fatalf("warmup left no CLI skill: %v", err)
	}
	for _, want := range []string{"name: deja-search", "deja search --json", "deja blame"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the skill does not teach %q", want)
		}
	}
	// The point of the second skill: it must not send an agent at MCP tools
	// that a CLI-only machine never installed.
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "- recall") || strings.HasPrefix(line, "- how:") {
			t.Errorf("the CLI skill names an MCP tool as a thing to call: %q", line)
		}
	}
}

// Both skills describe the same index, so each has to say when it is the wrong
// door — otherwise an agent holding one of them reaches for an API this machine
// does not have.
func TestTheTwoSkillsPointAtEachOther(t *testing.T) {
	if !strings.Contains(cliSkillBody, "MCP tool") {
		t.Error("the CLI skill never mentions the MCP tool it defers to")
	}
	if !strings.Contains(skillBody, "deja search --json") {
		t.Error("the MCP skill never names the shell path for a session without the tools")
	}
}

// An edited skill is the user's. Every other skill deja writes keeps their
// wording rather than replacing it, and this one is written by warmup, which
// runs far more often than install.
func TestWarmupKeepsAnEditedCLISkill(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "warmup"); err != nil {
		t.Fatal(err)
	}
	mine := "---\nname: deja-search\ndescription: mine\n---\n\nmy own wording\n"
	if err := os.WriteFile(cliSkillPath(), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "warmup")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(cliSkillPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != mine {
		t.Errorf("warmup overwrote a skill the user had edited:\n%s", got)
	}
	// And says nothing about it. warmup's stdout is empty by contract; the
	// "kept your edited skill" line belongs to an install someone is watching,
	// not to every warmup for as long as the edit lives.
	if out != "" {
		t.Errorf("warmup printed on an edited skill: %q", out)
	}
}

// Uninstalling one harness out of several must not take the skill from the
// rest: it describes the CLI, which every one of them can still shell out to.
func TestUninstallKeepsTheCLISkillWhileAnotherTargetStays(t *testing.T) {
	hermeticEnv(t)
	path := wiringStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"targets":["codex","claude-code"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !cliSkillStillWanted([]string{"codex"}) {
		t.Error("removing one of two wired harnesses took the skill from the other")
	}
	if cliSkillStillWanted([]string{"codex", "claude-code"}) {
		t.Error("the last harness left and the skill stayed")
	}
	// A machine with no record at all keeps it: the CLI-only install never
	// wired anything, and that is exactly who this skill is for.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if cliSkillStillWanted(nil) {
		t.Error("an unwired machine counted as wanting the skill for a harness")
	}
}

func TestRemoveCLISkillLeavesOneTheUserRewrote(t *testing.T) {
	hermeticEnv(t)
	if _, err := writeCLISkill(); err != nil {
		t.Fatal(err)
	}
	mine := "my own wording\n"
	if err := os.WriteFile(cliSkillPath(), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeCLISkill(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(cliSkillPath()); err != nil {
		t.Errorf("uninstall deleted a skill the user had rewritten: %v", err)
	}
	// And it does take back the copy it wrote itself. (Writing over the edited
	// one is refused by design, so the edit goes first.)
	if err := os.Remove(cliSkillPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := writeCLISkill(); err != nil {
		t.Fatal(err)
	}
	if err := removeCLISkill(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cliSkillPath()); !os.IsNotExist(err) {
		t.Errorf("uninstall left deja's own copy behind: %v", err)
	}
}

// The copy in the repo is what a reader sees on GitHub and what someone on a
// machine deja cannot write to copies by hand. It has to be the file the binary
// would have written, plus the registry block only ClawHub reads (#4378).
func TestBundledCLISkillMatchesTheInstaller(t *testing.T) {
	got := string(repoFile(t, filepath.Join("skills", "deja-search", "SKILL.md")))
	if got != cliSkillRegistryFile() {
		t.Errorf("skills/deja-search/SKILL.md has drifted from cliSkillRegistryFile:\n--- file ---\n%s\n--- installer ---\n%s", got, cliSkillRegistryFile())
	}
	if strings.Replace(got, "\n"+cliSkillMeta, "", 1) != cliSkillFile() {
		t.Error("the registry copy differs from the installed skill by more than its metadata")
	}
}

// The Agent Skills spec types `metadata` as string keys to string values, and a
// reader that decodes it that way drops the whole skill when a value is a
// nested map: Crush 0.97.1 rejected deja-search on every start with "cannot
// unmarshal !!map into string" (#4378). Every skill deja writes into a shared
// skills directory has to be one such a reader accepts.
func TestInstalledSkillsKeepMetadataFlat(t *testing.T) {
	for name, file := range map[string]string{
		"deja-search":  cliSkillFile(),
		"deja-history": skillFile(""),
	} {
		parts := strings.SplitN(file, "---\n", 3)
		if len(parts) != 3 || parts[0] != "" {
			t.Fatalf("%s: no frontmatter:\n%s", name, file)
		}
		inMeta := false
		for _, line := range strings.Split(strings.TrimSuffix(parts[1], "\n"), "\n") {
			switch {
			case line == "metadata:":
				inMeta = true
			case !strings.HasPrefix(line, " "):
				inMeta = false
				if strings.HasPrefix(line, "metadata:") {
					t.Errorf("%s: metadata is not a block of string values: %q", name, line)
				}
			case inMeta:
				k, v, ok := strings.Cut(strings.TrimPrefix(line, "  "), ": ")
				if !ok || strings.HasPrefix(line, "   ") || strings.Contains(k, " ") || v == "" || strings.ContainsAny(v[:1], "{[|>") {
					t.Errorf("%s: metadata value is not a string: %q", name, line)
				}
			}
		}
	}
}

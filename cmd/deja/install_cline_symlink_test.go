package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func symlinkClineTest(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(link), err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func requireClineTestLink(t *testing.T, link, target string) {
	t.Helper()
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("%s is not a symlink: %v", link, err)
	}
	if got != target {
		t.Fatalf("%s points to %q, want %q", link, got, target)
	}
}

func requireClineTestExists(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("%s should still exist: %v", path, err)
		}
	}
}

func installClineAutoForTest(t *testing.T, extra ...string) (dir, entry, manifest, skill string) {
	t.Helper()
	t.Setenv("CLINE_MCP_SETTINGS_PATH", "")
	dir, entry, manifest, skill = clineTestPaths(t)
	args := append([]string{"install", "cline-auto", "--no-index"}, extra...)
	if out, err := captureRun(t, args...); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	return dir, entry, manifest, skill
}

func replaceClineTestDirWithLink(t *testing.T, path, target string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("remove %s: %v", path, err)
	}
	symlinkClineTest(t, target, path)
}

// A plugin root that is a symlink must not be followed: the files behind it
// belong to whatever tree the link names, not to deja's plugin directory.
func TestClineUninstallRefusesASymlinkedPluginRoot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dangling bool
	}{
		{"directory", false},
		{"dangling", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			external := filepath.Join(t.TempDir(), "external-deja")
			files := map[string]string{
				filepath.Join(external, "index.js"):                           "external plugin\n",
				filepath.Join(external, "package.json"):                       "external manifest\n",
				filepath.Join(external, "skills", "deja-history", "SKILL.md"): "external skill\n",
				filepath.Join(external, "notes", "todo.md"):                   "mine\n",
			}
			if !tc.dangling {
				for path, body := range files {
					writeClineTestFile(t, path, body)
				}
			}
			plugins := sources.ClinePluginsDir()
			if err := os.MkdirAll(plugins, 0o755); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(plugins, "deja")
			symlinkClineTest(t, external, dir)

			_, err := installClineAuto("/bin/deja", true)
			if err == nil {
				t.Fatal("uninstall followed a symlinked plugin root")
			}
			if !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "symbolic link") {
				t.Errorf("error does not name the root link: %v", err)
			}
			requireClineTestLink(t, dir, external)
			if tc.dangling {
				if _, statErr := os.Lstat(external); !os.IsNotExist(statErr) {
					t.Errorf("dangling root link created or changed its target: %v", statErr)
				}
			} else {
				for path, body := range files {
					requireClineTestFile(t, path, body)
				}
			}
			if _, statErr := os.Stat(plugins); statErr != nil {
				t.Errorf("plugin parent was cleaned through a root link: %v", statErr)
			}
		})
	}
}

// A symlinked skills/ is a reader layout: remove the two files beside it, keep
// the link and the skill target, and say what was kept.
func TestClineUninstallKeepsASymlinkedSkillsDir(t *testing.T) {
	hermeticEnv(t)
	dir, entry, manifest, skill := installClineAutoForTest(t, "--no-guidance")
	skills := filepath.Join(dir, "skills")
	external := filepath.Join(t.TempDir(), "external-skills")
	externalSkill := filepath.Join(external, "deja-history", "SKILL.md")
	extra := filepath.Join(external, "deja-history", "notes.md")
	writeClineTestFile(t, externalSkill, "a skill the reader wrote\n")
	writeClineTestFile(t, extra, "reader notes\n")
	replaceClineTestDirWithLink(t, skills, external)

	// Both names share one guidance harness, so uninstall them together: the
	// default guidance phase runs for each and must not cross the link either.
	out, err := captureRun(t, "uninstall", "cline", "cline-auto", "--no-index")
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	requireClineTestGone(t, entry, manifest)
	requireClineTestLink(t, skills, external)
	requireClineTestFile(t, externalSkill, "a skill the reader wrote\n")
	requireClineTestFile(t, extra, "reader notes\n")
	for _, want := range []string{"kept", shortHome(skill), shortHome(skills), "symbolic link"} {
		if !strings.Contains(out, want) {
			t.Errorf("uninstall did not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "removed "+shortHome(skill)) {
		t.Errorf("uninstall claimed the linked skill was removed:\n%s", out)
	}
}

// The same boundary one level deeper: skills/ is real and deja-history is the
// link.
func TestClineUninstallKeepsASymlinkedSkillDir(t *testing.T) {
	hermeticEnv(t)
	dir, entry, manifest, skill := installClineAutoForTest(t, "--no-guidance")
	skillDir := filepath.Join(dir, "skills", "deja-history")
	external := filepath.Join(t.TempDir(), "external-skill")
	externalSkill := filepath.Join(external, "SKILL.md")
	extra := filepath.Join(external, "notes.md")
	writeClineTestFile(t, externalSkill, "a skill the reader wrote\n")
	writeClineTestFile(t, extra, "reader notes\n")
	replaceClineTestDirWithLink(t, skillDir, external)

	out, err := captureRun(t, "uninstall", "cline-auto", "--no-index")
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	requireClineTestGone(t, entry, manifest)
	requireClineTestLink(t, skillDir, external)
	requireClineTestFile(t, externalSkill, "a skill the reader wrote\n")
	requireClineTestFile(t, extra, "reader notes\n")
	requireClineTestExists(t, filepath.Join(dir, "skills"))
	for _, want := range []string{"kept", shortHome(skill), shortHome(skillDir), "symbolic link"} {
		if !strings.Contains(out, want) {
			t.Errorf("uninstall did not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "removed "+shortHome(skill)) {
		t.Errorf("uninstall claimed the linked skill was removed:\n%s", out)
	}
}

// The old pruning bug was not only in file removal: isRealDir follows a link
// in an intermediate component, so an emptied deja-history behind a linked
// skills/ could be rmdir'd. The external empty directory must stay.
func TestClineUninstallDoesNotPruneThroughASymlinkedSkillsDir(t *testing.T) {
	hermeticEnv(t)
	dir, entry, manifest, _ := installClineAutoForTest(t, "--no-guidance")
	skills := filepath.Join(dir, "skills")
	external := filepath.Join(t.TempDir(), "external-skills")
	externalSkillDir := filepath.Join(external, "deja-history")
	if err := os.MkdirAll(externalSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	replaceClineTestDirWithLink(t, skills, external)

	out, err := captureRun(t, "uninstall", "cline-auto", "--no-index")
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	requireClineTestGone(t, entry, manifest)
	requireClineTestLink(t, skills, external)
	entries, err := os.ReadDir(externalSkillDir)
	if err != nil {
		t.Fatalf("the external empty directory was removed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("the external directory is no longer empty: %v", entries)
	}
}

// A directory at a file path is the reader's, even when empty: os.Remove would
// rmdir it, so the type check has to come first.
func TestClineUninstallReportsADirectoryAtAFilePath(t *testing.T) {
	for _, tc := range []struct {
		name string
		rel  string
	}{
		{"entry", "index.js"},
		{"manifest", "package.json"},
		{"skill", "skills/deja-history/SKILL.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			dir, entry, manifest, skill := clineTestPaths(t)
			if _, err := installClineAuto("/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			user := filepath.Join(dir, "notes", "todo.md")
			writeClineTestFile(t, user, "mine\n")
			target := filepath.Join(dir, filepath.FromSlash(tc.rel))
			if err := os.Remove(target); err != nil {
				t.Fatalf("remove %s: %v", target, err)
			}
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", target, err)
			}

			_, err := installClineAuto("/bin/deja", true)
			if err == nil {
				t.Fatal("uninstall removed an empty directory at a file path")
			}
			if !strings.Contains(err.Error(), target) || !strings.Contains(err.Error(), "directory") {
				t.Errorf("error does not name the directory at %s: %v", target, err)
			}
			fi, statErr := os.Stat(target)
			if statErr != nil || !fi.IsDir() {
				t.Errorf("the user's empty directory at %s was removed: %v", target, statErr)
			}
			requireClineTestFile(t, user, "mine\n")
			for _, path := range []string{entry, manifest, skill} {
				if path != target {
					requireClineTestExists(t, path)
				}
			}
		})
	}
}

// A symlink at a file path, including a dangling one, is kept and named; the
// other files are not deleted before the conflict is reported.
func TestClineUninstallReportsAFileSymlink(t *testing.T) {
	for _, tc := range []struct {
		name string
		rel  string
	}{
		{"entry", "index.js"},
		{"manifest", "package.json"},
		{"skill", "skills/deja-history/SKILL.md"},
	} {
		for _, dangling := range []bool{false, true} {
			name := tc.name
			if dangling {
				name += " dangling"
			}
			t.Run(name, func(t *testing.T) {
				hermeticEnv(t)
				dir, entry, manifest, skill := clineTestPaths(t)
				if _, err := installClineAuto("/bin/deja", false); err != nil {
					t.Fatalf("install: %v", err)
				}
				targetPath := filepath.Join(dir, filepath.FromSlash(tc.rel))
				external := filepath.Join(t.TempDir(), "external", filepath.Base(targetPath))
				if !dangling {
					writeClineTestFile(t, external, "external content\n")
				}
				if err := os.Remove(targetPath); err != nil {
					t.Fatalf("remove %s: %v", targetPath, err)
				}
				symlinkClineTest(t, external, targetPath)

				_, err := installClineAuto("/bin/deja", true)
				if err == nil {
					t.Fatal("uninstall removed a symlink at a file path")
				}
				if !strings.Contains(err.Error(), targetPath) || !strings.Contains(err.Error(), "symbolic link") {
					t.Errorf("error does not name the file symlink %s: %v", targetPath, err)
				}
				requireClineTestLink(t, targetPath, external)
				if !dangling {
					requireClineTestFile(t, external, "external content\n")
				}
				for _, path := range []string{entry, manifest, skill} {
					if path != targetPath {
						requireClineTestExists(t, path)
					}
				}
			})
		}
	}
}

// A dangling link inside the tree is a boundary to keep, not a missing file:
// the safe files still come out, and the result says why the skill stayed.
func TestClineUninstallKeepsAnInternalDanglingLink(t *testing.T) {
	for _, tc := range []struct {
		name string
		rel  string
	}{
		{"skills", "skills"},
		{"skill directory", "skills/deja-history"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			dir, entry, manifest, skill := clineTestPaths(t)
			if _, err := installClineAuto("/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			link := filepath.Join(dir, filepath.FromSlash(tc.rel))
			target := filepath.Join(t.TempDir(), "missing-target")
			replaceClineTestDirWithLink(t, link, target)

			res, err := installClineAuto("/bin/deja", true)
			if err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if res.Action != "removed" {
				t.Fatalf("uninstall action = %q, want removed", res.Action)
			}
			requireClineTestGone(t, entry, manifest)
			requireClineTestLink(t, link, target)
			if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
				t.Errorf("a dangling internal link created its target: %v", statErr)
			}
			for _, want := range []string{"kept", shortHome(skill), shortHome(link), "symbolic link"} {
				if !strings.Contains(res.Note, want) {
					t.Errorf("result note does not say %q: %q", want, res.Note)
				}
			}
		})
	}
}

// With both removable files gone and the skill behind a link, the CLI must say
// what was kept rather than "not installed", on the first and a repeated run.
func TestClineUninstallOfAnInternalLinkWithMissingFilesIsExplained(t *testing.T) {
	hermeticEnv(t)
	dir, entry, manifest, skill := installClineAutoForTest(t, "--no-guidance")
	skills := filepath.Join(dir, "skills")
	external := filepath.Join(t.TempDir(), "external-skills")
	externalSkill := filepath.Join(external, "deja-history", "SKILL.md")
	writeClineTestFile(t, externalSkill, "reader skill\n")
	replaceClineTestDirWithLink(t, skills, external)
	for _, path := range []string{entry, manifest} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove %s: %v", path, err)
		}
	}

	for i := 0; i < 2; i++ {
		out, err := captureRun(t, "uninstall", "cline-auto", "--no-index")
		if err != nil {
			t.Fatalf("uninstall %d: %v\n%s", i+1, err, out)
		}
		if strings.Contains(out, "cline-auto: not installed") {
			t.Errorf("run %d hid the kept skill behind 'not installed':\n%s", i+1, out)
		}
		for _, want := range []string{"kept", shortHome(skill), shortHome(skills), "symbolic link"} {
			if !strings.Contains(out, want) {
				t.Errorf("run %d did not say %q:\n%s", i+1, want, out)
			}
		}
		requireClineTestLink(t, skills, external)
		requireClineTestFile(t, externalSkill, "reader skill\n")
	}
}

// A link above the plugin root is a normal layout for CLINE_DIR or plugins/.
// It is followed; only the tree deja writes is bounded.
func TestClineUninstallFollowsALegitimateUpperSymlink(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) (clineDir, link, target string)
	}{
		{"CLINE_DIR", func(t *testing.T) (string, string, string) {
			real := filepath.Join(t.TempDir(), "cline-real")
			link := filepath.Join(t.TempDir(), "cline-link")
			if err := os.MkdirAll(real, 0o755); err != nil {
				t.Fatal(err)
			}
			symlinkClineTest(t, real, link)
			return link, link, real
		}},
		{"plugins", func(t *testing.T) (string, string, string) {
			root := t.TempDir()
			external := filepath.Join(t.TempDir(), "external-plugins")
			if err := os.MkdirAll(external, 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "plugins")
			symlinkClineTest(t, external, link)
			return root, link, external
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			clineDir, link, target := tc.setup(t)
			t.Setenv("CLINE_DIR", clineDir)
			dir, entry, manifest, skill := clineTestPaths(t)
			if _, err := installClineAuto("/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			if _, err := installClineAuto("/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			requireClineTestGone(t, entry, manifest, skill)
			requireClineTestLink(t, link, target)
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("plugin directory survived an uninstall through a legitimate upper link: %v", err)
			}
		})
	}
}

// A missing skills/ tree is still a normal uninstall of the two files that are
// there, and the recorded empty plugin directory goes too.
func TestClineUninstallWithoutASkillsTree(t *testing.T) {
	hermeticEnv(t)
	dir, entry, manifest, _ := clineTestPaths(t)
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "skills")); err != nil {
		t.Fatal(err)
	}
	res, err := installClineAuto("/bin/deja", true)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.Action != "removed" {
		t.Fatalf("uninstall action = %q, want removed", res.Action)
	}
	requireClineTestGone(t, entry, manifest)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("recorded plugin directory survived without a skills tree: %v", err)
	}
}

// A component that should be a directory but is a regular file is a real
// error, not a missing skill directory, and nothing is deleted first.
func TestClineUninstallReportsANonDirectoryComponent(t *testing.T) {
	for _, rel := range []string{"skills", "skills/deja-history"} {
		t.Run(rel, func(t *testing.T) {
			hermeticEnv(t)
			dir, entry, manifest, _ := clineTestPaths(t)
			if _, err := installClineAuto("/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			component := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.RemoveAll(component); err != nil {
				t.Fatal(err)
			}
			writeClineTestFile(t, component, "not a directory\n")

			_, err := installClineAuto("/bin/deja", true)
			if err == nil {
				t.Fatal("uninstall accepted a regular file where a directory belongs")
			}
			if !strings.Contains(err.Error(), component) || !strings.Contains(err.Error(), "not a directory") {
				t.Errorf("error does not name the non-directory component %s: %v", component, err)
			}
			requireClineTestFile(t, component, "not a directory\n")
			requireClineTestExists(t, entry, manifest)
		})
	}
}

// The plugin root itself has the same type rule: a regular file where deja's
// directory belongs is a conflict, not an empty plugin.
func TestClineUninstallReportsAPluginRootThatIsNotADirectory(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(sources.ClinePluginsDir(), "deja")
	writeClineTestFile(t, dir, "not a directory\n")

	_, err := installClineAuto("/bin/deja", true)
	if err == nil {
		t.Fatal("uninstall accepted a regular file as the plugin root")
	}
	if !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error does not name the non-directory plugin root %s: %v", dir, err)
	}
	requireClineTestFile(t, dir, "not a directory\n")
}

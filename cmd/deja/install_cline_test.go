package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestInstallClineWritesAPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Plugins sit beside the data directory, not inside it: a plugin under
	// ~/.cline/data/plugins is never discovered.
	path := filepath.Join(home, ".cline", "plugins", "deja", "index.js")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plugin not written where cline looks: %v", err)
	}
	js := string(b)
	// The registry validates the manifest before setup runs and rejects
	// contributions that were not declared.
	for _, want := range []string{`capabilities: ["rules", "commands", "skills", "hooks"]`, "registerRule", "registerCommand"} {
		if !strings.Contains(js, want) {
			t.Fatalf("plugin missing %q:\n%s", want, js)
		}
	}
	// A string here would freeze whatever history existed at install time.
	// Only the function form is resolved per session.
	if !strings.Contains(js, "content: () =>") {
		t.Fatalf("rule content is not lazy, recall would never update:\n%s", js)
	}
	if !strings.Contains(js, `hook-context`) {
		t.Fatalf("plugin does not call the context hook:\n%s", js)
	}
}

// Cline hooks look like the obvious channel and are not one: their stdout is
// closed and no hook output can carry context. Writing one would leave a dead
// integration that looks installed.
func TestInstallClineWritesNoHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, dir := range []string{
		filepath.Join(home, ".cline", "hooks"),
		filepath.Join(home, "Documents", "Cline", "Hooks"),
	} {
		if _, err := os.Stat(dir); err == nil {
			t.Fatalf("install created %s; cline hook output is discarded", dir)
		}
	}
}

func TestInstallClineIsIdempotentAndRemovable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	first, err := installClineAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if first.Action != "created" {
		t.Fatalf("first install action = %q", first.Action)
	}
	again, err := installClineAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if again.Action != "unchanged" {
		t.Fatalf("reinstall rewrote the plugin: %q", again.Action)
	}
	if _, err := installClineAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cline", "plugins", "deja")); !os.IsNotExist(err) {
		t.Fatalf("plugin survived uninstall: %v", err)
	}
	second, err := installClineAuto("/bin/deja", true)
	if err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
	if second.Action != "unchanged" {
		t.Fatalf("second uninstall action = %q, want unchanged", second.Action)
	}
}

// Cline loads a plugin's skills/ and takes its name only from a package.json
// whose cline.plugins lists the entry file. Without one the deja-history skill
// was never loaded and Settings > Plugins listed the plugin as "index" (#4316).
func TestInstallClineWritesAPackageManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(home, ".cline", "plugins", "deja")
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatalf("no package.json beside the plugin: %v", err)
	}
	var pkg struct {
		Name  string `json:"name"`
		Cline struct {
			Plugins []string `json:"plugins"`
		} `json:"cline"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatalf("package.json: %v\n%s", err, b)
	}
	if pkg.Name != "deja" || len(pkg.Cline.Plugins) != 1 || pkg.Cline.Plugins[0] != "./index.js" {
		t.Fatalf("package.json does not name the plugin entry: %s", b)
	}
	again, err := installClineAuto("/bin/deja", false)
	if err != nil || again.Action != "unchanged" {
		t.Fatalf("reinstall: %q %v", again.Action, err)
	}
	if _, err := installClineAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plugin directory survived uninstall: %v", err)
	}
}

// The skill install writes for Cline is guidance, and doctor said
// "unsupported" about it before and after install (#4317).
func TestGuidanceStatusSeesTheClineSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	if got := guidanceStatus("cline"); got != "missing" {
		t.Fatalf("before install: guidance %q, want missing", got)
	}
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, h := range []string{"cline", "cline-auto"} {
		if got := guidanceStatus(h); got != "written" {
			t.Fatalf("after install: %s guidance %q, want written", h, got)
		}
	}
}

// clineTestPaths names the three files install writes, so a test says which one
// survived rather than repeating the layout.
func clineTestPaths(t *testing.T) (dir, entry, manifest, skill string) {
	t.Helper()
	dir = filepath.Join(sources.ClinePluginsDir(), "deja")
	entry = filepath.Join(dir, "index.js")
	manifest = filepath.Join(dir, "package.json")
	skill = filepath.Join(dir, "skills", "deja-history", "SKILL.md")
	return dir, entry, manifest, skill
}

func writeClineTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func requireClineTestFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func requireClineTestGone(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived uninstall (stat: %v)", path, err)
		}
	}
}

func requireClineTestEmptyDir(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s is not empty: %v", path, entries)
	}
}

// resetClineTestRunRecord stands in for a new deja process: what the install
// run made is known from wiring.json, not from this process's memory.
func resetClineTestRunRecord() {
	createdDirsByThisRun, createdByThisRun, snapshotsByThisRun = nil, nil, nil
	blocksAddedThisRun, blocksForgottenThisRun = nil, map[string]bool{}
}

// Uninstall used to RemoveAll the plugin directory, so a note or any other
// file the reader kept beside deja's three went with it (#5300).
func TestClineUninstallKeepsFilesBesideThePlugin(t *testing.T) {
	hermeticEnv(t)
	dir, entry, manifest, skill := clineTestPaths(t)
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	files := map[string]string{
		filepath.Join(dir, "notes", "todo.md"):                       "mine\n",
		filepath.Join(dir, "mine.txt"):                               "a file only the reader wrote\n",
		filepath.Join(dir, "index.js.bak"):                           "an older plugin\n",
		filepath.Join(dir, "skills", "deja-history", "SKILL.md.bak"): "the reader's copy\n",
	}
	for path, body := range files {
		writeClineTestFile(t, path, body)
	}
	res, err := installClineAuto("/bin/deja", true)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.Action != "removed" {
		t.Fatalf("uninstall action = %q, want removed", res.Action)
	}
	requireClineTestGone(t, entry, manifest, skill)
	for path, body := range files {
		requireClineTestFile(t, path, body)
	}
}

// Another skill and extra files inside deja-history are the reader's: only the
// SKILL.md deja wrote goes, and the directories that still hold something stay.
func TestClineUninstallKeepsExtraSkills(t *testing.T) {
	hermeticEnv(t)
	dir, entry, manifest, skill := clineTestPaths(t)
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	kept := map[string]string{
		filepath.Join(dir, "skills", "deja-history", "notes.md"): "extra in deja-history\n",
		filepath.Join(dir, "skills", "mine", "SKILL.md"):         "---\nname: mine\n---\n",
	}
	for path, body := range kept {
		writeClineTestFile(t, path, body)
	}
	if _, err := installClineAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	requireClineTestGone(t, entry, manifest, skill)
	for path, body := range kept {
		requireClineTestFile(t, path, body)
	}
}

// A directory the reader had before install is theirs even when uninstall
// leaves it empty; only the empty tree deja recorded making is pruned (#3239).
func TestClineUninstallKeepsDirectoriesItDidNotCreate(t *testing.T) {
	t.Run("plugin directory", func(t *testing.T) {
		hermeticEnv(t)
		dir, entry, manifest, skill := clineTestPaths(t)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := installClineAuto("/bin/deja", false); err != nil {
			t.Fatalf("install: %v", err)
		}
		if _, err := installClineAuto("/bin/deja", true); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
		requireClineTestGone(t, entry, manifest, skill)
		requireClineTestEmptyDir(t, dir)
	})

	t.Run("skill directory", func(t *testing.T) {
		hermeticEnv(t)
		dir, entry, manifest, skill := clineTestPaths(t)
		skillDir := filepath.Dir(skill)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := installClineAuto("/bin/deja", false); err != nil {
			t.Fatalf("install: %v", err)
		}
		if _, err := installClineAuto("/bin/deja", true); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
		requireClineTestGone(t, entry, manifest, skill)
		for _, path := range []string{dir, filepath.Dir(skillDir), skillDir} {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("pre-created directory %s was removed: %v", path, err)
			}
		}
		requireClineTestEmptyDir(t, skillDir)
	})

	// Control: the same uninstall does take back the tree deja recorded.
	t.Run("installed tree", func(t *testing.T) {
		hermeticEnv(t)
		dir, entry, manifest, skill := clineTestPaths(t)
		if _, err := installClineAuto("/bin/deja", false); err != nil {
			t.Fatalf("install: %v", err)
		}
		if _, err := installClineAuto("/bin/deja", true); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
		requireClineTestGone(t, entry, manifest, skill)
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("recorded plugin directory survived uninstall: %v", err)
		}
	})
}

// A partial install, or a second uninstall, still removes whichever of the
// three files are there and never claims one that was already missing.
func TestClineUninstallHandlesMissingPluginFiles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing []string
	}{
		{"entry", []string{"index"}},
		{"manifest", []string{"package"}},
		{"skill", []string{"skill"}},
		{"all three", []string{"index", "package", "skill"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			dir, entry, manifest, skill := clineTestPaths(t)
			if _, err := installClineAuto("/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			user := filepath.Join(dir, "notes", "todo.md")
			writeClineTestFile(t, user, "mine\n")
			paths := map[string]string{"index": entry, "package": manifest, "skill": skill}
			missing := map[string]bool{}
			for _, name := range tc.missing {
				missing[name] = true
				if err := os.Remove(paths[name]); err != nil {
					t.Fatalf("remove %s: %v", paths[name], err)
				}
			}
			res, err := installClineAuto("/bin/deja", true)
			if err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			for name, path := range paths {
				if !missing[name] {
					requireClineTestGone(t, path)
				}
			}
			requireClineTestFile(t, user, "mine\n")
			if len(tc.missing) == len(paths) {
				if res.Action != "unchanged" {
					t.Fatalf("uninstall of nothing action = %q, want unchanged", res.Action)
				}
				if res.Path != entry {
					t.Errorf("uninstall of nothing names %s, want the entry %s", res.Path, entry)
				}
				return
			}
			if res.Action != "removed" {
				t.Fatalf("uninstall action = %q, want removed", res.Action)
			}
			if missing["index"] && res.Path == entry {
				t.Errorf("uninstall claimed the missing entry was removed: %s", res.Path)
			}
		})
	}
}

// CLINE_DIR moves the plugin directory, and an uninstall there must not touch
// the default location.
func TestClineUninstallHonorsCLINE_DIR(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	custom := filepath.Join(t.TempDir(), "cline")
	t.Setenv("CLINE_DIR", custom)
	dir, entry, manifest, skill := clineTestPaths(t)
	if _, err := installClineAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	user := filepath.Join(dir, "notes", "todo.md")
	writeClineTestFile(t, user, "mine\n")
	sentinel := filepath.Join(home, ".cline", "plugins", "deja", "index.js")
	writeClineTestFile(t, sentinel, "not deja's\n")
	if _, err := installClineAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	requireClineTestGone(t, entry, manifest, skill)
	requireClineTestFile(t, user, "mine\n")
	requireClineTestFile(t, sentinel, "not deja's\n")
}

// The install run's in-memory record is gone by the time a later process
// uninstalls; wiring.json still has to vouch for the empty directories. The
// MCP uninstall keeps the reader's own server (#5300).
func TestClineAutoUninstallUsesThePersistedDirectoryRecord(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("CLINE_MCP_SETTINGS_PATH", "")
	mcp := sources.ClineMCPSettingsPath()
	seed := []byte(`{"mcpServers": {"zz-mine": {"command": "my-server"}}}` + "\n")
	if err := os.MkdirAll(filepath.Dir(mcp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcp, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "cline-auto", "--no-index", "--no-guidance")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	dir, entry, manifest, skill := clineTestPaths(t)
	for _, path := range []string{entry, manifest, skill} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("install did not write %s: %v", path, err)
		}
	}
	resetClineTestRunRecord()
	out, err = captureRun(t, "uninstall", "cline-auto", "--no-index", "--no-guidance")
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if strings.Contains(out, "refused") {
		t.Errorf("uninstall over the persisted record reported a refusal:\n%s", out)
	}
	requireClineTestGone(t, entry, manifest, skill)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("plugin directory survived an uninstall with a persisted record: %v", err)
	}
	b, err := os.ReadFile(mcp)
	if err != nil {
		t.Fatalf("read %s: %v", mcp, err)
	}
	var root struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("settings %s: %v\n%s", mcp, err, b)
	}
	if _, ok := root.MCPServers["zz-mine"]; !ok {
		t.Errorf("uninstall removed the reader's own MCP server:\n%s", b)
	}
	if _, ok := root.MCPServers["deja"]; ok {
		t.Errorf("uninstall left deja's MCP entry:\n%s", b)
	}
}

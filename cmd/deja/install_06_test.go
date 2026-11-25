package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Claude Code rewrites ~/.claude.json while it runs, and `deja install` is
// often run from inside it. A save that landed between deja's read and its
// rename was put back by the rename: 31 and 116 of 200 runs lost the client's
// change in the hunt's race test (#4561). The edit is redone on the client's
// file now.
func TestInstallKeepsAClientSaveMadeDuringTheEdit(t *testing.T) {
	hermeticEnv(t)
	p := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{\n  \"numStartups\": 0\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := false
	beforeConfigReplace = func(path string) {
		if saved || filepath.Base(path) != filepath.Base(p) {
			return
		}
		saved = true
		// The client's save: read, change, temp file and rename.
		b, _ := os.ReadFile(p)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["numStartups"] = 1
		nb, jerr := json.MarshalIndent(m, "", "  ")
		if jerr != nil {
			t.Fatal(jerr)
		}
		tmp := p + ".client-tmp"
		if err := os.WriteFile(tmp, nb, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, p); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { beforeConfigReplace = func(string) {} })
	if _, err := captureRun(t, "install", "claude-code", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if !saved {
		t.Fatal("the client never saved; the test did not race anything")
	}
	b, _ := os.ReadFile(p)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["numStartups"] != float64(1) {
		t.Errorf("the client's save was lost:\n%s", b)
	}
	if servers, _ := m["mcpServers"].(map[string]any); servers["deja"] == nil {
		t.Errorf("deja's entry is missing:\n%s", b)
	}
}

// A config the reader made read-only was rewritten anyway: the temp file is
// renamed over it, which the file's own mode never stops, and the result kept
// 0444 (#4558). Refused now, and the file is left as it was.
func TestInstallRefusesAReadOnlyConfig(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file mode is not enforced here")
	}
	for _, c := range []struct {
		target, seed string
		path         func() string
	}{
		{"cursor", "{\n  \"mcpServers\": {}\n}\n", func() string { return filepath.Join(sources.CursorCLIHome(), "mcp.json") }},
		{"codex", "model = \"o3\"\n", func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"hermes", "model: hermes-3\n", func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o444); err != nil {
				t.Fatal(err)
			}
			_, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance")
			if err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Errorf("install did not refuse the read-only config: %v", err)
			}
			if got, _ := os.ReadFile(p); string(got) != c.seed {
				t.Errorf("a read-only config was rewritten:\n%s", got)
			}
			if _, err := os.Stat(p + ".bak"); err == nil {
				t.Errorf("a snapshot was taken of a config deja did not change")
			}
		})
	}
}

// A target that edits two files refuses on the read-only one before writing
// the other.
func TestAutoTargetWithAReadOnlyHooksFileWritesNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file mode is not enforced here")
	}
	hermeticEnv(t)
	hooks := filepath.Join(sources.CodexHome(), "hooks.json")
	cfg := filepath.Join(sources.CodexHome(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(hooks), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks, []byte("{}\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	const seed = "model = \"o3\"\n"
	if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "codex-auto", "--no-index", "--no-guidance"); err == nil {
		t.Fatal("install wrote through a read-only hooks.json")
	}
	if got, _ := os.ReadFile(cfg); string(got) != seed {
		t.Errorf("a refused target wrote config.toml:\n%s", got)
	}
}

// hermes-auto on a read-only config.yaml whose MCP entry is already right:
// the MCP step has nothing to write, so the refusal came from plugins.enabled
// after the plugin files were already in.
func TestHermesAutoOnAReadOnlyConfigWritesNoPluginFiles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file mode is not enforced here")
	}
	hermeticEnv(t)
	cfg := filepath.Join(sources.HermesHome(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("model: hermes-3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "hermes", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o444); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "install", "hermes-auto", "--no-index", "--no-guidance")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("install did not refuse the read-only config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sources.HermesHome(), "plugins", "deja")); err == nil {
		t.Errorf("a refused install wrote the plugin files")
	}
}

// A read-only config directory was reported as a permission error on
// ~/.codex/.deja-tmp-4168817699 — the scratch file writeIfChanged creates to
// write atomically. The reader cannot look at it, chmod it, or find it (#1686).
func TestInstallNamesTheConfigNotTheTempFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the directory mode is not enforced here")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No config to back up: with one present the backup step fails first and
	// names a real path, and the temp name never surfaces.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := captureRun(t, "install", "codex")
	if err == nil {
		t.Fatal("install wrote into a read-only directory")
	}
	msg := err.Error()
	if strings.Contains(msg, "deja-tmp") {
		t.Errorf("the refusal names deja's scratch file: %s", msg)
	}
	if !strings.Contains(msg, filepath.Join(".codex", "config.toml")) {
		t.Errorf("the refusal does not name the config it could not write: %s", msg)
	}
	// The remedy still has to read as a permissions problem, which means the
	// error must stay an fs.ErrPermission after the path is rewritten.
	if !strings.Contains(msg, "permissions") {
		t.Errorf("a permission denial lost its remedy: %s", msg)
	}
}

// writeIfChanged's own error, checked directly: the path is the config, and
// errors.Is still sees the permission underneath.
func TestWriteIfChangedReportsTheDestination(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the directory mode is not enforced here")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	path := filepath.Join(dir, "config.toml")
	_, err := writeIfChanged(path, nil, []byte("x\n"))
	if err == nil {
		t.Fatal("writeIfChanged wrote into a read-only directory")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error names %q, not the destination %q", err, path)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("error is no longer a permission error: %v", err)
	}
}

func TestInstallReasonixLeavesAForeignDejaPackage(t *testing.T) {
	home := reasonixTestHome(t, false)
	foreign := `{"apiVersion":"reasonix.io/plugin/v2","name":"deja","mcpServers":{"deja":{"type":"stdio","command":"node","args":["deja-notes.js"]}}}`
	writeTestFile(t, filepath.Join(home, "plugins", "deja", "reasonix-plugin.json"), foreign)
	before := treeOf(t, home)
	if _, err := installReasonix("/bin/deja", false, true); err == nil || !strings.Contains(err.Error(), "did not write") {
		t.Fatalf("install over a foreign package = %v, want a refusal", err)
	}
	if res, err := installReasonix("/bin/deja", true, true); err != nil || res.Action != "unchanged" {
		t.Fatalf("uninstall of a foreign package = %+v %v, want unchanged", res, err)
	}
	sameTree(t, "the Reasonix home with someone else's deja plugin", before, treeOf(t, home))
}

// A plugin named deja that someone installed with --link: plugins/deja is a
// link and the record points at its own source. Neither is deja's.
func TestUninstallReasonixLeavesALinkedForeignDeja(t *testing.T) {
	home := reasonixTestHome(t, false)
	src := t.TempDir()
	writeTestFile(t, filepath.Join(src, "reasonix-plugin.json"), `{"apiVersion":"reasonix.io/plugin/v2","name":"deja","mcpServers":{"deja":{"type":"stdio","command":"node","args":["notes.js"]}}}`)
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(home, "plugins", "deja")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	writeTestFile(t, filepath.Join(home, "plugin-packages.json"), "{\n  \"version\": 1,\n  \"plugins\": [\n    {\n      \"name\": \"deja\",\n      \"source\": \"x\",\n      \"root\": \"/elsewhere\",\n      \"manifestKind\": \"reasonix\",\n      \"enabled\": true\n    }\n  ]\n}\n")
	before := treeOf(t, home)
	if _, err := installReasonix("/bin/deja", true, true); err != nil {
		t.Fatal(err)
	}
	sameTree(t, "a home with a linked foreign deja after uninstall", before, treeOf(t, home))
	if _, err := installReasonix("/bin/deja", false, true); err == nil {
		t.Error("install went ahead over a linked foreign deja")
	}
}

// A record named deja with nothing on disk and a source that is not deja's is
// another package's record, and stays.
func TestUninstallReasonixLeavesAForeignRecordWithoutADirectory(t *testing.T) {
	home := reasonixTestHome(t, true)
	record := func(source string) string {
		b, err := json.MarshalIndent(map[string]any{"version": 1, "plugins": []map[string]any{
			{"name": "deja", "source": source, "root": "plugins/deja", "manifestKind": "reasonix", "enabled": true},
			{"name": "zeta", "root": "plugins/zeta", "manifestKind": "reasonix", "enabled": true},
		}}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	st := record(filepath.Join(t.TempDir(), "someone", "deja-plugin"))
	writeTestFile(t, filepath.Join(home, "plugin-packages.json"), st)
	before := treeOf(t, home)
	if _, err := installReasonix("/bin/deja", true, true); err != nil {
		t.Fatal(err)
	}
	sameTree(t, "a home with a foreign deja record after uninstall", before, treeOf(t, home))
	// deja's own record with its directory gone is still deja's to remove.
	writeTestFile(t, filepath.Join(home, "plugin-packages.json"), record(reasonixPluginSourceDir()))
	if _, err := installReasonix("/bin/deja", true, true); err != nil {
		t.Fatal(err)
	}
	if names := reasonixStateEntries(t, home); len(names) != 1 || names[0].Name != "zeta" {
		t.Errorf("deja's own orphaned record was kept: %+v", names)
	}
}

// A link at plugins/deja to a directory holding a deja-like package — someone
// linked a copy of their own — is still theirs: install would write through
// the link into their directory.
func TestInstallReasonixDoesNotWriteThroughALink(t *testing.T) {
	home := reasonixTestHome(t, false)
	src := t.TempDir()
	own := `{"apiVersion":"reasonix.io/plugin/v2","name":"deja","mcpServers":{"deja":{"type":"stdio","command":"/opt/deja/bin/deja","args":["mcp"]}}}`
	writeTestFile(t, filepath.Join(src, "reasonix-plugin.json"), own)
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(home, "plugins", "deja")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	before := treeOf(t, src)
	if _, err := installReasonix("/bin/deja", false, true); err == nil {
		t.Error("install went ahead through a linked plugins/deja")
	}
	sameTree(t, "the linked package's own directory", before, treeOf(t, src))
}

// The same path as Windows and Reasonix may spell it. The comparison of the
// text runs on every OS; the resolving half is exercised where it can be.
func TestSamePathSpellingReadsWindowsShapes(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		fold bool
		want bool
	}{
		{`C:\Users\runneradmin\AppData\Roaming\deja\reasonix-plugin`, `C:/Users/runneradmin/AppData/Roaming/deja/reasonix-plugin/`, true, true},
		{`C:\Users\RunnerAdmin\x`, `c:\users\runneradmin\x`, true, true},
		{`C:\Users\RunnerAdmin\x`, `c:\users\runneradmin\x`, false, false},
		{`C:\a\.\b\..\c`, `C:/a/c`, true, true},
		{`C:\a\b`, `C:\a\c`, true, false},
		{"/home/u/.config/deja/reasonix-plugin", "/home/u/.config/deja/reasonix-plugin", false, true},
	} {
		if got := samePathSpelling(tc.a, tc.b, tc.fold); got != tc.want {
			t.Errorf("samePathSpelling(%q, %q, fold=%v) = %v, want %v", tc.a, tc.b, tc.fold, got, tc.want)
		}
	}
	if rxSamePath("", "") {
		t.Error("two empty paths are not a match")
	}
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err == nil && !rxSamePath(link, dir) {
		t.Error("a path and a link to it are the same path")
	}
}

// deja's own package is deja's whatever its build is called: on Windows the
// entry names the build, and a test binary is deja.test.exe.
func TestReasonixPackageIsOursWhateverTheBuildIsCalled(t *testing.T) {
	reasonixTestHome(t, false)
	build := filepath.Join(t.TempDir(), "deja.test.exe")
	files := reasonixPackageFiles(build, true)
	if err := writePackageTree(reasonixPluginSourceDir(), files); err != nil {
		t.Fatal(err)
	}
	if err := writePackageTree(reasonixInstalledRoot(), files); err != nil {
		t.Fatal(err)
	}
	if !reasonixPackageIsOurs(reasonixInstalledManifest()) {
		t.Fatal("deja's own package, named for a build called deja.test.exe, read as someone else's")
	}
	if _, err := installReasonix(build, false, true); err != nil {
		t.Errorf("reinstall over deja's own package: %v", err)
	}
	// Changed by hand to run something else, it is not deja's copy any more.
	writeTestFile(t, reasonixInstalledManifest(), strings.Replace(string(files["reasonix-plugin.json"]), "reasonix-ext", "other-ext", 1))
	if reasonixPackageIsOurs(reasonixInstalledManifest()) {
		t.Error("a package that differs from deja's copy and runs no deja binary read as deja's")
	}
}

// reasonixTestHome is a Reasonix home with a config and, when other is set,
// another plugin already recorded the way Reasonix writes its state file.
func reasonixTestHome(t *testing.T, other bool) string {
	t.Helper()
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "reasonix-home")
	t.Setenv("REASONIX_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	noReasonixCLI(t)
	writeTestFile(t, filepath.Join(home, "config.toml"), "[agent]\nmodel = \"deepseek-chat\"\n")
	if other {
		writeTestFile(t, filepath.Join(home, "plugin-packages.json"),
			"{\n  \"version\": 1,\n  \"plugins\": [\n    {\n      \"name\": \"zeta\",\n      \"root\": \"plugins/zeta\",\n      \"manifestKind\": \"reasonix\",\n      \"enabled\": true\n    }\n  ]\n}\n")
		writeTestFile(t, filepath.Join(home, "plugins", "zeta", "reasonix-plugin.json"), `{"apiVersion":"reasonix.io/plugin/v2","name":"zeta"}`)
	}
	return home
}

func noReasonixCLI(t *testing.T) {
	t.Helper()
	prev := reasonixCLI
	reasonixCLI = func() string { return "" }
	t.Cleanup(func() { reasonixCLI = prev })
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeOf is every file and directory under root with its contents.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		rel, _ := filepath.Rel(root, p)
		if info.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, _ := os.ReadFile(p) //nolint:gosec // the walk is over the test's own temp home
		out[rel] = string(b)
		return nil
	})
	return out
}

// sameTree compares two snapshots of a directory. The one file allowed to
// appear is deja's usual snapshot of a config the reader already had, and only
// when it holds that config's original bytes.
func sameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	after = cloneTree(after)
	for k, v := range after {
		if orig, ok := before[strings.TrimSuffix(k, ".bak")]; ok && strings.HasSuffix(k, ".bak") && v == orig {
			if _, had := before[k]; !had {
				delete(after, k)
			}
		}
	}
	var diff []string
	for k, v := range before {
		if got, ok := after[k]; !ok {
			diff = append(diff, "gone: "+k)
		} else if got != v {
			diff = append(diff, "changed: "+k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			diff = append(diff, "left behind: "+k)
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Errorf("%s is not as it was:\n  %s", what, strings.Join(diff, "\n  "))
	}
}

func cloneTree(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

func installReasonixTarget(t *testing.T, target string, uninstall bool) string {
	t.Helper()
	return captureStdout(t, func() {
		args := []string{target, "--no-index"}
		if err := runInstall(index.DefaultDir(), args, uninstall); err != nil {
			t.Fatalf("install %s (uninstall=%v): %v", target, uninstall, err)
		}
	})
}

func reasonixStateEntries(t *testing.T, home string) []reasonixEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "plugin-packages.json"))
	if err != nil {
		t.Fatalf("state file: %v", err)
	}
	var st struct {
		Plugins []reasonixEntry `json:"plugins"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("state file is not JSON: %v", err)
	}
	return st.Plugins
}

func TestInstallReasonixAutoWritesThePackageAndItsRecord(t *testing.T) {
	home := reasonixTestHome(t, true)
	installReasonixTarget(t, "reasonix-auto", false)

	var m struct {
		APIVersion string `json:"apiVersion"`
		MCPServers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
		Skills   []string `json:"skills"`
		Commands []string `json:"commands"`
		Runtime  struct {
			Command    string   `json:"command"`
			Args       []string `json:"args"`
			Required   bool     `json:"required"`
			Intercepts []string `json:"intercepts"`
		} `json:"runtime"`
	}
	b, err := os.ReadFile(filepath.Join(home, "plugins", "deja", "reasonix-plugin.json"))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	// The launcher on unix; on Windows, where the launcher is a shell script
	// Reasonix cannot run, the build itself.
	exe, _ := os.Executable()
	launcher := hookExeFor(exe, false)
	if m.APIVersion != "reasonix.io/plugin/v2" || !rxSamePath(m.MCPServers["deja"].Command, launcher) || m.MCPServers["deja"].Args[0] != "mcp" {
		t.Errorf("manifest = %s, want a v2 package whose MCP server runs the launcher", b)
	}
	// Reasonix refuses a runtime command that is not absolute.
	if !filepath.IsAbs(m.Runtime.Command) || m.Runtime.Args[0] != "reasonix-ext" || m.Runtime.Required {
		t.Errorf("runtime = %+v, want an optional, absolute deja reasonix-ext", m.Runtime)
	}
	if strings.Join(m.Runtime.Intercepts, ",") != strings.Join(rxSubscriptions, ",") {
		t.Errorf("intercepts = %v, want what the sidecar subscribes to (%v)", m.Runtime.Intercepts, rxSubscriptions)
	}
	for _, rel := range []string{"skills/deja-history/SKILL.md", "commands/deja.md"} {
		if _, err := os.Stat(filepath.Join(home, "plugins", "deja", filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s missing: %v", rel, err)
		}
	}
	entries := reasonixStateEntries(t, home)
	if len(entries) != 2 || entries[0].Name != "deja" || entries[1].Name != "zeta" {
		t.Fatalf("state = %+v, want deja beside the plugin already there, sorted", entries)
	}
	if e := entries[0]; !e.Enabled || e.Root != "plugins/deja" || e.ManifestKind != "reasonix" || e.Source != reasonixPluginSourceDir() {
		t.Errorf("deja's record = %+v", e)
	}
}

func TestInstallReasonixTwiceChangesNothing(t *testing.T) {
	home := reasonixTestHome(t, true)
	installReasonixTarget(t, "reasonix-auto", false)
	before := treeOf(t, home)
	out := installReasonixTarget(t, "reasonix-auto", false)
	if !strings.Contains(out, "unchanged") {
		t.Errorf("second install said:\n%s\nwant unchanged", out)
	}
	sameTree(t, "the Reasonix home after a second install", before, treeOf(t, home))
	if n := len(reasonixStateEntries(t, home)); n != 2 {
		t.Errorf("state holds %d records after two installs, want 2", n)
	}
}

func TestInstallReasonixAdoptsAnEarlierInstall(t *testing.T) {
	home := reasonixTestHome(t, true)
	// A package an older deja wrote from somewhere else: another binary, another
	// source directory, an older version.
	writeTestFile(t, filepath.Join(home, "plugins", "deja", "reasonix-plugin.json"),
		`{"apiVersion":"reasonix.io/plugin/v2","name":"deja","mcpServers":{"deja":{"type":"stdio","command":"/opt/old/deja","args":["mcp"]}}}`)
	st := "{\n  \"version\": 1,\n  \"plugins\": [\n    {\n      \"name\": \"deja\",\n      \"source\": \"/opt/old/src\",\n      \"root\": \"plugins/deja\",\n      \"version\": \"0.1.0\",\n      \"manifestKind\": \"reasonix\",\n      \"enabled\": true\n    },\n    {\n      \"name\": \"zeta\",\n      \"root\": \"plugins/zeta\",\n      \"manifestKind\": \"reasonix\",\n      \"enabled\": true\n    }\n  ]\n}\n"
	writeTestFile(t, filepath.Join(home, "plugin-packages.json"), st)
	out := installReasonixTarget(t, "reasonix", false)
	if !strings.Contains(out, "updated") {
		t.Errorf("install over an earlier one said:\n%s\nwant updated", out)
	}
	entries := reasonixStateEntries(t, home)
	if len(entries) != 2 || entries[0].Source != reasonixPluginSourceDir() {
		t.Fatalf("state = %+v, want the one deja record rewritten in place", entries)
	}
	if b, _ := os.ReadFile(reasonixInstalledManifest()); bytes.Contains(b, []byte("/opt/old/deja")) {
		t.Errorf("the old binary is still named in the manifest:\n%s", b)
	}
}

func TestUninstallReasonixRestoresTheHome(t *testing.T) {
	for _, tc := range []struct {
		name  string
		other bool
	}{{"with another plugin", true}, {"with no state file", false}} {
		t.Run(tc.name, func(t *testing.T) {
			home := reasonixTestHome(t, tc.other)
			before := treeOf(t, home)
			installReasonixTarget(t, "reasonix-auto", false)
			if len(treeOf(t, home)) == len(before) {
				t.Fatal("install wrote nothing, so the round trip proves nothing")
			}
			out := installReasonixTarget(t, "reasonix-auto", true)
			if !strings.Contains(out, "removed") {
				t.Errorf("uninstall said:\n%s", out)
			}
			sameTree(t, "the Reasonix home after uninstall", before, treeOf(t, home))
			if _, err := os.Stat(reasonixPluginSourceDir()); !os.IsNotExist(err) {
				t.Errorf("deja's copy of the package is still at %s", reasonixPluginSourceDir())
			}
		})
	}
}

// With reasonix on PATH the package goes through Reasonix's own installer, and
// the uninstall still takes back exactly what that installer made.
func TestInstallReasonixHandsThePackageToTheCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake reasonix is a shell script")
	}
	for _, other := range []bool{true, false} {
		t.Run(fmt.Sprintf("other plugin %v", other), func(t *testing.T) {
			reasonixViaFakeCLI(t, other, "")
		})
	}
	// Reasonix signed a receipt with the key after the install: the key is
	// in use and stays, whoever made it.
	t.Run("receipt key in use", func(t *testing.T) {
		reasonixViaFakeCLI(t, false, "receipt")
	})
	t.Run("key signed a credential journal", func(t *testing.T) {
		reasonixViaFakeCLI(t, false, "journal")
	})
}

func reasonixViaFakeCLI(t *testing.T, other bool, used string) {
	t.Helper()
	home := reasonixTestHome(t, other)
	bin := filepath.Join(t.TempDir(), "reasonix")
	log := filepath.Join(t.TempDir(), "calls")
	zeta := ""
	if other {
		zeta = `,
    {
      "name": "zeta",
      "root": "plugins/zeta",
      "manifestKind": "reasonix",
      "enabled": true
    }`
	}
	// Does what `reasonix plugin install --replace` does to the home: copies
	// the source to plugins/<name> and records it beside any other record.
	script := `#!/bin/sh
echo "$@ REASONIX_HOME=$REASONIX_HOME" >> "` + log + `"
src="$3"
rm -rf "$REASONIX_HOME/plugins/deja"
mkdir -p "$REASONIX_HOME/plugins"
cp -R "$src" "$REASONIX_HOME/plugins/deja"
# Any reasonix run makes its crash directory and its receipt-signing key
# when there are none.
mkdir -p "$REASONIX_HOME/cli-crash-fatal"
mkdir -p "$REASONIX_HOME/transactions/model-settings-receipts"
key="$REASONIX_HOME/transactions/model-settings-receipts/request-digest.key"
[ -f "$key" ] || printf 0123456789abcdef0123456789abcdef > "$key"
cat >"$REASONIX_HOME/plugin-packages.json" <<EOF
{
  "version": 1,
  "plugins": [
    {
      "name": "deja",
      "source": "$src",
      "root": "plugins/deja",
      "version": "0.0.0-dev",
      "description": "Recall your own past coding sessions before you ask (deja-vu)",
      "manifestKind": "reasonix",
      "enabled": true
    }` + zeta + `
  ]
}
EOF
`
	writeTestFile(t, bin, script)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	reasonixCLI = func() string { return bin }
	prevVersion := version
	version = "dev"
	t.Cleanup(func() { version = prevVersion })

	before := treeOf(t, home)
	installReasonixTarget(t, "reasonix-auto", false)
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("reasonix was never run: %v", err)
	}
	want := "plugin install " + reasonixPluginSourceDir() + " --name deja --replace --yes REASONIX_HOME=" + home
	if strings.TrimSpace(string(calls)) != want {
		t.Errorf("reasonix ran as\n  %s\nwant\n  %s", calls, want)
	}
	installReasonixTarget(t, "reasonix-auto", false)
	if n := strings.Count(string(mustRead(t, log)), "\n"); n != 1 {
		t.Errorf("a second install ran reasonix again (%d runs); nothing had changed", n)
	}
	receipts := filepath.Join(home, "transactions", "model-settings-receipts")
	switch used {
	case "receipt":
		writeTestFile(t, filepath.Join(receipts, "abc.json"), "{}")
	case "journal":
		writeTestFile(t, filepath.Join(home, "transactions", "model-credentials", "j1.json"), "{}")
	}
	installReasonixTarget(t, "reasonix-auto", true)
	after := treeOf(t, home)
	if used != "" {
		if _, err := os.Stat(filepath.Join(receipts, "request-digest.key")); err != nil {
			t.Fatalf("uninstall took a key Reasonix has signed with: %v", err)
		}
		for k := range after {
			if strings.HasPrefix(k, "transactions") {
				delete(after, k)
			}
		}
	}
	sameTree(t, "the Reasonix home after a CLI install and uninstall", before, after)
}

func TestUninstallReasonixTakesDejasCopyOfThePackage(t *testing.T) {
	reasonixTestHome(t, false)
	if _, err := installReasonix("/bin/deja", false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(reasonixPluginSourceDir()); err != nil {
		t.Fatalf("install left no source for Reasonix to update from: %v", err)
	}
	if res, err := installReasonix("/bin/deja", true, true); err != nil || res.Action != "removed" {
		t.Fatalf("uninstall = %+v %v", res, err)
	}
	if _, err := os.Stat(reasonixPluginSourceDir()); !os.IsNotExist(err) {
		t.Errorf("deja's copy of the package is still at %s", reasonixPluginSourceDir())
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDoctorReadsTheReasonixWiring(t *testing.T) {
	home := reasonixTestHome(t, false)
	wiring := func() (string, bool) {
		for _, a := range autoWirings() {
			if a.name == "reasonix" {
				return autoWiringState(a)
			}
		}
		t.Fatal("doctor has no reasonix row")
		return "", false
	}
	if state, _ := wiring(); state == "wired" {
		t.Fatalf("doctor calls a home with no package wired")
	}
	installReasonixTarget(t, "reasonix-auto", false)
	if state, missing := wiring(); state != "wired" || missing {
		t.Fatalf("after install: state=%s binaryMissing=%v, want wired", state, missing)
	}
	// A package Reasonix holds no enabled record of does not load.
	statePath := filepath.Join(home, "plugin-packages.json")
	b := mustRead(t, statePath)
	writeTestFile(t, statePath, strings.Replace(string(b), `"enabled": true`, `"enabled": false`, 1))
	if state, _ := wiring(); state != "stale" {
		t.Errorf("disabled package: state=%s, want stale", state)
	}
	writeTestFile(t, statePath, string(b))
	// The runtime command is a field of its own; a launcher that went away
	// must still be reported.
	manifest := reasonixInstalledManifest()
	var doc map[string]any
	if err := json.Unmarshal(mustRead(t, manifest), &doc); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "gone", "deja")
	typedOrFatal[map[string]any](t, doc["runtime"])["command"] = gone
	edited, jerr := json.MarshalIndent(doc, "", "  ")
	if jerr != nil {
		t.Fatal(jerr)
	}
	writeTestFile(t, manifest, string(edited))
	if _, missing := wiring(); !missing {
		t.Errorf("runtime pointing at %s: doctor did not report it missing", gone)
	}
	if got := reasonixRuntimeMissing(); filepath.ToSlash(got) != filepath.ToSlash(gone) {
		t.Errorf("missing runtime = %q, want %q", got, gone)
	}
}

// The plain target on a machine that has the runtime keeps it: `install
// --all` runs both, and dropping it only for the -auto target to put it back
// rewrote the package twice on every run.
func TestInstallReasonixPlainKeepsAnInstalledRuntime(t *testing.T) {
	reasonixTestHome(t, false)
	installReasonixTarget(t, "reasonix-auto", false)
	out := installReasonixTarget(t, "reasonix", false)
	if !strings.Contains(out, "unchanged") {
		t.Errorf("plain install after -auto said:\n%s", out)
	}
	if !strings.Contains(string(mustRead(t, reasonixInstalledManifest())), `"reasonix-ext"`) {
		t.Error("the plain target took the runtime out")
	}
	out = captureStdout(t, func() {
		if err := runInstall(index.DefaultDir(), []string{"--all", "--no-index"}, false); err != nil {
			t.Fatal(err)
		}
	})
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "reasonix") && !strings.Contains(line, "unchanged") {
			t.Errorf("install --all rewrote the package: %s", line)
		}
	}
}

// The MCP server rides in the same plugin package, so `reasonix plugin
// disable deja` stops it too. The auto-recall row turned stale and the MCP
// row kept reading wired (#4409).
func TestDoctorMarksTheReasonixMCPRowSwitchedOff(t *testing.T) {
	home := reasonixTestHome(t, false)
	installReasonixTarget(t, "reasonix-auto", false)
	row := func() doctorMCPStatus {
		for _, r := range collectDoctorMCP() {
			if r.Name == "reasonix" {
				return r
			}
		}
		t.Fatal("doctor has no reasonix MCP row")
		return doctorMCPStatus{}
	}
	text := func() string {
		var b strings.Builder
		doctorMCP(&b)
		return b.String()
	}
	if r := row(); r.State != "wired" || r.SwitchedOff {
		t.Fatalf("enabled package: %+v, want wired and on", r)
	}
	if strings.Contains(text(), "switched off") {
		t.Fatalf("enabled package reads switched off:\n%s", text())
	}
	statePath := filepath.Join(home, "plugin-packages.json")
	b := mustRead(t, statePath)
	writeTestFile(t, statePath, strings.Replace(string(b), `"enabled": true`, `"enabled": false`, 1))
	if r := row(); !r.SwitchedOff {
		t.Errorf("disabled package: %+v, want switched_off", r)
	}
	if out := text(); !strings.Contains(out, "switched off") {
		t.Errorf("disabled package: text row says nothing:\n%s", out)
	}
}

// The switched-off line names the way back for the install the user made: an
// MCP-only `deja install reasonix` told to run reasonix-auto would get
// auto-recall it never asked for (#4409).
func TestDoctorSwitchedOffLineKeepsAPlainInstallPlain(t *testing.T) {
	home := reasonixTestHome(t, false)
	installReasonixTarget(t, "reasonix", false)
	statePath := filepath.Join(home, "plugin-packages.json")
	b := mustRead(t, statePath)
	writeTestFile(t, statePath, strings.Replace(string(b), `"enabled": true`, `"enabled": false`, 1))
	note := doctorMCPSwitchedOff("reasonix")
	if note == "" || strings.Contains(note, "reasonix-auto") {
		t.Errorf("plain install, disabled: %q, want a line that does not point at reasonix-auto", note)
	}
	if !strings.Contains(note, "reasonix plugin enable deja") {
		t.Errorf("plain install, disabled: %q, want the enable command that undoes the disable", note)
	}
}

// Every refusal, whatever it was, ended with "check those paths' permissions":
// a config with a `//` comment in it refused five targets for a syntax error
// and sent the reader to look at file permissions on files they can read
// perfectly well (#1663).
func TestRefusalRemedyMatchesTheFailure(t *testing.T) {
	parse := errors.New("invalid character '/' looking for beginning of object key string")
	denied := &fs.PathError{Op: "open", Path: "/x/settings.json", Err: fs.ErrPermission}

	cases := []struct {
		name string
		errs []error
		want string
	}{
		{"parse errors only", []error{parse, parse}, "fix what each one reports"},
		{"mixed", []error{denied, parse}, "fix what each one reports"},
		{"permission errors only", []error{denied, denied}, "check those paths' permissions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := refusalRemedy(c.errs); !strings.Contains(got, c.want) {
				t.Errorf("remedy for %s is %q, want it to say %q", c.name, got, c.want)
			}
		})
	}
}

// The end-to-end shape: a config deja cannot parse is refused, and the sentence
// that closes the run does not blame permissions.
//
// It used to be a commented config that stood here. A comment is no longer a
// refusal — the entry is edited as text and the comment survives (#1664) — so
// the case is a file that is genuinely broken, which is what the message was
// always about.
func TestInstallUnparseableJSONDoesNotBlamePermissions(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("HOME"), ".gemini")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settings, []byte("{\n  \"theme\": \"dark\",\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := captureRun(t, "install", "gemini")
	if err == nil {
		t.Fatal("install accepted a settings.json it cannot parse")
	}
	msg := err.Error()
	if strings.Contains(msg, "permissions") {
		t.Errorf("a parse error is reported as a permissions problem: %s", msg)
	}
	if !strings.Contains(msg, "unexpected end of JSON input") && !strings.Contains(msg, "invalid character") {
		t.Errorf("the refusal does not carry the parse error: %s", msg)
	}
}

// And the comment itself: the same target takes it now, and gives it back.
func TestInstallTakesACommentedGeminiConfig(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("HOME"), ".gemini")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(root, "settings.json")
	before := "{\n  // mine\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(settings, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini", "--no-index"); err != nil {
		t.Fatalf("a comment refused the target: %v", err)
	}
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "// mine") || !strings.Contains(string(b), `"theme": "dark"`) {
		t.Errorf("the reader's file did not survive:\n%s", b)
	}
	if !strings.Contains(string(b), `"deja"`) {
		t.Errorf("deja's entry was not written:\n%s", b)
	}
}

// regressHome points every harness and deja path at a fresh temp home.
func regressHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// goose and other Windows resolvers read APPDATA, not the home directory.
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, ".deja-index"))
	for _, k := range []string{"GOOSE_PATH_ROOT", "GROK_HOME", "TRAE_HOME", "TRAECLI_HOME", "CODEWHALE_HOME",
		"REASONIX_HOME", "REASONIX_STATE_HOME", "KILO_CONFIG_DIR", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(k, "")
	}
	return home
}

func regressWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func regressRun(t *testing.T, uninstall bool, args ...string) error {
	t.Helper()
	var err error
	captureStdout(t, func() { err = runInstall(filepath.Join(homeDir(), ".deja-index"), args, uninstall) })
	return err
}

// A server of the user's own that happens to be named "deja" is replaced on
// install, and the uninstall then deletes the .bak that held it: the original
// entry (a different program, with its env) is gone for good.
func TestUninstallKeepsTheSnapshotOfAForeignDejaEntry(t *testing.T) {
	home := regressHome(t)
	path := filepath.Join(home, ".claude.json")
	orig := "{\n  \"mcpServers\": {\n    \"deja\": {\"command\": \"/usr/local/bin/other-tool\", \"args\": [\"serve\"], \"env\": {\"K\": \"V\"}}\n  }\n}\n"
	regressWrite(t, path, orig)
	if err := regressRun(t, false, "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if err := regressRun(t, true, "claude-code"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	bak, _ := os.ReadFile(path + ".bak")
	if !strings.Contains(string(got), "other-tool") && !strings.Contains(string(bak), "other-tool") {
		t.Fatalf("the user's own \"deja\" server is gone from both the config and its snapshot:\nconfig: %s\nbak: %q", got, bak)
	}
}

// The launcher every hook runs lost its exec bit; doctor says reinstall
// fixes it, but install sees identical bytes and leaves the mode alone.
func TestReinstallRestoresTheLauncherExecBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	regressHome(t)
	if err := regressRun(t, false, "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	launcher := dejaLauncherPath()
	if err := os.Chmod(launcher, 0o644); err != nil {
		t.Fatal(err)
	}
	if claudeHookShell() != "" {
		if note := claudeHookRunNote(claudeHookWiringState().hooks); !strings.Contains(note, "exits 126") {
			t.Errorf("doctor's note for a launcher that cannot run: %q", note)
		}
	}
	if err := regressRun(t, false, "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("launcher still %v after the reinstall doctor recommends; every hook exits 126", fi.Mode())
	}
}

// doctor's "N of M events wired" line tells the reader to run a bare
// `deja install`, which refuses: install needs a target.
func TestDoctorOutOfDateAdviceIsARunnableCommand(t *testing.T) {
	regressHome(t)
	writeClaudeSettings(t, "SessionStart", "UserPromptSubmit")
	var out bytes.Buffer
	doctorHooks(&out)
	m := regexp.MustCompile("events wired — no [^;]*; run `deja ([^`]*)`").FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no out-of-date advice in:\n%s", out.String())
	}
	args := strings.Fields(m[1])
	if args[0] != "install" {
		t.Fatalf("advice is not an install: %q", m[1])
	}
	if err := regressRun(t, false, append(args[1:], "--no-index")...); err != nil {
		t.Fatalf("doctor advised `deja %s`, which fails: %v", m[1], err)
	}
}

func regressAutoState(t *testing.T, name string) string {
	t.Helper()
	for _, a := range autoWirings() {
		if a.name == name {
			s, _ := autoWiringState(a)
			return s
		}
	}
	t.Fatalf("no auto wiring row %q", name)
	return ""
}

// doctor calls auto-recall "stale" (and points at the -auto install) for a
// harness deja never hooked: CodeWhale's own config.toml, or the plain
// codewhale / reasonix MCP install, which writes the same file the hooks row reads.
func TestDoctorCallsAnUnhookedHarnessMissing(t *testing.T) {
	t.Run("codewhale user config", func(t *testing.T) {
		regressHome(t)
		regressWrite(t, codewhaleConfigPath(), "model = \"x\"\n")
		if s := regressAutoState(t, "codewhale"); s != "missing" {
			t.Fatalf("codewhale hooks row = %q for a config.toml deja never touched", s)
		}
	})
	t.Run("plain codewhale install", func(t *testing.T) {
		regressHome(t)
		if err := regressRun(t, false, "codewhale", "--no-index"); err != nil {
			t.Fatal(err)
		}
		if s := regressAutoState(t, "codewhale"); s != "missing" {
			t.Fatalf("codewhale hooks row = %q after the MCP-only target", s)
		}
	})
	t.Run("plain reasonix install", func(t *testing.T) {
		regressHome(t)
		if err := regressRun(t, false, "reasonix", "--no-index"); err != nil {
			t.Fatal(err)
		}
		if s := regressAutoState(t, "reasonix"); s != "missing" {
			t.Fatalf("reasonix hooks row = %q after the MCP-only target", s)
		}
	})
}

// opencode/kilocode uninstall adds an empty "mcp" block to a config deja
// never wrote (and leaves one behind after install, edit, uninstall). Every
// other MCP writer has the #676/#2604 guard; updateOpencodeJSON does not.
func TestOpencodeUninstallLeavesNoEmptyMCPBlock(t *testing.T) {
	const orig = "{\n  \"theme\": \"dark\"\n}\n"
	for _, tc := range []struct{ target, rel string }{
		{"opencode", ".config/opencode/opencode.json"},
		{"kilocode", ".config/kilo/kilo.json"},
	} {
		t.Run(tc.target+" never installed", func(t *testing.T) {
			home := regressHome(t)
			path := filepath.Join(home, tc.rel)
			regressWrite(t, path, orig)
			if err := regressRun(t, true, tc.target); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			if string(got) != orig {
				t.Fatalf("uninstall rewrote a config deja never touched:\n%s", got)
			}
		})
		t.Run(tc.target+" edited after install", func(t *testing.T) {
			home := regressHome(t)
			path := filepath.Join(home, tc.rel)
			regressWrite(t, path, orig)
			if err := regressRun(t, false, tc.target, "--no-index"); err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			b, _ := os.ReadFile(path)
			if err := json.Unmarshal(b, &root); err != nil {
				t.Fatal(err)
			}
			root["userEdit"] = 1.0
			b, _ = json.MarshalIndent(root, "", "  ")
			regressWrite(t, path, string(b)+"\n")
			if err := regressRun(t, true, tc.target); err != nil {
				t.Fatal(err)
			}
			b, _ = os.ReadFile(path)
			root = nil
			if err := json.Unmarshal(b, &root); err != nil {
				t.Fatal(err)
			}
			if _, ok := root["mcp"]; ok {
				t.Fatalf("uninstall left the \"mcp\" block install added:\n%s", b)
			}
		})
	}
}

// `deja uninstall qwen` fails on a settings.json with a comment in it — a
// file `deja install qwen` edits fine — because the qwen-auto status line
// writer parses strict JSON while the hook writer beside it blanks comments.
func TestQwenUninstallReadsCommentedSettings(t *testing.T) {
	regressHome(t)
	path := filepath.Join(sources.QwenConfigDir(), "settings.json")
	regressWrite(t, path, "// mine\n{\n  \"theme\": \"dark\"\n}\n")
	if err := regressRun(t, false, "qwen", "--no-index"); err != nil {
		t.Fatalf("install qwen: %v", err)
	}
	if err := regressRun(t, true, "qwen"); err != nil {
		t.Fatalf("uninstall qwen refused the file install just edited: %v", err)
	}
}

// deja's own goose recipe from an earlier install (another binary path, or
// an older template) is snapshotted as if it were the user's, and the uninstall
// puts it back: mentionsDeja does not recognise gooseRecipe's text.
func TestGooseUninstallDropsDejasOwnOlderRecipe(t *testing.T) {
	regressHome(t)
	recipe := gooseRecipePath()
	regressWrite(t, recipe, gooseRecipe("/old/place/deja"))
	if err := regressRun(t, false, "goose-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if err := regressRun(t, true, "goose-auto"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(recipe); err == nil {
		t.Fatalf("uninstall left deja's own recipe behind, naming a deja binary:\n%s", b)
	}
}

// A second identical install of grok-auto / trae-auto rewrites config.toml,
// reports "updated" and snapshots deja's own file as .bak.
func TestRepeatInstallLeavesTOMLWithStatusLineAlone(t *testing.T) {
	for _, tc := range []struct {
		target string
		path   func() string
	}{
		{"grok-auto", func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
		{"trae-auto", traeConfigPath},
	} {
		t.Run(tc.target, func(t *testing.T) {
			regressHome(t)
			if err := regressRun(t, false, tc.target, "--no-index"); err != nil {
				t.Fatal(err)
			}
			out := captureStdout(t, func() {
				if err := runInstall(filepath.Join(homeDir(), ".deja-index"), []string{tc.target, "--no-index"}, false); err != nil {
					t.Error(err)
				}
			})
			if _, err := os.Stat(tc.path() + ".bak"); err == nil {
				t.Errorf("a repeat install snapshotted deja's own %s", tc.path())
			}
			if strings.Contains(out, "updated "+shortHome(tc.path())) {
				t.Errorf("a repeat install reports a change:\n%s", out)
			}
		})
	}
}

// doctor calls Roo's auto-recall row "wired" on a machine where only codex
// was installed: the row reads the shared ~/.agents skill every harness writes.
func TestDoctorRooNotWiredByAnotherTargetsSkill(t *testing.T) {
	regressHome(t)
	if err := regressRun(t, false, "codex", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if s := regressAutoState(t, "roo"); s == "wired" {
		t.Fatalf("roo row = %q though roo was never installed", s)
	}
}

// #3659 asked for three things about an MCP entry naming a deja build under
// another basename: that it be recognised, reported, and **removable**. The
// first two have tests beside the reader (doctor_renamed_binary_test.go); this
// is the third, and it is the one an uninstall gets wrong quietly — removal
// matches entries by what they name, so an entry naming `deja-probe` outlived
// every uninstall and went on calling a binary nobody has.
func TestInstallAdoptsAndUninstallRemovesAnEntryNamingAnotherBuild(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	hermes := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(hermes, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", hermes)
	// The record is what would otherwise recognise the path, so it is emptied:
	// the name has to answer on its own (#3681 made that true for hooks).
	forgetWrittenExes()
	t.Cleanup(forgetWrittenExes)

	path := filepath.Join(hermes, "config.yaml")
	theirs := "provider: openai\nmcp_servers:\n  deja:\n    command: \"" +
		filepath.Join(home, "gone", "deja-probe") + "\"\n    args: [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "hermes", "--no-index"); err != nil {
		t.Fatalf("install hermes: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "deja-probe") {
		t.Errorf("the install left the entry naming another build:\n%s", after)
	}
	if n := strings.Count(string(after), "deja:"); n != 1 {
		t.Errorf("the install stacked a second entry beside it (%d):\n%s", n, after)
	}

	if _, cerr := captureRun(t, "uninstall", "hermes"); cerr != nil {
		t.Fatalf("uninstall hermes: %v", cerr)
	}
	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(final), "deja") {
		t.Errorf("the uninstall left deja in the config:\n%s", final)
	}
	// The reader's own key and their other settings stay: emptied of deja and
	// of nothing else (#2583). An `mcp_servers:` with nothing under it is what
	// hermes reads as `{}`.
	if !strings.Contains(string(final), "provider: openai") {
		t.Errorf("the uninstall took the reader's own settings:\n%s", final)
	}
}

// dejasOwnClaudeEntry is the entry install writes on this platform: Windows
// runs deja through cmd, so spelling the Unix form here made install see a
// changed entry and say so — about wiring it had written itself (#2808).
func dejasOwnClaudeEntry(t *testing.T) string {
	t.Helper()
	command, args := mcpCommandArgs("/usr/local/bin/deja")
	entry, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"deja": map[string]any{"type": "stdio", "command": command, "args": args},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(entry)
}

// Install writes into files people also edit. Replacing a deja entry someone
// pointed at their own wrapper is the right thing to do; doing it in the same
// sentence deja prints for a config that never mentioned it is not (#2390).
func TestInstallSaysWhenItReplacedAnEntrySomeoneChanged(t *testing.T) {
	cases := []struct {
		name   string
		before string
		want   string // "" means: say nothing besides the action
	}{
		{"a config that never mentioned deja",
			`{"mcpServers":{"mine":{"command":"/usr/local/bin/my-server"}}}`, ""},
		{"the entry deja itself wrote", dejasOwnClaudeEntry(t), ""},
		{"an entry someone pointed at their own wrapper",
			`{"mcpServers":{"deja":{"command":"/home/me/bin/deja-wrapper","args":["mcp","--quiet"]}}}`,
			"/home/me/bin/deja-wrapper"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
			if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}

			r, err := installTarget("claude", "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if tc.want == "" {
				if r.Note != "" {
					t.Errorf("install spoke about an entry nobody had changed: %q", r.Note)
				}
				return
			}
			if !strings.Contains(r.Note, tc.want) {
				t.Errorf("install replaced their wiring without naming it: note %q", r.Note)
			}
		})
	}
}

// rooHost lays out one VS Code host the way sources.RooRoots expects it, with
// the given settings file in place.
func rooHost(t *testing.T, dir, settings string) string {
	t.Helper()
	root := filepath.Join(dir, "globalStorage", "rooveterinaryinc.roo-cline")
	if err := os.MkdirAll(filepath.Join(root, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		if err := os.WriteFile(filepath.Join(root, "settings", "mcp_settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// roo wires one settings file per host, so a refusal on the second host left
// the first one written with a .bak beside it while the run reported the
// target refused (#2750).
func TestRooWritesNoHostWhenOneOfThemWillRefuse(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	bad := rooHost(t, filepath.Join(dir, "b"), "{ this is not json ]\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, bad}, string(os.PathListSeparator)))

	first := filepath.Join(good, "settings", "mcp_settings.json")
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "roo", "--no-index"); err == nil {
		t.Fatal("a target that cannot finish reported itself installed")
	}
	after, rerr := os.ReadFile(first)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("the first host was written before the refusal:\n%s", after)
	}
	if _, err := os.Stat(first + ".bak"); err == nil {
		t.Errorf("a snapshot was left beside a file that was never changed")
	}
}

// The check has to accept what the writer accepts: installMCPJSON takes a
// settings file with comments in it, so refusing one here would keep somebody
// who annotated their config from installing at all (#1664).
func TestRooTakesACommentedHostTheWriterCanEdit(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	host := rooHost(t, filepath.Join(dir, "a"), "{\n  // mine\n  \"mcpServers\": {}\n}\n")
	t.Setenv("DEJA_ROO_ROOTS", host)

	path := filepath.Join(host, "settings", "mcp_settings.json")
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatalf("a commented host was refused: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "// mine") {
		t.Errorf("the comment was dropped:\n%s", b)
	}
	if !strings.Contains(string(b), "\"deja\"") {
		t.Errorf("the entry was not written:\n%s", b)
	}
}

// A settings file whose mcpServers is not an object is what installMCPJSON
// refuses, and it has to be refused before the other host is written.
func TestRooRefusesABlockTheWriterCannotUseBeforeWriting(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	bad := rooHost(t, filepath.Join(dir, "b"), "{\"mcpServers\": [1, 2]}\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, bad}, string(os.PathListSeparator)))

	first := filepath.Join(good, "settings", "mcp_settings.json")
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "roo", "--no-index"); err == nil {
		t.Fatal("a block the writer cannot use was accepted")
	}
	after, rerr := os.ReadFile(first)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("the first host was written before the refusal:\n%s", after)
	}
}

// On the way out there is nothing to refuse over: uninstall takes deja out of
// the hosts it can read and leaves the rest alone, the way #2399 has it for a
// block deja never wrote.
func TestUninstallRooTakesDejaOutOfTheHostsItCanRead(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	t.Setenv("DEJA_ROO_ROOTS", good)
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	bad := rooHost(t, filepath.Join(dir, "b"), "{ not json ]\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, bad}, string(os.PathListSeparator)))

	if _, err := captureRun(t, "uninstall", "roo"); err != nil {
		t.Fatalf("uninstall refused over a host it was not asked to edit: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(good, "settings", "mcp_settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\"deja\"") {
		t.Errorf("deja was left in a host that could be read:\n%s", b)
	}
}

// The JSONC writer refuses more than the parsed one does, and the check has to
// refuse the same shapes: a commented host whose block is not an object, or
// whose top level is not an object at all, used to pass the check and refuse
// one host too late.
func TestRooAsksTheJSONCWriterWhatItWouldRefuse(t *testing.T) {
	for _, bad := range []string{
		"{\n  // mine\n  \"mcpServers\": [1, 2]\n}\n",
		"{\n  // mine\n  \"mcpServers\": null\n}\n",
		"// mine\n[1, 2]\n",
		"// mine\n5\n",
		"// mine\nnull\n",
		"null\n",
	} {
		t.Run(strings.TrimSpace(bad), func(t *testing.T) {
			hermeticEnv(t)
			dir := t.TempDir()
			good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
			other := rooHost(t, filepath.Join(dir, "b"), bad)
			t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, other}, string(os.PathListSeparator)))

			first := filepath.Join(good, "settings", "mcp_settings.json")
			before, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "roo", "--no-index"); err == nil {
				t.Fatal("a host the writer would refuse was accepted")
			}
			after, rerr := os.ReadFile(first)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if !bytes.Equal(after, before) {
				t.Errorf("the first host was written before the refusal:\n%s", after)
			}
			if _, err := os.Stat(first + ".bak"); err == nil {
				t.Errorf("a snapshot was left beside a file that was never changed")
			}
		})
	}
}

// Reading the settings is not the whole question: the snapshot and the write
// both land in the host's settings directory.
func TestRooAsksWhetherItCanWriteTheHostAtAll(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	other := rooHost(t, filepath.Join(dir, "b"), "{\n  \"mcpServers\": {}\n}\n")
	closed := filepath.Join(other, "settings")
	if err := os.Chmod(closed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(closed, 0o755) })
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, other}, string(os.PathListSeparator)))

	first := filepath.Join(good, "settings", "mcp_settings.json")
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "roo", "--no-index"); err == nil {
		t.Fatal("a host deja cannot write was accepted")
	}
	after, rerr := os.ReadFile(first)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("the first host was written before the refusal:\n%s", after)
	}
}

// A host that is there and cannot be read still has deja in it, so the run
// says so rather than reporting no host at all.
func TestUninstallRooNamesTheHostItCouldNotRead(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	t.Setenv("DEJA_ROO_ROOTS", good)
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	bad := rooHost(t, filepath.Join(dir, "b"), "{ not json ]\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, bad}, string(os.PathListSeparator)))

	out, err := captureRun(t, "uninstall", "roo")
	if err != nil {
		t.Fatalf("uninstall refused over a host it was not asked to edit: %v", err)
	}
	if strings.Contains(out, "no Roo host found") {
		t.Errorf("a host that is there was reported as no host at all:\n%s", out)
	}
	if !strings.Contains(out, "could not read") {
		t.Errorf("the host deja could not read is not named:\n%s", out)
	}
}

// The write follows a symlinked settings directory, so the question of whether
// the host takes a write has to follow it too: a Lstat walked past the link,
// answered about the host root, and refused a host that installs fine.
func TestRooFollowsASymlinkedSettingsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	hermeticEnv(t)
	dir := t.TempDir()
	host := rooHost(t, filepath.Join(dir, "a"), "")
	elsewhere := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(host, "settings")
	if err := os.RemoveAll(settings); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, settings); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(host, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(host, 0o755) })
	t.Setenv("DEJA_ROO_ROOTS", host)

	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatalf("a host whose settings directory is a writable symlink was refused: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(elsewhere, "mcp_settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\"deja\"") {
		t.Errorf("the entry was not written:\n%s", b)
	}
}

// A host deja cannot write is skipped on the way out rather than aborting the
// run: the loop used to stop at the first one and leave every later host wired.
func TestUninstallRooCleansTheHostsItCanAndNamesTheRest(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	hermeticEnv(t)
	dir := t.TempDir()
	a := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	b := rooHost(t, filepath.Join(dir, "b"), "{\n  \"mcpServers\": {}\n}\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{a, b}, string(os.PathListSeparator)))
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	closed := filepath.Join(a, "settings")
	if err := os.Chmod(closed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(closed, 0o755) })

	out, err := captureRun(t, "uninstall", "roo")
	if err != nil {
		t.Fatalf("uninstall stopped at the host it could not write: %v", err)
	}
	if !strings.Contains(out, "cannot write") {
		t.Errorf("the host deja could not write is not named:\n%s", out)
	}
	left, rerr := os.ReadFile(filepath.Join(b, "settings", "mcp_settings.json"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if strings.Contains(string(left), "\"deja\"") {
		t.Errorf("a host deja could clean was left wired:\n%s", left)
	}
}

// A block deja never wrote is left alone and reported unchanged (#2399), so it
// is not a host the uninstall gave up on and must not be named as one.
func TestUninstallRooDoesNotNameAHostItNeverTouched(t *testing.T) {
	hermeticEnv(t)
	for _, shape := range []string{
		"{\"mcpServers\": [1, 2]}\n",
		"{\n  // mine\n  \"mcpServers\": [1, 2]\n}\n",
	} {
		t.Run(strings.TrimSpace(shape), func(t *testing.T) {
			dir := t.TempDir()
			a := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
			t.Setenv("DEJA_ROO_ROOTS", a)
			if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
				t.Fatal(err)
			}
			b := rooHost(t, filepath.Join(dir, "b"), shape)
			t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{a, b}, string(os.PathListSeparator)))

			out, err := captureRun(t, "uninstall", "roo")
			if err != nil {
				t.Fatalf("uninstall refused over a block it never wrote: %v", err)
			}
			if strings.Contains(out, "could not read") || strings.Contains(out, "cannot write") {
				t.Errorf("a host deja never touched was named as one it gave up on:\n%s", out)
			}
		})
	}
}

// The write reason is for a host there is something to take out of. A host
// with no deja in its settings — or none at all — is not written on the way
// out either, so a directory that refuses the write changes nothing there.
func TestUninstallRooDoesNotNameAHostWithNothingOfDejaInIt(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	for _, other := range []string{"", "{\n  \"mcpServers\": {\"mine\": {\"command\": \"x\"}}\n}\n"} {
		name := "no settings file"
		if other != "" {
			name = "someone else's server"
		}
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			dir := t.TempDir()
			a := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
			t.Setenv("DEJA_ROO_ROOTS", a)
			if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
				t.Fatal(err)
			}
			b := rooHost(t, filepath.Join(dir, "b"), other)
			closed := filepath.Join(b, "settings")
			if err := os.Chmod(closed, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(closed, 0o755) })
			t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{a, b}, string(os.PathListSeparator)))

			out, err := captureRun(t, "uninstall", "roo")
			if err != nil {
				t.Fatalf("uninstall refused over a host with nothing of deja's in it: %v", err)
			}
			if strings.Contains(out, "cannot write") {
				t.Errorf("a host with nothing to remove was named as one deja gave up on:\n%s", out)
			}
		})
	}
}

// A host with no settings file gets one from deja, then a second write in the
// same run allowed deja's tool — and snapshotted deja's own file from a moment
// before as a .bak the uninstall then called a config the reader already had.
func TestInstallRooDoesNotSnapshotItsOwnFreshWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{rooStorage(t, home, "Code")}, string(os.PathListSeparator)))
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	baks, _ := filepath.Glob(filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings", "*.bak"))
	if len(baks) != 0 {
		t.Fatalf("a snapshot of deja's own fresh write: %v", baks)
	}
}

// A settings file the reader already had still gets its snapshot on the first
// write, as before.
func TestInstallRooStillSnapshotsAFileTheReaderHad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	store := rooStorage(t, home, "Code")
	t.Setenv("DEJA_ROO_ROOTS", store)
	path := filepath.Join(store, "settings", "mcp_settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := `{"mcpServers":{"other":{"command":"x","args":[]}}}` + "\n"
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path + ".bak"); err != nil || string(got) != theirs {
		t.Fatalf("the reader's file was not snapshotted before the first write: %v %q", err, got)
	}
}

// Roo writes mcp_settings.json on first start with an empty, multi-line
// mcpServers object. Install and uninstall gave it back as `{}`: the emptied
// object was re-marshalled, though the bytes it had were in the .bak beside it
// (#4423).
func TestUninstallRooGivesBackTheDefaultSettingsBytes(t *testing.T) {
	hermeticEnv(t)
	const original = "{\n  \"mcpServers\": {\n\n  }\n}"
	root := rooHost(t, t.TempDir(), original)
	t.Setenv("DEJA_ROO_ROOTS", root)
	path := filepath.Join(root, "settings", "mcp_settings.json")
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "roo"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != original {
		t.Errorf("settings after uninstall = %q, want the bytes Roo wrote %q", b, original)
	}

	// The control: a server added while deja was wired is not in the
	// snapshot, so the snapshot is not what the file goes back to.
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	mine := strings.Replace(string(b), "\"mcpServers\": {", "\"mcpServers\": {\n    \"fs\": {\"command\": \"fs-mcp\"},", 1)
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "roo"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "fs-mcp") || strings.Contains(string(b), "\"deja\"") {
		t.Errorf("settings after the second uninstall:\n%s", b)
	}
}

// The snapshot stands in only for what deja took out. A config deja is not in
// is the reader's as it is now, and an uninstall that meets one must not put
// back the layout the snapshot had, nor a number the snapshot rounds to the
// same float.
func TestUninstallLeavesAJSONConfigWithoutDejaAsItIs(t *testing.T) {
	t.Cleanup(func() { removingWiring = false })
	removingWiring = true
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path+".bak", []byte("{\n  \"mcpServers\": {}\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mine := []byte(`{"mcpServers":{}}` + "\n")
	if err := os.WriteFile(path, mine, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeIfChanged(path, mine, mine); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, mine) {
		t.Errorf("a config with no deja in it became %q, want it left as %q", b, mine)
	}

	// A number the snapshot holds differently, past float64's precision.
	if err := os.WriteFile(path+".bak", []byte("{\"id\": 12345678901234567890}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := []byte("{\"id\": 12345678901234567891, \"mcpServers\": {\"deja\": {}}}\n")
	next := []byte("{\"id\": 12345678901234567891}\n")
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeIfChanged(path, old, next); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, next) {
		t.Errorf("uninstall wrote %q, want %q: the snapshot's id is another number", b, next)
	}
}

func rooStorage(t *testing.T, home, host string) string {
	t.Helper()
	p := filepath.Join(home, "Library", "Application Support", host, "User", "globalStorage", "rooveterinaryinc.roo-cline")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Roo keeps MCP settings per editor host. Someone running it in both VS Code
// and Cursor would otherwise get recall in one and silence in the other.
func TestInstallRooWritesEveryHostItRunsIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{
		rooStorage(t, home, "Code"),
		rooStorage(t, home, "Cursor"),
	}, string(os.PathListSeparator)))
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, host := range []string{"Code", "Cursor"} {
		p := filepath.Join(home, "Library", "Application Support", host, "User", "globalStorage",
			"rooveterinaryinc.roo-cline", "settings", "mcp_settings.json")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: settings not written: %v", host, err)
		}
		var root struct {
			Servers map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(b, &root); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if _, ok := root.Servers["deja"]; !ok {
			t.Fatalf("%s: no deja server: %s", host, b)
		}
	}
	if _, err := installRoo("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage",
		"rooveterinaryinc.roo-cline", "settings", "mcp_settings.json"))
	if strings.Contains(string(b), "deja") {
		t.Fatalf("uninstall left the server behind: %s", b)
	}
}

// A host Roo has never run in has no storage directory. Creating one there
// leaves settings for an editor that will never read them.
func TestInstallRooSkipsHostsWithoutStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	absent := filepath.Join(home, "Library", "Application Support", "VSCodium", "User", "globalStorage", "rooveterinaryinc.roo-cline")
	t.Setenv("DEJA_ROO_ROOTS", absent)
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("install created storage for a host that does not have Roo: %v", err)
	}
}

// Roo has no hook that could refresh a digest, so what goes into the global
// rules is guidance — text that stays true — not recalled sessions.
// Roo reads ~/.roo/skills, which costs its frontmatter until something looks
// relevant. The rules file it used before is read verbatim into the system
// prompt for every mode and every task, so install takes that one away.
func TestRooGuidanceIsASkillAndDropsTheRulesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	rules := rooRulesPath()
	if err := os.MkdirAll(filepath.Dir(rules), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules, []byte(guidanceStart+"\nold\n"+guidanceEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("roo", false); err != nil {
		t.Fatalf("guidance: %v", err)
	}
	p := guidancePath("roo")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("skill missing: %v", err)
	}
	if !strings.Contains(string(b), "name: deja-history") {
		t.Fatalf("no skill frontmatter:\n%s", b)
	}
	// Mode-specific directories sit beside this one; writing into one of them
	// would make recall advice appear in Code mode and vanish in Ask mode.
	if strings.Contains(p, "skills-") {
		t.Fatalf("guidance landed in a mode-specific directory: %s", p)
	}
	if _, err := os.Stat(rules); !os.IsNotExist(err) {
		left, _ := os.ReadFile(rules)
		t.Fatalf("always-on rules file survived: %q err=%v", left, err)
	}
	// The skill is shared, so it only goes when the last harness reading it
	// does. Record roo as the only one, then take it away.
	recordWiring([]string{"roo"}, false)
	if _, err := installGuidance("roo", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("uninstall left the skill")
	}
}

// Roo only writes MCP settings into hosts it has actually run in, so on a
// machine without one nothing is written — and the message used to be
// "unchanged roo", naming a path that does not exist and explaining nothing.
func TestRooSaysWhyNothingWasWritten(t *testing.T) {
	hermeticEnv(t)
	res, err := installRoo("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "" {
		t.Fatalf("no host means no path to report, got %q", res.Path)
	}
	if !strings.Contains(res.Action, "no Roo host") {
		t.Fatalf("the reader needs to know why: %q", res.Action)
	}
}

// doctor reads past the key at what an entry runs, and calls `deja-vu` deja's
// wiring. install did not: it wrote a second server beside it, so the harness
// started two on every session, one of them possibly pointing at a binary that
// is gone, and said only "updated" (#2712). It now takes the entry over instead
// of adding a sibling (#2269), which answers the same problem without leaving
// two servers behind at all.
func TestInstallAdoptsTheEntryAlreadyRunningDeja(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcpServers":{"deja-vu":{"command":"/old/bin/deja","args":["mcp"],"type":"stdio"}}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "install", "claude-code", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already here") {
		t.Errorf("install did not say it took over the entry it found:\n%s", out)
	}

	servers := claudeMCPServers(t, path)
	if len(servers) != 1 {
		t.Fatalf("the harness is left starting %d deja servers:\n%v", len(servers), servers)
	}
	entry, _ := servers["deja-vu"].(map[string]any)
	if entry == nil {
		t.Fatalf("install renamed the reader's entry instead of updating it:\n%v", servers)
	}
	if cmd, _ := entry["command"].(string); cmd == "/old/bin/deja" {
		t.Errorf("the adopted entry still runs the old binary:\n%v", entry)
	}

	// Not asserted here: what a second run does. install recognises its own
	// entry by the binary's name, and the test binary is called deja.test, so
	// the second run would not know the entry it just wrote. That is the
	// fixture, not the behaviour.
}

func claudeMCPServers(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	return servers
}

// An entry that is not deja's is not reported: the line is about deja's own
// wiring turning up twice, not about what else the reader wired.
func TestInstallSaysNothingAboutSomebodyElsesServer(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcpServers":{"mine":{"command":"/usr/local/bin/mine","args":["serve"]}}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "claude-code", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "also runs deja") {
		t.Errorf("somebody else's server was reported as deja's:\n%s", out)
	}
}

// The name in an argument is not the server. A filesystem server told to serve
// a checkout called deja carries the binary's name in its arguments, and a line
// telling the reader to remove it is deja pointing at a config that is none of
// its business.
func TestInstallDoesNotClaimAServerThatMerelyNamesAPath(t *testing.T) {
	for _, c := range []struct{ name, seed string }{
		{"a path argument", `{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/Users/me/code/deja"]}}}`},
		{"deja run for something else", `{"mcpServers":{"notes":{"command":"deja","args":["--not-mcp"]}}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			path := sources.ClaudeJSONPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := captureRun(t, "install", "claude-code", "--no-index")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "also runs deja") {
				t.Errorf("an entry that only names deja was reported as one:\n%s", out)
			}
		})
	}
}

// Every MCP writer edits the file that would hold the other entry, so every one
// of them has to take it over and say so — the first version told only Claude
// Code's reader, and an earlier one left a second server behind.
func TestEveryMCPWriterAdoptsAnEntryAlreadyRunningDeja(t *testing.T) {
	for _, c := range []struct{ target, file, seed string }{
		{"cursor", ".cursor/mcp.json",
			`{"mcpServers":{"deja-vu":{"command":"/old/bin/deja","args":["mcp"]}}}`},
		{"opencode", ".config/opencode/opencode.json",
			`{"mcp":{"deja-vu":{"type":"local","command":["/old/bin/deja","mcp"]}}}`},
		{"openclaw", ".openclaw/openclaw.json",
			`{"mcp":{"servers":{"deja-vu":{"command":"/old/bin/deja","args":["mcp"]}}}}`},
	} {
		t.Run(c.target, func(t *testing.T) {
			home := hermeticEnv(t)
			path := filepath.Join(home, "home", c.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := captureRun(t, "install", c.target, "--no-index")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "already here") {
				t.Errorf("%s did not say it took the entry over:\n%s", c.target, out)
			}
		})
	}
}

// With two hand-written entries, one is taken over and what is left is named.
func TestTheEntriesLeftBesideTheAdoptedOneAreNamed(t *testing.T) {
	hermeticEnv(t)
	path := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcpServers":{` +
		`"deja-vu":{"command":"/old/bin/deja","args":["mcp"]},` +
		`"deja-old":{"command":"/older/bin/deja","args":["mcp"]}}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "claude-code", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already here") {
		t.Errorf("install did not take over one of them:\n%s", out)
	}
	// The one it did not adopt is still a server the harness will start.
	if !strings.Contains(out, `"deja-vu"`) {
		t.Errorf("the entry left beside the adopted one went unnamed:\n%s", out)
	}
	if !strings.Contains(out, "also runs deja") {
		t.Errorf("nothing said what that entry is:\n%s", out)
	}
}

// Every per-prompt integration has to send the agent session id. The hook skips
// what it already showed a session, keyed by that id, and writes down what it
// shows — with no id both halves are off and the same block goes out again on
// the next message. Measured in deja's own injection log on a real store: of
// 165 blocks, 83 were a word-for-word repeat of an earlier one, and all but
// five of those came within a minute.
func TestPerPromptPayloadsCarryTheSessionID(t *testing.T) {
	for _, tc := range []struct {
		name, src, from, payload string
	}{
		// pi puts no id on the event; it keeps one on the session manager, which
		// every handler reaches through ctx.
		{"pi", piExtensionTS("/bin/deja"), "m.getSessionId ? m.getSessionId() : m.sessionId", "session_id: sessionID"},
		{"omp", ompExtensionJS("/bin/deja"), "m.getSessionId ? m.getSessionId() : m.sessionId", "session_id: sessionID"},
		// OpenClaw's before_prompt_build event has none; it is on ctx (#4581),
		// read through where(), which also names the agent's workspace.
		{"openclaw", openclawPluginJS("/bin/deja"), "ctx?.sessionId", "session_id: at.id"},
	} {
		if !strings.Contains(tc.src, "hook-prompt") {
			t.Fatalf("%s: no per-prompt recall in the generated plugin", tc.name)
		}
		// The field has to be in the payload, not merely mentioned: reading a
		// session id and then not sending it is the same defect.
		if !strings.Contains(tc.src, tc.payload) {
			t.Errorf("%s: payload has no session_id, so recall repeats itself:\n%s",
				tc.name, tc.src)
		}
		// And it has to come from the harness rather than be an empty string
		// threaded through to look right.
		if !strings.Contains(tc.src, tc.from) {
			t.Errorf("%s: the session id is not read from the harness (%s):\n%s",
				tc.name, tc.from, tc.src)
		}
	}
}

// The documented install is `curl -fsSL … | sh`, which makes stdin the script
// itself: [ -t 0 ] is false on a real terminal, so a guard written that way puts
// the PATH offer behind a condition no reader of the README can satisfy. Every
// one of them gets ACTION REQUIRED and edits a dotfile by hand instead — on
// macOS, where ~/.local/bin is not on PATH by default, that is the step between
// installing deja and being able to run it.
//
// The offer reads from the controlling terminal instead. No terminal at all —
// CI, a Dockerfile — still falls through to the message and never blocks.
func TestInstallerAsksThroughTheControllingTerminal(t *testing.T) {
	b, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	// Comments are allowed to name the old condition; code is not.
	var code []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "#") {
			code = append(code, line)
		}
	}
	src := strings.Join(code, "\n")

	if strings.Contains(src, "[ -t 0 ]") {
		t.Error("install.sh gates something on stdin being a terminal; under `curl | sh` it never is")
	}
	if !strings.Contains(src, "read -r answer < /dev/tty") {
		t.Error("the PATH offer does not read from /dev/tty, so a piped install cannot answer it")
	}
	// Prompting with no terminal to prompt on would hang a CI install.
	if !strings.Contains(src, `[ -t 1 ] && [ -r /dev/tty ]`) {
		t.Error("the offer is not guarded by a writable terminal and a readable /dev/tty")
	}
}

// installPipeHelperEnv is the home TestInstallPipeHelper runs in, and the
// switch that makes it run at all: a *_HELPER name is how this package talks
// to a copy of its own binary, and testenv scrubs every other DEJA_ name a
// child could be pointed at.
const installPipeHelperEnv = "DEJA_INSTALL_PIPE_HELPER"

// A stdout that closes early must cost deja its output, not the record of what
// it did (#5322). `deja install aider | head -1` reads one line and closes the
// pipe while the run still has its wiring to write: the config is already
// edited, and recordWiring — a deferred call — never gets there, so uninstall
// is handed a file it has no record of and puts back a file of deja's own
// making. Expected: the closed pipe loses lines, not state.
func TestInstallKeepsWiringWhenStdoutClosesEarly(t *testing.T) {
	hermeticEnv(t)
	home := homeDir()
	conf := filepath.Join(home, ".aider.conf.yml")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("read: CONVENTIONS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var said bytes.Buffer
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInstallPipeHelper$")
	cmd.Env = append(os.Environ(), installPipeHelperEnv+"="+home)
	cmd.Stdout, cmd.Stdin, cmd.Stderr = stdoutW, stdin, &said
	if serr := cmd.Start(); serr != nil {
		t.Fatal(serr)
	}
	// Only the child's ends are in its way now, and a helper that never
	// reaches the barrier is not worth waiting for.
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	_ = stdoutW.Close()
	_ = stdin.Close()
	// One line, the way `head -1` takes one line — the pipe with it, so every
	// line the install prints from here is a write on a closed pipe. The
	// barrier is what keeps this from being a race: without the wait the
	// install starts before the pipe closes, and a child quick enough to
	// finish while the parent is still reading would record its wiring
	// normally and prove nothing.
	line, rerr := bufio.NewReader(stdout).ReadString('\n')
	_ = stdout.Close()
	if rerr != nil || strings.TrimSpace(line) != "ready" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("the helper never reached the install (%v); it said %q; stderr:\n%s",
			rerr, strings.TrimSpace(line), said.String())
	}
	// The install runs now, against a stdout nobody is reading.
	_ = stdinW.Close()
	if werr := cmd.Wait(); werr != nil {
		t.Fatalf("a closed stdout ended the run before it could record the wiring (%v); stderr:\n%s",
			werr, said.String())
	}
	wiring, err := os.ReadFile(filepath.Join(home, ".config", "deja", "wiring.json"))
	if err != nil {
		t.Fatalf("the run left no wiring record: %v; stderr:\n%s", err, said.String())
	}
	if want := aiderReadWasKey + "read: CONVENTIONS.md"; !strings.Contains(string(wiring), want) {
		t.Errorf("wiring.json holds no record of the read: line install promoted (%s):\n%s", want, wiring)
	}
	// The record is what uninstall reads, so the reader's own line comes back.
	var out string
	var runErr error
	captureStderr(t, func() { out, runErr = captureRun(t, "uninstall", "aider") })
	if runErr != nil {
		t.Fatalf("uninstall: %v\n%s", runErr, out)
	}
	restored, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("uninstall left the reader without a config: %v", err)
	}
	if string(restored) != "read: CONVENTIONS.md\n" {
		t.Errorf("the config did not come back as the reader wrote it:\n%q", restored)
	}
}

// TestInstallPipeHelper is the deja process the test above drives. TestMain
// hands this process a home of its own; the one it wants is the test's, which
// holds the .aider.conf.yml install is about to edit.
func TestInstallPipeHelper(t *testing.T) {
	home := os.Getenv(installPipeHelperEnv)
	if home == "" {
		return
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// At the barrier, and saying so. The test closes the pipe only once this
	// line has been read, and lets the install start only after that.
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run([]string{"install", "aider", "--no-index"}); err != nil {
		fmt.Fprintln(os.Stderr, "deja:", err)
		os.Exit(1)
	}
	// os.Exit rather than a return: the framework prints its summary on
	// stdout, which is the pipe the test has just closed. Letting that line
	// decide the exit status would measure the framework, not the install.
	os.Exit(0)
}

// A reader who lists "deja" in plugins.allow, as doctor tells them to
// (#5256), wrote the name into their own config. The first uninstall write
// took that for deja's wiring and deleted the snapshot of it, so the writes
// after it had nothing to hand back and the reader's one-line blocks came
// back split (#5281).
func TestOpenClawUninstallKeepsASnapshotThatOnlyAllowsDeja(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "// user settings\n{\n  // top comment\n" +
		"  \"agents\": {\"defaults\": {\"model\": \"anthropic/x\"}},\n" +
		"  \"plugins\": {\"allow\": [\"my-plugin\", \"deja\"], \"entries\": {\"my-plugin\": {\"enabled\": true}}, \"load\": {\"paths\": [\"/opt/my-plugin\"]}},\n" +
		"  \"hooks\": {\"internal\": {\"enabled\": true, \"entries\": {\"session-memory\": {\"enabled\": true}}}},\n" +
		"  \"mcp\": {\"servers\": {\"zz-mine\": {\"command\": \"my-server\"}}}\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "openclaw-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := captureRun(t, "uninstall", "openclaw-auto"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if got := readFile(t, path); got != seed {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, seed)
	}
	if got := readFile(t, path+".bak"); got != seed {
		t.Errorf("the reader's snapshot is gone or changed:\n%s", got)
	}
}

// The same reader choosing deja as OpenClaw's memory plugin names it as a map
// value, not a list item, and the first uninstall write still deleted the
// snapshot of that config (#5308).
func TestOpenClawUninstallKeepsASnapshotThatOnlyNamesDejaAsTheMemorySlot(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "// user settings\n{\n  // top comment\n" +
		"  \"agents\": {\"defaults\": {\"model\": \"anthropic/x\"}},\n" +
		"  \"plugins\": {\"allow\": [\"my-plugin\"], \"slots\": {\"memory\": \"deja\"}, \"entries\": {\"my-plugin\": {\"enabled\": true}}, \"load\": {\"paths\": [\"/opt/my-plugin\"]}},\n" +
		"  \"hooks\": {\"internal\": {\"enabled\": true, \"entries\": {\"session-memory\": {\"enabled\": true}}}},\n" +
		"  \"mcp\": {\"servers\": {\"zz-mine\": {\"command\": \"my-server\"}}}\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "openclaw-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := captureRun(t, "uninstall", "openclaw-auto"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if got := readFile(t, path); got != seed {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, seed)
	}
	if got := readFile(t, path+".bak"); got != seed {
		t.Errorf("the reader's snapshot is gone or changed:\n%s", got)
	}
}

// The name as a value of the reader's is theirs; the name as a key, or a
// command of deja's, is deja's wiring and its snapshot still goes.
func TestDropOwnBackupTellsTheReadersDejaFromDejas(t *testing.T) {
	for _, tc := range []struct {
		name, bak string
		kept      bool
	}{
		{"allow list naming deja", `{"plugins": {"allow": ["deja"]}}`, true},
		{"commented allow list naming deja", "{\n  // \"deja\" goes here\n  \"plugins\": {\"allow\": [ \"deja\" ]}\n}", true},
		{"memory slot naming deja", `{"plugins": {"slots": {"memory": "deja"}}}`, true},
		{"memory slot naming deja beside deja's entry", `{"plugins": {"slots": {"memory": "deja"}}, "mcp": {"servers": {"deja": {"command": "/opt/x/deja", "args": ["mcp"]}}}}`, false},
		{"deja's plugin package, named by value", `{"name": "deja", "version": "1.0.0"}`, false},
		{"grok's server list, id and label", `{"mcp": {"servers": [{"id": "deja", "label": "deja", "command": "/opt/x/deja"}]}}`, false},
		{"deja's entry", `{"mcpServers": {"deja": {"command": "/opt/x/deja", "args": ["mcp"]}}}`, false},
		{"deja's entry beside the reader's allow", `{"allow": ["deja"], "mcp": {"servers": {"deja": {"command": "/opt/x/deja", "args": ["mcp"]}}}}`, false},
		{"deja's hook behind the reader's &&", `{"allow": ["deja"], "statusLine": {"command": "cd /x && /opt/x/deja statusline"}}`, false},
		{"deja's hook", `{"hooks": [{"command": "/opt/x/deja-hook hook-context --x"}]}`, false},
		{"not JSON, deja's TOML table", "[mcp_servers.deja]\ncommand = \"/opt/x/deja\"\nargs = [\"mcp\"]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			if err := os.WriteFile(path+".bak", []byte(tc.bak), 0o644); err != nil {
				t.Fatal(err)
			}
			dropOwnBackup(path)
			_, err := os.Stat(path + ".bak")
			if kept := err == nil; kept != tc.kept {
				t.Errorf("kept = %v, want %v", kept, tc.kept)
			}
		})
	}
}

// Homes with a space in them are ordinary on macOS ("/Users/John Smith") and
// Windows ("C:\\Program Files"). deja writes its hook as one command string, so
// any harness that splits that string on whitespace runs "/Users/John" with
// "Smith/bin/deja" as an argument. Whatever the answer per harness, the path
// must not be written bare into a place that needs it quoted.
func TestSpaceInTheBinaryPathIsQuotedWhereItMustBe(t *testing.T) {
	const exe = "/Users/John Smith/bin/deja"

	for _, target := range installTargetNames() {
		if !strings.HasSuffix(target, "-auto") {
			continue
		}
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			// VS Code resolves through APPDATA on Windows, and vscode-auto
			// writes there.
			t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			if _, err := installTarget(target, exe, false); err != nil {
				t.Fatalf("install: %v", err)
			}

			_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || strings.HasPrefix(path, filepath.Join(home, "index.db")) {
					return nil //nolint:nilerr // unreadable entries are skipped
				}
				b, rerr := os.ReadFile(path) //nolint:gosec // the walk is over the test's own temp home
				if rerr != nil {
					return nil //nolint:nilerr // unreadable files are skipped
				}
				body := string(b)
				if !strings.Contains(body, "John Smith") {
					return nil
				}
				rel, _ := filepath.Rel(home, path)
				// A shell-quoted or JSON-quoted path is fine. What is not is
				// the path sitting bare in a YAML scalar or a shell line,
				// where the space ends the word.
				for line := range strings.SplitSeq(body, "\n") {
					if !strings.Contains(line, "John Smith") {
						continue
					}
					trimmed := strings.TrimSpace(line)
					quoted := strings.Contains(line, `"`+exe) || strings.Contains(line, `'`+exe) ||
						strings.Contains(line, `"`+exe+` `) || strings.Contains(line, exe+`"`) ||
						strings.Contains(line, `John Smith`) && strings.Count(line, `"`) >= 2
					if !quoted {
						t.Errorf("%s has the path unquoted, so the space ends the word:\n  %s", rel, trimmed)
					}
				}
				return nil
			})
		})
	}
}

// Each host's payload, as captured on its stand or documented, finds the
// session the way Claude's transcript_path does: Qwen sends only session_id,
// Kimi sessionId, Copilot names the session's directory, agy sends
// session_id beside a transcript named transcript.jsonl, and Cursor,
// CodeBuddy and Grok send a transcript path whose name is the id.
func TestStatuslinePayloadsOfEveryHostFindTheSession(t *testing.T) {
	dir := seedTouchedIndex(t, 3, "/w/t/pool.go")
	for name, payload := range map[string]string{
		"qwen":      `{"session_id":"t02","version":"0.20.0","workspace":{"current_dir":"/w/t"}}`,
		"kimi":      `{"model":"m","cwd":"/w/t","sessionId":"t02","version":"2.1.1"}`,
		"copilot":   `{"cwd":"/w/t","session_id":"t02","transcript_path":"/h/.copilot/session-state/t02"}`,
		"cursor":    `{"session_id":"x","transcript_path":"/h/.cursor/projects/w-t/agent-transcripts/t02/t02.jsonl","cwd":"/w/t"}`,
		"codebuddy": `{"hook_event_name":"Status","session_id":"t02","transcript_path":"/h/.codebuddy/projects/w-t/t02.jsonl"}`,
		"agy": `{"conversation_id":"t02","session_id":"t02","transcript_path":"/h/.gemini/antigravity-cli/brain/t02/.system_generated/logs/transcript.jsonl",` +
			`"cwd":"/w/t","workspace":{"current_dir":"/w/t","project_dir":"/w/t"},"agent_state":"idle"}`,
	} {
		in := readStatuslineInput(strings.NewReader(payload))
		if m, ok := statuslineMemory(dir, in); !ok || m.Path != "/w/t/pool.go" {
			t.Errorf("%s: the payload did not find the session (%+v, %v)", name, m, ok)
		}
	}
	// CodeBuddy before the first prompt, Kimi before the first message.
	for _, payload := range []string{`{"session_id":"unknown"}`, `{"sessionId":""}`} {
		if _, ok := statuslineMemory(dir, readStatuslineInput(strings.NewReader(payload))); ok {
			t.Errorf("%s named a session", payload)
		}
	}
}

func hostStatuslineCases(home string) []struct {
	target, path string
	read         func(t *testing.T, path string) string
} {
	jsonAt := func(keys ...string) func(t *testing.T, path string) string {
		return func(t *testing.T, path string) string {
			t.Helper()
			b, err := os.ReadFile(path)
			if err != nil {
				return ""
			}
			var root map[string]any
			if err := json.Unmarshal(b, &root); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			cur := root
			for _, k := range keys {
				next, _ := cur[k].(map[string]any)
				cur = next
			}
			cmd, _ := cur["command"].(string)
			return cmd
		}
	}
	tomlAt := func(table string) func(t *testing.T, path string) string {
		return func(_ *testing.T, path string) string {
			b, _ := os.ReadFile(path)
			return strings.Trim(tomlTableValue(string(b), table, "command"), `"`)
		}
	}
	return []struct {
		target, path string
		read         func(t *testing.T, path string) string
	}{
		{"cursor-auto", filepath.Join(home, ".cursor", "cli-config.json"), jsonAt("statusLine")},
		{"copilot-auto", filepath.Join(home, ".copilot", "settings.json"), jsonAt("statusLine")},
		{"qwen-auto", filepath.Join(home, ".qwen", "settings.json"), jsonAt("ui", "statusLine")},
		{"gigacode-auto", filepath.Join(home, ".gigacode", "settings.json"), jsonAt("ui", "statusLine")},
		{"codebuddy-auto", filepath.Join(home, ".codebuddy", "settings.json"), jsonAt("statusLine")},
		{"grok-auto", filepath.Join(home, ".grok", "config.toml"), tomlAt("ui.status_line")},
		{"kimi-auto", filepath.Join(home, ".kimi-code", "tui.toml"), tomlAt("status_line")},
		{"trae-auto", filepath.Join(home, ".trae", "traecli.toml"), tomlAt("tui.statusline")},
		{"antigravity-auto", filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), jsonAt("statusLine")},
	}
}

// Every host with a command status line gets deja's from its -auto target,
// in the file and under the key its stand read it from, and uninstall takes
// it back out.
func TestAutoTargetsWireTheHostsStatusLine(t *testing.T) {
	hermeticEnv(t)
	home := sources.Home()
	want := hookRun(hookExeFor("/opt/deja/bin/deja", false), "statusline")
	for _, c := range hostStatuslineCases(home) {
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", false); err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		if got := c.read(t, c.path); got != want {
			t.Errorf("%s: status line command = %q in %s, want %q", c.target, got, c.path, want)
		}
		before, _ := os.ReadFile(c.path)
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if after, _ := os.ReadFile(c.path); !bytes.Equal(after, before) {
			t.Errorf("%s: a second install changed %s:\n%s\n---\n%s", c.target, c.path, before, after)
		}
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", true); err != nil {
			t.Fatal(err)
		}
		if got := c.read(t, c.path); got != "" {
			t.Errorf("%s: uninstall left %q", c.target, got)
		}
		// A line deja wrote from a binary that has since moved is deja's, and
		// is rewritten rather than kept as somebody else's.
		seedStatusline(t, c.target, c.path, "/gone/bin/deja statusline")
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if got := c.read(t, c.path); got != want {
			t.Errorf("%s: a moved binary's line stayed %q", c.target, got)
		}
	}
}

func seedStatusline(t *testing.T, target, path, cmd string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	q, jerr := json.Marshal(cmd)
	if jerr != nil {
		t.Fatal(jerr)
	}
	var seed string
	switch {
	case strings.HasSuffix(path, "tui.toml"):
		seed = "[status_line]\ncommand = " + string(q) + "\n"
	case strings.HasSuffix(path, "traecli.toml"):
		seed = "[tui.statusline]\ntype = \"command\"\ncommand = " + string(q) + "\n"
	case strings.HasSuffix(path, ".toml"):
		seed = "[ui.status_line]\ntype = \"command\"\ncommand = " + string(q) + "\n"
	case target == "qwen-auto" || target == "gigacode-auto":
		seed = `{"ui":{"statusLine":{"type":"command","command":` + string(q) + `}}}`
	default:
		seed = `{"statusLine":{"type":"command","command":` + string(q) + `}}`
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A status line somebody set up is theirs: install leaves it and says how to
// run both, and uninstall does not take it.
func TestAutoTargetsLeaveSomeoneElsesStatusLine(t *testing.T) {
	hermeticEnv(t)
	home := sources.Home()
	for _, c := range hostStatuslineCases(home) {
		seedStatusline(t, c.target, c.path, "~/bin/mine.sh")
		r, err := installTarget(c.target, "/opt/deja/bin/deja", false)
		if err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		if got := c.read(t, c.path); got != "~/bin/mine.sh" {
			t.Errorf("%s: install replaced the reader's status line with %q", c.target, got)
		}
		if !strings.Contains(r.Note, "mine.sh") {
			t.Errorf("%s: install did not say how to run both: %+v", c.target, r)
		}
		if _, err := installTarget(c.target, "/opt/deja/bin/deja", true); err != nil {
			t.Fatal(err)
		}
		if got := c.read(t, c.path); got != "~/bin/mine.sh" {
			t.Errorf("%s: uninstall took the reader's status line", c.target)
		}
	}
}

func TestInstallStatuslineErrorAndUninstallIdempotent(t *testing.T) {
	t.Run("malformed settings", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"statusLine":`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installStatusline("/bin/deja", false); err == nil {
			t.Fatal("expected malformed settings.json error")
		}
	})

	t.Run("uninstall preserves foreign statusline", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		old := `{"statusLine":{"type":"command","command":"/bin/other status"}}`
		if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := installStatusline("/bin/deja", true)
		if err != nil {
			t.Fatal(err)
		}
		if r.Action != "unchanged" {
			t.Fatalf("action = %q", r.Action)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "/bin/other status") || strings.Contains(string(b), "/bin/deja statusline") {
			t.Fatalf("settings changed: %s", b)
		}
	})
}

func TestInstallStatuslineInstallConflictAndRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r, err := installStatusline("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "created" {
		t.Fatalf("install result = %#v", r)
	}
	r, err = installStatusline("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "unchanged" {
		t.Fatalf("second install = %#v", r)
	}
	r, err = installStatusline("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "updated" {
		t.Fatalf("uninstall = %#v", r)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "statusLine") {
		t.Fatalf("statusline left in settings: %s", b)
	}

	if _, err := installStatusline("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(path, []byte(`{"statusLine":{"type":"command","command":"/bin/other status"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installStatusline("/bin/deja", false); err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("conflict err = %v", err)
	}
}

func TestInstallTargetErrorsAndAliases(t *testing.T) {
	if _, err := installTarget("missing", "/bin/deja", false); err == nil || !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("unknown target err = %v", err)
	}
	if err := runInstall(index.DefaultDir(), nil, false); err == nil || !strings.Contains(err.Error(), "install needs a target") {
		t.Fatalf("install with no target err = %v", err)
	}
	// Several names are a target list since #3686 — the stale-wiring row asks
	// for exactly that — so two unknown ones are two refusals, each named.
	err := runInstall(index.DefaultDir(), []string{"a", "b"}, false)
	// Nothing was written, so the answer is the two names and nothing about
	// a partial run.
	if err == nil || !strings.Contains(err.Error(), `unknown target "a"`) || !strings.Contains(err.Error(), `unknown target "b"`) ||
		strings.Contains(err.Error(), "finished what it could") {
		t.Fatalf("install args [a b] err = %v", err)
	}
	if err := runInstall(index.DefaultDir(), nil, true); err == nil || !strings.Contains(err.Error(), "uninstall needs a target") {
		t.Fatalf("uninstall err = %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installTarget("claude", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
}

// Config files are very often symlinks into a dotfiles repository. Writing
// through one by rename replaces the link with a regular file: the user's repo
// stops tracking their config, and the next `stow`/`chezmoi` run either
// clobbers deja's wiring or reports a conflict. What deja writes has to land in
// the file the link points at, with the link still a link.
func TestInstallWritesThroughASymlink(t *testing.T) {
	for _, tc := range []struct{ target, rel, seed string }{
		{"qwen-auto", ".qwen/settings.json", "{}\n"},
		{"codex-auto", ".codex/hooks.json", "{}\n"},
		// Not `{}`: deja refuses to append its block to a flow document (#4555).
		{"goose-auto", ".config/goose/config.yaml", "GOOSE_MODE: auto\n"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			setTestHome(t, home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			// The dotfiles repo, and the config linked into place from it.
			repo := filepath.Join(home, "dotfiles")
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			realPath := filepath.Join(repo, filepath.Base(tc.rel))
			if err := os.WriteFile(realPath, []byte(tc.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			link := homeConfigPath(home, tc.rel)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(realPath, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}

			if _, err := installTarget(tc.target, "/usr/local/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}

			fi, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s is no longer a symlink; the dotfiles repo has lost the file", tc.rel)
			}
			b, err := os.ReadFile(realPath)
			if err != nil {
				t.Fatal(err)
			}
			if !dejaWiringPresent(string(b), "/usr/local/bin/deja") {
				t.Fatalf("the wiring did not reach the file the link points at:\n%s", b)
			}
		})
	}
}

func syncTimerHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// The machine that most needs to send is the one nobody is sitting at, so the
// timer has to exist as a file a service manager reads — not as a hook, which
// only fires while someone is working.
func TestInstallSyncTimerWritesAUnitTheSystemReads(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no timer for this platform")
	}
	syncTimerHome(t)
	r, err := installSyncTimer("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action == "unchanged" {
		t.Errorf("the first install wrote nothing")
	}
	if _, serr := os.Stat(r.Path); serr != nil {
		t.Fatalf("the unit is not where install said it is: %v", serr)
	}
	// systemd splits the work in two: install reports the timer, because that
	// is what a person enables, and the command lives in the service beside
	// it. launchd keeps both in one plist.
	unit := r.Path
	if runtime.GOOS == "linux" {
		unit = filepath.Join(syncAutoUnitDir(), "deja-sync.service")
	}
	b, err := os.ReadFile(unit)
	if err != nil {
		t.Fatalf("cannot read %s: %v", unit, err)
	}
	body := string(b)
	// It has to run the bare form and nothing else: that is the one that
	// reaches every machine deja knows and does nothing when there are none.
	// Checked as the whole argument list, because "sync" appears in a unit
	// pinned to one host too.
	if got, want := syncTimerArgs(t, body), []string{"/usr/local/bin/deja", "sync"}; !sameArgs(got, want) {
		t.Errorf("the unit runs %v, want %v:\n%s", got, want, body)
	}
}

func TestInstallSyncTimerIsIdempotentAndReversible(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no timer for this platform")
	}
	syncTimerHome(t)
	first, err := installSyncTimer("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := installSyncTimer("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Action != "unchanged" {
		t.Errorf("a second install rewrote the unit: %s", second.Action)
	}
	if _, err := installSyncTimer("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Errorf("the unit survived the uninstall: %v", err)
	}
}

// Uninstalling on a machine that never installed must not create anything,
// the shape of #676.
func TestUninstallSyncTimerLeavesAnUntouchedMachineAlone(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no timer for this platform")
	}
	home := syncTimerHome(t)
	// The directory a unit would go in already exists: a machine with other
	// launch agents or other systemd units is the ordinary case, and an
	// uninstall that writes an empty file is only invisible when the write
	// happens to fail for want of a directory.
	if err := os.MkdirAll(filepath.Dir(syncAutoPlistPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(syncAutoUnitDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installSyncTimer("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	var wrote []string
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			wrote = append(wrote, path)
		}
		return nil
	})
	if len(wrote) > 0 {
		t.Errorf("uninstall created files on a machine that never installed: %v", wrote)
	}
}

// A home directory is a user-chosen string, and both unit formats are picky in
// different ways: XML refuses an unescaped ampersand, systemd splits ExecStart
// on a space unless the word is quoted.
func TestSyncTimerUnitsSurviveAnAwkwardPath(t *testing.T) {
	exe := `/Users/John & "Jane" Smith/bin/deja`
	plist := syncAutoPlist(exe)
	for _, bad := range []string{`John & "Jane"`} {
		if strings.Contains(plist, bad) {
			t.Errorf("the plist carries the path unescaped, so launchd will refuse it:\n%s", plist)
		}
	}
	if !strings.Contains(plist, "&amp;") || !strings.Contains(plist, "&quot;") {
		t.Errorf("the plist did not escape the path:\n%s", plist)
	}

	service := syncAutoService(exe)
	for line := range strings.SplitSeq(service, "\n") {
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		// systemd takes the first whitespace-delimited word as the binary
		// unless it is quoted, so an unquoted path with a space runs the
		// wrong thing — or nothing. Quoted, and with the quotes inside the
		// path escaped, or the string ends early (#2621).
		if !strings.Contains(line, `"`+systemdEscape(exe)+`"`) {
			t.Errorf("ExecStart does not quote the path: %s", line)
		}
		if strings.Contains(line, `/Users/John & "Jane" Smith/`) {
			t.Errorf("ExecStart carries the quotes unescaped: %s", line)
		}
	}
}

// A platform with no timer must say so. Reporting success where nothing will
// ever run the sync is the failure this whole target exists to prevent — and
// it is checked from every platform, because a refusal that only runs on the
// one machine that cannot schedule is a refusal nobody checks.
func TestSyncTimerRefusesAPlatformItCannotSchedule(t *testing.T) {
	syncTimerHome(t)
	r, err := installSyncTimerFor("plan9", "/usr/local/bin/deja", false)
	if err == nil {
		t.Fatalf("install reported success on a platform with no timer: %+v", r)
	}
	if r.Action != "" {
		t.Errorf("a refusal still claimed to have done something: %q", r.Action)
	}
	if !strings.Contains(err.Error(), "plan9") {
		t.Errorf("the error does not name the platform: %v", err)
	}
}

// Every install target is driven for real by the suite's own invariants
// against a temp home. Without this guard one run of the suite loaded a live
// launch agent into the developer's launchd, pointing at a plist the test then
// deleted.
func TestSyncTimerDoesNotTouchTheRealServiceManager(t *testing.T) {
	if !underTestBinary() {
		t.Fatal("this binary is not recognised as a test binary, so install would call the real service manager")
	}
	out, err := runServiceManager("false")
	if err != nil || out != "" {
		t.Errorf("a service manager command ran under test: %q %v", out, err)
	}
}

// syncTimerArgs pulls the command a unit runs out of either format: the
// <string> entries of a plist's ProgramArguments, or the words of ExecStart.
func syncTimerArgs(t *testing.T, body string) []string {
	t.Helper()
	if strings.Contains(body, "ProgramArguments") {
		_, rest, _ := strings.Cut(body, "ProgramArguments")
		rest, _, _ = strings.Cut(rest, "</array>")
		parts := strings.Split(rest, "<string>")[1:]
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			arg, _, _ := strings.Cut(part, "</string>")
			out = append(out, strings.TrimSpace(arg))
		}
		return out
	}
	for line := range strings.SplitSeq(body, "\n") {
		if v, ok := strings.CutPrefix(line, "ExecStart="); ok {
			return strings.Fields(strings.ReplaceAll(v, `"`, ""))
		}
	}
	t.Fatalf("no command in the unit:\n%s", body)
	return nil
}

func sameArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The target is only offered where deja can actually schedule something.
// Offering one that always fails is worse than not offering it — and the
// suite's own invariants install every target for real, so a target that
// refuses its platform fails them.
func TestSyncTimerIsOfferedOnlyWhereItCanRun(t *testing.T) {
	for goos, want := range map[string]bool{
		"darwin": true, "linux": true,
		"windows": false, "plan9": false, "freebsd": false,
	} {
		if got := syncTimerSchedulable(goos); got != want {
			t.Errorf("syncTimerSchedulable(%q) = %v, want %v", goos, got, want)
		}
	}
	// And the list follows that answer on this machine.
	var listed bool
	for _, n := range installTargetNames() {
		if n == "sync-timer" {
			listed = true
		}
	}
	if listed != syncTimerSchedulable(runtime.GOOS) {
		t.Errorf("sync-timer listed=%v on %s, but schedulable=%v",
			listed, runtime.GOOS, syncTimerSchedulable(runtime.GOOS))
	}
}

// The first command a new machine runs, and it answered with a word the reader
// has to go look up (#830).
func TestInstallWithoutATargetNamesWhatIsHere(t *testing.T) {
	hermeticEnv(t)

	// Nothing installed: point at the list rather than at nothing.
	err := runInstall(t.TempDir(), nil, false)
	if err == nil {
		t.Fatal("install with no target succeeded")
	}
	if !strings.Contains(err.Error(), "deja help") {
		t.Errorf("a bare machine gets no pointer: %v", err)
	}

	// Agents present: name them, and the two bulk flags the README uses.
	home := os.Getenv("HOME")
	for _, d := range []string{".claude", ".codex"} {
		if merr := os.MkdirAll(filepath.Join(home, d), 0o755); merr != nil {
			t.Fatal(merr)
		}
	}
	err = runInstall(t.TempDir(), nil, false)
	if err == nil {
		t.Fatal("install with no target succeeded")
	}
	msg := err.Error()
	for _, want := range []string{"claude-code", "codex", "--all", "--auto"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not name %q: %v", want, err)
		}
	}

	// uninstall says uninstall, not install.
	err = runInstall(t.TempDir(), nil, true)
	if err == nil || !strings.HasPrefix(err.Error(), "uninstall") {
		t.Errorf("uninstall error = %v", err)
	}
}

// installTargetNames feeds `deja help`, shell completion, doctor's coverage
// check, and the expansion uninstall uses to reach hooks. A target implemented
// in installTargetCase but missing from that list is therefore invisible in all
// four at once, and nothing noticed: grok-auto shipped that way, so
// `uninstall --all` left ~/.grok/hooks/deja.json behind pointing at a binary
// the user had just removed, and doctor never checked it.
//
// The other direction is already covered — the help test asserts every listed
// name appears in the help text. This is the missing half: every name the
// table answers to must be listed.
func TestEveryImplementedTargetIsListed(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "install_target_cases.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, n := range installTargetNames() {
		listed[n] = true
	}

	var fn *ast.FuncDecl
	ast.Inspect(file, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == "installTargetCase" {
			fn = d
			return false
		}
		return true
	})
	if fn == nil {
		t.Fatal("installTargetCase not found in install_target_cases.go; this test needs updating")
	}

	// One listed name per writer is enough: several names sharing a writer are
	// aliases of the same install, and advertising every spelling would only
	// pad the help. What must never happen is a writer none of whose names is
	// listed — that is a whole install path nothing can see.
	var groups [][]string
	byWriter := map[string]int{}
	ast.Inspect(fn, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		lit, ok := kv.Key.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil || name == "" {
			return true
		}
		if id, ok := kv.Value.(*ast.Ident); ok {
			if g, seen := byWriter[id.Name]; seen {
				groups[g] = append(groups[g], name)
				return true
			}
			byWriter[id.Name] = len(groups)
		}
		groups = append(groups, []string{name})
		return true
	})
	found := len(groups)
	for _, names := range groups {
		ok := false
		for _, name := range names {
			if listed[name] {
				ok = true
				break
			}
			// A target can be implemented everywhere and offered only where it
			// works. sync-timer answers on every platform — with an
			// explanation naming the one it cannot schedule for, which is
			// better than "unknown target" — but it is listed only where deja
			// can write a unit, because the list is what `--all` installs.
			if name == "sync-timer" && !syncTimerSchedulable(runtime.GOOS) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("installTarget answers to %v but installTargetNames() lists none of them: "+
				"help, completion, doctor and uninstall --all all miss that install", names)
		}
	}
	if found == 0 {
		t.Fatal("no targets read out of installTargetCase; this test needs updating")
	}
}

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
			return nil //nolint:nilerr // unreadable entries are skipped
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
	agents := guidancePath("codex")
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
	realPath := filepath.Join(home, "dotfiles", "skills")
	theirs := filepath.Join(realPath, "their-skill", "SKILL.md")
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
	if err := os.Symlink(realPath, link); err != nil {
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
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		// The store's own state is not wiring: notes, the index, the record of
		// what was installed.
		if strings.Contains(filepath.ToSlash(p), "/.config/deja/") || strings.Contains(filepath.ToSlash(p), "/deja/notes") {
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // the walk is over the test's own temp home
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

// Each install and uninstall below is a deja process of its own: what a run
// created is recorded per process, and carried over, a second install would skip
// the snapshot that a real second run takes.
func anotherRun() {
	createdDirsByThisRun, createdByThisRun, snapshotsByThisRun = nil, nil, nil
	blocksAddedThisRun, blocksForgottenThisRun = nil, map[string]bool{}
}

// A file deja owns whole embeds the binary path. A reinstall from another path
// rewrites it and writeIfChanged snapshots deja's previous text as a .bak; uninstall
// removed the file but left that snapshot and reported it as the reader's (#5299).
func TestUninstallDropsTheSnapshotOfDejasOwnSteeringText(t *testing.T) {
	hermeticEnv(t)
	first := filepath.Join(t.TempDir(), "bin-one", "deja")
	second := filepath.Join(t.TempDir(), "bin-two", "deja")
	for _, exe := range []string{first, second} {
		anotherRun()
		if _, err := installKiro(exe, false); err != nil {
			t.Fatal(err)
		}
	}
	path := kiroSteeringPath()
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("setup: the reinstall from another path should have snapshotted the old text: %v", err)
	}
	anotherRun()
	if _, err := installKiro(second, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("deja's steering file survived uninstall: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("deja's own text was kept as a snapshot of the reader's config: %v", err)
	}
}

// The other side of the same rule: a steering file the reader wrote is replaced by
// install, and uninstall puts it back rather than deleting their snapshot.
func TestUninstallPutsBackTheSteeringFileItReplaced(t *testing.T) {
	hermeticEnv(t)
	path := kiroSteeringPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "# my own steering\nAlways answer in French.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "deja")
	anotherRun()
	if _, err := installKiro(exe, false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != mine {
		t.Fatalf("install kept no copy of the file it replaced: %v", err)
	}
	anotherRun()
	if _, err := installKiro(exe, true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != mine {
		t.Errorf("the reader's steering file was not put back: %v\n%s", err, b)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("the copy was put back and the snapshot left behind: %v", err)
	}
}

func TestSameTextExceptExe(t *testing.T) {
	body := "intro\nrun /new/bin/deja search -- \"q\"\noutro\n"
	for _, c := range []struct {
		name, other string
		want        bool
	}{
		{"identical", body, true},
		{"another binary path", "intro\nrun /old/bin/deja search -- \"q\"\noutro\n", true},
		{"CRLF copy", "intro\r\nrun /old/deja search -- \"q\"\r\noutro\r\n", true},
		{"a reader's edit to another line", "intro\nrun /old/bin/deja search -- \"q\"\nmy note\n", false},
		{"a reader's edit to the exe line itself", "intro\nrun /old/bin/deja find -- \"q\"\noutro\n", false},
		{"a line added", body + "extra\n", false},
	} {
		if got := sameTextExceptExe(body, c.other, "/new/bin/deja"); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if sameTextExceptExe(body, "intro\nrun /old/bin/deja search -- \"q\"\noutro\n", "") {
		t.Error("without an exe only an identical text is deja's")
	}
}

// A config that spells deja's entry as an array of tables, `[[mcp_servers.deja]]`,
// got a plain `[mcp_servers.deja]` appended: tomlInlineKey trimmed the brackets
// off every header, so it read the array as the table deja was about to write,
// and tomlMCPHeader skips `[[` headers, so no deja block was found. TOML forbids
// declaring a key as both, so the file stopped loading in every TOML parser
// (#5297). Refused now with the file untouched, like the inline spellings.
func TestInstallRefusesAnArrayOfTablesNamedLikeDejasTable(t *testing.T) {
	for _, c := range []struct {
		target string
		path   func() string
	}{
		{"codex", func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"grok", func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			seed := "[[mcp_servers.deja]]\ncommand = \"/usr/local/bin/deja\"\nargs = [\"mcp\"]\n"
			if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance")
			if err == nil || !strings.Contains(err.Error(), "array of tables") {
				t.Errorf("install did not refuse the array of tables: %v", err)
			}
			if got, _ := os.ReadFile(p); string(got) != seed {
				t.Errorf("config changed:\n%s", got)
			}
		})
	}
}

// The control: deja's own `[[hooks]]` header is an array of tables by design, so
// a reader's `[[hooks]]` entries stay legal beside the one it appends.
func TestInstallKeepsAReadersHooksArrayOfTables(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.KimiConfigDir(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "default_model = \"kimi-k2\"\n\n[[hooks]]\nevent = \"Stop\"\ncommand = \"my-hook\"\n"
	if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index", "--no-guidance"); err != nil {
		t.Fatalf("install refused a file whose [[hooks]] entries are the reader's own: %v", err)
	}
	if got, _ := os.ReadFile(p); !strings.Contains(string(got), "my-hook") {
		t.Errorf("the reader's hook was lost:\n%s", got)
	}
}

// The TOML side of TestUninstallKeepsTheSnapshotOfAForeignDejaEntry: a
// [mcp_servers.deja] of the reader's own is replaced on install, so after the
// uninstall it has to survive in the config or in its snapshot.
func TestUninstallKeepsTheSnapshotOfAForeignTOMLDejaEntry(t *testing.T) {
	for _, tc := range []struct {
		target string
		path   func(home string) string
	}{
		{"codex", func(h string) string { return filepath.Join(h, ".codex", "config.toml") }},
		{"grok", func(h string) string { return filepath.Join(h, ".grok", "config.toml") }},
		{"trae", func(string) string { return traeConfigPath() }},
	} {
		t.Run(tc.target, func(t *testing.T) {
			home := regressHome(t)
			path := tc.path(home)
			orig := "model = \"x\"\n\n[mcp_servers.deja]\ncommand = \"/usr/local/bin/other-tool\"\nargs = [\"serve\"]\n\n[mcp_servers.deja.env]\nK = \"V\"\n"
			regressWrite(t, path, orig)
			if err := regressRun(t, false, tc.target, "--no-index"); err != nil {
				t.Fatal(err)
			}
			if err := regressRun(t, true, tc.target); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			bak, _ := os.ReadFile(path + ".bak")
			if !strings.Contains(string(got), "other-tool") && !strings.Contains(string(bak), "other-tool") {
				t.Fatalf("the user's own [mcp_servers.deja] is gone from both the config and its snapshot:\nconfig: %s\nbak: %q", got, bak)
			}
		})
	}
}

// kimi and codewhale keep their servers in JSON beside a TOML config.
func TestUninstallKeepsAForeignDejaEntryBesideTOML(t *testing.T) {
	for _, tc := range []struct {
		target, key string
		path        func() string
	}{
		{"kimi", "mcpServers", func() string { return filepath.Join(sources.KimiConfigDir(), "mcp.json") }},
		{"codewhale", "servers", codewhaleMCPPath},
	} {
		t.Run(tc.target, func(t *testing.T) {
			regressHome(t)
			path := tc.path()
			regressWrite(t, path, "{\n  \""+tc.key+"\": {\n    \"deja\": {\"command\": \"/usr/local/bin/other-tool\", \"args\": [\"serve\"]}\n  }\n}\n")
			if err := regressRun(t, false, tc.target, "--no-index"); err != nil {
				t.Fatal(err)
			}
			if err := regressRun(t, true, tc.target); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			bak, _ := os.ReadFile(path + ".bak")
			if !strings.Contains(string(got), "other-tool") && !strings.Contains(string(bak), "other-tool") {
				t.Fatalf("the user's own \"deja\" server is gone:\nconfig: %s\nbak: %q", got, bak)
			}
		})
	}
}

// deja's own block, under any binary name, is still deja's to drop.
func TestHoldsForeignTOMLDejaEntry(t *testing.T) {
	for body, want := range map[string]bool{
		"[mcp_servers.deja]\ntype = \"stdio\"\ncommand = \"/opt/bin/deja\"\nargs = [\"mcp\"]\n":          false,
		"[mcp_servers.deja]\ncommand = \"/opt/bin/renamed\"\nargs = [\n  \"mcp\",\n]\n":                  false,
		"[mcp_servers.deja]\ncommand = \"cmd\"\nargs = [\"/c\", \"deja\", \"mcp\"]\n":                    false,
		"[mcp_servers.deja]\ncommand = \"/usr/local/bin/other-tool\"\nargs = [\"serve\"]\n":              true,
		"[mcp_servers.deja]\nurl = \"https://example.com/mcp\"\n":                                        true,
		"[mcp_servers.other]\ncommand = \"/usr/local/bin/other-tool\"\n\n[tui]\nstatusline = \"deja\"\n": false,
	} {
		if got := holdsForeignDejaEntry([]byte(body)); got != want {
			t.Errorf("holdsForeignDejaEntry(%q) = %v, want %v", body, got, want)
		}
	}
}

// A TOML config that already defines the key inline got a `[mcp_servers.deja]`
// or `[[hooks]]` table appended that redefines it, and codex, grok and kimi
// then refused to load the whole file (#4554). Refused now, file untouched.
func TestInstallRefusesATOMLKeyWrittenInline(t *testing.T) {
	for _, c := range []struct {
		target, seed string
		path         func() string
	}{
		{"codex", "model = \"o3\"\nmcp_servers = { mine = { command = \"my-server\" } }\n",
			func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"grok", "model = \"grok-4\"\nmcp_servers = { mine = { command = \"my-server\" } }\n",
			func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
		{"kimi-auto", "default_model = \"kimi-k2\"\nhooks = []\n",
			func() string { return filepath.Join(sources.KimiConfigDir(), "config.toml") }},
		// deja itself, inline under the table or as dotted keys: the header deja
		// appends redefines that one key, and TOML refuses it the same way.
		{"codex", "[mcp_servers]\ndeja = { command = \"/usr/local/bin/deja\", args = [\"mcp\"] }\n",
			func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"codex", "mcp_servers.deja.command = \"/usr/local/bin/deja\"\n",
			func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"grok", "[mcp_servers]\ndeja = { command = \"/usr/local/bin/deja\" }\n",
			func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance")
			if err == nil || !strings.Contains(err.Error(), "inline") {
				t.Errorf("install did not refuse the inline key: %v", err)
			}
			if got, _ := os.ReadFile(p); string(got) != c.seed {
				t.Errorf("config changed:\n%s", got)
			}
		})
	}
}

// The control: dotted keys are tables TOML lets a header extend.
func TestInstallExtendsDottedTOMLKeys(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.CodexHome(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("mcp_servers.mine.command = \"my-server\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); !strings.Contains(string(got), "[mcp_servers.deja]") {
		t.Errorf("deja's table missing:\n%s", got)
	}
}

func tomlShapeTargets() []struct {
	target string
	path   func() string
} {
	return []struct {
		target string
		path   func() string
	}{
		{"codex", func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"grok", func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
	}
}

func seedTOML(t *testing.T, p, seed string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Codex takes multi-line text in developer_instructions and compact_prompt. A
// line of it starting with `[` was read as a table header: install and
// uninstall refused the file as broken, and a line spelling deja's own header
// had deja's command written over the string, after which codex would not load
// the config (#5158).
func TestTOMLMultilineStringLinesAreText(t *testing.T) {
	seeds := map[string]string{
		"ref-link": "model = \"o3\"\ndeveloper_instructions = \"\"\"\nFollow the style guide.\n[style]: https://example.com/style\n\"\"\"\n\n[mcp_servers.zz-mine]\ncommand = \"my-server\"\n",
		"literal":  "model = \"o3\"\ndeveloper_instructions = '''\nChecklist:\n[1] run the tests\n'''\n\n[mcp_servers.zz-mine]\ncommand = \"my-server\"\n",
		"header":   "compact_prompt = \"\"\"\n[mcp_servers.deja]\nnot a table\n\"\"\"\n\n[mcp_servers.zz-mine]\ncommand = \"my-server\"\n",
	}
	for _, tg := range tomlShapeTargets() {
		for name, seed := range seeds {
			t.Run(tg.target+"/"+name, func(t *testing.T) {
				hermeticEnv(t)
				p := tg.path()
				seedTOML(t, p, seed)
				if _, err := captureRun(t, "install", tg.target, "--no-index", "--no-guidance"); err != nil {
					t.Fatalf("install refused: %v", err)
				}
				got, _ := os.ReadFile(p)
				s := string(got)
				// The string survives whole, and deja's table follows it.
				str, _, _ := strings.Cut(seed, "\n\n")
				if !strings.HasPrefix(s, str+"\n") {
					t.Errorf("the string changed:\n%s", s)
				}
				if n := strings.Count(s[len(str):], "[mcp_servers.deja]"); n != 1 {
					t.Errorf("%d deja tables after the string, want 1:\n%s", n, s)
				}
				if _, err := captureRun(t, "uninstall", tg.target); err != nil {
					t.Fatalf("uninstall refused: %v", err)
				}
				if got, _ := os.ReadFile(p); string(got) != seed {
					t.Errorf("uninstall left:\n%s\nwant:\n%s", got, seed)
				}
			})
		}
	}
}

// `[ mcp_servers.deja ]`, `[mcp_servers . deja]` and `[mcp_servers."deja"]`
// all name the table `[mcp_servers.deja]` does. Install appended a second one
// beside the spaced spellings and codex refused the file with "duplicate key";
// doctor called the machine not wired (#5159).
func TestTOMLHeaderSpellingsNameDejasTable(t *testing.T) {
	for _, tg := range tomlShapeTargets() {
		for _, header := range []string{"[ mcp_servers.deja ]", "[mcp_servers . deja]", `[mcp_servers."deja"]`, "[mcp_servers.'deja']  # mine"} {
			t.Run(tg.target+"/"+header, func(t *testing.T) {
				hermeticEnv(t)
				p := tg.path()
				seedTOML(t, p, "model = \"o3\"\n\n"+header+"\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\nstartup_timeout_sec = 30\n")
				if !doctorTOMLWired(p) {
					t.Errorf("doctor does not see %s", header)
				}
				out, err := captureRun(t, "install", tg.target, "--no-index", "--no-guidance")
				if err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(p)
				s := string(got)
				if strings.Contains(s, "[mcp_servers.deja]") || !strings.Contains(s, header) {
					t.Errorf("a second table beside %s:\n%s", header, s)
				}
				if strings.Contains(s, "/old/deja") || !strings.Contains(s, "startup_timeout_sec = 30") {
					t.Errorf("the table was not updated where it stands:\n%s", s)
				}
				if strings.Contains(out, "instead of adding a second entry") {
					t.Errorf("deja's own table reported as a hand-named one:\n%s", out)
				}
				if _, err := captureRun(t, "uninstall", tg.target); err != nil {
					t.Fatal(err)
				}
				if got, _ := os.ReadFile(p); strings.Contains(string(got), "deja") {
					t.Errorf("uninstall left deja's table:\n%s", got)
				}
			})
		}
	}
}

// The name is a TOML key, so a quoted one with a dot in it is one server, not
// a sub-table of another: uninstall must not take `"deja.old"` for deja's.
func TestTOMLQuotedDottedServerIsNotASubTable(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.CodexHome(), "config.toml")
	seed := "[mcp_servers.\"deja.old\"]\ncommand = \"other-server\"\n"
	seedTOML(t, p, seed)
	if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "codex"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != seed {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, seed)
	}
}

// The keys in deja's own table that deja does not write are the reader's.
// Every install rewrote the table whole and dropped them — a timeout, the
// env_vars that hand deja its index dir (#5124) — while the same keys under a
// hand-named table survived (#5161).
func TestTOMLOwnTableKeepsTheReadersKeys(t *testing.T) {
	for _, tg := range tomlShapeTargets() {
		t.Run(tg.target, func(t *testing.T) {
			hermeticEnv(t)
			p := tg.path()
			kept := []string{"[mcp_servers.deja]  # memory", "startup_timeout_sec = 30", "tool_timeout_sec = 120", `env_vars = ["DEJA_INDEX_DIR"]`, `enabled_tools = ["search"]`}
			seedTOML(t, p, "model = \"o3\"\n\n"+kept[0]+"\ncommand = \"/old/deja\"\nargs = [\n  \"mcp\",\n]\n"+strings.Join(kept[1:], "\n")+"\n\n[mcp_servers.zz-mine]\ncommand = \"my-server\"\n")
			if _, err := captureRun(t, "install", tg.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			s := string(got)
			for _, k := range kept {
				if !strings.Contains(s, k+"\n") {
					t.Errorf("install dropped %q:\n%s", k, s)
				}
			}
			if strings.Contains(s, "/old/deja") || strings.Count(s, "[mcp_servers.deja]") != 1 || strings.Count(s, "args =") != 1 {
				t.Errorf("deja's command was not rewritten in place:\n%s", s)
			}
			if _, err := captureRun(t, "install", tg.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			if again, _ := os.ReadFile(p); string(again) != s {
				t.Errorf("a second install changed the file:\n%s", again)
			}
		})
	}
}

// The control: a table named deja that runs something else is still replaced
// whole — keeping its url beside deja's command would leave codex a server
// with two transports.
func TestTOMLForeignTableUnderDejasNameIsReplaced(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.CodexHome(), "config.toml")
	seedTOML(t, p, "[mcp_servers.deja]\nurl = \"https://example.com/mcp\"\n")
	if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); strings.Contains(string(got), "url =") || !strings.Contains(string(got), "command =") {
		t.Errorf("the foreign table was not replaced:\n%s", got)
	}
}

// A triple quote that opens nothing — inside a one-line string, a literal, or
// escaped in a multi-line one — must not hide the tables after it, or install
// misses deja's own and appends a second one codex refuses (#5158).
func TestTOMLStrayTripleQuoteHidesNoTable(t *testing.T) {
	for name, pre := range map[string]string{
		"literal-in-basic-ml": "developer_instructions = \"\"\"Wrap SQL in ''' fences.\nKeep diffs small.\n\"\"\"\n",
		"in-basic":            "note = \"use ''' for raw\"\n",
		"in-literal":          "note = 'say \"\"\" here'\n",
		"escaped":             "developer_instructions = \"\"\"\nwrite \\\"\"\" for docstrings\n\"\"\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			p := filepath.Join(sources.CodexHome(), "config.toml")
			seed := pre + "\n[mcp_servers.deja]\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\n"
			seedTOML(t, p, seed)
			if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			if n := strings.Count(string(got), "[mcp_servers.deja]"); n != 1 || strings.Contains(string(got), "/old/deja") {
				t.Errorf("%d deja tables, want 1 updated in place:\n%s", n, got)
			}
			if _, err := captureRun(t, "uninstall", "codex"); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); strings.Contains(string(got), "mcp_servers") || !strings.HasPrefix(string(got), pre) {
				t.Errorf("uninstall left:\n%s", got)
			}
		})
	}
}

// An install before #5159 recorded an adopted hand-named table by its header
// as written, quotes and all; uninstall still takes it back.
func TestTOMLUninstallFindsAQuotedRecord(t *testing.T) {
	for _, hdr := range []string{`"deja-vu"`, `'deja-vu'`, `'deja.vu'`} {
		t.Run(hdr, func(t *testing.T) {
			hermeticEnv(t)
			p := filepath.Join(sources.CodexHome(), "config.toml")
			seedTOML(t, p, "model = \"o3\"\n\n[mcp_servers."+hdr+"]\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\n")
			noteBlockAdded(p, "mcp_servers."+hdr)
			if _, err := captureRun(t, "uninstall", "codex"); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); strings.Contains(string(got), "/old/deja") {
				t.Errorf("uninstall left the adopted table:\n%s", got)
			}
		})
	}
}

// A url beside deja's command is two transports; install replaces the table
// whole, as it did before the rewrite kept the reader's keys.
func TestTOMLDejaTableWithAURLIsReplaced(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.CodexHome(), "config.toml")
	seedTOML(t, p, "[mcp_servers.deja]\nurl = \"http://192.0.2.1/mcp\"\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\n")
	if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); strings.Contains(string(got), "url =") {
		t.Errorf("url kept beside deja's command:\n%s", got)
	}
}

// A second install that changes nothing still has a snapshot beside every file
// it wrote the first time, and the kept-snapshot line reads the paths from the
// result. `wroteAll` dropped a path whose action was "unchanged", so the second
// run of grok-auto reported only the hooks file and the two configs vanished
// from the accounting — the same shape #3389 fixed one layer down (review).
func TestASecondInstallStillNamesEveryFileItWrote(t *testing.T) {
	tmp := hermeticEnv(t)
	grok := filepath.Join(tmp, "grok")
	if err := os.MkdirAll(grok, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GROK_ROOT", grok)
	t.Setenv("GROK_HOME", grok)

	first, err := installTarget("grok-auto", "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.touched()) < 3 {
		t.Fatalf("first run touched %v, want the config, the settings and the hooks file", first.touched())
	}
	second, err := installTarget("grok-auto", "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(second.touched(), " ")
	for _, want := range []string{"config.toml", "user-settings.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("second run touched %v, want %s among them", second.touched(), want)
		}
	}
}

// `deja install trae` writes the block `traex mcp add deja -- <deja> mcp`
// writes on traecli 0.207.1: [mcp_servers.deja] in ${TRAE_HOME}/traecli.toml,
// command and args, no type.
func TestInstallTraeMCP(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	cfg := filepath.Join(home, ".trae", "traecli.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "model = \"luna\"\n\n[mcp_servers.other]\ncommand = \"x\"\n"
	if err := os.WriteFile(cfg, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := installTarget("trae", "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Path != cfg {
		t.Fatalf("path = %q, want %q", r.Path, cfg)
	}
	got := readString(t, cfg)
	command, args := mcpCommandArgs("/bin/deja")
	want := fmt.Sprintf("[mcp_servers.deja]\ncommand = %q\nargs = %s\n", command, tomlStringArray(args))
	if !strings.HasPrefix(got, mine) || !strings.Contains(got, want) {
		t.Fatalf("traecli.toml:\n%s", got)
	}
	if strings.Contains(got, "type =") {
		t.Fatalf("traecli.toml carries a type key TRAE does not write:\n%s", got)
	}
	if _, err := installTarget("trae", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, cfg); got != mine {
		t.Fatalf("after uninstall:\n%q\nwant\n%q", got, mine)
	}

	// TRAE_HOME moves the config with the rest of TRAE's home.
	t.Setenv("TRAE_HOME", filepath.Join(home, "th"))
	if r, err := installTarget("trae", "/bin/deja", false); err != nil || r.Path != filepath.Join(home, "th", "traecli.toml") {
		t.Fatalf("TRAE_HOME install = %q, %v", r.Path, err)
	}
}

// `deja install trae-auto` puts Codex's hook set into the hooks.json TRAE
// reads — ${TRAECLI_HOME:-$TRAE_HOME/cli}/hooks.json, the destination `traex
// migrate hooks --user` names — and uninstall returns both files as they were,
// taking with it the trust pins TRAE keeps in traecli.toml.
func TestInstallTraeAuto(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	trae := filepath.Join(home, ".trae")
	hooksPath := filepath.Join(trae, "cli", "hooks.json")
	cfg := filepath.Join(trae, "traecli.toml")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ownHooks := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}` + "\n"
	ownCfg := "model = \"luna\"\n"
	if err := os.WriteFile(hooksPath, []byte(ownHooks), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(ownCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("trae-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(readString(t, hooksPath)), &root); err != nil {
		t.Fatal(err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	for _, h := range codexHookWiring {
		if !strings.Contains(traeJSON(t, hooks[h.Event]), h.Sub) {
			t.Errorf("%s has no %s: %v", h.Event, h.Sub, hooks[h.Event])
		}
	}
	if hooks["Stop"] == nil {
		t.Errorf("the reader's Stop hook is gone: %v", hooks)
	}
	if !strings.Contains(readString(t, cfg), "[mcp_servers.deja]") {
		t.Errorf("traecli.toml has no server:\n%s", readString(t, cfg))
	}
	// Codex's trust is not TRAE's: nothing deja did here may touch it.
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); err == nil {
		t.Errorf("trae-auto wrote codex's hooks.json")
	}

	// TRAE pins each hook it was allowed to run in its own config.
	pin := "\n[hooks.state." + strconv.Quote(hooksPath+":session_start:0:0") + "]\ntrusted_hash = \"sha256:x\"\n"
	if err := os.WriteFile(cfg, []byte(readString(t, cfg)+"\n[hooks.state]\n"+pin), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("trae-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, hooksPath); got != ownHooks {
		t.Errorf("hooks.json after uninstall:\n%q\nwant\n%q", got, ownHooks)
	}
	if got := readString(t, cfg); got != ownCfg {
		t.Errorf("traecli.toml after uninstall:\n%q\nwant\n%q", got, ownCfg)
	}

	// TRAECLI_HOME moves the hooks file, not the config.
	t.Setenv("TRAECLI_HOME", filepath.Join(home, "state"))
	if _, err := installTarget("trae-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "state", "hooks.json")); err != nil {
		t.Errorf("TRAECLI_HOME: %v", err)
	}
}

func traeJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A target nobody has heard of wired nothing, exited 1 — and left the CLI skill
// behind on the way, so a typo created a file the reader never asked for
// (#3583).
func TestAnUnknownTargetWritesNothing(t *testing.T) {
	hermeticEnv(t)
	said, err := captureRunStderr(t, "install", "frobnicate")
	if err == nil {
		t.Fatalf("an unknown target exited 0: %s", said)
	}
	if _, statErr := os.Stat(cliSkillPath()); statErr == nil {
		t.Fatalf("the skill was written for a target that does not exist: %s", cliSkillPath())
	}
	// Nor the install lock: it made ~/.config/deja for a run that wires
	// nothing (#5052).
	if _, statErr := os.Stat(installLockPath()); statErr == nil {
		t.Fatalf("an unknown target created the install lock: %s", installLockPath())
	}
}

// A target that exists still gets it, which is what the file is for.
func TestAKnownTargetStillWritesTheSkill(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRunStderr(t, "install", "claude"); err != nil {
		t.Fatalf("install claude: %v", err)
	}
	if _, err := os.Stat(cliSkillPath()); err != nil {
		t.Fatalf("the skill is missing after a real install: %v", err)
	}
}

// And a run where one target of two is unknown keeps the skill, because the
// other one was wired.
func TestOneGoodTargetAmongBadOnesKeepsTheSkill(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRunStderr(t, "install", "claude"); err != nil {
		t.Fatalf("install claude: %v", err)
	}
	before, err := os.ReadFile(cliSkillPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "deja") {
		t.Fatalf("the skill does not mention deja: %.80s", before)
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A generated layer holds the empty array. A list entry cannot follow it, so
// the literal has to go — otherwise dsh refuses the whole profile and the only
// symptom is a harness that will not boot.
func TestDeepSeekPatchReplacesTheEmptyArray(t *testing.T) {
	got := dshPatchWith("# a comment\n[]\n", dshPatchBlock("/usr/local/bin/deja", false))
	if strings.Contains(got, "[]") {
		t.Errorf("the empty-array literal survived:\n%s", got)
	}
	if !strings.Contains(got, "# a comment") {
		t.Errorf("the user's own line was dropped:\n%s", got)
	}
	// An insert list, not a bare row: a patch entry addresses a row that
	// already exists, and dsh answers `entry "mcp-deja" not found` for one that
	// does not.
	if !strings.Contains(got, "- insert:") {
		t.Errorf("the block is not an insert list:\n%s", got)
	}
	if !strings.Contains(got, "@deepseek-ai/dsh-mcp-client") {
		t.Errorf("the block names no plugin:\n%s", got)
	}
}

func TestDeepSeekPatchIsRewrittenNotRepeated(t *testing.T) {
	first := dshPatchWith("[]\n", dshPatchBlock("/old/deja", false))
	second := dshPatchWith(first, dshPatchBlock("/new/deja", false))
	if strings.Count(second, dshBlockStart) != 1 {
		t.Errorf("the block was added twice:\n%s", second)
	}
	if strings.Contains(second, "/old/deja") {
		t.Errorf("the previous path survived a re-install:\n%s", second)
	}
}

func TestInstallDeepSeekWritesTheHomeLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	r, err := installDeepSeekMCP("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".dsh", "cordis.patch.yml"); r.Path != want {
		t.Fatalf("wrote %q, want the home layer %q", r.Path, want)
	}
	body, err := os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "serverName: deja") {
		t.Errorf("layer does not configure the server:\n%s", body)
	}

	// Uninstall leaves the file loadable rather than empty: a patch file with
	// nothing in it fails the profile load.
	if _, err := installDeepSeekMCP("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "mcp-deja") {
		t.Errorf("uninstall left the entry behind:\n%s", body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("uninstall left %q, want the empty array", strings.TrimSpace(string(body)))
	}
}

// dsh registers slash commands in code, so /deja is a plugin file the profile
// row names by absolute path. Both the shape of that file and the order of the
// two writes were learnt by running dsh: a row naming a missing file, a wrong
// `inject` declaration, or `handle` where it wants `handler` each fail the
// whole profile load rather than skipping the plugin.
func TestInstallDeepSeekWritesTheCommandPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, ".dsh", "plugins", "deja", "command.js")
	body, err := os.ReadFile(plugin)
	if err != nil {
		t.Fatalf("no command plugin: %v", err)
	}
	js := string(body)
	if !strings.Contains(js, "apply.inject") {
		t.Errorf("the plugin declares no dependency, so ctx.commands is unreachable:\n%s", js)
	}
	if !strings.Contains(js, "async handler(") {
		t.Errorf("the command has no handler field dsh accepts:\n%s", js)
	}
	if !strings.Contains(js, `name: "deja"`) {
		t.Errorf("the command is not named deja:\n%s", js)
	}

	layer, err := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// Quoted the way the writer quotes it: a Windows path goes into the layer
	// as "C:\\Users\\…", so comparing against the raw path fails there and
	// passes everywhere else — which is how it reached CI.
	if !strings.Contains(string(layer), yamlQuote(plugin)) {
		t.Errorf("the layer does not name the plugin file:\n%s", layer)
	}

	if _, err := installDeepSeekMCP("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(plugin)); !os.IsNotExist(err) {
		t.Errorf("uninstall left the plugin behind: %v", err)
	}
}

// The pre-step chain answers a decision — { kind: "enter", messages } — not a
// bare list, and returning one without its kind kills the turn. Both the event
// and the shape came from probing a running dsh.
func TestInstallDeepSeekWritesTheAutoRecallPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, ".dsh", "plugins", "deja", "auto.js")
	body, err := os.ReadFile(plugin)
	if err != nil {
		t.Fatalf("no auto-recall plugin: %v", err)
	}
	js := string(body)
	if !strings.Contains(js, `ctx.on("agent/pre-step"`) {
		t.Errorf("the plugin listens on no event that can inject:\n%s", js)
	}
	if !strings.Contains(js, "...decision") {
		t.Errorf("the handler drops the chain's decision, so the turn dies on its kind:\n%s", js)
	}
	if !strings.Contains(js, `"hook-prompt", "--plain"`) {
		t.Errorf("the plugin never asks deja for a block:\n%s", js)
	}
	if !strings.Contains(js, "prompt !== asked") {
		t.Errorf("no guard on the cached prompt, so deja runs again every step:\n%s", js)
	}

	layer, err := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(layer), yamlQuote(plugin)) {
		t.Errorf("the layer does not name the auto-recall plugin:\n%s", layer)
	}
}

// dsh refuses to boot a profile whose plugin list names a file that is not
// there, so the plain target must leave neither the row nor a stale plugin
// behind when someone drops back to it from --auto.
func TestInstallDeepSeekWithoutAutoLeavesNoDanglingPluginRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, ".dsh", "plugins", "deja", "auto.js")
	if _, err := os.Stat(plugin); !os.IsNotExist(err) {
		t.Errorf("auto-recall plugin survived an install without --auto: %v", err)
	}
	layer, err := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(layer), yamlQuote(plugin)) {
		t.Errorf("the layer still names a plugin nobody wrote:\n%s", layer)
	}
	if !strings.Contains(string(layer), yamlQuote(filepath.Join(home, ".dsh", "plugins", "deja", "command.js"))) {
		t.Errorf("dropping --auto took the command plugin with it:\n%s", layer)
	}
}

// Uninstalling on a machine that never had deja must not create the file.
func TestUninstallDeepSeekWithoutALayerWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	r, err := installDeepSeekMCP("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", r.Action)
	}
	if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
		t.Errorf("uninstall created %s", r.Path)
	}
}

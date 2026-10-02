package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

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
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if info.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, _ := os.ReadFile(p)
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
	for k, v := range m {
		out[k] = v
	}
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
	doc["runtime"].(map[string]any)["command"] = gone
	edited, _ := json.MarshalIndent(doc, "", "  ")
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
	for _, line := range strings.Split(out, "\n") {
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

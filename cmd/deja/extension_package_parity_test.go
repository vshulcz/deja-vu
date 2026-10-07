package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The packages under extensions/ are what a harness's own catalog installs,
// and `deja install <harness>-auto` is what the CLI writes. A user gets one or
// the other, so a hook the installer runs and the package does not is a
// feature that exists for half of them. They drifted: on 07.10.2026 the Kimi
// plugin ran hook-prompt alone, the OpenClaw package had no digest and no
// compaction capture, the dsh package nothing after a tool, the Grok plugin
// one hook whose output Grok throws away.
//
// The rule checked here is the one a reader would state: every deja hook the
// installer's files call, the package's files call too. Read off the files on
// both sides, so neither can satisfy it with a list kept beside the code.
func TestExtensionPackagesRunEveryHookTheInstallerDoes(t *testing.T) {
	for _, p := range []struct {
		dir     string
		targets []string
		// Extra files on the package side that belong to it but live
		// outside its directory.
		extra []string
		// Generated sources install picks between at run time: only one is
		// written per machine, and the package has to cover both.
		generated func() []string
		// Hooks the package cannot run, with the host-side reason.
		cannot map[string]string
	}{
		{dir: "kimi", targets: []string{"kimi-auto"}, extra: []string{"kimi.plugin.json"}},
		{dir: "openclaw", targets: []string{"openclaw-auto"}},
		{dir: "dsh", targets: []string{"deepseek-auto"}},
		{dir: "hermes", targets: []string{"hermes-auto"}, cannot: map[string]string{
			// The fix pair rides transform_tool_result, a plugin hook. The
			// catalog package is a memory provider, and Hermes loads one with
			// a context whose register_hook does nothing
			// (plugins/memory/__init__.py _ProviderCollector, 0.17.0).
			"hook-tool-after": "a Hermes memory provider cannot register hooks",
		}},
		{dir: "grok", targets: []string{"grok-auto"}},
		{dir: "opencode", targets: []string{"opencode-auto"}, generated: func() []string {
			return []string{opencodePluginJS("/bin/deja"), opencodeLegacyPluginJS("/bin/deja")}
		}},
		{dir: "pi", targets: []string{"pi-auto"}},
	} {
		t.Run(p.dir, func(t *testing.T) {
			hermeticEnv(t)
			var installed []string
			for _, target := range p.targets {
				if _, err := installTarget(target, "/bin/deja", false); err != nil {
					t.Fatalf("install %s: %v", target, err)
				}
			}
			installed = append(installed, packageTexts(t, filepath.Dir(os.Getenv("DEJA_INDEX_DIR")), nil)...)
			if p.generated != nil {
				installed = append(installed, p.generated()...)
			}
			want := dejaHooksIn(installed)
			if len(want) == 0 {
				t.Fatalf("install %v wrote no deja hook at all; this test is reading the wrong place", p.targets)
			}

			root := filepath.Join("..", "..")
			pkg := packageTexts(t, filepath.Join(root, "extensions", p.dir), map[string]bool{"test": true, "docs": true, "node_modules": true})
			for _, rel := range p.extra {
				b, err := os.ReadFile(filepath.Join(root, rel))
				if err != nil {
					t.Fatal(err)
				}
				pkg = append(pkg, string(b))
			}
			have := dejaHooksIn(pkg)
			var missing []string
			for hook := range want {
				if have[hook] {
					continue
				}
				if _, ok := p.cannot[hook]; ok {
					continue
				}
				missing = append(missing, hook)
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("extensions/%s does not run %s, which `deja install %s` wires — a user of the package goes without it",
					p.dir, strings.Join(missing, ", "), strings.Join(p.targets, ", "))
			}
			// An exception stops being one when the installer stops wiring
			// the hook or the package starts running it.
			for hook, why := range p.cannot {
				if !want[hook] || have[hook] {
					t.Errorf("extensions/%s lists %s as impossible (%s), but that no longer describes the code", p.dir, hook, why)
				}
			}
		})
	}
}

var dejaHookToken = regexp.MustCompile(`\bhook-(?:context|prompt|tool-after|tool|precompact|session-end|spawn)\b`)

// dejaHooksIn is the set of deja hook subcommands named in the texts.
// "hook-tool" is matched only as a whole token, so hook-tool-after does not
// count as it.
func dejaHooksIn(texts []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range texts {
		for _, m := range dejaHookToken.FindAllString(s, -1) {
			out[m] = true
		}
	}
	return out
}

// packageTexts reads every regular file below root, skipping the named
// directories.
func packageTexts(t *testing.T, root string, skip map[string]bool) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && skip[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

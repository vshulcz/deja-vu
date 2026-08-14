package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The bundle and the local installer have to hook the same events. Stating the
// list twice is how they drift: the manifest test named three while the
// installer wired four, so `PreToolUse` could have been dropped from
// plugin.json with every test still green — and plugin users would have lost
// the recall that fires before an edit or a command, silently.
//
// So the expectation is read out of the installer rather than written beside
// it: install into an empty home, see what it wrote, and require the manifest
// to carry the same events with the same matchers.
func TestPluginManifestHooksWhatTheInstallerHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
	if _, err := installClaudeHook("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("the installer wrote no settings: %v", err)
	}
	wired := hookMatchers(t, settings, "deja")
	if len(wired) < 4 {
		t.Fatalf("the installer wired %d events; this test exists because that "+
			"number changes: %v", len(wired), wired)
	}

	manifest := hookMatchers(t, repoFile(t, "claude-plugin/.claude-plugin/plugin.json"),
		"deja.sh")

	for _, event := range sortedKeys(wired) {
		got, ok := manifest[event]
		if !ok {
			t.Errorf("the installer hooks %s and the bundle does not, so a plugin "+
				"user gets less than someone who ran `deja install`", event)
			continue
		}
		// A hook with the wrong matcher fires on nothing and looks healthy in
		// every listing the user can see.
		if got != wired[event] {
			t.Errorf("%s matcher: bundle %q, installer %q", event, got, wired[event])
		}
	}
	for _, event := range sortedKeys(manifest) {
		if _, ok := wired[event]; !ok {
			t.Errorf("the bundle hooks %s and the installer does not; one of them "+
				"is wrong", event)
		}
	}
}

// hookMatchers reads event -> matcher for the entries whose command mentions
// want, so the user's own hooks in the same file are ignored.
func hookMatchers(t *testing.T, body []byte, want string) map[string]string {
	t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	out := map[string]string{}
	for event, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if strings.Contains(h.Command, want) {
					out[event] = g.Matcher
				}
			}
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

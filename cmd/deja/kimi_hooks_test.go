package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kimiUserHooks = `model = "kimi"

# deja: auto-recall (managed by ` + "`deja install kimi-auto`" + `)
[[hooks]]
event = "UserPromptSubmit"
command = "/old/deja hook-prompt --plain"
timeout = 30

# my careful note about the next hook
[[hooks]]
event = "PreToolUse"
command = "my-hook"
`

// The removal ran from deja's marker to the next line starting with "[", so a
// comment the user wrote above the *next* hook went with it (#1699).
func TestRemoveKimiHookKeepsTheNextHooksComment(t *testing.T) {
	got := removeKimiHookBlock(kimiUserHooks)
	if strings.Contains(got, "/old/deja") {
		t.Errorf("deja's own block survived:\n%s", got)
	}
	if !strings.Contains(got, "# my careful note about the next hook") {
		t.Errorf("the user's comment was deleted:\n%s", got)
	}
	if !strings.Contains(got, "my-hook") {
		t.Errorf("the user's hook was deleted:\n%s", got)
	}
}

// Two marked blocks: the first removal consumed the second one's marker and
// left the block behind, so kimi ran recall twice and nothing could find the
// orphan again.
func TestRemoveKimiHookTakesEveryMarkedBlock(t *testing.T) {
	const two = `model = "kimi"

# deja: auto-recall (managed by ` + "`deja install kimi-auto`" + `)
[[hooks]]
event = "UserPromptSubmit"
command = "/a/deja hook-prompt --plain"

# deja: auto-recall (managed by ` + "`deja install kimi-auto`" + `)
[[hooks]]
event = "UserPromptSubmit"
command = "/b/deja hook-prompt --plain"

[[hooks]]
event = "PreToolUse"
command = "my-hook"
`
	got := removeKimiHookBlock(two)
	for _, gone := range []string{"/a/deja", "/b/deja", kimiHookMarker} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived the removal:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "my-hook") {
		t.Errorf("the user's hook was deleted:\n%s", got)
	}
	if n := strings.Count(got, "[[hooks]]"); n != 1 {
		t.Errorf("expected one hook left, found %d:\n%s", n, got)
	}
}

// The control: a block with nothing after it, and a config with no deja block
// at all, are both left sensible.
func TestRemoveKimiHookOnTheOrdinaryShapes(t *testing.T) {
	const last = `model = "kimi"

[[hooks]]
event = "PreToolUse"
command = "my-hook"

# deja: auto-recall (managed by ` + "`deja install kimi-auto`" + `)
[[hooks]]
event = "UserPromptSubmit"
command = "/old/deja hook-prompt --plain"
timeout = 30
`
	got := removeKimiHookBlock(last)
	if strings.Contains(got, "/old/deja") || strings.Contains(got, kimiHookMarker) {
		t.Errorf("deja's block survived:\n%s", got)
	}
	if !strings.Contains(got, "my-hook") {
		t.Errorf("the user's hook was deleted:\n%s", got)
	}
	const none = "model = \"kimi\"\n\n[[hooks]]\nevent = \"PreToolUse\"\ncommand = \"my-hook\"\n"
	if got := removeKimiHookBlock(none); strings.TrimSpace(got) != strings.TrimSpace(none) {
		t.Errorf("a config without deja was changed:\n%s", got)
	}
}

// Kimi's channels, measured on 0.28.1 by reading the requests it sent: the
// digest and the per-prompt block both ride UserPromptSubmit — several hooks on
// that event all run and their output is joined — and PreCompact is wired for
// its side effect, since nothing a hook prints there is read back.
func TestKimiWiresTheThreeChannelsItHas(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installKimiAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	for _, want := range []string{
		`command = "/bin/deja hook-context --plain --once"`,
		`command = "/bin/deja hook-prompt --plain"`,
		`event = "PreCompact"`,
		`command = "/bin/deja hook-precompact"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("missing %s:\n%s", want, cfg)
		}
	}
	// Every block deja writes carries the marker, or the ones without it are
	// left behind on uninstall and keep firing.
	if got, want := strings.Count(cfg, kimiHookMarker), strings.Count(cfg, "[[hooks]]"); got != want {
		t.Fatalf("%d markers for %d hooks:\n%s", got, want, cfg)
	}
	if _, err := installKimiAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if strings.Contains(string(b), "deja") {
		t.Fatalf("uninstall left deja behind:\n%s", b)
	}
}

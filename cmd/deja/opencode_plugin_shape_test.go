package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestOpencodePluginShapeFollowsTheInstalledVersion pins the one thing both
// opencode majors agree on: each refuses the other's file. 1.18.32 rejects a
// default export that carries no server() ("Plugin .../deja.js must default
// export an object with server()"), and 2.0.12 rejects a module with no
// default export at all ("Missing key at [\"default\"]"). A machine still on
// 1.x therefore has to be written the named-export plugin.
func TestOpencodePluginShapeFollowsTheInstalledVersion(t *testing.T) {
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })

	opencodeVersionMajor = func() int { return 1 }
	v1 := opencodePluginJSFor("/bin/deja")
	if !strings.Contains(v1, "export const DejaRecall") {
		t.Errorf("opencode 1.x was written a plugin it cannot load:\n%s", v1)
	}
	if strings.Contains(v1, "export default") {
		t.Error("the 1.x plugin carries a default export, which 1.18.32 rejects unless it has server()")
	}

	opencodeVersionMajor = func() int { return 2 }
	v2 := opencodePluginJSFor("/bin/deja")
	if !strings.Contains(v2, `export default {`) || !strings.Contains(v2, `id: "deja-recall"`) {
		t.Errorf("opencode 2.x was written a plugin it cannot load:\n%s", v2)
	}
}

// TestOpencodeLegacyPluginKeepsItsHooks checks the 1.x template still wires
// what it used to, since it is now reachable only through the version fork and
// nothing else exercises it.
func TestOpencodeLegacyPluginKeepsItsHooks(t *testing.T) {
	js := opencodeLegacyPluginJS("/bin/deja")
	for _, want := range []string{
		"experimental.chat.system.transform",
		"experimental.chat.messages.transform",
		"experimental.session.compacting",
		"tool.execute.before",
		"tool.execute.after",
		"hook-context",
		"hook-prompt",
		"hook-tool-after",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("the 1.x plugin no longer wires %q", want)
		}
	}
}

// TestDoctorCallsTheWrongShapeStale covers the upgrade: the file stays where it
// was, names the hook it always named, and stops being loaded the day opencode
// crosses a major. Reported as wired it hides exactly the machine that just
// lost its memory.
func TestDoctorCallsTheWrongShapeStale(t *testing.T) {
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_OPENCODE_MAJOR", "1")

	opencodeVersionMajor = func() int { return 1 }
	if _, err := installOpencodePlugin("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	row := autoWirings()[0]
	if row.name != "opencode" {
		t.Fatalf("first auto-recall row is %q, not opencode", row.name)
	}
	if got, _ := autoWiringState(row); got != "wired" {
		t.Fatalf("a 1.x plugin on a 1.x machine reads %q, want wired", got)
	}

	opencodeVersionMajor = func() int { return 2 }
	if got, _ := autoWiringState(row); got != "stale" {
		t.Errorf("the 1.x plugin on a 2.x machine reads %q, want stale", got)
	}
}

// Both shapes have to carry the file line, and they are two separate templates:
// the 1.x hook table and the 2.x ctx.tool domain. The seam was scoped to a tool
// name in both, so on opencode a read produced nothing and everything deja
// knows at the point of an action reached every harness except this one.
func TestBothOpencodePluginsCarryTheFileLineBackFromARead(t *testing.T) {
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	for name, js := range map[string]string{
		"1.x": opencodeLegacyPluginJS("/bin/deja"),
		"2.x": opencodePluginJS("/bin/deja"),
	} {
		after := js[strings.Index(js, "execute.after"):]
		if !strings.Contains(after, "filePath") {
			t.Errorf("%s: the after-seam does not read the path the tool was given", name)
		}
		if !strings.Contains(after, "hook-tool") {
			t.Errorf("%s: the after-seam never calls the point-of-action hook", name)
		}
		// Ahead of the gate that keeps the rest of the seam for the shell: the
		// whole failure of this channel was that a file action never got past it.
		shell := strings.Index(after, `!== "bash") return`)
		if shell < 0 {
			shell = strings.Index(after, `!== "shell" ||`)
		}
		if at := strings.Index(after, "fpath"); at < 0 || (shell >= 0 && at > shell) {
			t.Errorf("%s: the file branch sits behind the command gate", name)
		}
	}
}

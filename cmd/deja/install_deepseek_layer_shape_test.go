package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dshLayerHome(t *testing.T, seed string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	path := filepath.Join(home, ".dsh", "cordis.patch.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if seed != "" {
		if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func readLayer(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// dsh applies a layer's patches in order, and a row addressing an id that is
// not inserted yet is dropped ("entry not found"). Re-install appended its
// block after the user's own `disabled: true` row for mcp-deja, so the switch
// stopped applying and deja ran again (#5180).
func TestDeepSeekReinstallKeepsTheBlockAboveTheUsersSwitch(t *testing.T) {
	path := dshLayerHome(t, "- id: timer\n  disabled: true\n")
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	off := "- id: mcp-deja\n  disabled: true\n- id: deja-auto\n  disabled: true\n"
	if err := os.WriteFile(path, []byte(readLayer(t, path)+off), 0o644); err != nil {
		t.Fatal(err)
	}
	if !dshRowDisabled(readLayer(t, path), "mcp-deja") || !dshRowDisabled(readLayer(t, path), "deja-auto") {
		t.Errorf("doctor does not see the user's switch rows after the block:\n%s", readLayer(t, path))
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got := readLayer(t, path)
	block, user := strings.Index(got, dshBlockStart), strings.Index(got, off)
	if block < 0 || user < 0 || block > user {
		t.Fatalf("the block no longer comes before the rows that patch it:\n%s", got)
	}
	if !dshRowDisabled(got, "mcp-deja") {
		t.Errorf("mcp-deja reads on after re-install:\n%s", got)
	}

	// A switch row above the block patches nothing, so it is not off.
	above := "- id: mcp-deja\n  disabled: true\n" + strings.Replace(got, off, "", 1)
	if dshRowDisabled(above, "mcp-deja") {
		t.Errorf("a row above the insert was read as a switch dsh applies:\n%s", above)
	}
	// The later row decides.
	if dshRowDisabled(got+"- id: mcp-deja\n  disabled: false\n", "mcp-deja") {
		t.Errorf("a later `disabled: false` did not turn it back on")
	}
}

// dsh reads the layer as a single document. Appending after a closing `...`
// made a second one, and a column-0 row after a list indented by two spaces
// ended the list; either way dsh refused to start (#5181).
func TestDeepSeekInstallKeepsTheLayerOneParseableList(t *testing.T) {
	for _, tc := range []struct{ name, seed, want string }{
		{"document end", "- id: timer\n  disabled: true\n...\n", "...\n"},
		{"document end and comment", "- id: timer\n  disabled: true\n...\n# trailing note\n", "...\n# trailing note\n"},
		{"indented list", "  - id: timer\n    disabled: true\n", ""},
		{"indented after comment", "# mine\n---\n    - id: timer\n      disabled: true\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := dshLayerHome(t, tc.seed)
			if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			got := readLayer(t, path)
			if tc.want != "" && !strings.HasSuffix(got, dshBlockEnd+"\n"+tc.want) {
				t.Errorf("the block is not inside the document:\n%s", got)
			}
			want := ""
			for _, line := range strings.Split(tc.seed, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "- ") {
					want = line[:yamlIndentWidth(line)]
					break
				}
			}
			for _, line := range strings.Split(got, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "- insert:") && !strings.HasPrefix(line, want+"- insert:") {
					t.Errorf("deja's row is at %q, the list is at %q:\n%s", line, want, got)
				}
			}
			again, err := installDeepSeekAuto("/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if readLayer(t, path) != got {
				t.Errorf("a second install changed the layer (%s):\n%s", again.Action, readLayer(t, path))
			}
			if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if back := readLayer(t, path); back != tc.seed {
				t.Errorf("uninstall left\n%q\nwant\n%q", back, tc.seed)
			}
		})
	}
}

// A read-only layer is refused at its own write, which came after the three
// plugin directories: install said it left the file alone, doctor called
// auto-recall wired, and uninstall said "not installed" and kept them (#5182).
func TestDeepSeekReadOnlyLayerIsRefusedBeforeAnyPlugin(t *testing.T) {
	path := dshLayerHome(t, "- id: timer\n  disabled: true\n")
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if configWritable(path) == nil {
		t.Skip("the read-only mode does not hold here")
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err == nil {
		t.Fatal("install over a read-only layer succeeded")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "plugins")); !os.IsNotExist(err) {
		t.Errorf("plugins were written for a layer deja could not change (stat: %v)", err)
	}
}

// A layer that already holds deja's block is not written, so locking it does
// not stop a re-install from refreshing the plugins.
func TestDeepSeekReinstallOverALockedWiredLayer(t *testing.T) {
	path := dshLayerHome(t, "- id: timer\n  disabled: true\n")
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Errorf("re-install over a locked layer that needs no change: %v", err)
	}
	// Without its final newline the layer still needs no change.
	_ = os.Chmod(path, 0o644)
	if err := os.WriteFile(path, []byte(strings.TrimRight(readLayer(t, path), "\n")), 0o444); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0o444)
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Errorf("re-install over a locked layer with no final newline: %v", err)
	}
}

// What an older install left: its block after `...`. Re-install moves the
// block inside the document, so dsh starts again. A switch it left above the
// block patches nothing; doctor and install say deja is on, as dsh runs it.
func TestDeepSeekReinstallHealsWhatTheAppendBroke(t *testing.T) {
	block := dshPatchBlock("/usr/local/bin/deja", true)
	path := dshLayerHome(t, "- id: timer\n...\n"+block+"\n")
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got := readLayer(t, path); !strings.HasSuffix(got, dshBlockEnd+"\n...\n") {
		t.Errorf("the block stayed after the document end:\n%s", got)
	}

	path = dshLayerHome(t, "- id: mcp-deja\n  disabled: True\n"+block+"\n")
	r, err := installDeepSeekAuto("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := readLayer(t, path); dshRowDisabled(got, "mcp-deja") || strings.Contains(r.Note, switchedOffNote) {
		t.Errorf("a dead row above the block read as off (note %q):\n%s", r.Note, got)
	}
	if !dshRowDisabled(readLayer(t, path)+"- id: mcp-deja\n  disabled: True\n", "mcp-deja") {
		t.Errorf("`disabled: True` below the block is not read as off")
	}

	// A switch in the block that a later row of the user's turns back on: no
	// "switched off" note.
	off := strings.Replace(block, "    - id: mcp-deja\n", "    - id: mcp-deja\n      disabled: true\n", 1)
	dshLayerHome(t, off+"\n- id: mcp-deja\n  disabled: false\n")
	if r, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil || strings.Contains(r.Note, switchedOffNote) {
		t.Errorf("note %q (err %v) for a switch the user turned back on", r.Note, err)
	}
}

// The generated `[]` goes back inside the document on uninstall: after `...`
// the layer would be null, which dsh refuses (#3271).
func TestDeepSeekUninstallPutsTheArrayInsideTheDocument(t *testing.T) {
	for _, seed := range []string{"# generated\n[]\n...\n", "---\n[]\n", "# generated\r\n[]\r\n...\r\n", "---\r\n[]\r\n"} {
		path := dshLayerHome(t, seed)
		if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
			t.Fatal(err)
		}
		if got := readLayer(t, path); got != seed {
			t.Errorf("uninstall left %q, want %q", got, seed)
		}
	}
}

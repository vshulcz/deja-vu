package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plugin written before #4937 tags its notices with the v3 "plugin" source
// kind, which dsh's v4 sessions refuse on append, so every turn it speaks in
// fails. The file still calls deja, so doctor read it as wired and the
// machine looked fine until `deja install deepseek` was run again.
func TestDoctorFlagsADshPluginWithTheRetiredSourceKind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	row := func() string {
		var buf bytes.Buffer
		doctorAutoRecall(&buf)
		for l := range strings.SplitSeq(buf.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "deepseek ") {
				return l
			}
		}
		t.Fatalf("no deepseek row:\n%s", buf.String())
		return ""
	}
	if r := row(); !strings.Contains(r, "wired") {
		t.Fatalf("a fresh install: %q", r)
	}
	path := dshAutoPath()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(b), `source: { kind: "plugin:deja", form: "notice" }`, `source: { kind: "plugin", plugin: "deja", form: "notice" }`, 1)
	if old == string(b) {
		t.Fatal("the template no longer has the source line this test rewrites")
	}
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	r := row()
	if !strings.Contains(r, "stale") || !strings.Contains(r, "deja install deepseek-auto") {
		t.Errorf("the pre-#4937 plugin: %q", r)
	}
	if st, _ := autoWiringState(autoWiring{name: "deepseek", path: dshAutoPath, marker: "hook-prompt"}); st != "stale" {
		t.Errorf("--json state = %q", st)
	}
}

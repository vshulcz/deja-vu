package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What makes the context file reach aider is the read: entry in
// ~/.aider.conf.yml. The row looked at the file alone: wired with the entry
// gone, and missing — the word for never installed — when the entry was there
// and the file was not, which makes aider print an error on every start
// (#4327).
func TestDoctorAiderRowReadsTheConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index"))
	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "aider" {
			row = a
		}
	}
	conf := filepath.Join(home, ".aider.conf.yml")
	ctx := aiderContextPath()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	text := func() string {
		var b bytes.Buffer
		doctorAutoRecall(&b)
		for _, l := range strings.Split(b.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "aider ") {
				return l
			}
		}
		return ""
	}
	cases := []struct {
		name, conf string
		ctxThere   bool
		want       string
	}{
		{"installed", "model: gpt-4o\nread:\n  - " + ctx + "\n", true, "wired"},
		{"entry gone", "model: gpt-4o\n", true, "stale"},
		{"no config", "", true, "stale"},
		{"file gone", "read:\n  - " + ctx + "\n", false, "broken"},
		{"scalar entry", "read: " + ctx + "\n", true, "wired"},
		{"never installed", "", false, "missing"},
		{"another key names it", "notes:\n  - " + ctx + "\n", true, "stale"},
	}
	for _, c := range cases {
		_ = os.Remove(conf)
		_ = os.Remove(ctx)
		if c.conf != "" {
			write(conf, c.conf)
		}
		if c.ctxThere {
			write(ctx, "digest\n")
		}
		if got, _ := autoWiringState(row); got != c.want {
			t.Errorf("%s: state %q, want %q", c.name, got, c.want)
		}
		if l := text(); !strings.Contains(l, " "+c.want+" ") {
			t.Errorf("%s: row %q, want %s", c.name, l, c.want)
		}
	}
}

// A config saved with a byte order mark — PowerShell's default, and install
// writes it back — still has its read: entry on the first line.
func TestDoctorAiderReadsAConfigWithABOM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ctx := aiderContextPath()
	if err := os.MkdirAll(filepath.Dir(ctx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ctx, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aiderConfPath(), []byte("\xef\xbb\xbfread:\n  - "+ctx+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := aiderWiring(true); got != "" {
		t.Errorf("a wired config with a BOM reads as %q", got)
	}
}

package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeProjectAliases(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(ProjectAliasPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectAliasPath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectAliasNamesEveryDirectoryUnderAPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	mono := filepath.Join(home, "mono")
	writeProjectAliases(t, "# one project for the whole tree\n"+
		mono+" = work\n"+
		filepath.Join(mono, "tools")+" = tools\n"+
		"~/trees = work\n"+
		"relative/path = nope\n"+
		filepath.Join(home, "noname")+" =\n"+
		"no separator here\n")

	for _, c := range []struct {
		dir, want string
	}{
		{mono, "work"},
		{filepath.Join(mono, "svc", "api"), "work"},
		{filepath.Join(mono, "tools", "lint"), "tools"},
		{filepath.Join(home, "trees", "task-1", "svc"), "work"},
		// A sibling sharing the prefix as text is another directory.
		{filepath.Join(home, "mono-other", "app"), "mono-other/app"},
		{filepath.Join(home, "noname", "app"), "noname/app"},
		{filepath.Join(home, "relative", "path"), "relative/path"},
	} {
		if got := cwdProjectName(c.dir); got != c.want {
			t.Errorf("cwdProjectName(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
	// A note filed from a directory under the alias lands under its name.
	if got := ClaudeProjectName(filepath.Join(mono, "svc")); got != "work" {
		t.Errorf("ClaudeProjectName under the alias = %q, want work", got)
	}
}

func TestProjectAliasFileEditIsReadWithoutRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, "mono", "svc")
	if _, ok := ProjectAliasFor(dir); ok {
		t.Fatal("an alias with no file")
	}
	writeProjectAliases(t, filepath.Join(home, "mono")+" = work\n")
	if got, _ := ProjectAliasFor(dir); got != "work" {
		t.Fatalf("after writing the file: %q", got)
	}
	writeProjectAliases(t, filepath.Join(home, "mono")+" = jobs\n")
	// The same size, so only the time tells the two apart.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(ProjectAliasPath(), later, later); err != nil {
		t.Fatal(err)
	}
	if got, _ := ProjectAliasFor(dir); got != "jobs" {
		t.Fatalf("after editing the file: %q", got)
	}
	if err := os.Remove(ProjectAliasPath()); err != nil {
		t.Fatal(err)
	}
	if got, ok := ProjectAliasFor(dir); ok {
		t.Fatalf("after removing the file: %q", got)
	}
}

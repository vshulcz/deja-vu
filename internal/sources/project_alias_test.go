package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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

func aliasHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
	return home
}

// A repository excluded by its own name stays out when an alias covering it
// is added later: renamed to the tree's alias it no longer matched the pattern
// and went into the index (#5373).
func TestExcludeFileBeatsTheAlias(t *testing.T) {
	home := aliasHome(t)
	work := filepath.Join(home, "work")
	ledgerd := filepath.Join(work, "svc", "ledgerd")
	api := filepath.Join(work, "svc", "api")
	writeProjectAliases(t, work+" = work\n")
	if err := os.WriteFile(ExcludePath(), []byte("svc/ledgerd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cwdProjectName(ledgerd); got != "svc/ledgerd" {
		t.Errorf("cwdProjectName(excluded repo) = %q, want svc/ledgerd", got)
	}
	if got := ClaudeProjectName(ledgerd); got == "work" {
		t.Errorf("a note from the excluded repo is filed under the alias")
	}
	if got := cwdProjectName(api); got != "work" {
		t.Errorf("cwdProjectName(api) = %q, want work", got)
	}
	kept := FilterSessions([]model.Session{
		{ID: "l", Project: cwdProjectName(ledgerd)},
		{ID: "a", Project: cwdProjectName(api)},
	})
	if len(kept) != 1 || kept[0].ID != "a" {
		t.Errorf("FilterSessions kept %+v, want only the api session", kept)
	}

	// A pattern for the alias name takes out the whole tree.
	if err := os.WriteFile(ExcludePath(), []byte("# private\nwork\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cwdProjectName(api); got != "work" {
		t.Errorf("cwdProjectName(api) = %q, want work", got)
	}
	if kept := FilterSessions([]model.Session{{ID: "a", Project: cwdProjectName(api)}}); len(kept) != 0 {
		t.Errorf("an exclude pattern for the alias left %+v", kept)
	}

	// The variable counts like the file.
	if err := os.Remove(ExcludePath()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "ledgerd")
	if got := cwdProjectName(ledgerd); got != "svc/ledgerd" {
		t.Errorf("with DEJA_EXCLUDE_PROJECTS, cwdProjectName = %q, want svc/ledgerd", got)
	}
}

// A long-lived process names a Claude folder once and keeps the name. Editing
// the alias file has to drop those names, or the folder stays filed under an
// alias that is gone.
func TestAliasEditForgetsCachedFolderNames(t *testing.T) {
	home := aliasHome(t)
	repo := filepath.Join(home, "work", "svc", "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	folder := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '.' || r == '_' {
			return '-'
		}
		return r
	}, repo)
	if resolveEncodedPath(folder) != repo {
		t.Skipf("%s does not resolve back here", folder)
	}
	writeProjectAliases(t, filepath.Join(home, "work")+" = work\n")
	if got := claudeProjectName(folder); got != "work" {
		t.Fatalf("with the alias: %q", got)
	}
	// An exclude edit drops them too: the name decided before it would carry
	// an excluded repository into the index under the alias.
	if err := os.WriteFile(ExcludePath(), []byte("svc/api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeProjectName(folder); got != "svc/api" {
		t.Errorf("after excluding the repository: %q, want svc/api", got)
	}
	if err := os.Remove(ExcludePath()); err != nil {
		t.Fatal(err)
	}
	if got := claudeProjectName(folder); got != "work" {
		t.Fatalf("after dropping the pattern: %q", got)
	}
	if err := os.Remove(ProjectAliasPath()); err != nil {
		t.Fatal(err)
	}
	if got := claudeProjectName(folder); got != "svc/api" {
		t.Errorf("after removing the alias: %q, want svc/api", got)
	}
}

func TestProjectAliasFingerprint(t *testing.T) {
	home := aliasHome(t)
	if got := ProjectAliasFingerprint(); got != "none" {
		t.Fatalf("no file: %q", got)
	}
	writeProjectAliases(t, "# only a comment\n")
	if got := ProjectAliasFingerprint(); got != "none" {
		t.Fatalf("no valid line: %q", got)
	}
	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	writeProjectAliases(t, a+" = one\n"+b+" = two\n")
	first := ProjectAliasFingerprint()
	writeProjectAliases(t, "# reordered\n"+b+" = two\n\n"+a+" = one\n")
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(ProjectAliasPath(), later, later)
	if got := ProjectAliasFingerprint(); got != first {
		t.Errorf("order and comments changed the fingerprint: %q vs %q", got, first)
	}
	writeProjectAliases(t, a+" = one\n"+b+" = three\n")
	_ = os.Chtimes(ProjectAliasPath(), later.Add(time.Hour), later.Add(time.Hour))
	if got := ProjectAliasFingerprint(); got == first || got == "none" {
		t.Errorf("a renamed tree kept the fingerprint %q", got)
	}
}

// The write-back looks for the checkout a codex session ran in by the nearest
// directory carrying its project name. Under an alias every directory carries
// it, so the file's own folder won; it now finds nothing and leaves it to the
// thread's recorded cwd.
func TestSessionDirFromPathsIgnoresTheAlias(t *testing.T) {
	home := aliasHome(t)
	work := filepath.Join(home, "work")
	writeProjectAliases(t, work+" = work\n")
	deep := filepath.Join(work, "svc", "api", "internal", "store")
	s := model.Session{Project: "work", Messages: []model.Message{
		{Role: RoleFiles, Text: filepath.Join(deep, "a.go") + "\n" + filepath.Join(deep, "b.go")},
	}}
	if got := sessionDirFromPaths(s); got == deep {
		t.Errorf("sessionDirFromPaths = %q, the file's folder rather than a checkout", got)
	}
	s.Project = "svc/api"
	s.Messages[0].Text = filepath.Join(work, "svc", "api", "a.go")
	if got := sessionDirFromPaths(s); got != filepath.Join(work, "svc", "api") {
		t.Errorf("a session indexed under its own name: %q", got)
	}
}

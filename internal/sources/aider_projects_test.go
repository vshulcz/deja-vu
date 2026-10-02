package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aider writes its history at the git root it runs in, not in $HOME, so a
// project nobody listed in DEJA_AIDER_ROOTS was never read. `deja aider`
// records the directory it starts aider in (#4326).
func TestAiderFilesReadsRecordedProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_AIDER_ROOTS", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	proj := t.TempDir()
	hist := filepath.Join(proj, ".aider.chat.history.md")
	if err := os.WriteFile(hist, []byte("# aider chat started at 2026-01-01 00:00:00\n#### q\nans\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AiderFiles(); len(got) != 0 {
		t.Fatalf("nothing recorded yet, found %v", got)
	}
	for range 2 {
		if err := RecordAiderProject(proj); err != nil {
			t.Fatal(err)
		}
	}
	if got := AiderFiles(); len(got) != 1 || got[0] != hist {
		t.Fatalf("AiderFiles = %v, want [%s]", got, hist)
	}
	b, err := os.ReadFile(AiderProjectsPath())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), proj); n != 1 {
		t.Fatalf("project recorded %d times, want once:\n%s", n, b)
	}
}

// One history reached under two spellings is one history. A project recorded
// through a symlink, or typed in another case on a case-insensitive disk, and
// also under DEJA_AIDER_ROOTS was read twice, and every session in it was
// indexed twice under two ids.
func TestAiderFilesReadsOneHistoryOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".aider.chat.history.md"), []byte("# aider chat started at 2026-01-01 00:00:00\n#### q\nans\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(proj, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	t.Setenv("DEJA_AIDER_ROOTS", root)
	if err := RecordAiderProject(link); err != nil {
		t.Fatal(err)
	}
	if got := AiderFiles(); len(got) != 1 {
		t.Fatalf("AiderFiles = %v, want the one history once", got)
	}
}

// The list keeps the projects that are still there: a deleted one is dropped
// the next time `deja aider` records a project, so the list does not grow
// with every directory aider was ever started in.
func TestRecordAiderProjectDropsDeletedProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	gone := filepath.Join(t.TempDir(), "gone")
	kept := t.TempDir()
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{gone, kept} {
		if err := RecordAiderProject(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := RecordAiderProject(kept); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(AiderProjectsPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), gone) || !strings.Contains(string(b), kept) {
		t.Fatalf("list after %s was deleted:\n%s", gone, b)
	}
}

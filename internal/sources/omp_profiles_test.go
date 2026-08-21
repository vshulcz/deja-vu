package sources

import (
	"os"
	"path/filepath"
	"testing"
)

func writeOmpSession(t *testing.T, dir, id, cwd string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-08-21T17-14-49-300Z_"+id+".jsonl")
	body := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-08-21T17:14:49.300Z","cwd":"` + cwd + `"}
{"type":"message","id":"u1","timestamp":"2026-08-21T17:14:50.000Z","message":{"role":"user","content":[{"type":"text","text":"which profile am i in"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A named profile relocates omp's whole user scope, sessions included, and an
// omp directory under XDG_DATA_HOME relocates it again — with no `agent`
// segment there. Reading only the default profile answers a person who lives in
// one with an empty history and no reason for it.
func TestOmpSessionRootsCoverProfilesAndXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OMP_ROOT", "")
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)

	writeOmpSession(t, filepath.Join(home, ".omp", "agent", "sessions", "-work-a"), "aaa", "/work/a")
	writeOmpSession(t, filepath.Join(home, ".omp", "profiles", "work", "agent", "sessions", "-work-b"), "bbb", "/work/b")
	writeOmpSession(t, filepath.Join(xdg, "omp", "sessions", "-work-c"), "ccc", "/work/c")
	writeOmpSession(t, filepath.Join(xdg, "omp", "profiles", "side", "sessions", "-work-d"), "ddd", "/work/d")

	if got := len(OmpSessionFiles()); got != 4 {
		t.Fatalf("found %d transcripts, want 4 (default, named profile, XDG, XDG profile):\n%v",
			got, OmpSessionFiles())
	}
	ids := map[string]string{}
	for _, s := range LoadOmp() {
		ids[s.ID] = s.Project
	}
	for _, id := range []string{"aaa", "bbb", "ccc", "ddd"} {
		if _, ok := ids[id]; !ok {
			t.Errorf("session %s was not loaded: %v", id, ids)
		}
	}
	if got := ids["bbb"]; got != "work/b" {
		t.Errorf("profile session project = %q, want work/b", got)
	}
	if got := ids["ddd"]; got != "work/d" {
		t.Errorf("XDG profile session project = %q, want work/d", got)
	}

	// A header without a cwd is what makes the root matter: then the project is
	// read off the directory the transcript sits in, and measuring a profile
	// session against the default root leaves the profile path inside the name.
	dir := filepath.Join(home, ".omp", "profiles", "work", "agent", "sessions", "-work-e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","version":3,"id":"eee","timestamp":"2026-08-21T17:14:49.300Z"}
{"type":"message","id":"u1","timestamp":"2026-08-21T17:14:50.000Z","message":{"role":"user","content":[{"type":"text","text":"no cwd here"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, s := range LoadOmp() {
		if s.ID != "eee" {
			continue
		}
		if s.Project != "work/e" {
			t.Errorf("cwd-less profile session project = %q, want work/e", s.Project)
		}
	}
}

// The override means that directory and nothing else: a test or a relocated
// install should not pick up this machine's own profiles.
func TestOmpRootOverrideIsTheOnlyRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeOmpSession(t, filepath.Join(home, ".omp", "agent", "sessions", "-work-a"), "aaa", "/work/a")

	only := t.TempDir()
	t.Setenv("DEJA_OMP_ROOT", only)
	writeOmpSession(t, filepath.Join(only, "-work-e"), "eee", "/work/e")

	roots := OmpSessionRoots()
	if len(roots) != 1 || roots[0] != only {
		t.Fatalf("roots = %v, want only %q", roots, only)
	}
	ss := LoadOmp()
	if len(ss) != 1 || ss[0].ID != "eee" {
		t.Fatalf("loaded %#v", ss)
	}
}

// XDG only counts when omp actually lives there: the variable is set on most
// Linux desktops, and treating it as a root regardless would walk a directory
// that has nothing to do with omp.
func TestOmpIgnoresXDGWithoutAnOmpDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OMP_ROOT", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	roots := OmpSessionRoots()
	if len(roots) != 1 {
		t.Fatalf("roots = %v, want just the default profile", roots)
	}
}

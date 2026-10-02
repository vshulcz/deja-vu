package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A profile keeps its sessions in its own store, and `hermes --resume <id>`
// looks only in the active profile's: a session recorded under `hermes -p work`
// came back "Session not found" (#4248). A sticky `hermes profile use work` turns
// it around, and the root store then needs `-p default`.
func TestResumeHermesNamesTheProfileTheSessionIsIn(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "hermes")
	t.Setenv("DEJA_HERMES_HOME", home)
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	const id = "20261001_182638_264fd2"
	cmdFor := func(path string) string {
		t.Helper()
		_, cmd, err := resumeCommand(model.Session{ID: id, Harness: "hermes", Project: "p", Path: path})
		if err != nil {
			t.Fatalf("resume %s: %v", path, err)
		}
		return cmd
	}
	profile := filepath.Join(home, "profiles", "work", "state.db")
	root := filepath.Join(home, "state.db")
	if got, want := cmdFor(profile), "hermes -p work --resume "+id; got != want {
		t.Errorf("profile session: %q, want %q", got, want)
	}
	if got, want := cmdFor(root), "hermes --resume "+id; got != want {
		t.Errorf("root session: %q, want %q", got, want)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "active_profile"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := cmdFor(root), "hermes -p default --resume "+id; got != want {
		t.Errorf("root session with work active: %q, want %q", got, want)
	}
	if got, want := cmdFor(profile), "hermes -p work --resume "+id; got != want {
		t.Errorf("profile session with work active: %q, want %q", got, want)
	}
}

// Run as a child of `hermes -p work`, deja inherits HERMES_HOME pointed at the
// profile's own directory, and read that as the root: the root store's sessions
// and the other profiles' came out with bare commands that open work's. Hermes
// takes the root to be two levels up from a directory under `profiles`, and in
// that mode the command always names the profile. A Postgres session keeps the
// command it had; a directory Hermes would not take as a profile name is
// refused rather than handed to -p.
func TestResumeHermesUnderAProfilesHome(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "hermes")
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(root, "profiles", "work"))
	const id = "20261001_182638_264fd2"
	cmdFor := func(path string) (string, error) {
		_, cmd, err := resumeCommand(model.Session{ID: id, Harness: "hermes", Project: "p", Path: path})
		return cmd, err
	}
	for path, want := range map[string]string{
		filepath.Join(root, "state.db"):                      "hermes -p default --resume " + id,
		filepath.Join(root, "profiles", "work", "state.db"):  "hermes -p work --resume " + id,
		filepath.Join(root, "profiles", "other", "state.db"): "hermes -p other --resume " + id,
	} {
		if got, err := cmdFor(path); err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", path, got, err, want)
		}
	}

	t.Setenv("DEJA_HERMES_HOME", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active_profile"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := cmdFor("hermes-pg:abc123"); err != nil || got != "hermes --resume "+id {
		t.Errorf("postgres session: %q, %v; want the plain command", got, err)
	}
	if got, err := cmdFor(filepath.Join(root, "profiles", "Work.Old", "state.db")); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Errorf("a directory Hermes takes for no profile: %q, %v; want a refusal", got, err)
	}
}

// `hermes sessions delete` takes the row out of state.db while deja keeps the
// session searchable, and the printed `hermes --resume` then failed with
// "Session not found" (#4250) — the opencode case of #4205.
func TestResumeRefusesAHermesSessionDeletedInHermes(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "state.db")
	script := `create table sessions(id text primary key, cwd text);
create table messages(id integer primary key, session_id text, role text, content text, timestamp real);
insert into sessions values('20261001_181726_aaaaaa','/tmp/proj');
insert into messages values(1,'20261001_181726_aaaaaa','user','hello',1);`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	gone := model.Session{Harness: "hermes", ID: "20261001_181726_5aedef", Path: db}
	if err := resumeGoneError(gone); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a session hermes no longer has was offered for resume: %v", err)
	}
	kept := model.Session{Harness: "hermes", ID: "20261001_181726_aaaaaa", Path: db}
	if err := resumeGoneError(kept); err != nil {
		t.Fatalf("a session still in hermes was refused: %v", err)
	}
	// No store to ask: nothing is refused on a guess.
	missing := model.Session{Harness: "hermes", ID: "20261001_181726_5aedef", Path: filepath.Join(tmp, "missing.db")}
	if err := resumeGoneError(missing); err != nil {
		t.Fatalf("refused with no store to ask: %v", err)
	}
}

// The profile home Hermes exports may come with a trailing slash, through a
// symlink, or relative to where deja runs; each read as the root before, and a
// store outside the root fell through to `-p default`. Only the root's own
// store is `default`, and what is in neither place keeps the plain command.
func TestResumeHermesProfileHomeAsHermesWritesIt(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(filepath.Join(root, "profiles", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skip("no symlinks here")
	}
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	const id = "20261001_182638_264fd2"
	cmdFor := func(path string) string {
		t.Helper()
		_, cmd, err := resumeCommand(model.Session{ID: id, Harness: "hermes", Project: "p", Path: path})
		if err != nil {
			t.Fatalf("resume %s: %v", path, err)
		}
		return cmd
	}
	rootDB := filepath.Join(root, "state.db")
	workDB := filepath.Join(root, "profiles", "work", "state.db")
	for _, home := range []string{
		filepath.Join(root, "profiles", "work") + string(filepath.Separator),
		filepath.Join(link, "profiles", "work"),
	} {
		t.Setenv("DEJA_HERMES_HOME", home)
		if got, want := cmdFor(rootDB), "hermes -p default --resume "+id; got != want {
			t.Errorf("HERMES_HOME=%s, root store: %q, want %q", home, got, want)
		}
		if got, want := cmdFor(workDB), "hermes -p work --resume "+id; got != want {
			t.Errorf("HERMES_HOME=%s, work store: %q, want %q", home, got, want)
		}
	}
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(root, "profiles", "work"))
	if got, want := cmdFor(filepath.Join(tmp, "elsewhere", "state.db")), "hermes --resume "+id; got != want {
		t.Errorf("a store outside the root: %q, want %q", got, want)
	}
}

// Hermes takes no profile by a reserved name, and `-p default` is the root:
// a directory with one of those names under profiles/ is refused.
func TestResumeHermesRefusesAReservedProfileDirectory(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "hermes")
	t.Setenv("DEJA_HERMES_HOME", root)
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	for _, name := range []string{"default", "hermes", "test", "tmp", "root", "sudo"} {
		path := filepath.Join(root, "profiles", name, "state.db")
		_, cmd, err := resumeCommand(model.Session{ID: "20261001_182638_264fd2", Harness: "hermes", Project: "p", Path: path})
		if err == nil || !strings.Contains(err.Error(), "deja show") {
			t.Errorf("profiles/%s: %q, %v; want a refusal", name, cmd, err)
		}
	}
}

// A profile directory may itself be a symlink — profiles/big moved to a bigger
// disk. Hermes decides the profile on the path as written, so resolving it
// first took big's sessions for strays and printed the plain command, and with
// HERMES_HOME on big the root and the other profiles lost their flag too.
func TestResumeHermesSymlinkedProfile(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(filepath.Join(root, "profiles", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "data", "big"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "data", "big"), filepath.Join(root, "profiles", "big")); err != nil {
		t.Skip("no symlinks here")
	}
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	const id = "20261001_182638_264fd2"
	cmdFor := func(path string) string {
		t.Helper()
		_, cmd, err := resumeCommand(model.Session{ID: id, Harness: "hermes", Project: "p", Path: path})
		if err != nil {
			t.Fatalf("resume %s: %v", path, err)
		}
		return cmd
	}
	bigDB := filepath.Join(root, "profiles", "big", "state.db")
	for _, home := range []string{root, filepath.Join(root, "profiles", "big")} {
		t.Setenv("DEJA_HERMES_HOME", home)
		for path, want := range map[string]string{
			bigDB: "hermes -p big --resume " + id,
			filepath.Join(root, "profiles", "work", "state.db"): "hermes -p work --resume " + id,
		} {
			if got := cmdFor(path); got != want {
				t.Errorf("HERMES_HOME=%s, %s: %q, want %q", home, path, got, want)
			}
		}
	}
	if got, want := cmdFor(filepath.Join(root, "state.db")), "hermes -p default --resume "+id; got != want {
		t.Errorf("HERMES_HOME on big, root store: %q, want %q", got, want)
	}
}

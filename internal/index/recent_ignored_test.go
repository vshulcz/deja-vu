package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2070 put the ignore rule where metas turn into servable sessions so it would
// hold on every surface. The manifest walk was not one of them: `Recent` and the
// per-project variants build their result straight from the manifest, so
// `deja last` led with 253 rows out of 400 from the tree deja says it does not
// recall from, and the same helpers feed the session-start block, the brief,
// handoff, the page and the MCP resource list (#2541).
func TestTheRecentListingSkipsIgnoredSessions(t *testing.T) {
	tmp := t.TempDir()
	jobs := filepath.Join(tmp, "home", ".claude", "jobs", "abc", "projects", "-w-app")
	real := filepath.Join(tmp, "home", "projects", "-w-app")
	for _, d := range []string{jobs, real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	line := func(sid, role, text string) string {
		return `{"type":"` + role + `","sessionId":"` + sid + `","cwd":"/w/app",` +
			`"timestamp":"2026-01-02T03:04:05Z","message":{"role":"` + role +
			`","content":"` + text + `"}}`
	}
	write := func(dir, sid, text string) {
		body := strings.Join([]string{
			line(sid, "user", text),
			line(sid, "assistant", "Decision: we pinned the frobnicator to one shard."),
		}, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(jobs, "scratch", "the frobnicator keeps dropping its widgets under load")
	write(real, "keeper", "the frobnicator keeps dropping its widgets under load")

	// Both roots are read: the ignored one is a subtree of the store, which is
	// how it looks on a real machine.
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "home"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	recent, err := Recent(dir, 50)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the session that is not ignored is listed at all.
	found := false
	for _, s := range recent {
		if s.ID == "keeper" {
			found = true
		}
		if s.ID == "scratch" {
			t.Errorf("the listing served an ignored session: %s (%s)", s.ID, s.Path)
		}
	}
	if !found {
		t.Fatalf("the real session is not in the listing of %d, so this measures nothing", len(recent))
	}

	inProject, err := RecentInProject(dir, "w/app", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range inProject {
		if s.ID == "scratch" {
			t.Errorf("RecentInProject served an ignored session: %s", s.Path)
		}
	}
	byName, err := RecentProject(dir, "app", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range byName {
		if s.ID == "scratch" {
			t.Errorf("RecentProject served an ignored session: %s", s.Path)
		}
	}
	many, err := RecentProjects(dir, []string{"w/app", "app"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range many {
		if s.ID == "scratch" {
			t.Errorf("RecentProjects served an ignored session: %s", s.Path)
		}
	}

	// Named directly, it still answers: a person asking for a session by id is
	// asking for it.
	if _, ok, err := FindByIdentity(dir, "claude", "scratch"); err != nil || !ok {
		t.Errorf("an ignored session named by id did not answer: ok=%v err=%v", ok, err)
	}
}

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ProjectRelevant returns the sessions and, beside them, how many informative
// and strong terms each one holds. The per-prompt hook reads them positionally
// — `for i, s := range ranked { … matched[i] … }` — so the two have to be the
// same length and in the same order.
//
// They are not. The ranking is cut to n as metas, and the sessions are loaded
// afterwards through sessionsServable, which drops what the trust policy
// ignores (#2541) and what a query is not allowed to be served. Every session
// after a dropped one is then judged by its neighbour's counts (#2546).
func TestRelevantCountsStayBesideTheirSessions(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "claude")
	// Both leaves carry the project slug, so the two sessions land in the same
	// project and only the path tells them apart.
	jobs := filepath.Join(root, ".claude", "jobs", "abc", "-w-app")
	real := filepath.Join(root, "-w-app")
	for _, d := range []string{jobs, real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, sid, day, text string) {
		line := func(role, content string) string {
			return `{"type":"` + role + `","sessionId":"` + sid + `","cwd":"/w/app","timestamp":"2026-01-` +
				day + `T03:04:05Z","message":{"role":"` + role + `","content":"` + content + `"}}`
		}
		body := strings.Join([]string{
			line("user", text),
			line("assistant", "Decision: "+text),
		}, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The ignored session holds both rare words, so it ranks first; the two
	// real ones hold one each.
	write(jobs, "scratch", "05", "the quokkactl run hit snorblefunc under load")
	write(real, "keeper", "04", "the quokkactl run needed a second pass")
	write(real, "other", "03", "snorblefunc came back from the vendor tree")
	// Filler, so the two words are rare in this corpus: on a store of three
	// sessions nothing clears the idf floor and the ranking answers nothing.
	for i, topic := range []string{"the sidebar layout", "the invoice renderer", "the webpack config",
		"the login throttle", "the avatar uploader", "the cron scheduler", "the email templates",
		"the feature flags"} {
		write(real, fmt.Sprintf("f%02d", i), "02", "worked on "+topic+" today")
	}

	setHome(t, home)
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Whole-index scope: the claude reader names a session's project after the
	// first directory under its root, so the ignored copy is not in "w/app" —
	// the alignment this measures is the same either way.
	ss, matched, strong, _, err := ProjectRelevant(dir, nil,
		[]string{"quokkactl", "snorblefunc"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the ranking answered with the sessions that are not ignored.
	if len(ss) == 0 {
		t.Fatal("nothing was ranked, so this measures nothing")
	}
	for _, s := range ss {
		if s.ID == "scratch" {
			t.Fatalf("an ignored session was ranked: %s", s.Path)
		}
	}
	if len(matched) != len(ss) || len(strong) != len(ss) {
		t.Fatalf("%d sessions against %d matched and %d strong counts", len(ss), len(matched), len(strong))
	}
	// And the counts are the ones those sessions earned: each real session
	// holds exactly one of the two words.
	for i, s := range ss {
		if matched[i] > 1 {
			t.Errorf("%s was given %d informative terms; it holds one: %s",
				s.ID, matched[i], fmt.Sprint(matched))
		}
	}
}

package docsdate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A commit that rewrites only the sidebar is the one gennav makes on every
// page at once; it must not date the page. A change to the article must.
func TestASidebarOnlyCommitDoesNotDateThePage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	page := filepath.Join(repo, "docs", "guide", "a.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	commit := func(body, when string) {
		t.Helper()
		if err := os.WriteFile(page, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "docs"}, {"commit", "-qm", "c"}} {
			cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
			cmd.Env = append(os.Environ(),
				"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
				"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
				"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
				"GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	commit("<aside>one</aside><article>text</article>", "2026-09-01T12:00:00Z")
	commit("<aside>one two</aside><article>text</article>", "2026-09-20T12:00:00Z")
	day := func() string {
		t.Helper()
		d, err := New(repo)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = d.Close() }()
		got, err := d.Day("docs/guide/a.html")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := day(); got != "2026-09-01" {
		t.Errorf("after a sidebar-only commit the page is dated %s, want 2026-09-01", got)
	}

	commit("<aside>one two</aside><article>new text</article>", "2026-10-02T12:00:00Z")
	if got := day(); got != "2026-10-02" {
		t.Errorf("after an article change the page is dated %s, want 2026-10-02", got)
	}

	if err := os.WriteFile(page, []byte("<aside>x</aside><article>edited</article>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := day(), time.Now().UTC().Format("2006-01-02"); got != want {
		t.Errorf("an uncommitted article edit is dated %s, want today %s", got, want)
	}
}

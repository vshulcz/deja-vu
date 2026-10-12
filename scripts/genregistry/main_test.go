package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A page committed at 00:30 in +03:00 changed on the previous day in UTC, and
// the sitemap has to say that day whatever zone the author or the machine
// running the generator is in. Otherwise a branch made near midnight writes a
// sitemap the "generated docs are current" check disagrees with (#4906, #4908).
func TestLastmodIsTheUTCDayInEveryZone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	t.Setenv("HOME", repo)
	t.Setenv("USERPROFILE", repo)
	git := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		cmd.Env = append(cmd.Env, env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	page := filepath.Join(repo, "docs", "guide", "index.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("<title>guide</title>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(nil, "init", "-q")
	git(nil, "add", "docs")
	const nearMidnight = "2026-10-10T00:30:00+03:00"
	git([]string{"GIT_AUTHOR_DATE=" + nearMidnight, "GIT_COMMITTER_DATE=" + nearMidnight},
		"commit", "-qm", "docs: a page committed near midnight")
	t.Chdir(repo)

	const before = "<url><loc>" + site + "/guide/</loc><lastmod>2026-01-01</lastmod></url>"
	const want = "<url><loc>" + site + "/guide/</loc><lastmod>2026-10-09</lastmod></url>"
	for _, tz := range []string{"Asia/Tokyo", "America/Los_Angeles"} {
		t.Run(tz, func(t *testing.T) {
			t.Setenv("TZ", tz)
			got, err := refreshLastmod(before)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("TZ=%s wrote\n%s\nwant\n%s", tz, got, want)
			}
		})
	}
}

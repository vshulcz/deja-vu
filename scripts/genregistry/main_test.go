package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
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

func TestRunIndexesReadmeOnlyAndPrunesPerHarnessSitemapURLs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	t.Setenv("HOME", repo)
	t.Setenv("USERPROFILE", repo)
	t.Setenv("TZ", "UTC")

	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("docs/registry/registry.json", `{"harnesses":[{"id":"claude","display_name":"Claude Code"},{"id":"codex","display_name":"Codex CLI"}]}`)
	write("docs/registry/README.md", "# Session format registry\n\nOverview.\n")
	write("docs/registry/claude-code.md", "# Claude Code\n\nJSONL format.\n")
	write("docs/registry/codex.md", "# Codex CLI\n\nRollout format.\n")
	write("docs/guide/getting-started.html", "<!DOCTYPE html><head><script type=\"application/ld+json\">{\"datePublished\": \"2026-01-01\", \"dateModified\": \"2026-01-01\"}</script></head><body><main>hi</main></body>\n")
	write("docs/sitemap.xml", `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://vshulcz.github.io/deja-vu/guide/getting-started.html</loc><lastmod>2026-01-01</lastmod></url>
  <url><loc>https://vshulcz.github.io/deja-vu/registry/claude-code.html</loc><lastmod>2026-01-01</lastmod></url>
</urlset>
`)

	git := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		cmd.Env = append(cmd.Env, env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(nil, "init", "-q")
	git(nil, "add", "docs")
	const seedDay = "2026-10-10T12:00:00Z"
	git([]string{"GIT_AUTHOR_DATE=" + seedDay, "GIT_COMMITTER_DATE=" + seedDay},
		"commit", "-qm", "seed docs")

	t.Chdir(repo)
	if err := run(); err != nil {
		t.Fatalf("run(): %v", err)
	}

	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}

	readmeHTML := read("docs/registry/README.html")
	if !strings.Contains(readmeHTML, `<meta name="robots" content="index,follow,max-image-preview:large,max-snippet:-1">`) {
		t.Errorf("README.html missing index robots meta")
	}
	claudeHTML := read("docs/registry/claude-code.html")
	if !strings.Contains(claudeHTML, `<meta name="robots" content="noindex,follow">`) {
		t.Errorf("claude-code.html missing noindex robots meta")
	}

	sitemapXML := read("docs/sitemap.xml")
	if !strings.Contains(sitemapXML, "<loc>https://vshulcz.github.io/deja-vu/registry/README.html</loc>") {
		t.Errorf("sitemap.xml missing registry/README.html:\n%s", sitemapXML)
	}
	if strings.Contains(sitemapXML, "registry/claude-code.html") {
		t.Errorf("sitemap.xml still contains pruned registry/claude-code.html:\n%s", sitemapXML)
	}

	sitemapTXT := read("docs/sitemap.txt")
	if !strings.Contains(sitemapTXT, "https://vshulcz.github.io/deja-vu/registry/README.html") {
		t.Errorf("sitemap.txt missing registry/README.html:\n%s", sitemapTXT)
	}
	if strings.Contains(sitemapTXT, "registry/claude-code.html") {
		t.Errorf("sitemap.txt still contains pruned registry/claude-code.html:\n%s", sitemapTXT)
	}

	guideHTML := read("docs/guide/getting-started.html")
	if !strings.Contains(guideHTML, `"dateModified": "2026-10-10"`) {
		t.Errorf("getting-started.html dateModified not refreshed:\n%s", guideHTML)
	}
}

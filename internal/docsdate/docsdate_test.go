package docsdate

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A new harness changes the count in about a hundred page bodies and nothing
// else in them. A changed benchmark figure is still a change.
func TestAHarnessCountChangeDoesNotDateThePage(t *testing.T) {
	before := []byte(`<article><p>the transcripts of forty-two agents, 四十二个工具, 42 agent stores, <span class="fig">42</span>, and 41 others / 39 more. 97.2% R@5</p></article>`)
	after := []byte(`<article><p>the transcripts of forty-three agents, 四十三个工具, 43 agent stores, <span class="fig">43</span>, and 42 others / 40 more. 97.2% R@5</p></article>`)
	if !bytes.Equal(Content(before), Content(after)) {
		t.Errorf("a count-only change reads as new content:\n%s\n%s", Content(before), Content(after))
	}
	bench := []byte(`<article><p>the transcripts of forty-three agents, 四十三个工具, 43 agent stores, <span class="fig">43</span>, and 42 others / 40 more. 97.4% R@5</p></article>`)
	if bytes.Equal(Content(bench), Content(after)) {
		t.Error("a changed benchmark figure reads as no change")
	}
}

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
			cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
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
	if out, err := exec.CommandContext(t.Context(), "git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
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

// Most pages have an <article>, but not all of them, and a page cut off
// mid-tag is still a page: what is compared falls back to the page without its
// sidebar, and then to the whole page, rather than to nothing.
func TestContentWithoutAWholeArticle(t *testing.T) {
	for _, tc := range []struct{ page, want string }{
		{"<article>text</article>", "<article>text"},
		{"<h1>t</h1><aside>nav</aside><p>text</p>", "<h1>t</h1><p>text</p>"},
		{"<article>text<aside>nav</aside><p>more</p>", "<article>text<p>more</p>"},
		{"<p>text</p><aside>nav", "<p>text</p><aside>nav"},
		{"<p>text</p>", "<p>text</p>"},
	} {
		if got := string(Content([]byte(tc.page))); got != tc.want {
			t.Errorf("Content(%q) = %q, want %q", tc.page, got, tc.want)
		}
	}
}

// blob reads what `git cat-file --batch` prints. A path the commit does not
// have is an empty page; anything else that is not a header and a body of the
// size it gives is an error, not a short page that would date wrong.
func TestBlobReadsTheCatFileBatchFormat(t *testing.T) {
	for _, tc := range []struct {
		name, out, want string
		fails           bool
	}{
		{"blob", "abc blob 5\nhello\n", "hello", false},
		{"missing", "abc:docs/a.html missing\n", "", false},
		{"bad header", "fatal: not a valid object name\n", "", true},
		{"bad size", "abc blob five\nhello\n", "", true},
		{"short body", "abc blob 50\nhello\n", "", true},
		{"no header", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var in bytes.Buffer
			d := &Dater{in: nopCloser{&in}, out: bufio.NewReader(strings.NewReader(tc.out))}
			got, err := d.blob("abc", "docs/a.html")
			if tc.fails {
				if err == nil {
					t.Errorf("read %q from %q, want an error", got, tc.out)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("read %q, want %q", got, tc.want)
			}
			if in.String() != "abc:docs/a.html\n" {
				t.Errorf("asked cat-file for %q", in.String())
			}
		})
	}
	d := &Dater{in: failWriter{}, out: bufio.NewReader(strings.NewReader("abc blob 5\nhello\n"))}
	if _, err := d.blob("abc", "docs/a.html"); err == nil {
		t.Error("a request cat-file never got was not an error")
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (failWriter) Close() error              { return nil }

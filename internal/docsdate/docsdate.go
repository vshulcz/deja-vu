// Package docsdate dates a docs page by the last commit that changed what it
// says, not the last commit that touched the file.
//
// The two drifted apart because of the sidebar. gennav writes the same <aside>
// into every guide page, so a commit that adds one page to the sidebar rewrites
// all eighty, and the sitemap told crawlers that every page on the site had
// changed that day. A lastmod that moves on every page at once is one a crawler
// learns to ignore. The article is what a reader came for, so the article is
// what is compared; a page with no <article> is compared without its <aside>.
package docsdate

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNoHistory is a tracked file with no commit behind it in this clone.
var ErrNoHistory = errors.New("no commit in this clone touched")

// Dater answers for one repository. It keeps a `git cat-file --batch` open,
// because walking back past sidebar-only commits reads dozens of old copies of
// each page.
type Dater struct {
	root string
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
}

// New starts the reader for the repository at root.
func New(root string) (*Dater, error) {
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	return &Dater{root: root, cmd: cmd, in: in, out: bufio.NewReader(out)}, nil
}

// Close stops the reader.
func (d *Dater) Close() error {
	_ = d.in.Close()
	return d.cmd.Wait()
}

// Day is the UTC day of the newest commit that changed file's content, with
// file relative to the root in forward slashes. A file whose content differs
// from HEAD's is today: the commit that will carry it does not exist yet. A
// tracked file with no commit in this clone is an error, because that is what
// a shallow clone or an export looks like, and answering anyway would date the
// page wrong. A file that is not there is "".
func (d *Dater) Day(file string) (string, error) {
	cur, err := os.ReadFile(filepath.Join(d.root, filepath.FromSlash(file)))
	if err != nil {
		return "", nil
	}
	log, err := exec.Command("git", "-C", d.root, "log", "--format=%H %at", "--", file).Output()
	if err != nil {
		return "", fmt.Errorf("git log %s: %w", file, err)
	}
	lines := strings.Fields(strings.TrimSpace(string(log)))
	if len(lines) == 0 {
		tracked, _ := exec.Command("git", "-C", d.root, "ls-files", "--", file).Output()
		if len(bytes.TrimSpace(tracked)) == 0 {
			return today(), nil // never committed
		}
		return "", fmt.Errorf("%w: %s", ErrNoHistory, file)
	}
	type rev struct {
		hash string
		at   int64
	}
	var revs []rev
	for i := 0; i+1 < len(lines); i += 2 {
		at, err := strconv.ParseInt(lines[i+1], 10, 64)
		if err != nil {
			return "", fmt.Errorf("git log %s: %q is not a Unix time", file, lines[i+1])
		}
		revs = append(revs, rev{lines[i], at})
	}

	newest, err := d.blob(revs[0].hash, file)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(Content(cur), Content(newest)) {
		return today(), nil
	}
	prev := Content(newest)
	for i := 0; i+1 < len(revs); i++ {
		older, err := d.blob(revs[i+1].hash, file)
		if err != nil {
			return "", err
		}
		c := Content(older)
		if !bytes.Equal(c, prev) {
			return day(revs[i].at), nil
		}
		prev = c
	}
	return day(revs[len(revs)-1].at), nil
}

// blob reads file as it was at commit; a commit that deleted it reads as empty.
func (d *Dater) blob(commit, file string) ([]byte, error) {
	if _, err := fmt.Fprintf(d.in, "%s:%s\n", commit, file); err != nil {
		return nil, err
	}
	head, err := d.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	f := strings.Fields(head)
	if len(f) == 2 && f[1] == "missing" {
		return nil, nil
	}
	if len(f) != 3 {
		return nil, fmt.Errorf("git cat-file %s:%s: %q", commit, file, head)
	}
	n, err := strconv.Atoi(f[2])
	if err != nil {
		return nil, err
	}
	b := make([]byte, n+1) // and the newline after it
	if _, err := io.ReadFull(d.out, b); err != nil {
		return nil, err
	}
	return b[:n], nil
}

// Content is the part of a page that dates it: the <article> when there is
// one, otherwise the page without its <aside>.
func Content(page []byte) []byte {
	if i := bytes.Index(page, []byte("<article")); i >= 0 {
		if j := bytes.LastIndex(page, []byte("</article>")); j > i {
			return page[i:j]
		}
	}
	if i := bytes.Index(page, []byte("<aside")); i >= 0 {
		if j := bytes.Index(page[i:], []byte("</aside>")); j >= 0 {
			out := append([]byte{}, page[:i]...)
			return append(out, page[i+j+len("</aside>"):]...)
		}
	}
	return page
}

func day(unix int64) string { return time.Unix(unix, 0).UTC().Format("2006-01-02") }

func today() string { return time.Now().UTC().Format("2006-01-02") }

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// editSurfaceRepo makes the repository a harness fixture edits in, so `files`
// keeps its paths, and runs the commands from outside it.
func editSurfaceRepo(t *testing.T, tmp string) string {
	t.Helper()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	filesAskFromNowhere(t, tmp)
	return repo
}

// assertEditSurfaces checks that what a harness's reader records about one
// edit reaches the four surfaces built on it: restore hands the replaced span
// back, files lists the file beside the topic, the after-compaction block names
// it, and blame attributes a commit that removed the span to the session
// (#595).
func assertEditSurfaces(t *testing.T, harness, id, file, span, topic string) {
	t.Helper()
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(file)

	var out bytes.Buffer
	if err := runRestore(dir, []string{base}, &out); err != nil {
		t.Fatalf("restore: %v", err)
	}
	listing := out.String()
	if !strings.Contains(listing, "replaced spans recorded") || !strings.Contains(listing, harness) {
		t.Fatalf("restore lists no %s span for %s:\n%s", harness, base, listing)
	}
	found := false
	for n := 1; n <= strings.Count(listing, " B "); n++ {
		out.Reset()
		if err := runRestore(dir, []string{base, "--span", fmt.Sprint(n)}, &out); err != nil {
			t.Fatalf("restore --span %d: %v", n, err)
		}
		if strings.TrimSuffix(out.String(), "\n") == span {
			found = true
		}
	}
	if !found {
		t.Fatalf("restore never hands back %q", span)
	}

	out.Reset()
	if err := runFiles(dir, []string{topic}, &out); err != nil {
		t.Fatalf("files: %v", err)
	}
	if !strings.Contains(out.String(), base) {
		t.Fatalf("files %q does not list %s:\n%s", topic, base, out.String())
	}

	block := compactEvidence(dir, id, "")
	if !strings.Contains(block, "files it touched") || !strings.Contains(block, base) {
		t.Fatalf("the after-compaction block does not name %s:\n%s", base, block)
	}

	s, ok, err := index.FindByIDPreferProject(dir, id, "")
	if err != nil || !ok {
		t.Fatalf("session %s not in the index: %v", id, err)
	}
	line := strings.SplitN(span, "\n", 2)[0]
	commit := lineCommit{SHA: "abcdef12", When: time.Now().Add(24 * 365 * time.Hour)}
	target := search.BlameTarget{FullPath: file, Base: base, Line: 1}
	author, ok := attributeLine([]model.Session{s}, target, commit, map[string]bool{blameSpanKey(line): true})
	if !ok || author.Session.ID != id || author.Wrote {
		t.Fatalf("blame does not attribute the removed line to %s: %+v ok=%v", id, author.Session, ok)
	}
}

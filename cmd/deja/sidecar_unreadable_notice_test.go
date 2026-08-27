package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A sidecar that will not parse turns semantic search off. Falling back to the
// lexical order is right; doing it in silence is not — the same path says so
// when the endpoint is the missing piece, and doctor is a screen someone opens
// after noticing (#2201).
func TestAnUnreadableSidecarIsNamedOnTheSearchPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Cut off after the header, which is what a partial copy leaves.
	if err := os.WriteFile(embed.Path(dir), []byte("DJVE\x01\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := embed.Read(dir); err == nil {
		t.Fatal("the fixture parses, so this measures nothing")
	}

	said := func(run func(notice *os.File)) string {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "notice")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		run(f)
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// The note is once per process, so each case starts from an unspoken one.
	reset := func() { saidSidecarUnreadable = false }
	t.Cleanup(reset)

	hits := []search.Hit{{Session: model.Session{Harness: "claude", ID: "a1", Project: "app"}}}
	reset()
	rerank := said(func(notice *os.File) {
		maybeRerank(dir, hits, search.Options{Query: "pool"}, notice)
	})
	if !strings.Contains(rerank, "sidecar") || !strings.Contains(rerank, "deja embed") {
		t.Errorf("a rerank that gave up on an unreadable sidecar says %q", rerank)
	}
	reset()
	semantic := said(func(notice *os.File) {
		maybeSemantic(dir, nil, search.Options{Query: "pool"}, notice)
	})
	if !strings.Contains(semantic, "sidecar") || !strings.Contains(semantic, "deja embed") {
		t.Errorf("a semantic search that gave up on an unreadable sidecar says %q", semantic)
	}

	// One search runs the rerank and then the semantic tier when it found
	// nothing. One broken file is one fact.
	reset()
	both := said(func(notice *os.File) {
		maybeRerank(dir, nil, search.Options{Query: "pool"}, notice)
		maybeSemantic(dir, nil, search.Options{Query: "pool"}, notice)
	})
	if n := strings.Count(both, "will not parse"); n != 1 {
		t.Errorf("one search said it %d times:\n%s", n, both)
	}

	// A directory with no sidecar at all is the ordinary case and stays quiet.
	empty := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	reset()
	quiet := said(func(notice *os.File) {
		maybeRerank(empty, hits, search.Options{Query: "pool"}, notice)
		maybeSemantic(empty, nil, search.Options{Query: "pool"}, notice)
	})
	if quiet != "" {
		t.Errorf("nothing is embedded here and deja said %q", quiet)
	}
}

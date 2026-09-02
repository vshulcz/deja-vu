package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The injection log records which projects a digest was built from, and the
// cache is the path almost every session takes: a miss happens once per
// project, a hit happens every time after. Dropping the names on the way in
// made every one of those hits read as an injection from no project at all
// (#2889).
func TestTheHookCacheKeepsTheProjectsBehindTheDigest(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(tmp, "proj")
	writeHookCache(dir, cwd, "a digest", 2, 10, nil, 0, []string{"claude:s1"}, []string{"acme-api"})

	_, _, _, _, _, ids, projects := cachedHookDigestFor(dir, cwd)
	if len(ids) != 1 || ids[0] != "claude:s1" {
		t.Fatalf("session ids came back as %v", ids)
	}
	if len(projects) != 1 || projects[0] != "acme-api" {
		t.Fatalf("projects came back as %v, want the name the digest was built from", projects)
	}
}

package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Resuming a session leaves it out of its own digest, and a recent session is
// usually in the cached one, so every resume of it rebuilt the digest on the
// startup path; the refresh then cached it with that session in again, and
// the next resume rebuilt as well (#4224). The refresh caches the digest with
// each served session left out, so a resume reads it like any other start,
// and a first start renders only the digest it serves.
func TestResumingAServedSessionReadsTheDigestCache(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	now := time.Now()
	seedClaudeAt(t, claude, "app", "older-session", "the ledger export drops the last row", "the writer missed a final flush", now.Add(-2*time.Hour))
	seedClaudeAt(t, claude, "app", "live-session", "the glimmerquest cache misses on every cold start", "warming it at boot from the last snapshot fixed it", now.Add(-time.Minute))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	const cwd = "/tmp/app"
	before := hookDigestRenders.Load()
	_, _, _, _, _, ids, _ := cachedHookDigestFor(dir, cwd, "")
	if !slices.Contains(ids, "live-session") {
		t.Fatalf("the cached digest does not serve the recent session: %v", ids)
	}
	if n := hookDigestRenders.Load() - before; n != 1 {
		t.Fatalf("a first start rendered %d digests on the hook's path, want the 1 it serves", n)
	}
	// What the detached refresh the first start asked for writes.
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	runHookRefresh(dir)
	want, wantSessions, _, _, _, wantIDs, _ := hookDigestResultFor(dir, cwd, "live-session")

	resume := func(round string) {
		t.Helper()
		before := hookDigestRenders.Load()
		got, sessions, _, _, _, gotIDs, _ := cachedHookDigestFor(dir, cwd, "live-session")
		if n := hookDigestRenders.Load() - before; n != 0 {
			t.Fatalf("%s: resuming a served session built the digest %d times, want a cache read", round, n)
		}
		if strings.Contains(got, "glimmerquest") {
			t.Fatalf("%s: the session was served its own history:\n%s", round, got)
		}
		if !strings.Contains(got, "ledger export") || sessions != wantSessions || !slices.Equal(gotIDs, wantIDs) {
			t.Fatalf("%s: served %d sessions %v, a fresh build without it serves %d %v:\n%s\nwant:\n%s", round, sessions, gotIDs, wantSessions, wantIDs, got, want)
		}
	}
	resume("after the first refresh")
	runHookRefresh(dir)
	resume("after another refresh")

	// A new session still gets the whole digest from the same entry.
	if d, _, _, _, _, _, _ := cachedHookDigestFor(dir, cwd, "brand-new-session"); !strings.Contains(d, "glimmerquest") {
		t.Fatalf("a new session lost the recent one from the digest:\n%s", d)
	}
}

// The refresh rewrites the cache while hooks read it, and the entry carries a
// digest per served session: a hook that opened it mid-write read a torn
// entry. The new entry is renamed into place, so an open reader keeps the
// whole old one.
func TestTheHookCacheIsReplacedNotRewritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	const cwd = "/tmp/app"
	writeHookCache(dir, cwd, "the old digest", 1, 10, nil, 0, []string{"s1"}, nil)
	old, err := os.ReadFile(hookCachePath(dir, cwd))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(hookCachePath(dir, cwd))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	writeHookCache(dir, cwd, "the new digest, longer than the old one", 2, 20, nil, 0, []string{"s1", "s2"}, nil)
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(old) {
		t.Fatalf("a reader that opened the old entry read:\n%s\nwant the old entry whole:\n%s", got, old)
	}
}

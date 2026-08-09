package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestFixPairsKeepTheCommandThatSettledTheError(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: "timeout 5 curl example.internal", Time: now},
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "curl --max-time 5 example.internal", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "200 OK", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d", len(pairs))
	}
	if pairs[0].Command != "curl --max-time 5 example.internal" {
		t.Errorf("wrong command stored: %q", pairs[0].Command)
	}
}

// The command that did not settle it is not a fix: the same error right after
// it means the session was still failing.
func TestFixPairsDropACommandTheErrorSurvived(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "timeout 5 curl example.internal", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now.Add(2 * time.Minute)},
	}
	if pairs := fixPairsIn(ms, "claude:s1"); len(pairs) != 0 {
		t.Errorf("a command the error outlived was stored as a fix: %+v", pairs)
	}
}

// Sequence alone is 13% precise on a real store — the next command is usually
// the session moving on. A pair survives the build only with a second reason:
// the command names what the error named, or the same remedy recurs.
func TestBuildFixesDropsTheUnrelatedNextCommand(t *testing.T) {
	now := time.Now()
	unrelated := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now},
			{Role: "command", Text: "git status --short", Time: now.Add(time.Minute)},
		},
	}
	related := model.Session{
		Harness: "claude", ID: "s2",
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now},
			{Role: "command", Text: "brew services start postgresql && psql -c 'select 1'", Time: now.Add(time.Minute)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{unrelated, related}, func(s model.Session) string { return s.Harness + ":" + s.ID })
	got := ReadFixes(dir)
	if len(got) != 1 {
		t.Fatalf("want only the related pair, got %d: %+v", len(got), got)
	}
	if got[0].Key != "claude:s2" {
		t.Errorf("kept the wrong pair: %+v", got[0])
	}
	// And a lookup finds it from the error text alone, wherever in a paste the
	// line sits.
	found := FixesFor(dir, "traceback follows\npsql: connection refused on port 5432\n", 3)
	if len(found) != 1 {
		t.Errorf("the pair is not findable from the pasted error: %+v", found)
	}
}

// A command run after an error can carry a live secret. It is redacted in the
// record log; the mined pairs must be scrubbed the same way, or `deja fix`
// hands the secret straight back to an agent. This drives a real index build
// through the same path almost everyone builds their first index on.
func TestFixPairsAreRedactedLikeTheRecordLog(t *testing.T) {
	dir := t.TempDir()
	secret := "ghp_AbCdEf0123456789AbCdEf0123456789abcd"
	ss := []model.Session{{
		Harness: "claude", ID: "leak", Project: "p",
		Messages: []model.Message{
			{Role: "tool-output", Text: "fatal: could not read Username for github"},
			{Role: "command", Text: "git remote set-url origin https://" + secret + "@github.com/me/repo"},
			{Role: "tool-output", Text: "ok"},
		},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	for _, p := range ReadFixes(dir) {
		if strings.Contains(p.Command, secret) {
			t.Fatalf("mined fix pair carries a live secret: %q", p.Command)
		}
	}
	// And the pair is still there — redaction must not drop it.
	if len(ReadFixes(dir)) == 0 {
		t.Fatal("redaction dropped the pair entirely")
	}
}

// fixes.gob is written only by a full rebuild, so an index built by a version
// that predates it must be treated as stale on upgrade rather than answer
// `deja fix` with a false "no session ran a command after that error". Bumping
// the index version is what forces that one rebuild; guard both halves.
func TestUpgradedIndexRebuildsForFixes(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "s", Project: "p",
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432"},
			{Role: "command", Text: "brew services start postgresql && psql -c 'select 1'"},
			{Role: "tool-output", Text: "ok"},
		},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	// A full build writes the sidecar and stamps the current version.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != version {
		t.Fatalf("fresh build stamped version %d, want %d", m.Version, version)
	}
	if got := FixesFor(dir, "psql: connection refused on port 5432", 3); len(got) == 0 {
		t.Fatal("fresh build did not mine the fix pair")
	}
	// A store stamped by the previous version must not be judged fresh, so
	// Ensure re-ingests it and mines the sidecar it never had. If fixes.gob is
	// ever added without a version bump, manifestFresh returns true here and an
	// upgraded user is stuck with an empty `deja fix`.
	m.Version = version - 1
	if manifestFresh(m, m.Files, "") {
		t.Fatal("a store from the previous version is judged fresh — deja fix stays empty on upgrade")
	}
}

package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestForgetTombstoneLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index"))
	dir := DefaultDir()
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeRecord(f, Record{Key: "claude:abc123", Role: "user", Text: "one", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRecord(f, Record{Key: "claude:keep", Role: "user", Text: "two", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{
		"claude:abc123": {ID: "abc123", Harness: "claude", Project: "secret", Updated: time.Now()},
		"claude:keep":   {ID: "keep", Harness: "claude", Project: "public", Updated: time.Now()},
	}}); err != nil {
		t.Fatal(err)
	}
	r, err := Forget(dir, ForgetOptions{Session: "abc", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Sessions != 1 || r.Tombstones != 1 || len(Tombstones()) != 0 {
		t.Fatalf("dry run=%#v tombstones=%v", r, Tombstones())
	}
	r, err = Forget("", ForgetOptions{Project: "SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Sessions != 1 || r.Messages != 1 || r.Tombstones != 1 {
		t.Fatalf("forget=%#v", r)
	}
	if got := Tombstones(); len(got) != 1 || got[0] != "claude:abc123" {
		t.Fatalf("tombstones=%v", got)
	}
	if err := Unforget("abc123"); err != nil {
		t.Fatal(err)
	}
	if len(Tombstones()) != 0 {
		t.Fatal("unforget did not remove tombstone")
	}
}

func TestOldManifestRedactionCompatibility(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeGob(filepath.Join(dir, "manifest.gob"), manifestCore{Version: 9}); err != nil {
		t.Fatal(err)
	}
	if err := writeGob(filepath.Join(dir, "sessions.gob"), map[string]SessionMeta{}); err != nil {
		t.Fatal(err)
	}
	stats, err := Redactions(dir)
	if err != nil || stats.Total != 0 || len(stats.Rules) != 0 {
		t.Fatalf("stats=%#v err=%v", stats, err)
	}
	if _, err := RedactionReport(dir); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyHelpers(t *testing.T) {
	meta := SessionMeta{ID: "abcdef", Project: "My-App", Updated: time.Unix(20, 0)}
	if !sessionMatches(meta, ForgetOptions{Session: "abc"}) || !sessionMatches(meta, ForgetOptions{Project: "app"}) || !sessionMatches(meta, ForgetOptions{Before: time.Unix(21, 0)}) {
		t.Fatal("selectors did not match")
	}
	if sessionMatches(meta, ForgetOptions{Session: "zzz"}) || sessionMatches(meta, ForgetOptions{Project: "none"}) || sessionMatches(meta, ForgetOptions{Before: time.Unix(10, 0)}) || sessionMatches(meta, ForgetOptions{}) {
		t.Fatal("selectors matched unexpectedly")
	}
	ss := []model.Session{{Harness: "a", ID: "one"}, {Harness: "a", ID: "two"}}
	if got := filterTombstonedSet(ss, nil); len(got) != len(ss) {
		t.Fatalf("empty tombstones filtered=%#v", got)
	}
	if got := filterTombstonedSet(ss, map[string]bool{"a:one": true}); len(got) != 1 || got[0].ID != "two" {
		t.Fatalf("filtered=%#v", got)
	}
}

func TestTombstonePersistenceAndForgetNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	if err := writeTombstones(map[string]bool{"z:last": true, "a:first": true}); err != nil {
		t.Fatal(err)
	}
	if got := Tombstones(); len(got) != 2 || got[0] != "a:first" || got[1] != "z:last" {
		t.Fatalf("tombstones=%v", got)
	}
	if err := Unforget("first"); err != nil {
		t.Fatal(err)
	}
	if got := Tombstones(); len(got) != 1 || got[0] != "z:last" {
		t.Fatalf("after suffix unforget=%v", got)
	}
	if err := Unforget("z:"); err != nil {
		t.Fatal(err)
	}
	if len(Tombstones()) != 0 {
		t.Fatal("prefix unforget left tombstones")
	}

	dir := filepath.Join(home, "index")
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	result, err := Forget(dir, ForgetOptions{Project: "missing"})
	if err != nil || result != (ForgetResult{}) {
		t.Fatalf("no-op forget=%#v err=%v", result, err)
	}
}

func TestRedactionRuleCarryAndReport(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := Manifest{
		Redacted:       4,
		RedactionRules: map[string]int{"claude:aws-secret": 2, "opencode:bearer-token": 2, "malformed": 9},
		Files:          map[string]FileState{},
	}
	m := Manifest{Files: map[string]FileState{}}
	carryRedactions(&m, old, map[string]bool{"claude-source": true})
	if m.RedactionRules["claude:aws-secret"] != 2 || m.RedactionRules["opencode:bearer-token"] != 2 {
		t.Fatalf("carried rules=%v", m.RedactionRules)
	}
	m = Manifest{Files: map[string]FileState{}}
	carryRedactions(&m, old, map[string]bool{sources.OpencodeDB(): true})
	if _, ok := m.RedactionRules["opencode:bearer-token"]; ok {
		t.Fatalf("skipped opencode rule carried: %v", m.RedactionRules)
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Redacted: 3, RedactionRules: map[string]int{"claude:credential": 3}, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	stats, err := Redactions(dir)
	if err != nil || stats.Total != 3 || stats.Rules["claude"]["credential"] != 3 {
		t.Fatalf("redaction report=%#v err=%v", stats, err)
	}
}

func TestReadRecordsForForgetMissingIndex(t *testing.T) {
	if got := readRecordsForForget(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Fatalf("missing records=%v", got)
	}
}

func TestRebuildHonorsTombstonedLoadedSession(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "session.jsonl")
	write(t, path, claudeLine("dead-session", "2026-01-02T03:04:05Z", "tombstone needle"))
	dir := filepath.Join(tmp, "index")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil || len(m.Sessions) != 1 {
		t.Fatalf("manifest sessions=%d err=%v", len(m.Sessions), err)
	}
	for key := range m.Sessions {
		if err := rebuildWithTombstones(dir, "", "", currentFiles(""), nil, map[string]bool{key: true}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := Search(dir, search.Options{Query: "tombstone", All: true}); err != nil || len(got) != 0 {
		t.Fatalf("tombstoned search=%#v err=%v", got, err)
	}
}

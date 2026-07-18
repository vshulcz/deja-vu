package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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
	r, err = Forget(dir, ForgetOptions{Project: "SECRET"})
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
	if got := filterTombstonedSet(ss, map[string]bool{"a:one": true}); len(got) != 1 || got[0].ID != "two" {
		t.Fatalf("filtered=%#v", got)
	}
}

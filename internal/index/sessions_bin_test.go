package index

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func assertSameTimeZoneAndInstant(t *testing.T, field string, want, got time.Time) {
	t.Helper()
	if want.IsZero() != got.IsZero() {
		t.Fatalf("%s zero mismatch: want=%v got=%v", field, want, got)
	}
	if !want.Equal(got) {
		t.Fatalf("%s instant mismatch: want=%v got=%v", field, want, got)
	}
	_, wantOff := want.Zone()
	_, gotOff := got.Zone()
	if wantOff != gotOff {
		t.Fatalf("%s tz offset mismatch: want=%d got=%d", field, wantOff, gotOff)
	}
}

func assertSessionMetaEqual(t *testing.T, key string, want, got SessionMeta) {
	t.Helper()
	assertSameTimeZoneAndInstant(t, key+".Started", want.Started, got.Started)
	assertSameTimeZoneAndInstant(t, key+".Updated", want.Updated, got.Updated)
	assertSameTimeZoneAndInstant(t, key+".SharedStarted", want.SharedStarted, got.SharedStarted)
	assertSameTimeZoneAndInstant(t, key+".SharedUpdated", want.SharedUpdated, got.SharedUpdated)

	wantCopy, gotCopy := want, got
	wantCopy.Started, gotCopy.Started = time.Time{}, time.Time{}
	wantCopy.Updated, gotCopy.Updated = time.Time{}, time.Time{}
	wantCopy.SharedStarted, gotCopy.SharedStarted = time.Time{}, time.Time{}
	wantCopy.SharedUpdated, gotCopy.SharedUpdated = time.Time{}, time.Time{}

	if !reflect.DeepEqual(wantCopy, gotCopy) {
		t.Fatalf("%s mismatch:\nwant=%#v\ngot =%#v", key, wantCopy, gotCopy)
	}
}

func TestSessionsBinRoundTripAllFields(t *testing.T) {
	if got := reflect.TypeFor[SessionMeta]().NumField(); got != 41 {
		t.Fatalf("SessionMeta has %d fields; update binary catalog codec and this test", got)
	}

	tzPlus3 := time.FixedZone("UTC+3", 3*3600)
	tzMinus5 := time.FixedZone("UTC-5", -5*3600)

	want := map[string]SessionMeta{
		"claude:full": {
			ID: "full", Harness: "claude", Project: "alpha-repo", Path: "/Users/alex/src/app/session.jsonl",
			Title:   "Binary catalog indexing",
			Started: time.Date(2026, 6, 15, 10, 20, 30, 123456789, tzPlus3),
			Updated: time.Date(2026, 6, 15, 11, 45, 59, 987654321, tzMinus5),
			Ord:     42, Asked: []uint64{10, 200, 3000}, Touched: []string{"main.go", "pkg/store.go"}, Settled: "pkg/store.go",
			RanCommand: true, Shared: true, AgentTitle: true,
			SharedStarted: time.Date(2026, 6, 15, 10, 21, 0, 555, tzPlus3),
			SharedUpdated: time.Date(2026, 6, 15, 11, 40, 0, 777, time.UTC),
			Copies:        []string{"claude:copy-1", "codex:copy-2"}, OrigID: "orig-77", RemoteID: "remote-88",
			Kind: "claude-mem", Parent: "claude:parent-1", Agent: "researcher",
			Model:     &model.Model{Provider: "anthropic", ID: "claude-sonnet-4"},
			Tokens:    &model.Tokens{Input: 1200, Output: 350, Reasoning: 90, CacheRead: 800},
			ToolCalls: map[string]int{"bash": 3, "read_file": 7}, ToolErrors: 2, Opening: 19, From: "handoff:prior-1",
			Lifecycle: "active", LifecycleNote: "ready for review", LifecycleAt: "2026-06-15T11:45:00Z",
			GaveUp: true, Words: 9182, NoText: true, Counted: 11, LastMsg: 999,
			TouchHits: []int{1, 0, 3}, Hit: []uint64{5, 8, 13},
			Rules: []string{"never ship globals"}, Revokes: []string{"old rule"}, Planted: []string{"seed decision"},
		},
		"codex:empty-containers": {
			ID: "empty-containers", Harness: "codex", Project: "beta", Path: "/Users/alex/src/beta/rollout.jsonl",
			Started: time.Unix(1700000000, 0).UTC(), Updated: time.Unix(1700000100, 0).UTC(),
			Asked: []uint64{}, Touched: []string{}, Copies: []string{},
			Model: &model.Model{}, Tokens: &model.Tokens{}, ToolCalls: map[string]int{},
			TouchHits: []int{}, Hit: []uint64{}, Rules: []string{}, Revokes: []string{}, Planted: []string{},
		},
		"opencode:zero": {},
	}
	for i := range uint32(10) {
		k := fmt.Sprintf("extra:k%02d", i)
		want[k] = SessionMeta{ID: fmt.Sprintf("k%02d", i), Harness: "claude", Ord: i + 1}
	}

	b1, err := encodeSessionsBin(want)
	if err != nil {
		t.Fatalf("encodeSessionsBin 1: %v", err)
	}
	b2, err := encodeSessionsBin(want)
	if err != nil {
		t.Fatalf("encodeSessionsBin 2: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatal("encodeSessionsBin output is not deterministic across identical maps")
	}

	dir := t.TempDir()
	binPath := filepath.Join(dir, "sessions.bin")
	fi, err := writeSessionsBinAtomicStat(binPath, want)
	if err != nil {
		t.Fatalf("writeSessionsBinAtomicStat: %v", err)
	}
	if fi == nil || fi.Size() == 0 {
		t.Fatalf("unexpected FileInfo from writeSessionsBinAtomicStat: %v", fi)
	}

	got, err := readSessionsBin(dir)
	if err != nil {
		t.Fatalf("readSessionsBin: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded count = %d, want %d", len(got), len(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Fatalf("missing key %q after round-trip", k)
		}
		assertSessionMetaEqual(t, k, w, g)
	}
}

func TestSessionsBinCountAndKeyWithoutFullDecode(t *testing.T) {
	dir := t.TempDir()
	sessions := make(map[string]SessionMeta, 50)
	for i := range uint32(50) {
		id := fmt.Sprintf("s-%03d", i)
		sessions["claude:"+id] = SessionMeta{
			ID:      id,
			Harness: "claude",
			Title:   "title-" + id,
			Ord:     i + 1,
			Updated: time.Unix(1750000000+int64(i), 0).UTC(),
		}
	}

	m := Manifest{
		Version:  version,
		Format:   onDiskFormat,
		Sessions: sessions,
		Files:    map[string]FileState{},
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.bin")); err != nil {
		t.Fatalf("expected sessions.bin after writeManifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.gob")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("sessions.gob should not exist on clean sessions.bin write, got %v", err)
	}

	invalidateManifestCache(dir)
	beforeDecodes := manifestDecodes.Load()

	cnt, err := SessionCount(dir)
	if err != nil {
		t.Fatalf("SessionCount: %v", err)
	}
	if cnt != 50 {
		t.Fatalf("SessionCount = %d, want 50", cnt)
	}
	if got := manifestDecodes.Load(); got != beforeDecodes {
		t.Fatalf("SessionCount triggered %d full manifest decode(s)", got-beforeDecodes)
	}

	hit, ok := MetaForKey(dir, "claude:s-027")
	if !ok {
		t.Fatal("MetaForKey hit returned ok=false")
	}
	assertSessionMetaEqual(t, "claude:s-027", sessions["claude:s-027"], hit)

	if _, missOK := MetaForKey(dir, "claude:missing"); missOK {
		t.Fatal("MetaForKey miss unexpectedly returned ok=true")
	}
	if got := manifestDecodes.Load(); got != beforeDecodes {
		t.Fatalf("MetaForKey triggered %d full manifest decode(s)", got-beforeDecodes)
	}

	// Removing manifest.gob must make both helpers treat the store as missing.
	if err := os.Remove(filepath.Join(dir, "manifest.gob")); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir)
	if _, err := SessionCount(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("SessionCount without manifest.gob = %v, want fs.ErrNotExist", err)
	}
	if _, ok := MetaForKey(dir, "claude:s-027"); ok {
		t.Fatal("MetaForKey without manifest.gob returned ok=true, want false")
	}
}

func TestSessionsBinCorruption(t *testing.T) {
	sessions := map[string]SessionMeta{
		"claude:s1": {ID: "s1", Harness: "claude", Title: "one", Ord: 1},
		"codex:s2":  {ID: "s2", Harness: "codex", Title: "two", Ord: 2},
	}
	validBytes, err := encodeSessionsBin(sessions)
	if err != nil {
		t.Fatal(err)
	}
	core := manifestCore{Version: version, Format: onDiskFormat}

	cases := []struct {
		name        string
		mutate      func(b []byte) []byte
		checkHeader bool
	}{
		{"truncated header", func(b []byte) []byte { return bytes.Clone(b[:16]) }, true},
		{"bad magic", func(b []byte) []byte { out := bytes.Clone(b); copy(out[:8], "DJSESS00"); return out }, true},
		{"unsupported version", func(b []byte) []byte { out := bytes.Clone(b); out[8] = 2; return out }, true},
		{"non-zero flags", func(b []byte) []byte { out := bytes.Clone(b); out[24] = 1; return out }, true},
		{"dirOffset out of bounds", func(b []byte) []byte { out := bytes.Clone(b); out[16], out[17], out[18] = 0xfe, 0, 0; return out }, true},
		{"dirSize mismatch with count", func(b []byte) []byte { out := bytes.Clone(b); out[12] = 99; return out }, true},
		{"payload bit flip fails checksum", func(b []byte) []byte { out := bytes.Clone(b); out[40] ^= 0x01; return out }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := writeGobAtomicStat(filepath.Join(dir, "manifest.gob"), core); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "sessions.bin"), tc.mutate(validBytes), 0o600); err != nil {
				t.Fatal(err)
			}
			invalidateManifestCache(dir)

			if _, err := readSessionsBin(dir); !errors.Is(err, errCorruptIndex) {
				t.Fatalf("readSessionsBin = %v, want errCorruptIndex", err)
			}
			if _, err := readManifest(dir); !errors.Is(err, errCorruptIndex) {
				t.Fatalf("readManifest = %v, want errCorruptIndex", err)
			}
			if tc.checkHeader {
				if _, err := readSessionsBinCount(dir); !errors.Is(err, errCorruptIndex) {
					t.Fatalf("readSessionsBinCount = %v, want errCorruptIndex", err)
				}
				if _, err := SessionCount(dir); !errors.Is(err, errCorruptIndex) {
					t.Fatalf("SessionCount = %v, want errCorruptIndex", err)
				}
			}
		})
	}
}

func TestSessionsGobLegacyFallbackAndUpgrade(t *testing.T) {
	dir := t.TempDir()
	sessions := map[string]SessionMeta{
		"claude:legacy-1": {ID: "legacy-1", Harness: "claude", Title: "from gob", Ord: 3},
	}
	core := manifestCore{Version: version, Format: onDiskFormat}
	if _, err := writeGobAtomicStat(filepath.Join(dir, "sessions.gob"), sessions); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGobAtomicStat(filepath.Join(dir, "manifest.gob"), core); err != nil {
		t.Fatal(err)
	}

	if !HasManifest(dir) {
		t.Fatal("HasManifest returned false for legacy sessions.gob store")
	}
	if n, err := SessionCount(dir); err != nil || n != 1 {
		t.Fatalf("SessionCount legacy = (%d, %v), want (1, nil)", n, err)
	}
	if meta, ok := MetaForKey(dir, "claude:legacy-1"); !ok || meta.Title != "from gob" {
		t.Fatalf("MetaForKey legacy = (%+v, %v), want title 'from gob'", meta, ok)
	}
	if m, err := readManifestCached(dir); err != nil || m.Sessions["claude:legacy-1"].Title != "from gob" {
		t.Fatalf("readManifestCached legacy = (%v, %v)", len(m.Sessions), err)
	}

	// Compaction-only check must see a catalog regardless of .gob vs .bin.
	compactDir := t.TempDir()
	if err := writeGobAtomic(compactionSidecarPath(compactDir), map[string]CompactionState{
		"claude:c1": {SessionID: "c1", Harness: "claude"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := compactionOnlyManifest(compactDir); !ok {
		t.Fatal("compactionOnlyManifest = false when no catalog exists")
	}
	if _, err := writeSessionsBinAtomicStat(filepath.Join(compactDir, "sessions.bin"), sessions); err != nil {
		t.Fatal(err)
	}
	if _, ok := compactionOnlyManifest(compactDir); ok {
		t.Fatal("compactionOnlyManifest = true when sessions.bin exists")
	}

	// Writing the manifest upgrades legacy stores to sessions.bin and drops sessions.gob.
	if err := writeManifest(dir, Manifest{
		Version:  version,
		Format:   onDiskFormat,
		Sessions: sessions,
		Files:    map[string]FileState{},
	}); err != nil {
		t.Fatalf("writeManifest upgrade: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.bin")); err != nil {
		t.Fatalf("sessions.bin missing after upgrade: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.gob")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("legacy sessions.gob still present after upgrade: %v", err)
	}
}

func BenchmarkReadSessionsBinVsGob(b *testing.B) {
	sessions := make(map[string]SessionMeta, 1000)
	base := time.Unix(1750000000, 0).UTC()
	for i := range uint32(1000) {
		id := fmt.Sprintf("sess-%04d", i)
		sessions["claude:"+id] = SessionMeta{
			ID:        id,
			Harness:   "claude",
			Project:   "repo-bench",
			Path:      "/Users/alex/work/repo/" + id + ".jsonl",
			Title:     fmt.Sprintf("Investigate indexing throughput #%d", i),
			Started:   base,
			Updated:   base.Add(time.Duration(i) * time.Second),
			Ord:       i + 1,
			Asked:     []uint64{10, 20, 30},
			Touched:   []string{"a.go", "b.go"},
			Model:     &model.Model{Provider: "anthropic", ID: "claude-sonnet-4"},
			Tokens:    &model.Tokens{Input: 1000, Output: 250},
			ToolCalls: map[string]int{"bash": 2, "read": 5},
			Words:     320,
		}
	}

	dir := b.TempDir()
	core := manifestCore{Version: version, Format: onDiskFormat}
	if _, err := writeGobAtomicStat(filepath.Join(dir, "manifest.gob"), core); err != nil {
		b.Fatal(err)
	}
	if _, err := writeGobAtomicStat(filepath.Join(dir, "sessions.gob"), sessions); err != nil {
		b.Fatal(err)
	}
	if _, err := writeSessionsBinAtomicStat(filepath.Join(dir, "sessions.bin"), sessions); err != nil {
		b.Fatal(err)
	}
	targetKey := "claude:sess-0512"

	b.Run("gob-1000", func(b *testing.B) {
		p := filepath.Join(dir, "sessions.gob")
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			var dst map[string]SessionMeta
			if err := readGob(p, &dst); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("bin-full-1000", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, err := readSessionsBin(dir); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("bin-count-1000", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, err := readSessionsBinCount(dir); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("bin-key-1000", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, ok, err := readSessionsBinKey(dir, targetKey); err != nil || !ok {
				b.Fatalf("ok=%v err=%v", ok, err)
			}
		}
	})

	_ = maps.Clone(sessions)
}

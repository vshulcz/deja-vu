package index

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"weak"

	"github.com/vshulcz/deja-vu/internal/model"
)

func claudePairLines(id, cwd, ts1, ts2, errText, cmd string) string {
	return `{"type":"user","sessionId":"` + id + `","timestamp":"` + ts1 + `","cwd":"` + cwd + `","message":{"role":"user","content":[{"type":"tool_result","content":"` + errText + `"}]}}` + "\n" +
		`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + ts2 + `","cwd":"` + cwd + `","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}]}}` + "\n"
}

func normalizeFixPairsUTC(ps []FixPair) []FixPair {
	out := slices.Clone(ps)
	for i := range out {
		out[i].When = out[i].When.UTC()
	}
	return out
}

func sortRawFixPairs(ps []FixPair) []FixPair {
	out := normalizeFixPairsUTC(ps)
	slices.SortFunc(out, func(a, b FixPair) int {
		if c := cmp.Compare(a.Key, b.Key); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Error, b.Error); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Command, b.Command); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Edit, b.Edit); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		return a.When.Compare(b.When)
	})
	return out
}

func TestRemineFixesStreamsInterleavedAppendsForeignCopiesAndDeadRecords(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	projA := filepath.Join(claudeRoot, "-Users-alex-app")
	projB := filepath.Join(claudeRoot, "-Users-alex-infra")

	filler := strings.Repeat("context ", 400)
	userLine := func(id, cwd, ts, text string) string {
		return `{"type":"user","sessionId":"` + id + `","timestamp":"` + ts + `","cwd":"` + cwd + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}

	s1Path := filepath.Join(projA, "s1.jsonl")
	s2Path := filepath.Join(projA, "s2.jsonl")
	s3Path := filepath.Join(projA, "s3.jsonl")
	dupAPath := filepath.Join(projA, "dup.jsonl")
	dupBPath := filepath.Join(projB, "dup.jsonl")

	write(t, s1Path, userLine("s1", "/Users/alex/app", "2026-01-02T03:00:00Z", filler)+
		claudePairLines("s1", "/Users/alex/app", "2026-01-02T03:01:00Z", "2026-01-02T03:01:05Z", "undefined: alphaSymbol in pkg/a.go", "go build ./pkg/a"))
	write(t, s2Path, userLine("s2", "/Users/alex/app", "2026-01-02T03:00:00Z", filler)+
		claudePairLines("s2", "/Users/alex/app", "2026-01-02T03:02:00Z", "2026-01-02T03:02:05Z", "undefined: betaSymbol in pkg/b.go", "go build ./pkg/b"))
	write(t, s3Path, claudePairLines("s3", "/Users/alex/app", "2026-01-02T03:03:00Z", "2026-01-02T03:03:05Z", "undefined: staleSymbol in old/pkg.go", "make stale"))
	write(t, dupAPath, claudePairLines("dup", "/Users/alex/app", "2026-01-02T03:04:00Z", "2026-01-02T03:04:05Z", "undefined: ownConfig in cmd/app/main.go", "go build ./cmd/app"))
	write(t, dupBPath, claudePairLines("dup", "/Users/alex/infra", "2026-01-02T03:05:00Z", "2026-01-02T03:05:05Z", "undefined: foreignSecret in infra/tf.go", "terraform init"))
	indexPass(t, dir)

	// Interleave appends across s1 and s2 across two more incremental passes, and
	// rewrite s3 to introduce dead records while staying below compaction threshold.
	appendTo(t, s1Path, claudePairLines("s1", "/Users/alex/app", "2026-01-02T03:06:00Z", "2026-01-02T03:06:05Z", "undefined: gammaSymbol in pkg/c.go", "go test ./pkg/c"))
	appendTo(t, s2Path, claudePairLines("s2", "/Users/alex/app", "2026-01-02T03:07:00Z", "2026-01-02T03:07:05Z", "undefined: alphaSymbol in pkg/a.go", "go build ./pkg/a"))
	indexPass(t, dir)

	appendTo(t, s1Path, claudePairLines("s1", "/Users/alex/app", "2026-01-02T03:08:00Z", "2026-01-02T03:08:05Z", "undefined: deltaSymbol in pkg/d.go", "go test ./pkg/d"))
	writeAt(t, s3Path, claudePairLines("s3", "/Users/alex/app", "2026-01-02T03:09:00Z", "2026-01-02T03:09:05Z", "undefined: freshSymbol in new/pkg.go", "make fresh"), time.Now().Add(2*time.Minute))
	indexPass(t, dir)

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Sessions["claude:dup"].Shared {
		t.Fatalf("expected claude:dup to be marked shared, got %+v", m.Sessions["claude:dup"])
	}
	if len(m.DeadRecords) == 0 {
		t.Fatalf("expected DeadRecords after rewriting s3, got %d", len(m.DeadRecords))
	}

	rebuildDir := filepath.Join(tmp, "rebuild")
	if err = Ensure(rebuildDir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	wantFixes := normalizeFixPairsUTC(ReadFixes(rebuildDir))
	wantRaw, ok := readFixRaw(rebuildDir)
	if !ok || len(wantFixes) == 0 {
		t.Fatalf("rebuild produced empty fix tables: fixes=%d ok=%v", len(wantFixes), ok)
	}
	wantRawSorted := sortRawFixPairs(wantRaw)

	assertParity := func(label string) {
		t.Helper()
		gotFixes := ReadFixes(dir)
		if len(gotFixes) == 0 {
			t.Fatalf("%s: ReadFixes returned empty slice", label)
		}
		if !reflect.DeepEqual(normalizeFixPairsUTC(gotFixes), wantFixes) {
			t.Fatalf("%s: ReadFixes mismatch\n got: %+v\nwant: %+v", label, gotFixes, wantFixes)
		}
		for _, p := range gotFixes {
			if strings.Contains(p.Error, "staleSymbol") {
				t.Fatalf("%s: dead staleSymbol leaked into ReadFixes: %+v", label, p)
			}
		}
		gotRaw, ok := readFixRaw(dir)
		if !ok {
			t.Fatalf("%s: readFixRaw returned ok=false", label)
		}
		if gotRawSorted := sortRawFixPairs(gotRaw); !reflect.DeepEqual(gotRawSorted, wantRawSorted) {
			t.Fatalf("%s: sorted readFixRaw mismatch\n got: %+v\nwant: %+v", label, gotRawSorted, wantRawSorted)
		}
	}

	// Case A: recoffsets.bin present.
	_ = os.Remove(fixesPath(dir))
	_ = os.Remove(fixRawPath(dir))
	invalidateFixesCache()
	remineFixes(dir)
	assertParity("with recoffsets.bin")

	// Case B: recoffsets.bin absent, forcing scanRecordOffsets across the log.
	_ = os.Remove(fixesPath(dir))
	_ = os.Remove(fixRawPath(dir))
	_ = os.Remove(filepath.Join(dir, recordOffsetsFile))
	invalidateFixesCache()
	remineFixes(dir)
	assertParity("without recoffsets.bin")

	// Case C: mid-log corrupt record preserves existing fixes.gob.
	before := ReadFixes(dir)
	if len(before) == 0 {
		t.Fatal("expected non-empty fixes before corruption test")
	}
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	off := 4
	for range 3 {
		_, k := binary.Uvarint(b[off:])
		if k <= 0 {
			t.Fatalf("bad uvarint at off=%d", off)
		}
		off += k
	}
	b[off] = 0xFF
	if err := os.WriteFile(rp, b, 0o600); err != nil {
		t.Fatal(err)
	}
	remineFixes(dir)
	invalidateFixesCache()
	if after := ReadFixes(dir); !reflect.DeepEqual(after, before) {
		t.Fatalf("corrupt remine modified existing fixes.gob\n got: %+v\nwant: %+v", after, before)
	}
}

func TestRemineFixesReleasesSessionMessagesBetweenSessions(t *testing.T) {
	origWorkers := rerankWorkers
	rerankWorkers = func() int { return 1 }
	defer func() { rerankWorkers = origWorkers }()
	defer func() { remineSessionHook = nil }()

	const totalSessions = 24
	base := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	ss := make([]model.Session, 0, totalSessions)
	pad := strings.Repeat("x", 32<<10)
	for i := range totalSessions {
		t0 := base.Add(time.Duration(i) * time.Minute)
		ss = append(ss, model.Session{
			ID:      fmt.Sprintf("s%02d", i),
			Harness: "claude",
			Project: "app",
			Updated: t0,
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("ctx %d a %s", i, pad), Time: t0},
				{Role: "user", Text: fmt.Sprintf("ctx %d b %s", i, pad), Time: t0.Add(time.Second)},
				{Role: "user", Text: fmt.Sprintf("ctx %d c %s", i, pad), Time: t0.Add(2 * time.Second)},
				{Role: "user", Text: fmt.Sprintf("ctx %d d %s", i, pad), Time: t0.Add(3 * time.Second)},
				{
					Role: "tool-output",
					Text: "zsh:1: command not found: bigtool" + strconv.Itoa(i),
					Time: t0.Add(4 * time.Second),
				},
				{
					Role: "command",
					Text: "brew install bigtool" + strconv.Itoa(i),
					Time: t0.Add(5 * time.Second),
				},
				{
					Role: "tool-output",
					Text: "ok",
					Time: t0.Add(6 * time.Second),
				},
			},
		})
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(fixesPath(dir))
	_ = os.Remove(fixRawPath(dir))
	invalidateFixesCache()

	var (
		ptrs               []weak.Pointer[model.Message]
		seen               int
		reclaimedDuringRun int
	)
	remineSessionHook = func(msgs []model.Message) {
		seen++
		if len(msgs) > 0 {
			ptrs = append(ptrs, weak.Make(&msgs[0]))
		}
		if seen == totalSessions {
			runtime.GC()
			runtime.GC()
			for _, p := range ptrs[:len(ptrs)-1] {
				if p.Value() == nil {
					reclaimedDuringRun++
				}
			}
		}
	}

	remineFixes(dir)

	if seen != totalSessions {
		t.Fatalf("seen=%d, want %d", seen, totalSessions)
	}
	if reclaimedDuringRun < totalSessions-4 {
		t.Fatalf("reclaimedDuringRun=%d/%d, want >= %d", reclaimedDuringRun, totalSessions-1, totalSessions-4)
	}
	if got := ReadFixes(dir); len(got) != totalSessions {
		t.Fatalf("got %d mined pairs, want %d", len(got), totalSessions)
	}
}

func TestRemineFixesDefensiveBranches(t *testing.T) {
	// Missing manifest.json returns cleanly without writing fix tables.
	missingDir := t.TempDir()
	remineFixes(missingDir)
	if len(ReadFixes(missingDir)) != 0 {
		t.Fatalf("unexpected fixes in empty dir")
	}

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	makeSession := func(id string) model.Session {
		return model.Session{
			ID:      id,
			Harness: "claude",
			Project: "app",
			Updated: now,
			Messages: []model.Message{
				{Role: "tool-output", Text: "zsh:1: command not found: edgebin" + id, Time: now},
				{Role: "command", Text: "brew install edgebin" + id, Time: now.Add(time.Second)},
				{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Second)},
			},
		}
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, []model.Session{makeSession("s1"), makeSession("s2")}, nil, ""); err != nil {
		t.Fatal(err)
	}

	// Orphan record key removed from m.Sessions is skipped cleanly.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	delete(m.Sessions, "claude:s2")
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(fixesPath(dir))
	_ = os.Remove(fixRawPath(dir))
	invalidateFixesCache()
	remineFixes(dir)
	got := ReadFixes(dir)
	if len(got) != 1 || got[0].Key != "claude:s1" {
		t.Fatalf("expected only claude:s1 after deleting claude:s2 from manifest, got %+v", got)
	}

	// Oversized record header triggers scanRecordOffsets corruption error and preserves fixes.gob.
	_ = os.Remove(filepath.Join(dir, recordOffsetsFile))
	rp := filepath.Join(dir, "records.bin")
	origLog, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := slices.Clone(origLog)
	binary.LittleEndian.PutUint32(corrupt[:4], 0x7fffffff)
	if err = os.WriteFile(rp, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	remineFixes(dir)
	invalidateFixesCache()
	if after := ReadFixes(dir); !reflect.DeepEqual(after, got) {
		t.Fatalf("oversized log modified existing fixes.gob\n got: %+v\nwant: %+v", after, got)
	}

	// Missing records.bin preserves existing fixes.gob.
	if err = os.Remove(rp); err != nil {
		t.Fatal(err)
	}
	remineFixes(dir)
	invalidateFixesCache()
	if after := ReadFixes(dir); !reflect.DeepEqual(after, got) {
		t.Fatalf("missing records.bin modified existing fixes.gob\n got: %+v\nwant: %+v", after, got)
	}
}
